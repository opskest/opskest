import { useEffect, useRef, useState } from 'react'
import { Link } from 'wouter'
import { api, unwrapList } from '../lib/api'
import { dateServer, duration, ms } from '../lib/format'
import { normOutage } from '../lib/normalize'
import type { Heartbeat, Outage, Status } from '../lib/types'
import { BeatBar, StatusBadge } from './Status'
import { Button, Icon, SkeletonBlock } from './ui'

// A clickable heartbeat bar. Clicking a beat shows what happened on that
// check — status, reason, latency, response code, timings — and, for a
// DOWN beat, the outage it belongs to. Explains why a red/amber beat may
// have no outage: degraded (slow) and pending (failed once, being
// re-checked) beats never open one; only a confirmed DOWN does.

const EXPLAIN: Partial<Record<Status, string>> = {
  degraded: 'Slower than this monitor’s degraded threshold (or its certificate is near expiry). The monitor stayed up, so no outage was opened.',
  pending: 'This check failed and the monitor was re-checked quickly. It recovered before failing enough times to be confirmed down, so no outage was opened.',
  down: 'Confirmed down after the configured confirmations.',
  unknown: 'No result — Opskest itself had no internet connection, or the check could not run. Not counted against uptime.',
  maintenance: 'Inside a maintenance window: recorded, but it never alerts or counts against uptime.',
}

interface Props {
  monitorId: number
  beats: string
  lat?: number[]
  intervalS?: number
  lastCheckAt?: number
  size?: 'sm' | 'md' | 'lg'
  /** How many recent checks to draw (and load details for). */
  count?: number
  /** Where "View outage" goes; defaults to the Outages tab filtered to this monitor. */
  outageHref?: string
  onViewOutage?: (o: Outage) => void
}

export function BeatInspector({ monitorId, beats, lat, intervalS, lastCheckAt, size = 'md', count = 50, outageHref, onViewOutage }: Props) {
  const [back, setBack] = useState<number | null>(null)
  const [rows, setRows] = useState<Heartbeat[] | null>(null)
  const [outages, setOutages] = useState<Outage[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const loadedFor = useRef<{ id: number; at: number } | null>(null)

  // Fetch the detailed beats lazily on the first click (and again once the
  // bar has moved on), so an idle page costs nothing.
  useEffect(() => {
    if (back == null) return
    const stale = !loadedFor.current || loadedFor.current.id !== monitorId || loadedFor.current.at !== lastCheckAt
    if (!stale) return
    loadedFor.current = { id: monitorId, at: lastCheckAt ?? 0 }
    setError(null)
    Promise.all([
      api.get<unknown>(`/api/monitors/${monitorId}/beats?limit=${count}`),
      api.get<unknown>(`/api/monitors/${monitorId}/outages?limit=50`),
    ]).then(([b, o]) => {
      setRows(unwrapList<Heartbeat>(b, 'beats'))
      setOutages(unwrapList<unknown>(o, 'outages').map(normOutage))
    }).catch(() => setError('Could not load this check’s details.'))
  }, [back, monitorId, lastCheckAt, count])

  useEffect(() => { setBack(null); loadedFor.current = null }, [monitorId])

  const beat = back != null && rows ? rows[back] : undefined
  const outage = beat && beat.status === 'down' && outages
    ? outages.find(o => o.startedAt <= beat.ts && (o.endedAt == null || o.endedAt >= beat.ts))
    : undefined
  const href = outageHref ?? `/incidents?tab=outages&monitorId=${monitorId}`

  return (
    <div className="space-y-3">
      <div className={size === 'lg' ? 'py-1' : 'overflow-x-auto py-1'}>
        <BeatBar beats={beats} lat={lat} size={size} count={count} intervalS={intervalS} lastCheckAt={lastCheckAt}
          onBeatClick={b => setBack(cur => (cur === b ? null : b))} selectedBack={back} />
      </div>

      {back != null && (
        <div className="rounded-lg border border-default bg-surface-2/50 p-3.5 text-[12px] space-y-2" role="region" aria-label="Check details">
          {error ? (
            <p className="text-danger">{error}</p>
          ) : !rows ? (
            <SkeletonBlock lines={2} label="Loading check details" />
          ) : !beat ? (
            <p className="text-muted">This check is older than the detailed history shown here.</p>
          ) : (
            <>
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex flex-wrap items-center gap-2">
                  <StatusBadge status={beat.status} size="sm" />
                  <span className="text-body font-medium">{dateServer(beat.ts)}</span>
                  {beat.latencyMs >= 0 && <span className="text-muted font-mono">{ms(beat.latencyMs)}</span>}
                  {beat.code > 0 && <span className="text-muted font-mono">code {beat.code}</span>}
                </div>
                <Button size="sm" variant="ghost" onClick={() => setBack(null)} aria-label="Close check details" icon={<Icon.X className="w-3.5 h-3.5" />} />
              </div>
              {beat.msg && <p className="font-mono text-body [overflow-wrap:anywhere]">{beat.msg}</p>}
              {EXPLAIN[beat.status] && <p className="text-muted">{EXPLAIN[beat.status]}</p>}
              {beat.dnsMs >= 0 || beat.tcpMs >= 0 || beat.ttfbMs >= 0 ? (
                <p className="text-faint font-mono">
                  {[['DNS', beat.dnsMs], ['Connect', beat.tcpMs], ['TLS', beat.tlsMs], ['Server', beat.waitMs], ['App', beat.appMs]]
                    .filter(([, v]) => (v as number) >= 0).map(([k, v]) => `${k} ${v} ms`).join(' · ')}
                </p>
              ) : null}
              {beat.edge && <p className="text-faint">Answered by <span className="font-mono">{beat.edge}</span></p>}
              {beat.status === 'down' && (
                outage ? (
                  <div className="flex flex-wrap items-center gap-2 pt-1">
                    <span className="text-body-2">
                      Part of an outage since {dateServer(outage.startedAt)}
                      {outage.endedAt ? ` · lasted ${duration(outage.endedAt - outage.startedAt)}` : ' · ongoing'}
                      {outage.ackedBy ? ` · acknowledged by ${outage.ackedBy}` : ''}
                    </span>
                    {onViewOutage
                      ? <Button size="sm" variant="secondary" onClick={() => onViewOutage(outage)}>View outage</Button>
                      : <Link href={href} className="text-accent font-medium hover:underline">View outage →</Link>}
                  </div>
                ) : (
                  <p className="text-muted">No matching outage among the latest ones loaded — it may be older than the outage list shown.</p>
                )
              )}
            </>
          )}
        </div>
      )}
    </div>
  )
}
