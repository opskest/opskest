import type { Role } from './types'

// The role model on the client. Mirrors internal/auth/roles.go and
// Docs.md, "Roles": admin > user > viewer. The server is the authority (it
// answers 403); these helpers only decide what to show.
//
//   admin  - everything, including Settings, team, channels, backups.
//   user   - operates monitors, incidents, maintenance and status pages.
//   viewer - read-only; may still acknowledge outages and add notes.

type HasRole = { role: Role } | Role | null | undefined

function roleOf(who: HasRole): Role | undefined {
  return typeof who === 'string' ? who : who?.role
}

/** Admin: Settings, team and roles, alert channels, backups, updates. */
export function isAdmin(who: HasRole): boolean {
  return roleOf(who) === 'admin'
}

/** Admin or user: create, edit, delete and pause monitors, incidents, maintenance, pages. */
export function canWrite(who: HasRole): boolean {
  const r = roleOf(who)
  return r === 'admin' || r === 'user'
}

/** Role names as shown in the interface. */
export const ROLE_LABEL: Record<Role, string> = {
  admin: 'Admin',
  user: 'User',
  viewer: 'Viewer',
  disabled: 'Disabled',
}

/** One-line explanation of each role, for the role pickers. */
export const ROLE_HELP: Record<Role, string> = {
  admin: 'Full control: settings, team, channels, backups',
  user: 'Manage monitors, incidents and status pages',
  viewer: 'Read-only; can acknowledge and annotate outages',
  disabled: 'Cannot sign in; existing sessions ended',
}
