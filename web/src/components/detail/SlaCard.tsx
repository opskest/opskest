// Monthly SLA on a monitor's or group's page (GET /api/reports/sla): the
// month's availability against a target, the error budget it used, the
// outages behind it and the month's response-time percentiles. A group
// adds one line per member. CSV / JSON export for the same report.

import { useEffect, useMemo, useState } from 'react'
import { api, errorText } from '../../lib/api'
import { duration, ms, ymdServer } from '../../lib/format'
import { buttonClass, ErrorRetry, Icon, Select, SkeletonTable } from '../ui'
import { Pager, usePaging } from '../Pager'

interface SlaRow {
  id: number
  name: string
  availabilityPct: number | null
  downtimeMin: number
  outages: number
  longestMin: number
  mttrMin: number | null
  p50: number | null
  p95: number | null
  p99: number | null
  met: boolean | null
  budgetMin: number
  budgetUsedPct: number
}

interface SlaReport {
  monthToDate: boolean
  targetPct: number
  summary: { monitors: number; met: number; breached: number; availabilityPct: number | null; downtimeMin: number; outages: number }
  rows: SlaRow[]
}

const TARGETS = ['99', '99.5', '99.9', '99.95', '99.99']

function lastMonths(n: number): { value: string; label: string }[] {
  const out: { value: string; label: string }[] = []
  const [year, month] = ymdServer(Date.now()).split('-').map(Number)
  const d = new Date(Date.UTC(year, month - 1, 1))
  for (let i = 0; i < n; i++) {
    const value = `${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, '0')}`
    out.push({ value, label: d.toLocaleDateString(undefined, { timeZone: 'UTC', month: 'long', year: 'numeric' }) + (i === 0 ? ' (so far)' : '') })
    d.setUTCMonth(d.getUTCMonth() - 1)
  }
  return out
}

const pctText = (v: number | null) => (v == null ? '—' : `${(Math.floor(v * 1000 + 1e-6) / 1000).toFixed(3)}%`)
const mins = (m: number | null) => (m == null ? '—' : m <= 0 ? '0' : duration(m * 60_000))

function Budget({ used, budgetMin }: { used: number; budgetMin: number }) {
  const tone = used >= 100 ? 'bg-danger' : used >= 75 ? 'bg-warn' : 'bg-ok'
  return (
    <div className="flex flex-col gap-1.5">
      <div className="h-2 rounded-full bg-surface-2 overflow-hidden" aria-hidden="true">
        <div className={`h-full rounded-full ${tone}`} style={{ width: `${Math.min(100, used)}%` }} />
      </div>
      <span className="text-[11px] text-muted tnum">{used.toFixed(0)}% of {mins(budgetMin)} allowed</span>
    </div>
  )
}

function Tile({ label, value, tone = 'text-body', children }: { label: string; value: string; tone?: string; children?: React.ReactNode }) {
  return (
    <div className="py-4 flex flex-col gap-2 min-w-0">
      <span className="text-sm text-muted">{label}</span>
      <span className={`text-2xl font-semibold tnum truncate ${tone}`}>{value}</span>
      {children}
    </div>
  )
}

export function SlaCard({ scope, id }: { scope: 'monitor' | 'group'; id: number }) {
  const months = useMemo(() => lastMonths(13), [])
  const [month, setMonth] = useState(months[0].value)
  const [target, setTarget] = useState('99.9')
  const [data, setData] = useState<SlaReport | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [nonce, setNonce] = useState(0)
  const query = new URLSearchParams({ month, scope, id: String(id), target }).toString()

  useEffect(() => {
    let alive = true
    setData(null)
    setError(null)
    api.get<SlaReport>(`/api/reports/sla?${query}&limit=200`)
      .then(d => { if (alive) setData(d) })
      .catch(err => { if (alive) setError(errorText(err, 'Could not load the SLA report.')) })
    return () => { alive = false }
  }, [query, nonce])

  const rowPaging = usePaging(data?.rows ?? [], { resetKey: query })
  const row = scope === 'monitor' ? data?.rows[0] : undefined
  const met = row ? row.met : data ? data.summary.breached === 0 && data.summary.monitors > 0 : null

  return (
    <section className="p-5 rounded-[14px] bg-surface border border-default flex flex-col gap-6" aria-label="Monthly SLA">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-0.5">
          <h3 className="text-base font-semibold text-body">Availability report</h3>
          <p className="text-sm text-muted mt-1">Compare measured availability with your service target.</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Select aria-label="Month" value={month} onChange={e => setMonth(e.target.value)} dense>
            {months.map(m => <option key={m.value} value={m.value}>{m.label}</option>)}
          </Select>
          <Select aria-label="SLA target" value={target} onChange={e => setTarget(e.target.value)} dense>
            {TARGETS.map(t => <option key={t} value={t}>Target {t}%</option>)}
          </Select>
          <a href={`/api/reports/sla?${query}&format=csv`} className={buttonClass('secondary', 'sm')}>
            <Icon.External className="w-3.5 h-3.5" /> CSV
          </a>
        </div>
      </div>

      {error && <ErrorRetry message={error} onRetry={() => setNonce(n => n + 1)} />}
      {!error && !data && <SkeletonTable rows={4} cols={4} label="Loading availability report" />}

      {data && (
        <>
          <div className="grid grid-cols-2 xl:grid-cols-4 gap-x-6 border-y border-default">
            <Tile
              label={data.monthToDate ? 'Availability so far' : 'Availability'}
              value={pctText(row ? row.availabilityPct : data.summary.availabilityPct)}
              tone={met == null ? 'text-muted' : met ? 'text-ok' : 'text-danger'}
            >
              {met != null && (
                <span className={`text-[11px] font-medium ${met ? 'text-ok' : 'text-danger'}`}>
                  {met ? `Meets ${data.targetPct}%` : `Below ${data.targetPct}%`}
                  {scope === 'group' && ` · ${data.summary.met}/${data.summary.monitors} monitors`}
                </span>
              )}
            </Tile>
            <Tile label="Downtime" value={mins(row ? row.downtimeMin : data.summary.downtimeMin)} tone={(row ? row.downtimeMin : data.summary.downtimeMin) > 0 ? 'text-danger' : 'text-body'}>
              {row && <Budget used={row.budgetUsedPct} budgetMin={row.budgetMin} />}
            </Tile>
            <Tile label="Outages" value={String(row ? row.outages : data.summary.outages)}>
              {row && row.outages > 0 && <span className="text-[11px] text-muted tnum">longest {mins(row.longestMin)} · MTTR {mins(row.mttrMin)}</span>}
            </Tile>
            {row
              ? (
                <Tile label="p50 · p95 · p99" value={row.p95 != null ? ms(row.p95) : '—'}>
                  <span className="text-[11px] text-muted tnum">{row.p50 != null ? `${ms(row.p50)} · ${ms(row.p95 ?? 0)} · ${ms(row.p99 ?? 0)}` : 'no response times this month'}</span>
                </Tile>
              )
              : <Tile label="Monitors" value={String(data.summary.monitors)}><span className="text-[11px] text-muted">{data.summary.breached} below target</span></Tile>}
          </div>

          {scope === 'group' && data.rows.length > 0 && (
            <ul className="flex flex-col divide-y divide-default rounded-lg border border-default overflow-hidden">
              {rowPaging.rows.map(r => (
                <li key={r.id} className="grid grid-cols-[1fr_auto_auto] sm:grid-cols-[1fr_110px_90px_140px] items-center gap-3 px-3.5 py-2 text-[13px]">
                  <span className="truncate text-body">{r.name}</span>
                  <span className={`font-mono tnum text-right ${r.met == null ? 'text-muted' : r.met ? 'text-ok' : 'text-danger'}`}>{pctText(r.availabilityPct)}</span>
                  <span className="tnum text-right text-muted">{r.outages} outage{r.outages === 1 ? '' : 's'}</span>
                  <span className="hidden sm:block"><Budget used={r.budgetUsedPct} budgetMin={r.budgetMin} /></span>
                </li>
              ))}
            </ul>
          )}
          {scope === 'group' && <Pager paging={rowPaging} noun="Monitors" />}
        </>
      )}
    </section>
  )
}
