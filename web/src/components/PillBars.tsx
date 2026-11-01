// The daily-uptime strip (spec §11/§13) as rounded pills: one tall,
// fully-rounded bar per calendar day, tight gaps, coloured with the status
// tokens (ok / warn / danger, grey for "no data"), so light and dark follow
// the theme with no JS. Presentational: it takes the bars and an optional
// `onDay`, so the monitor page, the status pages and project views render it
// from whatever data they already have.
//
// Phones show the newest 45 days: 90 slivers in 340px are unreadable.
//
// Keyboard: with `onDay` the strip is one tab stop; ← → Home End move between
// days and Enter opens the focused one. Every pill has an accessible label
// (date, state, uptime) and a tooltip on hover and focus.

import { useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { dateOnlyServer, serverDayStartMs, pct, serverDayOffsetMs, ymdServer } from '../lib/format'
import { serverTimeZone } from '../lib/runtime'
import type { DailyBar } from '../lib/types'

/** Pills drawn on a phone. */
export const NARROW_PILLS = 45

export type PillTone = 'ok' | 'warn' | 'danger' | 'none'

/** One day's state. A day with downtime is never green; under 99 % (or half the
 * day's checks failing) it is red, otherwise amber. */
export function pillTone(bar: Pick<DailyBar, 'uptime' | 'down'>): PillTone {
  if (bar.uptime == null) return 'none'
  if (bar.uptime < 50 || (bar.down > 0 && bar.uptime < 99)) return 'danger'
  if (bar.down > 0 || bar.uptime < 99.95) return 'warn'
  return 'ok'
}

const TONE_CLASS: Record<PillTone, string> = {
  ok: 'bg-ok',
  warn: 'bg-warn',
  danger: 'bg-danger',
  none: 'bg-strong/30',
}
const TONE_WORD: Record<PillTone, string> = { ok: 'operational', warn: 'degraded', danger: 'outage', none: 'no data' }

function pillLabel(bar: DailyBar): string {
  const tone = pillTone(bar)
  if (tone === 'none') return `${dateOnlyServer(bar.day)}: no data`
  const outages = typeof bar.outages === 'number' && bar.outages > 0 ? `, ${bar.outages} outage${bar.outages === 1 ? '' : 's'}` : ''
  return `${dateOnlyServer(bar.day)}: ${TONE_WORD[tone]}, ${pct(bar.uptime)} uptime${outages}`
}

const HEIGHT = { lg: 'h-9', md: 'h-7', sm: 'h-5' } as const

/** True on phones, where the strip shows fewer pills. */
export function useNarrowPills(): boolean {
  const q = '(max-width: 639px)'
  const [narrow, setNarrow] = useState(() => typeof window !== 'undefined' && window.matchMedia(q).matches)
  useEffect(() => {
    const mq = window.matchMedia(q)
    const on = () => setNarrow(mq.matches)
    mq.addEventListener('change', on)
    return () => mq.removeEventListener('change', on)
  }, [])
  return narrow
}

/** How many pills a strip of `total` days draws right now. */
export function usePillCount(total: number): number {
  const narrow = useNarrowPills()
  return narrow ? Math.min(total, NARROW_PILLS) : total
}

export function PillBars({
  bars, onDay, label = 'Daily uptime', tip, size = 'lg',
}: {
  bars: DailyBar[]
  onDay?: (day: number) => void
  label?: string
  size?: keyof typeof HEIGHT
  /** Rich tooltip for a pill, shown on hover and keyboard focus. */
  tip?: (bar: DailyBar) => ReactNode
}) {
  const narrow = useNarrowPills()
  const shown = narrow && bars.length > NARROW_PILLS ? bars.slice(-NARROW_PILLS) : bars
  const [focusRaw, setFocus] = useState(-1)
  const [tipAt, setTipAt] = useState<number | null>(null)
  const ref = useRef<HTMLDivElement>(null)
  const focus = focusRaw >= 0 && focusRaw < shown.length ? focusRaw : shown.length - 1

  if (shown.length === 0) {
    return <p className="text-[13px] text-muted py-2">No history yet — daily history follows midnight in {serverTimeZone()}.</p>
  }

  function move(e: KeyboardEvent<HTMLDivElement>) {
    let next = -1
    if (e.key === 'ArrowRight') next = Math.min(shown.length - 1, focus + 1)
    else if (e.key === 'ArrowLeft') next = Math.max(0, focus - 1)
    else if (e.key === 'Home') next = 0
    else if (e.key === 'End') next = shown.length - 1
    if (next < 0) return
    e.preventDefault()
    setFocus(next)
    ref.current?.querySelectorAll<HTMLElement>('[data-day]')[next]?.focus()
  }

  const cls = `flex-1 min-w-0 rounded-full ${HEIGHT[size]}`
  const tipBar = tip && tipAt != null && tipAt < shown.length ? shown[tipAt] : null
  const tipLeft = tipAt != null ? ((tipAt + 0.5) / shown.length) * 100 : 0

  return (
    <div className="relative" onPointerLeave={() => setTipAt(null)}>
      <div
        ref={ref}
        role={onDay ? 'group' : 'img'}
        aria-label={`${label}, last ${shown.length} days`}
        onKeyDown={onDay ? move : undefined}
        onBlur={() => setTipAt(null)}
        className="flex w-full items-stretch gap-[2px] sm:gap-[3px]"
      >
        {shown.map((bar, i) => {
          const title = pillLabel(bar)
          const color = TONE_CLASS[pillTone(bar)]
          const hover = tip ? { onPointerEnter: () => setTipAt(i) } : {}
          if (!onDay) return <span key={bar.day} data-day title={tip ? undefined : title} className={`${cls} ${color}`} {...hover} />
          return (
            <button
              key={bar.day}
              type="button"
              data-day
              title={tip ? undefined : title}
              aria-label={title}
              tabIndex={i === focus ? 0 : -1}
              onFocus={() => { setFocus(i); if (tip) setTipAt(i) }}
              onClick={() => onDay(bar.day)}
              className={`${cls} ${color} cursor-pointer p-0 border-0 hover:brightness-110 hover:scale-y-[1.08] focus-visible:outline-offset-1 transition-transform motion-reduce:transition-none ${tipAt === i ? 'brightness-110 scale-y-[1.08]' : ''}`}
              {...hover}
            />
          )
        })}
      </div>
      {tipBar && (
        <div
          role="tooltip"
          className="pointer-events-none absolute bottom-full mb-2.5 z-30 w-72 max-w-[80vw] rounded-[10px] border border-default bg-surface shadow-xl px-3.5 py-3 text-[12px] animate-fade-in"
          style={{ left: `${tipLeft}%`, transform: `translateX(${tipLeft < 20 ? '-10%' : tipLeft > 80 ? '-90%' : '-50%'})` }}
        >
          {tip!(tipBar)}
        </div>
      )}
    </div>
  )
}

/**
 * A full strip of server calendar days ending today, one bar per day: days the
 * API has no row for (nothing measured, or before the monitor existed) are
 * "no data" bars, so every strip lines up by date.
 */
export function fillDays(bars: DailyBar[], days = 90): DailyBar[] {
  const byDay = new Map(bars.map(b => [ymdServer(b.day), b]))
  const today = serverDayStartMs(ymdServer(Date.now())) ?? Date.now()
  const out: DailyBar[] = []
  for (let i = days - 1; i >= 0; i--) {
    const day = serverDayOffsetMs(today, -i)
    out.push(byDay.get(ymdServer(day)) ?? { day, uptime: null, down: 0 })
  }
  return out
}

/** "90 days ago ... Today" under a strip; `live` puts the pulse dot by "Today". */
export function PillAxis({ days, live = false, className = '' }: { days: number; live?: boolean; className?: string }) {
  const shown = usePillCount(days)
  return (
    <div className={`flex items-center justify-between gap-3 font-mono text-[11px] text-faint tnum ${className}`} aria-hidden="true">
      <span>{Math.max(1, shown)} days ago</span>
      <span className="inline-flex items-center gap-1.5">{live && <span className="live-dot" />}Today</span>
    </div>
  )
}
