// The time zone a status page shows its dates in. A visitor can read times in
// their own zone or in the installation's server zone; the choice is a
// per-browser convenience (never sent anywhere). The zone's name is always
// shown next to the dates so a time is never ambiguous.

import { useCallback, useMemo, useState } from 'react'
import { localZone } from '../../lib/format'
import { serverTimeZone } from '../../lib/runtime'

const KEY = 'opskest-status-zone'
export type ZoneMode = 'local' | 'server'

export interface DisplayZone {
  mode: ZoneMode
  /** IANA name used for every date on the page. */
  tz: string
  /** "Europe/Berlin" or "UTC", with the short form where the runtime has one: "Europe/Berlin (GMT+1)". */
  label: string
  /** True when the viewer's zone and the server's are the same, so there is nothing to choose. */
  single: boolean
  setMode: (m: ZoneMode) => void
}

function readMode(): ZoneMode {
  try { return window.localStorage.getItem(KEY) === 'server' ? 'server' : 'local' } catch { return 'local' }
}

export function shortZone(tz: string, at = Date.now()): string {
  try {
    return new Intl.DateTimeFormat(undefined, { timeZone: tz, timeZoneName: 'short' }).formatToParts(new Date(at)).find(p => p.type === 'timeZoneName')?.value ?? ''
  } catch { return '' }
}

export function useDisplayZone(): DisplayZone {
  const [mode, setModeState] = useState<ZoneMode>(readMode)
  const setMode = useCallback((m: ZoneMode) => {
    setModeState(m)
    try { window.localStorage.setItem(KEY, m) } catch { /* the choice just does not persist */ }
  }, [])
  return useMemo(() => {
    const local = localZone()
    const server = serverTimeZone()
    const single = local === server
    const tz = single || mode === 'local' ? local : server
    const short = shortZone(tz)
    return { mode: single ? 'local' : mode, tz, label: short && short !== tz ? `${tz} (${short})` : tz, single, setMode }
  }, [mode, setMode])
}

const fmtCache = new Map<string, Intl.DateTimeFormat>()
function fmt(tz: string, key: string, opts: Intl.DateTimeFormatOptions): Intl.DateTimeFormat {
  const k = `${tz}|${key}`
  let f = fmtCache.get(k)
  if (!f) {
    try { f = new Intl.DateTimeFormat(undefined, { ...opts, timeZone: tz }) } catch { f = new Intl.DateTimeFormat(undefined, opts) }
    fmtCache.set(k, f)
  }
  return f
}

/** "28 Oct 2026, 14:05 GMT+1" */
export function dateTimeIn(at: number, tz: string): string {
  if (!Number.isFinite(at) || at <= 0) return '—'
  return fmt(tz, 'dt', { day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit', timeZoneName: 'short' }).format(new Date(at))
}
/** "14:05 GMT+1" */
export function timeIn(at: number, tz: string): string {
  if (!Number.isFinite(at) || at <= 0) return '—'
  return fmt(tz, 't', { hour: '2-digit', minute: '2-digit', timeZoneName: 'short' }).format(new Date(at))
}
/** "Tue, 28 Oct 2026" */
export function dayIn(at: number, tz: string): string {
  return fmt(tz, 'd', { weekday: 'short', day: 'numeric', month: 'short', year: 'numeric' }).format(new Date(at))
}

const ymdCache = new Map<string, Intl.DateTimeFormat>()
/** "2026-10-28": the calendar date of an instant in a zone. */
export function ymdIn(at: number, tz: string): string {
  let f = ymdCache.get(tz)
  if (!f) {
    try { f = new Intl.DateTimeFormat('en-CA', { timeZone: tz, year: 'numeric', month: '2-digit', day: '2-digit' }) } catch { f = new Intl.DateTimeFormat('en-CA', { year: 'numeric', month: '2-digit', day: '2-digit' }) }
    ymdCache.set(tz, f)
  }
  const p = f.formatToParts(new Date(at))
  const get = (t: string) => p.find(x => x.type === t)?.value ?? '00'
  return `${get('year')}-${get('month')}-${get('day')}`
}

/** Two buttons: your zone or the server's. Hidden when they are the same. */
export function ZoneSwitch({ zone, className = '' }: { zone: DisplayZone; className?: string }) {
  return (
    <div className={`flex flex-wrap items-center gap-x-3 gap-y-1.5 text-[12px] text-muted ${className}`}>
      <span>Times in <span className="font-medium text-body-2">{zone.label}</span></span>
      {!zone.single && (
        <div role="group" aria-label="Time zone" className="inline-flex rounded-[10px] bg-surface-2 p-0.5 gap-0.5">
          {([['local', 'Your time'], ['server', 'Server time']] as const).map(([m, text]) => (
            <button
              key={m}
              type="button"
              aria-pressed={zone.mode === m}
              onClick={() => zone.setMode(m)}
              className={`px-2.5 h-7 rounded-lg text-[12px] font-medium transition-colors ${zone.mode === m ? 'bg-surface text-body' : 'text-muted hover:text-body'}`}
            >{text}</button>
          ))}
        </div>
      )}
    </div>
  )
}
