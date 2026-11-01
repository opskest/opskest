package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestSearchEngineRules(t *testing.T) {
	env := newTestEnv(t)
	mon := env.createMonitor(httpMonitorIn("seo monitor", "https://example.com/seo"))
	for _, p := range []map[string]any{
		{"slug": "seo-pub", "title": "Pub", "visibility": "public", "layout": "list", "items": []map[string]any{{"monitorId": mon.ID, "sort": 0}}},
		{"slug": "seo-priv", "title": "Priv", "visibility": "private", "layout": "list", "items": []map[string]any{{"monitorId": mon.ID, "sort": 0}}},
	} {
		if r := env.do(http.MethodPost, "/api/pages", p, "admin"); r.status != http.StatusCreated {
			t.Fatalf("create page: %d %s", r.status, r.body)
		}
	}

	robots := env.do(http.MethodGet, "/robots.txt", nil, "")
	if robots.status != http.StatusOK || !strings.Contains(string(robots.body), "Allow: /status/") || !strings.Contains(string(robots.body), "Disallow: /api/") {
		t.Fatalf("robots.txt: %d %q", robots.status, robots.body)
	}
	if strings.Contains(string(robots.body), "Disallow: /\n") {
		t.Fatal("robots.txt must not block the whole site: a blocked page can't show its noindex header")
	}

	noindex := []string{
		"/", "/login", "/monitors", "/settings", "/status/seo-priv", "/status/nope", "/status/seo-priv/history",
		"/status/seo-pub/subscribe/confirm", "/status/seo-pub/unsubscribe", "/status/seo-pub/board",
		"/api/me", "/api/public/pages/seo-pub", "/api/public/pages/seo-pub/badge.svg", "/api/public/pages/seo-pub/summary.json", "/healthz",
	}
	for _, path := range noindex {
		r := env.do(http.MethodGet, path, nil, "")
		if got := r.header.Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
			t.Errorf("%s: X-Robots-Tag = %q, want noindex", path, got)
		}
	}
	// A signed-in session changes nothing.
	if got := env.do(http.MethodGet, "/monitors", nil, "admin").header.Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
		t.Errorf("signed-in /monitors: %q", got)
	}

	for _, path := range []string{"/status/seo-pub", "/status/seo-pub/", "/status/seo-pub/history", "/status/seo-pub/incidents/12"} {
		r := env.do(http.MethodGet, path, nil, "")
		if got := r.header.Get("X-Robots-Tag"); got != "" {
			t.Errorf("%s: public status page must be indexable, got X-Robots-Tag %q", path, got)
		}
	}
}
