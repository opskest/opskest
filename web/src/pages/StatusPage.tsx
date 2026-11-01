// Public and private status pages. A page's `layout` is chosen when
// it is created and never changes: a 'list' page is /status/:slug (plus its
// history and incident pages) and has no wallboard; a 'board' page is
// /status/:slug/board and /status/:slug moves there. The other view's address
// is "not found", like an unknown page. Rendered
// outside the signed-in shell — a public page must work for anyone with
// the link.
//
// Data: one GET /api/public/pages/{slug}, then the page's scoped SSE
// stream carrying only its (sanitized) monitors. A private page answers
// 404 to a signed-out visitor, exactly like a missing page, so
// on a 404 this asks /api/me: signed out → "This page is private — Sign
// in"; signed in → "Page not found".

import { useEffect, useMemo, useRef, useState } from 'react'
import { useLocation, useSearch } from 'wouter'
import { Board } from '../components/board/Board'
import { ReconnectingBanner, TopStrip } from '../components/board/TopStrip'
import { ListView, type PageSection } from '../components/status/ListView'
import { overallFrom, StatusFooter, StatusMessage, StatusShell } from '../components/status/StatusChrome'
import { Alert, Button, ErrorRetry, FullScreenSpinner } from '../components/ui'
import { api, ApiError, errorText } from '../lib/api'
import type { BoardGroup } from '../lib/boardLayout'
import { effectiveStatus, totalsByStatus } from '../lib/boardLayout'
import { useLiveScoped } from '../lib/live'
import { normPublicMonitor, normPublicPage } from '../lib/normalize'
import { normPublicExtras, type PublicExtras } from '../lib/pageSettings'
import type { PublicMonitor, PublicPage } from '../lib/types'
import { useCoarseNow, useThemeOverride } from '../hooks/useBoardClock'
import { usePageAlarm } from '../hooks/usePageAlarm'

type Load =
  | { state: 'loading' }
  | { state: 'ready'; data: PublicPage; extra: PublicExtras }
  | { state: 'notfound' }
  | { state: 'private' }
  | { state: 'error'; message: string; status: number }

/** The page's groups in order, plus monitors listed on their own (not in
 * any of the page's groups) as one more section. */
function sectionsOf(data: PublicPage): PageSection[] {
  const inGroup = new Set(data.groups.flatMap(g => g.monitorIds))
  const sections: PageSection[] = data.groups.map((g, i) => ({ key: `g${g.id}-${i}`, anchor: `group-${g.id}`, name: g.name, description: g.description, monitorIds: g.monitorIds }))
  const loose = data.monitors.filter(m => !inGroup.has(m.id)).map(m => m.id)
  if (loose.length > 0) sections.push({ key: 'loose', anchor: 'group-other', name: sections.length > 0 ? 'Other services' : 'Services', monitorIds: loose })
  return sections
}

// Stable normalized objects per raw summary, so memoised cards only
// re-render for monitors that actually changed.
const normCache = new WeakMap<object, PublicMonitor>()
function toPublic(raw: object): PublicMonitor {
  let m = normCache.get(raw)
  if (!m) {
    m = normPublicMonitor(raw)
    normCache.set(raw, m)
  }
  return m
}

function LivePage({ slug, data, extra, board, refreshError, onRefresh }: { slug: string; data: PublicPage; extra: PublicExtras; board: boolean; refreshError: string | null; onRefresh: () => void }) {
  const live = useLiveScoped(`/api/public/pages/${encodeURIComponent(slug)}/stream`)
  const search = useSearch()
  const theme = new URLSearchParams(search).get('theme')
  useThemeOverride(theme === 'dark' || theme === 'light' ? theme : null)

  const initial = useMemo(() => new Map(data.monitors.map(m => [m.id, m])), [data.monitors])
  // Live summaries replace the payload's as they arrive.
  const monitors = useMemo(() => {
    if (live.lastUpdate === 0) return initial as ReadonlyMap<number, PublicMonitor>
    // Snapshots and removals are authoritative, including an empty page.
    // Page metadata limits the stream to the currently published selection.
    const current = new Map<number, PublicMonitor>()
    for (const [id, m] of live.monitors) if (initial.has(id)) current.set(id, toPublic(m))
    return current as ReadonlyMap<number, PublicMonitor>
  }, [live.monitors, live.lastUpdate, initial])

  const sections = useMemo(() => sectionsOf(data), [data])
  const overall = useMemo(() => {
    if (live.lastUpdate === 0) return data.overall
    const onPage = new Set(sections.flatMap(s => s.monitorIds))
    return overallFrom([...monitors.values()].filter(m => onPage.has(m.id)), data.maintenance.active.length > 0)
  }, [live.lastUpdate, monitors, data, sections])

  useEffect(() => {
    const prev = document.title
    document.title = `${data.page.title} · Status`
    return () => { document.title = prev }
  }, [data.page.title])

  const now = useCoarseNow(live.lastUpdate)
  const connected = live.connected
  const isPublic = data.page.visibility === 'public'

  const groups = useMemo<BoardGroup[]>(() => sections.map(s => ({ key: s.key, name: s.name, monitorIds: s.monitorIds })), [sections])
  const counts = useMemo(() => totalsByStatus(monitors.values(), now), [monitors, now])
  const alarmInput = useMemo(() => {
    const onPage = new Set(sections.flatMap(s => s.monitorIds))
    return [...monitors.values()].filter(m => onPage.has(m.id)).map(m => ({ id: m.id, status: effectiveStatus(m, now).status }))
  }, [monitors, sections, now])
  // Only the board plays sound; the list view is for reading.
  const alarm = usePageAlarm(board ? alarmInput : [], board, `page:${slug}`, data.page.alarm)

  if (!board) {
    return (
      <ListView
        slug={slug}
        data={data}
        extra={extra}
        sections={sections}
        monitors={monitors}
        overall={overall}
        connected={connected}
        counts={counts}
        receivedAt={live.lastUpdate}
        refreshWarning={refreshError ? <Alert tone="warn">{refreshError} <button type="button" className="underline" onClick={onRefresh}>Try again</button></Alert> : undefined}
      />
    )
  }
  return (
    <div className="h-[100dvh] flex flex-col overflow-hidden bg-ground text-body">
      <h1 className="sr-only">{data.page.title}</h1>
      <TopStrip
        counts={counts}
        clusters={[]}
        connectivity={{ online: true, since: 0 }}
        receivedAt={live.lastUpdate}
        title={data.page.title}
        alarm={alarm}
      />
      {!connected && live.lastUpdate > 0 && <ReconnectingBanner receivedAt={live.lastUpdate} />}
      {refreshError && <Alert tone="warn">{refreshError} <button type="button" className="underline" onClick={onRefresh}>Try again</button></Alert>}
      <main className="flex-1 min-h-0">
        <Board
          groups={groups}
          monitors={monitors}
          mode={isPublic ? 'public' : 'private'}
          showReason={!isPublic}
          showLatency={isPublic ? data.page.showLatency : true}
          now={now}
        />
      </main>
    </div>
  )
}

export default function StatusPage({ slug, board = false }: { slug: string; board?: boolean }) {
  const [load, setLoad] = useState<Load>({ state: 'loading' })
  const [nonce, setNonce] = useState(0)
  const [refreshError, setRefreshError] = useState<string | null>(null)
  const [, navigate] = useLocation()
  const refresh = useRef<() => void>(() => {})
  // The owner picks how often a visible page re-reads incidents and maintenance.
  const refreshMs = (load.state === 'ready' ? load.extra.settings.refreshSeconds : 60) * 1000

  useEffect(() => {
    if (load.state !== 'ready') return
    let alive = true
    let inFlight = false
    let lastAttempt = 0
    async function update(force = false) {
      if (inFlight || document.visibilityState === 'hidden' || (!force && Date.now() - lastAttempt < 5000)) return
      inFlight = true
      lastAttempt = Date.now()
      try {
        const raw = await api.get<unknown>(`/api/public/pages/${encodeURIComponent(slug)}`)
        if (alive) { setLoad({ state: 'ready', data: normPublicPage(raw), extra: normPublicExtras(raw) }); setRefreshError(null) }
      } catch (err) {
        if (!alive) return
        if (err instanceof ApiError && (err.status === 401 || err.status === 404)) setLoad({ state: 'notfound' })
        else setRefreshError('Incident and maintenance updates could not be refreshed. Showing the last available information.')
      } finally { inFlight = false }
    }
    refresh.current = () => { void update(true) }
    const onVisible = () => { if (document.visibilityState === 'visible') void update() }
    const interval = window.setInterval(() => { void update() }, refreshMs)
    window.addEventListener('focus', onVisible)
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      alive = false
      refresh.current = () => {}
      window.clearInterval(interval)
      window.removeEventListener('focus', onVisible)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [slug, load.state, refreshMs])

  const boardOnly = load.state === 'ready' && !board && load.data.page.layout === 'board'
  useEffect(() => {
    if (boardOnly) navigate(`/status/${encodeURIComponent(slug)}/board`, { replace: true })
  }, [boardOnly, slug, navigate])

  useEffect(() => {
    let alive = true
    setLoad({ state: 'loading' })
    setRefreshError(null)
    api.get<unknown>(`/api/public/pages/${encodeURIComponent(slug)}`)
      .then(raw => { if (alive) setLoad({ state: 'ready', data: normPublicPage(raw), extra: normPublicExtras(raw) }) })
      .catch(async err => {
        if (!alive) return
        if (err instanceof ApiError && err.status === 404) {
          try {
            await fetch('/api/me', { credentials: 'include' }).then(r => { if (!r.ok) throw new ApiError(r.status, r.statusText) })
            if (alive) setLoad({ state: 'notfound' })
          } catch (meErr) {
            if (alive) setLoad(meErr instanceof ApiError && meErr.status === 401 ? { state: 'private' } : { state: 'notfound' })
          }
          return
        }
        setLoad({ state: 'error', message: errorText(err, 'Could not load this page.'), status: err instanceof ApiError ? err.status : 0 })
      })
    return () => { alive = false }
  }, [slug, nonce])

  if (load.state === 'loading') return <FullScreenSpinner />
  if (load.state === 'notfound') return <StatusMessage title="Page not found">There is no status page at this address.</StatusMessage>
  if (load.state === 'private') {
    const next = `${window.location.pathname}${window.location.search}`
    return (
      <StatusMessage
        title="This page is private"
        action={<Button variant="primary" onClick={() => { window.location.href = `/login?next=${encodeURIComponent(next)}` }}>Sign in</Button>}
      >
        Sign in with your Opskest account to view it. If it still isn&apos;t there after you sign in, the link is wrong.
      </StatusMessage>
    )
  }
  if (load.state === 'error') {
    return (
      <StatusShell>
        <main className="flex-1 w-full max-w-4xl mx-auto px-4 sm:px-6 py-10">
          {load.status === 429
            ? <Alert tone="warn">Too many requests — wait a few seconds and reload.</Alert>
            : <ErrorRetry message={load.message} onRetry={() => setNonce(n => n + 1)} />}
        </main>
        <StatusFooter />
      </StatusShell>
    )
  }
  // A page is a status page or a wallboard from creation, never both: the
  // other view's address is not found (list page) or moves here (board page).
  if (load.state === 'ready' && board && load.data.page.layout !== 'board') return <StatusMessage title="Page not found">There is no status page at this address.</StatusMessage>
  if (load.state === 'ready' && !board && load.data.page.layout === 'board') return <FullScreenSpinner />
  return <LivePage key={slug} slug={slug} data={load.data} extra={load.extra} board={board} refreshError={refreshError} onRefresh={() => refresh.current()} />
}
