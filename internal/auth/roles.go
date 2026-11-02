package auth

import (
	"net/http"

	"opskest/internal/store"
)

// The role model, defined once. Roles are ordered admin > user > viewer;
// "disabled" is not a role that can sign in and ranks below everything.
//
//	admin  - everything, including workspace settings, team, channels,
//	         backups, storage, updates, regions config, audit and delivery logs.
//	user   - the day-to-day operator: monitors, incidents, maintenance, status
//	         pages, templates, projects, tags, import/export. Sees alert
//	         channels by id, name and type only.
//	viewer - read-only, except that a viewer may annotate (acknowledge
//	         outages and add/edit/delete their own outage notes).
//
// Every API route is registered with the minimum role it needs (see
// internal/api/permissions.go) and the table test in internal/api classifies
// every route. Docs.md, "Roles" has the matrix.
const (
	RoleAdmin  = store.RoleAdmin
	RoleUser   = store.RoleUser
	RoleViewer = store.RoleViewer
)

// ValidRole reports whether role is one a person can be assigned to. The
// administrator-only "disabled" state is separate: see ValidAssignableRole.
func ValidRole(role string) bool {
	return role == RoleAdmin || role == RoleUser || role == RoleViewer
}

// ValidAssignableRole is ValidRole plus "disabled" (an account that is kept
// but cannot sign in). It is what the users API accepts.
func ValidAssignableRole(role string) bool {
	return ValidRole(role) || role == store.RoleDisabled
}

// Rank orders roles for comparison: admin 3, user 2, viewer 1, anything else 0.
func Rank(role string) int {
	switch role {
	case RoleAdmin:
		return 3
	case RoleUser:
		return 2
	case RoleViewer:
		return 1
	}
	return 0
}

// AtLeast reports whether role meets the minimum role.
func AtLeast(role, minimum string) bool {
	return Rank(minimum) > 0 && Rank(role) >= Rank(minimum)
}

// CanWrite reports whether role may change operational data (user or admin).
func CanWrite(role string) bool { return AtLeast(role, RoleUser) }

// RequireRole answers 401 {"error":"unauthenticated"} with no session and
// 403 {"error":"forbidden"} for a signed-in role below minimum.
func RequireRole(minimum string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, ok := UserFrom(r.Context())
			if !ok {
				writeJSONError(w, http.StatusUnauthorized, "unauthenticated")
				return
			}
			if !AtLeast(u.Role, minimum) {
				msg := "forbidden"
				if minimum == RoleAdmin {
					msg = "admin only"
				}
				writeJSONError(w, http.StatusForbidden, msg)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
