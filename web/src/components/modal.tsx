// Modal dialog with a focus trap, Escape to close, focus restore on close,
// and aria-labelledby its heading. ConfirmModal is the destructive-action
// shape used everywhere ("Delete X?" + Cancel / Delete).

import { useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Button } from './button'
import { Icon } from './icons'
import { themeSource } from '../lib/themeSource'
import { useSpringEntrance } from '../hooks/useSpringEntrance'

export function Modal({
  open, onClose, title, description, children, footer, footerStart, wide, size,
}: {
  open: boolean
  onClose: () => void
  title: string
  description?: ReactNode
  children?: ReactNode
  footer?: ReactNode
  /** Left side of the footer, beside the buttons (a switch, a hint). */
  footerStart?: ReactNode
  /** Wider panel for dialogs holding a list or table rather than a form. */
  wide?: boolean
  /** Panel width for a two-column form: `lg` is 48rem, `xl` is 56rem. */
  size?: 'lg' | 'xl'
}) {
  const ref = useRef<HTMLDivElement>(null)
  const contextRef = useRef<HTMLSpanElement>(null)
  const overlayRef = useRef<HTMLDivElement>(null)
  // Escape parent layout and stacking contexts while retaining page branding.
  useLayoutEffect(() => {
    if (!open || !contextRef.current || !overlayRef.current) return
    const inherited = getComputedStyle(themeSource(contextRef.current))
    for (const property of Array.from(inherited)) {
      if (property.startsWith('--')) overlayRef.current.style.setProperty(property, inherited.getPropertyValue(property))
    }
  })
  useSpringEntrance(ref, open)
  const titleId = useId()
  const descId = useId()
  // Callers pass an inline onClose; keep the latest without re-running the
  // effect (which would steal focus on every keystroke).
  const onCloseRef = useRef(onClose)
  useEffect(() => { onCloseRef.current = onClose }, [onClose])

  // The element that had focus when the dialog opened, read during render:
  // a child with autoFocus is focused by React while it mounts, which is
  // before any effect runs, so an effect would record the dialog's own field.
  const openerRef = useRef<HTMLElement | null>(null)
  if (open && openerRef.current === null) openerRef.current = document.activeElement as HTMLElement | null

  useEffect(() => {
    if (!open) return
    const focusables = () =>
      Array.from(ref.current?.querySelectorAll<HTMLElement>(
        'input:not([disabled]), button:not([disabled]), select:not([disabled]), textarea:not([disabled]), [href], [tabindex]:not([tabindex="-1"])',
      ) ?? [])
    ;(focusables().find(el => el.tagName === 'INPUT' || el.tagName === 'TEXTAREA') ?? focusables()[0])?.focus()

    function onKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') { e.preventDefault(); onCloseRef.current(); return }
      if (e.key !== 'Tab') return
      const items = focusables()
      if (items.length === 0) return
      const first = items[0]
      const last = items[items.length - 1]
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus() }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
    }
    document.addEventListener('keydown', onKeyDown)
    const prevOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.removeEventListener('keydown', onKeyDown)
      document.body.style.overflow = prevOverflow
      const opener = openerRef.current
      openerRef.current = null
      if (opener && opener.isConnected && opener !== document.body) opener.focus()
    }
  }, [open])

  if (!open) return null

  return <><span hidden ref={contextRef} />{createPortal(
    <div ref={overlayRef} className="fixed inset-0 z-50 flex items-end sm:items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/60 animate-fade-in" onClick={onClose} aria-hidden="true" />
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={description ? descId : undefined}
        data-modal
        className={`relative flex w-full min-w-0 flex-col overflow-hidden ${size === 'xl' ? 'max-w-4xl' : size === 'lg' ? 'max-w-3xl' : wide ? 'max-w-2xl' : 'max-w-lg'}
          max-h-[calc(100dvh-2rem)] bg-surface border border-default rounded-[20px] shadow-2xl`}
      >
        <header className={`flex shrink-0 items-start gap-3 px-6 pb-4 pt-5 sm:px-7 ${children ? 'border-b border-default' : 'pb-3'}`}>
          <div className="flex min-w-0 flex-1 flex-col gap-1.5">
            <h2 id={titleId} className="text-base font-semibold text-body text-balance">{title}</h2>
            {description && <div id={descId} className="text-[13px] text-muted leading-relaxed">{description}</div>}
          </div>
          <button type="button" onClick={onClose} aria-label="Close"
            className="-mr-2 -mt-1 flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-muted transition-colors hover:bg-surface-hover hover:text-body">
            <Icon.X className="w-4 h-4" />
          </button>
        </header>
        {children && <div data-modal-body className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden scroll-slim px-6 py-5 sm:px-7">{children}</div>}
        {(footer || footerStart) && (
          <footer data-modal-footer className={`flex shrink-0 flex-wrap items-center gap-x-4 gap-y-3 px-6 py-4 sm:px-7 ${children ? 'border-t border-default' : ''}`}>
            {footerStart && <div className="min-w-0 sm:mr-auto">{footerStart}</div>}
            <div className={`flex flex-wrap justify-end gap-2 ${footerStart ? '' : 'ml-auto'}`}>{footer}</div>
          </footer>
        )}
      </div>
    </div>, document.body,
  )}</>
}

/** "Delete X?" — Cancel / confirm, busy while `onConfirm` runs. */
export function ConfirmModal({
  open, title, description, confirmLabel = 'Delete', tone = 'danger', onConfirm, onClose,
}: {
  open: boolean
  title: string
  description?: ReactNode
  confirmLabel?: string
  tone?: 'danger' | 'primary'
  onConfirm: () => Promise<unknown> | void
  onClose: () => void
}) {
  const [busy, setBusy] = useState(false)
  useEffect(() => { if (!open) setBusy(false) }, [open])

  async function run() {
    setBusy(true)
    try {
      await onConfirm()
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onClose={() => { if (!busy) onClose() }}
      title={title}
      description={description}
      footer={(
        <>
          <Button variant="secondary" onClick={onClose} disabled={busy}>Cancel</Button>
          <Button variant={tone} onClick={run} busy={busy}>{confirmLabel}</Button>
        </>
      )}
    />
  )
}
