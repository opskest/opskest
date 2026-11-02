package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestProfileSelfUpdatePersistsWithoutChangingIdentity(t *testing.T) {
	env := newTestEnv(t)
	before, err := env.store.GetUser(context.Background(), env.viewerID)
	if err != nil {
		t.Fatal(err)
	}
	response := env.do(http.MethodPut, "/api/me", map[string]any{"name": "  Sam Rivera  "}, "viewer")
	if response.status != http.StatusOK {
		t.Fatalf("PUT: %d %s", response.status, response.body)
	}
	var updated struct {
		OK   bool   `json:"ok"`
		Name string `json:"name"`
	}
	response.json(t, &updated)
	if !updated.OK || updated.Name != "Sam Rivera" {
		t.Fatalf("response: %+v", updated)
	}
	after, err := env.store.GetUser(context.Background(), env.viewerID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != "Sam Rivera" || after.Email != before.Email || after.Role != before.Role || after.AvatarURL != before.AvatarURL {
		t.Fatalf("identity changed: before=%+v after=%+v", before, after)
	}
	var me struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Role string `json:"role"`
	}
	env.do(http.MethodGet, "/api/me", nil, "viewer").json(t, &me)
	if me.ID != env.viewerID || me.Name != "Sam Rivera" || me.Role != "viewer" {
		t.Fatalf("fresh identity: %+v", me)
	}
	admin, err := env.store.GetUser(context.Background(), env.adminID)
	if err != nil || admin.Name == "Sam Rivera" {
		t.Fatalf("another user changed: %+v %v", admin, err)
	}
}

func TestProfileRejectsUnauthenticatedAndPrivilegedFields(t *testing.T) {
	env := newTestEnv(t)
	if response := env.do(http.MethodPut, "/api/me", map[string]any{"name": "Anonymous"}, ""); response.status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d", response.status)
	}
	for _, field := range []string{"id", "role", "email", "username", "avatarUrl"} {
		response := env.do(http.MethodPut, "/api/me", map[string]any{"name": "Modified", field: "admin"}, "viewer")
		if response.status != http.StatusBadRequest {
			t.Fatalf("accepted %s: %d %s", field, response.status, response.body)
		}
	}
	viewer, err := env.store.GetUser(context.Background(), env.viewerID)
	if err != nil || viewer.Role != "viewer" || viewer.Name == "Modified" {
		t.Fatalf("rejected update modified identity: %+v %v", viewer, err)
	}
}

func TestProfileNameValidation(t *testing.T) {
	env := newTestEnv(t)
	for _, name := range []string{"", "   ", strings.Repeat("界", 101), "Sam\nRivera", "\tSam", "Sam\x00Rivera"} {
		response := env.do(http.MethodPut, "/api/me", map[string]any{"name": name}, "admin")
		if response.status != http.StatusBadRequest {
			t.Fatalf("accepted invalid name %q: %d", name, response.status)
		}
	}
	response := env.do(http.MethodPut, "/api/me", map[string]any{"name": strings.Repeat("界", 100)}, "admin")
	if response.status != http.StatusOK {
		t.Fatalf("valid Unicode name rejected: %d %s", response.status, response.body)
	}
}
