// Timestamp — a moment shown the short way, with the exact time on hover or
// keyboard focus. Relative text ("12m ago") refreshes on one shared timer, and
// the exact time uses the installation's display zone (format.ts), so every
// screen agrees. The <time> element carries the ISO instant for machines and
// the exact time is in the accessible text, not only in the tooltip.

import { useSyncExternalStore } from 'react'
import { ago, dateServer, duration } from '../lib/format'
import { Tooltip } from './overlay'

// One interval for every Timestamp on the page: subscribers are notified, and
// each re-renders only when its own text changed.
const listeners = new Set<() => void>()
let timer: number | undefined
function subscribe(fn: () => void) {
  listeners.add(fn)
  if (timer == null) timer = window.setInterval(() => listeners.forEach(l => l()), 5000)
  return () => {
    listeners.delete(fn)
    if (listeners.size === 0 && timer != null) { window.clearInterval(timer); timer = undefined }
  }
}

function relativeText(at: number, future: boolean): string {
  if (!Number.isFinite(at) || at <= 0) return '—'
  const diff = at - Date.now()
  if (diff > 0) return future ? `in ${duration(diff)}` : 'just now'
  return ago(at)
}

/** "just now", "12m ago", or "in 3h 5m" for a time ahead. */
export function useRelative(at: number, future = true): string {
  return useSyncExternalStore(subscribe, () => relativeText(at, future))
}

export function Timestamp({
  at, mode = 'relative', future = true, prefix, bare, className = '',
}: {
  at: number | null | undefined
  /** relative: "12m ago" with the exact time on hover; absolute: the exact time with the relative on hover. */
  mode?: 'relative' | 'absolute'
  /** Allow "in 3h" for a time in the future (otherwise it reads "just now"). */
  future?: boolean
  /** Text before the time, e.g. "Updated". */
  prefix?: string
  /** Drop the trailing "ago" for use after a word like "since". */
  bare?: boolean
  className?: string
}) {
  const valid = at != null && Number.isFinite(at) && at > 0
  const rel = useRelative(valid ? at : 0, future)
  if (!valid) return <span className={className}>{'—'}</span>
  const exact = dateServer(at)
  const shown = mode === 'relative' ? (bare ? rel.replace(/ ago$/, '') : rel) : exact
  const tip = mode === 'relative' ? exact : rel
  return (
    <Tooltip content={tip}>
      <time dateTime={new Date(at).toISOString()} className={`tnum whitespace-nowrap cursor-default underline decoration-dotted decoration-faint/60 underline-offset-[3px] ${className}`}>
        {prefix ? `${prefix} ` : ''}{shown}
        <span className="sr-only"> ({mode === 'relative' ? exact : rel})</span>
      </time>
    </Tooltip>
  )
}
