// "Embed" box for a published status page: pick a badge (page status, one
// service's status, or one service's uptime), preview it on light and dark
// backgrounds, and copy the image URL or a Markdown / HTML snippet.
//
// Badges are served by /api/public/pages/{slug}/badge.svg and
// /services/{id}/(badge|uptime).svg, for public pages only. They read the
// saved page, so everything here is built from the saved slug and items.

import { useMemo, useState } from 'react'
import type { MonitorSummary } from '../../lib/types'
import { Alert, Field, Select } from '../ui'
import { CopyRow } from '../detail/PushCard'

export interface SavedItem {
  groupId: number | null
  monitorId: number | null
  displayName: string
}

type Kind = 'page' | 'status' | 'uptime'
const WINDOWS = ['24h', '7d', '30d', '90d'] as const
const MAX_SERVICES = 300

/** Services on the saved page, as the public page lists them: a monitor
 *  listed on its own keeps its display name; group members use their own. */
export function badgeServices(items: SavedItem[], monitors: Map<number, MonitorSummary>): { id: number; name: string }[] {
  const out = new Map<number, string>()
  for (const it of items) {
    if (it.groupId == null) continue
    for (const m of monitors.values()) if (m.groupId === it.groupId && !out.has(m.id)) out.set(m.id, m.name)
  }
  for (const it of items) {
    if (it.monitorId == null) continue
    out.set(it.monitorId, it.displayName.trim() || monitors.get(it.monitorId)?.name || `Service ${it.monitorId}`)
  }
  return [...out].map(([id, name]) => ({ id, name })).sort((a, b) => a.name.localeCompare(b.name)).slice(0, MAX_SERVICES)
}

const mdSafe = (s: string) => s.replace(/[[\]()\\<>*_`|]/g, '').replace(/\s+/g, ' ').trim().slice(0, 60)
const attrSafe = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/\s+/g, ' ').trim().slice(0, 60)

export function EmbedBadges({ slug, title, items, monitors, isNew, savedPublic, origin: canonicalOrigin, layout = 'list' }: {
  /** A badge links to the page's own view: the wallboard for a board page. */
  layout?: 'list' | 'board'
  /** The page's saved public address (its canonical origin); this browser's when empty. */
  origin?: string
  slug: string
  title: string
  items: SavedItem[]
  monitors: Map<number, MonitorSummary>
  isNew: boolean
  savedPublic: boolean
}) {
  const [kind, setKind] = useState<Kind>('page')
  const [serviceId, setServiceId] = useState('')
  const [win, setWin] = useState<(typeof WINDOWS)[number]>('30d')
  const services = useMemo(() => badgeServices(items, monitors), [items, monitors])

  if (isNew || !slug) {
    return <Alert tone="accent">Create the page first. Badges are available once it is published.</Alert>
  }
  if (!savedPublic) {
    return <Alert tone="warn">Badges are only served for public status pages. Set access to Public and save, then come back here.</Alert>
  }

  const origin = canonicalOrigin ? canonicalOrigin.replace(/\/+$/, '') : window.location.origin
  const pageUrl = `${origin}/status/${slug}${layout === 'board' ? '/board' : ''}`
  const base = `${origin}/api/public/pages/${encodeURIComponent(slug)}`
  const svc = services.find(s => String(s.id) === serviceId) ?? services[0]
  const needsService = kind !== 'page'

  let img = `${base}/badge.svg`
  const pageName = mdSafe(title) || slug
  let alt = /status$/i.test(pageName) ? pageName : `${pageName} status`
  if (needsService && svc) {
    img = `${base}/services/${svc.id}/${kind === 'uptime' ? `uptime.svg?window=${win}` : 'badge.svg'}`
    alt = kind === 'uptime' ? `${mdSafe(svc.name)} uptime ${win}` : `${mdSafe(svc.name)} status`
  }
  const markdown = `[![${alt}](${img})](${pageUrl})`
  const html = `<a href="${pageUrl}"><img src="${img}" alt="${attrSafe(alt)}"></a>`
  const unavailable = needsService && !svc

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-end gap-4">
        <Field label="Badge" className="w-full sm:w-56">
          {fid => (
            <Select id={fid} value={kind} onChange={e => setKind(e.target.value as Kind)}>
              <option value="page">Page status</option>
              <option value="status">Service status</option>
              <option value="uptime">Service uptime</option>
            </Select>
          )}
        </Field>
        {needsService && (
          <Field label="Service" className="w-full sm:w-64" hint={services.length === 0 ? 'Add services to the page and save first.' : undefined}>
            {fid => (
              <Select id={fid} value={svc ? String(svc.id) : ''} disabled={services.length === 0} onChange={e => setServiceId(e.target.value)}>
                {services.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}
              </Select>
            )}
          </Field>
        )}
        {kind === 'uptime' && (
          <Field label="Window" className="w-32">
            {fid => (
              <Select id={fid} value={win} onChange={e => setWin(e.target.value as (typeof WINDOWS)[number])}>
                {WINDOWS.map(w => <option key={w} value={w}>{w}</option>)}
              </Select>
            )}
          </Field>
        )}
      </div>

      {unavailable ? (
        <p className="text-[13px] text-muted">No saved services on this page yet.</p>
      ) : (
        <>
          <div className="grid grid-cols-2 rounded-[10px] border border-default overflow-hidden" role="group" aria-label="Badge preview">
            {/* The badge draws its own background; these swatches only show it sits well on both. */}
            <div className="bg-white px-4 py-5 flex items-center justify-center min-h-[64px]">
              <img key={`l-${img}`} src={img} alt={alt} height={20} className="h-5 max-w-full" />
            </div>
            <div className="bg-[#0d1117] border-l border-default px-4 py-5 flex items-center justify-center min-h-[64px]">
              <img key={`d-${img}`} src={img} alt="" aria-hidden="true" height={20} className="h-5 max-w-full" />
            </div>
          </div>
          <div className="flex flex-col gap-2.5">
            <CopyRow text={markdown} label="Markdown snippet" />
            <CopyRow text={html} label="HTML snippet" />
            <CopyRow text={img} label="Image URL" />
          </div>
          <p className="text-[12px] text-muted">
            Badges update about once a minute and show only status, uptime and the public service name — never targets, reasons or runbooks.
            They follow the saved page, so save changes first.
          </p>
        </>
      )}
    </div>
  )
}
