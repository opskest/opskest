package api

import (
	"net/http"
	"strings"
	"testing"
)

func settingsPage(slug string, settings map[string]any, extra map[string]any) map[string]any {
	m := map[string]any{"slug": slug, "title": "Settings page", "visibility": "public", "layout": "list"}
	if settings != nil {
		m["settings"] = settings
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestPageSettingsValidation(t *testing.T) {
	env := newTestEnv(t)
	bad := []struct {
		name     string
		settings map[string]any
		field    string
	}{
		{"unknown header style", map[string]any{"headerStyle": "neon"}, "headerStyle"},
		{"unknown font", map[string]any{"font": "Comic Sans MS"}, "font"},
		{"unknown corners", map[string]any{"corners": "blob"}, "corners"},
		{"unknown colour mode", map[string]any{"colorMode": "sepia"}, "colorMode"},
		{"javascript link in description", map[string]any{"descriptionMd": "[x](javascript:alert(1))"}, "descriptionMd"},
		{"data link in footer", map[string]any{"footerMd": "[x](data:text/html,hi)"}, "footerMd"},
		{"relative link", map[string]any{"descriptionMd": "[x](/login)"}, "descriptionMd"},
		{"http image", map[string]any{"footerMd": "![logo](http://cdn.example.com/a.png)"}, "footerMd"},
		{"html tag", map[string]any{"descriptionMd": "hello <script>alert(1)</script>"}, "descriptionMd"},
		{"html img", map[string]any{"footerMd": "<img src=x onerror=alert(1)>"}, "footerMd"},
		{"long description", map[string]any{"descriptionMd": strings.Repeat("a", 2001)}, "descriptionMd"},
		{"long footer", map[string]any{"footerMd": strings.Repeat("a", 601)}, "footerMd"},
		{"control character", map[string]any{"descriptionMd": "a\x00b"}, "descriptionMd"},
		{"multiline banner", map[string]any{"banner": map[string]any{"text": "a\nb", "severity": "info"}}, "bannerText"},
		{"long banner", map[string]any{"banner": map[string]any{"text": strings.Repeat("b", 301), "severity": "info"}}, "bannerText"},
		{"bad banner link", map[string]any{"banner": map[string]any{"text": "[go](vbscript:x)", "severity": "info"}}, "bannerText"},
		{"banner severity", map[string]any{"banner": map[string]any{"text": "hi", "severity": "panic"}}, "bannerSeverity"},
		{"refresh", map[string]any{"refreshSeconds": 5}, "refreshSeconds"},
		{"canonical http", map[string]any{"canonicalUrl": "http://status.example.com"}, "canonicalUrl"},
		{"canonical path", map[string]any{"canonicalUrl": "https://example.com/status"}, "canonicalUrl"},
		{"canonical query", map[string]any{"canonicalUrl": "https://example.com/?a=b"}, "canonicalUrl"},
		{"canonical userinfo", map[string]any{"canonicalUrl": "https://user:pw@example.com"}, "canonicalUrl"},
		{"canonical javascript", map[string]any{"canonicalUrl": "javascript:alert(1)"}, "canonicalUrl"},
	}
	for _, c := range bad {
		r := env.do(http.MethodPost, "/api/pages", settingsPage("bad-"+strings.ReplaceAll(c.name, " ", "-"), c.settings, nil), "admin")
		if r.status != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body %s)", c.name, r.status, r.body)
			continue
		}
		if !strings.Contains(string(r.body), `"`+c.field+`"`) {
			t.Errorf("%s: error does not name %q: %s", c.name, c.field, r.body)
		}
	}
}

func TestPageSettingsRoundTripAndDefaults(t *testing.T) {
	env := newTestEnv(t)

	// A page created the way older clients do, with no settings at all.
	r := env.do(http.MethodPost, "/api/pages", settingsPage("legacy", nil, nil), "admin")
	if r.status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	var legacy struct {
		ID       int64 `json:"id"`
		Settings struct {
			HeaderStyle    string `json:"headerStyle"`
			Font           string `json:"font"`
			Corners        string `json:"corners"`
			ShowUptime     bool   `json:"showUptime"`
			ShowCert       bool   `json:"showCert"`
			RefreshSeconds int    `json:"refreshSeconds"`
			FeedEnabled    bool   `json:"feedEnabled"`
		} `json:"settings"`
	}
	r.json(t, &legacy)
	st := legacy.Settings
	if st.HeaderStyle != "plain" || st.Font != "system" || st.Corners != "rounded" || !st.ShowUptime || st.ShowCert || st.RefreshSeconds != 60 || !st.FeedEnabled {
		t.Fatalf("defaults = %+v", st)
	}

	// A partial document keeps the defaults of everything it leaves out.
	r = env.do(http.MethodPut, "/api/pages/"+itoa(legacy.ID), settingsPage("legacy", map[string]any{
		"font": "serif", "corners": "square", "colorMode": "light", "headerStyle": "solid",
		"descriptionMd": "We **care** about [uptime](https://example.com/up) and [mail](mailto:ops@example.com).",
		"footerMd":      "![badge](https://cdn.example.com/b.png)",
		"banner":        map[string]any{"text": "Planned work *tonight*", "severity": "warning"},
		"canonicalUrl":  "https://Status.Example.com/",
		"showCert":      true, "showLastCheck": true, "refreshSeconds": 30,
	}, nil), "admin")
	if r.status != http.StatusOK {
		t.Fatalf("update: %d %s", r.status, r.body)
	}
	got := env.do(http.MethodGet, "/api/pages/"+itoa(legacy.ID), nil, "admin")
	var page struct {
		Settings struct {
			Font         string `json:"font"`
			ShowUptime   bool   `json:"showUptime"`
			ShowCert     bool   `json:"showCert"`
			CanonicalURL string `json:"canonicalUrl"`
			Refresh      int    `json:"refreshSeconds"`
			Banner       struct {
				Severity string `json:"severity"`
			} `json:"banner"`
		} `json:"settings"`
	}
	got.json(t, &page)
	if page.Settings.Font != "serif" || !page.Settings.ShowUptime || !page.Settings.ShowCert || page.Settings.Refresh != 30 ||
		page.Settings.Banner.Severity != "warning" || page.Settings.CanonicalURL != "https://status.example.com" {
		t.Fatalf("round trip = %+v", page.Settings)
	}

	// An update that does not mention settings keeps what is stored.
	r = env.do(http.MethodPut, "/api/pages/"+itoa(legacy.ID), settingsPage("legacy", nil, map[string]any{"title": "Renamed"}), "admin")
	if r.status != http.StatusOK {
		t.Fatalf("update without settings: %d %s", r.status, r.body)
	}
	got = env.do(http.MethodGet, "/api/pages/"+itoa(legacy.ID), nil, "admin")
	got.json(t, &page)
	if page.Settings.Font != "serif" || page.Settings.CanonicalURL != "https://status.example.com" {
		t.Fatalf("settings lost on an update that omitted them: %+v", page.Settings)
	}
}

func TestPublicPageCarriesSettingsAndHonoursSwitches(t *testing.T) {
	env := newTestEnv(t)
	mon := downMonitor(t, env, "secret monitor", "https://secret-host.internal/x", "HTTP 503 from secret-host")
	r := env.do(http.MethodPost, "/api/pages", settingsPage("sw", map[string]any{
		"showUptime": false, "feedEnabled": false, "showLastCheck": true,
		"banner":        map[string]any{"text": "Heads up", "severity": "critical"},
		"descriptionMd": "Hello [world](https://example.com)",
		"canonicalUrl":  "https://status.example.org",
	}, map[string]any{"items": []map[string]any{{"monitorId": mon.ID, "sort": 0}}}), "admin")
	if r.status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	pub := env.do(http.MethodGet, "/api/public/pages/sw", nil, "")
	var body struct {
		Page struct {
			Settings struct {
				Banner struct {
					Text, Severity string
				} `json:"banner"`
				DescriptionMD string `json:"descriptionMd"`
				ShowUptime    bool   `json:"showUptime"`
				CanonicalURL  string `json:"canonicalUrl"`
			} `json:"settings"`
			FeedAvailable bool `json:"feedAvailable"`
		} `json:"page"`
		Uptime        map[string]any `json:"uptime"`
		MonitorExtras map[string]struct {
			LastCheckAt *int64 `json:"lastCheckAt"`
			CertDays    *int   `json:"certDays"`
		} `json:"monitorExtras"`
	}
	pub.json(t, &body)
	s := body.Page.Settings
	if s.Banner.Text != "Heads up" || s.Banner.Severity != "critical" || s.DescriptionMD == "" || s.ShowUptime || s.CanonicalURL != "https://status.example.org" {
		t.Fatalf("settings = %+v", s)
	}
	if len(body.Uptime) != 0 {
		t.Errorf("uptime map = %v, want empty while uptime is hidden", body.Uptime)
	}
	ex, ok := body.MonitorExtras[itoa(mon.ID)]
	if !ok || ex.LastCheckAt == nil || ex.CertDays != nil {
		t.Errorf("extras = %+v, want a last-check time and no certificate days", body.MonitorExtras)
	}
	if body.Page.FeedAvailable {
		t.Errorf("feedAvailable = true with the feed turned off")
	}
	assertNoLeak(t, pub.body)

	// The feed is off: 404, like a page that does not exist.
	if f := env.do(http.MethodGet, "/api/public/pages/sw/feed.atom", nil, ""); f.status != http.StatusNotFound {
		t.Errorf("feed with feedEnabled=false: %d, want 404", f.status)
	}

	// Switches off by default: no extras at all.
	r = env.do(http.MethodPost, "/api/pages", settingsPage("sw2", nil, map[string]any{"items": []map[string]any{{"monitorId": mon.ID, "sort": 0}}}), "admin")
	if r.status != http.StatusCreated {
		t.Fatalf("create sw2: %d %s", r.status, r.body)
	}
	plain := env.do(http.MethodGet, "/api/public/pages/sw2", nil, "")
	if bytesContains(plain.body, "lastCheckAt") || bytesContains(plain.body, "certDays") {
		t.Errorf("a page that did not opt in carries check times or certificate days: %s", plain.body)
	}
}

func TestPublicFeedUsesCanonicalOrigin(t *testing.T) {
	f := newPublicFixture(t, "canon", false, "public")
	var p struct {
		ID int64 `json:"id"`
	}
	list := f.env.do(http.MethodGet, "/api/pages", nil, "admin")
	var all struct {
		Pages []struct {
			ID   int64  `json:"id"`
			Slug string `json:"slug"`
		} `json:"pages"`
	}
	list.json(t, &all)
	for _, pg := range all.Pages {
		if pg.Slug == "canon" {
			p.ID = pg.ID
		}
	}
	r := f.env.do(http.MethodPut, "/api/pages/"+itoa(p.ID), settingsPage("canon", map[string]any{"canonicalUrl": "https://status.example.net"}, map[string]any{
		"title": "Fixture", "items": []map[string]any{{"monitorId": f.monID, "sort": 0}},
	}), "admin")
	if r.status != http.StatusOK {
		t.Fatalf("update: %d %s", r.status, r.body)
	}
	feed := f.env.do(http.MethodGet, "/api/public/pages/canon/feed.atom", nil, "")
	if !bytesContains(feed.body, "https://status.example.net/status/canon/incidents/") || !bytesContains(feed.body, "https://status.example.net/api/public/pages/canon/feed.atom") {
		t.Errorf("feed does not use the canonical origin: %s", feed.body)
	}
	sum := f.env.do(http.MethodGet, "/api/public/pages/canon/summary.json", nil, "")
	if !bytesContains(sum.body, `"url": "https://status.example.net/status/canon"`) {
		t.Errorf("summary page url: %s", sum.body)
	}
}

func TestPublicSummaryShapeAndPrivacy(t *testing.T) {
	f := newPublicFixture(t, "sumpub", true, "public")
	r := f.env.do(http.MethodGet, "/api/public/pages/sumpub/summary.json", nil, "")
	if r.status != http.StatusOK {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	assertNoLeak(t, r.body)
	for _, secret := range []string{"wedged", "Other team"} {
		if bytesContains(r.body, secret) {
			t.Errorf("summary leaks %q: %s", secret, r.body)
		}
	}
	var s struct {
		Page struct {
			ID, Name, URL string
		} `json:"page"`
		Status struct {
			Indicator, Description string
		} `json:"status"`
		Components []struct {
			ID, Name, Status string
			Description      *string
		} `json:"components"`
		Incidents []struct {
			Name            string
			Impact          string
			IncidentUpdates []struct{ Body string } `json:"incident_updates"`
		} `json:"incidents"`
		Maintenance []any `json:"scheduled_maintenances"`
	}
	r.json(t, &s)
	if s.Page.ID != "sumpub" || !strings.HasSuffix(s.Page.URL, "/status/sumpub") {
		t.Errorf("page = %+v", s.Page)
	}
	if s.Status.Indicator != "critical" {
		t.Errorf("indicator = %q, want critical (the only service is down)", s.Status.Indicator)
	}
	if len(s.Components) != 1 || s.Components[0].Status != "major_outage" {
		t.Fatalf("components = %+v", s.Components)
	}
	if s.Components[0].Description == nil || *s.Components[0].Description != "Public API" {
		t.Errorf("component description = %v", s.Components[0].Description)
	}
	if len(s.Incidents) != 1 || s.Incidents[0].Name != "Elevated errors" || s.Incidents[0].Impact != "major" {
		t.Errorf("incidents = %+v", s.Incidents)
	}
	if len(s.Incidents[0].IncidentUpdates) != 1 {
		t.Errorf("updates = %+v, want only the public one", s.Incidents[0].IncidentUpdates)
	}

	if got := r.header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("public summary CORS = %q, want *", got)
	}
	if !strings.Contains(r.header.Get("Cache-Control"), "public") {
		t.Errorf("Cache-Control = %q", r.header.Get("Cache-Control"))
	}
	if r.header.Get("X-Robots-Tag") == "" {
		t.Errorf("summary.json must be noindex")
	}
	// Conditional requests.
	again := f.env.doWithHeader(http.MethodGet, "/api/public/pages/sumpub/summary.json", "If-None-Match", r.header.Get("ETag"))
	if again.status != http.StatusNotModified {
		t.Errorf("If-None-Match: %d, want 304", again.status)
	}
}

func TestPrivateSummary404ThenNoStore(t *testing.T) {
	f := newPublicFixture(t, "sumpriv", true, "private")
	if r := f.env.do(http.MethodGet, "/api/public/pages/sumpriv/summary.json", nil, ""); r.status != http.StatusNotFound {
		t.Errorf("anonymous: %d, want 404", r.status)
	}
	if r := f.env.do(http.MethodGet, "/api/public/pages/nope/summary.json", nil, ""); r.status != http.StatusNotFound {
		t.Errorf("unknown page: %d, want 404", r.status)
	}
	r := f.env.do(http.MethodGet, "/api/public/pages/sumpriv/summary.json", nil, "viewer")
	if r.status != http.StatusOK {
		t.Fatalf("viewer: %d %s", r.status, r.body)
	}
	if !strings.Contains(r.header.Get("Cache-Control"), "no-store") || r.header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("private summary headers: cache=%q cors=%q", r.header.Get("Cache-Control"), r.header.Get("Access-Control-Allow-Origin"))
	}
}

func TestSummaryIndicatorRaisedByOpenIncident(t *testing.T) {
	env := newTestEnv(t)
	mon := env.createMonitor(httpMonitorIn("calm", "https://example.com/calm"))
	page := env.do(http.MethodPost, "/api/pages", settingsPage("calm", nil, map[string]any{"items": []map[string]any{{"monitorId": mon.ID, "sort": 0}}}), "admin")
	var p struct {
		ID int64 `json:"id"`
	}
	page.json(t, &p)
	if r := env.do(http.MethodPost, "/api/incidents", map[string]any{
		"title": "Degraded API", "impact": "minor", "body": "Investigating", "pageIds": []int64{p.ID}, "monitorIds": []int64{mon.ID},
	}, "admin"); r.status != http.StatusCreated {
		t.Fatalf("incident: %d %s", r.status, r.body)
	}
	r := env.do(http.MethodGet, "/api/public/pages/calm/summary.json", nil, "")
	if !bytesContains(r.body, `"indicator": "minor"`) {
		t.Errorf("an open minor incident should raise the indicator to minor: %s", r.body)
	}
}
