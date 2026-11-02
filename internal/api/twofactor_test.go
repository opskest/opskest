package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// putSettings saves the workspace settings with the given overrides, starting
// from what is stored.
func putSettings(t *testing.T, env *testEnv, override map[string]any, drop ...string) *apiResponse {
	t.Helper()
	st, err := env.store.GetSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(st)
	body := map[string]any{}
	_ = json.Unmarshal(raw, &body)
	for k, v := range override {
		body[k] = v
	}
	for _, k := range drop {
		delete(body, k)
	}
	return env.do(http.MethodPut, "/api/settings", body, "admin")
}

func enrol(t *testing.T, env *testEnv, userID int64, username string) {
	t.Helper()
	_, err := env.store.W.ExecContext(t.Context(),
		`INSERT INTO local_credentials(user_id,username,password_hash,totp_secret) VALUES(?,?,?,?)`, userID, username, "x", []byte("sealed"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestRequireTwoFactor(t *testing.T) {
	env := newTestEnv(t)

	// Off by default: nobody is held back.
	if resp := env.do(http.MethodGet, "/api/monitors", nil, "user"); resp.status != http.StatusOK {
		t.Fatalf("policy off: monitors = %d", resp.status)
	}

	if resp := putSettings(t, env, map[string]any{"requireTwoFactor": true}); resp.status != http.StatusOK {
		t.Fatalf("turn policy on = %d: %s", resp.status, resp.body)
	}
	enrol(t, env, env.viewerID, "viewer")

	// Someone without an authenticator can only reach what sets one up.
	resp := env.do(http.MethodGet, "/api/monitors", nil, "user")
	if resp.status != http.StatusForbidden {
		t.Fatalf("no 2FA: monitors = %d, want 403", resp.status)
	}
	var gated map[string]string
	_ = json.Unmarshal([]byte(resp.body), &gated)
	if gated["code"] != "two_factor_required" {
		t.Errorf("403 body = %s", resp.body)
	}
	var me map[string]any
	if resp := env.do(http.MethodGet, "/api/me", nil, "user"); resp.status != http.StatusOK {
		t.Fatalf("me = %d", resp.status)
	} else {
		_ = json.Unmarshal([]byte(resp.body), &me)
	}
	if me["twoFactorRequired"] != true || me["twoFactorEnabled"] != false {
		t.Errorf("me = %v", me)
	}
	if resp := env.do(http.MethodGet, "/api/auth/security", nil, "user"); resp.status == http.StatusForbidden {
		t.Errorf("security page was gated: %d", resp.status)
	}
	// An administrator keeps the settings, so the policy can always be undone.
	if resp := env.do(http.MethodGet, "/api/settings", nil, "admin"); resp.status != http.StatusOK {
		t.Errorf("admin settings while unenrolled = %d, want 200", resp.status)
	}
	if resp := env.do(http.MethodGet, "/api/monitors", nil, "admin"); resp.status != http.StatusForbidden {
		t.Errorf("admin monitors while unenrolled = %d, want 403", resp.status)
	}

	// Someone with one is untouched, and cannot switch it off.
	if resp := env.do(http.MethodGet, "/api/monitors", nil, "viewer"); resp.status != http.StatusOK {
		t.Errorf("enrolled viewer monitors = %d, want 200", resp.status)
	}
	if resp := env.do(http.MethodPost, "/api/auth/2fa/disable", map[string]any{"password": "x"}, "viewer"); resp.status != http.StatusConflict {
		t.Errorf("disable while required = %d, want 409", resp.status)
	}

	// The team list shows who has it (the administrator sets theirs up first).
	enrol(t, env, env.adminID, "admin")
	var list struct {
		Users []struct {
			ID        int64 `json:"id"`
			TwoFactor bool  `json:"twoFactorEnabled"`
		} `json:"users"`
	}
	if resp := env.do(http.MethodGet, "/api/users", nil, "admin"); resp.status != http.StatusOK {
		t.Fatalf("users = %d", resp.status)
	} else {
		_ = json.Unmarshal([]byte(resp.body), &list)
	}
	for _, u := range list.Users {
		if want := u.ID == env.viewerID || u.ID == env.adminID; u.TwoFactor != want {
			t.Errorf("user %d twoFactorEnabled = %v, want %v", u.ID, u.TwoFactor, want)
		}
	}

	// A settings save that omits the field does not switch the policy off.
	if resp := putSettings(t, env, nil, "requireTwoFactor"); resp.status != http.StatusOK {
		t.Fatalf("save without the field = %d: %s", resp.status, resp.body)
	}
	if resp := env.do(http.MethodGet, "/api/monitors", nil, "user"); resp.status != http.StatusForbidden {
		t.Errorf("policy dropped by omission: monitors = %d", resp.status)
	}

	// Resetting another account's authenticator; not your own.
	if resp := env.do(http.MethodDelete, "/api/users/"+itoa64(env.adminID)+"/2fa", nil, "admin"); resp.status != http.StatusBadRequest {
		t.Errorf("reset own = %d, want 400", resp.status)
	}
	if resp := env.do(http.MethodDelete, "/api/users/"+itoa64(env.viewerID)+"/2fa", nil, "user"); resp.status != http.StatusForbidden {
		t.Errorf("non-admin reset = %d, want 403", resp.status)
	}
	if resp := env.do(http.MethodDelete, "/api/users/"+itoa64(env.viewerID)+"/2fa", nil, "admin"); resp.status != http.StatusNoContent {
		t.Fatalf("reset = %d: %s", resp.status, resp.body)
	}
	if resp := env.do(http.MethodGet, "/api/monitors", nil, "viewer"); resp.status != http.StatusForbidden {
		t.Errorf("after reset the viewer is not held back: %d", resp.status)
	}

	// Switching the policy off releases everyone.
	if resp := putSettings(t, env, map[string]any{"requireTwoFactor": false}); resp.status != http.StatusOK {
		t.Fatalf("turn policy off = %d", resp.status)
	}
	if resp := env.do(http.MethodGet, "/api/monitors", nil, "user"); resp.status != http.StatusOK {
		t.Errorf("policy off again: monitors = %d", resp.status)
	}
}
