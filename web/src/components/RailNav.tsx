import { useEffect, useRef, type KeyboardEvent, type ReactNode } from 'react'
import { Link } from 'wouter'

// A left rail of grouped navigation next to one content card, the layout of
// the Incidents inbox, shared so pages that use it look and behave alike.
// Below the large breakpoint the rail becomes a single horizontally
// scrolling row (it scrolls inside itself, never the page). Items are links,
// so the URL stays the source of truth; arrow keys, Home and End move
// between them and the current one carries aria-current="page".

export interface RailItem {
  id: string
  label: string
  href: string
  icon?: ReactNode
  /** A trailing figure. `undefined` shows nothing, `null` a dash while it loads. */
  count?: number | string | null
}
export interface RailGroup { label: string; items: RailItem[] }

/** The two-column frame: rail on the left (240px), content on the right. */
export function SideNavLayout({ rail, children, className = '' }: { rail: ReactNode; children: ReactNode; className?: string }) {
  return (
    <div className={`grid min-w-0 items-start gap-4 lg:grid-cols-[240px_minmax(0,1fr)] lg:gap-6 ${className}`}>
      {rail}
      <div className="min-w-0">{children}</div>
    </div>
  )
}

export function RailNav({ label, groups, current }: { label: string; groups: RailGroup[]; current: string }) {
  const nav = useRef<HTMLElement>(null)
  // In the horizontal row, bring the current item into view (inside the row only).
  useEffect(() => {
    const el = nav.current
    const active = el?.querySelector<HTMLElement>('[aria-current="page"]')
    if (!el || !active || el.scrollWidth <= el.clientWidth) return
    const box = el.getBoundingClientRect()
    const at = active.getBoundingClientRect()
    if (at.left < box.left || at.right > box.right) el.scrollLeft += at.left - box.left - (box.width - at.width) / 2
  }, [current])
  function onKeyDown(e: KeyboardEvent<HTMLElement>) {
    const keys = ['ArrowDown', 'ArrowUp', 'ArrowRight', 'ArrowLeft', 'Home', 'End']
    if (!keys.includes(e.key)) return
    const items = [...e.currentTarget.querySelectorAll<HTMLElement>('[data-rail-item]')]
    const at = items.indexOf(document.activeElement as HTMLElement)
    if (at < 0) return
    e.preventDefault()
    const next = e.key === 'Home' ? 0 : e.key === 'End' ? items.length - 1
      : e.key === 'ArrowDown' || e.key === 'ArrowRight' ? (at + 1) % items.length : (at - 1 + items.length) % items.length
    items[next]?.focus()
  }
  return (
    <nav ref={nav} aria-label={label} onKeyDown={onKeyDown}
      className="flex min-w-0 items-stretch flex-wrap gap-1 pb-1 lg:sticky lg:top-[88px] lg:flex-col lg:flex-nowrap lg:gap-5 lg:pb-0">
      {groups.map((g, gi) => (
        <div key={g.label} role="group" aria-label={g.label} className="flex shrink-0 items-stretch gap-1 lg:flex-col">
          {gi > 0 && <span aria-hidden="true" className="mx-1 my-2 w-px shrink-0 bg-[var(--border)] lg:hidden" />}
          <div className="mb-0.5 hidden px-3 text-[11px] font-semibold uppercase tracking-wider text-faint lg:block">{g.label}</div>
          {g.items.map(item => {
            const active = item.id === current
            return (
              <Link key={item.id} href={item.href} data-rail-item aria-current={active ? 'page' : undefined}
                className={`flex min-h-11 shrink-0 items-center gap-2.5 whitespace-nowrap rounded-lg px-3 text-sm transition-colors lg:min-h-10 ${active ? 'bg-surface-2 font-semibold text-body' : 'text-muted hover:bg-surface hover:text-body'}`}>
                {item.icon && <span aria-hidden="true" className={`flex h-4 w-4 shrink-0 items-center justify-center ${active ? 'text-accent' : 'text-faint'}`}>{item.icon}</span>}
                <span className="flex-1">{item.label}</span>
                {item.count !== undefined && <span className="tnum min-w-[1.25rem] text-right text-xs text-muted">{item.count ?? '—'}</span>}
              </Link>
            )
          })}
        </div>
      ))}
    </nav>
  )
}
