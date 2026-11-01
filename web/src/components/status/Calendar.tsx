// A small month calendar for status pages: days can carry a mark (how many
// incidents or maintenance windows touch them), one day can be selected, and
// the whole grid works from the keyboard. It deals in calendar dates
// ("2026-10-28") only; the caller decides which time zone an instant belongs
// to, so the same component serves the viewer's zone and the server's.
//
// Keyboard (one tab stop, like the daily uptime bars): arrows move by a day or
// a week, Home/End go to the start/end of the week, PageUp/PageDown change the
// month, Enter or Space selects the focused day.
//
// A mark is never colour alone: marked days carry a bar at the bottom of the
// cell, a count, and a spoken label ("3 incidents").

import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { Icon } from '../ui'

export interface DayMark {
  count: number
  tone: 'danger' | 'warn' | 'accent'
}
export interface MonthRef { y: number; m: number }

const pad = (n: number) => String(n).padStart(2, '0')
export const keyOf = (y: number, m: number, d: number) => `${y}-${pad(m)}-${pad(d)}`
export function parseKey(key: string): { y: number; m: number; d: number } | null {
  const r = /^(\d{4})-(\d{2})-(\d{2})$/.exec(key)
  return r ? { y: Number(r[1]), m: Number(r[2]), d: Number(r[3]) } : null
}
export const monthOfKey = (key: string): MonthRef => { const p = parseKey(key) ?? { y: 1970, m: 1 }; return { y: p.y, m: p.m } }
export function shiftMonth(ref: MonthRef, delta: number): MonthRef {
  const t = ref.y * 12 + (ref.m - 1) + delta
  return { y: Math.floor(t / 12), m: (t % 12) + 1 }
}
/** Add days to a calendar date (no time zone involved). */
export function addDays(key: string, delta: number): string {
  const p = parseKey(key)!
  const d = new Date(Date.UTC(p.y, p.m - 1, p.d + delta))
  return keyOf(d.getUTCFullYear(), d.getUTCMonth() + 1, d.getUTCDate())
}
const daysIn = (y: number, m: number) => new Date(Date.UTC(y, m, 0)).getUTCDate()
/** 0 = Monday … 6 = Sunday */
const weekdayOf = (y: number, m: number, d: number) => (new Date(Date.UTC(y, m - 1, d)).getUTCDay() + 6) % 7
const cmpMonth = (a: MonthRef, b: MonthRef) => (a.y - b.y) * 12 + (a.m - b.m)

const MONTH_FMT = new Intl.DateTimeFormat(undefined, { month: 'long', year: 'numeric', timeZone: 'UTC' })
const LONG_FMT = new Intl.DateTimeFormat(undefined, { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric', timeZone: 'UTC' })
const WEEKDAY_NARROW = new Intl.DateTimeFormat(undefined, { weekday: 'narrow', timeZone: 'UTC' })
const WEEKDAY_LONG = new Intl.DateTimeFormat(undefined, { weekday: 'long', timeZone: 'UTC' })
// 2024-01-01 was a Monday.
const weekdayLabels = Array.from({ length: 7 }, (_, i) => {
  const t = Date.UTC(2024, 0, 1 + i)
  return { short: WEEKDAY_NARROW.format(t), long: WEEKDAY_LONG.format(t) }
})
export const longDay = (key: string) => { const p = parseKey(key); return p ? LONG_FMT.format(Date.UTC(p.y, p.m - 1, p.d)) : key }
/** "Thu, Oct 29": one short line for a row; use `longDay` for the full date in a title or aria-label. */
const SHORT_FMT = new Intl.DateTimeFormat(undefined, { weekday: 'short', month: 'short', day: 'numeric', timeZone: 'UTC' })
export const shortDay = (key: string) => { const p = parseKey(key); return p ? SHORT_FMT.format(Date.UTC(p.y, p.m - 1, p.d)) : key }
export const monthTitle = (ref: MonthRef) => MONTH_FMT.format(Date.UTC(ref.y, ref.m - 1, 1))

const TONE_VAR: Record<DayMark['tone'], string> = { danger: 'var(--danger)', warn: 'var(--warn)', accent: 'var(--accent)' }

export function Calendar({
  month, onMonth, today, marks, selected, onSelect, label, noun = 'incident', nounPlural, compact = false, minMonth, maxMonth,
}: {
  month: MonthRef
  onMonth: (m: MonthRef) => void
  /** Today's date in the zone the calendar is shown in. */
  today: string
  marks: ReadonlyMap<string, DayMark>
  selected: string | null
  /** Called with the date, or null when the selected day is chosen again. */
  onSelect?: (key: string | null) => void
  label: string
  noun?: string
  nounPlural?: string
  compact?: boolean
  minMonth?: MonthRef
  maxMonth?: MonthRef
}) {
  const plural = nounPlural ?? `${noun}s`
  const first = weekdayOf(month.y, month.m, 1)
  const count = daysIn(month.y, month.m)
  const monthKey = `${month.y}-${pad(month.m)}`
  const [focusKey, setFocusKey] = useState<string>(() => selected && selected.startsWith(monthKey) ? selected : today.startsWith(monthKey) ? today : keyOf(month.y, month.m, 1))
  const grid = useRef<HTMLDivElement>(null)
  const pendingFocus = useRef(false)
  // The tab stop always sits on a day of the visible month.
  const stop = focusKey.startsWith(monthKey) ? focusKey : selected && selected.startsWith(monthKey) ? selected : today.startsWith(monthKey) ? today : keyOf(month.y, month.m, 1)

  useEffect(() => {
    if (!pendingFocus.current) return
    pendingFocus.current = false
    grid.current?.querySelector<HTMLElement>(`[data-date="${stop}"]`)?.focus()
  }, [stop, month])

  const canPrev = !minMonth || cmpMonth(month, minMonth) > 0
  const canNext = !maxMonth || cmpMonth(month, maxMonth) < 0

  function moveTo(key: string) {
    const m = monthOfKey(key)
    if ((minMonth && cmpMonth(m, minMonth) < 0) || (maxMonth && cmpMonth(m, maxMonth) > 0)) return
    pendingFocus.current = true
    setFocusKey(key)
    if (m.y !== month.y || m.m !== month.m) onMonth(m)
  }

  function onKey(e: KeyboardEvent<HTMLDivElement>) {
    const p = parseKey(stop)!
    const wd = weekdayOf(p.y, p.m, p.d)
    let next: string | null = null
    switch (e.key) {
      case 'ArrowLeft': next = addDays(stop, -1); break
      case 'ArrowRight': next = addDays(stop, 1); break
      case 'ArrowUp': next = addDays(stop, -7); break
      case 'ArrowDown': next = addDays(stop, 7); break
      case 'Home': next = addDays(stop, -wd); break
      case 'End': next = addDays(stop, 6 - wd); break
      case 'PageUp': case 'PageDown': {
        const m = shiftMonth(month, e.key === 'PageUp' ? -1 : 1)
        next = keyOf(m.y, m.m, Math.min(p.d, daysIn(m.y, m.m)))
        break
      }
    }
    if (!next) return
    e.preventDefault()
    moveTo(next)
  }

  const cells = useMemo(() => {
    const out: (number | null)[] = Array(first).fill(null)
    for (let d = 1; d <= count; d++) out.push(d)
    while (out.length % 7 !== 0) out.push(null)
    const rows: (number | null)[][] = []
    for (let i = 0; i < out.length; i += 7) rows.push(out.slice(i, i + 7))
    return rows
  }, [first, count])

  const nav = 'inline-flex items-center justify-center w-9 h-9 rounded-[10px] text-muted hover:text-body hover:bg-surface-hover disabled:opacity-40 disabled:pointer-events-none transition-colors'
  const cell = compact ? 'h-9' : 'h-11'

  return (
    <div className="w-full">
      <div className="flex items-center justify-between gap-2 mb-2">
        <h3 className="text-[14px] font-semibold text-body" aria-live="polite">{monthTitle(month)}</h3>
        <div className="flex items-center gap-1">
          <button type="button" className={nav} disabled={!canPrev} aria-label={`Previous month, ${monthTitle(shiftMonth(month, -1))}`} onClick={() => onMonth(shiftMonth(month, -1))}><Icon.Chevron className="w-4 h-4 rotate-180" /></button>
          <button type="button" className={nav} disabled={!canNext} aria-label={`Next month, ${monthTitle(shiftMonth(month, 1))}`} onClick={() => onMonth(shiftMonth(month, 1))}><Icon.Chevron className="w-4 h-4" /></button>
        </div>
      </div>
      <div ref={grid} role="grid" aria-label={`${label}, ${monthTitle(month)}`} onKeyDown={onKey} className="flex flex-col gap-1">
        <div role="row" className="grid grid-cols-7 gap-1">
          {weekdayLabels.map(w => <span key={w.long} role="columnheader" aria-label={w.long} className="text-center text-[11px] font-medium text-faint py-1">{w.short}</span>)}
        </div>
        {cells.map((row, ri) => (
          <div key={ri} role="row" className="grid grid-cols-7 gap-1">
            {row.map((d, ci) => {
              if (d == null) return <span key={ci} role="gridcell" aria-hidden="true" />
              const key = keyOf(month.y, month.m, d)
              const mark = marks.get(key)
              const isSel = selected === key
              const isToday = today === key
              const spoken = `${longDay(key)}${isToday ? ', today' : ''}${mark ? `, ${mark.count} ${mark.count === 1 ? noun : plural}` : `, no ${plural}`}`
              const tint = mark ? Math.min(46, 16 + mark.count * 10) : 0
              return (
                <span key={ci} role="gridcell" aria-selected={isSel}>
                  <button
                    type="button"
                    data-date={key}
                    aria-label={spoken}
                    aria-pressed={onSelect ? isSel : undefined}
                    tabIndex={key === stop ? 0 : -1}
                    onFocus={() => setFocusKey(key)}
                    onClick={() => { setFocusKey(key); onSelect?.(isSel ? null : key) }}
                    style={mark ? { backgroundColor: `color-mix(in srgb, ${TONE_VAR[mark.tone]} ${tint}%, var(--surface))` } : undefined}
                    className={`relative w-full ${cell} rounded-[10px] border text-[13px] tnum flex items-center justify-center transition-colors ${
                      isSel ? 'border-accent bg-accent-subtle text-body font-semibold' : isToday ? 'border-strong text-body font-semibold' : 'border-transparent text-body-2'
                    } ${mark || isSel ? '' : 'hover:bg-surface-2'} ${onSelect ? 'cursor-pointer' : 'cursor-default'}`}
                  >
                    {d}
                    {mark && (
                      <>
                        {!compact && mark.count > 1 && <span aria-hidden="true" className="absolute top-0.5 right-1.5 text-[10px] font-semibold text-body-2">{mark.count}</span>}
                        <span aria-hidden="true" className="absolute left-2.5 right-2.5 bottom-1 h-[3px] rounded-full" style={{ backgroundColor: TONE_VAR[mark.tone] }} />
                      </>
                    )}
                  </button>
                </span>
              )
            })}
          </div>
        ))}
      </div>
    </div>
  )
}
