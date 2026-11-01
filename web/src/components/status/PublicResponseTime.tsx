// Opt-in response-time chart on a public status page: one component at a
// time (a selector, not N charts), p50 and p95 only, over 24h / 7d / 30d.

import { useEffect, useState } from 'react'
import { api } from '../../lib/api'
import { ms } from '../../lib/format'
import { LATENCY_RANGES } from '../../lib/latency'
import type { LatencyPoint } from '../../lib/types'
import { PercentileChart, SeriesLegend } from '../charts/PercentileChart'
import { Select, Tabs } from '../ui'

type Range = '24h' | '7d' | '30d'
interface Resp {
  summary: { p50: number | null; p95: number | null }
  points: { t: number; p50: number | null; p95: number | null }[]
}

const toPoint = (p: Resp['points'][number]): LatencyPoint => ({
  t: p.t, p50: p.p50, p95: p.p95, count: 0, avg: null, min: null, max: null, p90: null, p99: null,
})

export function PublicResponseTime({ slug, components }: { slug: string; components: { id: number; name: string }[] }) {
  const [id, setId] = useState(components[0]?.id ?? 0)
  const [range, setRange] = useState<Range>('24h')
  const [data, setData] = useState<Resp | null>(null)
  const [failed, setFailed] = useState(false)
  const [retry, setRetry] = useState(0)
  const [now, setNow] = useState(() => Date.now())
  // Keep the selection valid as components come and go (paused, removed).
  useEffect(() => {
    if (!components.some(c => c.id === id) && components[0]) setId(components[0].id)
  }, [components, id])

  useEffect(() => {
    if (!id) return
    let alive = true
    setData(null)
    setFailed(false)
    api.get<Resp>(`/api/public/pages/${encodeURIComponent(slug)}/latency?monitor=${id}&range=${range}`)
      .then(d => { if (alive) { setData({ ...d, points: d.points ?? [] }); setNow(Date.now()) } })
      .catch(() => { if (alive) setFailed(true) })
    return () => { alive = false }
  }, [slug, id, range, retry])

  const r = LATENCY_RANGES[range]
  const name = components.find(c => c.id === id)?.name ?? ''

  return (
    <section aria-labelledby="rt-h" className="rounded-[14px] border border-default bg-surface p-5 sm:p-6 flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <h2 id="rt-h" className="text-[16px] font-semibold text-body mr-auto">Response time analysis</h2>
        {components.length > 1 && (
          <Select aria-label="Service response time" value={id} onChange={e => setId(Number(e.target.value))} className="w-full sm:w-56">
            {components.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}
          </Select>
        )}
        <Tabs size="sm" label="Time range" tabs={(['24h', '7d', '30d'] as Range[]).map(k => ({ key: k, label: k }))} active={range} onChange={setRange} />
      </div>
      <div className="flex flex-wrap items-center gap-4 text-[13px]">
        <SeriesLegend all={['p50', 'p95']} on={['p50', 'p95']} />
        {data && (
          <span className="text-muted tnum">
            Typical <strong className="text-body font-semibold">{ms(data.summary.p50)}</strong>
            {' · '}95th percentile <strong className="text-body font-semibold">{ms(data.summary.p95)}</strong>
          </span>
        )}
      </div>
      {failed ? (
        <p className="text-[13px] text-muted">Response times could not be loaded. <button type="button" className="text-accent hover:underline" onClick={() => setRetry(n => n + 1)}>Try again</button></p>
      ) : data ? (
        <PercentileChart points={data.points.map(toPoint)} series={['p50', 'p95']} widthMs={r.widthMs} from={now - r.spanMs} to={now} height={160}
          label={`${name} response time over the last ${range}: typical ${ms(data.summary.p50)}, 95th percentile ${ms(data.summary.p95)}`} />
      ) : (
        <div className="h-[160px] rounded-[10px] bg-surface-2/40 animate-pulse" />
      )}
    </section>
  )
}
