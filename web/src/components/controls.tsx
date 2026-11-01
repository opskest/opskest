// Small shared controls: Kbd, Switch, SegmentedControl, FilterChips and Collapsible.

import { useId, useRef, useState, type InputHTMLAttributes, type KeyboardEvent, type ReactNode } from 'react'
import { Icon } from './icons'

// ── Kbd ─────────────────────────────────────────────────────────────────────

/** True on Apple platforms, where the shortcut modifier is Command. */
export function isApple(): boolean {
  return typeof navigator !== 'undefined' && /Mac|iPhone|iPad|iPod/i.test(navigator.platform || navigator.userAgent)
}

/** The label of the shortcut modifier key for this platform. */
export const modKey = (): string => (isApple() ? '⌘' : 'Ctrl')

/** A keyboard hint. Pass keys as children, or a list to get one cap per key. */
export function Kbd({ children, keys, className = '' }: { children?: ReactNode; keys?: string[]; className?: string }) {
  const cap = 'inline-flex h-[18px] min-w-[18px] items-center justify-center rounded-md border border-default bg-surface-2 px-1 font-mono text-[10px] font-medium leading-none text-muted'
  if (keys) {
    return (
      <span className={`inline-flex items-center gap-1 ${className}`}>
        {keys.map((k, i) => <kbd key={i} className={cap}>{k}</kbd>)}
      </span>
    )
  }
  return <kbd className={`${cap} ${className}`}>{children}</kbd>
}

// ── Switch ──────────────────────────────────────────────────────────────────

/**
 * An on/off setting that takes effect as a state, not a choice to submit.
 * A drop-in for Checkbox (same props); the native input keeps keyboard,
 * label-click and form behaviour, and announces as a switch.
 */
export function Switch({
  label, hint, className = '', ...rest
}: { label: ReactNode; hint?: ReactNode } & Omit<InputHTMLAttributes<HTMLInputElement>, 'type' | 'role'>) {
  const id = useId()
  const hintId = useId()
  return (
    <div className={`flex flex-col gap-0.5 ${className}`}>
      <label htmlFor={id} className={`group inline-flex items-start gap-3 text-[13px] text-body-2 select-none ${rest.disabled ? 'opacity-50 cursor-not-allowed' : 'cursor-pointer'}`}>
        <input
          id={id}
          type="checkbox"
          role="switch"
          aria-describedby={hint ? hintId : undefined}
          {...rest}
          className="peer sr-only"
        />
        <span
          aria-hidden="true"
          className="relative mt-px inline-block h-5 w-9 shrink-0 rounded-full border border-strong bg-surface-2 transition-colors duration-150
            peer-checked:border-transparent peer-checked:bg-accent
            peer-focus-visible:outline peer-focus-visible:outline-2 peer-focus-visible:outline-offset-2 peer-focus-visible:outline-accent
            before:absolute before:left-0.5 before:top-0.5 before:h-3.5 before:w-3.5 before:rounded-full before:bg-muted before:transition-transform before:duration-150
            peer-checked:before:translate-x-4 peer-checked:before:bg-accent-fg"
        />
        <span className="min-w-0 leading-5">{label}</span>
      </label>
      {hint && <p id={hintId} className="pl-12 text-[12px] leading-relaxed text-muted">{hint}</p>}
    </div>
  )
}

// ── SegmentedControl ────────────────────────────────────────────────────────

/** A single choice among a few short options, shown as one rail. Arrow keys move and choose. */
export function SegmentedControl<K extends string>({
  value, options, onChange, label, size = 'md', className = '',
}: {
  value: K
  options: { key: K; label: ReactNode; ariaLabel?: string }[]
  onChange: (key: K) => void
  label: string
  size?: 'sm' | 'md'
  className?: string
}) {
  const ref = useRef<HTMLDivElement>(null)
  const current = Math.max(0, options.findIndex(o => o.key === value))

  function onKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    let next = -1
    if (e.key === 'ArrowRight' || e.key === 'ArrowDown') next = (current + 1) % options.length
    else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') next = (current - 1 + options.length) % options.length
    else if (e.key === 'Home') next = 0
    else if (e.key === 'End') next = options.length - 1
    if (next < 0) return
    e.preventDefault()
    onChange(options[next].key)
    ref.current?.querySelectorAll<HTMLButtonElement>('[role="radio"]')[next]?.focus()
  }

  return (
    <div ref={ref} role="radiogroup" aria-label={label} onKeyDown={onKeyDown}
      className={`inline-flex max-w-full gap-1 rounded-xl bg-surface-2 p-1 ${className}`}>
      {options.map((o, i) => {
        const on = o.key === value
        return (
          <button
            key={o.key}
            type="button"
            role="radio"
            aria-checked={on}
            aria-label={o.ariaLabel}
            tabIndex={i === current ? 0 : -1}
            onClick={() => onChange(o.key)}
            className={`${size === 'sm' ? 'h-7 px-2.5 text-[12px]' : 'h-8 px-3 text-[13px]'} inline-flex items-center gap-1.5 whitespace-nowrap rounded-lg font-medium transition-colors
              ${on ? 'bg-surface text-body' : 'text-muted hover:text-body hover:bg-surface-hover'}`}
          >
            {o.label}
          </button>
        )
      })}
    </div>
  )
}

// ── FilterChips ─────────────────────────────────────────────────────────────

/**
 * The one way to filter a list by a single facet. `chips` (default) is a wrapping row of pills; `rail`
 * is the same choices stacked in a side column (collapsing to a scrolling row on phones). Both are a
 * labelled group of toggle buttons (aria-pressed) with an optional count.
 */
export function FilterChips<K extends string>({
  value, options, onChange, label, variant = 'chips', className = '',
}: {
  value: K
  options: { key: K; label: ReactNode; count?: number }[]
  onChange: (key: K) => void
  label: string
  variant?: 'chips' | 'rail'
  className?: string
}) {
  const rail = variant === 'rail'
  return (
    <div role="group" aria-label={label}
      className={`${rail ? 'flex gap-1 overflow-x-auto pb-1 lg:flex-col lg:overflow-x-visible lg:pb-0' : 'flex min-w-0 max-w-full flex-wrap gap-2'} ${className}`}>
      {options.map(o => {
        const on = o.key === value
        return (
          <button key={o.key} type="button" aria-pressed={on} onClick={() => onChange(o.key)}
            className={rail
              ? `flex shrink-0 items-center justify-between gap-5 rounded-[10px] px-3 h-10 text-left text-sm transition-colors ${on ? 'bg-surface-2 font-semibold text-body' : 'text-muted hover:bg-surface hover:text-body'}`
              : `inline-flex h-8 shrink-0 items-center gap-2 whitespace-nowrap rounded-full border px-3 text-[13px] transition-colors ${on ? 'border-accent bg-accent-subtle text-body' : 'border-default text-muted hover:border-strong hover:text-body'}`}>
            <span className={rail ? 'truncate' : ''}>{o.label}</span>
            {o.count != null && <span className="tnum text-[12px] text-muted">{o.count}</span>}
          </button>
        )
      })}
    </div>
  )
}

// ── Collapsible ─────────────────────────────────────────────────────────────

/**
 * A heading that shows or hides its content with an animated height. The
 * content stays mounted (so form state survives) but is inert and invisible
 * while closed. Controlled when `open` is given, otherwise self-managed.
 */
export function Collapsible({
  title, summary, open: controlled, defaultOpen = false, onOpenChange, children, className = '',
}: {
  title: ReactNode
  summary?: ReactNode
  open?: boolean
  defaultOpen?: boolean
  onOpenChange?: (open: boolean) => void
  children: ReactNode
  className?: string
}) {
  const [own, setOwn] = useState(defaultOpen)
  const open = controlled ?? own
  const id = useId()
  const toggle = () => { const next = !open; setOwn(next); onOpenChange?.(next) }
  const inert = open ? {} : { inert: '' } as Record<string, string>
  return (
    <div className={`rounded-[14px] border border-default bg-surface ${className}`}>
      <button
        type="button"
        aria-expanded={open}
        aria-controls={id}
        onClick={toggle}
        className="flex w-full items-center gap-2.5 rounded-[14px] px-4 py-3 text-left transition-colors hover:bg-surface-hover/60"
      >
        <Icon.Chevron className={`h-3.5 w-3.5 shrink-0 text-muted transition-transform duration-200 ${open ? 'rotate-90' : ''}`} />
        <span className="text-[13px] font-semibold text-body">{title}</span>
        {summary && !open && <span className="min-w-0 truncate text-[12px] text-muted">{summary}</span>}
      </button>
      <div id={id} aria-hidden={!open} {...inert}
        className={`grid transition-[grid-template-rows,visibility] duration-200 ease-out ${open ? 'grid-rows-[1fr] visible' : 'grid-rows-[0fr] invisible'}`}>
        <div className="min-h-0 overflow-hidden">
          <div className="px-4 pb-4 pt-1">{children}</div>
        </div>
      </div>
    </div>
  )
}
