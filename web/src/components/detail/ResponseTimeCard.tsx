import { useCallback, useEffect, useState } from 'react'
import { api, errorText } from '../../lib/api'
import { dateOnlyServer, dateServer, ms } from '../../lib/format'
import { LATENCY_RANGES } from '../../lib/latency'
import type { LatencyRange, LatencySeries, LatencyStats } from '../../lib/types'
import { PercentileChart, SeriesLegend, type SeriesKey } from '../charts/PercentileChart'
import { EmptyState, ErrorRetry, Skeleton, Tabs } from '../ui'

// A monitor's response time over a chosen range: percentile stat tiles and
// a p50/p95/p99 (+ optional p90/avg) chart.
//
// What is counted: every check that recorded a duration. That includes a
// check that failed after a response arrived (an HTTP 500, a failed keyword)
// and excludes checks that never got one (timeout, refused, DNS error), so
// the sample count is "checks with a response time", not "successful checks".
// 1h and 24h read raw checks and are exact. 7d and longer read hourly or
// daily rollups: count, average, min and max stay exact, percentiles are
// histogram estimates within about 5 %. Rollup ranges also start at the first
// whole UTC hour (7d, 30d) or server-zone day (90d) inside the range, because
// a rollup cannot be split; the server reports that start as `from`.

const ALL: SeriesKey[] = ['p50', 'p90', 'p95', 'p99', 'avg']
const DEFAULT_ON: SeriesKey[] = ['p50', 'p95', 'p99']

const TILES: { k: keyof LatencyStats; label: string; percentile?: boolean }[] = [
  { k: 'p50', label: 'p50', percentile: true },
  { k: 'p90', label: 'p90', percentile: true },
  { k: 'p95', label: 'p95', percentile: true },
  { k: 'p99', label: 'p99', percentile: true },
  { k: 'avg', label: 'Average' },
  { k: 'min', label: 'Fastest' },
  { k: 'max', label: 'Slowest' },
]

const RANGE_NAME: Record<LatencyRange, string> = {
  '1h': 'Last hour', '24h': 'Last 24 hours', '7d': 'Last 7 days', '30d': 'Last 30 days', '90d': 'Last 90 days',
}

type Resp = LatencySeries & { from?: number; to?: number }

export function ResponseTimeCard({ monitorId }: { monitorId: number }) {
  const [range, setRange] = useState<LatencyRange>('24h')
  const [data, setData] = useState<Resp | null>(null)
  const [err, setErr] = useState('')
  const [on, setOn] = useState<SeriesKey[]>(DEFAULT_ON)
  const [now, setNow] = useState(() => Date.now())
  const [loadedAt, setLoadedAt] = useState(0)
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    let cancelled = false
    const load = () => {
      api.get<Resp>(`/api/monitors/${monitorId}/latency?range=${range}`)
        .then(d => { if (!cancelled) { setData({ ...d, points: d.points ?? [] }); setErr(''); setNow(Date.now()); setLoadedAt(Date.now()) } })
        .catch(e => { if (!cancelled) setErr(errorText(e, 'Response times could not load.')) })
    }
    setData(null)
    setErr('')
    load()
    const t = window.setInterval(load, range === '1h' ? 30_000 : 60_000)
    return () => { cancelled = true; window.clearInterval(t) }
  }, [monitorId, range, attempt])
  const retry = useCallback(() => setAttempt(n => n + 1), [])

  const r = LATENCY_RANGES[range]
  const to = data?.to ?? now
  const from = data?.from ?? to - r.spanMs
  // Rollups written before the upgrade that added histograms carry only
  // averages: chart the average (and say so) instead of an empty grid.
  const legacy = !!data && data.points.length > 0 && data.points.every(p => p.p50 == null) && data.points.some(p => p.avg != null)
  const shown: SeriesKey[] = legacy ? ['avg'] : on
  const toggle = (k: SeriesKey) => setOn(cur => cur.includes(k) ? (cur.length > 1 ? cur.filter(x => x !== k) : cur) : ALL.filter(x => x === k || cur.includes(x)))

  const count = data?.summary.count ?? 0
  const estimated = !!data && data.source !== 'raw' && !legacy
  const rollup = !!data && data.source !== 'raw'
  const startText = rollup && data?.from
    ? (data.source === 'daily' ? `from ${dateOnlyServer(data.from)}` : `from ${dateServer(data.from)}`)
    : ''

  return (
    <section className="p-5 rounded-[14px] bg-surface border border-default space-y-6" aria-labelledby="rt-title">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 id="rt-title" className="text-base font-semibold text-body">Response time</h3>
          <p className="text-sm text-muted mt-1">
            {RANGE_NAME[range]}
            {startText && <>, counted {startText} <span className="text-faint">({data!.source === 'daily' ? 'daily summaries start on whole days in the server zone' : 'hourly summaries start on whole UTC hours'})</span></>}
            {' · '}
            {data ? `${count.toLocaleString()} ${count === 1 ? 'check' : 'checks'} with a response time` : <><span className="sr-only">Loading</span><Skeleton className="inline-block h-3 w-28 align-middle" /></>}
          </p>
          {data && (
            <p className="text-xs text-faint mt-1">
              {estimated
                ? 'Percentiles are estimates within about 5% (marked ≈). Average, fastest and slowest are exact.'
                : legacy
                  ? 'Percentiles are recorded from the upgrade on; showing the average until this range has them.'
                  : 'Exact values from the recorded checks.'}
              {' '}Checks that never got a response, such as timeouts and refused connections, have no response time and are not counted.
            </p>
          )}
        </div>
        <Tabs<LatencyRange> label="Time range" size="sm" active={range} onChange={setRange}
          tabs={(Object.keys(LATENCY_RANGES) as LatencyRange[]).map(k => ({ key: k, label: LATENCY_RANGES[k].label }))} />
      </div>

      {err && (
        <ErrorRetry message={data ? `Could not refresh response times (${err}). Showing data from ${dateServer(loadedAt)}.` : err} onRetry={retry} />
      )}

      <dl className="flex flex-wrap gap-x-7 gap-y-4 py-4 border-y border-default">
        {TILES.map(({ k, label, percentile }) => {
          const v = data ? data.summary[k] as number | null : null
          const approx = percentile && estimated && v != null
          return (
            <div key={k}>
              <dt className="text-xs text-muted">{label}</dt>
              <dd className="text-base font-medium text-body tnum mt-1" title={approx ? 'Estimated from a histogram, within about 5%' : undefined}>
                {approx && <span aria-hidden="true" className="text-faint">≈ </span>}
                {approx && <span className="sr-only">About </span>}
                {data ? ms(v) : '—'}
              </dd>
            </div>
          )
        })}
      </dl>

      {data && count === 0 ? (
        <EmptyState compact title="No check in this range recorded a response time.">Either no check ran in this window, or every check failed before a response arrived. Failed checks that did get a response, such as an error page, are counted.</EmptyState>
      ) : (
        <>
          {data && count < 20 && (
            <p className="text-xs text-warn" role="note">Only {count} {count === 1 ? 'check' : 'checks'} in this range: p95 and p99 are close to the slowest one or two, so treat them as rough.</p>
          )}
          <SeriesLegend all={legacy ? ['avg'] : ALL} on={shown} toggle={legacy ? undefined : toggle} />
          {data ? (
            <PercentileChart points={data.points} series={shown} widthMs={r.widthMs} from={from} to={to}
              label={`Response time over the ${RANGE_NAME[range].toLowerCase()}: p50 ${ms(data.summary.p50)}, p95 ${ms(data.summary.p95)}, p99 ${ms(data.summary.p99)}${estimated ? ' (estimated)' : ''}, from ${count} checks`} />
          ) : err ? (
            <div className="h-[180px] rounded-lg border border-dashed border-default" />
          ) : (
            <div className="h-[180px] rounded-lg bg-surface-2/40 animate-pulse" />
          )}
        </>
      )}
    </section>
  )
}
