package api

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"opskest/internal/model"
)

// badgePageBody creates a public page holding the given monitors and
// returns its slug.
func badgePage(t *testing.T, env *testEnv, slug, visibility string, items ...map[string]any) {
	t.Helper()
	if r := env.do(http.MethodPost, "/api/pages", map[string]any{
		"slug": slug, "title": "Badge page", "visibility": visibility, "layout": "list", "items": items,
	}, "admin"); r.status != http.StatusCreated {
		t.Fatalf("create page: %d %s", r.status, r.body)
	}
}

func badgeItem(id int64, displayName string) map[string]any {
	return map[string]any{"monitorId": id, "sort": 0, "displayName": displayName}
}

func upMonitor(t *testing.T, env *testEnv, name string) MonitorOut {
	t.Helper()
	m := env.createMonitor(httpMonitorIn(name, "https://secret-host.internal/"+strings.ReplaceAll(name, " ", "-")))
	env.engine.Apply(m.ID, model.Result{At: time.Now(), OK: true, LatencyMs: 20})
	env.writer.Flush()
	return m
}

// assertWellFormedSVG fails unless body parses as XML with an <svg> root.
func assertWellFormedSVG(t *testing.T, body []byte) {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(body))
	first := true
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("badge is not well-formed XML: %v\n%s", err, body)
		}
		if se, ok := tok.(xml.StartElement); ok && first {
			first = false
			if se.Name.Local != "svg" {
				t.Fatalf("root element = %q, want svg", se.Name.Local)
			}
		}
	}
}

func assertBadgeHeaders(t *testing.T, r *apiResponse, status int) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status = %d, want %d (%s)", r.status, status, r.body)
	}
	if ct := r.header.Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
		t.Errorf("Content-Type = %q, want image/svg+xml", ct)
	}
	if cc := r.header.Get("Cache-Control"); cc != "public, max-age=60" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if r.header.Get("ETag") == "" {
		t.Errorf("missing ETag")
	}
}

func TestBadgeOverallStatus(t *testing.T) {
	env := newTestEnv(t)
	good := upMonitor(t, env, "good one")
	bad := downMonitor(t, env, "bad one", "https://secret-host.internal/x", "HTTP 503 from secret-host")

	// Only the healthy monitor: operational.
	badgePage(t, env, "ok", "public", badgeItem(good.ID, ""))
	r := env.do(http.MethodGet, "/api/public/pages/ok/badge.svg", nil, "")
	assertBadgeHeaders(t, r, http.StatusOK)
	assertWellFormedSVG(t, r.body)
	for _, want := range []string{"<title>status: operational</title>", `aria-label="status: operational"`, "#2e7d32"} {
		if !bytesContains(r.body, want) {
			t.Errorf("operational badge lacks %q: %s", want, r.body)
		}
	}

	// Half down: a major outage, same rule as the page's overall.
	badgePage(t, env, "mixed", "public", badgeItem(good.ID, ""), badgeItem(bad.ID, ""))
	r = env.do(http.MethodGet, "/api/public/pages/mixed/badge.svg", nil, "")
	assertBadgeHeaders(t, r, http.StatusOK)
	if !bytesContains(r.body, "<title>status: major outage</title>") {
		t.Errorf("mixed badge = %s, want major outage", r.body)
	}
}

func TestBadgeETagNotModified(t *testing.T) {
	env := newTestEnv(t)
	m := upMonitor(t, env, "etag one")
	badgePage(t, env, "etag", "public", badgeItem(m.ID, ""))

	first := env.do(http.MethodGet, "/api/public/pages/etag/badge.svg", nil, "")
	etag := first.header.Get("ETag")
	req, _ := http.NewRequest(http.MethodGet, env.srv.URL+"/api/public/pages/etag/badge.svg", nil)
	req.Header.Set("If-None-Match", etag)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusNotModified || len(body) != 0 {
		t.Errorf("conditional GET = %d with %d body bytes, want 304 and none", resp.StatusCode, len(body))
	}
}

func TestBadgeServiceStatus(t *testing.T) {
	env := newTestEnv(t)
	up := upMonitor(t, env, "api")
	slow := env.createMonitor(httpMonitorIn("slow", "https://secret-host.internal/slow"))
	env.engine.Apply(slow.ID, model.Result{At: time.Now(), OK: true, Degraded: true, LatencyMs: 900, Reason: "slow response from secret-host"})
	down := downMonitor(t, env, "db", "https://secret-host.internal/db", "HTTP 503 from secret-host")
	badgePage(t, env, "svc", "public", badgeItem(up.ID, ""), badgeItem(slow.ID, ""), badgeItem(down.ID, ""))

	cases := []struct {
		id   int64
		name string
		want string
	}{
		{up.ID, "api", "operational"},
		{slow.ID, "slow", "degraded"},
		{down.ID, "db", "outage"},
	}
	for _, c := range cases {
		r := env.do(http.MethodGet, "/api/public/pages/svc/services/"+itoa(c.id)+"/badge.svg", nil, "")
		assertBadgeHeaders(t, r, http.StatusOK)
		assertWellFormedSVG(t, r.body)
		if want := "<title>" + c.name + ": " + c.want + "</title>"; !bytesContains(r.body, want) {
			t.Errorf("service %d badge lacks %q: %s", c.id, want, r.body)
		}
		assertNoLeak(t, r.body)
		if bytesContains(r.body, "slow response") {
			t.Errorf("badge leaks a reason: %s", r.body)
		}
	}
}

func TestBadgeUptime(t *testing.T) {
	env := newTestEnv(t)
	up := upMonitor(t, env, "api")
	fresh := env.createMonitor(httpMonitorIn("fresh", "https://secret-host.internal/fresh"))
	badgePage(t, env, "up", "public", badgeItem(up.ID, ""), badgeItem(fresh.ID, ""))

	for _, w := range []string{"", "24h", "7d", "30d", "90d"} {
		path := "/api/public/pages/up/services/" + itoa(up.ID) + "/uptime.svg"
		if w != "" {
			path += "?window=" + w
		}
		r := env.do(http.MethodGet, path, nil, "")
		assertBadgeHeaders(t, r, http.StatusOK)
		assertWellFormedSVG(t, r.body)
		shown := w
		if shown == "" {
			shown = "30d"
		}
		if !bytesContains(r.body, "api uptime "+shown+": 100%") {
			t.Errorf("window %q badge = %s, want 100%% for %s", w, r.body, shown)
		}
		assertNoLeak(t, r.body)
	}

	// No checks yet: nothing to count, shown as n/a rather than 0% or 100%.
	r := env.do(http.MethodGet, "/api/public/pages/up/services/"+itoa(fresh.ID)+"/uptime.svg?window=7d", nil, "")
	assertBadgeHeaders(t, r, http.StatusOK)
	if !bytesContains(r.body, "fresh uptime 7d: n/a") {
		t.Errorf("fresh uptime badge = %s", r.body)
	}
}

func TestUptimeBadgeText(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	cases := []struct {
		in    *float64
		text  string
		color string
	}{
		{nil, "n/a", badgeGray},
		{f(100), "100%", badgeGreen},
		{f(99.996), "99.99%", badgeGreen}, // never rounds up to a perfect score
		{f(99.95), "99.95%", badgeGreen},
		{f(99.5), "99.5%", badgeAmber},
		{f(97), "97%", badgeOrange},
		{f(80.126), "80.12%", badgeRed},
		{f(-3), "0%", badgeRed},
		{f(140), "100%", badgeGreen},
	}
	for _, c := range cases {
		text, color := uptimeBadgeText(c.in)
		if text != c.text || color != c.color {
			t.Errorf("uptimeBadgeText(%v) = %q,%q want %q,%q", c.in, text, color, c.text, c.color)
		}
	}
}

// TestBadgeStatusVocabulary pins the text and colour for every status the
// engine can report, and that each renders to well-formed SVG.
func TestBadgeStatusVocabulary(t *testing.T) {
	overall := map[string]string{
		"operational": "operational", "degraded": "degraded", "partial": "partial outage",
		"major": "major outage", "maintenance": "maintenance", "bogus": "unknown",
	}
	for in, want := range overall {
		text, color := overallBadgeText(in)
		if text != want || color == "" {
			t.Errorf("overallBadgeText(%q) = %q,%q want %q", in, text, color, want)
		}
		assertWellFormedSVG(t, []byte(renderBadge("status", text, color)))
	}
	service := map[model.Status]string{
		model.StatusUp: "operational", model.StatusDegraded: "degraded", model.StatusPending: "degraded",
		model.StatusDown: "outage", model.StatusMaintenance: "maintenance",
		model.StatusPaused: "paused", model.StatusUnknown: "unknown",
	}
	for in, want := range service {
		if text, _ := serviceBadgeText(in); text != want {
			t.Errorf("serviceBadgeText(%v) = %q want %q", in, text, want)
		}
	}
}

func TestBadgeEscapesAndBoundsLabel(t *testing.T) {
	svg := renderBadge(`<script>alert(1)</script> & "x" 'y'`, `</svg><b>`, badgeRed)
	assertWellFormedSVG(t, []byte(svg))
	for _, bad := range []string{"<script>", "</svg><b>", "<b>"} {
		if strings.Contains(svg, bad) {
			t.Errorf("badge does not escape %q: %s", bad, svg)
		}
	}

	if got := badgeLabelText("  a\n\tb \x00 c‮ "); got != "a b c" {
		t.Errorf("badgeLabelText control chars = %q", got)
	}
	long := strings.Repeat("x", 500)
	if got := badgeLabelText(long); len([]rune(got)) != badgeMaxLabelLen || !strings.HasSuffix(got, "…") {
		t.Errorf("badgeLabelText long = %q (%d runes)", got, len([]rune(got)))
	}
	if got := badgeLabelText("  \n "); got != "service" {
		t.Errorf("badgeLabelText empty = %q", got)
	}
}

// TestBadgeDisplayNameEscapedEndToEnd: an admin-entered display name with
// markup and an enormous length reaches the badge escaped and bounded.
func TestBadgeDisplayNameEscapedEndToEnd(t *testing.T) {
	env := newTestEnv(t)
	m := upMonitor(t, env, "plain")
	badgePage(t, env, "name", "public", badgeItem(m.ID, `<img src=x onerror=alert(1)>`+strings.Repeat("z", 400)))
	r := env.do(http.MethodGet, "/api/public/pages/name/services/"+itoa(m.ID)+"/badge.svg", nil, "")
	assertBadgeHeaders(t, r, http.StatusOK)
	assertWellFormedSVG(t, r.body)
	if bytesContains(r.body, "<img") || bytesContains(r.body, "zzzzzzzzzzzzzzzzzzzzzzzzzzzz") || len(r.body) > 2000 {
		t.Errorf("badge not escaped/bounded (%d bytes): %s", len(r.body), r.body)
	}
}

func TestBadgeBadWindow(t *testing.T) {
	env := newTestEnv(t)
	m := upMonitor(t, env, "w")
	badgePage(t, env, "win", "public", badgeItem(m.ID, ""))
	base := "/api/public/pages/win/services/" + itoa(m.ID) + "/uptime.svg?window="
	for _, w := range []string{"1y", "30D", "../../etc", "<svg/onload=1>", strings.Repeat("9", 5000)} {
		r := env.do(http.MethodGet, base+strings.NewReplacer("<", "%3C", ">", "%3E").Replace(w), nil, "")
		if r.status != http.StatusBadRequest {
			t.Errorf("window %.12q: status %d, want 400", w, r.status)
		}
		if bytesContains(r.body, "svg/onload") || bytesContains(r.body, "99999") {
			t.Errorf("window reflected in response: %.200s", r.body)
		}
	}
}

// TestBadges404Identical: every kind of miss — unknown page, private page
// (even for a signed-in user), unknown/non-numeric service, service that
// exists but is not on this page — answers the same 404 image, byte for byte.
func TestBadges404Identical(t *testing.T) {
	env := newTestEnv(t)
	on := upMonitor(t, env, "on page")
	off := upMonitor(t, env, "off page")
	badgePage(t, env, "pub", "public", badgeItem(on.ID, ""))
	badgePage(t, env, "priv", "private", badgeItem(on.ID, ""))

	type miss struct{ path, as string }
	svc := func(slug string, id string, leaf string) string {
		return "/api/public/pages/" + slug + "/services/" + id + "/" + leaf
	}
	misses := []miss{
		{"/api/public/pages/nope/badge.svg", ""},
		{"/api/public/pages/priv/badge.svg", ""},
		{"/api/public/pages/priv/badge.svg", "viewer"},
		{"/api/public/pages/priv/badge.svg", "admin"},
		{svc("nope", itoa(on.ID), "badge.svg"), ""},
		{svc("priv", itoa(on.ID), "badge.svg"), "admin"},
		{svc("priv", itoa(on.ID), "uptime.svg"), ""},
		{svc("pub", "999999", "badge.svg"), ""},
		{svc("pub", "abc", "badge.svg"), ""},
		{svc("pub", itoa(off.ID), "badge.svg"), ""},
		{svc("pub", itoa(off.ID), "uptime.svg?window=30d"), ""},
		{svc("pub", "999999", "uptime.svg?window=bogus"), ""}, // a bad window never outranks a miss
	}
	var want []byte
	for _, m := range misses {
		r := env.do(http.MethodGet, m.path, nil, m.as)
		if r.status != http.StatusNotFound {
			t.Errorf("%s as %q: status %d, want 404", m.path, m.as, r.status)
			continue
		}
		if ct := r.header.Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
			t.Errorf("%s: 404 Content-Type = %q", m.path, ct)
		}
		assertWellFormedSVG(t, r.body)
		if want == nil {
			want = r.body
		} else if !bytes.Equal(want, r.body) {
			t.Errorf("%s as %q: 404 body differs from the others", m.path, m.as)
		}
		if bytesContains(r.body, "on page") || bytesContains(r.body, "off page") {
			t.Errorf("404 body names a monitor: %s", r.body)
		}
	}
}

func TestBadgeRateLimited(t *testing.T) {
	env := newTestEnv(t)
	m := upMonitor(t, env, "rl")
	badgePage(t, env, "brl", "public", badgeItem(m.ID, ""))
	for i := 0; i < 200; i++ {
		if env.do(http.MethodGet, "/api/public/pages/brl/badge.svg", nil, "").status == http.StatusTooManyRequests {
			return
		}
	}
	t.Fatalf("200 badge requests from one IP never hit the rate limit")
}

// TestBadgeNeverLeaksFixtureSecrets runs the badges against the shared
// public fixture (down monitor with a sensitive target, reason and runbook).
func TestBadgeNeverLeaksFixtureSecrets(t *testing.T) {
	f := newPublicFixture(t, "badgeleak", true, "public")
	for _, p := range badgePaths("badgeleak", f.monID) {
		r := f.env.do(http.MethodGet, p, nil, "")
		assertBadgeHeaders(t, r, http.StatusOK)
		assertNoLeak(t, r.body)
		for _, needle := range []string{"secret-runbook", "secret-host", "internal:", "wedged", "Fixture"} {
			if bytesContains(r.body, needle) {
				t.Errorf("%s leaks %q: %s", p, needle, r.body)
			}
		}
	}
}

// badgePaths lists every badge endpoint for a page and one of its services;
// TestPublicEndpointsNeverLeak walks the same list.
func badgePaths(slug string, monID int64) []string {
	base := "/api/public/pages/" + slug
	return []string{
		base + "/badge.svg",
		base + "/services/" + itoa(monID) + "/badge.svg",
		base + "/services/" + itoa(monID) + "/uptime.svg?window=30d",
	}
}
