// The app chrome for every signed-in page: Brand, the primary nav, the
// theme toggle and the account menu. Below 1024 px the nav folds into a
// menu button so the header never scrolls sideways.

import { Suspense, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Link, useLocation } from 'wouter'
import { useTheme } from '../hooks/useTheme'
import { useLive } from '../lib/live'
import { canWrite, isAdmin, ROLE_LABEL } from '../lib/roles'
import type { Me } from '../lib/types'
import Brand from './Brand'
import KestrelMark from './KestrelMark'
import { Icon } from './icons'
import { Kbd, modKey, SkeletonRows, Tooltip } from './ui'

interface NavItem {
  label: string
  href: string
  adminOnly?: boolean
  /** Hidden from viewers: needs the user or admin role. */
  writerOnly?: boolean
  icon: keyof typeof Icon
}

const NAV_ITEMS: NavItem[] = [
  { label: 'Overview', icon: 'Activity', href: '/' },
  { label: 'Monitors', icon: 'Activity', href: '/monitors' },
  { label: 'Projects', icon: 'Folder', href: '/projects' },
  { label: 'Providers', icon: 'Globe', href: '/third-party' },
  { label: 'Incidents', icon: 'AlertTriangle', href: '/incidents' },
  { label: 'Maintenance', icon: 'Wrench', href: '/maintenance' },
  { label: 'Status Pages', icon: 'Globe', href: '/pages', writerOnly: true },
  { label: 'Settings', icon: 'Gear', href: '/settings', adminOnly: true },
]

function isActive(path: string, href: string): boolean {
  if (href === '/') return path === '/'
  if (href === '/projects') return path === '/projects' || path.startsWith('/projects/') || path.startsWith('/groups/')
  return path === href || path.startsWith(`${href}/`)
}

/** Closes a popover on outside click and Escape. */
function useDismiss(open: boolean, close: () => void) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    function onDown(e: MouseEvent) {
      if (!ref.current?.contains(e.target as Node)) close()
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') close()
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open, close])
  return ref
}

function AccountMenu({ user, onLogout, locked = false }: { user: Me; onLogout: () => void; locked?: boolean }) {
  const [open, setOpen] = useState(false)
  const ref = useDismiss(open, () => setOpen(false))
  const trigger = useRef<HTMLButtonElement>(null)
  useEffect(() => { if (open) ref.current?.querySelector<HTMLElement>('[role="menuitem"]')?.focus() }, [open])
  const initial = (user.name || user.username || user.email).charAt(0).toUpperCase()

  return (
    <div ref={ref} className="relative shrink-0">
      <button
        ref={trigger}
        type="button"
        onClick={() => setOpen(v => !v)}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`Account: ${user.username || user.email}`}
        className="flex items-center gap-2 h-10 min-w-10 px-2 rounded-[10px] hover:bg-surface-hover transition-colors"
      >
        {user.avatarUrl
          ? <img src={user.avatarUrl} alt="" referrerPolicy="no-referrer" className="w-6 h-6 rounded-sm" />
          : (
            <span aria-hidden="true" className="w-6 h-6 rounded-sm bg-surface-2 border border-default flex items-center justify-center text-[11px] font-semibold text-body-2">
              {initial}
            </span>
          )}
        <span className="hidden xl:inline text-xs text-muted max-w-[160px] truncate">{user.username || user.email}</span>
        <Icon.Chevron className="hidden sm:block w-3 h-3 text-faint rotate-90 shrink-0" />
      </button>

      {open && (
        <div role="menu" aria-label="Account" className="absolute right-0 top-full mt-2 w-72 max-w-[calc(100vw-32px)] z-40 bg-surface border border-default rounded-xl shadow-lg p-1.5 flex flex-col"
          onKeyDown={event => {
            if (event.key === 'Escape') { event.stopPropagation(); setOpen(false); trigger.current?.focus(); return }
            const items = Array.from(event.currentTarget.querySelectorAll<HTMLElement>('[role="menuitem"]'))
            const index = items.indexOf(document.activeElement as HTMLElement)
            const next = event.key === 'ArrowDown' ? (index + 1) % items.length : event.key === 'ArrowUp' ? (index + items.length - 1) % items.length : event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : -1
            if (next >= 0) { event.preventDefault(); items[next]?.focus() }
            if (event.key === 'Tab') setOpen(false)
          }}>
          <div className="px-3 py-3 mb-1 border-b border-default">
            <p className="text-[13px] font-medium text-body truncate">{user.name || user.username || user.email}</p>
            <div className="mt-1 flex items-center justify-between gap-3"><p className="text-[12px] text-muted truncate">{user.username || user.email}</p><span className="shrink-0 rounded-md bg-surface-2 px-2 py-0.5 text-[11px] text-muted">{ROLE_LABEL[user.role] ?? 'Viewer'}</span></div>
          </div>
          <Link href="/account/security" role="menuitem" onClick={() => setOpen(false)} className="flex items-center gap-3 rounded-lg px-3 py-3 text-[13px] text-body-2 hover:bg-surface-hover focus-visible:bg-surface-hover"><Icon.Shield className="w-4 h-4 shrink-0 text-muted" />Account security</Link>
          {isAdmin(user) && !locked && <Link href="/settings" role="menuitem" onClick={() => setOpen(false)} className="flex items-center gap-3 rounded-lg px-3 py-3 text-[13px] text-body-2 hover:bg-surface-hover focus-visible:bg-surface-hover"><Icon.Gear className="w-4 h-4 shrink-0 text-muted" />Workspace settings</Link>}
          <div className="my-1 border-t border-default" role="separator" />
          <button
            type="button"
            role="menuitem"
            onClick={() => { setOpen(false); onLogout() }}
            className="flex items-center gap-3 rounded-lg px-3 py-3 text-[13px] text-body-2 hover:bg-surface-hover focus-visible:bg-surface-hover transition-colors text-left"
          >
            <Icon.SignOut className="w-3.5 h-3.5 text-muted" />
            Sign out
          </button>
        </div>
      )}
    </div>
  )
}

export function ThemeToggle({ className = '' }: { className?: string }) {
  return <ThemeSliderSwitch className={className} />
}

export function ThemeSliderSwitch({ className = '' }: { className?: string }) {
  const { theme, setTheme } = useTheme()
  const [open, setOpen] = useState(false)
  const ref = useDismiss(open, () => setOpen(false))
  const trigger = useRef<HTMLButtonElement>(null)
  const choices = [
    { value: 'light' as const, label: 'Light', glyph: Icon.Sun },
    { value: 'dark' as const, label: 'Dark', glyph: Icon.Moon },
    { value: 'system' as const, label: 'Auto', glyph: Icon.System },
  ]
  const current = choices.find(choice => choice.value === theme) ?? choices[2]
  const Glyph = current.glyph
  function close() { setOpen(false); trigger.current?.focus() }
  return <div ref={ref} className={`relative shrink-0 ${className}`}>
    <Tooltip content={`Appearance: ${current.label}`} disabled={open}><button ref={trigger} type="button" aria-label={`Appearance: ${current.label}`} aria-haspopup="menu" aria-expanded={open}
      onClick={() => setOpen(value => !value)} onKeyDown={event => { if (event.key === 'ArrowDown') { event.preventDefault(); setOpen(true) } }}
      className="h-10 w-10 inline-flex items-center justify-center rounded-[10px] text-muted hover:text-body hover:bg-surface-2 transition-colors">
      <Glyph className="h-[18px] w-[18px]" />
    </button></Tooltip>
    {open && <div role="menu" aria-label="Appearance" className="absolute right-0 top-full mt-2 z-50 min-w-[168px] rounded-xl border border-default bg-surface p-1.5 shadow-lg"
      onKeyDown={event => {
        if (event.key === 'Escape') { event.stopPropagation(); close(); return }
        const buttons = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('button'))
        const index = buttons.indexOf(document.activeElement as HTMLButtonElement)
        const next = event.key === 'ArrowDown' ? (index + 1) % buttons.length : event.key === 'ArrowUp' ? (index + buttons.length - 1) % buttons.length : event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : -1
        if (next >= 0) { event.preventDefault(); buttons[next]?.focus() }
        if (event.key === 'Tab') setOpen(false)
      }}>
      {choices.map(choice => <button key={choice.value} type="button" role="menuitemradio" aria-checked={theme === choice.value} autoFocus={theme === choice.value}
        onClick={() => { setTheme(choice.value); close() }} className="flex w-full h-10 items-center gap-3 rounded-lg px-3 text-[13px] text-body-2 hover:bg-surface-2 focus:bg-surface-2">
        <choice.glyph className="h-4 w-4 shrink-0" /><span className="flex-1 text-left">{choice.label}</span>{theme === choice.value && <Icon.Check className="h-4 w-4 text-accent" />}
      </button>)}
    </div>}
  </div>
}

function NavLink({ item, path, onClick, block }: { item: NavItem; path: string; onClick?: () => void; block?: boolean }) {
  const active = isActive(path, item.href)
  const Glyph = Icon[item.icon]
  return (
    <Link
      href={item.href}
      onClick={onClick}
      aria-current={active ? 'page' : undefined}
      className={`flex items-center gap-3 ${block ? 'px-3 py-2.5' : 'px-3 py-2'} rounded-md text-[13px] font-medium whitespace-nowrap transition-colors
        ${active ? 'nav-current bg-surface-2 text-body font-semibold' : 'text-muted hover:text-body hover:bg-surface-hover'}`}
    >
      <Glyph className="w-4 h-4 shrink-0" />{item.label}
    </Link>
  )
}

function MobileNav({ items, path }: { items: NavItem[]; path: string }) {
  const [open, setOpen] = useState(false)
  const ref = useDismiss(open, () => setOpen(false))
  return (
    <div ref={ref} className="relative lg:hidden">
      <button
        type="button"
        onClick={() => setOpen(v => !v)}
        aria-expanded={open}
        aria-label="Menu"
        className="w-10 h-10 flex items-center justify-center rounded-[10px] text-muted hover:text-body hover:bg-surface-hover"
      >
        {open ? <Icon.X className="w-4 h-4" /> : <Icon.Menu className="w-4 h-4" />}
      </button>
      {open && (
        <nav aria-label="Primary" className="absolute right-0 top-full mt-1 w-56 z-40 bg-surface border border-default rounded-xl shadow-lg p-1 flex flex-col animate-fade-in">
          {items.map(item => <NavLink key={item.href} item={item} path={path} block onClick={() => setOpen(false)} />)}
        </nav>
      )}
    </div>
  )
}

/** The live system summary in the header. Its own component so a locked session never opens the live stream. */
function HealthStatus() {
  const live = useLive()
  const health = useMemo(() => {
    let down = 0
    let degraded = 0
    let waiting = 0
    let maintenance = 0
    let active = 0
    for (const m of live.monitors.values()) {
      if (!m.paused && !m.vendor) {
        active++
        if (m.status === 'down') down++
        else if (m.status === 'degraded') degraded++
        else if (m.status === 'maintenance' || m.maint) maintenance++
        else if (m.status !== 'up') waiting++
      }
    }
    if (!live.connected) return { label: live.lastUpdate ? 'Live updates paused' : 'Connecting', tone: 'text-muted' }
    if (!live.connectivity.online) return { label: 'Monitor network offline', tone: 'text-warn' }
    if (down) return { label: `${down} down`, tone: 'text-danger' }
    if (degraded) return { label: `${degraded} degraded`, tone: 'text-warn' }
    if (waiting) return { label: `${waiting} awaiting results`, tone: 'text-muted' }
    if (maintenance) return { label: `${maintenance} in maintenance`, tone: 'text-muted' }
    if (!active) return { label: 'No active monitors', tone: 'text-muted' }
    return { label: 'All systems operational', tone: 'text-ok' }
  }, [live.monitors, live.connected, live.lastUpdate, live.connectivity.online])
  return <span className="hidden xl:inline-flex items-center gap-2 ml-5 text-xs text-muted" role="status"><span className={'h-1.5 w-1.5 rounded-full bg-current ' + health.tone} />{health.label}</span>
}

export type ShellWidth = 'narrow' | 'wide' | 'full'

/** `fit` makes the page exactly one screen tall on wide displays: nothing scrolls except regions the page marks as scrollable. */
export function PageShell({ user, onLogout, children, width = 'wide', fit = false }: { user: Me; onLogout: () => void; children: ReactNode; width?: ShellWidth; fit?: boolean }) {
  const [path] = useLocation()
  // While the workspace requires two-factor and this account has none, the app is just Account security: no navigation, no search.
  const locked = !!user.twoFactorRequired
  const items = locked ? [] : NAV_ITEMS.filter(item => (!item.adminOnly || isAdmin(user)) && (!item.writerOnly || canWrite(user)))
  const current = items.find(item => isActive(path, item.href))
  return <div className="workspace-root min-h-screen bg-ground text-body lg:pl-[72px] pb-20 lg:pb-0">
    <a href="#main" className="skip-link">Skip to content</a>
    <aside className="fixed inset-y-0 left-0 z-40 hidden lg:flex w-[72px] flex-col items-center border-r border-default bg-surface py-5">
      <Link href="/" aria-label="Opskest home" className="mb-8 flex h-10 w-10 items-center justify-center text-accent"><KestrelMark className="w-9 h-9" /></Link>
      {!locked && <nav aria-label="Workspace" className="flex flex-col gap-2 w-full px-2">{items.map(item => {
        const active=isActive(path,item.href), Glyph=Icon[item.icon]
        const short = item.label === 'Status Pages' ? 'Publish' : item.label === 'Maintenance' ? 'Schedule' : item.label
        return <Link key={item.href} href={item.href} title={item.label} aria-current={active ? 'page' : undefined} className={'group flex flex-col items-center justify-center gap-1.5 h-[58px] rounded-xl transition-colors ' + (active ? 'bg-accent-subtle text-accent' : 'text-muted hover:text-body hover:bg-surface-2')}><Glyph className="h-5 w-5" /><span className="text-[9px] leading-none font-medium">{short}</span></Link>
      })}</nav>}
      {!locked && <div className="mt-auto pt-5"><button type="button" aria-label="Search workspace" onClick={() => window.dispatchEvent(new Event('opskest:search'))} className="h-10 w-10 rounded-xl flex items-center justify-center text-muted hover:text-body hover:bg-surface-2"><Icon.Search className="w-5 h-5" /></button></div>}
    </aside>
    <header className="sticky top-0 z-30 flex h-16 items-center gap-3 border-b border-default bg-ground px-4 sm:px-7">
      <div className="hidden lg:flex items-center gap-3 min-w-0"><Brand href="/" /><span className="text-faint">/</span><span className="text-sm text-muted truncate">{current?.label ?? 'Account'}</span></div>
      <div className="lg:hidden min-w-0 flex-1"><span className="text-base font-semibold truncate block">{current?.label ?? 'Account'}</span></div>
      {!locked && <HealthStatus />}
      <div className="hidden lg:block flex-1" />
      {!locked && <button type="button" onClick={() => window.dispatchEvent(new Event('opskest:search'))} aria-label="Search workspace" className="inline-flex h-10 items-center gap-3 rounded-[10px] border border-default bg-surface px-3 text-muted hover:text-body hover:border-strong"><Icon.Search className="h-4 w-4" /><span className="hidden 2xl:inline text-xs">Find a service or action</span><span className="hidden lg:inline-flex border-l border-default pl-3"><Kbd keys={[modKey(), 'K']} /></span></button>}
      <ThemeSliderSwitch /><AccountMenu user={user} onLogout={onLogout} locked={locked} />{!locked && <MobileNav items={items} path={path} />}
    </header>
    <main id="main" className={'mx-auto min-w-0 px-4 sm:px-7 lg:px-9 py-6 sm:py-8 ' + (width === 'narrow' ? 'max-w-5xl' : width === 'full' ? 'w-full' : 'max-w-[1800px]') + (fit ? ' xl:relative xl:flex xl:flex-col xl:h-[calc(100dvh-4rem)] xl:overflow-hidden xl:py-5' : '')}>
      <Suspense fallback={<SkeletonRows rows={5} label="Opening workspace" />}>{children}</Suspense>
    </main>
    {!locked && <nav aria-label="Mobile workspace" className="workspace-dock fixed bottom-0 inset-x-0 z-30 lg:hidden border-t border-default bg-surface px-2 pb-[env(safe-area-inset-bottom)] flex items-center justify-around h-[68px]">{items.filter(item => ['/','/monitors','/projects','/incidents'].includes(item.href)).map(item => {
      const Glyph=Icon[item.icon],active=isActive(path,item.href)
      return <Link key={item.href} href={item.href} aria-current={active ? 'page' : undefined} className={'flex flex-col items-center gap-1.5 px-4 py-2 rounded-xl text-[10px] ' + (active ? 'text-accent bg-accent-subtle' : 'text-muted')}><Glyph className="w-5 h-5" />{item.label}</Link>
    })}</nav>}
  </div>
}

/**
 * The header of every signed-in page: a 27px title (with optional inline badges), one supporting line
 * (`children`) or a free-form `meta` row, and the page's actions on the right. Detail pages pass `back`
 * for the "All monitors" style link above the title. Nothing else on a page is an h1.
 */
export function PageTitle({ title, children, actions, back, badges, meta }: {
  title: ReactNode
  children?: ReactNode
  actions?: ReactNode
  back?: { href: string; label: string }
  badges?: ReactNode
  meta?: ReactNode
}) {
  return (
    <div data-page-title className="mb-7">
      {back && (
        <Link href={back.href} className="mb-4 inline-flex items-center gap-2 text-[13px] text-muted transition-colors hover:text-body">
          <Icon.ArrowLeft className="h-4 w-4" />{back.label}
        </Link>
      )}
      <div className="flex flex-wrap items-start justify-between gap-5">
        <div className="flex min-w-0 flex-col gap-1">
          <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5">
            <h1 className="min-w-0 text-[27px] leading-tight font-semibold tracking-tight text-body text-balance [overflow-wrap:anywhere]">{title}</h1>
            {badges}
          </div>
          {children && <p data-page-sub className="text-[14px] text-muted leading-relaxed max-w-[65ch]">{children}</p>}
          {meta}
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
    </div>
  )
}

/** A within-page breadcrumb, e.g. Monitors › api.example.com › Edit. */
export function Crumbs({ items }: { items: { label: string; href?: string }[] }) {
  return (
    <nav aria-label="Breadcrumb" className="flex items-center gap-2 min-w-0 overflow-hidden">
      {items.map((item, i) => {
        const isLast = i === items.length - 1
        return (
          <span key={`${item.label}-${i}`} className="flex items-center gap-2 min-w-0">
            {i > 0 && <Icon.Chevron className="w-3.5 h-3.5 text-faint shrink-0" />}
            {item.href && !isLast
              ? <Link href={item.href} className="text-sm font-medium text-muted hover:text-body truncate transition-colors">{item.label}</Link>
              : <span className="text-sm font-semibold text-body truncate" aria-current="page">{item.label}</span>}
          </span>
        )
      })}
    </nav>
  )
}
