package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestTemplatesCRUDAndScrubbing(t *testing.T) {
	env := newTestEnv(t)

	cfg := map[string]any{
		"type":           "http",
		"name":           "Checkout API",
		"target":         "https://internal.example.com/health",
		"groupId":        3,
		"intervalS":      30,
		"retries":        1,
		"notifyDegraded": true,
		"secrets":        map[string]any{"basicPass": "hunter2", "bearerToken": "tok-123"},
		"headers": []any{
			map[string]any{"name": "X-Env", "value": "prod"},
			map[string]any{"name": "Authorization", "value": "Bearer abc", "secret": true},
		},
		"settings": map[string]any{"keyword": `"status":"ok"`, "dependsOn": []int{7}},
	}
	res := env.do(http.MethodPost, "/api/templates", map[string]any{"name": "Prod web", "description": "Strict", "config": cfg}, "admin")
	if res.status != http.StatusCreated {
		t.Fatalf("create: %d %s", res.status, res.body)
	}
	var created struct {
		ID     int64          `json:"id"`
		Type   string         `json:"type"`
		Config map[string]any `json:"config"`
	}
	res.json(t, &created)
	if created.Type != "http" {
		t.Fatalf("type %q", created.Type)
	}
	body := string(res.body)
	for _, leak := range []string{"hunter2", "tok-123", "Bearer abc", "internal.example.com", "Checkout API"} {
		if strings.Contains(body, leak) {
			t.Errorf("template must not keep %q: %s", leak, body)
		}
	}
	for _, key := range []string{"name", "target", "groupId", "secrets"} {
		if _, ok := created.Config[key]; ok {
			t.Errorf("config still has %q", key)
		}
	}
	if st, _ := created.Config["settings"].(map[string]any); st["keyword"] == nil || st["dependsOn"] != nil {
		t.Errorf("settings: %v", created.Config["settings"])
	}
	if hs, _ := created.Config["headers"].([]any); len(hs) != 1 {
		t.Errorf("only the non-secret header survives: %v", created.Config["headers"])
	}

	// Duplicate names (any case) conflict.
	dup := env.do(http.MethodPost, "/api/templates", map[string]any{"name": "prod WEB", "config": cfg}, "admin")
	if dup.status != http.StatusConflict {
		t.Errorf("duplicate: %d", dup.status)
	}

	// Rename and list.
	put := env.do(http.MethodPut, "/api/templates/"+itoa(created.ID), map[string]any{"name": "Production web", "config": cfg}, "admin")
	if put.status != http.StatusOK || !strings.Contains(string(put.body), "Production web") {
		t.Fatalf("update: %d %s", put.status, put.body)
	}
	list := env.do(http.MethodGet, "/api/templates", nil, "admin")
	if list.status != http.StatusOK || !strings.Contains(string(list.body), "Production web") {
		t.Fatalf("list: %d %s", list.status, list.body)
	}

	// Validation.
	for name, in := range map[string]map[string]any{
		"no name":      {"name": " ", "config": cfg},
		"unknown type": {"name": "x", "config": map[string]any{"type": "carrier-pigeon"}},
		"not object":   {"name": "x", "config": []any{1}},
	} {
		if r := env.do(http.MethodPost, "/api/templates", in, "admin"); r.status != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}

	// Only admins.
	if r := env.do(http.MethodGet, "/api/templates", nil, "viewer"); r.status != http.StatusForbidden {
		t.Errorf("viewer list: %d", r.status)
	}
	if r := env.do(http.MethodPost, "/api/templates", map[string]any{"name": "v", "config": cfg}, "viewer"); r.status != http.StatusForbidden {
		t.Errorf("viewer create: %d", r.status)
	}
	if r := env.do(http.MethodGet, "/api/templates", nil, ""); r.status != http.StatusUnauthorized {
		t.Errorf("anonymous list: %d", r.status)
	}

	if r := env.do(http.MethodDelete, "/api/templates/"+itoa(created.ID), nil, "admin"); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d", r.status)
	}
	if r := env.do(http.MethodDelete, "/api/templates/"+itoa(created.ID), nil, "admin"); r.status != http.StatusNotFound {
		t.Errorf("delete again: %d", r.status)
	}
}
