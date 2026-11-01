import { memo } from 'react'
import { Link } from 'wouter'
import { duration, ms } from '../../lib/format'
import { STATUS_LABEL, statusColor } from '../Status'
import type { BoardMonitor } from '../../lib/boardLayout'
import type { Tier } from '../../lib/boardFit'
import { beatStatus } from '../../lib/status'

function pct(v: number | null | undefined): string {
  return v != null ? `${v.toFixed(2)}%` : '—'
}

export interface MonitorCardProps {
  monitor: BoardMonitor
  mode: 'private' | 'public'
  showReason: boolean
  showLatency: boolean
  linkMonitors: boolean
  now: number
  /** 'full' adds the strip of recent checks; 'lean' keeps name, state and time. */
  tier?: Tier
  /** Fill the grid cell (fixed 8/12/16 layouts) instead of keeping the natural height. */
  fill?: boolean
  /** Some tile on the board wraps its name onto two lines: reserve two lines so every strip and footer lines up. */
  reserveName?: boolean
}

/**
 * One service tile: status dot and name, the state in words, a strip of
 * rounded pills for the recent checks, and how long it has been in this
 * state. Quiet when healthy, coloured only where something is wrong. All
 * sizes are em, so the tile scales with the board's type size.
 */
export const MonitorCard = memo(function MonitorCard({
  monitor, mode, showReason, showLatency, linkMonitors, now, tier = 'full', fill = false, reserveName = false,
}: MonitorCardProps) {
  const paused = monitor.paused || monitor.status === 'paused'
  const status = paused ? 'paused' : monitor.status
  const isDown = status === 'down'
  const isWarn = status === 'degraded' || status === 'pending'
  const problem = isDown || isWarn || status === 'unknown'
  const color = statusColor(status)

  const sinceText = monitor.since > 0 ? duration(Math.max(0, now - monitor.since)) : ''
  const latencyText = showLatency && monitor.latencyMs != null && monitor.latencyMs >= 0 ? ms(monitor.latencyMs) : null
  const reason = isDown && showReason && monitor.reason ? monitor.reason : ''
  const word = isDown ? 'Down for' : isWarn ? (status === 'pending' ? 'Checking for' : 'Degraded for') : status === 'unknown' ? 'No data for' : paused ? 'Paused' : 'Up for'

  const frame = isDown ? 'border-danger/60' : isWarn ? 'border-warn/60' : 'border-default'

  const content = (
    <div
      className={`w-full rounded-[0.9em] border bg-surface flex flex-col select-none leading-[1.3] h-full justify-center ${frame}`}
      style={{ padding: '0.9em 1em', gap: '0.7em' }}
    >
      <div className="flex items-center justify-between" style={{ gap: '0.7em', minHeight: reserveName ? '2.65em' : undefined }}>
        <span className="flex items-center min-w-0" style={{ gap: '0.55em' }}>
          <span aria-hidden="true" className={`rounded-full shrink-0 ${color.dot} ${isDown ? 'motion-safe:animate-pulse' : ''}`} style={{ width: '0.65em', height: '0.65em' }} />
          <span className="font-mono font-medium text-body line-clamp-2 [overflow-wrap:anywhere]" style={{ fontSize: '1em', lineHeight: 1.3 }}>{monitor.name}</span>
        </span>
        <span className={`shrink-0 font-medium ${color.text}`} style={{ fontSize: '0.82em', lineHeight: 1.5 }}>{STATUS_LABEL[status] || status}</span>
      </div>

      {tier === 'full' && (
        <div className="flex" style={{ gap: '2px', height: fill ? '2.6em' : '1.7em' }} aria-hidden="true">
          {(monitor.beats ?? '').slice(-30).split('').map((ch, i) => (
            <span key={i} className={`flex-1 min-w-0 rounded-full opacity-90 ${statusColor(beatStatus(ch)).dot}`} />
          ))}
          {!monitor.beats && <span className="flex-1 rounded-full bg-surface-hover" />}
        </div>
      )}

      {reason && (
        <span className="text-danger font-medium line-clamp-3 [overflow-wrap:anywhere]" style={{ fontSize: '0.86em' }}>{reason}</span>
      )}

      <div className="flex flex-wrap items-baseline justify-between font-mono" style={{ gap: '0.15em 0.7em', fontSize: '0.8em' }}>
        <span className={`font-medium tnum whitespace-nowrap ${problem ? color.text : 'text-body-2'}`}>
          {paused ? word : `${word} ${sinceText}`.trim()}
        </span>
        <span className="text-muted tnum whitespace-nowrap">{latencyText ? `${latencyText} · ` : ''}24h {pct(monitor.uptime24h)}</span>
      </div>
    </div>
  )

  if (linkMonitors && mode === 'private') {
    return <Link href={`/monitors/${monitor.id}`} className="block w-full min-w-0 h-full">{content}</Link>
  }
  return content
})

/** The tile that stands for healthy services folded away to make room for problems. */
export function SummaryCard({ count, up, paused }: { count: number; up: number; paused: number }) {
  return (
    <div className="w-full rounded-[0.9em] border border-default bg-surface flex flex-col select-none leading-[1.3]" style={{ padding: '0.9em 1em', gap: '0.7em' }}>
      <div className="flex items-start justify-between" style={{ gap: '0.7em' }}>
        <span className="flex items-start" style={{ gap: '0.55em' }}>
          <span aria-hidden="true" className="rounded-full shrink-0 bg-ok" style={{ width: '0.65em', height: '0.65em', marginTop: '0.42em' }} />
          <span className="font-semibold text-body tracking-tight" style={{ fontSize: '1.05em', lineHeight: 1.25 }}>+{count} more operational</span>
        </span>
        <span className="shrink-0 font-semibold text-ok" style={{ fontSize: '0.86em', lineHeight: 1.45 }}>Operational</span>
      </div>
      <span className="text-muted" style={{ fontSize: '0.86em' }}>{up} up{paused ? `, ${paused} paused` : ''}. Hidden so every problem stays on screen.</span>
    </div>
  )
}
