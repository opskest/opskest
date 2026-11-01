// "New incident" (admin): title, impact, affected monitors, the status
// pages to show it on, and the first message. The server opens every
// incident as Investigating; later updates move it along.

import { useEffect, useMemo, useState } from 'react'
import { api, errorText, unwrapList } from '../../lib/api'
import { normIncident, normPage } from '../../lib/normalize'
import type { Incident, IncidentIn, MonitorSummary, Page } from '../../lib/types'
import { Alert, Button, Field, Input, Modal, Select, Textarea, useToast } from '../ui'
import { SEVERITIES, useTeam } from '../../hooks/useTeam'

function CheckList({ legend, items, selected, onToggle, empty, filter }: {
  legend: string; items: { id: number; label: string }[]; selected: Set<number>; onToggle: (id: number) => void; empty: string; filter?: boolean
}) {
  const [q, setQ] = useState('')
  const matches = filter && q ? items.filter(i => i.label.toLowerCase().includes(q.toLowerCase())) : items
  // Never render thousands of checkboxes: show the first 100, filter for the rest.
  const shown = matches.slice(0, 100)
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <span className="text-[11px] font-medium text-muted">{legend}{selected.size > 0 ? ` · ${selected.size} selected` : ''}</span>
      {filter && <Input aria-label={`Filter ${legend.toLowerCase()}`} placeholder="Filter…" value={q} onChange={e => setQ(e.target.value)} />}
      <div role="group" aria-label={legend} className="border border-default rounded-[10px] h-40 overflow-y-auto scroll-slim p-1.5 flex flex-col gap-0.5">
        {shown.length === 0 && <p className="text-[12px] text-faint px-1.5 py-1">{empty}</p>}
        {shown.map(i => (
          <label key={i.id} className="flex items-center gap-2 px-1.5 py-1 text-[13px] text-body-2 hover:bg-surface-hover rounded-sm cursor-pointer">
            <input type="checkbox" checked={selected.has(i.id)} onChange={() => onToggle(i.id)} className="w-3.5 h-3.5 accent-accent" />
            <span className="truncate">{i.label}</span>
          </label>
        ))}
        {matches.length > shown.length && <p className="text-[12px] text-faint px-1.5 py-1">Showing {shown.length} of {matches.length}. Type in the filter to narrow the list.</p>}
      </div>
    </div>
  )
}

export function NewIncidentModal({
  open, onClose, onCreated, monitors,
}: { open: boolean; onClose: () => void; onCreated: (i: Incident) => void; monitors: Map<number, MonitorSummary> }) {
  const { push } = useToast()
  const [title, setTitle] = useState('')
  const [impact, setImpact] = useState('minor')
  const [severity, setSeverity] = useState('major')
  const [commanderId, setCommanderId] = useState<number | null>(null)
  const team = useTeam(true)
  const [monitorIds, setMonitorIds] = useState<Set<number>>(new Set())
  const [pageIds, setPageIds] = useState<Set<number>>(new Set())
  const [body, setBody] = useState('')
  const [pages, setPages] = useState<Page[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!open) return
    setTitle(''); setImpact('minor'); setMonitorIds(new Set()); setPageIds(new Set()); setBody(''); setError(null)
    api.get<unknown>('/api/pages').then(d => setPages(unwrapList(d, 'pages').map(normPage))).catch(() => setPages([]))
  }, [open])

  const monitorItems = useMemo(
    () => [...monitors.values()].filter(m => !m.vendor).map(m => ({ id: m.id, label: m.name })).sort((a, b) => a.label.localeCompare(b.label)),
    [monitors],
  )

  function toggle(set: Set<number>, setSet: (s: Set<number>) => void, id: number) {
    const next = new Set(set)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    setSet(next)
  }

  async function submit() {
    if (!title.trim() || !body.trim()) { setError('A title and a first message are required.'); return }
    setBusy(true)
    setError(null)
    try {
      const payload: IncidentIn = { title: title.trim(), impact, severity, commanderId, body: body.trim(), monitorIds: [...monitorIds], pageIds: [...pageIds] }
      const created = normIncident(await api.post<unknown>('/api/incidents', payload))
      onCreated(created)
      push('Incident opened.', 'ok')
      onClose()
    } catch (err) {
      setError(errorText(err, 'Could not create that incident.'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="New incident"
      description="Opens as Investigating. Post updates from the incident page as you learn more."
      size="lg"
      footer={(
        <>
          <Button variant="secondary" onClick={onClose}>Cancel</Button>
          <Button variant="primary" onClick={submit} busy={busy}>Open incident</Button>
        </>
      )}
    >
      <div className="flex flex-col gap-5">
        {error && <Alert tone="danger">{error}</Alert>}
        <div className="grid grid-cols-1 items-start gap-5 sm:grid-cols-2">
          <Field label="Title">{id => <Input id={id} value={title} onChange={e => setTitle(e.target.value)} placeholder="Payments gateway slow to respond" maxLength={200} />}</Field>
          <Field label="Impact">
            {id => (
              <Select id={id} value={impact} onChange={e => setImpact(e.target.value)}>
                <option value="minor">Minor</option>
                <option value="major">Major</option>
                <option value="maintenance">Maintenance</option>
              </Select>
            )}
          </Field>
        </div>
        <div className="grid grid-cols-1 items-start gap-5 sm:grid-cols-2">
          <Field label="Severity" hint="Internal only — never shown on status pages.">
            {id => (
              <Select id={id} value={severity} onChange={e => setSeverity(e.target.value)}>
                {SEVERITIES.map(s => <option key={s.id} value={s.id}>{s.label}</option>)}
              </Select>
            )}
          </Field>
          <Field label="Owner" hint="Who is running the response.">
            {id => (
              <Select id={id} value={commanderId ?? ''} onChange={e => setCommanderId(e.target.value ? Number(e.target.value) : null)}>
                <option value="">No owner yet</option>
                {team.map(m => <option key={m.id} value={m.id}>{m.label}</option>)}
              </Select>
            )}
          </Field>
        </div>
        <div className="grid grid-cols-1 items-start gap-5 sm:grid-cols-2">
          <CheckList legend="Affected monitors" items={monitorItems} selected={monitorIds} onToggle={id => toggle(monitorIds, setMonitorIds, id)} empty="No monitors yet." filter />
          <CheckList filter legend="Show on status pages" items={pages.map(p => ({ id: p.id, label: `${p.title} (/status/${p.slug})` }))} selected={pageIds} onToggle={id => toggle(pageIds, setPageIds, id)} empty="No status pages yet." />
        </div>
        <Field label="First message" hint="Plain text; links are auto-linked.">
          {id => <Textarea id={id} value={body} onChange={e => setBody(e.target.value.slice(0, 5000))} rows={4} placeholder="We're seeing elevated error rates on checkout and are investigating." />}
        </Field>
      </div>
    </Modal>
  )
}
