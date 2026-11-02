import { serverTimeZone } from './runtime.ts'
// Small, dependency-free formatters shared across every page. Every
// timestamp is unix milliseconds; every uptime figure is a 0–100 percentage.
// Operational timestamps follow the configured server zone.

const DAY_MS = 86_400_000

/** Compact duration: seconds under a minute, then `12m`, `3h 4m`, `2d 1h`. */
export function duration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '0s'
  let s = Math.floor(ms / 1000)
  if (s < 60) return `${s}s`
  const days = Math.floor(s / 86400)
  s -= days * 86400
  const hours = Math.floor(s / 3600)
  s -= hours * 3600
  const mins = Math.floor(s / 60)
  if (days > 0) return hours > 0 ? `${days}d ${hours}h` : `${days}d`
  if (hours > 0) return mins > 0 ? `${hours}h ${mins}m` : `${hours}h`
  return `${mins}m`
}

/** Relative time, e.g. "just now", "12m ago". */
export function ago(at: number): string {
  if (!Number.isFinite(at) || at <= 0) return '—'
  const diff = Date.now() - at
  if (diff < 5_000) return 'just now'
  return `${duration(diff)} ago`
}

/** Uptime percentage: two decimals, floored; "100%" only when it is exactly 100. */
export function pct(n: number | null | undefined): string {
  if (n == null || Number.isNaN(n)) return '—'
  // Floored, never rounded up: an uptime of 99.996 % had downtime, and
  // "100%" beside a day that shows an outage reads as a contradiction.
  if (n >= 100) return '100%'
  const floored = Math.floor(n * 100 + 1e-6) / 100 // 1e-6: 99.99 * 100 is 9998.999…
  if (floored <= 0) return '0%'
  return `${floored.toFixed(2)}%`
}

/** "99.9%" — floored to one decimal, so 99.97 never reads as a clean 100%. */
export function pctShort(n: number): string {
  if (n >= 100) return '100%'
  if (n <= 0) return '0%'
  return `${(Math.floor(n * 10) / 10).toFixed(1)}%`
}

/** A latency in ms, switching to seconds past 1000 ms. */
export function ms(n: number | null | undefined): string {
  if (n == null || Number.isNaN(n) || n < 0) return '—'
  if (n < 1000) return `${Math.round(n)} ms`
  return `${(n / 1000).toFixed(2)} s`
}

export function plural(n: number, word: string, pluralWord = `${word}s`): string {
  return `${n} ${n === 1 ? word : pluralWord}`
}

const LOCAL_DT = new Intl.DateTimeFormat(undefined, { day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit', timeZoneName: 'short' })
const LOCAL_TIME = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', timeZoneName: 'short' })
const LOCAL_DAY = new Intl.DateTimeFormat(undefined, { weekday: 'short', day: 'numeric', month: 'short', year: 'numeric' })
const LOCAL_MONTH = new Intl.DateTimeFormat(undefined, { month: 'long', year: 'numeric' })

/** "12 Oct 2026, 14:05 GMT+1" in the viewer's own time zone. */
export function dateLocal(at: number): string {
  if (!Number.isFinite(at) || at <= 0) return '—'
  return LOCAL_DT.format(new Date(at))
}

/** "14:05 GMT+1" in the viewer's own time zone. */
export function timeLocal(at: number): string {
  if (!Number.isFinite(at) || at <= 0) return '—'
  return LOCAL_TIME.format(new Date(at))
}

/** "Mon, 12 Oct 2026" in the viewer's own time zone. */
export function dayLocal(at: number): string {
  return LOCAL_DAY.format(new Date(at))
}

/** "October 2026" in the viewer's own time zone. */
export function monthLocal(at: number): string {
  return LOCAL_MONTH.format(new Date(at))
}

/** The viewer's IANA zone, e.g. "Europe/Berlin". */
export function localZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || 'local time'
}

function serverFormatter(options: Intl.DateTimeFormatOptions): Intl.DateTimeFormat {
  return new Intl.DateTimeFormat(undefined, { ...options, timeZone: serverTimeZone() })
}

export function serverZoneLabel(at = Date.now()): string {
  return serverFormatter({ timeZoneName: 'short' }).formatToParts(new Date(at)).find(part => part.type === 'timeZoneName')?.value ?? serverTimeZone()
}

/** Operational timestamps use the configured server (display) zone. */
export function dateServer(at: number): string {
  if (!Number.isFinite(at) || at <= 0) return '—'
  return serverFormatter({ day: '2-digit', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZoneName: 'short' }).format(new Date(at))
}
export function dateOnlyServer(at: number): string {
  if (!Number.isFinite(at)) return '—'
  return serverFormatter({ day: '2-digit', month: 'short', year: 'numeric' }).format(new Date(at))
}
export function timeServer(at: number): string {
  if (!Number.isFinite(at) || at <= 0) return '—'
  return serverFormatter({ hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(new Date(at))
}
export function whenServer(at: number): string {
  const diff = at - Date.now()
  const rel = diff > 0 ? `in ${duration(diff)}` : `${duration(-diff)} ago`
  return `${serverFormatter({ weekday: 'short' }).format(new Date(at))} ${timeServer(at)} ${serverZoneLabel(at)} (${rel})`
}
export function ymdServer(at: number): string {
  return utcMsToZonedDateTime(at, serverTimeZone()).slice(0, 10)
}

const zoneFormatters = new Map<string, Intl.DateTimeFormat>()
function partsInZone(at: number, tz: string): number[] {
  let formatter = zoneFormatters.get(tz)
  if (!formatter) {
    formatter = new Intl.DateTimeFormat('en-GB', { timeZone: tz, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
    zoneFormatters.set(tz, formatter)
  }
  const parts = formatter.formatToParts(new Date(at))
  return ['year', 'month', 'day', 'hour', 'minute'].map(type => Number(parts.find(part => part.type === type)?.value))
}
const pad = (n: number) => String(n).padStart(2, '0')
const wallString = ([year, month, day, hour, minute]: number[]) => `${year}-${pad(month)}-${pad(day)}T${pad(hour)}:${pad(minute)}`

/** Reject DST gaps; an ambiguous fall-back time selects the earlier instant. */
export function zonedDateTimeToUtcMs(dtLocal: string, tz: string): number | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(dtLocal)
  if (!match) return null
  const [y, mo, d, h, mi] = match.slice(1).map(Number)
  if (y < 100 || mo < 1 || mo > 12 || d < 1 || h > 23 || mi > 59) return null
  const wall = Date.UTC(y, mo - 1, d, h, mi)
  if (new Date(wall).getUTCDate() !== d) return null
  try {
    const offsets = new Set<number>()
    for (let delta = -36; delta <= 36; delta += 6) {
      const probe = wall + delta * 3_600_000
      const [py, pm, pd, ph, pmin] = partsInZone(probe, tz)
      offsets.add(Date.UTC(py, pm - 1, pd, ph, pmin) - probe)
    }
    const matches = [...offsets].map(offset => wall - offset).filter(at => wallString(partsInZone(at, tz)) === wallString([y, mo, d, h, mi]))
    return matches.length ? Math.min(...matches) : null
  } catch { return null }
}
export function utcMsToZonedDateTime(at: number | null | undefined, tz: string): string {
  if (at == null || !Number.isFinite(at)) return ''
  try { return wallString(partsInZone(at, tz)) } catch { return '' }
}
/** Calendar midnight in the configured server zone. */
export function serverDayStartMs(yyyyMmDd: string): number | null {
  const midnight = zonedDateTimeToUtcMs(`${yyyyMmDd}T00:00`, serverTimeZone())
  if (midnight != null) return midnight
  if (!/^\d{4}-\d{2}-\d{2}$/.test(yyyyMmDd)) return null
  // Some zones advance the clock at midnight. A calendar day starts at
  // its first real local minute, while maintenance input still rejects gaps.
  const [year, month, day] = yyyyMmDd.split('-').map(Number)
  const check = new Date(Date.UTC(year, month - 1, day))
  if (check.getUTCFullYear() !== year || check.getUTCMonth() !== month - 1 || check.getUTCDate() !== day) return null
  for (let minute = 1; minute < 24 * 60; minute++) {
    const instant = zonedDateTimeToUtcMs(`${yyyyMmDd}T${pad(Math.floor(minute / 60))}:${pad(minute % 60)}`, serverTimeZone())
    if (instant != null) return instant
  }
  return null
}
/** DST-aware end of a server calendar day. */
export function serverDayEndMs(day: number): number {
  return serverDayOffsetMs(day, 1)
}
/** Move by calendar dates in the server zone, never by fixed 24-hour intervals. */
export function serverDayOffsetMs(at: number, offset: number): number {
  const [year, month, date] = ymdServer(at).split('-').map(Number)
  const next = new Date(Date.UTC(year, month - 1, date + offset)).toISOString().slice(0, 10)
  return serverDayStartMs(next) ?? at + offset * DAY_MS
}
export { DAY_MS }
