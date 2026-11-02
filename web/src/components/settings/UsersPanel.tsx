import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { api, errorText, unwrapList, ApiError } from '../../lib/api'
import { dateServer } from '../../lib/format'
import type { AuditEntry, Me, Role, User } from '../../lib/types'
import { Pager, usePaging } from '../Pager'
import { Button, ConfirmModal, EmptyState, ErrorRetry, Field, Timestamp, Input, Modal, Select, SkeletonRows, Badge, Icon, useToast } from '../ui'
import { FieldGrid } from '../form/primitives'
import { DANGER_GHOST, RowActions, SettingsActions, SettingsLoading, SettingsSection, SettingsStats, SettingsTable, ST_BODY, ST_HEAD, ST_ROW, ST_TABLE, ST_TD, ST_TH } from './SettingsPanel'

// Settings → Security & Team. Administrators create local accounts.
// anyone on an allowed domain gets Viewer on first sign-in. Admins can
// pre-add people with a role, change roles, disable (signs them out
// everywhere) and remove accounts. The last admin and your own account
// are protected server-side.

const ROLE_HELP: Record<Role, string> = {
  admin: 'Full control: settings, team, channels and backups',
  user: 'Manage monitors, incidents and status pages',
  viewer: 'Read-only; can acknowledge and annotate outages',
  disabled: 'Cannot sign in; existing sessions ended',
}

function initials(u: User): string {
  const src = u.name || (u.username || u.email)
  return src.split(/[\s@.]+/).filter(Boolean).slice(0, 2).map(p => p[0]!.toUpperCase()).join('')
}

function nextBeforeOf(d: unknown): number | null {
  const nb = d && typeof d === 'object' ? (d as { nextBefore?: unknown }).nextBefore : null
  return typeof nb === 'number' ? nb : null
}

export function UsersPanel({ me, onLoaded }: { me: Me; onLoaded?: (users: User[]) => void }) {
  const toast = useToast()
  const [users, setUsers] = useState<User[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [name, setName] = useState('')
  const [role, setRole] = useState<Role>('viewer')
  const [fieldErr, setFieldErr] = useState<Record<string, string>>({})
  const [adding, setAdding] = useState(false)
  const [addOpen, setAddOpen] = useState(false)
  const [removeTarget, setRemoveTarget] = useState<User | null>(null)
  const [resetTarget, setResetTarget] = useState<User | null>(null)
  const [audit, setAudit] = useState<AuditEntry[] | null>(null)

  const load = useCallback(() => {
    setError(null)
    api.get<unknown>('/api/users')
      .then(d => { const list = unwrapList<User>(d, 'users'); setUsers(list); onLoaded?.(list) })
      .catch(e => setError(errorText(e, 'Could not load users')))
    api.get<unknown>('/api/audit-log?limit=50')
      .then(d => { setAudit(unwrapList<AuditEntry>(d, 'entries')); setAuditBefore(nextBeforeOf(d)) })
      .catch(() => { setAudit([]); setAuditBefore(null) })
  }, [onLoaded])
  useEffect(load, [load])

  // The audit log pages by a `before` cursor; what is loaded is paged 25 at a
  // time and "Load older" appends the next 50.
  const [auditBefore, setAuditBefore] = useState<number | null>(null)
  const [auditBusy, setAuditBusy] = useState(false)
  async function loadOlderAudit() {
    if (auditBefore == null) return
    setAuditBusy(true)
    try {
      const d = await api.get<unknown>(`/api/audit-log?limit=50&before=${auditBefore}`)
      setAudit(cur => {
        const have = new Set((cur ?? []).map(a => a.id))
        return [...(cur ?? []), ...unwrapList<AuditEntry>(d, 'entries').filter(a => !have.has(a.id))]
      })
      setAuditBefore(nextBeforeOf(d))
    } catch (e) {
      toast.push(errorText(e, 'Could not load older activity'), 'danger')
    } finally { setAuditBusy(false) }
  }
  const userPaging = usePaging(users ?? [])
  const auditPaging = usePaging(audit ?? [])

  const adminCount = users?.filter(u => u.role === 'admin').length ?? 0

  async function addUser(e: FormEvent) {
    e.preventDefault()
    setFieldErr({})
    setAdding(true)
    try {
      await api.post('/api/users', { username, password, name, role })
      toast.push(`Added ${username}`, 'ok')
      setUsername(''); setPassword(''); setName(''); setRole('viewer')
      setAddOpen(false)
      load()
    } catch (err) {
      if (err instanceof ApiError && err.fields) setFieldErr(err.fields)
      else toast.push(errorText(err, 'Could not add user'), 'danger')
    } finally {
      setAdding(false)
    }
  }

  async function changeRole(u: User, next: Role) {
    try {
      await api.put(`/api/users/${u.id}`, { role: next })
      toast.push(`${(u.username || u.email)} is now ${next}`, 'ok')
      load()
    } catch (err) {
      toast.push(errorText(err, 'Could not change role'), 'danger')
      load()
    }
  }

  async function remove(u: User) {
    try {
      await api.del(`/api/users/${u.id}`)
      toast.push(`Removed ${(u.username || u.email)}`, 'ok')
      load()
    } catch (err) {
      toast.push(errorText(err, 'Could not remove user'), 'danger')
    } finally {
      setRemoveTarget(null)
    }
  }

  async function resetTwoFactor(u: User) {
    try {
      await api.del(`/api/users/${u.id}/2fa`)
      toast.push(`Two-factor reset for ${(u.username || u.email)}`, 'ok')
      load()
    } catch (err) {
      toast.push(errorText(err, 'Could not reset two-factor'), 'danger')
    } finally {
      setResetTarget(null)
    }
  }

  const addAction = (
    <SettingsActions>
      <Button variant="primary" icon={<Icon.Plus className="w-4 h-4" />} onClick={() => { setFieldErr({}); setAddOpen(true) }}>Add user</Button>
    </SettingsActions>
  )
  if (error) return <>{addAction}<SettingsSection><ErrorRetry message={error} onRetry={load} /></SettingsSection></>
  if (!users) return <>{addAction}<SettingsLoading label="Loading users" sections={2} rows={3} /></>
  const count = (r: Role) => users.filter(u => u.role === r).length

  return (
    <>
      {addAction}

      <Modal
        open={addOpen}
        onClose={() => { if (!adding) setAddOpen(false) }}
        title="Add a teammate"
        description="They sign in with this username and password, and can change it and enable two-factor authentication in their account settings."
        size="lg"
        footer={<>
          <Button type="button" variant="secondary" onClick={() => setAddOpen(false)} disabled={adding}>Cancel</Button>
          <Button type="submit" form="add-user-form" variant="primary" busy={adding}>Add user</Button>
        </>}
      >
        <form id="add-user-form" onSubmit={addUser}>
          <FieldGrid>
          <Field label="Username" error={fieldErr.username} hint="Used to sign in.">{id => <Input id={id} autoComplete="off" required autoFocus value={username} onChange={e => setUsername(e.target.value)} placeholder="alex" />}</Field>
          <Field label="Initial password" error={fieldErr.password} hint="At least 12 characters">{id => <Input id={id} type="password" autoComplete="new-password" required minLength={12} value={password} onChange={e => setPassword(e.target.value)} />}</Field>
          <Field label="Name" error={fieldErr.name} hint="Optional. Shown instead of the username.">
            {id => <Input id={id} value={name} onChange={e => setName(e.target.value)} placeholder="Alex Doe" />}
          </Field>
          <Field label="Role" error={fieldErr.role} hint={ROLE_HELP[role]}>
            {id => <Select id={id} value={role} onChange={e => setRole(e.target.value as Role)}>
              <option value="viewer">Viewer</option>
              <option value="user">User</option>
              <option value="admin">Admin</option>
            </Select>}
          </Field>
          </FieldGrid>
        </form>
      </Modal>

      <SettingsSection title="Team members" description="Everyone who can sign in, their role and last activity.">
      <SettingsStats items={[
        { label: 'Admins', value: count('admin'), hint: 'Full access' },
        { label: 'Users', value: count('user'), hint: 'Operate monitoring' },
        { label: 'Viewers', value: count('viewer'), hint: 'Read and acknowledge' },
        { label: 'Disabled', value: count('disabled'), hint: 'Cannot sign in' },
      ]} />
      <SettingsTable scroll>
        <table className={`${ST_TABLE} min-w-[760px]`}>
          <thead className={ST_HEAD}>
            <tr>
              <th className={ST_TH}>User</th>
              <th className={ST_TH}>Role</th>
              <th className={ST_TH}>Two-factor</th>
              <th className={ST_TH}>Last sign-in</th>
              <th className={ST_TH}>Added</th>
              <th className={`${ST_TH} text-right`}><span className="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody className={ST_BODY}>
            {userPaging.rows.map(u => {
              const isMe = u.id === me.id
              const lastAdmin = u.role === 'admin' && adminCount <= 1
              const locked = isMe || lastAdmin
              return (
                <tr key={u.id} className={ST_ROW}>
                  <td className={ST_TD}>
                    <div className="flex items-center gap-2.5">
                      {u.avatarUrl
                        ? <img src={u.avatarUrl} alt="" className="w-7 h-7 rounded-full" referrerPolicy="no-referrer" />
                        : <span className="w-7 h-7 rounded-full bg-surface-2 text-muted flex items-center justify-center text-[11px] font-semibold">{initials(u)}</span>}
                      <div className="min-w-0">
                        <div className="font-medium text-body truncate">{u.name || (u.username || u.email).split('@')[0]}{isMe && <span className="ml-1.5"><Badge tone="accent">you</Badge></span>}</div>
                        <div className="truncate font-mono text-xs text-muted">{(u.username || u.email)}</div>
                      </div>
                    </div>
                  </td>
                  <td className={ST_TD}>
                    <Select
                      aria-label={`Role for ${(u.username || u.email)}`}
                      value={u.role}
                      disabled={locked}
                      title={isMe ? 'You cannot change your own role' : lastAdmin ? 'The last admin cannot be demoted' : ROLE_HELP[u.role]}
                      onChange={e => changeRole(u, e.target.value as Role)}
                      dense className="w-32"
                    >
                      <option value="admin">Admin</option>
                      <option value="user">User</option>
                      <option value="viewer">Viewer</option>
                      <option value="disabled">Disabled</option>
                    </Select>
                  </td>
                  <td className={ST_TD}><Badge tone={u.twoFactorEnabled ? 'ok' : 'neutral'}>{u.twoFactorEnabled ? 'On' : 'Off'}</Badge></td>
                  <td className={`${ST_TD} text-muted`}>{u.lastLoginAt ? <Timestamp at={u.lastLoginAt} future={false} /> : <span className="text-faint">never</span>}</td>
                  <td className={`${ST_TD} text-faint`}>{dateServer(u.createdAt)}</td>
                  <td className={`${ST_TD} text-right`}>
                    <RowActions>
                      {u.twoFactorEnabled && !isMe && (
                        <Button size="sm" variant="ghost" aria-label={`Reset two-factor for ${(u.username || u.email)}`}
                          title="Remove their authenticator so they can set it up again" onClick={() => setResetTarget(u)}>Reset two-factor</Button>
                      )}
                      <Button size="sm" variant="ghost" className={DANGER_GHOST} disabled={locked} aria-label={`Remove ${(u.username || u.email)}`}
                        title={locked ? 'Protected account' : 'Remove user'} onClick={() => setRemoveTarget(u)}>Remove</Button>
                    </RowActions>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </SettingsTable>
      <Pager paging={userPaging} noun="Users" />
      </SettingsSection>

      <SettingsSection title="Recent activity" description="Who changed what (monitors, channels, pages, users, restores).">
      {audit && audit.length === 0 ? (
        <EmptyState compact title="No activity recorded yet">Changes to monitors, channels, pages and users appear here.</EmptyState>
      ) : (
        <SettingsTable scroll>
          <table className={`${ST_TABLE} min-w-[560px]`}>
            <thead className={ST_HEAD}>
              <tr><th className={ST_TH}>When</th><th className={ST_TH}>Who</th><th className={ST_TH}>Action</th><th className={ST_TH}>Detail</th></tr>
            </thead>
            <tbody className={ST_BODY}>
              {!audit ? (
                <tr><td colSpan={4} className="p-3"><SkeletonRows rows={3} framed={false} label="Loading activity" /></td></tr>
              ) : auditPaging.rows.map(a => (
                <tr key={a.id} className={ST_ROW}>
                  <td className={`${ST_TD} text-faint whitespace-nowrap`}><Timestamp at={a.ts} future={false} /></td>
                  <td className={`${ST_TD} text-muted font-mono`}>{a.userEmail || 'system'}</td>
                  <td className={`${ST_TD} text-body`}>{a.action} {a.entity}{a.entityId ? ` #${a.entityId}` : ''}</td>
                  <td className={`${ST_TD} text-muted font-mono truncate max-w-xs`} title={a.detail}>{a.detail || '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </SettingsTable>
      )}
      {audit && audit.length > 0 && <Pager paging={auditPaging} noun="Entries" hasMore={auditBefore != null} onLoadMore={loadOlderAudit} loadingMore={auditBusy} />}
      </SettingsSection>


      <ConfirmModal
        open={resetTarget !== null}
        title="Reset two-factor?"
        tone="primary"
        description={resetTarget && <>This removes the authenticator app and recovery codes of <span className="font-mono">{resetTarget.username || resetTarget.email}</span>, for example after a lost phone. They will be asked to set it up again the next time they sign in if your workspace requires two-factor; otherwise they can do it from Account security.</>}
        confirmLabel="Reset two-factor"
        onConfirm={() => (resetTarget ? resetTwoFactor(resetTarget) : undefined)}
        onClose={() => setResetTarget(null)}
      />

      <ConfirmModal
        open={removeTarget !== null}
        title="Remove user?"
        description={removeTarget && <><span className="font-mono">{removeTarget.username || removeTarget.email}</span> loses access and is signed out. An administrator must create a new account to restore their access.</>}
        confirmLabel="Remove"
        onConfirm={() => (removeTarget ? remove(removeTarget) : undefined)}
        onClose={() => setRemoveTarget(null)}
      />
    </>
  )
}
