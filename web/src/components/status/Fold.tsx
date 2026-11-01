// Collapsible parts of a status page. A public page starts collapsed: groups,
// incident timelines, past days and maintenance details show a headline and
// open on request, or all at once when the owner chose "open everything" or
// the visitor presses Expand all.
//
// A closed body stays in the page with hidden="until-found", so the browser's
// find-in-page can still reach it (and opens it, through `beforematch`) where
// that is supported; elsewhere it is simply hidden. Nothing is stored: the
// state lives for one page load.

import { createContext, useCallback, useContext, useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { Icon } from '../ui'

export interface Expansion {
  /** What Expand all / Collapse all last asked for. */
  open: boolean
  /** Bumped by each press, so a part reacts even when `open` did not change. */
  revision: number
  /** The page owner's "open everything on load" choice. */
  initial: boolean
}

export const ExpandContext = createContext<Expansion>({ open: false, revision: 0, initial: false })
/** Lets a view with no state of its own (the history page) offer Expand all. */
export const ExpandSetContext = createContext<(open: boolean) => void>(() => {})

export function useExpansionState(initial: boolean): [Expansion, (open: boolean) => void] {
  const [s, setS] = useState<Expansion>({ open: initial, revision: 0, initial })
  const set = useCallback((open: boolean) => setS(c => ({ ...c, open, revision: c.revision + 1 })), [])
  return [s, set]
}

/** State, ids and the body ref for one collapsible part. `anchor` is the
 * element id a URL hash can point at: it opens the part and scrolls to it. */
export function useFold(anchor?: string) {
  const ctx = useContext(ExpandContext)
  const [open, setOpen] = useState(ctx.initial)
  const id = useId()
  const body = useRef<HTMLElement | null>(null)

  useEffect(() => { if (ctx.revision > 0) setOpen(ctx.open) }, [ctx.revision, ctx.open])

  // Layout effect: a closed body is hidden before the first paint, so a
  // collapsed page never flashes open.
  useLayoutEffect(() => {
    const el = body.current
    if (!el) return
    if (open) el.removeAttribute('hidden')
    else el.setAttribute('hidden', 'until-found')
    const onMatch = () => setOpen(true)
    el.addEventListener('beforematch', onMatch)
    return () => el.removeEventListener('beforematch', onMatch)
  })

  useEffect(() => {
    if (!anchor) return
    const reveal = () => {
      if (window.location.hash !== `#${anchor}`) return
      setOpen(true)
      window.requestAnimationFrame(() => document.getElementById(anchor)?.scrollIntoView({ block: 'start' }))
    }
    reveal()
    window.addEventListener('hashchange', reveal)
    return () => window.removeEventListener('hashchange', reveal)
  }, [anchor])

  const setBody = useCallback((el: HTMLElement | null) => { body.current = el }, [])
  return { open, setOpen, toggle: () => setOpen(o => !o), bodyId: `fold-${id.replace(/:/g, '')}`, bodyRef: setBody }
}

/** The small "Show / Hide" button under or beside a collapsed part. */
export function FoldToggle({ open, controls, onClick, children, className = '' }: { open: boolean; controls: string; onClick: () => void; children: ReactNode; className?: string }) {
  return (
    <button
      type="button"
      aria-expanded={open}
      aria-controls={controls}
      onClick={onClick}
      className={`inline-flex items-center gap-1.5 self-start rounded-[10px] text-[13px] font-medium text-accent hover:underline underline-offset-2 ${className}`}
    >
      <Icon.Chevron className={`w-3.5 h-3.5 transition-transform motion-reduce:transition-none ${open ? 'rotate-90' : ''}`} />
      {children}
    </button>
  )
}
