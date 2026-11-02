// Status presentation shared by every view. Colour never carries meaning
// alone: StatusDot is decorative and always paired with a text
// label; StatusBadge and UptimePill carry the label themselves; BeatBar has
// an aria-label summarising its counts.

import { memo } from 'react'
import { dateServer, ms, pctShort, pct } from '../lib/format'
import { beatStatus, SEVERITY_ORDER, STATUS_LABEL } from '../lib/status'
import type { Status } from '../lib/types'

export { STATUS_LABEL } from '../lib/status'

export interface StatusColorClasses {
  dot: string
  text: string
  bg: string
  border: string
}

// UP → --ok, DOWN → --danger, PENDING/DEGRADED → --warn,
// MAINTENANCE → --accent, UNKNOWN/PAUSED → --faint.
const STATUS_COLOR: Record<Status, StatusColorClasses> = {
  up: { dot: 'bg-ok', text: 'text-ok', bg: 'bg-ok-bg', border: 'border-ok' },
  degraded: { dot: 'bg-warn', text: 'text-warn', bg: 'bg-warn-bg', border: 'border-warn' },
  pending: { dot: 'bg-warn', text: 'text-warn', bg: 'bg-warn-bg', border: 'border-warn' },
  down: { dot: 'bg-danger', text: 'text-danger', bg: 'bg-danger-bg', border: 'border-danger' },
  maintenance: { dot: 'bg-accent', text: 'text-accent', bg: 'bg-accent-subtle', border: 'border-accent' },
  unknown: { dot: 'bg-faint', text: 'text-muted', bg: 'bg-surface-2', border: 'border-faint' },
  paused: { dot: 'bg-faint', text: 'text-muted', bg: 'bg-surface-2', border: 'border-faint' },
}

export function statusColor(status: Status): StatusColorClasses {
  return STATUS_COLOR[status]
}

const DOT_SIZE = { sm: 'w-2 h-2', md: 'w-2.5 h-2.5', lg: 'w-3 h-3' } as const

/**
 * The state dot. Decorative by default (it sits beside a text label); pass
 * `label` to make it stand alone as an image with that accessible name, e.g.
 * `<StatusDot status="down" label="Down" />`.
 */
export function StatusDot({ status, pulse, label, size = 'sm', className = '' }: {
  status: Status; pulse?: boolean; label?: string; size?: keyof typeof DOT_SIZE; className?: string
}) {
  const shouldPulse = pulse ?? status === 'pending'
  return (
    <span
      {...(label ? { role: 'img', 'aria-label': label } : { 'aria-hidden': true })}
      className={`inline-block rounded-full shrink-0 ${DOT_SIZE[size]} ${STATUS_COLOR[status].dot} ${shouldPulse ? 'animate-pulse motion-reduce:animate-none' : ''} ${className}`}
    />
  )
}

/** A dot plus an sr-only label — for places that show only the dot. */
export function StatusMark({ status, className = '' }: { status: Status; className?: string }) {
  return (
    <span className={`inline-flex items-center shrink-0 ${className}`} title={STATUS_LABEL[status]}>
      <StatusDot status={status} />
      <span className="sr-only">{STATUS_LABEL[status]}</span>
    </span>
  )
}

const BADGE_SIZE = {
  sm: 'gap-1.5 text-[11px] px-2 py-1',
  md: 'gap-2 text-[12px] px-2.5 py-1',
  lg: 'gap-2.5 text-base px-4 py-2',
} as const

export function StatusBadge({
  status, size = 'md', className = '', title, label,
}: { status: Status; size?: keyof typeof BADGE_SIZE; className?: string; title?: string; label?: string }) {
  const { text, bg } = STATUS_COLOR[status]
  return (
    <span
      title={title}
      className={`inline-flex items-center font-medium rounded-md whitespace-nowrap ${BADGE_SIZE[size]} ${bg} ${text} ${className}`}
    >
      <StatusDot status={status} className={size === 'lg' ? 'w-2.5 h-2.5' : ''} />
      {label ?? STATUS_LABEL[status]}
    </span>
  )
}

/** A compact availability value paired with the current state. */
export function UptimePill({ status, uptime, className = '' }: { status: Status; uptime: number | null; className?: string }) {
  const { text, bg } = STATUS_COLOR[status]
  return (
    <span
      title={`${STATUS_LABEL[status]} · 24h uptime ${pct(uptime)}`}
      className={`inline-flex items-center justify-center shrink-0 min-w-[64px] h-7 px-2 rounded-md
        text-[11px] font-semibold tnum whitespace-nowrap ${bg} ${text} ${className}`}
    >
      <span className="sr-only">{STATUS_LABEL[status]}, 24-hour uptime </span>
      {uptime == null || status === 'paused' ? STATUS_LABEL[status] : pctShort(uptime)}
    </span>
  )
}

// ── Beat bar ────────────────────────────────────────────────────────────────

const BEAT_SIZE = {
  sm: { bar: 'inline-flex items-end gap-[2px]', beat: 'w-[4px] h-5 rounded-full' },
  md: { bar: 'inline-flex items-end gap-[2px]', beat: 'w-[5px] h-6 rounded-full' },
  lg: { bar: 'flex w-full items-end gap-px sm:gap-[2px] xl:gap-[3px]', beat: 'flex-1 min-w-[2px] h-9 rounded-full' },
} as const

export interface BeatBarProps {
  /** Compact beats, oldest to newest (u d p g k m). */
  beats: string
  /** Latencies aligned with `beats`. */
  lat?: number[]
  size?: keyof typeof BEAT_SIZE
  /** Slots to draw: the newest `count` beats, empty slots pad the left. */
  count?: number
  /** With both, the tooltip gives each beat's approximate time. */
  intervalS?: number
  lastCheckAt?: number
  /** Makes beats clickable; receives how many checks back (0 = latest). */
  onBeatClick?: (back: number) => void
  /** Highlights the beat that many checks back. */
  selectedBack?: number | null
}

function BeatBarImpl({ beats, lat, size = 'md', count = 50, intervalS, lastCheckAt, onBeatClick, selectedBack }: BeatBarProps) {
  const dims = BEAT_SIZE[size]
  const chars = beats.slice(-count).split('')
  const padCount = Math.max(0, count - chars.length)
  const latOffset = lat ? lat.length - chars.length : 0

  const tally = new Map<Status, number>()
  for (const ch of chars) {
    const s = beatStatus(ch)
    tally.set(s, (tally.get(s) ?? 0) + 1)
  }
  const summary = SEVERITY_ORDER.filter(s => tally.has(s)).map(s => `${tally.get(s)} ${STATUS_LABEL[s].toLowerCase()}`).join(', ')

  return (
    <div className={dims.bar} role={onBeatClick ? 'group' : 'img'} aria-label={chars.length ? `Last ${chars.length} checks: ${summary}` : 'No checks yet'}>
      {Array.from({ length: padCount }, (_, i) => (
        <span key={`pad-${i}`} aria-hidden="true" className={`${dims.beat} bg-surface-2`} />
      ))}
      {chars.map((ch, i) => {
        const status = beatStatus(ch)
        const latency = lat ? lat[latOffset + i] : undefined
        const back = chars.length - 1 - i
        const when = intervalS && lastCheckAt
          ? dateServer(lastCheckAt - back * intervalS * 1000)
          : back === 0 ? 'latest' : `${back} checks ago`
        const label = `${when} · ${STATUS_LABEL[status]}${latency != null && latency >= 0 ? ` · ${ms(latency)}` : ''}`
        if (onBeatClick) {
          const selected = selectedBack === back
          return (
            <button
              key={i}
              type="button"
              title={`${label} — click for details`}
              aria-label={label}
              aria-pressed={selected}
              onClick={() => onBeatClick(back)}
              className={`${dims.beat} ${STATUS_COLOR[status].dot} cursor-pointer hover:opacity-70 focus-visible:outline-2 focus-visible:outline-offset-2 ${selected ? 'ring-2 ring-offset-1 ring-offset-surface ring-body' : ''}`}
            />
          )
        }
        return (
          <span
            key={i}
            title={label}
            aria-hidden="true"
            className={`${dims.beat} ${STATUS_COLOR[status].dot} hover:opacity-70`}
          />
        )
      })}
    </div>
  )
}

export const BeatBar = memo(BeatBarImpl)
