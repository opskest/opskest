// Shared workbench controls use the theme tokens in index.css.
// Modal and Toast live in their own files and are re-exported here.

import { SkeletonBlock } from './skeleton'
import {
  Children, cloneElement, isValidElement, useId, useRef, type InputHTMLAttributes, type KeyboardEvent, type ReactNode,
  type SelectHTMLAttributes, type TextareaHTMLAttributes,
} from 'react'

export { Button, Spinner, buttonClass } from './button'
export { Icon } from './icons'
export { ConfirmModal, Modal } from './modal'
export { ToastProvider, useToast } from './toast'
export { Drawer } from './drawer'
export { DropdownMenu, type MenuEntry } from './menu'
export { FloatingLayer, HoverCard, Popover, Tooltip } from './overlay'
export { Skeleton, SkeletonBlock, SkeletonCards, SkeletonRows, SkeletonTable } from './skeleton'
export { Collapsible, FilterChips, isApple, Kbd, modKey, SegmentedControl, Switch } from './controls'
export { Timestamp, useRelative } from './Timestamp'
// Calendar and the date/time inputs import Input from this file, so they are
// imported from ./Calendar and ./DateInputs directly.

/** A section that is loading its own data: the skeleton of a heading and a few lines (never a bare spinner). */
export function LoadingBlock({ label = 'Loading', className = '' }: { label?: string; className?: string }) {
  return <SkeletonBlock lines={3} label={label} className={className} />
}

export function FullScreenSpinner() {
  return (
    <div className="min-h-screen bg-ground flex items-center justify-center">
      <div className="w-5 h-5 border-2 border-default border-t-accent rounded-full animate-spin" role="status" aria-label="Loading" />
    </div>
  )
}

// ── Form fields ─────────────────────────────────────────────────────────────

export function Field({
  label, hint, error, children, className = '',
}: {
  label: string; hint?: ReactNode; error?: string; className?: string
  children: ReactNode | ((id: string) => ReactNode)
}) {
  const id = useId()
  let controlId = id
  const content = typeof children === 'function' ? children(id) : Children.map(children, child => {
    if (!isValidElement<{ id?: string }>(child) || !['input', 'select', 'textarea', Input, Select, Textarea].includes(child.type as typeof Input)) return child
    controlId = child.props.id || id
    return cloneElement(child, { id: controlId })
  })
  return (
    <div className={`flex flex-col gap-1 min-w-0 ${className}`}>
      <label htmlFor={controlId} className="text-[12px] font-medium text-body-2">{label}</label>
      {content}
      {error
        ? <p className="text-[11px] text-danger">{error}</p>
        : hint && <p className="text-[12px] text-muted leading-relaxed">{hint}</p>}
    </div>
  )
}

// `dense` is the one small control size (36px tall, 12px text — the height of a small Button), for filters beside a table.
const CONTROL_BASE = `bg-ground border border-default text-body rounded-[10px]
  px-3 placeholder:text-faint min-w-0
  hover:border-strong focus:outline-none focus:ring-0 focus:border-accent focus:bg-surface transition-colors disabled:opacity-50
  aria-[invalid=true]:border-danger aria-[invalid=true]:focus:ring-danger/20`
const SIZE_CLS = { md: 'h-10 text-[14px]', dense: 'h-9 text-[12px]' }

export function Select({ className = '', dense, ...rest }: SelectHTMLAttributes<HTMLSelectElement> & { dense?: boolean }) {
  return <select {...rest} className={`${CONTROL_BASE} ${SIZE_CLS[dense ? 'dense' : 'md']} pr-8 cursor-pointer ${className}`} />
}

export function Input({ className = '', dense, ...rest }: InputHTMLAttributes<HTMLInputElement> & { dense?: boolean }) {
  return <input {...rest} className={`${CONTROL_BASE} ${SIZE_CLS[dense ? 'dense' : 'md']} ${className}`} />
}

export function Textarea({ className = '', rows = 4, ...rest }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea rows={rows} {...rest} className={`${CONTROL_BASE} ${SIZE_CLS.md} h-auto py-2 leading-relaxed resize-y ${className}`} />
}

export function Checkbox({ label, className = '', ...rest }: { label: ReactNode } & InputHTMLAttributes<HTMLInputElement>) {
  const id = useId()
  return (
    <label htmlFor={id} className={`inline-flex items-center gap-2 text-[13px] text-body-2 cursor-pointer select-none ${className}`}>
      <input id={id} type="checkbox" {...rest} className="w-3.5 h-3.5 rounded-sm border border-strong accent-accent cursor-pointer shrink-0" />
      {label}
    </label>
  )
}

// ── Tabs ────────────────────────────────────────────────────────────────────

/** A tablist; ← → Home End move between tabs (and select them). */
export function Tabs<K extends string>({
  tabs, active, onChange, label, size = 'md',
}: {
  tabs: { key: K; label: ReactNode }[]; active: K; onChange: (key: K) => void; label?: string; size?: 'sm' | 'md'
}) {
  const ref = useRef<HTMLDivElement>(null)

  function onKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    const i = tabs.findIndex(t => t.key === active)
    let next = -1
    if (e.key === 'ArrowRight') next = (i + 1) % tabs.length
    else if (e.key === 'ArrowLeft') next = (i - 1 + tabs.length) % tabs.length
    else if (e.key === 'Home') next = 0
    else if (e.key === 'End') next = tabs.length - 1
    if (next < 0) return
    e.preventDefault()
    onChange(tabs[next].key)
    ref.current?.querySelectorAll<HTMLButtonElement>('[role="tab"]')[next]?.focus()
  }

  return (
    <div
      ref={ref}
      role="tablist"
      aria-label={label}
      onKeyDown={onKeyDown}
      className="inline-flex max-w-full items-center gap-1 rounded-xl bg-surface-2 p-1 flex-wrap"
    >
      {tabs.map(tab => {
        const selected = active === tab.key
        return (
          <button
            key={tab.key}
            type="button"
            role="tab"
            aria-selected={selected}
            tabIndex={selected ? 0 : -1}
            onClick={() => onChange(tab.key)}
            className={`px-3 ${size === 'sm' ? 'h-7 text-[12px]' : 'h-8 text-[13px]'} shrink-0 font-medium rounded-lg transition-colors whitespace-nowrap
              ${selected ? 'bg-surface text-body' : 'text-muted hover:text-body hover:bg-surface-hover'}`}
          >
            {tab.label}
          </button>
        )
      })}
    </div>
  )
}

// ── Badge ───────────────────────────────────────────────────────────────────

export type Tone = 'neutral' | 'ok' | 'warn' | 'danger' | 'accent'

const TONE: Record<Tone, string> = {
  neutral: 'bg-surface-2 text-muted border-default',
  ok: 'bg-ok-bg text-ok border-transparent',
  warn: 'bg-warn-bg text-warn border-transparent',
  danger: 'bg-danger-bg text-danger border-transparent',
  accent: 'bg-accent-subtle text-accent border-accent-border',
}

export function Badge({
  tone = 'neutral', dot, children, title, className = '',
}: { tone?: Tone; dot?: boolean; children: ReactNode; title?: string; className?: string }) {
  return (
    <span
      title={title}
      className={`inline-flex items-center gap-1.5 text-[11px] font-medium px-2 py-1 rounded-md border whitespace-nowrap ${TONE[tone]} ${className}`}
    >
      {dot && <span aria-hidden="true" className="w-1.5 h-1.5 rounded-full bg-current" />}
      {children}
    </span>
  )
}

/** The pulse that says the stream is running. */
export function LiveDot({ label }: { label?: string }) {
  return (
    <span className="inline-flex items-center gap-2">
      <span className="live-dot" aria-hidden="true" />
      {label && <span className="text-[12px] text-ok font-medium">{label}</span>}
    </span>
  )
}

// ── Surfaces ────────────────────────────────────────────────────────────────

export function Card({ children, className = '', as: As = 'div' }: { children: ReactNode; className?: string; as?: 'div' | 'section' }) {
  return <As className={`bg-surface border border-default rounded-[14px] ${className}`}>{children}</As>
}

/** A card with a heading row (title, optional hint and actions). */
export function Panel({
  title, hint, actions, children, className = '', bodyClassName = 'p-4',
}: { title: ReactNode; hint?: ReactNode; actions?: ReactNode; children: ReactNode; className?: string; bodyClassName?: string }) {
  return (
    <section className={`bg-surface border border-default rounded-[14px] min-w-0 ${className}`}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 pt-3.5">
        <h2 className="text-base font-semibold text-body">{title}</h2>
        {hint && <span className="text-[12px] text-muted">{hint}</span>}
        {actions && <div className="ml-auto flex items-center gap-2">{actions}</div>}
      </div>
      <div className={bodyClassName}>{children}</div>
    </section>
  )
}

// ── Feedback ────────────────────────────────────────────────────────────────

export function Alert({
  tone = 'danger', children, onDismiss,
}: { tone?: 'danger' | 'warn' | 'ok' | 'accent'; children: ReactNode; onDismiss?: () => void }) {
  const cls = {
    danger: 'bg-danger-bg text-danger',
    warn: 'bg-warn-bg text-warn',
    ok: 'bg-ok-bg text-ok',
    accent: 'bg-accent-subtle text-accent',
  }[tone]
  return (
    <div role={tone === 'danger' ? 'alert' : 'status'} className={`flex items-start gap-2.5 text-sm rounded-[10px] px-3.5 py-2.5 ${cls}`}>
      <svg aria-hidden="true" className="w-4 h-4 mt-0.5 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
        <circle cx="12" cy="12" r="10" /><line x1="12" y1="8" x2="12" y2="12" /><line x1="12" y1="16" x2="12.01" y2="16" />
      </svg>
      <div className="flex-1 leading-relaxed min-w-0 [overflow-wrap:anywhere]">{children}</div>
      {onDismiss && (
        <button type="button" onClick={onDismiss} aria-label="Dismiss" className="shrink-0 opacity-60 hover:opacity-100 leading-none text-base">
          &times;
        </button>
      )}
    </div>
  )
}

/** A failed section fetch, with a way back — never a dead end. */
export function ErrorRetry({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <Alert tone="danger">
      <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
        {message}
        <button type="button" onClick={onRetry} className="font-medium underline underline-offset-2 hover:no-underline">
          Retry
        </button>
      </span>
    </Alert>
  )
}

/**
 * The one empty state: a left-aligned dashed 14px container with a heading, a one-line explanation and
 * at most one action. `compact` is for a panel inside a dense layout (the Overview columns); `bare`
 * drops the dashed frame when it already sits inside a card or list container.
 */
export function EmptyState({
  icon, title, children, action, heading, compact, bare,
}: { icon?: ReactNode; title: string; children?: ReactNode; action?: ReactNode; heading?: boolean; compact?: boolean; bare?: boolean }) {
  return (
    <div data-empty-state className={`flex flex-col items-center justify-center text-center gap-4 ${bare ? '' : 'rounded-[14px] bg-surface border border-dashed border-default'} ${compact ? 'py-6 px-5' : 'min-h-[16rem] py-12 px-6 sm:px-9'}`}>
      {icon && !compact && (
        <div className="w-11 h-11 rounded-[10px] bg-surface-2 flex items-center justify-center text-muted" aria-hidden="true">
          {icon}
        </div>
      )}
      <div className="flex flex-col gap-1.5 max-w-md">
        {heading ? <h1 className="text-base font-semibold text-body">{title}</h1> : <p className="text-base font-semibold text-body">{title}</p>}
        {children && <p className="text-[13px] text-muted leading-relaxed">{children}</p>}
      </div>
      {action}
    </div>
  )
}
