package api

import (
	"context"
	"net/http"
	"testing"
)

// A failed source must never turn into an apparently empty incident or
// maintenance list on a customer-facing page, including after history is cached.
func TestPublicPageDoesNotHideCommunicationFailures(t *testing.T) {
	for _, table := range []string{"incidents", "maintenance"} {
		t.Run(table, func(t *testing.T) {
			env := newTestEnv(t)
			monitor := env.createMonitor(httpMonitorIn("service", "https://example.com"))
			created := env.do(http.MethodPost, "/api/pages", map[string]any{
				"slug": "customer-status", "title": "Service status", "visibility": "public", "layout": "list",
				"items": []map[string]any{{"monitorId": monitor.ID, "sort": 0}},
			}, "admin")
			if created.status != http.StatusCreated {
				t.Fatalf("create page: %d %s", created.status, created.body)
			}
			warm := env.do(http.MethodGet, "/api/public/pages/customer-status", nil, "")
			if warm.status != http.StatusOK {
				t.Fatalf("initial page: %d %s", warm.status, warm.body)
			}
			if _, err := env.store.W.ExecContext(context.Background(), "ALTER TABLE "+table+" RENAME TO unavailable_source"); err != nil {
				t.Fatal(err)
			}
			failed := env.do(http.MethodGet, "/api/public/pages/customer-status", nil, "")
			if failed.status != http.StatusInternalServerError {
				t.Fatalf("failed %s source returned %d: %s", table, failed.status, failed.body)
			}
		})
	}
}
