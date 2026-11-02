// The Outages tab's summary row: active count, outages in the
// last 30 days, MTTR over 30 days and the most affected monitor (linked).

import { useEffect, useState, type ReactNode } from 'react'
import { Link } from 'wouter'
import { api, errorText } from '../lib/api'
import { duration, plural } from '../lib/format'
import { normOutageSummary } from '../lib/normalize'
import type { OutageSummary as Summary } from '../lib/types'
import { ErrorRetry, SkeletonCards } from './ui'

function Tile({ label, children, sub, onClick, active }: { label: string; children: ReactNode; sub?: string; onClick?: () => void; active?: boolean }) {
  const cls = `flex-1 min-w-[150px] bg-surface border rounded-[14px] px-4 py-3 flex flex-col gap-1 text-left ${active ? 'border-accent ring-1 ring-accent/40' : 'border-default'}`
  const body = (
    <>
      <span className="text-[11px] font-medium text-muted uppercase tracking-wide">{label}</span>
      <span className="text-xl font-semibold text-body tnum truncate">{children}</span>
      {sub && <span className="text-[12px] text-faint truncate">{sub}</span>}
    </>
  )
  return onClick
    ? <button type="button" onClick={onClick} aria-pressed={active} className={`${cls} hover:border-strong transition-colors`}>{body}</button>
    : <div className={cls}>{body}</div>
}

export function OutageSummary({
  monitorName, onNeedsNote, needsNoteActive, refreshKey = 0,
}: {
  monitorName: (id: number) => string
  /** Clicking "Needs a note" filters the table to those outages. */
  onNeedsNote?: () => void
  needsNoteActive?: boolean
  /** Bump to refetch (after a note is saved). */
  refreshKey?: number
}) {
  const [data, setData] = useState<Summary | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [nonce, setNonce] = useState(0)

  useEffect(() => {
    let alive = true
    setError(null)
    api.get<unknown>('/api/outages/summary')
      .then(d => { if (alive) setData(normOutageSummary(d)) })
      .catch(err => { if (alive) setError(errorText(err, 'Failed to load the outage summary')) })
    return () => { alive = false }
  }, [nonce, refreshKey])

  if (error) return <ErrorRetry message={error} onRetry={() => setNonce(n => n + 1)} />
  if (!data) return <SkeletonCards count={4} label="Loading outage summary" className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4" />

  return (
    <div className="flex flex-wrap gap-3">
      <Tile label="Active now"><span className={data.active > 0 ? 'text-danger' : ''}>{data.active}</span></Tile>
      <Tile label="Last 30 days">{data.last30d}</Tile>
      <Tile label="Needs a note (30 d)" sub="ended, cause not written down" onClick={onNeedsNote} active={needsNoteActive}>
        <span className={data.needsNote > 0 ? 'text-warn' : 'text-ok'}>{data.needsNote}</span>
      </Tile>
      <Tile label="MTTR (30 d)" sub="mean time to recovery">{data.mttrSeconds > 0 ? duration(data.mttrSeconds * 1000) : '—'}</Tile>
      <Tile label="Most affected (30 d)" sub={data.mostAffectedMonitorId != null ? plural(data.mostAffectedCount, 'outage') : undefined}>
        {data.mostAffectedMonitorId != null
          ? <Link href={`/monitors/${data.mostAffectedMonitorId}`} className="text-base text-accent hover:underline">{monitorName(data.mostAffectedMonitorId)}</Link>
          : <span className="text-faint">—</span>}
      </Tile>
    </div>
  )
}
