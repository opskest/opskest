// Chrome shared by the public/private status pages: the page shell, the
// header, the footer, full-page messages (not found, private, error), the
// pinned announcement and the overall banner with its rule.
//
// A status page is the same product as the app: it uses the same tokens,
// controls (Button, Input, Tabs, Badge), radii and type scale. What differs is
// what a customer-facing page legitimately needs: the owner's logo, accent
// colour and copy, and no app navigation.

import { useEffect, type CSSProperties, type ReactNode } from 'react'
import { Link, useLocation } from 'wouter'
import { localZone } from '../../lib/format'
import { serverTimeZone } from '../../lib/runtime'
import { accessibleBrand } from '../../lib/brandContrast'
import { DEFAULT_SETTINGS, pageOrigin, type BannerSeverity, type PageSettings } from '../../lib/pageSettings'
import { applyDefaultTheme } from '../../lib/theme'
import { useResolvedTheme } from '../../hooks/useResolvedTheme'
import type { OverallStatus, PageBranding, PublicMonitor } from '../../lib/types'
import Brand from '../Brand'
import KestrelMark from '../KestrelMark'
import { ThemeToggle } from '../PageShell'
import { buttonClass, EmptyState, Icon } from '../ui'
import { InlineMarkdown, Markdown } from './Markdown'
import { SubscribeMenu } from './Subscribe'
import type { DisplayZone } from './zone'

export const OVERALL: Record<OverallStatus, { text: string; tone: string; icon: 'check' | 'bolt' | 'wrench' }> = {
  operational: { text: 'All systems operational', tone: 'bg-ok-bg text-ok border-ok/30', icon: 'check' },
  degraded: { text: 'Degraded performance', tone: 'bg-warn-bg text-warn border-warn/30', icon: 'bolt' },
  partial: { text: 'Partial outage', tone: 'bg-warn-bg text-warn border-warn/30', icon: 'bolt' },
  major: { text: 'Major outage', tone: 'bg-danger-bg text-danger border-danger/30', icon: 'bolt' },
  maintenance: { text: 'Under maintenance', tone: 'bg-accent-subtle text-accent border-accent-border', icon: 'wrench' },
}

/** The server's rule, re-run on live statuses so the banner follows the
 * stream: any DOWN → partial (under half) or major (half or more); else any
 * DEGRADED/PENDING → degraded; else maintenance. */
export function overallFrom(monitors: PublicMonitor[], maintenanceActive: boolean): OverallStatus {
  const active = monitors.filter(m => m.status !== 'paused')
  const down = active.filter(m => m.status === 'down').length
  if (down > 0) return down * 2 >= active.length ? 'major' : 'partial'
  if (active.some(m => m.status === 'degraded' || m.status === 'pending')) return 'degraded'
  if (maintenanceActive || active.some(m => m.status === 'maintenance')) return 'maintenance'
  return 'operational'
}

const RANK: Record<OverallStatus, number> = { operational: 0, maintenance: 1, degraded: 2, partial: 3, major: 4 }
const IMPACT_LEVEL: Record<string, OverallStatus> = { minor: 'degraded', major: 'partial', maintenance: 'maintenance' }

/** Raise the monitor-derived status to what open incidents declare: a
 * page with an open "major" incident never reads "All systems operational"
 * just because every check happens to pass. */
export function withIncidents(overall: OverallStatus, active: { impact: string }[]): OverallStatus {
  let out = overall
  for (const i of active) {
    const lvl = IMPACT_LEVEL[i.impact] ?? 'degraded'
    if (RANK[lvl] > RANK[out]) out = lvl
  }
  return out
}

/** The one width every status page section lines up to. */
export const STATUS_WIDTH = 'max-w-[1000px]'

const BANNER: Record<OverallStatus, { title: string; frame: string; icon: string }> = {
  operational: { title: 'All systems operational', frame: 'bg-ok-bg border-ok/40', icon: 'bg-ok text-surface' },
  degraded: { title: 'Degraded performance', frame: 'bg-warn-bg border-warn/40', icon: 'bg-warn text-surface' },
  partial: { title: 'Partial outage', frame: 'bg-warn-bg border-warn/40', icon: 'bg-warn text-surface' },
  major: { title: 'Major outage', frame: 'bg-danger-bg border-danger/40', icon: 'bg-danger text-surface' },
  maintenance: { title: 'Maintenance in progress', frame: 'bg-accent-subtle border-accent-border', icon: 'bg-accent text-accent-fg' },
}

function bannerSubtitle(overall: OverallStatus, down: number, affected: number, total: number): string {
  switch (overall) {
    case 'operational': return total > 0 ? (total === 1 ? 'The service is responding normally.' : `All ${total} services are responding normally.`) : 'Every service is responding normally.'
    case 'major':
    case 'partial': return total > 0 ? `${down} of ${total} services ${down === 1 ? 'is' : 'are'} down${affected > down ? `, ${affected - down} more degraded` : ''}.` : 'Some services are unavailable.'
    case 'degraded': return affected > 0 ? `${affected} service${affected === 1 ? ' is' : 's are'} slower than usual or erroring intermittently.` : 'Some services are slower than usual.'
    case 'maintenance': return 'Scheduled maintenance is under way; some services may be briefly unavailable.'
  }
}

/** The single overall-status banner at the top of a page: a full-width
 * bordered banner tinted in the status colour (round filled icon, the state
 * in large type, one sentence), with the update time right-aligned in mono
 * under a dashed underline and the live-stream indicator. The state is
 * written in words as well as shown in colour and shape. */
export function OverallBanner({
  overall, connected, connecting = false, downCount = 0, affectedCount = 0, totalCount = 0, unconfirmedCount = 0, updated, note,
}: {
  overall: OverallStatus
  connected: boolean
  connecting?: boolean
  /** Replaces the computed subtitle (e.g. an incident's title). */
  note?: string
  downCount?: number
  /** Down + degraded + pending. */
  affectedCount?: number
  totalCount?: number
  unconfirmedCount?: number
  updated?: ReactNode
}) {
  const b = BANNER[overall]
  const empty = totalCount === 0
  const uncertain = empty || (overall === 'operational' && unconfirmedCount > 0)
  return (
    <section aria-label="Overall status" className={`rounded-[14px] border p-4 sm:p-5 flex flex-col gap-3 ${uncertain ? 'bg-surface border-default' : b.frame}`}>
      <div className="flex items-center gap-3.5 min-w-0 flex-1">
        <span aria-hidden="true" className={`w-10 h-10 sm:w-11 sm:h-11 rounded-full flex items-center justify-center shrink-0 ${uncertain ? 'bg-surface-2 border border-default text-muted' : b.icon}`}>
          {uncertain ? <Icon.Activity className="w-5 h-5" /> : overall === 'operational'
            ? <Icon.Check className="w-5 h-5" />
            : overall === 'maintenance' ? <Icon.Wrench className="w-5 h-5" /> : <Icon.Alert className="w-5 h-5" />}
        </span>
        <div className="flex flex-col gap-0.5 min-w-0">
          <h2 className="text-[20px] sm:text-[24px] font-semibold leading-tight tracking-tight text-body text-balance">{empty ? 'No services published' : uncertain ? 'Some service states are unconfirmed' : b.title}</h2>
          <p className="text-[14px] text-body-2 break-words">{empty ? 'Status will appear when services are added to this page.' : uncertain ? `${unconfirmedCount} ${unconfirmedCount === 1 ? 'service is' : 'services are'} paused or awaiting current results.` : note ?? bannerSubtitle(overall, downCount, affectedCount, totalCount)}</p>
        </div>
      </div>
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 border-t border-default/60 pt-3 text-[12px] text-body-2 min-w-0">
        {updated && <span className="font-mono tnum underline decoration-dashed decoration-faint underline-offset-4">{updated}</span>}
        <span className="inline-flex items-center gap-1.5 whitespace-nowrap font-medium">
          <span aria-hidden="true" className={`w-1.5 h-1.5 rounded-full ${connected ? 'bg-ok status-pulse' : 'bg-warn'}`} />
          {connected ? 'Live updates' : connecting ? 'Connecting to live updates' : 'Reconnecting…'}
        </span>
      </div>
    </section>
  )
}

const ANNOUNCEMENT: Record<BannerSeverity, { frame: string; label: string; icon: 'info' | 'check' | 'alert' }> = {
  info: { frame: 'bg-accent-subtle border-accent-border text-body', label: 'Announcement', icon: 'info' },
  success: { frame: 'bg-ok-bg border-ok/30 text-body', label: 'Good news', icon: 'check' },
  warning: { frame: 'bg-warn-bg border-warn/30 text-body', label: 'Notice', icon: 'alert' },
  critical: { frame: 'bg-danger-bg border-danger/30 text-body', label: 'Important', icon: 'alert' },
}
const ANNOUNCEMENT_ICON: Record<BannerSeverity, string> = { info: 'text-accent', success: 'text-ok', warning: 'text-warn', critical: 'text-danger' }

/** The owner's pinned announcement. Plain labelled text on a tinted surface:
 * the severity is in the label, not only the colour. */
export function AnnouncementBanner({ banner }: { banner: PageSettings['banner'] }) {
  if (!banner.text.trim()) return null
  const a = ANNOUNCEMENT[banner.severity]
  const Glyph = a.icon === 'info' ? Icon.Info : a.icon === 'check' ? Icon.Check : Icon.Alert
  return (
    <aside aria-label={a.label} role={banner.severity === 'critical' ? 'alert' : 'note'} className={`flex items-start gap-3 rounded-[14px] border px-4 py-3 text-[14px] ${a.frame}`}>
      <Glyph className={`w-4 h-4 mt-1 shrink-0 ${ANNOUNCEMENT_ICON[banner.severity]}`} />
      <p className="min-w-0 break-words"><strong className="font-semibold">{a.label}: </strong><InlineMarkdown source={banner.text} /></p>
    </aside>
  )
}

/** The brand colour drives accents only (links, focus, maintenance); status
 * colours stay semantic. Applied as CSS variables on the page shell. */
function brandStyle(color: string | undefined, dark: boolean): CSSProperties | undefined {
  const brand = accessibleBrand(color ?? '', dark)
  if (!brand) return undefined
  const { accent, foreground } = brand
  return {
    '--accent': accent,
    '--accent-fg': foreground,
    '--brand': accent,
    '--brand-fg': foreground,
    '--brand-inv': foreground === '#ffffff' ? '#000000' : '#ffffff',
    '--accent-hover': `color-mix(in srgb, ${accent} 90%, ${dark ? 'white' : 'black'})`,
    '--accent-subtle': `color-mix(in srgb, ${accent} 10%, var(--surface))`,
    '--accent-border': `color-mix(in srgb, ${accent} 35%, var(--surface))`,
  } as CSSProperties
}

function setHeadLink(rel: string, href: string | null, type?: string): () => void {
  if (!href) return () => {}
  const el = document.createElement('link')
  el.rel = rel
  el.href = href
  if (type) el.type = type
  el.dataset.opskestStatus = rel
  document.head.appendChild(el)
  return () => el.remove()
}

export function StatusShell({
  children, branding, settings, isPublic = false, feedAvailable = false, canonical = false, slug,
}: {
  children: ReactNode
  branding?: PageBranding
  settings?: PageSettings
  /** Public pages announce their canonical address and feed to crawlers and readers. */
  isPublic?: boolean
  feedAvailable?: boolean
  canonical?: boolean
  slug?: string
}) {
  const theme = useResolvedTheme()
  const [location] = useLocation()
  const st = settings ?? DEFAULT_SETTINGS
  // The page's logo doubles as its favicon while it is open.
  useEffect(() => {
    const logo = branding?.logoUrl
    if (!logo) return
    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    if (!link) return
    const prev = link.href
    link.href = logo
    return () => { link.href = prev }
  }, [branding?.logoUrl])

  // Font and corner style are attributes on <html> so dialogs that render in a
  // portal follow the page too; they are removed again when the page unmounts.
  useEffect(() => {
    const root = document.documentElement
    root.dataset.statusFont = st.font
    root.dataset.statusCorners = st.corners
    return () => { delete root.dataset.statusFont; delete root.dataset.statusCorners }
  }, [st.font, st.corners])

  // The owner's default appearance applies to visitors who never chose one.
  useEffect(() => (st.colorMode ? applyDefaultTheme(st.colorMode) : undefined), [st.colorMode])

  useEffect(() => {
    if (!isPublic || !canonical || !slug) return
    return setHeadLink('canonical', `${pageOrigin(st)}${location}`)
  }, [isPublic, canonical, slug, location, st])
  useEffect(() => {
    if (!isPublic || !feedAvailable || !slug) return
    return setHeadLink('alternate', `${pageOrigin(st)}/api/public/pages/${encodeURIComponent(slug)}/feed.atom`, 'application/atom+xml')
  }, [isPublic, feedAvailable, slug, st])

  return <div className="min-h-screen bg-ground text-body flex flex-col" style={brandStyle(branding?.brandColor, theme === 'dark')}>{children}</div>
}

/** Public navigation and page identity. Private pages omit subscriptions
 * because external feed readers and webhooks cannot use the session. */
export function StatusHeader({
  slug, title, branding, settings = DEFAULT_SETTINGS, isPublic = true, feedAvailable = true, layout = 'list',
}: {
  slug: string
  title: string
  branding?: PageBranding
  settings?: PageSettings
  isPublic?: boolean
  feedAvailable?: boolean
  /** A page is a status page or a wallboard for good; the nav only offers its own views. */
  layout?: 'list' | 'board'
}) {
  const home = `/status/${encodeURIComponent(slug)}`
  const style = settings.headerStyle === 'solid' ? 'status-header-solid' : settings.headerStyle === 'tinted' ? 'status-header-tinted' : 'bg-ground'
  const subscribeAvailable = isPublic && (!!branding?.subscribeEmail || !!branding?.subscribeSlack || feedAvailable)
  return (
    <header className={`border-b border-default relative sm:sticky sm:top-0 z-40 ${style}`}>
      <div className={`${STATUS_WIDTH} mx-auto px-4 sm:px-6 min-h-16 flex flex-wrap items-center gap-x-4 gap-y-2 py-2`}>
        <Link href={layout === 'board' ? `${home}/board` : home} aria-label={`${title} status home`} className="flex items-center gap-3 min-w-0 mr-auto rounded-[10px]">
          {branding?.logoUrl
            ? <img src={branding.logoUrl} alt={title} className="h-8 w-auto max-w-[160px] object-contain" referrerPolicy="no-referrer" />
            : <KestrelMark className="w-8 h-8 shrink-0 text-accent" />}
          <span className="text-[16px] font-semibold tracking-tight truncate max-w-[180px] sm:max-w-[320px]">{title}</span>
        </Link>
        <div className="flex items-center gap-2">
          {branding?.supportUrl && <a href={branding.supportUrl} target="_blank" rel="noopener noreferrer" className={`hidden md:inline-flex ${buttonClass('ghost', 'md')}`}>Contact support</a>}
          {subscribeAvailable && <SubscribeMenu slug={slug} branding={branding} settings={settings} feedAvailable={feedAvailable} />}
          <ThemeToggle />
        </div>
      </div>
    </header>
  )
}

export function StatusFooter({ branding, settings, zone }: { branding?: PageBranding; settings?: PageSettings; zone?: DisplayZone }) {
  return (
    <footer className="mt-auto border-t border-default py-8 px-4 sm:px-6 text-center text-[12px] text-faint flex flex-col gap-2 items-center">
      {settings?.footerMd
        ? <Markdown source={settings.footerMd} className="max-w-2xl text-[13px] text-muted" />
        : branding?.footerText && <p className="text-muted">{branding.footerText}</p>}
      {(branding?.websiteUrl || branding?.supportUrl) && (
        <p className="flex gap-4 sm:hidden">
          {branding.websiteUrl && <a href={branding.websiteUrl} target="_blank" rel="noopener noreferrer" className="text-accent hover:underline">Website</a>}
          {branding.supportUrl && <a href={branding.supportUrl} target="_blank" rel="noopener noreferrer" className="text-accent hover:underline">Support</a>}
        </p>
      )}
      <p>{zone ? <>Times shown in {zone.label}.</> : <>Times shown in your time zone ({localZone()}).</>} Daily bars follow the server timezone ({serverTimeZone()}).</p>
      <p>Powered by Opskest</p>
    </footer>
  )
}

/** A full-page message in the same shell and header as a status page: not
 * found, private, error. It names no page, so it shows the Opskest mark. */
export function StatusMessage({ title, children, action }: { title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <StatusShell>
      <header className="border-b border-default bg-ground">
        <div className={`${STATUS_WIDTH} mx-auto px-4 sm:px-6 min-h-16 flex items-center justify-between gap-3 py-2`}>
          <Brand />
          <ThemeToggle />
        </div>
      </header>
      <main id="main" className={`flex-1 w-full ${STATUS_WIDTH} mx-auto px-4 sm:px-6 py-8 sm:py-12 flex items-start sm:items-center`}>
        <div className="w-full max-w-xl mx-auto"><EmptyState heading title={title} action={action}>{children}</EmptyState></div>
      </main>
      <StatusFooter />
    </StatusShell>
  )
}
