package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"opskest/internal/hub"
	"opskest/internal/model"
)

// maintFixture: a page with one monitor, an in-progress and an upcoming
// maintenance window with recognisable titles, and an incident whose impact
// is "maintenance".
func maintFixture(t *testing.T, env *testEnv, slug string, settings map[string]any) {
	t.Helper()
	mon := env.createMonitor(httpMonitorIn("svc "+slug, "https://secret-host.internal/"+slug))
	now := time.Now()
	for _, w := range []struct {
		title      string
		start, end time.Duration
	}{{"ACTIVEWINDOW-" + slug, -time.Hour, time.Hour}, {"UPCOMINGWINDOW-" + slug, 2 * time.Hour, 3 * time.Hour}} {
		if r := env.do(http.MethodPost, "/api/maintenance", map[string]any{
			"title": w.title, "kind": "once", "startsAt": now.Add(w.start).UnixMilli(), "endsAt": now.Add(w.end).UnixMilli(),
			"active": true, "tz": "UTC", "monitorIds": []int64{mon.ID},
		}, "admin"); r.status != http.StatusCreated {
			t.Fatalf("create maintenance: %d %s", r.status, r.body)
		}
	}
	body := map[string]any{
		"slug": slug, "title": "Maint page", "visibility": "public", "layout": "list",
		"items": []map[string]any{{"monitorId": mon.ID, "sort": 0}},
	}
	if settings != nil {
		body["settings"] = settings
	}
	page := env.do(http.MethodPost, "/api/pages", body, "admin")
	if page.status != http.StatusCreated {
		t.Fatalf("create page: %d %s", page.status, page.body)
	}
	var p struct {
		ID int64 `json:"id"`
	}
	page.json(t, &p)
	if r := env.do(http.MethodPost, "/api/incidents", map[string]any{
		"title": "MAINTINCIDENT-" + slug, "impact": "maintenance", "body": "planned work",
		"pageIds": []int64{p.ID}, "monitorIds": []int64{mon.ID},
	}, "admin"); r.status != http.StatusCreated {
		t.Fatalf("create incident: %d %s", r.status, r.body)
	}
}

func publicPaths(slug string) []string {
	base := "/api/public/pages/" + slug
	return []string{base, base + "/incidents", base + "/feed.atom", base + "/feed.rss", base + "/summary.json", base + "/badge.svg"}
}

// A page saved before the option existed, or created without it, shows
// maintenance: the default is on.
func TestPublicMaintenanceShownByDefault(t *testing.T) {
	env := newTestEnv(t)
	maintFixture(t, env, "maintdef", nil)
	r := env.do(http.MethodGet, "/api/public/pages/maintdef", nil, "")
	if r.status != http.StatusOK {
		t.Fatalf("status %d", r.status)
	}
	var body struct {
		Page struct {
			Settings map[string]any `json:"settings"`
		} `json:"page"`
		Maintenance *struct {
			Active   []struct{ Title string } `json:"active"`
			Upcoming []struct{ Title string } `json:"upcoming"`
		} `json:"maintenance"`
	}
	r.json(t, &body)
	if body.Page.Settings["showMaintenance"] != true {
		t.Errorf("showMaintenance = %v, want true by default", body.Page.Settings["showMaintenance"])
	}
	if body.Maintenance == nil || len(body.Maintenance.Active) != 1 || len(body.Maintenance.Upcoming) != 1 {
		t.Fatalf("maintenance = %+v, want one active and one upcoming window", body.Maintenance)
	}
	sum := env.do(http.MethodGet, "/api/public/pages/maintdef/summary.json", nil, "")
	if !strings.Contains(string(sum.body), "UPCOMINGWINDOW-maintdef") {
		t.Errorf("summary.json lacks the scheduled window: %s", sum.body)
	}
	inc := env.do(http.MethodGet, "/api/public/pages/maintdef/incidents", nil, "")
	if !strings.Contains(string(inc.body), "MAINTINCIDENT-maintdef") {
		t.Errorf("incident list lacks the maintenance incident: %s", inc.body)
	}
}

// With the switch off, no public endpoint of the page carries maintenance
// data, and nothing sensitive leaks either.
func TestPublicMaintenanceHidden(t *testing.T) {
	env := newTestEnv(t)
	maintFixture(t, env, "maintoff", map[string]any{"showMaintenance": false})

	for _, p := range publicPaths("maintoff") {
		r := env.do(http.MethodGet, p, nil, "")
		if r.status != http.StatusOK {
			t.Fatalf("%s: status %d", p, r.status)
		}
		assertNoLeak(t, r.body)
		for _, needle := range []string{"ACTIVEWINDOW", "UPCOMINGWINDOW", "MAINTINCIDENT", "planned work", "secret-host"} {
			if bytesContains(r.body, needle) {
				t.Errorf("%s still shows %q: %s", p, needle, r.body)
			}
		}
	}
	page := env.do(http.MethodGet, "/api/public/pages/maintoff", nil, "")
	if bytesContains(page.body, `"maintenance":`) {
		t.Errorf("the page JSON carries a maintenance block: %s", page.body)
	}
	if !bytesContains(page.body, `"showMaintenance":false`) {
		t.Errorf("the public settings do not say maintenance is off: %s", page.body)
	}
	sum := env.do(http.MethodGet, "/api/public/pages/maintoff/summary.json", nil, "")
	if !bytesContains(sum.body, `"scheduled_maintenances": []`) {
		t.Errorf("summary.json scheduled_maintenances is not empty: %s", sum.body)
	}
	// The incident permalink of a maintenance report is as unknown as any other 404.
	var list struct {
		Incidents []struct{ ID int64 } `json:"incidents"`
	}
	env.do(http.MethodGet, "/api/public/pages/maintoff/incidents", nil, "").json(t, &list)
	if len(list.Incidents) != 0 {
		t.Errorf("incident list = %+v, want none", list.Incidents)
	}

	// Switching it back on shows everything again; the setting is stored.
	var pages struct {
		Pages []struct {
			ID   int64
			Slug string
		} `json:"pages"`
	}
	env.do(http.MethodGet, "/api/pages", nil, "admin").json(t, &pages)
	var id int64
	for _, p := range pages.Pages {
		if p.Slug == "maintoff" {
			id = p.ID
		}
	}
	cur := env.do(http.MethodGet, "/api/pages/"+itoa(id), nil, "admin")
	var full map[string]any
	cur.json(t, &full)
	full["settings"].(map[string]any)["showMaintenance"] = true
	if r := env.do(http.MethodPut, "/api/pages/"+itoa(id), full, "admin"); r.status != http.StatusOK {
		t.Fatalf("update: %d %s", r.status, r.body)
	}
	if after := env.do(http.MethodGet, "/api/public/pages/maintoff", nil, ""); !bytesContains(after.body, "UPCOMINGWINDOW-maintoff") {
		t.Errorf("maintenance did not come back after turning it on: %s", after.body)
	}
}

// The hidden-maintenance mask never claims healthy service and never names
// the cause.
func TestHideMaintenanceMasksStatus(t *testing.T) {
	pm := hub.PublicMonitor{Status: model.StatusMaintenance, Beats: "uumm"}
	got := hub.HideMaintenance(pm)
	if got.Status != model.StatusUnknown || got.Beats != "uukk" {
		t.Errorf("HideMaintenance = %+v", got)
	}
	if up := hub.HideMaintenance(hub.PublicMonitor{Status: model.StatusUp, Beats: "uu"}); up.Status != model.StatusUp || up.Beats != "uu" {
		t.Errorf("a healthy monitor changed: %+v", up)
	}
}
