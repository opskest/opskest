package api

import (
	"net/http"
	"strings"
	"testing"
)

// A status page is a status page or a wallboard from the day it is created.
// The layout never converts: the update API refuses a change, the wallboard
// address is a 404 for every page that is not a board page, and the public
// payload stays free of anything board specific.
func TestPageLayoutIsFixed(t *testing.T) {
	env := newTestEnv(t)
	mon := env.createMonitor(httpMonitorIn("layout monitor", "https://example.com/layout-secret"))
	create := func(slug, layout string) int64 {
		t.Helper()
		r := env.do(http.MethodPost, "/api/pages", map[string]any{
			"slug": slug, "title": "T " + slug, "visibility": "public", "layout": layout,
			"items": []map[string]any{{"monitorId": mon.ID, "sort": 0}},
		}, "admin")
		if r.status != http.StatusCreated {
			t.Fatalf("create %s: %d %s", slug, r.status, r.body)
		}
		var out struct {
			ID int64 `json:"id"`
		}
		r.json(t, &out)
		return out.ID
	}
	listID := create("fixed-list", "list")
	boardID := create("fixed-board", "board")

	put := func(id int64, slug, layout string) *apiResponse {
		body := map[string]any{
			"slug": slug, "title": "Renamed", "visibility": "public",
			"items": []map[string]any{{"monitorId": mon.ID, "sort": 0}},
		}
		if layout != "" {
			body["layout"] = layout
		}
		return env.do(http.MethodPut, "/api/pages/"+itoa(id), body, "admin")
	}
	layoutOf := func(id int64) string {
		t.Helper()
		var p struct {
			Layout string `json:"layout"`
			Title  string `json:"title"`
		}
		env.do(http.MethodGet, "/api/pages/"+itoa(id), nil, "admin").json(t, &p)
		return p.Layout
	}

	// A change either way is a clear 400 and nothing is saved.
	for _, tc := range []struct {
		id         int64
		slug, want string
	}{{listID, "fixed-list", "board"}, {boardID, "fixed-board", "list"}} {
		r := put(tc.id, tc.slug, tc.want)
		if r.status != http.StatusBadRequest || !strings.Contains(string(r.body), "layout cannot be changed") {
			t.Fatalf("changing layout to %s: %d %s", tc.want, r.status, r.body)
		}
		if !strings.Contains(string(r.body), `"layout"`) {
			t.Errorf("the error names the layout field: %s", r.body)
		}
	}
	if layoutOf(listID) != "list" || layoutOf(boardID) != "board" {
		t.Fatal("a refused change must not be saved")
	}
	// An invalid value is refused too, and an unknown page is still a 404.
	if r := put(listID, "fixed-list", "grid"); r.status != http.StatusBadRequest {
		t.Fatalf("invalid layout: %d %s", r.status, r.body)
	}
	if r := put(987654, "nothing", "list"); r.status != http.StatusNotFound {
		t.Fatalf("unknown page: %d", r.status)
	}
	// Repeating the current layout, or leaving it out, saves everything else.
	for _, layout := range []string{"list", ""} {
		if r := put(listID, "fixed-list", layout); r.status != http.StatusOK {
			t.Fatalf("update with layout %q: %d %s", layout, r.status, r.body)
		}
	}
	if layoutOf(listID) != "list" {
		t.Fatal("layout survives an update that leaves it out")
	}

	// The wallboard exists only for a board page.
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/status/fixed-list/board", http.StatusNotFound},
		{"/status/no-such-page/board", http.StatusNotFound},
		{"/status/fixed-board/board", http.StatusOK},
		{"/status/fixed-list", http.StatusOK},
		{"/status/fixed-list/history", http.StatusOK},
		{"/status/fixed-board/history", http.StatusOK},
	} {
		if r := env.do(http.MethodGet, tc.path, nil, ""); r.status != tc.want {
			t.Errorf("GET %s = %d, want %d", tc.path, r.status, tc.want)
		}
	}
	// Search-engine rules are untouched: every wallboard address, 404 or not, is noindex;
	// the public list page and its history stay indexable.
	for _, path := range []string{"/status/fixed-list/board", "/status/fixed-board/board"} {
		if got := env.do(http.MethodGet, path, nil, "").header.Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
			t.Errorf("%s: X-Robots-Tag %q, want noindex", path, got)
		}
	}
	for _, path := range []string{"/status/fixed-list", "/status/fixed-list/history"} {
		if got := env.do(http.MethodGet, path, nil, "").header.Get("X-Robots-Tag"); got != "" {
			t.Errorf("%s must stay indexable, got %q", path, got)
		}
	}
	// The public page says which layout it is and still never leaks the target.
	for _, slug := range []string{"fixed-list", "fixed-board"} {
		r := env.do(http.MethodGet, "/api/public/pages/"+slug, nil, "")
		if r.status != http.StatusOK || strings.Contains(string(r.body), "layout-secret") {
			t.Fatalf("public page %s: %d (target must not appear)", slug, r.status)
		}
	}
}
