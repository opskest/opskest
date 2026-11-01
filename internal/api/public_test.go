package api

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"opskest/internal/hub"
	"opskest/internal/model"
)

// TestPublicMaintenanceNoInternals proves a public page's maintenance
// entries carry only presentation fields — never the window's target
// monitor/group ids (which can name monitors that aren't on the page) or
// who created it — and still carry the computed nextStart the card shows.
func TestPublicMaintenanceNoInternals(t *testing.T) {
	env := newTestEnv(t)
	onPage := env.createMonitor(httpMonitorIn("on page", "https://example.com/a"))
	offPage := env.createMonitor(httpMonitorIn("off page", "https://example.com/b"))

	start := time.Now().Add(1 * time.Hour).UnixMilli()
	end := time.Now().Add(2 * time.Hour).UnixMilli()
	if r := env.do(http.MethodPost, "/api/maintenance", map[string]any{
		"title": "DB upgrade", "kind": "once", "startsAt": start, "endsAt": end,
		"active": true, "tz": "UTC", "monitorIds": []int64{onPage.ID, offPage.ID},
	}, "admin"); r.status != http.StatusCreated {
		t.Fatalf("create maintenance: status = %d, body = %s", r.status, r.body)
	}
	if r := env.do(http.MethodPost, "/api/pages", map[string]any{
		"slug": "mainttest", "title": "Maint", "visibility": "public", "layout": "list",
		"items": []map[string]any{{"monitorId": onPage.ID, "sort": 0}},
	}, "admin"); r.status != http.StatusCreated {
		t.Fatalf("create page: status = %d, body = %s", r.status, r.body)
	}

	resp := env.do(http.MethodGet, "/api/public/pages/mainttest", nil, "")
	if resp.status != http.StatusOK {
		t.Fatalf("get page: status = %d, body = %s", resp.status, resp.body)
	}
	for _, needle := range []string{"monitorIds\":[", "groupIds", "createdBy"} {
		if bytesContains(resp.body, needle) {
			t.Errorf("public page leaks %q: %s", needle, resp.body)
		}
	}
	var body struct {
		Maintenance struct {
			Upcoming []struct {
				Title     string `json:"title"`
				NextStart *int64 `json:"nextStart"`
			} `json:"upcoming"`
		} `json:"maintenance"`
	}
	resp.json(t, &body)
	if len(body.Maintenance.Upcoming) != 1 || body.Maintenance.Upcoming[0].Title != "DB upgrade" {
		t.Fatalf("upcoming = %+v, want the DB upgrade window", body.Maintenance.Upcoming)
	}
	if body.Maintenance.Upcoming[0].NextStart == nil {
		t.Errorf("upcoming window has no nextStart")
	}
}

// TestPublicDayRespectsShowLatency proves the day drill-down omits latency
// on a public page that has showLatency off, as the page body does.
func TestPublicDayRespectsShowLatency(t *testing.T) {
	env := newTestEnv(t)
	mon := env.createMonitor(httpMonitorIn("lat monitor", "https://example.com/lat"))
	// The day view lists only non-up beats, so drive a failing check that
	// still measured a latency.
	env.engine.Apply(mon.ID, model.Result{
		At: time.Now(), OK: false, LatencyMs: 4321,
		Reason: "HTTP 503", ErrClass: model.ErrHTTPStatus,
	})
	env.writer.Flush()

	if r := env.do(http.MethodPost, "/api/pages", map[string]any{
		"slug": "nolat", "title": "No latency", "visibility": "public", "layout": "list",
		"showLatency": false,
		"items":       []map[string]any{{"monitorId": mon.ID, "sort": 0}},
	}, "admin"); r.status != http.StatusCreated {
		t.Fatalf("create page: status = %d, body = %s", r.status, r.body)
	}

	path := "/api/public/pages/nolat/day?monitor=" + strconv.FormatInt(mon.ID, 10) +
		"&date=" + time.Now().UTC().Format("2006-01-02")
	resp := env.do(http.MethodGet, path, nil, "")
	if resp.status != http.StatusOK {
		t.Fatalf("day: status = %d, body = %s", resp.status, resp.body)
	}
	if !bytesContains(resp.body, "\"ts\"") {
		t.Fatalf("day returned no beats: %s", resp.body)
	}
	if bytesContains(resp.body, "latencyMs") || bytesContains(resp.body, "4321") {
		t.Errorf("day leaks latency with showLatency off: %s", resp.body)
	}
}

// TestPublicDayRateLimited proves the day drill-down shares the public
// page rate limit: a burst well past it from one IP eventually 429s.
func TestPublicDayRateLimited(t *testing.T) {
	env := newTestEnv(t)
	mon := env.createMonitor(httpMonitorIn("rl monitor", "https://example.com/rl"))
	if r := env.do(http.MethodPost, "/api/pages", map[string]any{
		"slug": "rl", "title": "RL", "visibility": "public", "layout": "list",
		"items": []map[string]any{{"monitorId": mon.ID, "sort": 0}},
	}, "admin"); r.status != http.StatusCreated {
		t.Fatalf("create page: status = %d, body = %s", r.status, r.body)
	}
	path := "/api/public/pages/rl/day?monitor=" + strconv.FormatInt(mon.ID, 10) + "&date=2026-01-01"
	for i := 0; i < 100; i++ {
		if env.do(http.MethodGet, path, nil, "").status == http.StatusTooManyRequests {
			return
		}
	}
	t.Fatalf("100 day requests from one IP never hit the rate limit")
}

// TestComputeOverall pins the page-level rollup: one monitor that isn't
// down must not read as a major outage, and half-or-more down is major.
func TestComputeOverall(t *testing.T) {
	pm := func(ss ...model.Status) []hub.PublicMonitor {
		out := make([]hub.PublicMonitor, len(ss))
		for i, s := range ss {
			out[i] = hub.PublicMonitor{Status: s}
		}
		return out
	}
	cases := []struct {
		name string
		in   []hub.PublicMonitor
		want string
	}{
		{"empty", nil, "operational"},
		{"single up", pm(model.StatusUp), "operational"},
		{"single unknown", pm(model.StatusUnknown), "operational"},
		{"single down", pm(model.StatusDown), "major"},
		{"one of three down", pm(model.StatusDown, model.StatusUp, model.StatusUp), "partial"},
		{"two of four down", pm(model.StatusDown, model.StatusDown, model.StatusUp, model.StatusUp), "major"},
		{"degraded", pm(model.StatusDegraded, model.StatusUp), "degraded"},
		{"maintenance", pm(model.StatusMaintenance, model.StatusUp), "maintenance"},
	}
	for _, c := range cases {
		if got := computeOverall(c.in); got != c.want {
			t.Errorf("%s: computeOverall = %q, want %q", c.name, got, c.want)
		}
	}
}

// publicFixture: a public page with one down monitor carrying sensitive
// target/reason/runbook, an incident linked to the page (with a public
// and an internal update), an incident NOT linked to it, and response
// times opted in.
type publicFixture struct {
	env              *testEnv
	monID            int64
	linkedIncident   int64
	unlinkedIncident int64
}

func newPublicFixture(t *testing.T, slug string, showResponseTime bool, visibility string) publicFixture {
	t.Helper()
	env := newTestEnv(t)
	mon := downMonitor(t, env, "secret monitor", "https://secret-host.internal/x", "HTTP 503 from secret-host")
	page := env.do(http.MethodPost, "/api/pages", map[string]any{
		"slug": slug, "title": "Fixture <Status> & Co", "visibility": visibility, "layout": "list",
		"showResponseTime": showResponseTime,
		"items":            []map[string]any{{"monitorId": mon.ID, "sort": 0, "description": "Public API"}},
	}, "admin")
	if page.status != http.StatusCreated {
		t.Fatalf("create page: %d %s", page.status, page.body)
	}
	var p struct {
		ID int64 `json:"id"`
	}
	page.json(t, &p)

	mk := func(title string, pageIDs []int64) int64 {
		r := env.do(http.MethodPost, "/api/incidents", map[string]any{
			"title": title, "impact": "major", "body": "We are looking into <script>alert(1)</script>",
			"pageIds": pageIDs, "monitorIds": []int64{mon.ID},
		}, "admin")
		if r.status != http.StatusCreated {
			t.Fatalf("create incident: %d %s", r.status, r.body)
		}
		var inc struct {
			ID int64 `json:"id"`
		}
		r.json(t, &inc)
		return inc.ID
	}
	linked := mk("Elevated errors", []int64{p.ID})
	if r := env.do(http.MethodPost, "/api/incidents/"+itoa(linked)+"/updates", map[string]any{
		"status": "identified", "body": "internal: secret-host db is wedged", "internal": true,
	}, "admin"); r.status != http.StatusCreated {
		t.Fatalf("internal update: %d %s", r.status, r.body)
	}
	unlinked := mk("Other team's incident", nil)

	// Internal notes on the outage (one edited) and an edited incident update:
	// none of this text may ever reach a public endpoint.
	sum, ok := env.engine.Summary(mon.ID)
	if !ok || sum.OutageID == nil {
		t.Fatalf("fixture monitor has no open outage")
	}
	oid := itoa(*sum.OutageID)
	if r := env.do(http.MethodPost, "/api/outages/"+oid+"/notes", map[string]any{"body": "ONCALLNOTE-one: restart db-prod-7"}, "admin"); r.status != http.StatusCreated {
		t.Fatalf("outage note: %d %s", r.status, r.body)
	}
	second := env.do(http.MethodPost, "/api/outages/"+oid+"/notes", map[string]any{"body": "ONCALLNOTE-two"}, "viewer")
	var sn struct {
		ID int64 `json:"id"`
	}
	second.json(t, &sn)
	if r := env.do(http.MethodPut, "/api/outages/"+oid+"/notes/"+itoa(sn.ID), map[string]any{"body": "ONCALLEDIT-three: credentials rotated"}, "viewer"); r.status != http.StatusOK {
		t.Fatalf("edit outage note: %d %s", r.status, r.body)
	}
	var full struct {
		Updates []struct {
			ID       int64 `json:"id"`
			Internal bool  `json:"internal"`
		} `json:"updates"`
	}
	env.do(http.MethodGet, "/api/incidents/"+itoa(linked), nil, "admin").json(t, &full)
	for _, u := range full.Updates {
		if u.Internal {
			if r := env.do(http.MethodPut, "/api/incidents/"+itoa(linked)+"/updates/"+itoa(u.ID), map[string]any{"body": "INTERNALEDIT: still wedged"}, "admin"); r.status != http.StatusOK {
				t.Fatalf("edit internal update: %d %s", r.status, r.body)
			}
		}
	}
	return publicFixture{env: env, monID: mon.ID, linkedIncident: linked, unlinkedIncident: unlinked}
}

// TestPublicEndpointsNeverLeak extends TestPublicPageNeverLeaks to every
// public endpoint: no target, reason, runbook or internal update text.
func TestPublicEndpointsNeverLeak(t *testing.T) {
	f := newPublicFixture(t, "leakall", true, "public")
	paths := []string{
		"/api/public/pages/leakall",
		"/api/public/pages/leakall/incidents",
		"/api/public/pages/leakall/incidents/" + itoa(f.linkedIncident),
		"/api/public/pages/leakall/latency?monitor=" + itoa(f.monID) + "&range=24h",
		"/api/public/pages/leakall/feed.atom",
		"/api/public/pages/leakall/feed.rss",
		"/api/public/pages/leakall/summary.json",
		"/api/public/pages/leakall/day?monitor=" + itoa(f.monID) + "&date=" + time.Now().UTC().Format("2006-01-02"),
	}
	// Embeddable badges (public_badge.go): overall, service status, uptime.
	paths = append(paths, badgePaths("leakall", f.monID)...)
	for _, p := range paths {
		r := f.env.do(http.MethodGet, p, nil, "")
		if r.status != http.StatusOK {
			t.Fatalf("%s: status %d body %s", p, r.status, r.body)
		}
		assertNoLeak(t, r.body)
		for _, needle := range []string{"wedged", "ONCALLNOTE", "ONCALLEDIT", "INTERNALEDIT", "db-prod-7", "credentials rotated"} {
			if bytesContains(r.body, needle) {
				t.Errorf("%s leaks internal text %q: %s", p, needle, r.body)
			}
		}
	}
}

func TestPublicIncidentEndpoints(t *testing.T) {
	f := newPublicFixture(t, "hist", false, "public")
	env := f.env

	list := env.do(http.MethodGet, "/api/public/pages/hist/incidents?limit=1000", nil, "")
	var body struct {
		Incidents []struct {
			ID      int64 `json:"id"`
			Updates []struct {
				Body string `json:"body"`
			} `json:"updates"`
		} `json:"incidents"`
	}
	list.json(t, &body)
	if len(body.Incidents) != 1 || body.Incidents[0].ID != f.linkedIncident {
		t.Fatalf("history = %+v, want only the linked incident", body.Incidents)
	}
	if len(body.Incidents[0].Updates) != 1 {
		t.Fatalf("updates = %+v, want only the public one", body.Incidents[0].Updates)
	}

	// Exists, but not on this page: 404, same as unknown.
	for _, id := range []int64{f.unlinkedIncident, 99999} {
		if r := env.do(http.MethodGet, "/api/public/pages/hist/incidents/"+itoa(id), nil, ""); r.status != http.StatusNotFound {
			t.Errorf("incident %d: status %d, want 404", id, r.status)
		}
	}

	// Response times not opted in: 404.
	if r := env.do(http.MethodGet, "/api/public/pages/hist/latency?monitor="+itoa(f.monID), nil, ""); r.status != http.StatusNotFound {
		t.Errorf("latency without opt-in: %d, want 404", r.status)
	}

	// Feed: valid Atom, HTML in incident text escaped.
	feed := env.do(http.MethodGet, "/api/public/pages/hist/feed.atom", nil, "")
	if !bytesContains(feed.body, "<feed xmlns=\"http://www.w3.org/2005/Atom\">") || !bytesContains(feed.body, "Elevated errors") {
		t.Fatalf("feed = %s", feed.body)
	}
	if bytesContains(feed.body, "<script>") || !bytesContains(feed.body, "&lt;script&gt;") {
		t.Errorf("feed does not escape incident text: %s", feed.body)
	}
	if bytesContains(feed.body, "Other team") {
		t.Errorf("feed includes an incident not linked to the page")
	}
}

func TestPublicLatencyGate(t *testing.T) {
	f := newPublicFixture(t, "rt", true, "public")
	other := f.env.createMonitor(httpMonitorIn("not on page", "https://example.com/np"))
	if r := f.env.do(http.MethodGet, "/api/public/pages/rt/latency?monitor="+itoa(other.ID), nil, ""); r.status != http.StatusNotFound {
		t.Errorf("monitor not on page: %d, want 404", r.status)
	}
	if r := f.env.do(http.MethodGet, "/api/public/pages/rt/latency?monitor="+itoa(f.monID)+"&range=90d", nil, ""); r.status != http.StatusBadRequest {
		t.Errorf("public 90d: %d, want 400", r.status)
	}
	r := f.env.do(http.MethodGet, "/api/public/pages/rt/latency?monitor="+itoa(f.monID)+"&range=7d", nil, "")
	if r.status != http.StatusOK || bytesContains(r.body, "p99") || bytesContains(r.body, "max") {
		t.Errorf("public latency = %d %s, want 200 with p50/p95 only", r.status, r.body)
	}
}

func TestPrivatePageEndpoints404(t *testing.T) {
	f := newPublicFixture(t, "priv", true, "private")
	paths := []string{
		"/api/public/pages/priv/incidents",
		"/api/public/pages/priv/incidents/" + itoa(f.linkedIncident),
		"/api/public/pages/priv/latency?monitor=" + itoa(f.monID),
		"/api/public/pages/priv/feed.atom",
		"/api/public/pages/priv/feed.rss",
		"/api/public/pages/priv/day?monitor=" + itoa(f.monID) + "&date=2026-01-01",
	}
	for _, p := range paths {
		if r := f.env.do(http.MethodGet, p, nil, ""); r.status != http.StatusNotFound {
			t.Errorf("%s anonymous: %d, want 404", p, r.status)
		}
		if r := f.env.do(http.MethodGet, p, nil, "viewer"); r.status != http.StatusOK {
			t.Errorf("%s viewer: %d, want 200 (%s)", p, r.status, r.body)
		}
	}
}

// TestPublicPageDailyIncidentsAndBranding: the page body carries branding,
// component descriptions, and the linked incident on today's bar.
func TestPublicPageDailyIncidentsAndBranding(t *testing.T) {
	f := newPublicFixture(t, "bars", false, "public")
	r := f.env.do(http.MethodGet, "/api/public/pages/bars", nil, "")
	var body struct {
		Page struct {
			HistoryDays int `json:"historyDays"`
		} `json:"page"`
		MonitorDescriptions map[string]string `json:"monitorDescriptions"`
		Daily               map[string][]struct {
			Down        int `json:"down"`
			DownMinutes int `json:"downMinutes"`
			Incidents   []struct {
				ID int64 `json:"id"`
			} `json:"incidents"`
		} `json:"daily"`
	}
	r.json(t, &body)
	key := itoa(f.monID)
	if body.Page.HistoryDays != 90 || body.MonitorDescriptions[key] != "Public API" {
		t.Fatalf("page = %+v descriptions = %v", body.Page, body.MonitorDescriptions)
	}
	days := body.Daily[key]
	if len(days) == 0 {
		t.Fatalf("no daily bars: %s", r.body)
	}
	today := days[len(days)-1]
	if today.Down == 0 || today.DownMinutes == 0 {
		t.Errorf("today = %+v, want downtime from the down beat", today)
	}
	found := false
	for _, inc := range today.Incidents {
		if inc.ID == f.linkedIncident {
			found = true
		}
		if inc.ID == f.unlinkedIncident {
			t.Errorf("unlinked incident on the page's bars")
		}
	}
	if !found {
		t.Errorf("today's bar lacks the linked incident: %+v", today.Incidents)
	}
}

// TestPublicPageHistoryNotStaleAfterGroupChange: the cached bars are keyed
// by the exact monitor set, so moving monitors in/out of an on-page group
// (same count) never serves the old monitor's history.
func TestPublicPageHistoryNotStaleAfterGroupChange(t *testing.T) {
	env := newTestEnv(t)
	g := env.do(http.MethodPost, "/api/groups", map[string]any{"name": "G"}, "admin")
	var grp struct {
		ID int64 `json:"id"`
	}
	g.json(t, &grp)
	in := httpMonitorIn("a", "https://example.com/a")
	in.GroupID = &grp.ID
	a := env.createMonitor(in)
	b := env.createMonitor(httpMonitorIn("b", "https://example.com/b"))
	seedBeats(env, a.ID, []int{10})
	seedBeats(env, b.ID, []int{10})
	if r := env.do(http.MethodPost, "/api/pages", map[string]any{
		"slug": "grp", "title": "G", "visibility": "public", "layout": "list",
		"items": []map[string]any{{"groupId": grp.ID, "sort": 0}},
	}, "admin"); r.status != http.StatusCreated {
		t.Fatalf("page: %d %s", r.status, r.body)
	}
	first := env.do(http.MethodGet, "/api/public/pages/grp", nil, "")
	if !bytesContains(first.body, "\""+itoa(a.ID)+"\":[") {
		t.Fatalf("first body lacks monitor a: %s", first.body)
	}

	// Swap a out of the group and b in: same count, different set.
	upA := httpMonitorIn("a", "https://example.com/a")
	if r := env.do(http.MethodPut, "/api/monitors/"+itoa(a.ID), upA, "admin"); r.status != http.StatusOK {
		t.Fatalf("update a: %d %s", r.status, r.body)
	}
	upB := httpMonitorIn("b", "https://example.com/b")
	upB.GroupID = &grp.ID
	if r := env.do(http.MethodPut, "/api/monitors/"+itoa(b.ID), upB, "admin"); r.status != http.StatusOK {
		t.Fatalf("update b: %d %s", r.status, r.body)
	}
	second := env.do(http.MethodGet, "/api/public/pages/grp", nil, "")
	if bytesContains(second.body, "\""+itoa(a.ID)+"\":[") || !bytesContains(second.body, "\""+itoa(b.ID)+"\":[") {
		t.Fatalf("second body served stale history: %s", second.body)
	}
}

// TestPublicFeedPrivateNotCacheable: a private page's feed must not be
// marked cacheable by shared caches.
func TestPublicFeedPrivateNotCacheable(t *testing.T) {
	f := newPublicFixture(t, "pfeed", false, "private")
	r := f.env.do(http.MethodGet, "/api/public/pages/pfeed/feed.atom", nil, "viewer")
	if got := r.header.Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("private feed Cache-Control = %q", got)
	}
}

// TestPublicDayUsesOutagesAndDisplayDay pins the day view to the same day as
// the daily bars (DISPLAY_TZ, not UTC) and to outage rows, so an early-
// morning outage with no raw beats left still shows. A public page never
// sees the outage's reason; a private one does.
func TestPublicDayUsesOutagesAndDisplayDay(t *testing.T) {
	env := newTestEnv(t)
	mon := env.createMonitor(httpMonitorIn("day monitor", "https://example.com/day"))
	for _, p := range []map[string]any{
		{"slug": "dpub", "title": "Pub", "visibility": "public", "layout": "list", "items": []map[string]any{{"monitorId": mon.ID, "sort": 0}}},
		{"slug": "dpriv", "title": "Priv", "visibility": "private", "layout": "list", "items": []map[string]any{{"monitorId": mon.ID, "sort": 0}}},
	} {
		if r := env.do(http.MethodPost, "/api/pages", p, "admin"); r.status != http.StatusCreated {
			t.Fatalf("create page: %d %s", r.status, r.body)
		}
	}
	loc := env.store.Loc
	day := time.Now().In(loc).AddDate(0, 0, -10)
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	// 00:20–00:30 local: outside the UTC day of the same date whenever the
	// display zone is ahead of UTC, and with no heartbeats at all.
	addOutage(t, env, 7001, mon.ID, dayStart.Add(20*time.Minute), dayStart.Add(30*time.Minute))
	if _, err := env.store.W.ExecContext(context.Background(), `UPDATE outages SET first_reason='secret-reason-503', note='secret-note-db' WHERE id=7001`); err != nil {
		t.Fatal(err)
	}
	// Spans midnight into the next day: clipped to this day's end.
	addOutage(t, env, 7002, mon.ID, dayStart.Add(23*time.Hour+50*time.Minute), dayStart.Add(24*time.Hour+15*time.Minute))

	type period struct {
		Status string `json:"status"`
		Start  int64  `json:"start"`
		End    int64  `json:"end"`
		Reason string `json:"reason"`
		Note   string `json:"note"`
	}
	get := func(slug, role string) []period {
		resp := env.do(http.MethodGet, "/api/public/pages/"+slug+"/day?monitor="+itoa(mon.ID)+"&date="+dayStart.Format("2006-01-02"), nil, role)
		if resp.status != http.StatusOK {
			t.Fatalf("%s: status %d %s", slug, resp.status, resp.body)
		}
		if role == "" && (bytesContains(resp.body, "secret-reason-503") || bytesContains(resp.body, "secret-note-db")) {
			t.Fatalf("public day leaks the outage reason: %s", resp.body)
		}
		var got struct {
			Periods []period `json:"periods"`
		}
		resp.json(t, &got)
		return got.Periods
	}
	ps := get("dpub", "")
	if len(ps) != 2 {
		t.Fatalf("periods = %+v, want 2", ps)
	}
	if ps[0].Status != "down" || ps[0].Start != dayStart.Add(20*time.Minute).UnixMilli() || ps[0].End != dayStart.Add(30*time.Minute).UnixMilli() {
		t.Errorf("first = %+v", ps[0])
	}
	if ps[1].End != dayStart.Add(24*time.Hour).UnixMilli() {
		t.Errorf("midnight-spanning outage not clipped to the day: %+v", ps[1])
	}
	if priv := get("dpriv", "viewer"); len(priv) != 2 || priv[0].Reason != "secret-reason-503" || priv[0].Note != "secret-note-db" {
		t.Errorf("private day periods = %+v, want the reason", priv)
	}
}
