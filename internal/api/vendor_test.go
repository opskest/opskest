package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"opskest/internal/model"
)

// vendorMonitor creates a 3rd-party feed (Settings.Vendor) and drives it DOWN.
func vendorMonitor(t *testing.T, env *testEnv, name string, gid *int64) MonitorOut {
	t.Helper()
	in := httpMonitorIn(name, "https://status.provider.invalid/api/v2/summary.json")
	in.Retries = 0
	in.GroupID = gid
	in.Settings.Vendor = true
	out := env.createMonitor(in)
	env.engine.Apply(out.ID, model.Result{
		At: time.Now(), OK: false, LatencyMs: -1,
		Reason: "provider reports a major outage", ErrClass: model.ErrHTTPStatus,
	})
	env.writer.Flush()
	return out
}

// TestVendorIsNeverAnOutage: a 3rd-party feed going down updates its own
// state but opens no outage, and shows up in no outage list, summary,
// report, global event feed or metrics.
func TestVendorIsNeverAnOutage(t *testing.T) {
	env := newTestEnv(t)
	own := downMonitor(t, env, "own api", "https://example.com/own", "HTTP 503")
	v := vendorMonitor(t, env, "Provider status", nil)

	sum, ok := env.engine.Summary(v.ID)
	if !ok || sum.Status != model.StatusDown || !sum.Vendor || sum.OutageID != nil {
		t.Fatalf("vendor summary = %+v ok=%v, want DOWN, vendor, no outage", sum, ok)
	}
	if sum.Uptime24h != nil {
		t.Errorf("vendor has an uptime: %v", *sum.Uptime24h)
	}

	var outs struct {
		Outages []struct {
			MonitorID int64 `json:"monitorId"`
		} `json:"outages"`
	}
	env.do(http.MethodGet, "/api/outages?limit=200", nil, "viewer").json(t, &outs)
	if len(outs.Outages) != 1 || outs.Outages[0].MonitorID != own.ID {
		t.Fatalf("/api/outages = %+v, want only the own monitor's", outs.Outages)
	}
	if a := env.engine.ActiveOutages(); len(a) != 1 || a[0].MonitorID != own.ID {
		t.Fatalf("ActiveOutages (the SSE snapshot's list) = %+v", a)
	}
	var osum struct {
		Active int `json:"Active"`
	}
	env.do(http.MethodGet, "/api/outages/summary", nil, "viewer").json(t, &osum)
	if osum.Active != 1 {
		t.Fatalf("outage summary active = %d, want 1", osum.Active)
	}
	var mo struct {
		Outages []any `json:"outages"`
	}
	env.do(http.MethodGet, fmt.Sprintf("/api/monitors/%d/outages", v.ID), nil, "viewer").json(t, &mo)
	if len(mo.Outages) != 0 {
		t.Fatalf("vendor outages = %v", mo.Outages)
	}

	// /api/monitors still serves it, flagged, for the third-party page.
	var list struct {
		Monitors []struct {
			ID     int64  `json:"id"`
			Vendor bool   `json:"vendor"`
			Reason string `json:"reason"`
		} `json:"monitors"`
	}
	env.do(http.MethodGet, "/api/monitors", nil, "viewer").json(t, &list)
	found := false
	for _, m := range list.Monitors {
		if m.ID == v.ID {
			found = true
			if !m.Vendor || m.Reason == "" {
				t.Errorf("vendor entry = %+v", m)
			}
		}
	}
	if !found {
		t.Fatal("vendor missing from /api/monitors")
	}

	// Reports: not in "all", and its own scope is unknown.
	var rep slaResp
	env.do(http.MethodGet, "/api/reports/sla", nil, "viewer").json(t, &rep)
	for _, row := range rep.Rows {
		if row.ID == v.ID {
			t.Fatalf("vendor in the SLA report: %+v", row)
		}
	}
	if r := env.do(http.MethodGet, fmt.Sprintf("/api/reports/sla?scope=monitor&id=%d", v.ID), nil, "viewer"); r.status != http.StatusNotFound {
		t.Fatalf("vendor SLA scope = %d, want 404", r.status)
	}

	// The global events feed leaves it out; its own timeline stays.
	var evs struct {
		Events []struct {
			MonitorID int64 `json:"monitorId"`
		} `json:"events"`
	}
	env.do(http.MethodGet, "/api/events?limit=200", nil, "viewer").json(t, &evs)
	for _, e := range evs.Events {
		if e.MonitorID == v.ID {
			t.Fatalf("vendor status event in the global feed: %+v", e)
		}
	}
	if len(evs.Events) == 0 {
		t.Fatal("global feed lost the own monitor's events")
	}
	env.do(http.MethodGet, fmt.Sprintf("/api/events?monitorId=%d", v.ID), nil, "viewer").json(t, &evs)
	if len(evs.Events) == 0 {
		t.Fatal("vendor's own timeline is empty")
	}
}

// TestVendorRejectedAsComponentDependencyMaintenanceIncident: the API says
// no, clearly, wherever a feed would be treated as one of your own services.
func TestVendorRejectedAsComponentDependencyMaintenanceIncident(t *testing.T) {
	env := newTestEnv(t)
	gid := createGroup(t, env, "3rd party")
	v := vendorMonitor(t, env, "Provider status", &gid)
	own := env.createMonitor(httpMonitorIn("own", "https://example.com/own"))

	// Status page: a direct component is a 400 ...
	page := func(slug string, items []map[string]any) *apiResponse {
		return env.do(http.MethodPost, "/api/pages", map[string]any{
			"slug": slug, "title": slug, "visibility": "public", "layout": "list", "items": items,
		}, "admin")
	}
	r := page("direct", []map[string]any{{"monitorId": v.ID, "sort": 0}})
	if r.status != http.StatusBadRequest || !strings.Contains(string(r.body), "3rd-party") {
		t.Fatalf("page with a vendor component = %d %s", r.status, r.body)
	}
	// ... on update too.
	ok := page("ok", []map[string]any{{"monitorId": own.ID, "sort": 0}})
	if ok.status != http.StatusCreated {
		t.Fatalf("create page = %d %s", ok.status, ok.body)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	ok.json(t, &created)
	if u := env.do(http.MethodPut, fmt.Sprintf("/api/pages/%d", created.ID), map[string]any{
		"slug": "ok", "title": "ok", "visibility": "public", "layout": "list",
		"items": []map[string]any{{"monitorId": own.ID, "sort": 0}, {"monitorId": v.ID, "sort": 1}},
	}, "admin"); u.status != http.StatusBadRequest {
		t.Fatalf("update page with a vendor = %d %s", u.status, u.body)
	}

	// ... and a group that happens to contain a feed never exposes it.
	if g := page("viagroup", []map[string]any{{"groupId": gid, "sort": 0}}); g.status != http.StatusCreated {
		t.Fatalf("page with a group = %d %s", g.status, g.body)
	}
	pub := env.do(http.MethodGet, "/api/public/pages/viagroup", nil, "")
	if pub.status != http.StatusOK {
		t.Fatalf("public page = %d", pub.status)
	}
	for _, needle := range []string{"Provider status", "provider reports", "status.provider.invalid"} {
		if strings.Contains(string(pub.body), needle) {
			t.Errorf("public page leaks the vendor (%q): %s", needle, pub.body)
		}
	}
	if !strings.Contains(string(pub.body), `"monitors":[]`) {
		t.Errorf("public page lists monitors: %s", pub.body)
	}
	if r := env.do(http.MethodGet, fmt.Sprintf("/api/public/pages/viagroup/monitors/%d/history", v.ID), nil, ""); r.status != http.StatusNotFound {
		t.Errorf("vendor history on a public page = %d, want 404", r.status)
	}

	// Dependencies and groups.
	in := httpMonitorIn("child", "https://example.com/child")
	in.Settings.DependsOn = []int64{v.ID}
	if d := env.do(http.MethodPost, "/api/monitors", in, "admin"); d.status != http.StatusBadRequest || !strings.Contains(string(d.body), "3rd-party") {
		t.Fatalf("depending on a vendor = %d %s", d.status, d.body)
	}
	vin := httpMonitorIn("feed2", "https://status.provider.invalid/x")
	vin.Settings.Vendor = true
	vin.Settings.DependsOn = []int64{own.ID}
	if d := env.do(http.MethodPost, "/api/monitors", vin, "admin"); d.status != http.StatusBadRequest {
		t.Fatalf("a vendor depending on a monitor = %d %s", d.status, d.body)
	}
	grp := MonitorIn{Name: "g", Type: model.TypeGroup, IntervalS: 60, RetryIntervalS: 20, TimeoutMs: 5000}
	grp.Settings.Children = []int64{v.ID}
	if d := env.do(http.MethodPost, "/api/monitors", grp, "admin"); d.status != http.StatusBadRequest || !strings.Contains(string(d.body), "3rd-party") {
		t.Fatalf("group with a vendor child = %d %s", d.status, d.body)
	}

	// Maintenance and incidents.
	start, end := time.Now().Add(time.Hour).UnixMilli(), time.Now().Add(2*time.Hour).UnixMilli()
	if m := env.do(http.MethodPost, "/api/maintenance", map[string]any{
		"title": "w", "kind": "once", "startsAt": start, "endsAt": end, "active": true, "tz": "UTC", "monitorIds": []int64{v.ID},
	}, "admin"); m.status != http.StatusBadRequest || !strings.Contains(string(m.body), "3rd-party") {
		t.Fatalf("maintenance on a vendor = %d %s", m.status, m.body)
	}
	if i := env.do(http.MethodPost, "/api/incidents", map[string]any{
		"title": "x", "impact": "minor", "monitorIds": []int64{v.ID},
	}, "admin"); i.status != http.StatusBadRequest {
		t.Fatalf("incident with a vendor = %d %s", i.status, i.body)
	}
}
