package api

import "opskest/internal/auth"

// The permission table. Roles rank admin > user > viewer (internal/auth/roles.go);
// every route is registered through server.go's reg(pattern, min, handler)
// with the minimum role it needs, or through pub() when it is public and does
// its own gating. Docs.md, "Roles" has the matrix and TestRoutePermissionMatrix
// classifies every route independently, so a new route that is not in that
// table fails the build.
const (
	// rolePublic: no session required (the handler gates itself, e.g. 404).
	rolePublic = ""
	// roleViewer: any signed-in role. Reads, plus the annotation exceptions
	// (acknowledge an outage; add, edit, delete one's own outage notes).
	roleViewer = auth.RoleViewer
	// roleUser: operational writes, and reads of operator-only data
	// (status page editor, subscribers, import/export).
	roleUser = auth.RoleUser
	// roleAdmin: workspace settings, team, channels, backups, storage,
	// updates, regions config, audit and delivery logs.
	roleAdmin = auth.RoleAdmin
)

type routePolicy struct {
	Pattern string
	Min     string
}
