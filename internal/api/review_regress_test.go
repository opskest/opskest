package api

import (
	"context"
	"net/http"
	"testing"
)

// Changing a channel's type must not carry the old service's URL and secrets
// into the new one, and an update can't blank the name.
func TestChannelTypeChangeDropsOldConfig(t *testing.T) {
	env := newTestEnv(t)
	create := env.do(http.MethodPost, "/api/channels", map[string]any{
		"name": "slack", "type": "slack", "config": map[string]any{"url": "https://hooks.slack.com/services/T/B/secret"},
	}, "admin")
	if create.status != http.StatusCreated {
		t.Fatalf("create: %d %s", create.status, create.body)
	}
	var made struct {
		ID int64 `json:"id"`
	}
	create.json(t, &made)
	path := "/api/channels/" + itoa(made.ID)

	if r := env.do(http.MethodPut, path, map[string]any{"type": "slack", "config": map[string]any{"url": nil}}, "admin"); r.status != http.StatusBadRequest {
		t.Fatalf("an update without a name must be rejected: %d %s", r.status, r.body)
	}

	r := env.do(http.MethodPut, path, map[string]any{
		"name": "tg", "type": "telegram", "config": map[string]any{"apiKey": "123:abc", "target": "42"},
	}, "admin")
	if r.status != http.StatusOK {
		t.Fatalf("type change: %d %s", r.status, r.body)
	}
	stored, err := env.store.GetChannel(context.Background(), made.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Config.URL != "" {
		t.Fatalf("the Slack webhook URL survived the change to Telegram: %q", stored.Config.URL)
	}
}

func TestTestMonitorBoundsTheTimeout(t *testing.T) {
	env := newTestEnv(t)
	for _, tc := range []struct{ in, want int }{{9_000_000_000_000, 60_000}, {-5, 1000}, {5000, 5000}} {
		body := map[string]any{
			"name": "t", "type": "tcp", "target": "127.0.0.1:9", "intervalS": 60, "retryIntervalS": 20,
			"timeoutMs": tc.in, "settings": map[string]any{}, "secrets": map[string]any{}, "headers": []any{},
		}
		if r := env.do(http.MethodPost, "/api/monitors/test", body, "admin"); r.status != http.StatusOK {
			t.Fatalf("status = %d, body = %s", r.status, r.body)
		}
		last := env.checker.calls[len(env.checker.calls)-1]
		if last.TimeoutMs != tc.want {
			t.Errorf("timeoutMs %d reached the checker as %d, want %d", tc.in, last.TimeoutMs, tc.want)
		}
	}
}
