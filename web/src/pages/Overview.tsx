import { useEffect, useMemo, useState } from 'react'
import { Link } from 'wouter'
import { PageTitle } from '../components/PageShell'
import { UpdateBanner } from '../components/UpdateBanner'
import { Pager, ShowMoreButton, usePaging, useShowMore } from '../components/Pager'
import { BeatBar, StatusBadge } from '../components/Status'
import { Badge, Button, EmptyState, Icon, ErrorRetry, SkeletonBlock, SkeletonRows, Timestamp, Tooltip } from '../components/ui'
import { QuickLook } from '../components/QuickLook'
import { api, unwrapList } from '../lib/api'
import { dateServer, duration } from '../lib/format'
import { useLive } from '../lib/live'
import { normIncident, normMaintenance, normOutage } from '../lib/normalize'
import { ownMonitors } from '../lib/vendor'
import { severityLabel, severityTone } from '../hooks/useTeam'
import { canWrite, isAdmin as isAdminRole } from '../lib/roles'
import type { Incident, Maintenance, Me, MonitorSummary, Outage } from '../lib/types'

const DAY = 24 * 3600 * 1000

/** On wide screens a section keeps its heading fixed and scrolls its own list, so the page itself never grows. */
function Section({ title, hint, children, className = '' }: { title: string; hint: string; children: React.ReactNode; className?: string }) {
  return (
    <section className={'space-y-2.5 xl:space-y-0 xl:flex xl:flex-col xl:gap-2.5 xl:min-h-0 ' + className}>
      <div className="shrink-0">
        <h2 className="text-base font-semibold text-body">{title}</h2>
        <p className="text-[12px] text-muted">{hint}</p>
      </div>
      <div className="space-y-2.5 xl:relative xl:flex-1 xl:min-h-0 xl:overflow-y-auto">{children}</div>
    </section>
  )
}

function Empty({ title, children }: { title: string; children?: React.ReactNode }) {
  return <EmptyState compact title={title}>{children}</EmptyState>
}

function relative(ms: number, now: number): string {
  const d = ms - now
  return d > 0 ? `in ${duration(d)}` : `${duration(-d)} ago`
}

interface AttentionRow {
  key: string
  monitorId?: number
  kind: 'outage' | 'monitor' | 'incident'
  title: string
  href: string
  status: React.ReactNode
  detail: string
  since: number
  owner?: string
}

export default function Overview({ user }: { user: Me }) {
  const live = useLive()
  const isAdmin = isAdminRole(user) // update banner
  const writer = canWrite(user) // create actions, status page count
  const [incidents, setIncidents] = useState<Incident[]>([])
  const [maint, setMaint] = useState<Maintenance[]>([])
  const [resolved, setResolved] = useState<Outage[]>([])
  const [pages, setPages] = useState<number | null>(null)
  const [regional, setRegional] = useState<{ monitorId: number; name: string; summary: string }[]>([])
  const [loadError, setLoadError] = useState(false)
  const [loading, setLoading] = useState(true)
  const [nonce, setNonce] = useState(0)
  const now = live.lastUpdate || Date.now()
  const [quick, setQuick] = useState<AttentionRow | null>(null)
  const [quickOpen, setQuickOpen] = useState(false)

  useEffect(() => {
    let alive = true
    setLoading(true); setLoadError(false)
    const from = Date.now() - 7 * DAY
    Promise.all([
      api.get<unknown>('/api/incidents'), api.get<unknown>('/api/maintenance'),
      api.get<unknown>('/api/outages?active=false&from=' + from + '&limit=50'),
      api.get<unknown>('/api/regions/issues'), writer ? api.get<unknown>('/api/pages') : Promise.resolve(null),
    ]).then(([i,m,o,r,p]) => {
      if (!alive) return
      setIncidents(unwrapList<unknown>(i, 'incidents').map(normIncident))
      setMaint(unwrapList<unknown>(m, 'maintenance').map(normMaintenance))
      setResolved(unwrapList<unknown>(o, 'outages').map(normOutage))
      setRegional(unwrapList<{ monitorId: number; name: string; summary: string }>(r, 'issues'))
      if (p) setPages(unwrapList<unknown>(p, 'pages').length)
    }).catch(() => { if (alive) setLoadError(true) }).finally(() => { if (alive) setLoading(false) })
    return () => { alive = false }
  }, [writer, live.outages.length, nonce])

  // 3rd-party status feeds are alert-only: never counted here.
  const monitors = useMemo(() => ownMonitors(live.monitors.values()), [live.monitors])
  const outages = useMemo(() => live.outages.filter(o => !live.monitors.get(o.monitorId)?.vendor), [live.outages, live.monitors])
  const name = (id: number) => live.monitors.get(id)?.name ?? `Monitor ${id}`
  const counts = useMemo(() => {
    const c = { total: monitors.length, up: 0, down: 0, degraded: 0, paused: 0 }
    for (const m of monitors) {
      if (m.paused) c.paused++
      else if (m.status === 'down') c.down++
      else if (m.status === 'degraded' || m.status === 'pending') c.degraded++
      else if (m.status === 'up') c.up++
    }
    return c
  }, [monitors])

  const openIncidents = incidents.filter(i => i.status !== 'resolved')
  const maintNow = maint.filter(m => m.state === 'active')
  const maintSoon = maint.filter(m => m.state === 'upcoming')

  const attention = useMemo<AttentionRow[]>(() => {
    const rows: AttentionRow[] = []
    const outageMonitors = new Set<number>()
    for (const o of outages) {
      outageMonitors.add(o.monitorId)
      rows.push({
        key: `o${o.id}`, monitorId: o.monitorId, kind: 'outage', title: name(o.monitorId), href: `/monitors/${o.monitorId}`,
        status: <Badge tone="danger" dot>Down</Badge>, detail: o.firstReason || 'Check failed', since: o.startedAt,
        owner: o.ackedBy ? `Acknowledged by ${o.ackedBy}` : 'Not acknowledged',
      })
    }
    for (const m of monitors as MonitorSummary[]) {
      if (m.paused || outageMonitors.has(m.id)) continue
      if (m.status === 'degraded' || m.status === 'pending' || m.status === 'down') {
        rows.push({ key: `m${m.id}`, monitorId: m.id, kind: 'monitor', title: m.name, href: `/monitors/${m.id}`, status: <StatusBadge status={m.status} size="sm" />, detail: m.reason || '', since: m.since })
      }
    }
    for (const r of regional) {
      if (outageMonitors.has(r.monitorId)) continue
      rows.push({ key: `r${r.monitorId}`, monitorId: r.monitorId, kind: 'monitor', title: r.name, href: `/monitors/${r.monitorId}`, status: <Badge tone="warn">Regional issue</Badge>, detail: r.summary, since: Date.now() })
    }
    for (const i of openIncidents) {
      rows.push({ key: `i${i.id}`, kind: 'incident', title: i.title, href: `/incidents/${i.id}`, status: <Badge tone={severityTone(i.severity) === 'neutral' ? 'warn' : severityTone(i.severity)}>{severityLabel(i.severity)}</Badge>, detail: `Incident · ${i.status}`, since: i.createdAt, owner: i.commanderName ? `Owner: ${i.commanderName}` : 'No owner' })
    }
    return rows.sort((a, b) => b.since - a.since)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [outages, monitors, incidents, regional])

  // With thousands of monitors "needs attention" can be long: page it.
  const attentionPaging = usePaging(attention)
  const maintList = useMemo(() => [...maintNow, ...maintSoon], [maint])
  const maintMore = useShowMore(maintList, 10, 10)

  const activity = useMemo(() => {
    const from = now - 7 * DAY
    const rows: { key: string; at: number; title: string; href: string; what: string; tone: 'ok' | 'neutral' | 'accent' }[] = []
    for (const o of resolved) {
      if (o.endedAt && !live.monitors.get(o.monitorId)?.vendor) rows.push({ key: `o${o.id}`, at: o.endedAt, title: name(o.monitorId), href: `/monitors/${o.monitorId}`, what: `Recovered after ${duration(o.endedAt - o.startedAt)}`, tone: 'ok' })
    }
    for (const i of incidents) {
      if (i.resolvedAt && i.resolvedAt >= from) rows.push({ key: `i${i.id}`, at: i.resolvedAt, title: i.title, href: `/incidents/${i.id}`, what: 'Incident resolved', tone: 'accent' })
    }
    for (const m of maint) {
      if (m.state === 'past' && m.endsAt && m.endsAt >= from) rows.push({ key: `m${m.id}`, at: m.endsAt, title: m.title, href: '/maintenance', what: 'Maintenance finished', tone: 'neutral' })
    }
    return rows.sort((a, b) => b.at - a.at).slice(0, 30)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resolved, incidents, maint, now])

  if (loadError) return <div><PageTitle title="Operations" /><ErrorRetry message="Overview could not load. Current health cannot be confirmed." onRetry={() => setNonce(n => n + 1)} /></div>
  if (loading || !live.lastUpdate) return (
    <div className="space-y-7">
      <SkeletonBlock lines={2} label="Loading current operations" />
      <SkeletonRows rows={6} label="Loading unresolved issues" />
    </div>
  )
  return (
    <div className="space-y-7 xl:space-y-0 xl:flex-1 xl:min-h-0 xl:flex xl:flex-col xl:gap-5">
      {isAdmin && <UpdateBanner user={user} />}
      <div className="xl:shrink-0 xl:[&>[data-page-title]]:mb-0">
        <PageTitle title="Operations" actions={writer && <>
          <Link href="/monitors/new"><Button variant="primary" icon={<Icon.Plus className="w-4 h-4" />}>Add monitor</Button></Link>
          <Link href="/incidents?new=1"><Button icon={<Icon.Alert className="w-4 h-4" />}>Declare incident</Button></Link>
          <Link href="/maintenance?new=1"><Button icon={<Icon.Wrench className="w-4 h-4" />}>Schedule maintenance</Button></Link>
        </>}>Service health, unresolved issues and the latest changes.</PageTitle>
      </div>

      <section className="grid lg:grid-cols-2 gap-5 lg:gap-6 items-center rounded-[14px] border border-default bg-surface p-5 xl:shrink-0">
        <div className="min-w-0">
          <div className="flex items-center gap-3">
            <span className={`w-3 h-3 rounded-full shrink-0 ${!live.connected ? 'bg-warn' : counts.down ? 'bg-danger' : counts.degraded ? 'bg-warn' : counts.up ? 'bg-ok' : 'bg-surface-hover'}`} />
            <h2 className="text-xl font-semibold tracking-tight">{!live.connected ? 'Live updates are paused' : counts.down ? `${counts.down} ${counts.down === 1 ? 'service needs' : 'services need'} a response` : counts.degraded ? 'Some checks need attention' : counts.up ? 'Services are operating normally' : 'Waiting for check results'}</h2>
          </div>
          <p className="text-[13px] text-muted mt-1.5 ml-6"><span className="text-ok font-medium">{counts.up} operational</span> · {counts.total - counts.paused} active · {counts.paused} paused</p>
          <div className="flex gap-0.5 h-2 mt-3 ml-6" aria-hidden="true">{[{ count: counts.up, color: 'bg-ok' }, { count: counts.down, color: 'bg-danger' }, { count: counts.degraded, color: 'bg-warn' }, { count: Math.max(0, counts.total - counts.up - counts.down - counts.degraded), color: 'bg-surface-hover' }].map((part, i) => part.count > 0 && <span key={i} className={`rounded-full ${part.color}`} style={{ flex: part.count }} />)}</div>
        </div>
        <nav aria-label="Operations summary" className="grid grid-cols-2 sm:grid-cols-4 gap-2">
          {[
            { href: '/incidents?tab=outages', label: 'Active outages', value: outages.length, bad: outages.length > 0 },
            { href: '/incidents', label: 'Open incidents', value: openIncidents.length, bad: openIncidents.length > 0 },
            { href: '/maintenance', label: 'Active maintenance', value: maintNow.length, bad: false },
            { href: writer ? '/pages' : '/projects', label: writer ? 'Status pages' : 'Projects', value: writer ? pages ?? '—' : live.groups.length, bad: false },
          ].map(t => (
            <Link key={t.label} href={t.href} className="rounded-[10px] border border-default bg-ground px-3 py-2.5 hover:border-strong hover:bg-surface-hover transition-colors">
              <span className="block text-[11px] text-muted leading-tight">{t.label}</span>
              <strong className={`block text-2xl leading-tight tnum font-semibold ${t.bad ? 'text-danger' : 'text-body'}`}>{t.value}</strong>
            </Link>
          ))}
        </nav>
      </section>
      <div className="grid xl:grid-cols-2 gap-6 items-start xl:items-stretch xl:flex-1 xl:min-h-0 xl:grid-rows-[minmax(0,1fr)]"><div className="xl:min-h-0 xl:flex xl:flex-col">
      <Section className="xl:flex-1" title="Needs attention" hint="Everything currently unresolved, newest first.">
        {attention.length === 0 ? (
          <Empty title={!live.connected ? 'Live updates are paused' : monitors.length === 0 ? 'No monitors yet' : 'Nothing needs attention'}>{!live.connected ? 'Reconnecting; current health cannot be confirmed.' : monitors.length === 0 ? 'Add your first monitor to start watching a service.' : 'No unresolved issues in the current results.'}</Empty>
        ) : (
          <div className="rounded-[14px] border border-default bg-surface divide-y divide-default overflow-hidden">
            {attentionPaging.rows.map(r => (
              <div key={r.key} className="flex items-stretch hover:bg-surface-hover transition-colors">
                <Link href={r.href} className="flex min-w-0 flex-1 flex-wrap items-center gap-x-4 gap-y-1 py-3.5 pl-5 pr-2 md:grid md:grid-cols-[128px_minmax(0,1fr)_84px] 2xl:grid-cols-[128px_minmax(0,1fr)_150px_84px] md:gap-x-4">
                  <span className="shrink-0 justify-self-start">{r.status}</span>
                  <span className="min-w-0 w-full sm:w-auto sm:flex-1 md:w-auto">
                    <span className="block text-sm font-medium text-body truncate">{r.title}</span>
                    {(r.detail || r.owner) && (
                      <span className="block text-xs text-muted truncate">
                        {r.detail}
                        {r.owner && <>{r.detail ? ' · ' : ''}<span className={r.owner.startsWith('Not') || r.owner === 'No owner' ? 'text-warn' : ''}>{r.owner}</span></>}
                      </span>
                    )}
                  </span>
                  <span className="hidden 2xl:flex items-center">
                    {(() => { const mon = r.monitorId != null ? live.monitors.get(r.monitorId) : undefined; return mon && mon.beats ? <BeatBar beats={mon.beats} count={20} size="sm" intervalS={mon.intervalS} lastCheckAt={mon.lastCheckAt} /> : null })()}
                  </span>
                  <span className="text-xs text-muted whitespace-nowrap tnum md:text-right"><Timestamp at={r.since} prefix="since" bare future={false} /></span>
                </Link>
                <div className="flex items-center pr-3">
                  <Tooltip content="Quick look">
                    <button type="button" aria-label={`Quick look at ${r.title}`} onClick={() => { setQuick(r); setQuickOpen(true) }}
                      className="inline-flex h-8 w-8 items-center justify-center rounded-lg text-muted transition-colors hover:bg-surface-2 hover:text-body">
                      <Icon.Expand className="h-4 w-4" />
                    </button>
                  </Tooltip>
                </div>
              </div>
            ))}
          </div>
        )}
        <Pager paging={attentionPaging} noun="Items" />
      </Section>

      </div><aside className="space-y-8 xl:space-y-0 xl:min-h-0 xl:flex xl:flex-col xl:gap-5">
      {(maintNow.length > 0 || maintSoon.length > 0) && (
        <Section className="xl:shrink-0 xl:max-h-[45%]" title="Scheduled maintenance" hint="In progress and upcoming. Alerts are paused for the affected monitors while a window is active.">
          <div className="rounded-[14px] border border-default bg-surface divide-y divide-default overflow-hidden">
            {maintMore.shown.map(m => (
              <Link key={m.id} href="/maintenance" className="grid grid-cols-[128px_minmax(0,1fr)_84px] items-center gap-x-4 px-5 py-3.5 hover:bg-surface-hover transition-colors">
                <span className="justify-self-start">{m.state === 'active' ? <Badge tone="accent" dot>In progress</Badge> : <Badge tone="neutral">{m.startsAt ? `Starts ${relative(m.startsAt, now)}` : 'Upcoming'}</Badge>}</span>
                <span className="min-w-0">
                  <span className="block text-sm font-medium text-body truncate">{m.title}</span>
                  <span className="block text-xs text-muted truncate tnum">{m.kind === 'once' && m.endsAt ? `Until ${dateServer(m.endsAt)}` : m.state === 'active' ? 'In progress' : 'Scheduled'}</span>
                </span>
                <span className="text-xs text-muted whitespace-nowrap tnum text-right">{m.monitorIds.length + m.groupIds.length} {m.monitorIds.length + m.groupIds.length === 1 ? 'target' : 'targets'}</span>
              </Link>
            ))}
          </div>
          <ShowMoreButton hidden={maintMore.hidden} onClick={maintMore.more} noun="windows" />
        </Section>
      )}

      <Section className="xl:flex-1" title="Recent activity" hint="Resolved outages and incidents, and finished maintenance, from the last 7 days.">
        {activity.length === 0 ? (
          <Empty title="No recent activity">Resolved outages, incidents and finished maintenance appear here.</Empty>
        ) : (
          <div className="rounded-[14px] border border-default bg-surface divide-y divide-default overflow-hidden">
            {activity.map(a => (
              <Link key={a.key} href={a.href} className="grid grid-cols-[128px_minmax(0,1fr)_84px] items-center gap-x-4 px-5 py-3.5 hover:bg-surface-hover transition-colors">
                <span className="justify-self-start"><Badge tone={a.tone}>{a.what.split(' ')[0]}</Badge></span>
                <span className="min-w-0">
                  <span className="block text-sm font-medium text-body truncate">{a.title}</span>
                  <span className="block text-xs text-muted truncate">{a.what}</span>
                </span>
                <span className="text-xs text-muted tnum whitespace-nowrap text-right" title={dateServer(a.at)}>{relative(a.at, now)}</span>
              </Link>
            ))}
          </div>
        )}
      </Section>
      </aside></div>
      <QuickLook row={quick} open={quickOpen} onClose={() => setQuickOpen(false)} />
    </div>
  )
}
