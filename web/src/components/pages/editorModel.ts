// The status page editor's draft: its shape, validation and the request body.
// Shared by the create flow and the editor so both check a page the same way.

import { nextItemKey, type ItemDraft } from './PublicationItemsEditor'
import { DEFAULT_SETTINGS, normSettings, type PageSettings } from '../../lib/pageSettings'
import { normPage } from '../../lib/normalize'
import type { FieldErrors, PageBranding, PageLayout, PageVisibility } from '../../lib/types'
import type { HeavySoundType, LightSoundType } from '../../lib/sounds'

export const SLUG_RE = /^[a-z0-9-]{2,48}$/
export const RESERVED = new Set(['new', 'api', 'admin'])
export const HEX_RE = /^#[0-9a-fA-F]{6}$/

export interface Draft {
  slug: string
  title: string
  description: string
  visibility: PageVisibility
  layout: PageLayout
  showLatency: boolean
  branding: PageBranding
  settings: PageSettings
  subscriptions: { enabled: boolean; channelId: number | null }
  items: ItemDraft[]
  audio: { enabled: boolean; down: HeavySoundType; degraded: LightSoundType }
}

export const EMPTY_BRANDING: PageBranding = { logoUrl: '', brandColor: '', websiteUrl: '', supportUrl: '', footerText: '', showResponseTime: false, historyDays: 90 }
export const EMPTY_DRAFT: Draft = {
  slug: '', title: '', description: '', visibility: 'public', layout: 'list', showLatency: false,
  branding: EMPTY_BRANDING, settings: DEFAULT_SETTINGS, subscriptions: { enabled: false, channelId: null }, items: [],
  audio: { enabled: true, down: 'police-siren', degraded: 'modern-chime' },
}

export function httpsOk(v: string): boolean {
  if (!v.trim()) return true
  try { const u = new URL(v.trim()); return u.protocol === 'https:' && !!u.host } catch { return false }
}

export function slugify(title: string): string {
  return title.toLowerCase().normalize('NFKD').replace(/[̀-ͯ]/g, '')
    .replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 48).replace(/-+$/g, '')
}

/** The draft of a saved page, from GET /api/pages/{id}. */
export function draftFromRaw(raw: unknown): Draft {
  const p = normPage(raw)
  return {
    slug: p.slug, title: p.title, description: p.description, visibility: p.visibility, layout: p.layout, showLatency: p.showLatency,
    branding: p.branding,
    settings: normSettings((raw as { settings?: unknown }).settings),
    subscriptions: p.subscriptions,
    items: p.items.map(it => ({ key: nextItemKey(), groupId: it.groupId, monitorId: it.monitorId, displayName: it.displayName, description: it.description })),
    audio: { enabled: !p.alarm.muted, down: (p.alarm.down || 'police-siren') as HeavySoundType, degraded: (p.alarm.degraded || 'modern-chime') as LightSoundType },
  }
}

export function validate(d: Draft): FieldErrors {
  const fe: FieldErrors = {}
  if (!SLUG_RE.test(d.slug)) fe.slug = d.slug === '' ? 'required: 2–48 lowercase letters, digits or dashes' : 'use 2–48 lowercase letters, digits or dashes'
  else if (RESERVED.has(d.slug)) fe.slug = `"${d.slug}" is reserved`
  if (d.title.trim() === '') fe.title = 'required'
  else if (d.title.length > 200) fe.title = 'must be at most 200 characters'
  if (d.description.length > 1000) fe.description = 'must be at most 1000 characters'
  const b = d.branding
  if (!httpsOk(b.logoUrl)) fe.logoUrl = 'must be an https:// URL'
  if (!httpsOk(b.websiteUrl)) fe.websiteUrl = 'must be an https:// URL'
  if (!httpsOk(b.supportUrl)) fe.supportUrl = 'must be an https:// URL'
  if (b.brandColor && !HEX_RE.test(b.brandColor)) fe.brandColor = 'use #rrggbb'
  if (b.footerText.length > 200) fe.footerText = 'must be at most 200 characters'
  const st = d.settings
  if (st.descriptionMd.length > 2000) fe.descriptionMd = 'must be at most 2000 characters'
  if (st.footerMd.length > 600) fe.footerMd = 'must be at most 600 characters'
  if (st.banner.text.length > 300) fe.bannerText = 'must be at most 300 characters'
  if (st.banner.text.includes('\n')) fe.bannerText = 'must be a single line'
  for (const [key, text] of [['descriptionMd', st.descriptionMd], ['footerMd', st.footerMd], ['bannerText', st.banner.text]] as const) {
    if (/<\s*[a-z]/i.test(text) && !fe[key]) fe[key] = 'HTML is not allowed; use markdown'
    for (const m of text.matchAll(/(!?)\[[^\]\n]*\]\(\s*([^)\s]*)/g)) {
      const dest = m[2].toLowerCase()
      if (!fe[key] && (m[1] ? !dest.startsWith('https://') : !/^(https?:\/\/|mailto:)/.test(dest))) fe[key] = m[1] ? 'images must use https://' : 'links must start with https://, http:// or mailto:'
    }
  }
  if (st.canonicalUrl.trim() && !/^https:\/\/[^/\s?#@]+\/?$/i.test(st.canonicalUrl.trim())) fe.canonicalUrl = 'use an https:// address with no path, e.g. https://status.example.com'
  return fe
}

/** The request body of POST /api/pages and PUT /api/pages/{id}. */
export function buildBody(d: Draft) {
  return {
    slug: d.slug, title: d.title.trim(), description: d.description, visibility: d.visibility,
    layout: d.layout, showLatency: d.showLatency,
    alarmMuted: !d.audio.enabled, alarmDown: d.audio.down, alarmDegraded: d.audio.degraded,
    ...d.branding,
    settings: d.settings,
    subscriptionsEnabled: d.subscriptions.enabled,
    subscriberChannelId: d.subscriptions.channelId,
    items: d.items.map((it, i) => ({ sort: i, groupId: it.groupId, monitorId: it.monitorId, displayName: it.displayName.trim(), description: it.description.trim() })),
  }
}

export type SectionId = 'basics' | 'services' | 'appearance' | 'content' | 'updates' | 'access'
export const SECTION_IDS: SectionId[] = ['basics', 'services', 'appearance', 'content', 'updates', 'access']

const SECTION_OF: Record<string, SectionId> = {
  slug: 'basics', title: 'basics', description: 'basics', layout: 'basics',
  items: 'services',
  logoUrl: 'appearance', brandColor: 'appearance', websiteUrl: 'appearance', supportUrl: 'appearance', footerText: 'appearance', headerStyle: 'appearance', font: 'appearance', corners: 'appearance', colorMode: 'appearance',
  descriptionMd: 'content', footerMd: 'content', bannerText: 'content', bannerSeverity: 'content',
  subscriberChannelId: 'updates',
}
/** The editor section that holds a field (access for anything unknown). */
export const sectionOf = (key: string): SectionId => SECTION_OF[key] ?? 'access'
