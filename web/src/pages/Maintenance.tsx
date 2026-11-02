// /maintenance: the schedule as a month calendar by default, with an agenda for
// the chosen day, or as a grouped list (in progress, upcoming, past, inactive). Windows run in
// the timezone saved with each schedule; the calendar is drawn in the display zone. Admins
// create, edit, turn on or off and delete.

import { useEffect, useMemo, useState } from 'react'
import { Calendar, type CalendarMark } from '../components/Calendar'
import { MaintenanceForm } from '../components/MaintenanceForm'
import { PageTitle } from '../components/PageShell'
import { Pager, usePaging } from '../components/Pager'
import { Badge, Button, ConfirmModal, DropdownMenu, EmptyState, ErrorRetry, FilterChips, Icon, Input, SegmentedControl, SkeletonRows, Timestamp, Tooltip, useToast } from '../components/ui'
import { LIST_FRAME } from '../components/table'
import { api, errorText, unwrapList } from '../lib/api'
import { addDays, dayEndInZone, dayStartInZone, daysTouched, monthGrid, monthStart, occurrences, ymdInZone, type Ymd } from '../lib/calendar'
import { duration, serverZoneLabel } from '../lib/format'
import { serverTimeZone } from '../lib/runtime'
import { useLive } from '../lib/live'
import { normMaintenance } from '../lib/normalize'
import { canWrite } from '../lib/roles'
import type { Maintenance, MaintenanceIn, MaintenanceState, Me } from '../lib/types'

const WEEKDAY_SHORT = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']
const STATE_LABEL: Record<MaintenanceState, string> = { active: 'In progress', upcoming: 'Upcoming', past: 'Past', inactive: 'Inactive' }
const STATE_TONE: Record<MaintenanceState, 'accent' | 'warn' | 'neutral'> = { active: 'accent', upcoming: 'warn', past: 'neutral', inactive: 'neutral' }
const STATE_ORDER: MaintenanceState[] = ['active', 'upcoming', 'past', 'inactive']
const VIEW_KEY = 'opskest-maintenance-view'

type Layout = 'calendar' | 'list'

/** The calendar is the default; the list is remembered only after the person chose it. */
function readLayout(): Layout {
  try { return window.localStorage.getItem(VIEW_KEY) === 'list' ? 'list' : 'calendar' } catch { return 'calendar' }
}
function saveLayout(layout: Layout) {
  try { window.localStorage.setItem(VIEW_KEY, layout) } catch { /* storage may be unavailable */ }
}

const stateOf = (m: Maintenance): MaintenanceState => m.state ?? (m.active ? 'upcoming' : 'inactive')

function recurrenceLabel(m: Maintenance): string {
  const days = WEEKDAY_SHORT.filter((_, i) => (m.weekdays & (1 << i)) !== 0).join(', ')
  const hh = String(Math.floor(m.startMinute / 60)).padStart(2, '0')
  const mm = String(m.startMinute % 60).padStart(2, '0')
  return `Every ${days || '—'} at ${hh}:${mm} ${m.tz}, for ${m.durationMin} min`
}

/** "Mon 3 Nov, 02:00 – 03:00 UTC", or both dates when the window spans days. */
function rangeLabel(start: number, end: number, tz: string): string {
  const day = new Intl.DateTimeFormat(undefined, { weekday: 'short', day: 'numeric', month: 'short', timeZone: tz })
  const time = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZone: tz })
  const zone = new Intl.DateTimeFormat(undefined, { timeZoneName: 'short', timeZone: tz }).formatToParts(new Date(end)).find(p => p.type === 'timeZoneName')?.value ?? tz
  const s = new Date(start)
  const e = new Date(end)
  return day.format(s) === day.format(e)
    ? `${day.format(s)}, ${time.format(s)} – ${time.format(e)} ${zone}`
    : `${day.format(s)} ${time.format(s)} – ${day.format(e)} ${time.format(e)} ${zone}`
}

function WindowTiming({ m, state }: { m: Maintenance; state: MaintenanceState }) {
  if (state === 'active' && m.currentEnd != null) return <span className="font-medium text-accent"><Timestamp at={m.currentEnd} prefix="Ends" /></span>
  if (state === 'upcoming') {
    const at = m.nextStart ?? m.startsAt
    return at != null ? <span className="font-medium text-warn"><Timestamp at={at} prefix="Starts" /></span> : null
  }
  if (state === 'past' && m.endsAt != null) return <span><Timestamp at={m.endsAt} future={false} prefix="Ended" /></span>
  if (state === 'inactive') return <span>Turned off</span>
  return null
}

function WindowRow({ m, admin, monitorNames, projectNames, creator, onEdit, onToggle, onDelete }: {
  m: Maintenance
  admin: boolean
  monitorNames: string[]
  projectNames: string[]
  creator: string | null
  onEdit: () => void
  onToggle: () => void
  onDelete: () => void
}) {
  const state = stateOf(m)
  const total = monitorNames.length + projectNames.length
  const targetText = [
    monitorNames.length ? `${monitorNames.length} ${monitorNames.length === 1 ? 'monitor' : 'monitors'}` : '',
    projectNames.length ? `${projectNames.length} ${projectNames.length === 1 ? 'project' : 'projects'}` : '',
  ].filter(Boolean).join(' · ') || 'No targets'
  const span = m.kind === 'once'
    ? (m.startsAt != null && m.endsAt != null ? { range: rangeLabel(m.startsAt, m.endsAt, m.tz), length: duration(m.endsAt - m.startsAt) } : null)
    : { range: recurrenceLabel(m), length: duration(m.durationMin * 60_000) }
  return (
    <article id={`maintenance-${m.id}`} tabIndex={-1} className="group relative grid scroll-mt-24 gap-x-5 gap-y-2 px-5 py-3.5 transition-colors hover:bg-surface-hover focus:outline-none md:grid-cols-[minmax(0,1.4fr)_minmax(0,1.3fr)_auto] md:items-center">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <h3 className="min-w-0 truncate text-sm font-semibold text-body">{m.title}</h3>
          <Badge tone={STATE_TONE[state]} dot={state === 'active'}>{STATE_LABEL[state]}</Badge>
          <Badge>{m.kind === 'once' ? 'Once' : 'Weekly'}</Badge>
        </div>
        <p className="mt-1 truncate text-xs text-muted">{m.description || 'No description added.'}</p>
        <p className="mt-1 text-xs text-faint">{creator ? `Created by ${creator}` : 'Created'} · <Timestamp at={m.createdAt} future={false} /></p>
      </div>
      <div className="min-w-0 space-y-1 text-xs text-muted">
        {span && <p className="tnum text-body-2">{span.range}{m.kind === 'once' && <span className="text-muted"> · {span.length}</span>}</p>}
        <p className="tnum"><WindowTiming m={m} state={state} /></p>
        <p>
          <Tooltip disabled={total === 0} content={<div className="max-w-[260px] space-y-0.5">{[...monitorNames, ...projectNames.map(n => `${n} (project)`)].slice(0, 20).map((n, i) => <div key={i} className="truncate">{n}</div>)}{total > 20 && <div>and {total - 20} more</div>}</div>}>
            <span tabIndex={0} className="cursor-default underline decoration-dotted decoration-faint/60 underline-offset-[3px]">Applies to: {targetText}</span>
          </Tooltip>
          {total > 0 && <span className="sr-only"> ({[...monitorNames, ...projectNames].join(', ')})</span>}
        </p>
      </div>
      {admin && (
        <div className="flex shrink-0 items-center gap-1.5 md:justify-end">
          <Button size="sm" onClick={onEdit} icon={<Icon.Edit className="h-3.5 w-3.5" />}>Edit</Button>
          <DropdownMenu label={`More actions for ${m.title}`} items={[
            { key: 'toggle', label: m.active ? 'Turn off' : 'Turn on', icon: <Icon.Pause className="h-3.5 w-3.5" />, onSelect: onToggle },
            { key: 'sep', separator: true },
            { key: 'delete', label: 'Delete window', icon: <Icon.Trash className="h-3.5 w-3.5" />, tone: 'danger', onSelect: onDelete },
          ]} />
        </div>
      )}
    </article>
  )
}

export default function MaintenancePage({ user }: { user: Me }) {
  const admin = canWrite(user) // admin or user
  const live = useLive()
  const { push } = useToast()
  const [items, setItems] = useState<Maintenance[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [nonce, setNonce] = useState(0)
  const [formOpen, setFormOpen] = useState(() => new URLSearchParams(window.location.search).get('new') === '1')
  const [view, setView] = useState<MaintenanceState | 'all'>('all')
  const [query, setQuery] = useState('')
  const [editing, setEditing] = useState<Maintenance | null>(null)
  const [deleting, setDeleting] = useState<Maintenance | null>(null)
  const [layout, setLayout] = useState<Layout>(readLayout)
  const [people, setPeople] = useState<Map<number, string>>(new Map())
  const [calMonth, setCalMonth] = useState<Ymd>(() => monthStart(ymdInZone(Date.now(), serverTimeZone())))
  const [calDay, setCalDay] = useState<Ymd>(() => ymdInZone(Date.now(), serverTimeZone()))
  const [reveal, setReveal] = useState<number | null>(null)

  useEffect(() => {
    let alive = true
    setError(null)
    api.get<unknown>('/api/maintenance')
      .then(d => { if (alive) setItems(unwrapList(d, 'maintenance').map(normMaintenance)) })
      .catch(err => { if (alive) setError(errorText(err, 'Failed to load maintenance windows')) })
    return () => { alive = false }
  }, [nonce])

  // Authors are listed by name for administrators; everyone else just sees when it was created.
  useEffect(() => {
    if (!admin) return
    api.get<unknown>('/api/users')
      .then(d => setPeople(new Map(unwrapList<{ id: number; name?: string; username?: string; email?: string }>(d, 'users').map(u => [u.id, u.name || u.username || u.email || `user ${u.id}`]))))
      .catch(() => { /* optional */ })
  }, [admin])

  const monitorName = (id: number) => live.monitors.get(id)?.name ?? 'deleted monitor'
  const groupName = (id: number) => live.groups.find(g => g.id === id)?.name ?? 'Deleted project'

  const grouped = useMemo(() => {
    if (!items) return null
    const buckets: Record<MaintenanceState, Maintenance[]> = { active: [], upcoming: [], past: [], inactive: [] }
    const q = query.trim().toLowerCase()
    for (const m of items) {
      if (q && !`${m.title} ${m.description} ${m.monitorIds.map(monitorName).join(' ')} ${m.groupIds.map(groupName).join(' ')}`.toLowerCase().includes(q)) continue
      buckets[stateOf(m)].push(m)
    }
    buckets.upcoming.sort((a, b) => (a.nextStart ?? a.startsAt ?? Infinity) - (b.nextStart ?? b.startsAt ?? Infinity))
    buckets.past.sort((a, b) => (b.endsAt ?? 0) - (a.endsAt ?? 0))
    return buckets
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items, query, live.monitors, live.groups])

  const totals = useMemo(() => {
    const t: Record<MaintenanceState | 'all', number> = { all: items?.length ?? 0, active: 0, upcoming: 0, past: 0, inactive: 0 }
    for (const m of items ?? []) t[stateOf(m)]++
    return t
  }, [items])

  // Page the schedule as one flat list (in progress, upcoming, past, inactive in
  // that order); each state heading then shows only its slice of the page.
  const flat = useMemo(() => grouped ? STATE_ORDER.filter(s => view === 'all' || view === s).flatMap(s => grouped[s]) : [], [grouped, view])
  const paging = usePaging(flat, { resetKey: `${view}|${query}` })
  const pageIds = useMemo(() => new Set(paging.rows.map(m => m.id)), [paging.rows])

  // Every window occurrence that touches the visible grid, as days in the display zone.
  const tz = serverTimeZone()
  const days = useMemo(() => {
    const marks: Record<Ymd, CalendarMark[]> = {}
    const byDay: Record<Ymd, { m: Maintenance; start: number; end: number }[]> = {}
    if (!items) return { marks, byDay }
    const first = monthGrid(calMonth)[0][0].ymd
    const last = addDays(first, 41)
    const from = dayStartInZone(first, tz) ?? 0
    const to = dayEndInZone(last, tz) ?? Date.now()
    for (const m of items) {
      const state = stateOf(m)
      const tone: CalendarMark['tone'] = state === 'active' ? 'accent' : state === 'upcoming' ? 'warn' : 'neutral'
      for (const o of occurrences(m, from, to)) {
        for (const day of daysTouched(o.start, o.end, tz)) {
          if (day < first || day > last) continue
          ;(marks[day] ??= []).push({ tone, label: `${m.title} (${STATE_LABEL[state].toLowerCase()})` })
          ;(byDay[day] ??= []).push({ m, start: o.start, end: o.end })
        }
      }
    }
    for (const list of Object.values(byDay)) list.sort((a, b) => a.start - b.start)
    return { marks, byDay }
  }, [items, calMonth, tz])

  function chooseLayout(next: Layout) {
    setLayout(next)
    saveLayout(next)
  }

  // "View in schedule": switch to the list, land on the page holding the window, then bring it into view.
  function showInList(id: number) {
    setView('all')
    setQuery('')
    setLayout('list')
    const at = STATE_ORDER.flatMap(st => (items ?? []).filter(m => stateOf(m) === st)).findIndex(m => m.id === id)
    if (at >= 0) paging.setPage(Math.floor(at / paging.pageSize) + 1)
    setReveal(id)
  }
  useEffect(() => {
    if (reveal == null || layout !== 'list') return
    const el = document.getElementById(`maintenance-${reveal}`)
    if (!el) return
    el.scrollIntoView({ block: 'center' })
    el.focus({ preventScroll: true })
    setReveal(null)
  }, [reveal, layout, paging.page, view])

  async function setActive(m: Maintenance, active: boolean) {
    const body: MaintenanceIn = {
      title: m.title, description: m.description, kind: m.kind, startsAt: m.startsAt, endsAt: m.endsAt, weekdays: m.weekdays,
      startMinute: m.startMinute, durationMin: m.durationMin, tz: m.tz, active, monitorIds: m.monitorIds, groupIds: m.groupIds,
    }
    try {
      await api.put(`/api/maintenance/${m.id}`, body)
      push(active ? 'Maintenance window turned on.' : 'Maintenance window turned off.', 'ok')
      setNonce(n => n + 1)
    } catch (err) {
      push(errorText(err, 'Could not change that window.'), 'danger')
    }
  }

  function openForm(m: Maintenance | null) { setEditing(m); setFormOpen(true) }

  async function remove() {
    if (!deleting) return
    try {
      await api.del(`/api/maintenance/${deleting.id}`)
      setItems(prev => prev?.filter(m => m.id !== deleting.id) ?? prev)
      push('Maintenance window deleted.', 'ok')
    } catch (err) {
      push(errorText(err, 'Could not delete that window.'), 'danger')
    } finally {
      setDeleting(null)
    }
  }

  const dayTitle = new Intl.DateTimeFormat(undefined, { weekday: 'long', day: 'numeric', month: 'long', timeZone: 'UTC' }).format(new Date(`${calDay}T12:00:00Z`))

  return (
    <>
      <PageTitle
        title="Maintenance schedule"
        actions={admin ? <Button variant="primary" onClick={() => openForm(null)} icon={<Icon.Plus className="w-4 h-4" />}>New window</Button> : undefined}
      >
        Plan service work and pause alerts for affected monitors. Active maintenance is excluded from uptime.
      </PageTitle>

      {error && <ErrorRetry message={error} onRetry={() => setNonce(n => n + 1)} />}
      {!error && !grouped && (
        <div>
          <div className="mb-5 h-9" />
          <SkeletonRows rows={5} label="Loading maintenance windows" />
        </div>
      )}
      {items?.length === 0 && (
        <section aria-label="Maintenance schedule">
          <EmptyState icon={<Icon.Wrench className="h-5 w-5" />} title="No maintenance windows">
            {admin ? 'Create a window to schedule planned work and pause alerts for its monitors.' : 'Planned work and recurring maintenance will appear here.'}
          </EmptyState>
        </section>
      )}

      {grouped && items && items.length > 0 && (
        <div className="min-w-0">
          <div className="mb-5 flex flex-wrap items-center justify-between gap-3">
            <SegmentedControl label="Schedule layout" value={layout} onChange={chooseLayout} options={[
              { key: 'calendar', label: <><Icon.Calendar className="h-3.5 w-3.5" />Calendar</> },
              { key: 'list', label: <><Icon.List className="h-3.5 w-3.5" />List</> },
            ]} />
            <p className="text-xs text-muted">Times shown in <span className="font-medium text-body-2">{tz}</span>{serverZoneLabel() !== tz && ` (${serverZoneLabel()})`}; each window keeps the zone it was saved with.</p>
          </div>

          {layout === 'calendar' && (
            <div className="grid items-stretch gap-6 xl:grid-cols-2">
              <div className="min-w-0 rounded-[14px] border border-default bg-surface p-4">
                <Calendar size="lg" label="Maintenance schedule" value={calDay} onChange={setCalDay} marks={days.marks} onMonthChange={setCalMonth} />
                <ul className="mt-4 flex flex-wrap gap-x-5 gap-y-1 border-t border-default pt-3 text-[12px] text-muted" aria-label="Legend">
                  <li className="flex items-center gap-1.5"><span aria-hidden="true" className="h-1.5 w-1.5 rounded-full bg-accent" />In progress</li>
                  <li className="flex items-center gap-1.5"><span aria-hidden="true" className="h-1.5 w-1.5 rounded-full bg-warn" />Upcoming</li>
                  <li className="flex items-center gap-1.5"><span aria-hidden="true" className="h-1.5 w-1.5 rounded-full bg-faint" />Past or off</li>
                </ul>
              </div>
              <section aria-label={`Windows on ${calDay}`} aria-live="polite" className="flex min-w-0 flex-col overflow-hidden rounded-[14px] border border-default bg-surface">
                <h2 className="shrink-0 border-b border-default px-5 py-3.5 text-base font-semibold">{dayTitle}</h2>
                {(days.byDay[calDay] ?? []).length === 0
                  ? <p className="flex flex-1 items-center justify-center px-5 py-6 text-center text-[13px] text-muted">No maintenance this day.{admin ? ' Create a window to schedule planned work.' : ''}</p>
                  : <ul className="divide-y divide-default">
                    {(days.byDay[calDay] ?? []).map((o, i) => {
                      const state = stateOf(o.m)
                      return (
                        <li key={`${o.m.id}-${i}`} className="relative flex flex-col gap-2 px-5 py-3.5">
                          <div className="flex flex-wrap items-center gap-2">
                            <span className="text-[14px] font-semibold text-body [overflow-wrap:anywhere]">{o.m.title}</span>
                            <Badge tone={STATE_TONE[state]} dot={state === 'active'}>{STATE_LABEL[state]}</Badge>
                          </div>
                          <p className="text-[12px] text-muted tnum">{rangeLabel(o.start, o.end, tz)} · {duration(o.end - o.start)}</p>
                          <div className="flex gap-2">
                            <Button size="sm" onClick={() => showInList(o.m.id)}>View in schedule</Button>
                            {admin && <Button size="sm" variant="ghost" onClick={() => openForm(o.m)} icon={<Icon.Edit className="h-3.5 w-3.5" />}>Edit</Button>}
                          </div>
                        </li>
                      )
                    })}
                  </ul>}
              </section>
            </div>
          )}

          {layout === 'list' && (
            <div className="flex flex-col gap-5">
              <div className="flex flex-wrap items-center gap-3">
                <Input aria-label="Search maintenance windows" value={query} onChange={e => setQuery(e.target.value)} placeholder="Search title, description or target" className="w-full sm:max-w-xs" />
                <FilterChips label="Filter maintenance windows" value={view} onChange={setView}
                  options={(['all', ...STATE_ORDER] as const).map(key => ({ key, label: key === 'all' ? 'Full schedule' : STATE_LABEL[key], count: totals[key] }))} />
              </div>

              {flat.length === 0 && (
                query
                  ? <EmptyState title="No matching windows" action={<Button onClick={() => setQuery('')}>Clear search</Button>}>Try another title, description or monitor name.</EmptyState>
                  : view !== 'all'
                    ? <EmptyState title={view === 'active' ? 'No maintenance in progress' : `No ${STATE_LABEL[view].toLowerCase()} maintenance`} action={<Button onClick={() => setView('all')}>View full schedule</Button>}>Other windows are available in the full schedule.</EmptyState>
                    : null
              )}
              {STATE_ORDER.filter(s => grouped[s].some(m => pageIds.has(m.id))).map(s => (
                <section key={s} aria-label={`${STATE_LABEL[s]} windows`}>
                  <h2 className="mb-2.5 flex items-center gap-2 text-base font-semibold text-body">{STATE_LABEL[s]}<span className="tnum rounded-full bg-surface-2 px-2 py-px text-xs font-medium text-muted">{grouped[s].length}</span></h2>
                  <div className={LIST_FRAME}>
                    {grouped[s].filter(m => pageIds.has(m.id)).map(m => (
                      <WindowRow key={m.id} m={m} admin={admin}
                        monitorNames={m.monitorIds.map(monitorName)} projectNames={m.groupIds.map(groupName)}
                        creator={m.createdBy != null ? people.get(m.createdBy) ?? null : null}
                        onEdit={() => openForm(m)} onToggle={() => setActive(m, !m.active)} onDelete={() => setDeleting(m)} />
                    ))}
                  </div>
                </section>
              ))}
              <Pager paging={paging} noun="Windows" />
            </div>
          )}
        </div>
      )}

      {admin && (
        <MaintenanceForm
          open={formOpen}
          onClose={() => setFormOpen(false)}
          editing={editing}
          monitors={[...live.monitors.values()]}
          groups={live.groups}
          onSaved={() => { push(editing ? 'Maintenance window saved.' : 'Maintenance window created.', 'ok'); setNonce(n => n + 1) }}
        />
      )}
      <ConfirmModal
        open={deleting != null}
        title={`Delete "${deleting?.title ?? ''}"?`}
        description="Its monitors stop being held in maintenance by it. This can't be undone."
        confirmLabel="Delete window"
        onConfirm={remove}
        onClose={() => setDeleting(null)}
      />
    </>
  )
}
