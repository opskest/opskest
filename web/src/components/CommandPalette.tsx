import { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useLocation } from 'wouter'
import { api, unwrapList } from '../lib/api'
import { useLive } from '../lib/live'
import { canWrite, isAdmin as isAdminRole } from '../lib/roles'
import type { Me } from '../lib/types'
import { Icon, Kbd } from './ui'
import { useSpringEntrance } from '../hooks/useSpringEntrance'

// Ctrl/Cmd+K anywhere: jump to a monitor, group, status page or section,
// or start an action. Typing filters by name and target; ↑/↓ + Enter.

interface Item {
  id: string
  kind: 'Monitor' | '3rd party' | 'Project' | 'Status page' | 'Go to' | 'Action'
  title: string
  sub?: string
  href: string
}

const NAV: Item[] = [
  { id: 'nav-overview', kind: 'Go to', title: 'Overview', href: '/' },
  { id: 'nav-projects', kind: 'Go to', title: 'Projects', href: '/projects' },
  { id: 'nav-monitors', kind: 'Go to', title: 'Monitors', href: '/monitors' },
  { id: 'nav-3p', kind: 'Go to', title: 'Providers', href: '/third-party' },
  { id: 'nav-incidents', kind: 'Go to', title: 'Incidents', href: '/incidents' },
  { id: 'nav-outages', kind: 'Go to', title: 'Outages', href: '/incidents?tab=outages' },
  { id: 'nav-maint', kind: 'Go to', title: 'Maintenance', href: '/maintenance' },
]
/** Shown to the user and admin roles. */
const WRITER: Item[] = [
  { id: 'act-monitor', kind: 'Action', title: 'Add monitor', href: '/monitors/new' },
  { id: 'act-incident', kind: 'Action', title: 'Declare incident', href: '/incidents?new=1' },
  { id: 'act-maint', kind: 'Action', title: 'Schedule maintenance', href: '/maintenance?new=1' },
  { id: 'nav-pages', kind: 'Go to', title: 'Status pages', href: '/pages' },
]
/** Shown to admins only: everything under Settings. */
const ADMIN: Item[] = [
  { id: 'nav-settings', kind: 'Go to', title: 'Settings', href: '/settings' },
  { id: 'nav-regions', kind: 'Go to', title: 'Multi-region checks', href: '/settings?tab=regions' },
  { id: 'nav-notif', kind: 'Go to', title: 'Notification channels', href: '/settings?tab=notifications' },
  { id: 'nav-users', kind: 'Go to', title: 'Users & security', href: '/settings?tab=security' },
  { id: 'nav-backup', kind: 'Go to', title: 'Backup & restore', href: '/settings?tab=database' },
  { id: 'nav-import', kind: 'Go to', title: 'Import / export monitors', href: '/settings?tab=import' },
  { id: 'nav-logs', kind: 'Go to', title: 'Notification log', href: '/settings?tab=logs' },
]

function score(item: Item, q: string): number {
  if (!q) return 1
  const t = item.title.toLowerCase()
  const sub = (item.sub ?? '').toLowerCase()
  if (t === q) return 100
  if (t.startsWith(q)) return 80
  if (t.split(/[\s\-_/.]+/).some(w => w.startsWith(q))) return 60
  if (t.includes(q)) return 40
  if (sub.includes(q)) return 20
  // every query word somewhere
  const words = q.split(/\s+/).filter(Boolean)
  return words.length > 1 && words.every(w => t.includes(w) || sub.includes(w)) ? 30 : 0
}

export function CommandPalette({ user }: { user: Me }) {
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState('')
  const [sel, setSel] = useState(0)
  const [pages, setPages] = useState<Item[]>([])
  const live = useLive()
  const [, navigate] = useLocation()
  const input = useRef<HTMLInputElement>(null)
  const panel = useRef<HTMLDivElement>(null)
  useSpringEntrance(panel, open)
  const list = useRef<HTMLUListElement>(null)
  const isAdmin = isAdminRole(user)
  const writer = canWrite(user)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setOpen(o => !o)
      }
    }
    const onSearch = () => setOpen(true)
    window.addEventListener('opskest:search', onSearch)
    window.addEventListener('keydown', onKey)
    return () => { window.removeEventListener('keydown', onKey); window.removeEventListener('opskest:search', onSearch) }
  }, [])

  useEffect(() => {
    if (!open) return
    setQ('')
    setSel(0)
    const previous = document.activeElement as HTMLElement | null
    const timer = window.setTimeout(() => input.current?.focus(), 0)
    if (writer && pages.length === 0) {
      api.get<unknown>('/api/pages')
        .then(r => setPages(unwrapList<{ id: number; slug: string; title: string }>(r, 'pages')
          .map(p => ({ id: `page-${p.id}`, kind: 'Status page' as const, title: p.title, sub: `/status/${p.slug}`, href: `/pages/${p.id}` }))))
        .catch(() => {})
    }
    return () => { window.clearTimeout(timer); previous?.focus() }
  }, [open, writer])

  const items = useMemo(() => {
    const all: Item[] = []
    for (const m of live.monitors.values()) {
      all.push({ id: `m-${m.id}`, kind: m.vendor ? '3rd party' : 'Monitor', title: m.name, sub: m.target, href: m.vendor ? '/third-party' : `/monitors/${m.id}` })
    }
    for (const g of live.groups) all.push({ id: `g-${g.id}`, kind: 'Project', title: g.name, href: `/projects/${g.id}` })
    all.push(...(writer ? pages : []), ...NAV, ...(writer ? WRITER : []), ...(isAdmin ? ADMIN : []))
    const query = q.trim().toLowerCase()
    return all
      .map(it => ({ it, s: score(it, query) }))
      .filter(x => x.s > 0)
      .sort((a, b) => b.s - a.s || a.it.title.localeCompare(b.it.title))
      .slice(0, 50)
      .map(x => x.it)
  }, [live.monitors, live.groups, pages, q, isAdmin, writer])

  useEffect(() => { setSel(0) }, [q])
  // Only keyboard moves scroll the list. A hover also sets `sel`; scrolling
  // on hover moves the rows under a still pointer, which fires another hover.
  const keyMove = useRef(false)
  useEffect(() => {
    if (!keyMove.current) return
    keyMove.current = false
    list.current?.querySelector<HTMLElement>(`[data-idx="${sel}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [sel])

  if (!open) return null
  const go = (it: Item | undefined) => {
    if (!it) return
    setOpen(false)
    navigate(it.href)
  }

  return createPortal(
    <div className="fixed inset-0 z-50 flex items-start justify-center pt-[8vh] sm:pt-[12vh] px-4 bg-black/40" onMouseDown={() => setOpen(false)}>
      <div ref={panel} role="dialog" aria-modal="true" aria-label="Search and jump"
        className="w-full max-w-2xl max-h-[80dvh] flex flex-col bg-surface border border-strong focus-within:border-accent rounded-2xl shadow-xl overflow-hidden" onMouseDown={e => e.stopPropagation()}>
        <div className="shrink-0 flex items-center gap-3 px-4 border-b border-default">
          <Icon.Search className="w-4 h-4 text-muted shrink-0" />
          <input
            ref={input}
            value={q}
            onChange={e => setQ(e.target.value)}
            onKeyDown={e => {
              if (e.key === 'Tab') { e.preventDefault(); input.current?.focus() }
              else if (e.key === 'ArrowDown') { e.preventDefault(); keyMove.current = true; setSel(s => Math.max(0, Math.min(s + 1, items.length - 1))) }
              else if (e.key === 'ArrowUp') { e.preventDefault(); keyMove.current = true; setSel(s => Math.max(s - 1, 0)) }
              else if (e.key === 'Enter') { e.preventDefault(); go(items[sel]) }
              else if (e.key === 'Escape') { e.preventDefault(); setOpen(false) }
            }}
            placeholder="Find a service, project, page, or action…"
            className="command-search-input min-w-0 flex-1 h-14 bg-transparent text-[14px] text-body placeholder:text-faint"
            aria-label="Search"
            role="combobox"
            aria-expanded="true"
            aria-controls="cmdk-list"
            aria-activedescendant={items[sel] ? `cmdk-${items[sel].id}` : undefined}
          />
          <Kbd>Esc</Kbd>
        </div>
        <ul id="cmdk-list" ref={list} role="listbox" className="min-h-0 max-h-[50dvh] overflow-y-auto overscroll-contain p-2">
          {items.length === 0 && <li className="px-4 py-6 text-center text-[13px] text-muted">Nothing matches “{q}”.</li>}
          {items.map((it, i) => (
            <li key={it.id} id={`cmdk-${it.id}`} role="option" aria-selected={i === sel} data-idx={i}
              onMouseMove={() => { keyMove.current = false; setSel(i) }} onClick={() => go(it)}
              className={`flex items-center gap-3 rounded-lg px-3 py-3 cursor-pointer ${i === sel ? 'bg-accent-subtle' : ''}`}>
              <span className="shrink-0 text-muted" aria-hidden="true">{it.kind === 'Action' ? <Icon.Plus className="w-4 h-4" /> : it.kind === 'Project' ? <Icon.Folder className="w-4 h-4" /> : it.kind === 'Monitor' ? <Icon.Activity className="w-4 h-4" /> : <Icon.ArrowRight className="w-4 h-4" />}</span>
              <span className="min-w-0 flex-1">
                <span className="block text-[13px] text-body truncate">{it.title}</span>
                {it.sub && <span className="block text-[11px] text-muted font-mono truncate">{it.sub}</span>}
              </span>
              <span className="hidden sm:block shrink-0 text-[11px] text-muted">{it.kind === 'Go to' ? 'Navigate' : it.kind}</span>
            </li>
          ))}
        </ul>
        <div className="shrink-0 px-4 py-2.5 border-t border-default text-[11px] text-muted flex gap-4">
          <span className="inline-flex items-center gap-1.5"><Kbd keys={['↑', '↓']} /> move</span><span className="inline-flex items-center gap-1.5"><Kbd>Enter</Kbd> open</span><span className="inline-flex items-center gap-1.5"><Kbd>Esc</Kbd> close</span>
        </div>
      </div>
    </div>, document.body,
  )
}
