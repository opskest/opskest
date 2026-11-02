package api

import (
	"net/http"
	"strings"
	"testing"

	"opskest/internal/store"
)

func TestPageBrandingValidation(t *testing.T) {
	env := newTestEnv(t)
	base := func(extra map[string]any) map[string]any {
		m := map[string]any{"slug": "brand", "title": "Brand", "visibility": "public", "layout": "list"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	bad := []map[string]any{
		{"logoUrl": "http://example.com/logo.png"},
		{"logoUrl": "javascript:alert(1)"},
		{"websiteUrl": "ftp://example.com"},
		{"supportUrl": "https://"},
		{"brandColor": "red"},
		{"brandColor": "#12345"},
		{"footerText": strings.Repeat("x", 201)},
		{"historyDays": 45},
		{"items": []map[string]any{{"monitorId": 1, "sort": 0, "description": strings.Repeat("d", 201)}}},
	}
	for _, extra := range bad {
		if r := env.do(http.MethodPost, "/api/pages", base(extra), "admin"); r.status != http.StatusBadRequest {
			t.Errorf("%v: status = %d, want 400 (body %s)", extra, r.status, r.body)
		}
	}

	mon := env.createMonitor(httpMonitorIn("brand mon", "https://example.com/b"))
	good := base(map[string]any{
		"logoUrl": "https://cdn.example.com/logo.svg", "brandColor": "#0EA5E9",
		"websiteUrl": "https://example.com", "supportUrl": "https://example.com/support",
		"footerText": "© Example", "showResponseTime": true, "historyDays": 60,
		"items": []map[string]any{{"monitorId": mon.ID, "sort": 0, "description": "Public API"}},
	})
	r := env.do(http.MethodPost, "/api/pages", good, "admin")
	if r.status != http.StatusCreated {
		t.Fatalf("create: status = %d, body = %s", r.status, r.body)
	}
	var created store.Page
	r.json(t, &created)
	got := env.do(http.MethodGet, "/api/pages/"+itoa(created.ID), nil, "admin")
	var p store.Page
	got.json(t, &p)
	if p.LogoURL != "https://cdn.example.com/logo.svg" || p.BrandColor != "#0ea5e9" || p.HistoryDays != 60 ||
		!p.ShowResponseTime || p.FooterText != "© Example" || p.SupportURL == "" || p.WebsiteURL == "" {
		t.Fatalf("round trip = %+v", p)
	}
	if len(p.Items) != 1 || p.Items[0].Description != "Public API" {
		t.Fatalf("items = %+v", p.Items)
	}

	// Omitted historyDays defaults to 90.
	r = env.do(http.MethodPost, "/api/pages", map[string]any{"slug": "plain", "title": "Plain", "visibility": "public", "layout": "list"}, "admin")
	r.json(t, &created)
	if created.HistoryDays != 90 {
		t.Errorf("default historyDays = %d, want 90", created.HistoryDays)
	}
}
