package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestTagValidation(t *testing.T) {
	env := newTestEnv(t)
	post := func(body map[string]any) *apiResponse { return env.do(http.MethodPost, "/api/tags", body, "user") }

	if resp := post(map[string]any{"name": "  prod  ", "color": "#aabbcc"}); resp.status != http.StatusCreated {
		t.Fatalf("valid tag = %d: %s", resp.status, resp.body)
	}
	for name, body := range map[string]map[string]any{
		"blank name":        {"name": "   "},
		"long name":         {"name": strings.Repeat("a", 41)},
		"control character": {"name": "a\x00b"},
		"css as colour":     {"name": "ok", "color": "red; background:url(x)"},
		"short hex":         {"name": "ok", "color": "#abc"},
	} {
		if resp := post(body); resp.status != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", name, resp.status)
		}
	}
	// The name is stored trimmed, and a second tag cannot take it.
	if resp := post(map[string]any{"name": "prod"}); resp.status != http.StatusConflict {
		t.Errorf("duplicate = %d, want 409", resp.status)
	}
	// Empty colour means the default.
	if resp := post(map[string]any{"name": "plain"}); resp.status != http.StatusCreated {
		t.Errorf("empty colour = %d", resp.status)
	}
}
