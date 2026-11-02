// Create/edit a maintenance window: title and description; once
// (start and end in the chosen tz) or weekly (weekday toggles, start time,
// duration); IANA timezone; target monitors and groups; active.

import { useEffect, useMemo, useState } from 'react'
import { api, errorText } from '../lib/api'
import { utcMsToZonedDateTime, zonedDateTimeToUtcMs } from '../lib/format'
import { serverTimeZone } from '../lib/runtime'
import type { Group, Maintenance, MaintenanceIn, MaintenanceKind, MonitorSummary } from '../lib/types'
import { DateTimeInput, TimeInput } from './DateInputs'
import { Alert, Button, Field, Input, Modal, Select, Switch, Textarea } from './ui'

const WEEKDAYS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']

function minutesToTime(min: number): string {
  return `${String(Math.floor(min / 60) % 24).padStart(2, '0')}:${String(min % 60).padStart(2, '0')}`
}

function timeToMinutes(t: string): number {
  const [h, m] = t.split(':').map(Number)
  return Number.isNaN(h) || Number.isNaN(m) ? 0 : h * 60 + m
}

function draftFrom(m: Maintenance | null): MaintenanceIn {
  return m
    ? {
      title: m.title, description: m.description, kind: m.kind, startsAt: m.startsAt, endsAt: m.endsAt,
      weekdays: m.weekdays, startMinute: m.startMinute, durationMin: m.durationMin, tz: m.tz, active: m.active,
      monitorIds: m.monitorIds, groupIds: m.groupIds,
    }
    : {
      title: '', description: '', kind: 'once', startsAt: null, endsAt: null,
      weekdays: 0, startMinute: 23 * 60, durationMin: 60, tz: serverTimeZone(), active: true, monitorIds: [], groupIds: [],
    }
}

function TargetList({ legend, items, selected, onChange }: {
  legend: string; items: { id: number; label: string }[]; selected: Set<number>; onChange: (s: Set<number>) => void
}) {
  const [q, setQ] = useState('')
  const matches = q ? items.filter(i => i.label.toLowerCase().includes(q.toLowerCase())) : items
  // Never render thousands of checkboxes: show the first 100, filter for the rest.
  const shown = matches.slice(0, 100)
  function toggle(id: number) {
    const next = new Set(selected)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    onChange(next)
  }
  return (
    <div className="flex flex-col gap-1.5 min-w-0">
      <span className="text-[12px] font-medium text-body-2">{legend}{selected.size ? ` · ${selected.size}` : ''}</span>
      <Input aria-label={`Filter ${legend}`} placeholder="Filter…" value={q} onChange={e => setQ(e.target.value)} />
      <div role="group" aria-label={legend} className="border border-default rounded-[10px] h-[220px] overflow-y-auto scroll-slim p-1.5 flex flex-col gap-0.5 content-start">
        {shown.length === 0 && <p className="text-[12px] text-faint px-1.5 py-1">None.</p>}
        {shown.map(i => (
          <label key={i.id} className="flex items-center gap-2 px-1.5 py-1 text-[13px] text-body-2 hover:bg-surface-hover rounded-sm cursor-pointer">
            <input type="checkbox" checked={selected.has(i.id)} onChange={() => toggle(i.id)} className="w-3.5 h-3.5 accent-accent" />
            <span className="truncate">{i.label}</span>
          </label>
        ))}
        {matches.length > shown.length && <p className="text-[12px] text-faint px-1.5 py-1">Showing {shown.length} of {matches.length}. Type in the filter to narrow the list.</p>}
      </div>
    </div>
  )
}

export function MaintenanceForm({
  open, onClose, editing, monitors, groups, onSaved,
}: {
  open: boolean
  onClose: () => void
  editing: Maintenance | null
  monitors: MonitorSummary[]
  groups: Group[]
  onSaved: () => void
}) {
  const [d, setD] = useState<MaintenanceIn>(() => draftFrom(editing))
  const [monitorIds, setMonitorIds] = useState<Set<number>>(new Set())
  const [groupIds, setGroupIds] = useState<Set<number>>(new Set())
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [startInput, setStartInput] = useState('')
  const [endInput, setEndInput] = useState('')

  useEffect(() => {
    if (!open) return
    const x = draftFrom(editing)
    setD(x)
    setMonitorIds(new Set(x.monitorIds))
    setGroupIds(new Set(x.groupIds))
    setError(null)
    setStartInput(utcMsToZonedDateTime(x.startsAt, x.tz))
    setEndInput(utcMsToZonedDateTime(x.endsAt, x.tz))
  }, [open, editing])

  const set = <K extends keyof MaintenanceIn>(k: K, v: MaintenanceIn[K]) => setD(prev => ({ ...prev, [k]: v }))

  async function submit() {
    if (!d.title.trim()) { setError('Title is required.'); return }
    if (d.kind === 'once' && (d.startsAt == null || d.endsAt == null)) { setError('Pick a start and an end.'); return }
    if (d.kind === 'once' && d.startsAt != null && d.endsAt != null && d.endsAt <= d.startsAt) { setError('The end must be after the start.'); return }
    if (d.kind === 'weekly' && d.weekdays === 0) { setError('Pick at least one weekday.'); return }
    if (d.kind === 'weekly' && (d.durationMin < 1 || d.durationMin > 1440)) { setError('Duration must be 1–1440 minutes.'); return }
    if (monitorIds.size === 0 && groupIds.size === 0) { setError('Pick at least one monitor or group it applies to.'); return }
    setBusy(true)
    setError(null)
    const body: MaintenanceIn = { ...d, title: d.title.trim(), monitorIds: [...monitorIds], groupIds: [...groupIds] }
    try {
      if (editing) await api.put(`/api/maintenance/${editing.id}`, body)
      else await api.post('/api/maintenance', body)
      onSaved()
      onClose()
    } catch (err) {
      setError(errorText(err, 'Could not save this maintenance window.'))
    } finally {
      setBusy(false)
    }
  }

  const monitorItems = useMemo(() => monitors.filter(m => !m.vendor).map(m => ({ id: m.id, label: m.name })).sort((a, b) => a.label.localeCompare(b.label)), [monitors])
  const groupItems = [...groups].sort((a, b) => a.sort - b.sort).map(g => ({ id: g.id, label: g.name }))
  const tzLabel = d.tz
  const supportedZones = (Intl as typeof Intl & { supportedValuesOf?: (key: string) => string[] }).supportedValuesOf?.('timeZone') ?? []
  const timeZones = [...new Set([serverTimeZone(), d.tz, 'UTC', ...supportedZones])]
  function changeZone(tz: string) {
    set('tz', tz)
    setStartInput(utcMsToZonedDateTime(d.startsAt, tz))
    setEndInput(utcMsToZonedDateTime(d.endsAt, tz))
  }

  const startField = (
    <Field label={`Starts (${tzLabel})`} error={startInput && d.startsAt == null ? 'This local time does not exist in the selected timezone.' : undefined}>
      {id => <DateTimeInput id={id} tz={d.tz} value={startInput} onChange={wall => { setStartInput(wall); set('startsAt', wall ? zonedDateTimeToUtcMs(wall, d.tz) : null) }} buttonLabel="Choose start date and time" />}
    </Field>
  )

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={editing ? 'Edit maintenance window' : 'New maintenance window'}
      description="While a window is active its monitors show Maintenance and send no alerts."
      size="xl"
      footerStart={<Switch label="Active" hint="Off keeps the window without applying it." checked={d.active} onChange={e => set('active', e.target.checked)} />}
      footer={<><Button variant="secondary" onClick={onClose}>Cancel</Button><Button variant="primary" onClick={submit} busy={busy}>{editing ? 'Save' : 'Create window'}</Button></>}
    >
      <div className="flex flex-col gap-4">
        {error && <Alert tone="danger">{error}</Alert>}
        <div className="grid grid-cols-1 items-start gap-4 sm:grid-cols-2">
          <Field label="Title">{id => <Input id={id} value={d.title} onChange={e => set('title', e.target.value)} placeholder="Database upgrade" maxLength={200} />}</Field>
          <Field label="Description" hint="Shown on status pages that include these monitors.">{id => <Textarea id={id} value={d.description} onChange={e => set('description', e.target.value)} rows={3} className="resize-none" />}</Field>
        </div>

        <div className="grid grid-cols-2 items-start gap-4 lg:grid-cols-4">
          <Field label="Repeats">
            {id => (
              <Select id={id} value={d.kind} onChange={e => set('kind', e.target.value as MaintenanceKind)}>
                <option value="once">Once</option>
                <option value="weekly">Every week</option>
              </Select>
            )}
          </Field>
          <Field label="Timezone">
            {id => (
              <Select id={id} value={d.tz} onChange={e => changeZone(e.target.value)}>
                {timeZones.map(zone => <option key={zone} value={zone}>{zone}{zone === serverTimeZone() ? ' (server)' : ''}</option>)}
              </Select>
            )}
          </Field>
          {d.kind === 'once' ? (
            <>
              {startField}
              <Field label={`Ends (${tzLabel})`} error={endInput && d.endsAt == null ? 'This local time does not exist in the selected timezone.' : undefined}>
                {id => <DateTimeInput id={id} tz={d.tz} value={endInput} min={startInput.slice(0, 10) || undefined} onChange={wall => { setEndInput(wall); set('endsAt', wall ? zonedDateTimeToUtcMs(wall, d.tz) : null) }} buttonLabel="Choose end date and time" />}
              </Field>
            </>
          ) : (
            <>
              <Field label={`Starts at (${tzLabel})`}>
                {id => <TimeInput id={id} value={minutesToTime(d.startMinute)} onChange={v => { if (v) set('startMinute', timeToMinutes(v)) }} buttonLabel="Choose start time" />}
              </Field>
              <Field label="For (minutes)">
                {id => <Input id={id} type="number" min={1} max={1440} value={d.durationMin} onChange={e => set('durationMin', Number(e.target.value) || 0)} />}
              </Field>
            </>
          )}
        </div>

        {d.kind === 'weekly' && (
          <div className="flex flex-col gap-1.5">
            <span className="text-[12px] font-medium text-body-2" id="weekdays-label">On</span>
            <div className="grid grid-cols-7 gap-1.5" role="group" aria-labelledby="weekdays-label">
              {WEEKDAYS.map((w, bit) => {
                const on = (d.weekdays & (1 << bit)) !== 0
                return (
                  <button key={w} type="button" aria-pressed={on} onClick={() => set('weekdays', d.weekdays ^ (1 << bit))}
                    className={`h-9 rounded-[10px] text-[12px] font-medium border transition-colors ${on ? 'bg-accent text-accent-fg border-transparent' : 'bg-surface-2 text-muted border-default hover:border-strong'}`}>
                    {w}
                  </button>
                )
              })}
            </div>
          </div>
        )}

        <div className="grid grid-cols-1 items-stretch gap-4 sm:grid-cols-2">
          <TargetList legend="Monitors" items={monitorItems} selected={monitorIds} onChange={setMonitorIds} />
          <TargetList legend="Groups (current and future members)" items={groupItems} selected={groupIds} onChange={setGroupIds} />
        </div>
      </div>
    </Modal>
  )
}
