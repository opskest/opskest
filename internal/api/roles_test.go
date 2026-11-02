package api

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"opskest/internal/auth"
	"opskest/internal/store"
)

// routeMatrix is the independent classification of every route: the minimum
// role it needs. "public" routes take no session and gate themselves (404 for
// private or unknown resources). A new route must be added here, or
// TestRoutePermissionMatrix fails. Keep Docs.md, "Roles" in step.
//
// viewer: any signed-in role (reads, own-account security, and the annotation
// exceptions: acknowledge an outage, add/edit/delete own outage notes).
// user:   operators (writes, plus operator-only reads such as the status page
// editor, subscriber lists, import/export).
// admin:  workspace settings, team, channels, backups, storage, updates,
// regions config, audit and delivery logs, system info.
var routeMatrix = map[string]string{
	// Public, no session.
	"GET /api/runtime":                                      "public",
	"GET /healthz":                                          "public",
	"GET /api/auth/status":                                  "public",
	"POST /api/auth/setup":                                  "public",
	"POST /api/auth/login":                                  "public",
	"POST /api/auth/logout":                                 "public",
	"GET /push/{token}":                                     "public",
	"POST /push/{token}":                                    "public",
	"GET /metrics":                                          "public", // METRICS_TOKEN bearer, not a session
	"GET /api/public/pages/{slug}":                          "public",
	"GET /api/public/pages/{slug}/stream":                   "public",
	"GET /api/public/pages/{slug}/day":                      "public",
	"GET /api/public/pages/{slug}/incidents":                "public",
	"GET /api/public/pages/{slug}/incidents/{id}":           "public",
	"GET /api/public/pages/{slug}/latency":                  "public",
	"GET /api/public/pages/{slug}/feed.atom":                "public",
	"GET /api/public/pages/{slug}/feed.rss":                 "public",
	"GET /api/public/pages/{slug}/summary.json":             "public",
	"GET /api/public/pages/{slug}/badge.svg":                "public",
	"GET /api/public/pages/{slug}/services/{id}/badge.svg":  "public",
	"GET /api/public/pages/{slug}/services/{id}/uptime.svg": "public",
	"POST /api/public/pages/{slug}/subscribe":               "public",
	"POST /api/public/subscriptions/confirm":                "public",
	"POST /api/public/subscriptions/unsubscribe":            "public",

	// Every signed-in role: own account, reads, annotation.
	"GET /api/auth/security":                  "viewer",
	"POST /api/auth/2fa/enroll":               "viewer",
	"POST /api/auth/2fa/enable":               "viewer",
	"POST /api/auth/2fa/disable":              "viewer",
	"POST /api/auth/2fa/recovery":             "viewer",
	"POST /api/auth/password":                 "viewer",
	"GET /api/me":                             "viewer",
	"PUT /api/me":                             "viewer",
	"GET /api/monitors":                       "viewer",
	"GET /api/monitors/{id}":                  "viewer",
	"GET /api/monitors/{id}/beats":            "viewer",
	"GET /api/monitors/{id}/series":           "viewer",
	"GET /api/monitors/{id}/daily":            "viewer",
	"GET /api/monitors/{id}/latency":          "viewer",
	"GET /api/monitors/{id}/diagnostics":      "viewer",
	"GET /api/monitors/{id}/outages":          "viewer",
	"GET /api/monitors/{id}/regions":          "viewer",
	"GET /api/reports/sla":                    "viewer",
	"GET /api/events":                         "viewer",
	"GET /api/groups":                         "viewer",
	"GET /api/tags":                           "viewer",
	"GET /api/stream":                         "viewer",
	"GET /api/templates":                      "user",
	"GET /api/channels":                       "viewer", // id, name, type only below admin
	"GET /api/outages":                        "viewer",
	"GET /api/outages/summary":                "viewer",
	"POST /api/outages/{id}/ack":              "viewer", // annotation
	"POST /api/outages/{id}/note":             "viewer", // annotation
	"GET /api/outages/{id}/notes":             "viewer",
	"POST /api/outages/{id}/notes":            "viewer", // annotation
	"PUT /api/outages/{id}/notes/{noteId}":    "viewer", // annotation (own notes; admin any)
	"DELETE /api/outages/{id}/notes/{noteId}": "viewer", // annotation (own notes; admin any)
	"GET /api/incidents":                      "viewer",
	"GET /api/incidents/{id}":                 "viewer",
	"GET /api/maintenance":                    "viewer",
	"GET /api/regions/issues":                 "viewer",

	// User and admin: operational writes and operator-only reads.
	"POST /api/monitors":                         "user",
	"PUT /api/monitors/{id}":                     "user",
	"DELETE /api/monitors/{id}":                  "user",
	"POST /api/monitors/{id}/check":              "user",
	"POST /api/monitors/{id}/status":             "user",
	"POST /api/monitors/{id}/pause":              "user",
	"POST /api/monitors/{id}/resume":             "user",
	"POST /api/monitors/{id}/duplicate":          "user",
	"POST /api/monitors/test":                    "user",
	"POST /api/monitors/bulk":                    "user",
	"POST /api/monitors/dns/resolve":             "user",
	"POST /api/monitors/{id}/dns/rotate":         "user",
	"POST /api/monitors/{id}/regions/check":      "user",
	"POST /api/groups":                           "user",
	"PUT /api/groups/{id}":                       "user",
	"DELETE /api/groups/{id}":                    "user",
	"POST /api/groups/reorder":                   "user",
	"POST /api/tags":                             "user",
	"PUT /api/tags/{id}":                         "user",
	"DELETE /api/tags/{id}":                      "user",
	"POST /api/templates":                        "user",
	"PUT /api/templates/{id}":                    "user",
	"DELETE /api/templates/{id}":                 "user",
	"POST /api/incidents":                        "user",
	"PUT /api/incidents/{id}":                    "user",
	"DELETE /api/incidents/{id}":                 "user",
	"POST /api/incidents/{id}/updates":           "user",
	"PUT /api/incidents/{id}/updates/{updateId}": "user",
	"PUT /api/incidents/{id}/postmortem":         "user",
	"POST /api/maintenance":                      "user",
	"PUT /api/maintenance/{id}":                  "user",
	"DELETE /api/maintenance/{id}":               "user",
	"GET /api/pages":                             "user",
	"POST /api/pages":                            "user",
	"GET /api/pages/{id}":                        "user",
	"PUT /api/pages/{id}":                        "user",
	"DELETE /api/pages/{id}":                     "user",
	"GET /api/pages/{id}/subscribers":            "user",
	"DELETE /api/pages/{id}/subscribers/{sid}":   "user",
	"GET /api/export/monitors":                   "user",
	"GET /api/export/template.csv":               "user",
	"POST /api/import/monitors":                  "user",

	// Admin only.
	"GET /api/system":                   "admin",
	"POST /api/channels":                "admin",
	"PUT /api/channels/{id}":            "admin",
	"DELETE /api/channels/{id}":         "admin",
	"POST /api/channels/{id}/test":      "admin",
	"POST /api/channels/{id}/apply-all": "admin",
	"GET /api/users":                    "admin",
	"PUT /api/users/{id}":               "admin",
	"POST /api/users":                   "admin",
	"DELETE /api/users/{id}":            "admin",
	"DELETE /api/users/{id}/2fa":        "admin",
	"GET /api/settings":                 "admin",
	"PUT /api/settings":                 "admin",
	"GET /api/alert-log":                "admin",
	"POST /api/alert-log/{id}/resend":   "admin",
	"GET /api/regions":                  "admin",
	"POST /api/regions/check":           "admin",
	"PUT /api/regions/token":            "admin",
	"GET /api/data/usage":               "admin",
	"POST /api/data/clear":              "admin",
	"POST /api/data/compact":            "admin",
	"GET /api/audit-log":                "admin",
	"GET /api/backups":                  "admin",
	"POST /api/backups":                 "admin",
	"POST /api/backups/upload":          "admin",
	"GET /api/backups/{name}/download":  "admin",
	"DELETE /api/backups/{name}":        "admin",
	"POST /api/backups/{name}/restore":  "admin",
	"GET /api/updates":                  "admin",
	"PUT /api/updates/settings":         "admin",
	"POST /api/updates/check":           "admin",
	"POST /api/updates/install":         "admin",
	"POST /api/updates/rollback":        "admin",
}

// neverRunAllowed are routes with real side effects (or that never finish)
// even for an empty request: the matrix checks they are refused below their
// minimum role, and does not call them as an allowed role.
var neverRunAllowed = map[string]bool{
	"GET /api/stream":                  true,
	"PUT /api/settings":                true,
	"POST /api/data/clear":             true,
	"POST /api/data/compact":           true,
	"POST /api/backups":                true,
	"POST /api/backups/upload":         true,
	"POST /api/backups/{name}/restore": true,
	"DELETE /api/backups/{name}":       true,
	"POST /api/regions/check":          true,
	"PUT /api/regions/token":           true,
	"PUT /api/updates/settings":        true,
	"POST /api/updates/check":          true,
	"POST /api/updates/install":        true,
	"POST /api/updates/rollback":       true,
	"POST /api/auth/2fa/enroll":        true,
	"POST /api/auth/password":          true,
	"POST /api/auth/2fa/disable":       true,
	"POST /api/auth/2fa/recovery":      true,
	"POST /api/channels/{id}/test":     true,
}

func fillPath(pattern string) (method, path string) {
	method, path, _ = strings.Cut(pattern, " ")
	for _, p := range []string{"{id}", "{noteId}", "{updateId}", "{sid}", "{name}", "{token}", "{slug}"} {
		path = strings.ReplaceAll(path, p, "999999")
	}
	return method, path
}

// TestRoutePermissionMatrix checks (1) every registered route is classified
// here with the same minimum role the server registered it with, and no
// classified route is missing, and (2) role x route: a role below the minimum
// gets 401 (signed out) or 403, a role at or above it is not refused.
func TestRoutePermissionMatrix(t *testing.T) {
	env := newTestEnv(t)

	registered := map[string]string{}
	for _, p := range env.policy {
		min := p.Min
		if min == rolePublic {
			min = "public"
		}
		registered[p.Pattern] = min
	}
	var problems []string
	for pattern, got := range registered {
		want, ok := routeMatrix[pattern]
		switch {
		case !ok:
			problems = append(problems, "route not classified in routeMatrix: "+pattern)
		case want != got:
			problems = append(problems, pattern+": registered "+got+", matrix says "+want)
		}
	}
	for pattern := range routeMatrix {
		if _, ok := registered[pattern]; !ok {
			problems = append(problems, "matrix entry with no route: "+pattern)
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
	if t.Failed() {
		return
	}

	var patterns []string
	for pattern, min := range routeMatrix {
		if min != "public" {
			patterns = append(patterns, pattern)
		}
	}
	sort.Strings(patterns)

	rank := map[string]int{"": 0, "viewer": 1, "user": 2, "admin": 3}
	for _, pattern := range patterns {
		min := routeMatrix[pattern]
		method, path := fillPath(pattern)
		var body any
		if method == http.MethodPost || method == http.MethodPut {
			body = map[string]any{}
		}
		for _, as := range []string{"", "viewer", "user", "admin"} {
			allowed := rank[as] >= rank[min]
			if allowed && neverRunAllowed[pattern] {
				continue
			}
			resp := env.do(method, path, body, as)
			switch {
			case as == "":
				if resp.status != http.StatusUnauthorized {
					t.Errorf("%s as anonymous: status %d, want 401", pattern, resp.status)
				}
			case !allowed:
				if resp.status != http.StatusForbidden {
					t.Errorf("%s as %s: status %d, want 403", pattern, as, resp.status)
				}
			default:
				if resp.status == http.StatusUnauthorized || resp.status == http.StatusForbidden {
					t.Errorf("%s as %s: refused with %d, want it allowed (%s)", pattern, as, resp.status, resp.body)
				}
			}
		}
	}
}

func TestRoleHelpers(t *testing.T) {
	cases := []struct {
		role, min string
		want      bool
	}{
		{"admin", "admin", true}, {"admin", "user", true}, {"admin", "viewer", true},
		{"user", "admin", false}, {"user", "user", true}, {"user", "viewer", true},
		{"viewer", "admin", false}, {"viewer", "user", false}, {"viewer", "viewer", true},
		{"disabled", "viewer", false}, {"", "viewer", false}, {"bogus", "viewer", false},
		{"admin", "bogus", false},
	}
	for _, c := range cases {
		if got := auth.AtLeast(c.role, c.min); got != c.want {
			t.Errorf("AtLeast(%q, %q) = %v, want %v", c.role, c.min, got, c.want)
		}
	}
	for _, r := range []string{"admin", "user", "viewer"} {
		if !auth.ValidRole(r) || !auth.ValidAssignableRole(r) {
			t.Errorf("%q should be valid", r)
		}
	}
	if auth.ValidRole("disabled") || !auth.ValidAssignableRole("disabled") {
		t.Error("disabled is assignable but not a signing-in role")
	}
	if auth.ValidAssignableRole("owner") || auth.ValidAssignableRole("") {
		t.Error("unknown roles must be rejected")
	}
	if auth.CanWrite("viewer") || !auth.CanWrite("user") || !auth.CanWrite("admin") {
		t.Error("CanWrite: viewer false, user and admin true")
	}
}

// A user operates the workspace for real; a viewer cannot.
func TestUserRoleOperates(t *testing.T) {
	env := newTestEnv(t)

	resp := env.do(http.MethodPost, "/api/monitors", httpMonitorIn("web", "http://example.invalid"), "user")
	if resp.status != http.StatusCreated {
		t.Fatalf("user create monitor = %d: %s", resp.status, resp.body)
	}
	if resp := env.do(http.MethodPost, "/api/monitors", httpMonitorIn("web2", "http://example.invalid"), "viewer"); resp.status != http.StatusForbidden {
		t.Fatalf("viewer create monitor = %d, want 403", resp.status)
	}
	resp = env.do(http.MethodPost, "/api/tags", map[string]any{"name": "prod", "color": "#112233"}, "user")
	if resp.status != http.StatusCreated && resp.status != http.StatusOK {
		t.Fatalf("user create tag = %d: %s", resp.status, resp.body)
	}
}

func TestChannelListHidesConfigBelowAdmin(t *testing.T) {
	env := newTestEnv(t)
	const secretURL = "https://hooks.example.invalid/services/T000/B000/SUPERSECRET"
	resp := env.do(http.MethodPost, "/api/channels", map[string]any{
		"name": "ops", "type": "webhook", "config": map[string]any{"url": secretURL},
	}, "admin")
	if resp.status != http.StatusCreated {
		t.Fatalf("admin create channel = %d: %s", resp.status, resp.body)
	}

	for _, as := range []string{"user", "viewer"} {
		resp := env.do(http.MethodGet, "/api/channels", nil, as)
		if resp.status != http.StatusOK {
			t.Fatalf("%s list channels = %d", as, resp.status)
		}
		body := string(resp.body)
		if strings.Contains(body, "SUPERSECRET") || strings.Contains(body, "config") || strings.Contains(body, "isSet") {
			t.Errorf("%s sees channel configuration: %s", as, body)
		}
		var out struct {
			Channels []map[string]any `json:"channels"`
		}
		resp.json(t, &out)
		if len(out.Channels) != 1 || out.Channels[0]["name"] != "ops" || out.Channels[0]["type"] != "webhook" || out.Channels[0]["id"] == nil {
			t.Errorf("%s channel refs = %v", as, out.Channels)
		}
	}
	resp = env.do(http.MethodGet, "/api/channels", nil, "admin")
	if !strings.Contains(string(resp.body), "config") {
		t.Errorf("admin should still see the masked config: %s", resp.body)
	}
	if strings.Contains(string(resp.body), "SUPERSECRET") {
		t.Errorf("admin response leaks the secret URL: %s", resp.body)
	}
}

func TestUserAdministrationRoles(t *testing.T) {
	env := newTestEnv(t)

	// Role validation on create.
	for _, role := range []string{"owner", "root", "ADMIN"} {
		resp := env.do(http.MethodPost, "/api/users", map[string]any{"username": "bad" + strings.ToLower(role), "password": "correct horse battery 1", "role": role}, "admin")
		if resp.status != http.StatusBadRequest {
			t.Errorf("create with role %q = %d, want 400", role, resp.status)
		}
	}
	for _, role := range []string{"admin", "user", "viewer"} {
		resp := env.do(http.MethodPost, "/api/users", map[string]any{"username": "new" + role, "password": "correct horse battery 1", "role": role}, "admin")
		if resp.status != http.StatusCreated {
			t.Fatalf("create with role %q = %d: %s", role, resp.status, resp.body)
		}
		var out struct {
			Role string `json:"role"`
		}
		resp.json(t, &out)
		if out.Role != role {
			t.Errorf("created role = %q, want %q", out.Role, role)
		}
	}

	// Only admins change roles; unknown roles are rejected.
	path := "/api/users/" + itoa64(env.viewerID)
	if resp := env.do(http.MethodPut, path, map[string]any{"role": "user"}, "user"); resp.status != http.StatusForbidden {
		t.Errorf("user changing a role = %d, want 403", resp.status)
	}
	if resp := env.do(http.MethodPut, path, map[string]any{"role": "superuser"}, "admin"); resp.status != http.StatusBadRequest {
		t.Errorf("unknown role = %d, want 400", resp.status)
	}
	if resp := env.do(http.MethodPut, path, map[string]any{"role": "user"}, "admin"); resp.status != http.StatusOK {
		t.Fatalf("promote viewer to user = %d: %s", resp.status, resp.body)
	}
	if u, err := env.store.GetUser(t.Context(), env.viewerID); err != nil || u.Role != store.RoleUser {
		t.Fatalf("viewer after promotion = %+v, %v", u, err)
	}
	// The role is read from the store on every request, so the existing
	// session gains write access at once.
	if resp := env.do(http.MethodPost, "/api/tags", map[string]any{"name": "x", "color": "#112233"}, "viewer"); resp.status == http.StatusForbidden {
		t.Errorf("promoted account still refused: %d", resp.status)
	}

	// Nobody changes their own role or removes themselves, the last admin included.
	own := "/api/users/" + itoa64(env.adminID)
	if resp := env.do(http.MethodPut, own, map[string]any{"role": "user"}, "admin"); resp.status != http.StatusConflict {
		t.Errorf("admin demoting self = %d, want 409", resp.status)
	}
	if resp := env.do(http.MethodDelete, own, nil, "admin"); resp.status != http.StatusConflict {
		t.Errorf("admin deleting self = %d, want 409", resp.status)
	}
}

func TestLastAdminProtectedInStore(t *testing.T) {
	env := newTestEnv(t)
	ctx := t.Context()
	// env has exactly one admin.
	for _, role := range []string{store.RoleUser, store.RoleViewer, store.RoleDisabled} {
		if err := env.store.SetUserRole(ctx, env.adminID, role); err == nil {
			t.Errorf("demoting the last admin to %q succeeded", role)
		}
	}
	if err := env.store.DeleteUser(ctx, env.adminID); err == nil {
		t.Error("deleting the last admin succeeded")
	}
	// With a second admin the first can be demoted, then the second is last.
	second, err := env.store.CreateUser(ctx, "second@example.com", "Second", store.RoleAdmin, env.now())
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.SetUserRole(ctx, env.adminID, store.RoleUser); err != nil {
		t.Fatalf("demote with two admins: %v", err)
	}
	if err := env.store.SetUserRole(ctx, second.ID, store.RoleUser); err == nil {
		t.Error("demoting the now-last admin succeeded")
	}
}
