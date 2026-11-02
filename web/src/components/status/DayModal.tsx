// A status page's "click a day" view: one server calendar day at a glance.
// The summary (uptime, downtime) comes from the same daily bar the strip
// drew, so the bar, its tooltip and this view always agree; the timeline and
// the list of periods come from /day, which reads outage rows (kept as long
// as the monitor) plus degraded runs from raw beats while those are kept.
// ← → step through days without closing. Public pages never show reasons.

import { useEffect, useState } from 'react'
import { Link } from 'wouter'
import { api, errorText } from '../../lib/api'
import { duration, pct, serverDayEndMs, timeServer, ymdServer } from '../../lib/format'
import { serverTimeZone } from '../../lib/runtime'
import type { DailyBar, PublicMonitor } from '../../lib/types'
import { Button, ErrorRetry, Icon, LoadingBlock, Modal } from '../ui'
import { impactTone } from './IncidentCards'

interface Period { status: 'down' | 'degraded'; start: number; end: number; ongoing: boolean; reason: string; note: string }

function parsePeriods(data: unknown): Period[] {
  const raw = (data && typeof data === 'object' ? (data as { periods?: unknown }).periods : null)
  if (!Array.isArray(raw)) return []
  return raw.flatMap(p => {
    const o = (p ?? {}) as Record<string, unknown>
    const start = Number(o.start)
    const end = Number(o.end)
    if (!Number.isFinite(start) || !Number.isFinite(end)) return []
    return [{
      status: o.status === 'degraded' ? 'degraded' as const : 'down' as const,
      start, end, ongoing: o.ongoing === true,
      reason: typeof o.reason === 'string' ? o.reason : '',
      note: typeof o.note === 'string' ? o.note : '',
    }]
  })
}

function minutes(ms: number): string {
  if (ms <= 0) return '0 min'
  const m = Math.max(1, Math.round(ms / 60_000))
  return m < 60 ? `${m} min` : duration(m * 60_000)
}

export function DayModal({
  slug, monitor, bars, index, isPublic, base, onIndex, onClose, showUptime = true,
}: {
  slug: string
  monitor: PublicMonitor
  /** The component's whole strip, so ← → can move between days. */
  bars: DailyBar[]
  index: number
  isPublic: boolean
  /** The page owner can hide uptime percentages. */
  showUptime?: boolean
  base: string
  onIndex: (i: number) => void
  onClose: () => void
}) {
  const bar = bars[index]
  const [periods, setPeriods] = useState<Period[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [nonce, setNonce] = useState(0)
  const ymd = bar ? ymdServer(bar.day) : ''

  useEffect(() => {
    if (!bar) return
    let alive = true
    setPeriods(null)
    setError(null)
    api.get<unknown>(`/api/public/pages/${encodeURIComponent(slug)}/day?monitor=${monitor.id}&date=${ymd}`)
      .then(data => { if (alive) setPeriods(parsePeriods(data)) })
      .catch(err => { if (alive) setError(errorText(err, 'Could not load that day.')) })
    return () => { alive = false }
  }, [slug, monitor.id, ymd, nonce, bar])

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.target instanceof HTMLElement && e.target.closest('input, textarea, select')) return
      if (e.key === 'ArrowLeft' && index > 0) { e.preventDefault(); onIndex(index - 1) }
      if (e.key === 'ArrowRight' && index < bars.length - 1) { e.preventDefault(); onIndex(index + 1) }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [index, bars.length, onIndex])

  if (!bar) return null
  const now = Date.now()
  const dayEnd = serverDayEndMs(bar.day)
  const isToday = now >= bar.day && now < dayEnd
  const downMs = (periods ?? []).filter(p => p.status === 'down').reduce((t, p) => t + (p.end - p.start), 0)
  const outageCount = (periods ?? []).filter(p => p.status === 'down').length
  const incidents = bar.incidents ?? []
  const noData = bar.uptime == null

  return (
    <Modal open onClose={onClose} title={monitor.name} wide description={
      <span className="inline-flex flex-wrap items-center gap-x-2 gap-y-1">
        <span>{new Intl.DateTimeFormat(undefined, { weekday: 'long', day: '2-digit', month: 'long', year: 'numeric', timeZone: serverTimeZone() }).format(new Date(bar.day))}</span>
        {isToday && <span className="text-[12px] font-medium text-accent">Today so far</span>}
      </span>
    }>
      <div className="flex items-center justify-between gap-2 -mt-1">
        <span className="text-[12px] text-faint">Times are {serverTimeZone()}<span className="hidden sm:inline"> · ← → to change day</span></span>
        <div className="inline-flex items-center gap-1">
          <Button size="sm" aria-label="Previous day" disabled={index === 0} onClick={() => onIndex(index - 1)} className="!px-0 w-9" icon={<Icon.Chevron className="w-3.5 h-3.5 rotate-180" />} />
          <Button size="sm" aria-label="Next day" disabled={index >= bars.length - 1} onClick={() => onIndex(index + 1)} className="!px-0 w-9" icon={<Icon.Chevron className="w-3.5 h-3.5" />} />
        </div>
      </div>

      <dl className="grid sm:grid-cols-3 gap-4 py-4 border-y border-default">
        {showUptime && <Stat label="Uptime" value={noData ? '—' : pct(bar.uptime)} tone={noData ? 'muted' : bar.down > 0 ? 'warn' : 'ok'} />}
        <Stat label="Downtime" value={noData ? '—' : periods ? minutes(downMs) : bar.downMinutes ? `~${bar.downMinutes} min` : '0 min'} tone={downMs > 0 ? 'danger' : 'body'} />
        <Stat label="Outages" value={noData ? '—' : periods ? String(outageCount) : '…'} tone="body" />
      </dl>

      {error && <ErrorRetry message={error} onRetry={() => setNonce(n => n + 1)} />}
      {!error && periods == null && <LoadingBlock className="py-6" />}

      {periods != null && (
        <>
          <Timeline day={bar.day} periods={periods} noData={noData} now={now} />

          <section className="flex flex-col gap-2" aria-label="What happened">
            {periods.length === 0 && (
              <div className="flex items-start gap-2.5 rounded-[10px] bg-surface-2/60 px-3.5 py-3 text-[13px]">
                {noData
                  ? <><Icon.Info className="w-4 h-4 text-muted mt-0.5 shrink-0" /><span className="text-muted">No checks were recorded for this component on this day.</span></>
                  : bar.down > 0
                    ? <><Icon.Info className="w-4 h-4 text-warn mt-0.5 shrink-0" /><span className="text-body-2">A few checks failed, but none for long enough to count as an outage.</span></>
                    : <><Icon.Check className="w-4 h-4 text-ok mt-0.5 shrink-0" /><span className="text-body-2">No downtime{isToday ? ' so far today' : ' on this day'}.</span></>}
              </div>
            )}
            {periods.length > 0 && (
              <ul className="flex flex-col divide-y divide-default rounded-[10px] border border-default overflow-hidden">
                {periods.map((p, i) => (
                  <li key={`${p.start}-${i}`} className="px-3.5 py-2.5 flex flex-col gap-1 text-[13px]">
                    <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                      <span className={`inline-flex items-center gap-1.5 font-medium ${p.status === 'down' ? 'text-danger' : 'text-warn'}`}>
                        <span aria-hidden="true" className={`w-2 h-2 rounded-full ${p.status === 'down' ? 'bg-danger' : 'bg-warn'}`} />
                        {p.status === 'down' ? 'Outage' : 'Degraded'}
                      </span>
                      <span className="tnum text-body-2">
                        {p.start <= bar.day ? 'from before midnight' : timeServer(p.start)}
                        {' – '}
                        {p.ongoing ? 'ongoing' : p.end >= dayEnd ? 'past midnight' : timeServer(p.end)}
                      </span>
                      <span className="tnum text-muted ml-auto">{p.end > p.start ? minutes(p.end - p.start) : 'one check'}</span>
                    </div>
                    {!isPublic && p.note && <p className="text-[13px] text-body break-words whitespace-pre-line">{p.note}</p>}
                    {!isPublic && p.reason && <p className="text-[12px] text-muted break-words font-mono">{p.reason}</p>}
                  </li>
                ))}
              </ul>
            )}
          </section>
        </>
      )}

      {incidents.length > 0 && (
        <section className="flex flex-col gap-2" aria-label="Incidents">
          <h3 className="text-[14px] font-semibold text-body">Incidents</h3>
          <ul className="flex flex-col gap-1.5">
            {incidents.map(i => (
              <li key={i.id}>
                <Link href={`${base}/incidents/${i.id}`} onClick={onClose}
                  className="group flex items-center gap-2.5 rounded-[10px] border border-default px-3 py-2 text-[13px] hover:bg-surface-hover transition-colors">
                  <span aria-hidden="true" className={`w-2 h-2 rounded-full ${impactTone(i.impact) === 'danger' ? 'bg-danger' : impactTone(i.impact) === 'accent' ? 'bg-accent' : 'bg-warn'}`} />
                  <span className="flex-1 min-w-0 text-body truncate">{i.title}</span>
                  <span className="text-[11px] text-muted capitalize shrink-0">{i.impact}</span>
                  <Icon.Chevron className="w-3.5 h-3.5 text-faint group-hover:text-body shrink-0" />
                </Link>
              </li>
            ))}
          </ul>
        </section>
      )}
    </Modal>
  )
}

function Stat({ label, value, tone }: { label: string; value: string; tone: 'ok' | 'warn' | 'danger' | 'body' | 'muted' }) {
  const cls = { ok: 'text-ok', warn: 'text-warn', danger: 'text-danger', body: 'text-body', muted: 'text-muted' }[tone]
  return (
    <div className="px-3.5 py-3 flex flex-col gap-0.5 min-w-0">
      <dt className="text-[12px] font-medium text-muted">{label}</dt>
      <dd className={`text-lg font-semibold tnum truncate ${cls}`}>{value}</dd>
    </div>
  )
}

/** The server calendar day as a strip: green while measured and up, red for
 * outages, amber for degraded runs, hatched for the part of today that
 * hasn't happened yet. */
function Timeline({ day, periods, noData, now }: { day: number; periods: Period[]; noData: boolean; now: number }) {
  const dayEnd = serverDayEndMs(day)
  const dayLength = dayEnd - day
  const at = (t: number) => `${Math.min(100, Math.max(0, ((t - day) / dayLength) * 100))}%`
  const future = now < dayEnd ? Math.max(0, ((dayEnd - now) / dayLength) * 100) : 0
  return (
    <figure className="flex flex-col gap-1.5" aria-label="Timeline of the day">
      <div className={`relative h-6 rounded-md overflow-hidden ${noData ? 'bg-surface-2' : 'bg-ok/80'}`}>
        {periods.map((p, i) => (
          <span
            key={i}
            title={`${p.status === 'down' ? 'Outage' : 'Degraded'} ${timeServer(Math.max(p.start, day))}–${p.ongoing ? 'now' : timeServer(Math.min(p.end, dayEnd))}`}
            className={`absolute inset-y-0 ${p.status === 'down' ? 'bg-danger' : 'bg-warn'}`}
            style={{ left: at(p.start), width: `max(3px, calc(${at(p.end)} - ${at(p.start)}))` }}
          />
        ))}
        {future > 0 && (
          <span aria-hidden="true" className="absolute inset-y-0 right-0 bg-surface-2"
            style={{ width: `${future}%`, backgroundImage: 'repeating-linear-gradient(135deg, transparent 0 4px, var(--border-strong) 4px 5px)' }} />
        )}
      </div>
      <div className="flex justify-between text-[10.5px] text-faint tnum" aria-hidden="true">
        {[0, 0.25, 0.5, 0.75, 1].map(fraction => <span key={fraction}>{timeServer(day + dayLength * fraction)}</span>)}
      </div>
    </figure>
  )
}
