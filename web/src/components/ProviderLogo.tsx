import { useState, type CSSProperties } from 'react'
import { Icon } from './icons'
import { BRAND_ASSETS, DARK_PLATE_BRANDS, providerBrand } from '../lib/providers'
export { providerBrand } from '../lib/providers'

/** Up to two initials from a provider name ("Amazon Web Services" -> "AW"). */
export function monogram(name: string): string {
  const words = name.replace(/[^\p{L}\p{N}\s.]+/gu, ' ').split(/[\s.]+/).filter(Boolean)
  const letters = words.length > 1 ? words[0][0] + words[1][0] : (words[0] ?? '').slice(0, 2)
  return letters.toUpperCase()
}

/** A stable hue per name (FNV-1a), so each fallback tile keeps its own tint in every render. */
export function hue(name: string): number {
  let h = 2166136261
  for (let i = 0; i < name.length; i++) h = Math.imul(h ^ name.charCodeAt(i), 16777619)
  return (h >>> 0) % 360
}

/**
 * Local brand assets keep their colors in either theme on a white plate; pale
 * marks (DARK_PLATE_BRANDS) get a dark plate instead so they stay legible. Both
 * plates carry a hairline ring so they read against light and dark cards. The
 * mark always occupies a 24px box (32px tile with 4px padding, 40px tile with
 * 8px), so every logo has the same optical size. A provider with no asset, or an
 * asset that fails to load, gets a deterministic monogram tile (or a globe when
 * there is no name to derive one from).
 */
export default function ProviderLogo({ url, name, size = 'md', className = '' }: {
  url: string; name?: string; size?: 'sm' | 'md'; className?: string
}) {
  const brand = providerBrand(url)
  const ext = brand ? BRAND_ASSETS[brand] : undefined
  const [failed, setFailed] = useState<string | null>(null)
  const showImage = brand != null && ext != null && failed !== brand
  const dim = size === 'sm' ? 'w-8 h-8' : 'w-10 h-10'
  const base = `inline-flex items-center justify-center shrink-0 rounded-lg ${dim} ${className}`
  if (showImage) {
    return (
      <span aria-hidden="true" data-provider-logo={brand} className={`${base} ${size === 'sm' ? 'p-1' : 'p-2'} ${DARK_PLATE_BRANDS.has(brand) ? 'bg-[#1f1f1f] ring-1 ring-inset ring-white/15' : 'bg-white ring-1 ring-inset ring-black/10'}`}>
        <img src={`/providers/${brand}.${ext}`} alt="" width="24" height="24" className="block w-full h-full object-contain" draggable={false} onError={() => setFailed(brand)} />
      </span>
    )
  }
  const label = name ? monogram(name) : ''
  if (!label) {
    return <span aria-hidden="true" data-provider-logo={brand ?? 'unknown'} className={`${base} bg-surface-2 text-muted`}><Icon.Globe className="w-6 h-6" /></span>
  }
  const h = hue(name!)
  return (
    <span aria-hidden="true" data-provider-logo={brand ?? 'monogram'} style={{ '--mh': h } as CSSProperties} className={`${base} font-semibold tracking-tight select-none ring-1 ring-inset ring-black/5 dark:ring-white/10 bg-[hsl(var(--mh)_38%_92%)] text-[hsl(var(--mh)_45%_28%)] dark:bg-[hsl(var(--mh)_26%_24%)] dark:text-[hsl(var(--mh)_60%_85%)] ${size === 'sm' ? 'text-[11px]' : 'text-[13px]'}`}>
      {label}
    </span>
  )
}
