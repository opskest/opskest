import { createContext, useContext, useState, type HTMLAttributes, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Skeleton } from '../skeleton'
import { LIST_SCROLL } from '../table'
import { Button, Icon, useToast } from '../ui'

// The one scaffold every Settings tab is drawn with, so switching tabs never
// moves the title, the first section or the action buttons:
//
//   SettingsPanel    the single bordered card: title row (title, description,
//                    actions slot) with a fixed height, then the sections.
//   SettingsSection  one block of the body: title, description, optional
//                    trailing actions, content, optional footer.
//   SettingsFooter   the save bar: a status slot that always reserves its
//                    line, then the buttons on the right.
//   SettingsStats    a row of equal figure tiles (label, value, one hint line).
//   SettingsCard     one equal-height action tile (title, note, button at the foot).
//   CopyField        a read-only mono value with a Copy button, 40px like an input.
//   SettingsLoading  skeleton sections with the proportions of real ones.
//   SettingsActions  puts buttons into the title row from inside a tab body
//                    (a portal into the slot, so the tab owns its own state).

const SlotContext = createContext<HTMLElement | null>(null)

export function SettingsPanel({ id, title, titleExtra, description, actions, children }: {
  id: string
  title: string
  /** Small status next to the title (a chip with a tooltip, for example). */
  titleExtra?: ReactNode
  description: string
  actions?: ReactNode
  children: ReactNode
}) {
  const [slot, setSlot] = useState<HTMLElement | null>(null)
  return (
    <section aria-labelledby={`settings-${id}-title`} data-settings-panel={id} className="w-full min-w-0 overflow-hidden rounded-[14px] border border-default bg-surface">
      <header className="flex flex-wrap items-start justify-between gap-x-4 gap-y-3 border-b border-default px-5 py-5 sm:min-h-[89px]">
        <div className="min-w-0 flex-1 basis-72">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <h2 id={`settings-${id}-title`} className="text-base font-semibold leading-6 text-body">{title}</h2>
            {titleExtra ? <span className="-my-1 inline-flex">{titleExtra}</span> : null}
          </div>
          <p className="mt-1 max-w-[80ch] text-[13px] leading-5 text-muted">{description}</p>
        </div>
        <div className="flex min-h-9 flex-wrap items-center gap-2">
          {actions}
          <span ref={setSlot} className="contents" />
        </div>
      </header>
      <SlotContext.Provider value={slot}>
        <div className="divide-y divide-default">{children}</div>
      </SlotContext.Provider>
    </section>
  )
}

export function SettingsActions({ children }: { children: ReactNode }) {
  const slot = useContext(SlotContext)
  return slot ? createPortal(children, slot) : null
}

export function SettingsSection({ title, description, actions, footer, flush = false, children, className = '', ...rest }: {
  title?: ReactNode
  description?: ReactNode
  actions?: ReactNode
  footer?: ReactNode
  /** No side padding on the content, for lists that run edge to edge. */
  flush?: boolean
  children?: ReactNode
} & Omit<HTMLAttributes<HTMLElement>, 'title'>) {
  return (
    <section {...rest} className={`${flush ? '' : 'px-5 py-5'} ${className}`}>
      {(title || actions) && (
        <div className={`flex flex-wrap items-start justify-between gap-x-4 gap-y-2 ${children ? 'mb-4' : ''} ${flush ? 'px-5 pt-5' : ''}`}>
          <div className="min-w-0 flex-1 basis-60">
            {title && <h3 className="text-sm font-semibold text-body">{title}</h3>}
            {description && <p className="mt-1 max-w-[80ch] text-xs leading-relaxed text-muted">{description}</p>}
          </div>
          {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
        </div>
      )}
      {children && <div className={flush ? '' : 'space-y-4'}>{children}</div>}
      {footer}
    </section>
  )
}

/** The bottom bar of a form: a status line that always keeps its height, and the buttons. */
export function SettingsFooter({ status, children }: { status?: ReactNode; children: ReactNode }) {
  return (
    <div className="mt-5 flex flex-wrap items-center justify-end gap-x-3 gap-y-2 border-t border-default pt-4">
      <span role="status" aria-live="polite" className="mr-auto flex min-h-5 items-center gap-1.5 text-xs text-muted">{status}</span>
      {children}
    </div>
  )
}

/** A row of equal figure tiles: 11px label, 18px value, an optional 12px hint. Four across by default. */
export function SettingsStats({ items, columns = 'lg:grid-cols-4' }: {
  items: { label: string; value: ReactNode; hint?: ReactNode; tone?: string; mono?: boolean }[]
  columns?: string
}) {
  return (
    <dl className={`grid grid-cols-2 gap-3 ${columns}`}>
      {items.map(it => (
        <div key={it.label} data-settings-stat className="flex min-w-0 flex-col rounded-[14px] border border-default bg-surface-2/40 px-4 py-3">
          <dt className="text-[11px] font-semibold uppercase tracking-wider text-faint">{it.label}</dt>
          <dd className={`mt-1 truncate text-lg font-semibold leading-7 ${it.mono === false ? '' : 'font-mono'} ${it.tone ?? 'text-body'}`}>{it.value}</dd>
          {it.hint != null && <dd className="mt-0.5 text-[12px] leading-snug text-muted [overflow-wrap:anywhere]">{it.hint}</dd>}
        </div>
      ))}
    </dl>
  )
}

/** An action tile for a row of equal-height cards: title, a note, and the action pinned to the bottom. */
export function SettingsCard({ title, children, action }: { title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <div data-settings-card className="flex min-w-0 flex-col gap-3 rounded-[14px] border border-default bg-surface-2/40 p-4">
      <div className="min-w-0 flex-1">
        <h4 className="text-sm font-semibold text-body">{title}</h4>
        {children && <p className="mt-1 text-[12px] leading-snug text-muted">{children}</p>}
      </div>
      {action}
    </div>
  )
}

/** A read-only mono value (a command, a path) with a Copy button; the row is as tall as an input. */
export function CopyField({ text, label }: { text: string; label: string }) {
  const { push } = useToast()
  async function copy() {
    try {
      await navigator.clipboard.writeText(text)
      push(`${label} copied.`, 'ok')
    } catch {
      push('Could not copy. Select the text and copy it manually.', 'danger')
    }
  }
  return (
    <div className="flex min-w-0 items-stretch gap-2">
      <pre tabIndex={0} aria-label={label} className="flex min-h-10 min-w-0 flex-1 items-center whitespace-pre-wrap break-all rounded-[10px] border border-default bg-surface-2 px-3 py-2 font-mono text-[12px] text-body-2">{text}</pre>
      <Button onClick={copy} icon={<Icon.Copy className="h-3.5 w-3.5" />} aria-label={`Copy ${label}`}>Copy</Button>
    </div>
  )
}

/** A table frame used inside a section. */
export function SettingsTable({ children, className = '', scroll = false }: { children: ReactNode; className?: string; scroll?: boolean }) {
  return <div className={`scroll-slim ${scroll ? `${LIST_SCROLL} !max-h-[min(50dvh,30rem)]` : 'overflow-x-auto'} min-w-0 max-w-full rounded-[14px] border border-default ${className}`}>{children}</div>
}

/** Skeleton sections: a title and description bar, a block of rows. */
export function SettingsLoading({ label = 'Loading', sections = 2, rows = 3 }: { label?: string; sections?: number; rows?: number }) {
  return (
    <div role="status" aria-busy="true" className="divide-y divide-default">
      <span className="sr-only">{label}</span>
      {Array.from({ length: sections }, (_, s) => (
        <div key={s} className="px-5 py-5">
          <div className="mb-4 space-y-2">
            <Skeleton className="h-4 w-40" />
            <Skeleton className="h-3 w-2/3 max-w-md" />
          </div>
          <div className="space-y-3">
            {Array.from({ length: rows }, (_, r) => <Skeleton key={r} className="h-10 w-full" />)}
          </div>
        </div>
      ))}
    </div>
  )
}

// ── Shared table and row-action styles ──────────────────────────────────────
// Every table inside a Settings section uses these, so headers, cells and the
// buttons of an action cell look the same in every tab.

/** `<table className={ST_TABLE}>` inside a SettingsTable frame. */
export const ST_TABLE = 'w-full text-left text-[13px]'
/** `<thead className={ST_HEAD}>`. */
export const ST_HEAD = 'border-b border-default bg-surface-2'
/** `<th className={ST_TH}>`; add `text-right` for numeric or action columns. */
export const ST_TH = 'sticky top-0 z-[1] bg-surface-2 px-3 py-2.5 text-[11px] font-semibold uppercase tracking-wide text-muted whitespace-nowrap'
/** `<tbody className={ST_BODY}>`. */
export const ST_BODY = 'divide-y divide-default'
/** `<tr className={ST_ROW}>`. */
export const ST_ROW = 'hover:bg-surface-hover/50'
/** `<td className={ST_TD}>`. */
export const ST_TD = 'px-3 py-3 align-middle'
/** The note a table shows instead of rows. */
export const ST_EMPTY = 'px-3 py-8 text-center text-muted'
/** Added to a ghost Button for a destructive row action (same size as its neighbours). */
export const DANGER_GHOST = 'text-danger hover:bg-danger/10 hover:text-danger'

/** The buttons of one table row: right-aligned, one line, equal gaps. Use size="sm" for every button inside. */
export function RowActions({ children }: { children: ReactNode }) {
  return <div data-row-actions className="flex flex-nowrap items-center justify-end gap-2">{children}</div>
}
