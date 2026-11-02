// Floating layers: the anchored-positioning primitive plus Tooltip, HoverCard
// and Popover built on it.
//
// A layer is portalled to <body> and placed with position: fixed from its
// anchor's bounding box, so no overflow-hidden card, table frame or dialog can
// clip it and it adds nothing to the flow (no layout shift). It flips to the
// other side when there is no room, clamps inside the viewport and follows the
// anchor on scroll and resize. Custom properties (page brand tokens) are
// copied from the anchor, as Modal does for dialogs.

import {
  cloneElement, useCallback, useEffect, useId, useLayoutEffect, useRef, useState,
  type CSSProperties, type FocusEvent, type MouseEvent, type ReactElement, type ReactNode, type RefObject,
} from 'react'
import { createPortal } from 'react-dom'
import { themeSource } from '../lib/themeSource'

export type Side = 'top' | 'bottom' | 'left' | 'right'
export type Align = 'start' | 'center' | 'end'

const MARGIN = 8

interface PositionOptions { side: Side; align: Align; offset: number; matchWidth?: boolean }

/** Computes and applies fixed coordinates for `layer` next to `anchor`. */
function place(anchor: HTMLElement, layer: HTMLElement, { side, align, offset, matchWidth }: PositionOptions) {
  const a = anchor.getBoundingClientRect()
  if (matchWidth) layer.style.minWidth = `${a.width}px`
  const vw = document.documentElement.clientWidth
  const vh = window.innerHeight
  // Measure at natural size: reset constraints from a previous placement first.
  layer.style.maxHeight = ''
  const w = layer.offsetWidth
  const h = layer.offsetHeight
  let actual = side
  if (side === 'bottom' && a.bottom + offset + h > vh - MARGIN && a.top - offset - h >= MARGIN) actual = 'top'
  else if (side === 'top' && a.top - offset - h < MARGIN && a.bottom + offset + h <= vh - MARGIN) actual = 'bottom'
  else if (side === 'right' && a.right + offset + w > vw - MARGIN && a.left - offset - w >= MARGIN) actual = 'left'
  else if (side === 'left' && a.left - offset - w < MARGIN && a.right + offset + w <= vw - MARGIN) actual = 'right'

  let top: number
  let left: number
  if (actual === 'top' || actual === 'bottom') {
    top = actual === 'bottom' ? a.bottom + offset : a.top - offset - h
    left = align === 'start' ? a.left : align === 'end' ? a.right - w : a.left + a.width / 2 - w / 2
  } else {
    left = actual === 'right' ? a.right + offset : a.left - offset - w
    top = align === 'start' ? a.top : align === 'end' ? a.bottom - h : a.top + a.height / 2 - h / 2
  }
  // When even the better side is too short, scroll inside the layer instead of running off screen.
  const room = actual === 'bottom' ? vh - MARGIN - top : actual === 'top' ? a.top - offset - MARGIN : vh - 2 * MARGIN
  if (h > room && (actual === 'top' || actual === 'bottom')) {
    layer.style.maxHeight = `${Math.max(120, room)}px`
    if (actual === 'top') top = a.top - offset - Math.min(h, Math.max(120, room))
  }
  left = Math.min(Math.max(MARGIN, left), Math.max(MARGIN, vw - w - MARGIN))
  top = Math.min(Math.max(MARGIN, top), Math.max(MARGIN, vh - Math.min(h, vh - 2 * MARGIN) - MARGIN))
  layer.style.left = `${Math.round(left)}px`
  layer.style.top = `${Math.round(top)}px`
  layer.dataset.side = actual
  layer.style.visibility = 'visible'
}

/**
 * A portalled, anchored layer. Mount it only while it should be visible.
 * `anchor` is the element it follows.
 */
export function FloatingLayer({
  anchor, side = 'bottom', align = 'start', offset = 6, matchWidth, className = '', style, children,
  layerRef, ...rest
}: {
  anchor: HTMLElement | null
  side?: Side
  align?: Align
  offset?: number
  matchWidth?: boolean
  className?: string
  style?: CSSProperties
  children: ReactNode
  layerRef?: RefObject<HTMLDivElement>
} & Omit<React.HTMLAttributes<HTMLDivElement>, 'className' | 'style' | 'children'>) {
  const own = useRef<HTMLDivElement>(null)
  const ref = layerRef ?? own

  const reposition = useCallback(() => {
    if (anchor && ref.current && anchor.isConnected) place(anchor, ref.current, { side, align, offset, matchWidth })
  }, [anchor, ref, side, align, offset, matchWidth])

  useLayoutEffect(() => {
    const el = ref.current
    if (!el || !anchor) return
    // Page brand tokens live on a wrapper, not :root; carry them over.
    const inherited = getComputedStyle(themeSource(anchor))
    for (const property of Array.from(inherited)) {
      if (property.startsWith('--')) el.style.setProperty(property, inherited.getPropertyValue(property))
    }
    reposition()
  }, [anchor, ref, reposition])

  useEffect(() => {
    if (!anchor) return
    const onMove = () => reposition()
    window.addEventListener('resize', onMove)
    window.addEventListener('scroll', onMove, true)
    const observer = typeof ResizeObserver === 'function' && ref.current ? new ResizeObserver(onMove) : null
    if (observer && ref.current) observer.observe(ref.current)
    return () => {
      window.removeEventListener('resize', onMove)
      window.removeEventListener('scroll', onMove, true)
      observer?.disconnect()
    }
  }, [anchor, ref, reposition])

  if (!anchor) return null
  return createPortal(
    <div
      {...rest}
      ref={ref}
      className={`fixed z-[70] m-0 animate-fade-in ${className}`}
      style={{ left: 0, top: 0, visibility: 'hidden', ...style }}
    >
      {children}
    </div>,
    document.body,
  )
}

// ── Tooltip ─────────────────────────────────────────────────────────────────

let lastTooltipClosedAt = 0
const WARM_MS = 600

type HandlerProps = {
  onMouseEnter?: (e: MouseEvent<HTMLElement>) => void
  onMouseLeave?: (e: MouseEvent<HTMLElement>) => void
  onFocus?: (e: FocusEvent<HTMLElement>) => void
  onBlur?: (e: FocusEvent<HTMLElement>) => void
  onPointerDown?: (e: React.PointerEvent<HTMLElement>) => void
  'aria-describedby'?: string
  'aria-label'?: string
}

/**
 * A short label on hover or keyboard focus. It opens after a delay (instantly
 * when another tooltip just closed), closes on Escape, blur, pointer leave and
 * press, stays open while the pointer is over it, and never moves the layout.
 * Wrap exactly one element that can take mouse and focus events.
 */
export function Tooltip({
  content, children, delay = 450, side = 'top', align = 'center', onlyWhenTruncated, disabled,
}: {
  content: ReactNode
  children: ReactElement<HandlerProps>
  delay?: number
  side?: Side
  align?: Align
  /** Show only when the wrapped element's text is cut off (ellipsis). */
  onlyWhenTruncated?: boolean
  disabled?: boolean
}) {
  const id = useId()
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const timer = useRef<number>()
  const open = anchor != null

  const clear = () => { if (timer.current != null) { window.clearTimeout(timer.current); timer.current = undefined } }
  const hide = useCallback(() => {
    clear()
    setAnchor(current => { if (current) lastTooltipClosedAt = Date.now(); return null })
  }, [])
  const hideSoon = () => { clear(); timer.current = window.setTimeout(hide, 80) }
  const show = (el: HTMLElement, wait: number) => {
    if (disabled || content == null || content === '') return
    clear()
    const go = () => {
      if (onlyWhenTruncated && el.scrollWidth <= el.clientWidth + 1) return
      setAnchor(el)
    }
    const warm = Date.now() - lastTooltipClosedAt < WARM_MS
    if (warm || wait <= 0) go()
    else timer.current = window.setTimeout(go, wait)
  }

  useEffect(() => () => clear(), [])
  useEffect(() => {
    if (!open) return
    // Capture phase on window: Escape closes the tooltip before a dialog sees it.
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') { e.stopPropagation(); hide() } }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [open, hide])

  const child = children
  const props = child.props
  const sameAsLabel = typeof content === 'string' && props['aria-label'] === content
  return (
    <>
      {cloneElement(child, {
        onMouseEnter: (e: MouseEvent<HTMLElement>) => { props.onMouseEnter?.(e); show(e.currentTarget, delay) },
        onMouseLeave: (e: MouseEvent<HTMLElement>) => { props.onMouseLeave?.(e); hideSoon() },
        onFocus: (e: FocusEvent<HTMLElement>) => {
          props.onFocus?.(e)
          // Keyboard focus only: a mouse click should not pop a tooltip over the pressed control.
          if (e.currentTarget.matches(':focus-visible')) show(e.currentTarget, Math.min(delay, 250))
        },
        onBlur: (e: FocusEvent<HTMLElement>) => { props.onBlur?.(e); hide() },
        onPointerDown: (e: React.PointerEvent<HTMLElement>) => { props.onPointerDown?.(e); hide() },
        'aria-describedby': open && !sameAsLabel ? id : props['aria-describedby'],
      })}
      {open && (
        <FloatingLayer
          anchor={anchor}
          side={side}
          align={align}
          offset={8}
          id={id}
          role="tooltip"
          onMouseEnter={clear}
          onMouseLeave={hideSoon}
          className="max-w-[min(20rem,calc(100vw-16px))] rounded-lg border border-default bg-surface-2 px-2.5 py-1.5 text-[12px] leading-snug text-body shadow-lg [overflow-wrap:anywhere]"
        >
          {content}
        </FloatingLayer>
      )}
    </>
  )
}

// ── HoverCard ───────────────────────────────────────────────────────────────

/**
 * A richer preview that opens on hover or keyboard focus of its trigger and
 * stays open while the pointer or focus is inside it. Use sparingly: for a
 * quick look that never replaces the link it hangs from.
 */
export function HoverCard({
  card, children, openDelay = 550, closeDelay = 180, side = 'bottom', align = 'start', label,
}: {
  card: ReactNode
  children: ReactElement<HandlerProps>
  openDelay?: number
  closeDelay?: number
  side?: Side
  align?: Align
  label: string
}) {
  const id = useId()
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const timer = useRef<number>()
  const open = anchor != null
  const clear = () => { if (timer.current != null) { window.clearTimeout(timer.current); timer.current = undefined } }
  const hide = useCallback(() => { clear(); setAnchor(null) }, [])
  const openSoon = (el: HTMLElement, wait: number) => { clear(); timer.current = window.setTimeout(() => setAnchor(el), wait) }
  const closeSoon = () => { clear(); timer.current = window.setTimeout(hide, closeDelay) }

  useEffect(() => () => clear(), [])
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') { e.stopPropagation(); hide() } }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [open, hide])

  const props = children.props
  return (
    <>
      {cloneElement(children, {
        onMouseEnter: (e: MouseEvent<HTMLElement>) => { props.onMouseEnter?.(e); openSoon(e.currentTarget, openDelay) },
        onMouseLeave: (e: MouseEvent<HTMLElement>) => { props.onMouseLeave?.(e); closeSoon() },
        onFocus: (e: FocusEvent<HTMLElement>) => {
          props.onFocus?.(e)
          if (e.currentTarget.matches(':focus-visible')) openSoon(e.currentTarget, 300)
        },
        onBlur: (e: FocusEvent<HTMLElement>) => { props.onBlur?.(e); closeSoon() },
        onPointerDown: (e: React.PointerEvent<HTMLElement>) => { props.onPointerDown?.(e); hide() },
        'aria-describedby': open ? id : props['aria-describedby'],
      })}
      {open && (
        <FloatingLayer
          anchor={anchor}
          side={side}
          align={align}
          offset={8}
          id={id}
          role="group"
          aria-label={label}
          onMouseEnter={clear}
          onMouseLeave={closeSoon}
          onFocus={clear}
          onBlur={closeSoon}
          className="w-[min(20rem,calc(100vw-16px))] rounded-xl border border-default bg-surface p-3.5 text-[13px] text-body-2 shadow-xl"
        >
          {card}
        </FloatingLayer>
      )}
    </>
  )
}

// ── Popover ─────────────────────────────────────────────────────────────────

/**
 * A click-opened, anchored panel (the calendar, a time list). Controlled:
 * the owner holds `open`. Escape and a press outside close it; Escape does not
 * reach an enclosing dialog, and focus goes back to the anchor.
 */
export function Popover({
  open, onClose, anchorRef, children, label, side = 'bottom', align = 'start', className = '', role = 'dialog',
}: {
  open: boolean
  onClose: () => void
  anchorRef: RefObject<HTMLElement>
  children: ReactNode
  label: string
  side?: Side
  align?: Align
  className?: string
  role?: 'dialog' | 'listbox'
}) {
  const layer = useRef<HTMLDivElement>(null)
  const closeRef = useRef(onClose)
  useEffect(() => { closeRef.current = onClose }, [onClose])
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  useLayoutEffect(() => { setAnchor(open ? anchorRef.current : null) }, [open, anchorRef])

  useEffect(() => {
    if (!open) return
    const onDown = (e: PointerEvent) => {
      const target = e.target as Node
      if (layer.current?.contains(target) || anchorRef.current?.contains(target)) return
      closeRef.current()
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      e.stopPropagation()
      e.preventDefault()
      closeRef.current()
      anchorRef.current?.querySelector<HTMLElement>('input, button')?.focus()
    }
    document.addEventListener('pointerdown', onDown, true)
    window.addEventListener('keydown', onKey, true)
    return () => {
      document.removeEventListener('pointerdown', onDown, true)
      window.removeEventListener('keydown', onKey, true)
    }
  }, [open, anchorRef])

  if (!open) return null
  return (
    <FloatingLayer
      anchor={anchor}
      layerRef={layer}
      side={side}
      align={align}
      role={role}
      aria-label={label}
      className={`max-w-[calc(100vw-16px)] overflow-y-auto scroll-slim rounded-xl border border-default bg-surface shadow-xl ${className}`}
    >
      {children}
    </FloatingLayer>
  )
}
