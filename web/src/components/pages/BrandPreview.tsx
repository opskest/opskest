// A small live preview of a status page's header: brand colour, logo, header
// style, font and corners. Drawn from the draft, so it needs no saved page.
// The brand colour drives accents only, exactly as on the real page.

import type { CSSProperties } from 'react'
import { useResolvedTheme } from '../../hooks/useResolvedTheme'
import { accessibleBrand } from '../../lib/brandContrast'
import { FONTS, type PageSettings } from '../../lib/pageSettings'
import type { PageBranding } from '../../lib/types'

export function BrandPreview({ title, branding, settings }: { title: string; branding: PageBranding; settings: PageSettings }) {
  const dark = useResolvedTheme() === 'dark'
  const brand = accessibleBrand(branding.brandColor, dark)
  const vars = brand ? {
    '--accent': brand.accent, '--accent-fg': brand.foreground,
    '--accent-subtle': `color-mix(in srgb, ${brand.accent} 10%, var(--surface))`,
    '--accent-border': `color-mix(in srgb, ${brand.accent} 35%, var(--surface))`,
  } as CSSProperties : {}
  const radius = settings.corners === 'square' ? 'rounded-none' : 'rounded-[14px]'
  const inner = settings.corners === 'square' ? 'rounded-none' : 'rounded-[10px]'
  const head = settings.headerStyle === 'solid' ? 'bg-accent text-accent-fg' : settings.headerStyle === 'tinted' ? 'bg-accent-subtle text-body' : 'bg-surface-2 text-body'
  return (
    <div role="img" aria-label="Preview of the page header" style={{ ...vars, fontFamily: FONTS.find(f => f.value === settings.font)?.stack }}
      className={`overflow-hidden border border-default bg-ground ${radius}`}>
      <div className={`flex h-12 items-center gap-2.5 px-4 ${head}`}>
        {branding.logoUrl && /^https:\/\//i.test(branding.logoUrl) && <img src={branding.logoUrl} alt="" referrerPolicy="no-referrer" className="h-6 w-6 shrink-0 object-contain" />}
        <span className="min-w-0 flex-1 truncate text-[13px] font-semibold">{title || 'Your status page'}</span>
        <span className={`shrink-0 px-2.5 py-1 text-[11px] font-medium ${inner} ${settings.headerStyle === 'solid' ? 'bg-accent-fg text-accent' : 'bg-accent text-accent-fg'}`}>Subscribe</span>
      </div>
      <div className="flex flex-col gap-2.5 p-4">
        <div className={`flex items-center gap-2 border border-default bg-surface px-3 py-2.5 text-[12px] font-medium text-body ${inner}`}>
          <span aria-hidden="true" className="h-2 w-2 rounded-full bg-ok" />All systems operational
        </div>
        <div className="flex items-center justify-between gap-3 text-[12px]">
          <span className="text-body-2">Example service</span>
          <a className="font-medium text-accent underline-offset-2">Details</a>
        </div>
      </div>
    </div>
  )
}
