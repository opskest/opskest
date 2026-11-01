// Civil-date arithmetic for the Calendar and the date/time inputs.
//
// A "ymd" is a plain calendar date, 'YYYY-MM-DD', with no time zone. A "wall"
// is a wall-clock date and time, 'YYYY-MM-DDTHH:mm', also with no zone. All
// calendar maths (adding days, building a month grid, weekday lookups) happens
// on these strings through UTC-based Date arithmetic used only as a calendar
// calculator, so a daylight-saving change can never shift a date. Time zones
// enter only where an instant (unix milliseconds) is converted to or from a
// date: ymdInZone() and the wall helpers in format.ts do that.

import { utcMsToZonedDateTime, zonedDateTimeToUtcMs } from './format.ts'

export type Ymd = string
export type Wall = string

const pad = (n: number) => String(n).padStart(2, '0')

export interface YmdParts { y: number; m: number; d: number }

export function daysInMonth(y: number, m: number): number {
  return new Date(Date.UTC(y, m, 0)).getUTCDate()
}

/** Splits a valid 'YYYY-MM-DD'; null for anything else, including 2026-02-30. */
export function parseYmd(s: string): YmdParts | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s)
  if (!match) return null
  const y = Number(match[1])
  const m = Number(match[2])
  const d = Number(match[3])
  if (y < 1 || m < 1 || m > 12 || d < 1 || d > daysInMonth(y, m)) return null
  return { y, m, d }
}

export function formatYmd(y: number, m: number, d: number): Ymd {
  return `${String(y).padStart(4, '0')}-${pad(m)}-${pad(d)}`
}

function fromUtc(date: Date): Ymd {
  return formatYmd(date.getUTCFullYear(), date.getUTCMonth() + 1, date.getUTCDate())
}

/** Moves by whole calendar days; never affected by DST. */
export function addDays(ymd: Ymd, n: number): Ymd {
  const p = parseYmd(ymd)
  if (!p) return ymd
  return fromUtc(new Date(Date.UTC(p.y, p.m - 1, p.d + n)))
}

/** Moves by calendar months, clamping the day (31 Jan + 1 month = 28 Feb). */
export function addMonths(ymd: Ymd, n: number): Ymd {
  const p = parseYmd(ymd)
  if (!p) return ymd
  const index = p.y * 12 + (p.m - 1) + n
  const y = Math.floor(index / 12)
  const m = (index % 12 + 12) % 12 + 1
  return formatYmd(y, m, Math.min(p.d, daysInMonth(y, m)))
}

export function monthStart(ymd: Ymd): Ymd {
  const p = parseYmd(ymd)
  return p ? formatYmd(p.y, p.m, 1) : ymd
}

/** 0 = Sunday … 6 = Saturday. */
export function weekdayOf(ymd: Ymd): number {
  const p = parseYmd(ymd)
  return p ? new Date(Date.UTC(p.y, p.m - 1, p.d)).getUTCDay() : 0
}

/** Lexical order is date order for valid ymd strings. */
export const compareYmd = (a: Ymd, b: Ymd) => (a < b ? -1 : a > b ? 1 : 0)

export function clampYmd(ymd: Ymd, min?: Ymd, max?: Ymd): Ymd {
  if (min && ymd < min) return min
  if (max && ymd > max) return max
  return ymd
}

/** Calendar days from `a` to `b` (b - a). */
export function diffDays(a: Ymd, b: Ymd): number {
  const pa = parseYmd(a)
  const pb = parseYmd(b)
  if (!pa || !pb) return 0
  return Math.round((Date.UTC(pb.y, pb.m - 1, pb.d) - Date.UTC(pa.y, pa.m - 1, pa.d)) / 86_400_000)
}

export interface GridDay { ymd: Ymd; inMonth: boolean }

/**
 * The weeks that cover the month of `ymd`, as rows of 7 days. Always six rows
 * (so the popover never changes height when you page between months).
 * `weekStart` is the first column: 1 = Monday, 0 = Sunday.
 */
export function monthGrid(ymd: Ymd, weekStart: 0 | 1 = 1): GridDay[][] {
  const first = monthStart(ymd)
  const lead = (weekdayOf(first) - weekStart + 7) % 7
  const start = addDays(first, -lead)
  const month = first.slice(0, 7)
  const weeks: GridDay[][] = []
  for (let w = 0; w < 6; w++) {
    const row: GridDay[] = []
    for (let i = 0; i < 7; i++) {
      const day = addDays(start, w * 7 + i)
      row.push({ ymd: day, inMonth: day.slice(0, 7) === month })
    }
    weeks.push(row)
  }
  return weeks
}

// ── Instants ⇄ dates, in a named time zone ─────────────────────────────────

/** The calendar date an instant falls on in `tz`. */
export function ymdInZone(at: number, tz: string): Ymd {
  return utcMsToZonedDateTime(at, tz).slice(0, 10)
}

/** The wall-clock reading of an instant in `tz`, 'YYYY-MM-DDTHH:mm'. */
export function wallInZone(at: number, tz: string): Wall {
  return utcMsToZonedDateTime(at, tz)
}

/**
 * The first instant of a calendar day in `tz`. Zones that skip midnight (a DST
 * jump at 00:00) start the day at their first real minute.
 */
export function dayStartInZone(ymd: Ymd, tz: string): number | null {
  if (!parseYmd(ymd)) return null
  for (let minute = 0; minute < 24 * 60; minute++) {
    const at = zonedDateTimeToUtcMs(`${ymd}T${pad(Math.floor(minute / 60))}:${pad(minute % 60)}`, tz)
    if (at != null) return at
  }
  return null
}

/** The instant the day after `ymd` starts, so a day is [start, end). */
export function dayEndInZone(ymd: Ymd, tz: string): number | null {
  return dayStartInZone(addDays(ymd, 1), tz)
}

// ── Wall strings and the text a person types ──────────────────────────────

/** Splits a valid wall into its date and minutes-since-midnight. */
export function splitWall(wall: Wall): { ymd: Ymd; minutes: number } | null {
  const match = /^(\d{4}-\d{2}-\d{2})[T ](\d{2}):(\d{2})$/.exec(wall.trim())
  if (!match || !parseYmd(match[1])) return null
  const h = Number(match[2])
  const mi = Number(match[3])
  if (h > 23 || mi > 59) return null
  return { ymd: match[1], minutes: h * 60 + mi }
}

export function joinWall(ymd: Ymd, minutes: number): Wall {
  const m = ((Math.round(minutes) % 1440) + 1440) % 1440
  return `${ymd}T${pad(Math.floor(m / 60))}:${pad(m % 60)}`
}

/** Accepts 'YYYY-MM-DD HH:mm' or the ISO 'T' form; null when it is not a real date and time. */
export function parseWallText(text: string): Wall | null {
  const parts = splitWall(text)
  return parts ? joinWall(parts.ymd, parts.minutes) : null
}

/** 'YYYY-MM-DD HH:mm', the text shown in the field. */
export function formatWallText(wall: Wall): string {
  const parts = splitWall(wall)
  return parts ? `${parts.ymd} ${minutesToText(parts.minutes)}` : ''
}

export function minutesToText(min: number): string {
  const m = ((Math.round(min) % 1440) + 1440) % 1440
  return `${pad(Math.floor(m / 60))}:${pad(m % 60)}`
}

/** '9:5', '09:05', '0905' and '9' are all readable; null when out of range. */
export function parseTimeText(text: string): number | null {
  const t = text.trim()
  let match = /^(\d{1,2}):(\d{1,2})$/.exec(t)
  if (!match) match = /^(\d{2})(\d{2})$/.exec(t)
  if (!match && /^\d{1,2}$/.test(t)) match = [t, t, '0'] as unknown as RegExpExecArray
  if (!match) return null
  const h = Number(match[1])
  const mi = Number(match[2])
  return h > 23 || mi > 59 ? null : h * 60 + mi
}

// ── Marking days with scheduled windows ───────────────────────────────────

/** Every calendar date (in `tz`) touched by [start, end]; an end exactly at midnight does not touch the next day. */
export function daysTouched(start: number, end: number, tz: string): Ymd[] {
  const first = ymdInZone(start, tz)
  const last = ymdInZone(end > start ? end - 1 : end, tz)
  const days: Ymd[] = []
  for (let day = first, guard = 0; day <= last && guard < 400; day = addDays(day, 1), guard++) days.push(day)
  return days
}

export interface WindowSpec {
  kind: 'once' | 'weekly'
  startsAt: number | null
  endsAt: number | null
  /** Bit 0 = Monday … bit 6 = Sunday. */
  weekdays: number
  startMinute: number
  durationMin: number
  tz: string
}

/**
 * Every occurrence of a maintenance window that overlaps [from, to), as
 * instants. A weekly window is expanded per calendar date in its own zone, so
 * 02:30 stays 02:30 across a DST change; a time that does not exist that day
 * (the spring-forward gap) is skipped, matching how the server saves wall times.
 */
export function occurrences(w: WindowSpec, from: number, to: number): { start: number; end: number }[] {
  if (w.kind === 'once') {
    if (w.startsAt == null || w.endsAt == null) return []
    return w.startsAt < to && w.endsAt > from ? [{ start: w.startsAt, end: w.endsAt }] : []
  }
  const out: { start: number; end: number }[] = []
  // One day of slack each side: an occurrence in the window's zone can land on a neighbouring day in the display zone.
  let day = addDays(ymdInZone(from, w.tz), -2)
  const last = addDays(ymdInZone(to, w.tz), 1)
  for (let guard = 0; day <= last && guard < 800; day = addDays(day, 1), guard++) {
    const bit = (weekdayOf(day) + 6) % 7 // Monday = 0
    if ((w.weekdays & (1 << bit)) === 0) continue
    const start = zonedDateTimeToUtcMs(joinWall(day, w.startMinute), w.tz)
    if (start == null) continue
    const end = start + w.durationMin * 60_000
    if (start < to && end > from) out.push({ start, end })
  }
  return out
}
