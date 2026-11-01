// Date, time and date-range fields that pair a typed value with a Calendar (or
// time list) in a popover. Everything is a plain calendar value: 'YYYY-MM-DD'
// for a date, 'YYYY-MM-DDTHH:mm' (a "wall" reading) for a date and time and
// 'HH:mm' for a time of day. The owner turns a wall reading into an instant
// with the zone that applies (zonedDateTimeToUtcMs), so these fields never
// guess a zone and a DST gap is reported by the owner, not silently moved.
//
// The text field is the source of truth for keyboard and automation: type
// 2026-05-20 09:30 (a 'T' also works) and the value follows as soon as it is a
// real date and time. The popover is a convenience over the same value.

import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import {
  formatWallText, joinWall, minutesToText, parseTimeText, parseWallText, parseYmd, splitWall, ymdInZone, type Wall, type Ymd,
} from '../lib/calendar'
import { serverTimeZone } from '../lib/runtime'
import { Calendar, type CalendarMark } from './Calendar'
import { Icon } from './icons'
import { Popover } from './overlay'
import { Tooltip } from './overlay'
import { Input } from './ui'

/** Keeps a typed string in step with a controlled value without fighting the cursor. */
function useTypedValue<V extends string>(value: V, format: (v: V) => string, parse: (text: string) => V | null, empty: V) {
  const [text, setText] = useState(() => format(value))
  const last = useRef<V>(value)
  useEffect(() => {
    if (value !== last.current) { last.current = value; setText(format(value)) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value])
  return {
    text,
    type(t: string): V { const v = parse(t) ?? empty; last.current = v; setText(t); return v },
    settle() { setText(format(last.current)) },
  }
}

interface FieldChrome {
  id?: string
  name?: string
  disabled?: boolean
  placeholder?: string
  'aria-label'?: string
  'aria-invalid'?: boolean
  'aria-describedby'?: string
  className?: string
  /** Accessible name of the calendar button. */
  buttonLabel?: string
}

function CalendarButton({ label, open, onClick, buttonRef, disabled, icon }: {
  label: string; open: boolean; onClick: () => void; buttonRef: React.RefObject<HTMLButtonElement>; disabled?: boolean; icon?: ReactNode
}) {
  return (
    <Tooltip content={label}>
      <button
        ref={buttonRef}
        type="button"
        aria-label={label}
        aria-haspopup="dialog"
        aria-expanded={open}
        disabled={disabled}
        onClick={onClick}
        className="absolute right-1 top-1 inline-flex h-8 w-8 items-center justify-center rounded-lg text-muted transition-colors hover:bg-surface-hover hover:text-body disabled:opacity-50 aria-expanded:bg-surface-hover aria-expanded:text-body"
      >
        {icon ?? <Icon.Calendar className="h-4 w-4" />}
      </button>
    </Tooltip>
  )
}

/** Tab leaves the popover to the button it came from, so the page's tab order carries on from there. */
function tabOut(e: KeyboardEvent<HTMLElement>, close: () => void, button: React.RefObject<HTMLButtonElement>) {
  if (e.key !== 'Tab') return
  const items = Array.from(e.currentTarget.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), [tabindex="0"]'))
  const at = items.indexOf(document.activeElement as HTMLElement)
  if ((!e.shiftKey && at === items.length - 1) || (e.shiftKey && at <= 0)) { close(); button.current?.focus() }
}

// ── Date ────────────────────────────────────────────────────────────────────

export function DateInput({
  value, onChange, min, max, tz, marks, buttonLabel = 'Choose date', className = '', ...rest
}: { value: Ymd | ''; onChange: (ymd: Ymd | '') => void; min?: Ymd; max?: Ymd; tz?: string; marks?: Record<Ymd, CalendarMark[]> } & FieldChrome) {
  const [open, setOpen] = useState(false)
  const anchor = useRef<HTMLDivElement>(null)
  const button = useRef<HTMLButtonElement>(null)
  const typed = useTypedValue<string>(value, v => v, t => (parseYmd(t.trim()) ? t.trim() : null), '')
  const bad = typed.text.trim() !== '' && !parseYmd(typed.text.trim())
  return (
    <div ref={anchor} className={`relative ${className}`}>
      <Input
        {...rest}
        type="text"
        inputMode="numeric"
        autoComplete="off"
        placeholder={rest.placeholder ?? 'YYYY-MM-DD'}
        value={typed.text}
        aria-invalid={rest['aria-invalid'] || bad || undefined}
        onChange={e => onChange(typed.type(e.target.value))}
        onBlur={typed.settle}
        onKeyDown={e => { if (e.altKey && e.key === 'ArrowDown') { e.preventDefault(); setOpen(true) } }}
        className="w-full pr-10"
      />
      <CalendarButton label={buttonLabel} open={open} onClick={() => setOpen(o => !o)} buttonRef={button} disabled={rest.disabled} />
      <Popover open={open} onClose={() => setOpen(false)} anchorRef={anchor} label={buttonLabel}>
        <div className="p-3" onKeyDown={e => tabOut(e, () => setOpen(false), button)}>
          <Calendar autoFocus value={value} min={min} max={max} tz={tz} marks={marks}
            onChange={ymd => onChange(typed.type(ymd))} onCommit={() => { setOpen(false); anchor.current?.querySelector('input')?.focus() }} />
        </div>
      </Popover>
    </div>
  )
}

// ── Date and time ───────────────────────────────────────────────────────────

export function DateTimeInput({
  value, onChange, tz, min, max, defaultMinutes = 9 * 60, buttonLabel = 'Choose date and time', className = '', ...rest
}: {
  value: Wall | ''
  onChange: (wall: Wall | '') => void
  /** Decides "today" in the calendar; defaults to the display zone. */
  tz?: string
  min?: Ymd
  max?: Ymd
  /** Time of day used when a date is picked before any time is set. */
  defaultMinutes?: number
} & FieldChrome) {
  const [open, setOpen] = useState(false)
  const anchor = useRef<HTMLDivElement>(null)
  const button = useRef<HTMLButtonElement>(null)
  const typed = useTypedValue<string>(value, formatWallText, parseWallText, '')
  const parts = splitWall(value)
  const bad = typed.text.trim() !== '' && !parseWallText(typed.text)
  const timeId = useId()
  const [timeText, setTimeText] = useState(() => minutesToText(parts?.minutes ?? defaultMinutes))
  useEffect(() => { if (parts) setTimeText(minutesToText(parts.minutes)) }, [value]) // eslint-disable-line react-hooks/exhaustive-deps

  const emit = (w: Wall | '') => onChange(typed.type(w ? formatWallText(w) : ''))
  const dateOrToday = parts?.ymd ?? ymdInZone(Date.now(), tz ?? serverTimeZone())

  return (
    <div ref={anchor} className={`relative ${className}`}>
      <Input
        {...rest}
        type="text"
        inputMode="numeric"
        autoComplete="off"
        placeholder={rest.placeholder ?? 'YYYY-MM-DD HH:mm'}
        value={typed.text}
        aria-invalid={rest['aria-invalid'] || bad || undefined}
        onChange={e => onChange(typed.type(e.target.value))}
        onBlur={typed.settle}
        onKeyDown={e => { if (e.altKey && e.key === 'ArrowDown') { e.preventDefault(); setOpen(true) } }}
        className="w-full pr-10"
      />
      <CalendarButton label={buttonLabel} open={open} onClick={() => setOpen(o => !o)} buttonRef={button} disabled={rest.disabled} />
      <Popover open={open} onClose={() => setOpen(false)} anchorRef={anchor} label={buttonLabel}>
        <div className="flex flex-col gap-3 p-3" onKeyDown={e => tabOut(e, () => setOpen(false), button)}>
          <Calendar autoFocus value={parts?.ymd ?? ''} min={min} max={max} tz={tz}
            onChange={ymd => emit(joinWall(ymd, parts?.minutes ?? parseTimeText(timeText) ?? defaultMinutes))} />
          <div className="flex items-center gap-2 border-t border-default pt-3">
            <label htmlFor={timeId} className="text-[12px] font-medium text-body-2">Time</label>
            <Input id={timeId} value={timeText} inputMode="numeric" autoComplete="off" placeholder="HH:mm" dense className="w-24 text-center tnum"
              aria-invalid={parseTimeText(timeText) == null || undefined}
              onChange={e => {
                setTimeText(e.target.value)
                const m = parseTimeText(e.target.value)
                if (m != null) emit(joinWall(dateOrToday, m))
              }}
              onBlur={() => { const m = parseTimeText(timeText); if (m != null) setTimeText(minutesToText(m)) }} />
            <button type="button" onClick={() => { setOpen(false); anchor.current?.querySelector('input')?.focus() }}
              className="ml-auto inline-flex h-9 items-center rounded-[10px] bg-accent px-4 text-[13px] font-medium text-accent-fg transition-colors hover:bg-accent-hover">
              Done
            </button>
          </div>
        </div>
      </Popover>
    </div>
  )
}

// ── Date range ──────────────────────────────────────────────────────────────

export function DateRangeInput({
  start, end, onChange, min, max, tz, marks, startLabel = 'From date', endLabel = 'To date', buttonLabel = 'Choose date range', className = '', disabled,
}: {
  start: Ymd | ''
  end: Ymd | ''
  onChange: (start: Ymd | '', end: Ymd | '') => void
  min?: Ymd
  max?: Ymd
  tz?: string
  marks?: Record<Ymd, CalendarMark[]>
  startLabel?: string
  endLabel?: string
  buttonLabel?: string
  className?: string
  disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  const anchor = useRef<HTMLDivElement>(null)
  const button = useRef<HTMLButtonElement>(null)
  const ts = useTypedValue<string>(start, v => v, t => (parseYmd(t.trim()) ? t.trim() : null), '')
  const te = useTypedValue<string>(end, v => v, t => (parseYmd(t.trim()) ? t.trim() : null), '')
  const badS = ts.text.trim() !== '' && !parseYmd(ts.text.trim())
  const badE = te.text.trim() !== '' && !parseYmd(te.text.trim())
  const reversed = start !== '' && end !== '' && start > end
  const field = 'h-full min-w-0 flex-1 bg-transparent text-[14px] text-body tnum placeholder:text-faint disabled:opacity-50'
  return (
    <div ref={anchor} role="group" aria-label="Date range"
      className={`relative inline-flex h-10 w-full max-w-[19rem] items-center rounded-[10px] border bg-ground pl-3 pr-10 transition-colors focus-within:border-accent focus-within:bg-surface hover:border-strong
        ${badS || badE || reversed ? 'border-danger' : 'border-default'} ${className}`}>
      <input type="text" inputMode="numeric" autoComplete="off" aria-label={startLabel} aria-invalid={badS || reversed || undefined} placeholder="YYYY-MM-DD" disabled={disabled}
        value={ts.text} onChange={e => onChange(ts.type(e.target.value), end)} onBlur={ts.settle}
        onKeyDown={e => { if (e.altKey && e.key === 'ArrowDown') { e.preventDefault(); setOpen(true) } }}
        className={`${field} focus:outline-none`} />
      <span aria-hidden="true" className="px-1.5 text-[12px] text-faint">to</span>
      <input type="text" inputMode="numeric" autoComplete="off" aria-label={endLabel} aria-invalid={badE || reversed || undefined} placeholder="YYYY-MM-DD" disabled={disabled}
        value={te.text} onChange={e => onChange(start, te.type(e.target.value))} onBlur={te.settle}
        onKeyDown={e => { if (e.altKey && e.key === 'ArrowDown') { e.preventDefault(); setOpen(true) } }}
        className={`${field} focus:outline-none`} />
      <CalendarButton label={buttonLabel} open={open} onClick={() => setOpen(o => !o)} buttonRef={button} disabled={disabled} />
      <Popover open={open} onClose={() => setOpen(false)} anchorRef={anchor} label={buttonLabel}>
        <div className="p-3" onKeyDown={e => tabOut(e, () => setOpen(false), button)}>
          <Calendar autoFocus mode="range" start={start} end={end} min={min} max={max} tz={tz} marks={marks}
            onRangeChange={(a, b) => onChange(ts.type(a), te.type(b))}
            onCommit={() => { setOpen(false); anchor.current?.querySelector('input')?.focus() }} />
        </div>
      </Popover>
    </div>
  )
}

// ── Time of day ─────────────────────────────────────────────────────────────

export function TimeInput({
  value, onChange, step = 15, buttonLabel = 'Choose time', className = '', ...rest
}: {
  /** 'HH:mm', or '' for none. */
  value: string
  onChange: (hhmm: string) => void
  /** Minutes between the options in the list; any valid time can still be typed. */
  step?: number
} & FieldChrome) {
  const [open, setOpen] = useState(false)
  const anchor = useRef<HTMLDivElement>(null)
  const button = useRef<HTMLButtonElement>(null)
  const list = useRef<HTMLDivElement>(null)
  const fmt = (v: string) => v
  const parse = (t: string) => { const m = parseTimeText(t); return m == null ? null : minutesToText(m) }
  const typed = useTypedValue<string>(value, fmt, parse, '')
  const bad = typed.text.trim() !== '' && parseTimeText(typed.text) == null
  const current = parseTimeText(value)
  const options = Array.from({ length: Math.ceil(1440 / step) }, (_, i) => i * step)

  // On open, move into the list at the current time (or the nearest option).
  useEffect(() => {
    if (!open) return
    const at = current == null ? 0 : Math.round(current / step)
    const el = list.current?.querySelectorAll<HTMLElement>('[role="option"]')[at]
    el?.focus()
    el?.scrollIntoView?.({ block: 'center' })
  }, [open]) // eslint-disable-line react-hooks/exhaustive-deps

  function nudge(delta: number) {
    const base = current ?? 0
    const next = ((Math.round(base / step) * step + delta + 1440) % 1440)
    onChange(typed.type(minutesToText(next)))
  }

  function onListKey(e: KeyboardEvent<HTMLDivElement>) {
    const items = Array.from(list.current?.querySelectorAll<HTMLElement>('[role="option"]') ?? [])
    const at = items.indexOf(document.activeElement as HTMLElement)
    let next = -1
    if (e.key === 'ArrowDown') next = Math.min(items.length - 1, at + 1)
    else if (e.key === 'ArrowUp') next = Math.max(0, at - 1)
    else if (e.key === 'Home') next = 0
    else if (e.key === 'End') next = items.length - 1
    else if (e.key === 'PageDown') next = Math.min(items.length - 1, at + 8)
    else if (e.key === 'PageUp') next = Math.max(0, at - 8)
    if (next >= 0) { e.preventDefault(); items[next]?.focus() }
    else if (e.key === 'Tab') { setOpen(false); button.current?.focus() }
  }

  return (
    <div ref={anchor} className={`relative ${className}`}>
      <Input
        {...rest}
        type="text"
        inputMode="numeric"
        autoComplete="off"
        placeholder={rest.placeholder ?? 'HH:mm'}
        value={typed.text}
        aria-invalid={rest['aria-invalid'] || bad || undefined}
        onChange={e => onChange(typed.type(e.target.value))}
        onBlur={typed.settle}
        onKeyDown={e => {
          if (e.key === 'ArrowUp') { e.preventDefault(); nudge(step) }
          else if (e.key === 'ArrowDown') { e.preventDefault(); if (e.altKey) setOpen(true); else nudge(-step) }
        }}
        className="w-full pr-10"
      />
      <CalendarButton label={buttonLabel} open={open} onClick={() => setOpen(o => !o)} buttonRef={button} disabled={rest.disabled} icon={<Icon.Clock className="h-4 w-4" />} />
      <Popover open={open} onClose={() => setOpen(false)} anchorRef={anchor} label={buttonLabel} role="dialog" className="max-h-64 w-36">
        <div ref={list} role="listbox" aria-label={buttonLabel} onKeyDown={onListKey} className="flex flex-col p-1.5">
          {options.map(m => {
            const on = m === current
            return (
              <button key={m} type="button" role="option" aria-selected={on} tabIndex={-1}
                onClick={() => { onChange(typed.type(minutesToText(m))); setOpen(false); anchor.current?.querySelector('input')?.focus() }}
                className={`h-8 shrink-0 rounded-md px-2 text-center text-[13px] tnum transition-colors focus:bg-surface-hover ${on ? 'bg-accent-subtle font-semibold text-accent' : 'text-body-2 hover:bg-surface-hover'}`}>
                {minutesToText(m)}
              </button>
            )
          })}
        </div>
      </Popover>
    </div>
  )
}
