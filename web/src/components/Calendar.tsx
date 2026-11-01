// Calendar — a keyboard-operable month grid for picking a day or a range, and
// for showing marked days (scheduled work, outages). It works on plain calendar
// dates ('YYYY-MM-DD', see lib/calendar.ts), so no time zone or DST change can
// move a day; the zone only decides what "today" is.
//
// Keys on the grid: arrows move a day / a week, Home and End go to the start
// and end of the week, PageUp/PageDown change month (with Shift, year), Enter
// and Space choose. Focus is a single tab stop (roving tabindex).

import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import {
  addDays, addMonths, clampYmd, monthGrid, monthStart, parseYmd, weekdayOf, ymdInZone, type Ymd,
} from '../lib/calendar'
import { serverTimeZone } from '../lib/runtime'
import { Icon } from './icons'

export interface CalendarMark {
  tone: 'accent' | 'warn' | 'danger' | 'ok' | 'neutral'
  /** Spoken with the date, e.g. "Database upgrade". */
  label?: string
}

const DOT: Record<CalendarMark['tone'], string> = {
  accent: 'bg-accent', warn: 'bg-warn', danger: 'bg-danger', ok: 'bg-ok', neutral: 'bg-faint',
}

const FULL = new Intl.DateTimeFormat(undefined, { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric', timeZone: 'UTC' })
const MONTH = new Intl.DateTimeFormat(undefined, { month: 'long', year: 'numeric', timeZone: 'UTC' })
const WEEKDAY_SHORT = new Intl.DateTimeFormat(undefined, { weekday: 'short', timeZone: 'UTC' })
const WEEKDAY_LONG = new Intl.DateTimeFormat(undefined, { weekday: 'long', timeZone: 'UTC' })

function asDate(ymd: Ymd): Date {
  const p = parseYmd(ymd)!
  return new Date(Date.UTC(p.y, p.m - 1, p.d))
}

type Selection =
  | { mode?: 'single'; value: Ymd | ''; onChange: (ymd: Ymd) => void }
  | { mode: 'range'; start: Ymd | ''; end: Ymd | ''; onRangeChange: (start: Ymd, end: Ymd | '') => void }

export type CalendarProps = Selection & {
  min?: Ymd
  max?: Ymd
  /** Days to mark with a coloured dot, keyed by date. Several marks on one date are an array. */
  marks?: Record<Ymd, CalendarMark[]>
  /** The zone that decides which date is "today". Defaults to the display (server) zone. */
  tz?: string
  /** First column: 1 = Monday (default), 0 = Sunday. */
  weekStart?: 0 | 1
  /** Roomier cells for a page-level calendar. */
  size?: 'md' | 'lg'
  /** Move focus into the grid on mount (popovers). */
  autoFocus?: boolean
  /** Called when a day is chosen with Enter/click in single mode, or the range is completed. */
  onCommit?: () => void
  /** Called as the visible month changes. */
  onMonthChange?: (monthStartYmd: Ymd) => void
  label?: string
  className?: string
}

export function Calendar(props: CalendarProps) {
  const { min, max, marks, weekStart = 1, size = 'md', autoFocus, onMonthChange, label = 'Calendar', className = '' } = props
  const tz = props.tz ?? serverTimeZone()
  const today = ymdInZone(Date.now(), tz)
  const selected = props.mode === 'range' ? props.start : props.value

  const initial = clampYmd(parseYmd(selected) ? selected : today, min, max)
  const [focus, setFocus] = useState<Ymd>(initial)
  const [anchor, setAnchor] = useState<Ymd | ''>('') // first click of a range, waiting for the second
  const [hover, setHover] = useState<Ymd | ''>('')
  const grid = useRef<HTMLTableElement>(null)
  const wantFocus = useRef(!!autoFocus)
  const view = monthStart(focus)

  // Follow the value when the owner changes it (typing in the field).
  useEffect(() => {
    if (parseYmd(selected)) setFocus(selected)
  }, [selected])
  useEffect(() => { onMonthChange?.(view) }, [view]) // eslint-disable-line react-hooks/exhaustive-deps

  // After a keyboard move, put DOM focus on the new day once it has rendered.
  useEffect(() => {
    if (!wantFocus.current) return
    wantFocus.current = false
    grid.current?.querySelector<HTMLElement>(`[data-ymd="${focus}"]`)?.focus()
  }, [focus])

  const weeks = useMemo(() => monthGrid(view, weekStart), [view, weekStart])
  const heads = weeks[0].map(d => ({ short: WEEKDAY_SHORT.format(asDate(d.ymd)), long: WEEKDAY_LONG.format(asDate(d.ymd)) }))
  const disabled = (ymd: Ymd) => (min != null && ymd < min) || (max != null && ymd > max)

  function go(ymd: Ymd, keyboard = false) {
    const next = clampYmd(ymd, min, max)
    if (keyboard) wantFocus.current = true
    setFocus(next)
  }

  function choose(ymd: Ymd) {
    if (disabled(ymd)) return
    setFocus(ymd)
    if (props.mode === 'range') {
      if (!anchor) {
        setAnchor(ymd)
        props.onRangeChange(ymd, '')
      } else {
        const [a, b] = ymd < anchor ? [ymd, anchor] : [anchor, ymd]
        setAnchor('')
        setHover('')
        props.onRangeChange(a, b)
        props.onCommit?.()
      }
    } else {
      props.onChange(ymd)
      props.onCommit?.()
    }
  }

  function onKeyDown(e: KeyboardEvent<HTMLTableElement>) {
    let next: Ymd | null = null
    const col = (weekdayOf(focus) - weekStart + 7) % 7
    if (e.key === 'ArrowLeft') next = addDays(focus, -1)
    else if (e.key === 'ArrowRight') next = addDays(focus, 1)
    else if (e.key === 'ArrowUp') next = addDays(focus, -7)
    else if (e.key === 'ArrowDown') next = addDays(focus, 7)
    else if (e.key === 'Home') next = addDays(focus, -col)
    else if (e.key === 'End') next = addDays(focus, 6 - col)
    else if (e.key === 'PageUp') next = addMonths(focus, e.shiftKey ? -12 : -1)
    else if (e.key === 'PageDown') next = addMonths(focus, e.shiftKey ? 12 : 1)
    if (next) { e.preventDefault(); go(next, true) }
    else if (e.key === 'Escape' && anchor) { e.preventDefault(); e.stopPropagation(); setAnchor(''); setHover('') }
  }

  const inRange = (ymd: Ymd) => {
    if (props.mode !== 'range') return false
    const a = anchor || props.start
    const b = anchor ? (hover || anchor) : props.end
    if (!a || !b) return false
    const [lo, hi] = a <= b ? [a, b] : [b, a]
    return ymd >= lo && ymd <= hi
  }
  const isEdge = (ymd: Ymd) => {
    if (props.mode !== 'range') return ymd === selected
    return anchor ? ymd === anchor : ymd === props.start || ymd === props.end
  }

  const cell = size === 'lg' ? 'h-14 w-full text-[14px]' : 'h-9 w-9 text-[13px]'
  const navButton = 'inline-flex h-8 w-8 items-center justify-center rounded-lg text-muted transition-colors hover:bg-surface-hover hover:text-body disabled:opacity-40 disabled:hover:bg-transparent'
  const nextMonth = addMonths(view, 1)

  return (
    <div className={`${size === 'lg' ? 'w-full' : 'w-[280px] max-w-full'} select-none ${className}`}>
      <div className="mb-2 flex items-center gap-1">
        <button type="button" className={navButton} aria-label="Previous month" disabled={min != null && addDays(view, -1) < min}
          onClick={() => go(clampYmd(addMonths(focus, -1), min, max))}>
          <Icon.ChevronDown className="h-4 w-4 rotate-90" />
        </button>
        <h2 aria-live="polite" className="min-w-0 flex-1 text-center text-sm font-semibold text-body">{MONTH.format(asDate(view))}</h2>
        <button type="button" className={navButton} aria-label="Next month" disabled={max != null && nextMonth > max}
          onClick={() => go(clampYmd(addMonths(focus, 1), min, max))}>
          <Icon.ChevronDown className="h-4 w-4 -rotate-90" />
        </button>
        <button type="button" onClick={() => go(clampYmd(today, min, max))}
          className="ml-1 inline-flex h-8 items-center rounded-lg px-2.5 text-[12px] font-medium text-accent transition-colors hover:bg-accent-subtle">
          Today
        </button>
      </div>
      <table ref={grid} role="grid" aria-label={`${label}, ${MONTH.format(asDate(view))}`} onKeyDown={onKeyDown} className="w-full border-collapse">
        <thead>
          <tr>
            {heads.map(h => (
              <th key={h.long} scope="col" className="pb-1 text-center text-[11px] font-medium text-faint">
                <abbr title={h.long} className="no-underline">{h.short.slice(0, 2)}</abbr>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {weeks.map(week => (
            <tr key={week[0].ymd}>
              {week.map(({ ymd, inMonth }) => {
                const off = disabled(ymd)
                const on = isEdge(ymd)
                const between = !on && inRange(ymd)
                const dayMarks = marks?.[ymd]
                const labelText = [
                  FULL.format(asDate(ymd)),
                  ymd === today ? 'today' : '',
                  ...(dayMarks ?? []).map(m => m.label).filter(Boolean) as string[],
                ].filter(Boolean).join(', ')
                return (
                  <td key={ymd} role="gridcell" aria-selected={on || between || undefined} className="p-0 text-center">
                    <button
                      type="button"
                      data-ymd={ymd}
                      tabIndex={ymd === focus ? 0 : -1}
                      disabled={off}
                      aria-label={labelText}
                      aria-current={ymd === today ? 'date' : undefined}
                      onClick={() => choose(ymd)}
                      onMouseEnter={() => { if (anchor) setHover(ymd) }}
                      className={`${cell} relative mx-auto flex flex-col items-center justify-center rounded-lg tnum transition-colors disabled:cursor-not-allowed disabled:opacity-35
                        ${on ? 'bg-accent font-semibold text-accent-fg'
                          : between ? 'bg-accent-subtle text-accent'
                            : `hover:bg-surface-hover ${inMonth ? 'text-body-2' : 'text-faint'}`}
                        ${ymd === today && !on ? 'font-semibold text-accent ring-1 ring-inset ring-accent-border' : ''}`}
                    >
                      <span aria-hidden="true">{asDate(ymd).getUTCDate()}</span>
                      {dayMarks && dayMarks.length > 0 && (
                        <span aria-hidden="true" className={`absolute flex gap-0.5 ${size === 'lg' ? 'bottom-2' : 'bottom-1'}`}>
                          {dayMarks.slice(0, 3).map((m, i) => (
                            <span key={i} className={`h-1.5 w-1.5 rounded-full ${on ? 'bg-accent-fg' : DOT[m.tone]}`} />
                          ))}
                        </span>
                      )}
                    </button>
                  </td>
                )
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

