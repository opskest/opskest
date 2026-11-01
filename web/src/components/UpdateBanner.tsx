import { useEffect, useState } from 'react'
import { Link } from 'wouter'
import { api } from '../lib/api'
import { isAdmin } from '../lib/roles'
import type { Role } from '../lib/types'
import { bareVersion, normalizePayload, shouldShowBanner, type UpdatesPayload } from '../lib/updates'
import { Icon } from './ui'

// Overview banner for administrators: a newer release is available. Updates
// are admin only, so nothing is requested or shown for anyone else. It is
// fetched once per page load, never blocks the page, and a failure (including
// a 404 or 403 from a server without update support) shows nothing. Dismissal
// is remembered per version, so the next release brings the banner back.

const DISMISSED_KEY = 'opskest-update-dismissed'

function readDismissed(): string | null {
  try { return window.localStorage.getItem(DISMISSED_KEY) } catch { return null }
}
function writeDismissed(version: string) {
  try { window.localStorage.setItem(DISMISSED_KEY, version) } catch { /* storage may be unavailable */ }
}

// One request per page load, even if Overview remounts the banner while it reloads.
let requested = false
let cached: UpdatesPayload | null = null

export function UpdateBanner({ user }: { user: { role: Role } | null | undefined }) {
  const admin = isAdmin(user)
  const [info, setInfo] = useState<UpdatesPayload | null>(cached)
  const [dismissed, setDismissed] = useState<string | null>(readDismissed)

  useEffect(() => {
    if (!admin || requested) return
    requested = true
    api.get<UpdatesPayload>('/api/updates')
      .then(p => {
        if (!p || typeof p !== 'object' || typeof p.current !== 'string') return
        cached = normalizePayload(p)
        setInfo(cached)
      })
      .catch(() => { /* optional feature: stay quiet */ })
  }, [admin])

  if (!admin || !shouldShowBanner(info, dismissed) || !info) return null
  const latest = bareVersion(info.check.latest)

  return (
    <section aria-label="Update available" className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-lg border border-accent-border bg-accent-subtle px-4 py-3">
      <Icon.Refresh className="w-4 h-4 text-accent shrink-0" />
      <div className="min-w-0 flex-1 basis-56">
        <p className="text-sm font-semibold text-body">Update available: v{latest}</p>
        <p className="text-xs text-muted">You are running v{bareVersion(info.current)}.</p>
      </div>
      <div className="flex items-center gap-2">
        <Link
          href="/settings?tab=about#updates"
          aria-label={`View update ${latest}`}
          className="inline-flex items-center justify-center h-9 px-3 text-[12px] font-medium rounded-[10px] border border-transparent bg-accent hover:bg-accent-hover text-accent-fg transition-colors"
        >
          View
        </Link>
        <button
          type="button"
          onClick={() => { writeDismissed(info.check.latest); setDismissed(info.check.latest) }}
          className="inline-flex items-center justify-center h-9 px-3 text-[12px] font-medium rounded-[10px] text-muted hover:text-body hover:bg-surface-hover transition-colors"
          aria-label={`Dismiss the notice about version ${latest}`}
        >
          Dismiss
        </button>
      </div>
    </section>
  )
}
