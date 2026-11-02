// A status page's presentation and content options (the `settings` document of
// a page). They sit beside the shared Page / PublicPage types rather than in
// them: this module reads them from the same API responses.

export type HeaderStyle = 'plain' | 'tinted' | 'solid'
export type PageFont = 'system' | 'humanist' | 'neutral' | 'serif' | 'mono'
export type Corners = 'rounded' | 'square'
export type ColorMode = '' | 'system' | 'light' | 'dark'
export type BannerSeverity = 'info' | 'success' | 'warning' | 'critical'

export interface PageSettings {
  headerStyle: HeaderStyle
  font: PageFont
  corners: Corners
  /** Appearance for a first visit; '' keeps the app default. */
  colorMode: ColorMode
  descriptionMd: string
  footerMd: string
  banner: { text: string; severity: BannerSeverity }
  showUptime: boolean
  showCert: boolean
  showLastCheck: boolean
  refreshSeconds: 30 | 60 | 120 | 300
  /** Open every group and timeline on load. Off: the page starts collapsed. */
  defaultExpanded: boolean
  feedEnabled: boolean
  /** Show scheduled and in-progress maintenance on the public page. On by default. */
  showMaintenance: boolean
  /** https origin the page is reached at behind a proxy, or ''. */
  canonicalUrl: string
}

export const DEFAULT_SETTINGS: PageSettings = {
  headerStyle: 'plain', font: 'system', corners: 'rounded', colorMode: '',
  descriptionMd: '', footerMd: '', banner: { text: '', severity: 'info' },
  showUptime: true, showCert: false, showLastCheck: false, refreshSeconds: 60, defaultExpanded: false, feedEnabled: true, showMaintenance: true, canonicalUrl: '',
}

export const HEADER_STYLES: { value: HeaderStyle; label: string; hint: string }[] = [
  { value: 'plain', label: 'Plain', hint: 'The same neutral header as the app.' },
  { value: 'tinted', label: 'Tinted', hint: 'A soft wash of the brand colour.' },
  { value: 'solid', label: 'Solid', hint: 'Brand colour with legible text.' },
]

export const FONTS: { value: PageFont; label: string; stack: string }[] = [
  { value: 'system', label: 'Opskest (Segoe UI / system)', stack: "'Segoe UI', ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, sans-serif" },
  { value: 'humanist', label: 'Humanist sans', stack: "Seravek, 'Gill Sans Nova', Ubuntu, Calibri, 'DejaVu Sans', sans-serif" },
  { value: 'neutral', label: 'Neutral sans (Helvetica / Arial)', stack: "'Helvetica Neue', Helvetica, Arial, 'Liberation Sans', sans-serif" },
  { value: 'serif', label: 'Serif (Georgia)', stack: "Charter, 'Bitstream Charter', 'Sitka Text', Cambria, Georgia, serif" },
  { value: 'mono', label: 'Monospace', stack: "'JetBrains Mono', ui-monospace, 'SF Mono', Menlo, Consolas, monospace" },
]

export const REFRESH_CHOICES: { value: PageSettings['refreshSeconds']; label: string }[] = [
  { value: 30, label: 'Every 30 seconds' },
  { value: 60, label: 'Every minute' },
  { value: 120, label: 'Every 2 minutes' },
  { value: 300, label: 'Every 5 minutes' },
]

type Raw = Record<string, unknown>
const obj = (v: unknown): Raw => (v && typeof v === 'object' && !Array.isArray(v) ? v as Raw : {})
const str = (v: unknown): string => (typeof v === 'string' ? v : '')
const pick = <T extends string>(v: unknown, allowed: readonly T[], fallback: T): T => (allowed.includes(v as T) ? v as T : fallback)
const bool = (v: unknown, fallback: boolean): boolean => (typeof v === 'boolean' ? v : fallback)

export function normSettings(raw: unknown): PageSettings {
  const o = obj(raw)
  const b = obj(o.banner)
  const refresh = Number(o.refreshSeconds)
  const d = DEFAULT_SETTINGS
  return {
    headerStyle: pick(o.headerStyle, ['plain', 'tinted', 'solid'] as const, d.headerStyle),
    font: pick(o.font, ['system', 'humanist', 'neutral', 'serif', 'mono'] as const, d.font),
    corners: pick(o.corners, ['rounded', 'square'] as const, d.corners),
    colorMode: pick(o.colorMode, ['', 'system', 'light', 'dark'] as const, d.colorMode),
    descriptionMd: str(o.descriptionMd), footerMd: str(o.footerMd),
    banner: { text: str(b.text), severity: pick(b.severity, ['info', 'success', 'warning', 'critical'] as const, 'info') },
    showUptime: bool(o.showUptime, d.showUptime), showCert: bool(o.showCert, d.showCert), showLastCheck: bool(o.showLastCheck, d.showLastCheck),
    refreshSeconds: refresh === 30 || refresh === 120 || refresh === 300 ? refresh : 60,
    defaultExpanded: bool(o.defaultExpanded, d.defaultExpanded),
    feedEnabled: bool(o.feedEnabled, d.feedEnabled),
    showMaintenance: bool(o.showMaintenance, d.showMaintenance),
    canonicalUrl: str(o.canonicalUrl),
  }
}

/** The public page's own settings, read from `page.settings`. */
export function normPublicSettings(rawPublicPage: unknown): PageSettings {
  return normSettings(obj(obj(rawPublicPage).page).settings)
}

/** Opt-in per-service details (last check, certificate days) by monitor id. */
export interface MonitorExtra { lastCheckAt?: number; certDays?: number }

export function normMonitorExtras(rawPublicPage: unknown): Map<number, MonitorExtra> {
  const out = new Map<number, MonitorExtra>()
  for (const [id, v] of Object.entries(obj(obj(rawPublicPage).monitorExtras))) {
    const x = obj(v)
    out.set(Number(id), {
      lastCheckAt: typeof x.lastCheckAt === 'number' ? x.lastCheckAt : undefined,
      certDays: typeof x.certDays === 'number' ? x.certDays : undefined,
    })
  }
  return out
}

/** Settings plus the opt-in extras: what the status page needs beyond PublicPage. */
export interface PublicExtras { settings: PageSettings; extras: Map<number, MonitorExtra>; feedAvailable: boolean }

export function normPublicExtras(raw: unknown): PublicExtras {
  const page = obj(obj(raw).page)
  return { settings: normPublicSettings(raw), extras: normMonitorExtras(raw), feedAvailable: page.feedAvailable !== false }
}

/** The address a page is reached at: its canonical origin when set, else this browser's. */
export function pageOrigin(settings: Pick<PageSettings, 'canonicalUrl'>): string {
  return settings.canonicalUrl ? settings.canonicalUrl.replace(/\/+$/, '') : window.location.origin
}
