// /incidents/:id: the timeline of updates (plain text,
// links auto-linked), and for admins a "Post update" form (status + body),
// Resolve, edit title/impact, and delete. Admins can also edit any timeline
// entry in place (its text, and whether it is a team-only note); an edited
// entry is marked "(edited)".

import { useEffect, useMemo, useState } from 'react'
import { Link, useLocation } from 'wouter'
import { PageTitle } from '../components/PageShell'
import { ShowMoreButton, useShowMore } from '../components/Pager'
import { IMPACT_LABEL, impactTone, INCIDENT_STATUS, Linkified } from '../components/status/IncidentCards'
import { Alert, Badge, Button, Checkbox, ConfirmModal, DropdownMenu, EmptyState, ErrorRetry, Field, Icon, Input, Modal, SegmentedControl, Select, SkeletonBlock, Textarea, Timestamp, modKey, useToast } from '../components/ui'
import { api, ApiError, errorText } from '../lib/api'
import { dateServer, duration } from '../lib/format'
import { useLive } from '../lib/live'
import { normIncident } from '../lib/normalize'
import { canWrite } from '../lib/roles'
import type { Incident, IncidentStatus, Me } from '../lib/types'
import { SEVERITIES, severityLabel, severityTone, useTeam } from '../hooks/useTeam'

const STATUSES: IncidentStatus[] = ['investigating', 'identified', 'monitoring', 'resolved']
const TONE: Record<IncidentStatus, 'warn' | 'accent' | 'ok'> = { investigating: 'warn', identified: 'warn', monitoring: 'accent', resolved: 'ok' }

function EditModal({ incident, onClose, onSaved }: { incident: Incident; onClose: () => void; onSaved: () => void }) {
  const [title, setTitle] = useState(incident.title)
  const [impact, setImpact] = useState(incident.impact || 'minor')
  const [severity, setSeverity] = useState(incident.severity || 'minor')
  const [commanderId, setCommanderId] = useState<number | null>(incident.commanderId)
  const team = useTeam(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  async function save() {
    if (!title.trim()) { setError('Title is required.'); return }
    setBusy(true)
    try {
      await api.put(`/api/incidents/${incident.id}`, { title: title.trim(), impact, severity, commanderId: commanderId ?? 0, monitorIds: incident.monitorIds, pageIds: incident.pageIds })
      onSaved()
      onClose()
    } catch (err) {
      setError(errorText(err, 'Could not save.'))
      setBusy(false)
    }
  }
  return (
    <Modal open onClose={onClose} title="Edit incident" size="lg" footer={<><Button variant="secondary" onClick={onClose}>Cancel</Button><Button variant="primary" busy={busy} onClick={save}>Save</Button></>}>
      <div className="flex flex-col gap-5">
        {error && <Alert tone="danger">{error}</Alert>}
        <div className="grid grid-cols-1 items-start gap-x-5 gap-y-5 sm:grid-cols-2">
          <Field label="Title">{id => <Input id={id} value={title} onChange={e => setTitle(e.target.value)} maxLength={200} />}</Field>
          <Field label="Owner">
            {id => (
              <Select id={id} value={commanderId ?? ''} onChange={e => setCommanderId(e.target.value ? Number(e.target.value) : null)}>
                <option value="">No owner</option>
                {team.map(m => <option key={m.id} value={m.id}>{m.label}</option>)}
              </Select>
            )}
          </Field>
          <Field label="Impact">
            {id => (
              <Select id={id} value={impact} onChange={e => setImpact(e.target.value)}>
                <option value="minor">Minor</option><option value="major">Major</option><option value="maintenance">Maintenance</option>
              </Select>
            )}
          </Field>
          <Field label="Severity" hint="Internal only.">
            {id => (
              <Select id={id} value={severity} onChange={e => setSeverity(e.target.value)}>
                {SEVERITIES.map(s => <option key={s.id} value={s.id}>{s.label}</option>)}
              </Select>
            )}
          </Field>
        </div>
      </div>
    </Modal>
  )
}

export default function IncidentDetail({ id: idParam, user }: { id: string; user: Me }) {
  const id = Number(idParam)
  const admin = canWrite(user) // admin or user
  const live = useLive()
  const { push } = useToast()
  const [, navigate] = useLocation()

  const [incident, setIncident] = useState<Incident | null>(null)
  const [state, setState] = useState<'loading' | 'ready' | 'error' | 'notfound'>('loading')
  const [errorMsg, setErrorMsg] = useState('')
  const [nonce, setNonce] = useState(0)
  const [status, setStatus] = useState<IncidentStatus>('identified')
  const [body, setBody] = useState('')
  const [busy, setBusy] = useState<'update' | 'resolve' | null>(null)
  const [formError, setFormError] = useState<string | null>(null)
  const [editing, setEditing] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [mode, setMode] = useState<'public' | 'note'>('public')
  const [pm, setPm] = useState('')
  const [pmBusy, setPmBusy] = useState(false)
  // The timeline entry being edited in place.
  const [editUpdate, setEditUpdate] = useState<{ id: number; body: string; internal: boolean } | null>(null)
  const [editBusy, setEditBusy] = useState(false)
  const [editError, setEditError] = useState<string | null>(null)

  useEffect(() => {
    if (!Number.isFinite(id)) { setState('notfound'); return }
    let alive = true
    api.get<unknown>(`/api/incidents/${id}`)
      .then(raw => {
        if (!alive) return
        const inc = normIncident(raw)
        setIncident(inc)
        setPm(inc.postmortem)
        setState('ready')
        const next = STATUSES[Math.min(STATUSES.indexOf(inc.status) + 1, 2)]
        setStatus(inc.status === 'resolved' ? 'monitoring' : next ?? 'identified')
      })
      .catch(err => {
        if (!alive) return
        if (err instanceof ApiError && err.status === 404) setState('notfound')
        else { setErrorMsg(errorText(err, 'Failed to load this incident')); setState('error') }
      })
    return () => { alive = false }
  }, [id, nonce])

  const monitorLinks = useMemo(() => incident?.monitorIds.map(mid => ({ id: mid, name: live.monitors.get(mid)?.name })) ?? [], [incident, live.monitors])

  const timeline = useMemo(() => [...(incident?.updates ?? [])].sort((a, b) => b.createdAt - a.createdAt), [incident])
  const timelineMore = useShowMore(timeline, 10, 10, id)
  const linksMore = useShowMore(monitorLinks, 12, 12)

  async function post(next: IncidentStatus, internal = false) {
    if (!body.trim()) { setFormError(internal ? 'Write the note first.' : 'Write what changed first.'); return }
    setBusy(next === 'resolved' ? 'resolve' : 'update')
    setFormError(null)
    try {
      await api.post(`/api/incidents/${id}/updates`, { status: next, body: body.trim(), internal })
      setBody('')
      setNonce(n => n + 1)
      push(internal ? 'Note added.' : next === 'resolved' ? 'Incident resolved.' : 'Update posted.', 'ok')
    } catch (err) {
      setFormError(errorText(err, 'Could not post that update.'))
    } finally {
      setBusy(null)
    }
  }

  async function saveUpdateEdit() {
    if (!editUpdate) return
    const body = editUpdate.body.trim()
    if (editUpdate.internal && !body) { setEditError('A team note needs text.'); return }
    setEditBusy(true)
    setEditError(null)
    try {
      await api.put(`/api/incidents/${id}/updates/${editUpdate.id}`, { body, internal: editUpdate.internal })
      setEditUpdate(null)
      setNonce(n => n + 1)
      push('Update saved.', 'ok')
    } catch (err) {
      setEditError(errorText(err, 'Could not save that update.'))
    } finally {
      setEditBusy(false)
    }
  }

  async function savePostmortem() {
    setPmBusy(true)
    try {
      await api.put(`/api/incidents/${id}/postmortem`, { postmortem: pm })
      push('Postmortem saved.', 'ok')
      setNonce(n => n + 1)
    } catch (err) {
      push(errorText(err, 'Could not save the postmortem.'), 'danger')
    } finally {
      setPmBusy(false)
    }
  }

  async function remove() {
    try {
      await api.del(`/api/incidents/${id}`)
      push('Incident deleted.', 'ok')
      navigate('/incidents')
    } catch (err) {
      push(errorText(err, 'Could not delete this incident.'), 'danger')
      setConfirmDelete(false)
    }
  }

  if (state === 'loading') return <SkeletonBlock lines={6} label="Loading incident" />
  if (state === 'notfound') return <EmptyState heading title="Incident not found" action={<Button onClick={() => navigate('/incidents')}>Back to incidents</Button>}>It may have been deleted.</EmptyState>
  if (state === 'error' || !incident) return <ErrorRetry message={errorMsg} onRetry={() => setNonce(n => n + 1)} />

  const open = incident.status !== 'resolved'

  return (
    <div>
      <PageTitle back={{ href: '/incidents', label: 'All incidents' }} title={incident.title}>INC-{incident.id} · {open ? 'Response in progress' : 'Resolved incident'}</PageTitle>
      <div className="grid items-start gap-7 xl:grid-cols-[minmax(0,1fr)_320px]">
        <div className="space-y-7">
      <section className="flex flex-col gap-5 bg-surface border border-default rounded-[14px] p-5">
        <h2 className="text-base font-semibold text-body">Response timeline</h2>
        {timeline.length === 0
          ? <p className="text-[13px] text-muted">No updates yet.</p>
          : (
            <ol className="flex flex-col gap-4 border-l border-default pl-4 ml-1">
              {timelineMore.shown.map(u => (
                <li key={u.id} className="relative">
                  <span aria-hidden="true" className={`absolute -left-[23px] top-1 w-3 h-3 rounded-full border-2 border-surface ${u.status === 'resolved' ? 'bg-ok' : u.status === 'monitoring' ? 'bg-accent' : 'bg-warn'}`} />
                  <div className="flex flex-wrap items-center gap-2 mb-1">
                    {u.internal
                      ? <Badge tone="neutral">Team note</Badge>
                      : <Badge tone={TONE[u.status] ?? 'neutral'}>{INCIDENT_STATUS[u.status] ?? u.status}</Badge>}
                    <span className="text-[12px] text-muted tnum">
                      <Timestamp at={u.createdAt} mode="absolute" /> · {u.createdByName || 'System'}
                      {u.editedAt != null && <span className="text-faint" title={`Edited ${dateServer(u.editedAt)}`}> (edited<span className="sr-only"> {dateServer(u.editedAt)}</span>)</span>}
                    </span>
                    {admin && editUpdate?.id !== u.id && (
                      <span className="ml-auto">
                        <DropdownMenu
                          label={`Actions for the ${u.internal ? 'team note' : 'update'} from ${dateServer(u.createdAt)}`}
                          items={[{ key: 'edit', label: 'Edit', icon: <Icon.Edit className="h-3.5 w-3.5" />, onSelect: () => { setEditUpdate({ id: u.id, body: u.body, internal: u.internal }); setEditError(null) } }]}
                        />
                      </span>
                    )}
                  </div>
                  {editUpdate?.id === u.id ? (
                    <div className="flex flex-col gap-2">
                      {editError && <Alert tone="danger">{editError}</Alert>}
                      <Textarea
                        value={editUpdate.body}
                        autoFocus
                        rows={4}
                        aria-label="Edit update text"
                        onChange={e => setEditUpdate({ ...editUpdate, body: e.target.value.slice(0, 5000) })}
                        onKeyDown={e => {
                          if (e.key === 'Escape') { e.stopPropagation(); setEditUpdate(null) }
                          else if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); void saveUpdateEdit() }
                        }}
                      />
                      <Checkbox
                        checked={editUpdate.internal}
                        onChange={e => setEditUpdate({ ...editUpdate, internal: e.target.checked })}
                        label="Team note: visible to the team only, never on status pages"
                      />
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <span className="text-[11px] text-faint tnum">{editUpdate.body.length}/5000 · {modKey()} + Enter to save</span>
                        <span className="flex gap-2">
                          <Button size="sm" variant="secondary" onClick={() => setEditUpdate(null)} disabled={editBusy}>Cancel</Button>
                          <Button size="sm" variant="primary" onClick={() => void saveUpdateEdit()} busy={editBusy}
                            disabled={editUpdate.body === u.body && editUpdate.internal === u.internal}>Save</Button>
                        </span>
                      </div>
                    </div>
                  ) : u.body && <p className={`text-[13px] leading-relaxed whitespace-pre-wrap break-words ${u.internal ? 'text-muted italic' : 'text-body-2'}`}><Linkified text={u.body} /></p>}
                </li>
              ))}
            </ol>
          )}
        <ShowMoreButton hidden={timelineMore.hidden} step={timelineMore.step} onClick={timelineMore.more} noun="older updates" />
      </section>

      {admin && open && (
        <section className="bg-surface border border-default rounded-[14px] p-5 flex flex-col gap-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2 className="text-base font-semibold text-body">{mode === 'public' ? 'Post a status update' : 'Add a team note'}</h2>
            <SegmentedControl size="sm" label="Update type" value={mode} onChange={m => { setMode(m); setFormError(null) }} options={[{ key: 'public', label: 'Status update' }, { key: 'note', label: 'Team note' }]} />
          </div>
          {formError && <Alert tone="danger">{formError}</Alert>}
          {mode === 'note' ? (
            <>
              <Field label="Note" hint="Visible to the team only — never shown on status pages. Doesn't change the status.">
                {fid => <Textarea id={fid} value={body} onChange={e => setBody(e.target.value.slice(0, 5000))} rows={3} placeholder="What you tried, what you found, who you've called…" />}
              </Field>
              <div className="flex justify-end">
                <Button variant="primary" onClick={() => post(incident.status, true)} busy={busy === 'update'} disabled={busy != null}>Add note</Button>
              </div>
            </>
          ) : (<>
          <div className="grid grid-cols-1 items-start gap-5 sm:grid-cols-2">
            <Field label="New status">
              {fid => (
                <Select id={fid} value={status} onChange={e => setStatus(e.target.value as IncidentStatus)}>
                  {STATUSES.filter(s => s !== 'resolved').map(s => <option key={s} value={s}>{INCIDENT_STATUS[s]}</option>)}
                </Select>
              )}
            </Field>
            <Field label="Update" hint="Plain text; links are auto-linked. Shown on the linked status pages.">
              {fid => <Textarea id={fid} value={body} onChange={e => setBody(e.target.value.slice(0, 5000))} rows={3} placeholder="What's happening, and what's next?" />}
            </Field>
          </div>
          <div className="flex flex-wrap items-center gap-2 justify-end">
            <Button variant="success" onClick={() => post('resolved')} busy={busy === 'resolve'} disabled={busy != null}>Resolve with this message</Button>
            <Button variant="primary" onClick={() => post(status)} busy={busy === 'update'} disabled={busy != null}>Post update</Button>
          </div>
          </>)}
        </section>
      )}

      {(!open || incident.postmortem) && (
        <section className="bg-surface border border-default rounded-[14px] p-5 flex flex-col gap-3">
          <div>
            <h2 className="text-base font-semibold text-body">Postmortem</h2>
            <p className="text-[12px] text-muted">Internal write-up: what happened, the impact, the root cause, what changed, and follow-ups.</p>
          </div>
          {admin ? (
            <>
              <Textarea value={pm} onChange={e => setPm(e.target.value.slice(0, 50000))} rows={10}
                placeholder={'What happened\n\nImpact\n\nRoot cause\n\nWhat we changed\n\nFollow-ups'} />
              <div className="flex justify-end">
                <Button variant="primary" busy={pmBusy} disabled={pm === incident.postmortem} onClick={savePostmortem}>Save postmortem</Button>
              </div>
            </>
          ) : incident.postmortem
            ? <p className="text-[13px] text-body-2 whitespace-pre-wrap break-words"><Linkified text={incident.postmortem} /></p>
            : <p className="text-[13px] text-muted">Not written yet.</p>}
        </section>
      )}

        </div><aside className="xl:sticky xl:top-24 xl:max-h-[calc(100dvh-7rem)] xl:overflow-y-auto">      <div className="flex flex-col gap-5 rounded-[14px] border border-default bg-surface p-5">
        <div className="flex flex-col gap-1.5 min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-base font-semibold text-body">Incident details</h2>
            <Badge tone={TONE[incident.status]} dot={open}>{INCIDENT_STATUS[incident.status]}</Badge>
            <Badge tone={impactTone(incident.impact)}>{IMPACT_LABEL[incident.impact] ?? incident.impact}</Badge>
            <Badge tone={severityTone(incident.severity)}>{severityLabel(incident.severity)}</Badge>
          </div>
          <p className="text-[13px] text-body-2">
            <span className="text-muted">Owner: </span>
            {incident.commanderName || <span className="text-warn">nobody yet{admin ? ' — set one with Edit' : ''}</span>}
          </p>
          <p className="text-[13px] text-muted">
            Opened {dateServer(incident.createdAt)}
            {incident.resolvedAt ? ` · resolved ${dateServer(incident.resolvedAt)} after ${duration(incident.resolvedAt - incident.createdAt)}` : ` · open for ${duration(Date.now() - incident.createdAt)}`}
          </p>
          <p className="text-[13px] text-body-2 flex flex-wrap gap-x-2 gap-y-1">
            <span className="text-muted">Affects:</span>
            {monitorLinks.length === 0 && <span className="text-faint">no monitors linked</span>}
            {linksMore.shown.map(m => m.name
              ? <Link key={m.id} href={`/monitors/${m.id}`} className="text-accent hover:underline">{m.name}</Link>
              : <span key={m.id} className="text-faint">deleted monitor</span>)}
          </p>
          <ShowMoreButton hidden={linksMore.hidden} step={linksMore.step} onClick={linksMore.more} noun="monitors" />
        </div>
        {admin && (
          <div className="flex items-center gap-2">
            <Button size="sm" onClick={() => setEditing(true)}>Edit</Button>
            <Button size="sm" variant="ghost" className="text-danger hover:text-danger" onClick={() => setConfirmDelete(true)}>Delete</Button>
          </div>
        )}
      </div>

</aside>
      </div>
      {editing && <EditModal incident={incident} onClose={() => setEditing(false)} onSaved={() => setNonce(n => n + 1)} />}
      <ConfirmModal
        open={confirmDelete}
        title="Delete this incident?"
        description="It disappears from every status page it was shown on. This can't be undone."
        confirmLabel="Delete incident"
        onConfirm={remove}
        onClose={() => setConfirmDelete(false)}
      />
    </div>
  )
}
