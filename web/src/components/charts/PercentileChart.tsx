import { useMemo, useRef, useState } from 'react'
import type { LatencyPoint } from '../../lib/types'
import { ms } from '../../lib/format'

// Response-time chart: one line per percentile over time, hand-drawn SVG.
// The percentiles are ordinal steps of one measure, so they share one hue
// (the accent) at falling strength — p50 strongest — rather than unrelated
// categorical colours; avg is a muted dashed line. A missing point (no
// latency measured in that window) breaks the line instead of drawing 0.

export type SeriesKey = 'p50' | 'p90' | 'p95' | 'p99' | 'avg'

export const SERIES_STYLE: Record<SeriesKey, { label: string; stroke: string; opacity: number; width: number; dash?: string }> = {
  p50: { label: 'p50', stroke: 'var(--accent)', opacity: 1, width: 2 },
  p90: { label: 'p90', stroke: 'var(--accent)', opacity: 0.75, width: 1.5 },
  p95: { label: 'p95', stroke: 'var(--accent)', opacity: 0.6, width: 1.5 },
  p99: { label: 'p99', stroke: 'var(--accent)', opacity: 0.38, width: 1.5 },
  avg: { label: 'avg', stroke: 'var(--muted)', opacity: 0.9, width: 1.5, dash: '4 3' },
}

const W = 600
const PAD_TOP = 8
const PAD_BOTTOM = 4

function niceMax(v: number): number {
  if (v <= 0) return 10
  const exp = Math.pow(10, Math.floor(Math.log10(v)))
  for (const m of [1, 2, 2.5, 5, 10]) if (m * exp >= v) return m * exp
  return 10 * exp
}

export function fmtTick(t: number, spanMs: number): string {
  const d = new Date(t)
  if (spanMs <= 36 * 3_600_000) return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  return d.toLocaleDateString([], { month: 'short', day: 'numeric' })
}

function fmtWhen(t: number, widthMs: number): string {
  const d = new Date(t)
  if (widthMs >= 86_400_000) return d.toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' })
  return d.toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

interface Props {
  points: LatencyPoint[]
  series: SeriesKey[]
  /** Point width in ms — used to detect gaps and label the tooltip. */
  widthMs: number
  /** Window covered, so the x-axis spans the whole range even with sparse data. */
  from: number
  to: number
  height?: number
  /** Accessible name for the chart. */
  label: string
}

export function PercentileChart({ points: rawPoints, series, widthMs, from, to, height = 180, label }: Props) {
  const wrap = useRef<HTMLDivElement>(null)
  const [hover, setHover] = useState<number | null>(null)
  const points = useMemo(() => rawPoints.filter(p => p.t + widthMs / 2 >= from && p.t + widthMs / 2 <= to)
    .map(p => ({ ...p, ...Object.fromEntries(series.map(k => [k, p[k] != null && Number.isFinite(p[k]) ? p[k] : null])) }))
    .sort((a, b) => a.t - b.t), [rawPoints, series, widthMs, from, to])

  const { yMax, paths } = useMemo(() => {
    let max = 0
    for (const p of points) for (const k of series) { const v = p[k]; if (v != null && v > max) max = v }
    const yMax = niceMax(max)
    const span = Math.max(1, to - from)
    const x = (t: number) => ((t + widthMs / 2 - from) / span) * W
    const y = (v: number) => PAD_TOP + (1 - v / yMax) * (height - PAD_TOP - PAD_BOTTOM)
    const paths = series.map(k => {
      let d = ''
      const dots: { x: number; y: number }[] = []
      let prevT: number | null = null
      for (let i = 0; i < points.length; i++) {
        const p = points[i]
        const v = p[k]
        if (v == null) { prevT = null; continue }
        const gap = prevT == null || p.t - prevT > widthMs * 1.5
        d += `${gap ? 'M' : 'L'}${x(p.t).toFixed(1)},${y(v).toFixed(1)}`
        const next = points[i + 1]
        if (gap && (!next || next[k] == null || next.t - p.t > widthMs * 1.5)) dots.push({ x: x(p.t), y: y(v) })
        prevT = p.t
      }
      return { k, d, dots }
    })
    return { yMax, paths }
  }, [points, series, widthMs, from, to, height])

  const span = Math.max(1, to - from)
  const xPct = (t: number) => ((t + widthMs / 2 - from) / span) * 100

  function onMove(clientX: number) {
    const el = wrap.current
    if (!el || points.length === 0) return
    const r = el.getBoundingClientRect()
    const t = from + ((clientX - r.left) / r.width) * span
    let best = 0
    for (let i = 1; i < points.length; i++) {
      if (Math.abs(points[i].t + widthMs / 2 - t) < Math.abs(points[best].t + widthMs / 2 - t)) best = i
    }
    setHover(best)
  }

  const hp = hover != null ? points[hover] : null
  const ticks = [0, 0.5, 1].map(f => yMax * f)

  if (!points.some(p => series.some(k => p[k] != null))) {
    return (
      <div className="flex items-center justify-center text-[12px] text-faint border border-dashed border-default rounded-lg" style={{ height }}>
        No response times measured in this range
      </div>
    )
  }

  return (
    <div className="flex gap-2">
      {/* y-axis labels: recessive, three ticks */}
      <div className="relative w-11 shrink-0 text-[10px] text-faint font-mono tnum" style={{ height }} aria-hidden>
        {ticks.map(v => (
          <span key={v} className="absolute right-0 -translate-y-1/2" style={{ top: PAD_TOP + (1 - v / yMax) * (height - PAD_TOP - PAD_BOTTOM) }}>{ms(v)}</span>
        ))}
      </div>
      <div className="flex-1 min-w-0">
        <div
          ref={wrap}
          className="relative touch-none"
          style={{ height }}
          role="img"
          aria-label={label}
          onPointerMove={e => onMove(e.clientX)}
          onPointerDown={e => onMove(e.clientX)}
          onPointerLeave={() => setHover(null)}
        >
          <svg viewBox={`0 0 ${W} ${height}`} preserveAspectRatio="none" className="absolute inset-0 w-full h-full overflow-visible">
            {ticks.map(v => {
              const yy = PAD_TOP + (1 - v / yMax) * (height - PAD_TOP - PAD_BOTTOM)
              return <line key={v} x1={0} x2={W} y1={yy} y2={yy} stroke="var(--border)" strokeWidth={1} vectorEffect="non-scaling-stroke" />
            })}
            {paths.map(({ k, d, dots }) => {
              const st = SERIES_STYLE[k]
              return <g key={k}><path d={d} fill="none" stroke={st.stroke} strokeOpacity={st.opacity} strokeWidth={st.width} strokeDasharray={st.dash} strokeLinejoin="round" strokeLinecap="round" vectorEffect="non-scaling-stroke" />{dots.map((dot, i) => <path key={i} d={`M${dot.x},${dot.y}h0.1`} stroke={st.stroke} strokeOpacity={st.opacity} strokeWidth={4} strokeLinecap="round" vectorEffect="non-scaling-stroke" />)}</g>
            })}
          </svg>
          {hp && (
            <>
              <div className="absolute top-0 bottom-0 w-px bg-[var(--border-strong)] pointer-events-none" style={{ left: `${xPct(hp.t)}%` }} />
              <div
                className="absolute z-10 top-1 pointer-events-none rounded-md border border-default bg-surface shadow-md px-2.5 py-2 text-[11px] min-w-[132px]"
                style={xPct(hp.t) > 60 ? { right: `${100 - xPct(hp.t) + 1.5}%` } : { left: `${xPct(hp.t) + 1.5}%` }}
              >
                <div className="text-muted mb-1">{fmtWhen(hp.t, widthMs)}</div>
                {series.map(k => (
                  <div key={k} className="flex items-center justify-between gap-3">
                    <span className="flex items-center gap-1.5 text-muted">
                      <svg width="12" height="4" aria-hidden><line x1="0" x2="12" y1="2" y2="2" stroke={SERIES_STYLE[k].stroke} strokeOpacity={SERIES_STYLE[k].opacity} strokeWidth={2} strokeDasharray={SERIES_STYLE[k].dash} /></svg>
                      {SERIES_STYLE[k].label}
                    </span>
                    <span className="font-mono tnum text-body">{ms(hp[k])}</span>
                  </div>
                ))}
                {hp.count > 0 && <div className="text-faint mt-1 tnum">{hp.count.toLocaleString()} checks</div>}
              </div>
            </>
          )}
        </div>
        <div className="flex justify-between text-[10px] text-faint font-mono tnum mt-1" aria-hidden>
          <span>{fmtTick(from, span)}</span>
          <span>{fmtTick(from + span / 2, span)}</span>
          <span>{fmtTick(to, span)}</span>
        </div>
      </div>
    </div>
  )
}

/** Legend: always shown for ≥ 2 series; doubles as the series toggle. */
export function SeriesLegend({ all, on, toggle }: { all: SeriesKey[]; on: SeriesKey[]; toggle?: (k: SeriesKey) => void }) {
  return (
    <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="Series">
      {all.map(k => {
        const st = SERIES_STYLE[k]
        const active = on.includes(k)
        const body = (
          <>
            <svg width="14" height="4" aria-hidden><line x1="0" x2="14" y1="2" y2="2" stroke={st.stroke} strokeOpacity={active ? st.opacity : 0.25} strokeWidth={2} strokeDasharray={st.dash} /></svg>
            {st.label}
          </>
        )
        return toggle ? (
          <button key={k} type="button" aria-pressed={active} onClick={() => toggle(k)}
            className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-md border text-[11px] font-mono transition ${active ? 'border-default text-body bg-surface' : 'border-transparent text-faint'}`}>
            {body}
          </button>
        ) : (
          <span key={k} className="inline-flex items-center gap-1.5 text-[11px] font-mono text-muted">{body}</span>
        )
      })}
    </div>
  )
}
