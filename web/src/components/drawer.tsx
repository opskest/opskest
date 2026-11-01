// Drawer — a side panel for a quick look that keeps the page behind it in
// view. It follows Modal's conventions: a body portal that carries the page
// tokens, a scrim, a focus trap, Escape to close, focus restored to the
// opener, background scroll locked and aria-labelledby its title. On phones it
// takes the full width.

import { useEffect, useId, useLayoutEffect, useRef, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { themeSource } from '../lib/themeSource'
import { Icon } from './icons'

const FOCUSABLE = 'input:not([disabled]), button:not([disabled]), select:not([disabled]), textarea:not([disabled]), [href], [tabindex]:not([tabindex="-1"])'

export function Drawer({
  open, onClose, title, description, children, footer, side = 'right',
}: {
  open: boolean
  onClose: () => void
  title: string
  description?: ReactNode
  children?: ReactNode
  footer?: ReactNode
  side?: 'right' | 'left'
}) {
  const panel = useRef<HTMLDivElement>(null)
  const context = useRef<HTMLSpanElement>(null)
  const overlay = useRef<HTMLDivElement>(null)
  const titleId = useId()
  const descId = useId()
  const onCloseRef = useRef(onClose)
  useEffect(() => { onCloseRef.current = onClose }, [onClose])

  // Page brand tokens live on a wrapper rather than :root; carry them into the portal.
  useLayoutEffect(() => {
    if (!open || !context.current || !overlay.current) return
    const inherited = getComputedStyle(themeSource(context.current))
    for (const property of Array.from(inherited)) {
      if (property.startsWith('--')) overlay.current.style.setProperty(property, inherited.getPropertyValue(property))
    }
  })

  // Slide in from the edge; skipped for reduced motion.
  useEffect(() => {
    const el = panel.current
    if (!open || !el || window.matchMedia('(prefers-reduced-motion: reduce)').matches) return
    const from = side === 'right' ? 28 : -28
    const animation = el.animate(
      [{ transform: `translateX(${from}px)`, opacity: 0 }, { transform: 'translateX(0)', opacity: 1 }],
      { duration: 220, easing: 'cubic-bezier(0.16, 1, 0.3, 1)' },
    )
    return () => animation.cancel()
  }, [open, side])

  // The opener is read during render for the same reason Modal does: autofocus runs before effects.
  const opener = useRef<HTMLElement | null>(null)
  if (open && opener.current === null) opener.current = document.activeElement as HTMLElement | null

  useEffect(() => {
    if (!open) return
    const items = () => Array.from(panel.current?.querySelectorAll<HTMLElement>(FOCUSABLE) ?? [])
    // Land on the close button's neighbour: the first real control, else the panel itself.
    ;(items().find(el => el.dataset.drawerClose == null) ?? items()[0] ?? panel.current)?.focus()

    function onKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') { e.preventDefault(); onCloseRef.current(); return }
      if (e.key !== 'Tab') return
      const list = items()
      if (list.length === 0) { e.preventDefault(); return }
      const first = list[0]
      const last = list[list.length - 1]
      if (e.shiftKey && (document.activeElement === first || !panel.current?.contains(document.activeElement))) { e.preventDefault(); last.focus() }
      else if (!e.shiftKey && (document.activeElement === last || !panel.current?.contains(document.activeElement))) { e.preventDefault(); first.focus() }
    }
    document.addEventListener('keydown', onKeyDown)
    const prevOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.removeEventListener('keydown', onKeyDown)
      document.body.style.overflow = prevOverflow
      const el = opener.current
      opener.current = null
      if (el && el.isConnected && el !== document.body) el.focus()
    }
  }, [open])

  if (!open) return null
  return <><span hidden ref={context} />{createPortal(
    <div ref={overlay} className={`fixed inset-0 z-50 flex ${side === 'right' ? 'justify-end' : 'justify-start'}`}>
      <div className="absolute inset-0 bg-black/60 animate-fade-in" onClick={onClose} aria-hidden="true" />
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={description ? descId : undefined}
        tabIndex={-1}
        className={`relative flex h-full w-full max-w-full sm:max-w-md flex-col bg-surface shadow-2xl
          ${side === 'right' ? 'sm:border-l' : 'sm:border-r'} border-default`}
      >
        <div className="flex items-start gap-3 border-b border-default px-5 py-4">
          <div className="min-w-0 flex-1">
            <h2 id={titleId} className="text-base font-semibold text-body [overflow-wrap:anywhere]">{title}</h2>
            {description && <div id={descId} className="mt-1 text-[13px] text-muted leading-relaxed">{description}</div>}
          </div>
          <button type="button" data-drawer-close onClick={onClose} aria-label="Close"
            className="-mr-1.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-muted hover:bg-surface-hover hover:text-body transition-colors">
            <Icon.X className="h-4 w-4" />
          </button>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto scroll-slim px-5 py-4">{children}</div>
        {footer && <div className="flex flex-wrap justify-end gap-2 border-t border-default px-5 py-3.5 pb-[max(0.875rem,env(safe-area-inset-bottom))]">{footer}</div>}
      </div>
    </div>, document.body,
  )}</>
}
