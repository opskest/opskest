// The top of a monitor's page: a header card (state icon, state pill, how long
// it has been in this state, the target, Refresh), a metadata row, a four-up
// stat strip in one hairline-divided card, and the 90-day uptime history as
// rounded pills. Everything comes from data the page already has (the live
// summary, the monitor's recorded outages, GET /api/monitors/:id/daily).

import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { api } from '../../lib/api'
import { dateOnlyServer, duration, ms, pct } from '../../lib/format'
import { normDaily } from '../../lib/normalize'
import type { DailyBar, MonitorSummary, Status } from '../../lib/types'
import { PillAxis, PillBars, fillDays, pillTone } from '../PillBars'
import { statusColor } from '../Status'
import { Timestamp } from '../Timestamp'
import { Button, Icon } from '../ui'

const STATE_WORD: Record<Status, string> = {
  up: 'Operational', down: 'Down', degraded: 'Degraded', pending: 'Check pending', maintenance: 'In maintenance', paused: 'Paused', unknown: 'No data yet',
}

/** "3 days, 4 hours, 12 minutes": the three largest non-zero units. */
export function longSpan(span: number): string {
  if (!Number.isFinite(span) || span < 60_000) return 'less than a minute'
  const units: [string, number][] = [['day', 86_400_000], ['hour', 3_600_000], ['minute', 60_000]]
  let rest = span
  const out: string[] = []
  for (const [name, size] of units) {
    const n = Math.floor(rest / size)
    rest -= n * size
    if (n > 0) out.push(`${n} ${name}${n === 1 ? '' : 's'}`)
  }
  return out.join(', ')
}

function StateIcon({ status }: { status: Status }) {
  const cls = 'w-6 h-6'
  switch (status) {
    case 'up': return <Icon.Check className={cls} />
    case 'down': return <Icon.Alert className={cls} />
    case 'degraded':
    case 'pending': return <Icon.AlertTriangle className={cls} />
    case 'maintenance': return <Icon.Wrench className={cls} />
    case 'paused': return <Icon.Pause className={cls} />
    default: return <Icon.Activity className={cls} />
  }
}

export function MonitorHeaderCard({ m, project, onRefresh, refreshing, onCopy }: {
  m: MonitorSummary
  project?: string
  onRefresh: () => void
  refreshing?: boolean
  onCopy: (text: string, label: string) => void
}) {
  const status: Status = m.paused ? 'paused' : m.maint && m.status === 'up' ? 'maintenance' : m.status
  const c = statusColor(status)
  const word = STATE_WORD[status]
  const headline = status === 'paused' || status === 'unknown' || !m.since ? word : `${word} for ${longSpan(Date.now() - m.since)}`
  const latest = m.paused
    ? 'Paused'
    : !m.lastCheckAt ? 'No check yet'
      : m.status === 'down' ? (m.reason || 'Check failed')
        : m.latencyMs >= 0 ? `Responded in ${ms(m.latencyMs)}` : 'Check passed'
  const facts: [string, ReactNode][] = [
    ['Latest result', <span key="l" className={m.status === 'down' && !m.paused ? 'text-danger' : ''}>{latest}</span>],
    ['Checks every', duration(m.intervalS * 1000)],
    ['Type', m.type.toUpperCase()],
    ...(project ? [['Project', project] as [string, ReactNode]] : []),
    ...(m.certDays != null ? [['TLS expires', `${m.certDays} days`] as [string, ReactNode]] : []),
  ]
  return (
    <section aria-label="Monitor status" className="rounded-[14px] border border-default bg-surface">
      <div className="flex flex-wrap items-start gap-x-5 gap-y-4 p-4 sm:p-5">
        <span aria-hidden="true" className={`w-14 h-14 rounded-[14px] flex items-center justify-center shrink-0 ${c.bg} ${c.text}`}><StateIcon status={status} /></span>
        <div className="min-w-0 flex-1 basis-64 flex flex-col gap-1.5">
          <span className={`self-start inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-[12px] font-semibold ${c.bg} ${c.text}`}>
            <span aria-hidden="true" className="w-1.5 h-1.5 rounded-full bg-current" />{word}
          </span>
          <h2 className="text-[20px] sm:text-[22px] font-semibold leading-tight tracking-tight text-body [overflow-wrap:anywhere]">{headline}</h2>
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1.5 font-mono text-[12px] text-muted min-w-0">
            <a href={m.target} target="_blank" rel="noreferrer" title={m.target} className="inline-flex items-center gap-1 min-w-0 max-w-full text-accent hover:underline">
              <span className="truncate">{m.target}</span><Icon.External className="w-3 h-3 opacity-70 shrink-0" />
            </a>
            <button type="button" onClick={() => onCopy(m.target, 'target URL')} className="inline-flex items-center gap-1 font-sans text-[12px] text-muted hover:text-body px-2 py-0.5 rounded-md bg-surface-2 hover:bg-surface-hover transition-colors border border-default">
              <Icon.Copy className="w-3 h-3" />URL
            </button>
            <button type="button" onClick={() => onCopy(`curl -Iv "${m.target}"`, 'cURL command')} className="inline-flex items-center gap-1 font-sans text-[12px] text-muted hover:text-body px-2 py-0.5 rounded-md bg-surface-2 hover:bg-surface-hover transition-colors border border-default">
              <Icon.Copy className="w-3 h-3" />cURL
            </button>
          </div>
        </div>
        <div className="flex flex-col items-start sm:items-end gap-1.5 shrink-0">
          <Button variant="secondary" size="sm" busy={refreshing} onClick={onRefresh} icon={<Icon.Refresh className="w-4 h-4 text-muted" />}>Refresh</Button>
          <span className="text-[12px] text-muted tnum">{m.lastCheckAt ? <Timestamp at={m.lastCheckAt} prefix="Checked" future={false} /> : 'Not checked yet'}</span>
        </div>
      </div>
      <dl className="flex flex-wrap gap-x-8 gap-y-2 border-t border-default px-4 sm:px-5 py-3 text-[13px] rounded-b-[14px] bg-surface-2/40">
        {facts.map(([k, v]) => (
          <div key={k} className="flex items-baseline gap-2 min-w-0">
            <dt className="text-muted shrink-0">{k}</dt>
            <dd className="text-body-2 font-medium min-w-0 [overflow-wrap:anywhere] line-clamp-2">{v}</dd>
          </div>
        ))}
      </dl>
    </section>
  )
}

interface OutageLike { startedAt?: number; endedAt?: number | null }
const OUTAGE_PAGE = 25

/** Milliseconds of recorded downtime inside the last `windowMs`, or null when
 * the loaded outages (the newest 25) might not reach back that far. */
function downtimeIn(outages: OutageLike[], windowMs: number): number | null {
  const now = Date.now()
  const from = now - windowMs
  if (outages.length >= OUTAGE_PAGE) {
    const oldest = Math.min(...outages.map(o => o.startedAt ?? now))
    if (oldest > from) { /* the page covers the whole window */ } else return null
  }
  let total = 0
  for (const o of outages) {
    if (!o.startedAt) continue
    const end = o.endedAt || now
    total += Math.max(0, Math.min(end, now) - Math.max(o.startedAt, from))
  }
  return total
}

export function MonitorStatStrip({ m, outages, loading }: { m: MonitorSummary; outages: OutageLike[]; loading: boolean }) {
  const H = 3_600_000
  const open = Math.max(m.outageId != null ? 1 : 0, outages.filter(o => !o.endedAt).length)
  const tiles: { label: string; value: string; sub: { text: string; cls: string } }[] = ([
    ['Uptime · 24 hours', m.uptime24h, 24 * H],
    ['Uptime · 7 days', m.uptime7d, 7 * 24 * H],
    ['Uptime · 30 days', m.uptime30d, 30 * 24 * H],
  ] as const).map(([label, value, win]) => {
    let sub: { text: string; cls: string } = { text: 'No data yet', cls: 'text-muted' }
    if (value != null) {
      const down = loading ? null : downtimeIn(outages, win)
      if (value >= 100) sub = { text: 'No downtime', cls: 'text-ok' }
      else if (down != null && down > 0) sub = { text: `Down ${duration(down)}`, cls: value < 99 ? 'text-danger' : 'text-warn' }
      else sub = { text: 'Some downtime', cls: value < 99 ? 'text-danger' : 'text-warn' }
    }
    return { label, value: pct(value), sub }
  })
  tiles.push({
    label: 'Open now',
    value: String(open),
    sub: open > 0 ? { text: m.reason || 'Outage in progress', cls: 'text-danger' } : { text: 'Nothing open', cls: 'text-ok' },
  })
  return (
    <dl aria-label="Reliability summary" className="grid grid-cols-2 lg:grid-cols-4 gap-px overflow-hidden rounded-[14px] border border-default bg-default">
      {tiles.map(t => (
        <div key={t.label} className="bg-surface p-4 sm:p-5 min-w-0 flex flex-col gap-1">
          <dt className="text-[11px] font-semibold uppercase tracking-wide text-muted">{t.label}</dt>
          <dd className="text-[26px] leading-tight font-semibold tnum text-body truncate">{t.value}</dd>
          <dd className={`text-[12px] font-medium tnum line-clamp-2 break-words ${t.sub.cls}`}>{t.sub.text}</dd>
        </div>
      ))}
    </dl>
  )
}

function DayTip({ bar }: { bar: DailyBar }) {
  const tone = pillTone(bar)
  const word = tone === 'none' ? 'No data recorded' : tone === 'ok' ? 'No downtime' : tone === 'warn' ? 'Some downtime' : 'Major downtime'
  const cls = tone === 'ok' ? 'text-ok' : tone === 'warn' ? 'text-warn' : tone === 'danger' ? 'text-danger' : 'text-muted'
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-baseline justify-between gap-3">
        <span className="font-semibold text-body">{dateOnlyServer(bar.day)}</span>
        {bar.uptime != null && <span className="font-mono tnum text-body-2">{pct(bar.uptime)}</span>}
      </div>
      <p className={`font-medium ${cls}`}>{word}</p>
      {typeof bar.outages === 'number' && bar.outages > 0 && <p className="text-muted">{bar.outages} {bar.outages === 1 ? 'outage' : 'outages'} started</p>}
    </div>
  )
}

export function UptimeHistoryCard({ monitorId, uptime90d, refreshKey = 0 }: { monitorId: number; uptime90d: number | null; refreshKey?: number }) {
  const [rows, setRows] = useState<DailyBar[] | null>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    let alive = true
    setFailed(false)
    api.get<unknown>(`/api/monitors/${monitorId}/daily?days=90`)
      .then(d => { if (alive) setRows(normDaily(d)) })
      .catch(() => { if (alive) setFailed(true) })
    return () => { alive = false }
  }, [monitorId, refreshKey])
  const bars = useMemo(() => fillDays(rows ?? [], 90), [rows])
  return (
    <section aria-labelledby="uh-title" className="rounded-[14px] border border-default bg-surface">
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 px-4 sm:px-5 pt-4 sm:pt-5">
        <h3 id="uh-title" className="text-base font-semibold text-body">Uptime history</h3>
        <p className="text-[13px] text-muted tnum"><span className="font-mono font-semibold text-body">{pct(uptime90d)}</span> over 90 days</p>
      </div>
      <div className="px-4 sm:px-5 py-4 flex flex-col gap-2">
        {failed && rows === null
          ? <p role="alert" className="text-[13px] text-danger">Daily history could not load.</p>
          : <PillBars bars={bars} label="Daily uptime" tip={bar => <DayTip bar={bar} />} />}
        <PillAxis days={bars.length} />
      </div>
    </section>
  )
}
