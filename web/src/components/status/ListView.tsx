// Service health, current communications, and availability history.
// Public pages omit private check reasons; the API also sanitizes them.
//
// Reading order, top to bottom, in one centered column: the page's identity,
// one overall-status banner, what is happening now (active incidents, then
// scheduled maintenance), the services with their daily history, and the past
// 14 days of incidents.

import { memo, useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { Link } from 'wouter'
import { ago, dateOnlyServer, ms, pct } from '../../lib/format'
import type { MonitorExtra, PublicExtras } from '../../lib/pageSettings'
import type { DailyBar, Incident, OverallStatus, PublicMonitor, PublicPage, Status } from '../../lib/types'
import { fillDays, PillAxis, PillBars } from '../PillBars'
import { Button, Icon, Input, Select } from '../ui'
import { ReconnectingBanner } from '../board/TopStrip'
import { Pager, ShowMoreButton, usePaging, useShowMore } from '../Pager'
import { addDays, longDay, shortDay } from './Calendar'
import { DayModal } from './DayModal'
import { IMPACT_LABEL, IncidentCard, impactTone } from './IncidentCards'
import { MaintenanceBlock } from './MaintenanceBlock'
import { Markdown } from './Markdown'
import { PublicResponseTime } from './PublicResponseTime'
import { AnnouncementBanner, OverallBanner, STATUS_WIDTH, StatusFooter, StatusHeader, StatusShell, withIncidents } from './StatusChrome'
import { ExpandContext, useExpansionState, useFold } from './Fold'
import { useDisplayZone, ymdIn } from './zone'

export interface PageSection {
  key: string
  /** Element id a URL hash can point at (#group-3). */
  anchor: string
  name: string
  description?: string
  monitorIds: number[]
}

/** Tooltip body for one day's bar. */
function BarTip({ bar, clickable, showUptime }: { bar: DailyBar; clickable: boolean; showUptime: boolean }) {
  const incidents = bar.incidents ?? []
  const status = bar.uptime == null
    ? { text: 'No data recorded', cls: 'text-muted', dot: 'bg-strong' }
    : bar.down > 0
      ? { text: bar.downMinutes ? `About ${bar.downMinutes} min of downtime` : 'Some downtime', cls: bar.uptime < 99 ? 'text-danger' : 'text-warn', dot: bar.uptime < 99 ? 'bg-danger' : 'bg-warn' }
      : { text: 'No downtime', cls: 'text-ok', dot: 'bg-ok' }
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-baseline justify-between gap-3">
        <span className="font-semibold text-body text-[12px]">{dateOnlyServer(bar.day)}</span>
        {showUptime && bar.uptime != null && <span className="font-mono tnum text-body-2">{pct(bar.uptime)}</span>}
      </div>
      <p className={`flex items-center gap-1.5 font-medium ${status.cls}`}>
        <span aria-hidden="true" className={`w-1.5 h-1.5 rounded-full ${status.dot}`} />
        {status.text}
      </p>
      {incidents.length > 0 && (
        <ul className="flex flex-col gap-1.5 border-t border-default pt-2">
          {incidents.slice(0, 3).map(i => (
            <li key={i.id} className="flex items-start gap-2">
              <span aria-hidden="true" className={`mt-[3px] w-1 h-3 rounded-full shrink-0 ${impactTone(i.impact) === 'danger' ? 'bg-danger' : impactTone(i.impact) === 'accent' ? 'bg-accent' : 'bg-warn'}`} />
              <span className="text-body-2 leading-snug">{i.title}</span>
            </li>
          ))}
          {incidents.length > 3 && <li className="text-faint pl-3">+{incidents.length - 3} more</li>}
        </ul>
      )}
      {clickable && bar.uptime != null && <p className="text-[11px] text-faint">Click for the day’s timeline</p>}
    </div>
  )
}

const tipFor = (showUptime: boolean) => (bar: DailyBar) => <BarTip bar={bar} clickable showUptime={showUptime} />
const tipStaticFor = (showUptime: boolean) => (bar: DailyBar) => <BarTip bar={bar} clickable={false} showUptime={showUptime} />

function meanOf(values: (number | undefined)[]): number | null {
  const v = values.filter((x): x is number => x != null)
  return v.length > 0 ? v.reduce((a, b) => a + b, 0) / v.length : null
}

/** Worst-day roll-up of several components' bars, for a collapsed group. */
function combineBars(strips: DailyBar[][]): DailyBar[] {
  if (strips.length === 0) return []
  return strips[0].map((first, i) => {
    let uptime: number | null = null
    let down = 0
    let downMinutes = 0
    const incidents = new Map<number, { id: number; title: string; impact: string }>()
    for (const s of strips) {
      const b = s[i]
      if (!b) continue
      if (b.uptime != null) uptime = uptime == null ? b.uptime : Math.min(uptime, b.uptime)
      down += b.down
      downMinutes = Math.max(downMinutes, b.downMinutes ?? 0)
      for (const inc of b.incidents ?? []) incidents.set(inc.id, inc)
    }
    return { day: first.day, uptime, down, downMinutes, incidents: [...incidents.values()] }
  })
}

type Glyph = 'check' | 'alert' | 'wrench' | 'pause' | 'dot'

const ROW_STATUS: Record<Status, { text: string; cls: string; icon: Glyph; fill: string }> = {
  up: { text: 'Operational', cls: 'text-ok', icon: 'check', fill: 'bg-ok' },
  degraded: { text: 'Degraded', cls: 'text-warn', icon: 'alert', fill: 'bg-warn' },
  pending: { text: 'Check pending', cls: 'text-warn', icon: 'alert', fill: 'bg-warn' },
  down: { text: 'Outage', cls: 'text-danger', icon: 'alert', fill: 'bg-danger' },
  maintenance: { text: 'Maintenance', cls: 'text-accent', icon: 'wrench', fill: 'bg-accent' },
  paused: { text: 'Paused', cls: 'text-muted', icon: 'pause', fill: 'bg-strong' },
  unknown: { text: 'No data', cls: 'text-muted', icon: 'dot', fill: 'bg-strong' },
}

/** A round, filled badge with a shape per state (check, triangle, wrench, pause, dot). */
function RoundGlyph({ icon, fill }: { icon: Glyph; fill: string }) {
  return (
    <span aria-hidden="true" className={`w-5 h-5 rounded-full inline-flex items-center justify-center shrink-0 text-surface ${fill}`}>
      {icon === 'check' && <Icon.Check className="w-3 h-3" />}
      {icon === 'alert' && <Icon.AlertTriangle className="w-3 h-3" />}
      {icon === 'wrench' && <Icon.Wrench className="w-3 h-3" />}
      {icon === 'pause' && <Icon.Pause className="w-3 h-3" />}
      {icon === 'dot' && <span className="w-1.5 h-1.5 rounded-full bg-current" />}
    </span>
  )
}

/** Uptime right-aligned with the state's round icon. The state is always in
 * words: visibly when it is not "Operational" or when no uptime is shown. */
function StateReadout({ icon, text, cls, fill, uptime }: { icon: Glyph; text: string; cls: string; fill: string; uptime: number | null }) {
  const wordVisible = uptime == null || text !== 'Operational'
  return (
    <span className={`inline-flex items-center gap-2 text-[13px] shrink-0 ${cls}`}>
      {uptime != null && <span className="font-mono tnum font-medium text-body-2">{pct(uptime)}</span>}
      {wordVisible ? <span className="font-medium">{text}</span> : <span className="sr-only">{text}</span>}
      <RoundGlyph icon={icon} fill={fill} />
    </span>
  )
}

const MonitorRow = memo(function MonitorRow({
  m, bars, description, uptime, isPublic, showLatency, onDay, extra, tip,
}: {
  extra?: MonitorExtra
  tip: (bar: DailyBar) => ReactNode
  m: PublicMonitor
  bars: DailyBar[]
  description?: string
  uptime: number | null
  isPublic: boolean
  showLatency: boolean
  onDay: (m: PublicMonitor, index: number) => void
}) {
  const latency = (showLatency || !isPublic) && m.latencyMs != null && m.latencyMs > 0 ? ms(m.latencyMs) : null
  const reason = !isPublic && m.status !== 'up' && m.reason ? m.reason : null
  const indexOf = useMemo(() => new Map(bars.map((b, i) => [b.day, i])), [bars])

  const st = ROW_STATUS[m.status] ?? ROW_STATUS.unknown
  return (
    <li className="flex flex-col gap-3 px-4 sm:px-5 py-4 sm:py-5 hover:bg-surface-2/40 transition-colors">
      <div className="flex items-start justify-between gap-x-4 gap-y-1">
        <div className="min-w-0">
          <span className="flex items-center gap-2 font-mono text-[14px] font-semibold text-body break-words">
            {m.name}
            {description && <span title={description}><Icon.Info className="w-3.5 h-3.5 text-faint shrink-0" /></span>}
          </span>
          {description && <p className="mt-0.5 text-[13px] text-muted break-words">{description}</p>}
          {latency && <span className="text-[12px] text-muted tnum">{latency}</span>}
          {extra?.certDays != null && <p className="mt-1 text-[12px] text-muted tnum">{extra.certDays < 0 ? 'Certificate expired' : `Certificate valid for ${extra.certDays} ${extra.certDays === 1 ? 'day' : 'days'}`}</p>}
          {extra?.lastCheckAt != null && <p className="mt-1 text-[12px] text-muted tnum">Last checked <time dateTime={new Date(extra.lastCheckAt).toISOString()} title={new Date(extra.lastCheckAt).toLocaleString()}>{ago(extra.lastCheckAt)}</time></p>}
          {reason && <p className="mt-1 text-[12px] text-danger break-words">{reason}</p>}
        </div>
        <StateReadout {...st} uptime={uptime} />
      </div>
      <PillBars bars={bars} onDay={day => onDay(m, indexOf.get(day) ?? bars.length - 1)} label={`${m.name} daily uptime`} tip={tip} />
    </li>
  )
})

function groupSummary(members: PublicMonitor[]): { text: string; icon: Glyph; cls: string; fill: string } {
  const down = members.filter(m => m.status === 'down').length
  const issues = members.filter(m => m.status === 'degraded' || m.status === 'pending').length
  if (down > 0) return { text: down === members.length ? 'Major outage' : 'Partial outage', icon: 'alert', cls: 'text-danger', fill: 'bg-danger' }
  if (issues > 0) return { text: 'Degraded performance', icon: 'alert', cls: 'text-warn', fill: 'bg-warn' }
  if (members.some(m => m.status === 'maintenance')) return { text: 'Under maintenance', icon: 'wrench', cls: 'text-accent', fill: 'bg-accent' }
  if (members.every(m => m.status === 'up')) return { text: 'Operational', icon: 'check', cls: 'text-ok', fill: 'bg-ok' }
  return { text: `${members.filter(m => m.status === 'up').length} of ${members.length} operational`, icon: 'dot', cls: 'text-muted', fill: 'bg-strong' }
}

const SERVICE_SIZES = [8, 25, 50] as const

function ComponentGroup({
  s, members, bars, renderRow, days, uptime, revealKey, tipStatic, flat, keepClosedBody,
}: {
  tipStatic: (bar: DailyBar) => ReactNode
  s: PageSection
  members: PublicMonitor[]
  bars: Map<number, DailyBar[]>
  /** Mean uptime of the group's components over the window, for the collapsed strip. */
  uptime: number | null
  renderRow: (m: PublicMonitor) => ReactNode
  days: number
  revealKey: string
  /** A page with no groups: one card of rows, no collapsible header. */
  flat: boolean
  /** Keep a closed group's rows in the page (hidden until found) so in-page search reaches them. */
  keepClosedBody: boolean
}) {
  const sum = groupSummary(members)
  // A group can hold hundreds of services, each with a 90-day strip.
  const paging = usePaging(members, { pageSize: 8, resetKey: revealKey })
  const fold = useFold(s.anchor)
  const { open, setOpen } = fold
  // Searching or filtering opens the groups that match. Nothing else opens a
  // group by itself, not even one with a problem: its header names the status.
  useEffect(() => { if (revealKey) setOpen(true) }, [revealKey, setOpen])
  const combined = useMemo(() => combineBars(members.map(m => bars.get(m.id) ?? [])), [members, bars])

  const footer = (
    <div className="border-t border-default bg-surface-2/50 px-4 sm:px-5 py-2.5 rounded-b-[14px]">
      <PillAxis days={days} live />
    </div>
  )

  if (flat) {
    return (
      <section id={s.anchor} aria-label={s.name} className="rounded-[14px] border border-default bg-surface">
        <ul className="divide-y divide-default">{paging.rows.map(renderRow)}</ul>
        {members.length > 8 && <div className="border-t border-default px-4 py-3"><Pager paging={paging} noun="Services" sizes={SERVICE_SIZES} /></div>}
        {footer}
      </section>
    )
  }

  return (
    // No overflow-hidden: the day tooltips rise above the card's top edge.
    <section id={s.anchor} aria-labelledby={`${fold.bodyId}-h`} className="scroll-mt-20 rounded-[14px] border border-default bg-surface">
      <h3 id={`${fold.bodyId}-h`} className="m-0">
        <button
          type="button"
          onClick={fold.toggle}
          aria-expanded={open}
          aria-controls={fold.bodyId}
          className={`w-full flex items-start gap-3 px-4 sm:px-5 py-4 text-left hover:bg-surface-hover/40 transition-colors ${open ? 'rounded-t-[14px]' : 'rounded-t-[14px] rounded-b-none'}`}
        >
          <Icon.Chevron className={`w-4 h-4 mt-1 text-muted shrink-0 transition-transform motion-reduce:transition-none ${open ? 'rotate-90' : ''}`} />
          <span className="min-w-0 flex-1">
            <span className="font-mono text-[14px] font-semibold text-body break-words block">{s.name}</span>
            <span className="text-[12px] text-muted block tnum break-words">{members.length} {members.length === 1 ? 'service' : 'services'}{s.description ? ` · ${s.description}` : ''}</span>
          </span>
          <StateReadout {...sum} uptime={uptime} />
        </button>
      </h3>
      {!open && (
        <div className="px-4 sm:px-5 pb-4">
          <PillBars bars={combined} label={`${s.name} daily uptime (worst component each day)`} tip={tipStatic} />
        </div>
      )}
      {(open || keepClosedBody) && (
        <div ref={fold.bodyRef} id={fold.bodyId}>
          <ul className="divide-y divide-default border-t border-default">
            {paging.rows.map(renderRow)}
          </ul>
          {members.length > 8 && <div className="border-t border-default px-4 py-3"><Pager paging={paging} noun="Services" sizes={SERVICE_SIZES} /></div>}
        </div>
      )}
      {footer}
    </section>
  )
}

const IMPACT_RANK: Record<string, number> = { major: 3, minor: 2, maintenance: 1 }

/** One day of the past-incidents list. A day with incidents is collapsed to a
 * count and its worst impact; a quiet day says so and has nothing to open. */
function PastDay({ dayKey, today, items, base, tz }: { dayKey: string; today: boolean; items: Incident[]; base: string; tz: string }) {
  const fold = useFold(`day-${dayKey}`)
  const more = useShowMore(items, 3, 10, dayKey)
  const worst = items.reduce<string>((w, i) => ((IMPACT_RANK[i.impact] ?? 0) > (IMPACT_RANK[w] ?? 0) ? i.impact : w), '')
  const full = `${longDay(dayKey)}${today ? ', today' : ''}`
  // One compact line: a chevron slot (empty on a quiet day, so every date
  // starts at the same place), the short date, and "Today" as a small badge.
  const label = (
    <span className="inline-flex h-6 items-center gap-1.5 whitespace-nowrap">
      {items.length > 0
        ? <Icon.Chevron className={`h-3.5 w-3.5 shrink-0 text-muted transition-transform motion-reduce:transition-none ${fold.open ? 'rotate-90' : ''}`} />
        : <span aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />}
      <span className="tnum">{shortDay(dayKey)}</span>
      {today && <span className="rounded-full bg-surface-2 px-1.5 py-px text-[10px] font-semibold uppercase tracking-wide text-muted">Today</span>}
    </span>
  )
  return (
    <li id={`day-${dayKey}`} className="grid scroll-mt-20 gap-x-6 gap-y-1 border-t border-default py-3 first:border-t-0 first:pt-0 sm:grid-cols-[170px_minmax(0,1fr)]">
      {items.length === 0 ? (
        <>
          <h3 title={full} aria-label={full} className="text-[13px] font-semibold leading-6 text-body-2">{label}</h3>
          <p className="text-[13px] leading-6 text-muted">No incidents reported</p>
        </>
      ) : (
        <>
          <h3 className="text-[13px] font-semibold leading-6 text-body-2">
            <button type="button" title={full} aria-label={`${full}, ${items.length} ${items.length === 1 ? 'incident' : 'incidents'}`} aria-expanded={fold.open} aria-controls={fold.bodyId} onClick={fold.toggle} className="rounded-[10px] text-left hover:text-body">
              {label}
            </button>
          </h3>
          <div className="flex min-w-0 flex-col gap-4">
            {!fold.open && <p className="text-[13px] leading-6 text-body-2">{items.length} {items.length === 1 ? 'incident' : 'incidents'}{worst ? ` · highest impact ${IMPACT_LABEL[worst] ?? worst}` : ''}</p>}
            <div ref={fold.bodyRef} id={fold.bodyId} className="flex min-w-0 flex-col gap-5">
              {more.shown.map(i => <IncidentCard key={i.id} incident={i} past={i.status === 'resolved'} href={`${base}/incidents/${i.id}`} variant="flat" tz={tz} />)}
              <ShowMoreButton hidden={more.hidden} step={more.step} onClick={more.more} noun="incidents" />
            </div>
          </div>
        </>
      )}
    </li>
  )
}

function Updated({ at }: { at: number }) {
  const [, tick] = useState(0)
  useEffect(() => {
    const t = window.setInterval(() => tick(n => n + 1), 5000)
    return () => window.clearInterval(t)
  }, [])
  return <time dateTime={new Date(at).toISOString()} title={new Date(at).toLocaleString()}>Updated {new Date(at).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })} · {ago(at)}</time>
}

/** The last `count` calendar days in `tz`, newest first, each with the
 * incidents that started on it. Days with nothing stay in the list: a quiet
 * day is information too. */
function lastDays(incidents: Incident[], tz: string, count: number): { key: string; items: Incident[] }[] {
  const byDay = new Map<string, Incident[]>()
  for (const i of incidents) {
    const k = ymdIn(i.createdAt, tz)
    byDay.set(k, [...(byDay.get(k) ?? []), i])
  }
  const today = ymdIn(Date.now(), tz)
  return Array.from({ length: count }, (_, n) => {
    const key = addDays(today, -n)
    return { key, items: (byDay.get(key) ?? []).sort((a, b) => b.createdAt - a.createdAt) }
  })
}

const PAST_DAYS = 14

export function ListView({
  slug, data, extra, sections, monitors, overall, connected, receivedAt = 0, refreshWarning,
}: {
  extra: PublicExtras
  slug: string
  data: PublicPage
  sections: PageSection[]
  monitors: ReadonlyMap<number, PublicMonitor>
  overall: OverallStatus
  connected: boolean
  counts?: Record<Status, number>
  receivedAt?: number
  refreshWarning?: ReactNode
}) {
  const isPublic = data.page.visibility === 'public'
  const [loadedAt] = useState(() => Date.now())
  const branding = data.page.branding
  const { settings } = extra
  const zone = useDisplayZone()
  const tip = useMemo(() => tipFor(settings.showUptime), [settings.showUptime])
  const tipStatic = useMemo(() => tipStaticFor(settings.showUptime), [settings.showUptime])
  const [historyDays, setHistoryDays] = useState<number | null>(null)
  const availableDays = Math.max(1, branding.historyDays)
  const days = Math.min(historyDays ?? availableDays, availableDays)
  const ranges = [...new Set([7, 30, 90, availableDays].filter(n => n <= availableDays))].sort((a, b) => a - b)
  const [query, setQuery] = useState('')
  const [issuesOnly, setIssuesOnly] = useState(false)
  const [expansion, setAllOpen] = useExpansionState(settings.defaultExpanded)
  const base = `/status/${encodeURIComponent(slug)}`
  const [dayFor, setDayFor] = useState<{ monitor: PublicMonitor; index: number } | null>(null)
  const openDay = useCallback((monitor: PublicMonitor, index: number) => setDayFor({ monitor, index }), [])

  const bars = useMemo(() => {
    const out = new Map<number, DailyBar[]>()
    for (const s of sections) for (const id of s.monitorIds) out.set(id, fillDays(data.daily.get(id) ?? [], days))
    return out
  }, [data.daily, sections, days])

  const { active: activeIncidents, past: pastIncidents } = data.incidents
  const { active: activeMaint, upcoming: upcomingMaint } = data.maintenance
  const sectionMembers = sections
    .map(s => ({ ...s, members: s.monitorIds.map(id => monitors.get(id)).filter((m): m is PublicMonitor => m != null) }))
    .filter(s => s.members.length > 0)

  const onPage = sectionMembers.flatMap(s => s.members)
  const unique = new Map(onPage.map(m => [m.id, m]))
  const all = [...unique.values()]
  const down = all.filter(m => m.status === 'down').length
  const affected = all.filter(m => m.status === 'down' || m.status === 'degraded' || m.status === 'pending').length
  const flat = sectionMembers.length === 1 && sectionMembers[0].key === 'loose' && sectionMembers[0].name === 'Services'
  // Search and filters earn their space only once there is something to search.
  const showTools = unique.size > 6

  const pastDays = useMemo(() => lastDays(pastIncidents, zone.tz, PAST_DAYS), [pastIncidents, zone.tz])
  const activeMore = useShowMore(activeIncidents, 2, 10)
  const mergedOverall = withIncidents(overall, activeIncidents)
  const quietDays = pastDays.filter((d, n) => d.items.length === 0 && n !== 0).length

  const needle = query.trim().toLocaleLowerCase()
  const shown = sectionMembers.map(s => ({ ...s, members: s.members.filter(m => {
    const matches = !needle || `${s.name} ${m.name} ${data.monitorDescriptions.get(m.id) ?? ''}`.toLocaleLowerCase().includes(needle)
    return matches && (!issuesOnly || (m.status !== 'up' && m.status !== 'paused'))
  }) })).filter(s => s.members.length > 0)
  const matching = new Set(shown.flatMap(s => s.members.map(m => m.id))).size
  const filtering = needle !== '' || issuesOnly

  const renderRow = (m: PublicMonitor) => (
    <MonitorRow
      key={m.id}
      m={m}
      bars={bars.get(m.id) ?? []}
      description={data.monitorDescriptions.get(m.id)}
      uptime={settings.showUptime && days === availableDays ? data.uptime.get(m.id) ?? null : null}
      extra={extra.extras.get(m.id)}
      tip={tip}
      isPublic={isPublic}
      showLatency={data.page.showLatency}
      onDay={openDay}
    />
  )

  return (
    <ExpandContext.Provider value={expansion}>
    <StatusShell branding={branding} settings={settings} isPublic={isPublic} feedAvailable={extra.feedAvailable} canonical slug={slug}>
      <a href="#main" className="skip-link">Skip to content</a>
      <StatusHeader slug={slug} title={data.page.title} branding={branding} settings={settings} isPublic={isPublic} feedAvailable={extra.feedAvailable} />

      {!connected && receivedAt > 0 && <ReconnectingBanner receivedAt={receivedAt} />}

      <main id="main" className={`flex-1 w-full ${STATUS_WIDTH} mx-auto px-4 sm:px-6 py-4 sm:py-6 flex flex-col gap-5 sm:gap-6 break-words`}>
        {refreshWarning}
        <AnnouncementBanner banner={settings.banner} />
        {/* Identity and overall status share one row (stacked on phones) so the page opens on its answer. */}
        <div className="grid md:grid-cols-[minmax(0,1fr)_minmax(360px,460px)] gap-4 md:gap-8 items-center">
        <PageIdentity title={data.page.title} description={data.page.description} markdown={settings.descriptionMd} isPublic={isPublic} website={branding.websiteUrl} />
        <OverallBanner
          overall={mergedOverall}
          note={mergedOverall !== overall ? activeIncidents[0]?.title : undefined}
          connected={connected}
          connecting={receivedAt === 0}
          downCount={down}
          affectedCount={affected}
          totalCount={all.length}
          unconfirmedCount={all.filter(m => m.status === 'unknown' || m.status === 'paused').length}
          updated={<Updated at={receivedAt || loadedAt} />}
        />
        </div>

        {activeIncidents.length > 0 && (
          <section aria-labelledby="incidents-h" className="flex flex-col gap-3">
            <h2 id="incidents-h" className="text-[16px] font-semibold text-body flex items-center gap-2">
              <Icon.AlertTriangle className="w-4 h-4 text-danger" />
              Active incidents
            </h2>
            {activeMore.shown.map(i => <IncidentCard key={i.id} incident={i} href={`${base}/incidents/${i.id}`} tz={zone.tz} />)}
            <ShowMoreButton hidden={activeMore.hidden} step={activeMore.step} onClick={activeMore.more} noun="incidents" />
          </section>
        )}

        {(activeMaint.length > 0 || upcomingMaint.length > 0) && <MaintenanceBlock active={activeMaint} upcoming={upcomingMaint} zone={zone} />}

        {sectionMembers.length > 0 && (
          <section aria-labelledby="components-h" className="flex flex-col gap-4">
            <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
              <h2 id="components-h" className="text-[16px] font-semibold text-body">Service availability</h2>
              <div className="flex items-center gap-2">
                {!flat && <Button variant="ghost" size="sm" onClick={() => setAllOpen(!expansion.open)}>{expansion.open ? 'Collapse all' : 'Expand all'}</Button>}
                {ranges.length > 1 && <Select aria-label="Availability history range" value={days} onChange={e => setHistoryDays(Number(e.target.value))} className="!h-8 !text-[12px]">{ranges.map(n => <option key={n} value={n}>Past {n} days</option>)}</Select>}
              </div>
            </div>
            {showTools && (
              <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
                <Input type="search" aria-label="Search services" placeholder="Search services…" value={query} onChange={e => setQuery(e.target.value)} className="w-full sm:w-72" />
                <label className="inline-flex items-center gap-2 text-[13px] text-body-2"><input type="checkbox" checked={issuesOnly} onChange={e => setIssuesOnly(e.target.checked)} className="accent-accent" />Issues only</label>
                <p role="status" className="text-[12px] text-muted sm:ml-auto">{filtering ? `${matching} of ${unique.size} services${issuesOnly ? ' with issues' : ''}${needle ? ` matching “${query.trim()}”` : ''}` : `${unique.size} services`}</p>
              </div>
            )}
            {shown.length === 0 && <div className="py-8 text-center border-y border-default"><p className="text-[14px] text-body">No services match these filters.</p><Button variant="ghost" size="sm" onClick={() => { setQuery(''); setIssuesOnly(false) }}>Clear filters</Button></div>}
            {shown.map(s => (
              <ComponentGroup
                key={s.key}
                s={s}
                members={s.members}
                bars={bars}
                days={days}
                flat={flat}
                keepClosedBody={unique.size <= 40}
                revealKey={needle || (issuesOnly ? 'issues-only' : '')}
                uptime={settings.showUptime && days === availableDays ? meanOf(s.members.map(m => data.uptime.get(m.id))) : null}
                renderRow={renderRow}
                tipStatic={tipStatic}
              />
            ))}
            <Legend />
          </section>
        )}

        {branding.showResponseTime && all.length > 0 && (
          <PublicResponseTime slug={slug} components={all.map(m => ({ id: m.id, name: m.name }))} />
        )}

        <section aria-labelledby="past-h" className="flex flex-col gap-3">
          <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
            <h2 id="past-h" className="text-[16px] font-semibold text-body">Past incidents</h2>
            <div className="flex items-center gap-3">
              {pastDays.some(d => d.items.length > 0) && <Button variant="ghost" size="sm" onClick={() => setAllOpen(!expansion.open)}>{expansion.open ? 'Collapse all' : 'Expand all'}</Button>}
              <Link href={`${base}/history`} className="text-[13px] font-medium text-accent hover:underline">Full incident history →</Link>
            </div>
          </div>
          <ol className="flex flex-col">
            {pastDays.map((d, n) => (d.items.length > 0 || n === 0) && <PastDay key={d.key} dayKey={d.key} today={n === 0} items={d.items} base={base} tz={zone.tz} />)}
          </ol>
          {quietDays > 0 && <p className="text-[13px] text-muted">No incidents reported on the other {quietDays} {quietDays === 1 ? 'day' : 'days'} of the last {pastDays.length}.</p>}
        </section>
      </main>

      <StatusFooter branding={branding} settings={settings} zone={zone} />

      {dayFor && (
        <DayModal
          slug={slug}
          monitor={dayFor.monitor}
          bars={bars.get(dayFor.monitor.id) ?? []}
          index={dayFor.index}
          isPublic={isPublic}
          showUptime={settings.showUptime}
          base={base}
          onIndex={index => setDayFor(d => d && { ...d, index })}
          onClose={() => setDayFor(null)}
        />
      )}
    </StatusShell>
    </ExpandContext.Provider>
  )
}

/** Page title and the owner's description (markdown when set, else the plain description). */
function PageIdentity({ title, description, markdown, isPublic, website }: { title: string; description: string; markdown: string; isPublic: boolean; website?: string }) {
  return (
    <div className="break-words min-w-0">
      <h1 className="text-[24px] sm:text-[28px] font-semibold tracking-tight leading-tight break-words">{title}</h1>
      {markdown
        ? <Markdown source={markdown} className="mt-1.5 text-[14px] text-muted max-w-[65ch]" />
        : description && <p className="mt-1.5 text-[14px] leading-relaxed text-muted max-w-[65ch] line-clamp-3">{description}</p>}
      <div className="mt-2 flex flex-wrap items-center gap-x-5 gap-y-1.5 text-[13px]">
        {!isPublic && <span className="inline-flex items-center gap-1.5 text-muted"><Icon.Lock className="w-3.5 h-3.5" />Private status page</span>}
        {website && <a href={website} target="_blank" rel="noopener noreferrer" className="inline-flex gap-1.5 items-center text-accent hover:underline">Visit website<Icon.External className="w-3.5 h-3.5" /></a>}
      </div>
    </div>
  )
}

function Legend() {
  const items: { label: string; cls: string }[] = [
    { label: 'No downtime', cls: 'bg-ok' },
    { label: 'Some downtime', cls: 'bg-warn' },
    { label: 'Major downtime', cls: 'bg-danger' },
    { label: 'No data', cls: 'bg-strong/30' },
  ]
  return (
    <ul className="flex flex-wrap gap-x-4 gap-y-1 text-[12px] text-muted" aria-label="Bar colours">
      {items.map(i => (
        <li key={i.label} className="inline-flex items-center gap-1.5">
          <span aria-hidden="true" className={`w-1.5 h-3.5 rounded-full ${i.cls}`} />
          {i.label}
        </li>
      ))}
    </ul>
  )
}
