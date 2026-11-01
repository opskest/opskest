// Helpers for `vendor` monitors (3rd-party status feeds).

import type { Status, VendorIncident, VendorProvider } from './types'

const PROVIDER_LABEL: Record<VendorProvider, string> = {
  statuspage: 'Statuspage',
  google: 'Google status',
  rss: 'RSS feed',
}

export function providerLabel(p: string | undefined): string {
  return p && p in PROVIDER_LABEL ? PROVIDER_LABEL[p as VendorProvider] : 'Status feed'
}

/** The human-facing vendor status page for a feed URL (its origin). */
export function vendorPageUrl(target: string): string | null {
  try {
    const u = new URL(target)
    return u.protocol === 'http:' || u.protocol === 'https:' ? u.origin : null
  } catch {
    return null
  }
}

/** Our status, in the vendor's vocabulary. */
export function vendorStatusLabel(s: Status): string {
  switch (s) {
    case 'down': return 'Major outage'
    case 'degraded': return 'Degraded / partial'
    case 'maintenance': return 'Maintenance'
    case 'pending': return 'Checking'
    case 'up': return 'Operational'
    case 'paused': return 'Paused'
    default: return 'Unknown'
  }
}

function time(v: unknown): number | null {
  if (typeof v === 'number' && Number.isFinite(v)) return v
  if (typeof v === 'string') {
    const t = Date.parse(v)
    return Number.isNaN(t) ? null : t
  }
  return null
}

export function normVendorIncident(raw: unknown): VendorIncident {
  const o = (raw && typeof raw === 'object' ? raw : {}) as Record<string, unknown>
  const s = (v: unknown) => (typeof v === 'string' ? v : '')
  return {
    title: s(o.title) || 'Untitled incident',
    status: s(o.status),
    impact: s(o.impact),
    startedAt: time(o.startedAt),
    updatedAt: time(o.updatedAt),
    url: s(o.url),
    components: Array.isArray(o.components) ? o.components.filter((c): c is string => typeof c === 'string') : [],
  }
}

/** A vendor impact → badge tone. */
export function impactTone(impact: string): 'danger' | 'warn' | 'accent' | 'neutral' {
  const i = impact.toLowerCase()
  if (i === 'critical' || i === 'major') return 'danger'
  if (i === 'minor' || i === 'degraded' || i === 'partial') return 'warn'
  if (i === 'maintenance') return 'accent'
  return 'neutral'
}

/** Check if a monitor is a 3rd party vendor / external status page feed */
/** A 3rd-party status feed (monitor flag `vendor`): it only alerts on
 * incidents the vendor publishes and is never counted among your own
 * monitors. `groups` is accepted for older call sites and ignored. */
export function isThirdPartyMonitor(
  m: { type?: string; vendor?: boolean },
  _groups?: unknown,
): boolean {
  return m.vendor === true || m.type === 'vendor'
}

/** Your own monitors: everything except 3rd-party status feeds. */
export function ownMonitors<T extends { type?: string; vendor?: boolean }>(monitors: Iterable<T>): T[] {
  const out: T[] = []
  for (const m of monitors) if (!isThirdPartyMonitor(m)) out.push(m)
  return out
}

