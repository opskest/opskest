// The status page's two secondary routes, both public like the page itself:
//   /status/:slug/history        every incident linked to the page, by month
//   /status/:slug/incidents/:id  one incident's full public timeline (permalink)
// They load the page body once for its title and branding (served from the
// server's per-page cache), then their own endpoint.

import { Link } from 'wouter'
import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { IMPACT_LABEL, INCIDENT_STATUS, IncidentCard, IncidentSteps, StatusTag, UpdateBox, impactTone } from '../components/status/IncidentCards'
import { AnnouncementBanner, STATUS_WIDTH, StatusFooter, StatusHeader, StatusMessage, StatusShell } from '../components/status/StatusChrome'
import { Calendar, longDay, monthOfKey, monthTitle, type DayMark, type MonthRef } from '../components/status/Calendar'
import { dateTimeIn, useDisplayZone, ymdIn, ZoneSwitch, type DisplayZone } from '../components/status/zone'
import { ExpandContext, ExpandSetContext, useExpansionState } from '../components/status/Fold'
import { Pager, ShowMoreButton, usePaging, useShowMore } from '../components/Pager'
import { Badge, Button, ErrorRetry, FullScreenSpinner, Icon } from '../components/ui'
import { api, ApiError, errorText } from '../lib/api'
import { duration } from '../lib/format'
import { normIncident, normPublicPage } from '../lib/normalize'
import { normPublicExtras, type PublicExtras } from '../lib/pageSettings'
import type { Incident, PublicPage } from '../lib/types'

type Meta = { state: 'loading' } | { state: 'ready'; page: PublicPage['page']; extra: PublicExtras } | { state: 'notfound' } | { state: 'error'; message: string }

function usePageMeta(slug: string, retry: number): Meta {
  const [meta, setMeta] = useState<Meta>({ state: 'loading' })
  useEffect(() => {
    let alive = true
    api.get<unknown>(`/api/public/pages/${encodeURIComponent(slug)}`)
      .then(d => { if (alive) setMeta({ state: 'ready', page: normPublicPage(d).page, extra: normPublicExtras(d) }) })
      .catch(err => {
        if (!alive) return
        setMeta(err instanceof ApiError && err.status === 404 ? { state: 'notfound' } : { state: 'error', message: errorText(err, 'Could not load this page.') })
      })
    return () => { alive = false }
  }, [slug, retry])
  useEffect(() => {
    if (meta.state !== 'ready') return
    const prev = document.title
    document.title = `${meta.page.title} · Status`
    return () => { document.title = prev }
  }, [meta])
  return meta
}

function Frame({ slug, meta, children, canonical = true }: { slug: string; meta: Extract<Meta, { state: 'ready' }>; children: ReactNode; canonical?: boolean }) {
  const { page, extra } = meta
  const { settings } = extra
  const isPublic = page.visibility === 'public'
  const zone = useDisplayZone()
  const [expansion, setAllOpen] = useExpansionState(settings.defaultExpanded)
  return (
    <StatusShell branding={page.branding} settings={settings} isPublic={isPublic} feedAvailable={extra.feedAvailable} canonical={canonical} slug={slug}>
      <a href="#main" className="skip-link">Skip to content</a>
      <StatusHeader slug={slug} title={page.title} branding={page.branding} settings={settings} isPublic={isPublic} feedAvailable={extra.feedAvailable} layout={page.layout} />
      <main id="main" className={`flex-1 w-full ${STATUS_WIDTH} mx-auto px-4 sm:px-6 py-6 sm:py-10 flex flex-col gap-7 break-words`}>
        <Link href={`/status/${encodeURIComponent(slug)}${page.layout === 'board' ? '/board' : ''}`} className="inline-flex gap-1.5 items-center self-start text-[13px] font-medium text-accent hover:underline"><Icon.Chevron className="w-3.5 h-3.5 rotate-180" />{page.layout === 'board' ? 'Back to the wallboard' : `Current service health for ${page.title}`}</Link>
        <AnnouncementBanner banner={settings.banner} />
        <ZoneContext.Provider value={zone}><ExpandContext.Provider value={expansion}><ExpandSetContext.Provider value={setAllOpen}>{children}</ExpandSetContext.Provider></ExpandContext.Provider></ZoneContext.Provider>
      </main>
      <StatusFooter branding={page.branding} settings={settings} zone={zone} />
    </StatusShell>
  )
}

const ZoneContext = createContext<DisplayZone | null>(null)
function useZone(): DisplayZone {
  const z = useContext(ZoneContext)
  if (!z) throw new Error('useZone outside a status frame')
  return z
}

function Gate({ meta, onRetry, children }: { meta: Meta; onRetry: () => void; children: (m: Extract<Meta, { state: 'ready' }>) => ReactNode }) {
  if (meta.state === 'loading') return <FullScreenSpinner />
  if (meta.state === 'notfound') return <StatusMessage title="Page not found">There is no status page at this address.</StatusMessage>
  if (meta.state === 'error') return <StatusMessage title="Something went wrong" action={<Button onClick={onRetry}>Try again</Button>}>{meta.message}</StatusMessage>
  return <>{children(meta)}</>
}

export function StatusHistory({ slug }: { slug: string }) {
  const [retry, setRetry] = useState(0)
  const meta = usePageMeta(slug, retry)
  return (
    <Gate meta={meta} onRetry={() => setRetry(n => n + 1)}>
      {m => <Frame slug={slug} meta={m}><HistoryBody slug={slug} /></Frame>}
    </Gate>
  )
}

const TONE_RANK = { accent: 0, warn: 1, danger: 2 } as const
function markTone(impact: string): DayMark['tone'] {
  const t = impactTone(impact)
  return t === 'danger' ? 'danger' : t === 'accent' ? 'accent' : 'warn'
}

/** Incident history: a month calendar whose days are shaded by how many
 * incidents started on them, and the incidents below it (a day's, once one is
 * chosen). Only public incident fields are ever loaded. */
function HistoryBody({ slug }: { slug: string }) {
  const zone = useZone()
  const expansion = useContext(ExpandContext)
  const setAllOpen = useContext(ExpandSetContext)
  const { tz } = zone
  const [items, setItems] = useState<Incident[] | null>(null)
  const [next, setNext] = useState<{ ts: number; id: number } | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const today = ymdIn(Date.now(), tz)
  const [month, setMonth] = useState<MonthRef | null>(null)
  const [day, setDay] = useState<string | null>(null)
  const shownMonth = month ?? monthOfKey(today)

  async function load(before: { ts: number; id: number } | null) {
    setBusy(true)
    setErr(null)
    try {
      const q = before ? `?before=${before.ts}&beforeId=${before.id}&limit=50` : '?limit=50'
      const d = await api.get<{ incidents: unknown[]; nextBefore: number | null; nextBeforeId: number | null }>(`/api/public/pages/${encodeURIComponent(slug)}/incidents${q}`)
      const list = (d.incidents ?? []).map(normIncident)
      setItems(cur => (before && cur ? [...cur, ...list] : list))
      setNext(d.nextBefore != null && d.nextBeforeId != null ? { ts: d.nextBefore, id: d.nextBeforeId } : null)
    } catch (e) {
      setErr(errorText(e, 'Could not load incident history.'))
    } finally {
      setBusy(false)
    }
  }
  useEffect(() => { void load(null) }, [slug]) // eslint-disable-line react-hooks/exhaustive-deps

  // Going back in the calendar loads older incidents until the month is covered.
  const monthStart = `${shownMonth.y}-${String(shownMonth.m).padStart(2, '0')}-01`
  const oldestKey = items && items.length > 0 ? ymdIn(items[items.length - 1].createdAt, tz) : null
  useEffect(() => {
    if (items && next && !busy && !err && oldestKey != null && oldestKey > monthStart) void load(next)
  }, [items, next, busy, err, oldestKey, monthStart]) // eslint-disable-line react-hooks/exhaustive-deps

  const byDay = useMemo(() => {
    const out = new Map<string, Incident[]>()
    for (const i of items ?? []) {
      const k = ymdIn(i.createdAt, tz)
      out.set(k, [...(out.get(k) ?? []), i])
    }
    return out
  }, [items, tz])
  const marks = useMemo(() => {
    const out = new Map<string, DayMark>()
    for (const [k, list] of byDay) {
      let tone: DayMark['tone'] = 'accent'
      for (const i of list) { const t = markTone(i.impact); if (TONE_RANK[t] > TONE_RANK[tone]) tone = t }
      out.set(k, { count: list.length, tone: list.every(i => impactTone(i.impact) === 'neutral') ? 'warn' : tone })
    }
    return out
  }, [byDay])
  const inMonth = useMemo(() => {
    const prefix = monthStart.slice(0, 7)
    let n = 0
    for (const [k, list] of byDay) if (k.startsWith(prefix)) n += list.length
    return n
  }, [byDay, monthStart])

  const paging = usePaging(items ?? [], { pageSize: 5, resetKey: slug })
  const months = useMemo(() => {
    const out: { label: string; items: Incident[] }[] = []
    for (const i of paging.rows) {
      const label = monthTitle(monthOfKey(ymdIn(i.createdAt, tz)))
      if (out.length === 0 || out[out.length - 1].label !== label) out.push({ label, items: [] })
      out[out.length - 1].items.push(i)
    }
    return out
  }, [paging.rows, tz])
  const dayItems = day ? byDay.get(day) ?? [] : []
  const dayMore = useShowMore(dayItems, 10, 10, day)
  const href = (i: Incident) => `/status/${encodeURIComponent(slug)}/incidents/${i.id}`

  return (
    <>
      <div><h1 className="text-[24px] sm:text-[28px] leading-tight font-semibold tracking-tight text-body">Incident history</h1><p className="mt-1.5 text-[14px] text-muted max-w-[65ch]">Service disruptions and the updates shared by the team.</p></div>
      {items === null && !err && <p role="status" className="text-[13px] text-muted">Loading incidents…</p>}
      {err && <ErrorRetry message={err} onRetry={() => load(items && next ? next : null)} />}
      {items && (
        <div className="grid lg:grid-cols-[minmax(0,1fr)_320px] gap-x-8 gap-y-5 items-start">
          <div className="min-w-0 flex flex-col gap-5 order-2 lg:order-1">
            <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
              <p role="status" aria-live="polite" className="text-[14px] text-body-2">
                {day
                  ? <>{dayItems.length === 0 ? 'No incidents reported on' : `${dayItems.length} ${dayItems.length === 1 ? 'incident' : 'incidents'} on`} <span className="font-medium text-body">{longDay(day)}</span></>
                  : <>{inMonth === 0 ? 'No incidents reported in' : `${inMonth} ${inMonth === 1 ? 'incident' : 'incidents'} in`} <span className="font-medium text-body">{monthTitle(shownMonth)}</span></>}
                {busy && ' · loading older incidents…'}
              </p>
              <div className="flex flex-wrap gap-2">
                {day && <Button size="sm" onClick={() => setDay(null)}>Clear day filter</Button>}
                <Button size="sm" variant="ghost" onClick={() => setAllOpen(!expansion.open)}>{expansion.open ? 'Collapse all' : 'Expand all'}</Button>
              </div>
            </div>
            {items.length === 0 && (
              <p className="flex items-center gap-2 text-[13px] text-muted border-y border-default py-6">
                <Icon.Check className="w-4 h-4 text-ok shrink-0" /> No incidents have been reported on this page.
              </p>
            )}
            {day ? (
              <div className="flex flex-col">
                {dayMore.shown.map(i => <IncidentCard key={i.id} incident={i} past={i.status === 'resolved'} href={href(i)} tz={tz} />)}
                <ShowMoreButton hidden={dayMore.hidden} step={dayMore.step} onClick={dayMore.more} noun="incidents" />
              </div>
            ) : months.map(mo => (
              <section key={mo.label} className="grid sm:grid-cols-[140px_minmax(0,1fr)] gap-x-5 gap-y-2 border-t border-default pt-5">
                <div><h2 className="text-[16px] font-semibold text-body">{mo.label}</h2><p className="mt-1 text-[12px] text-muted tnum">{mo.items.length} reported {mo.items.length === 1 ? 'incident' : 'incidents'}</p></div>
                <div className="min-w-0 flex flex-col gap-1">{mo.items.map(i => <IncidentCard key={i.id} incident={i} past={i.status === 'resolved'} href={href(i)} tz={tz} />)}</div>
              </section>
            ))}
            {!day && items.length > 0 && <Pager paging={paging} noun="Incidents" sizes={HISTORY_SIZES} hasMore={next != null} onLoadMore={() => load(next)} loadingMore={busy} />}
          </div>
          <aside aria-label="Incident calendar" className="order-1 lg:order-2 lg:sticky lg:top-20 flex flex-col gap-3">
            <div className="rounded-[14px] border border-default bg-surface p-4">
              <Calendar
                month={shownMonth}
                onMonth={m => { setMonth(m); setDay(null) }}
                today={today}
                marks={marks}
                selected={day}
                onSelect={setDay}
                label="Incident calendar"
                maxMonth={monthOfKey(today)}
              />
              <ul aria-label="Calendar key" className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-muted">
                <li className="inline-flex items-center gap-1.5"><span aria-hidden="true" className="inline-block w-4 h-[3px] rounded-full bg-danger" />Major impact</li>
                <li className="inline-flex items-center gap-1.5"><span aria-hidden="true" className="inline-block w-4 h-[3px] rounded-full bg-warn" />Minor impact</li>
                <li className="inline-flex items-center gap-1.5"><span aria-hidden="true" className="inline-block w-4 h-[3px] rounded-full bg-accent" />Maintenance</li>
              </ul>
              <p className="mt-2 text-[12px] text-muted">Shaded days have incidents. Select a day to see only those.</p>
            </div>
            <ZoneSwitch zone={zone} />
          </aside>
        </div>
      )}
    </>
  )
}

const HISTORY_SIZES = [5, 10, 25, 50] as const

export function StatusIncident({ slug, id }: { slug: string; id: string }) {
  const [retry, setRetry] = useState(0)
  const meta = usePageMeta(slug, retry)
  return (
    <Gate meta={meta} onRetry={() => setRetry(n => n + 1)}>
      {m => <Frame slug={slug} meta={m}><IncidentBody slug={slug} id={id} /></Frame>}
    </Gate>
  )
}

function IncidentBody({ slug, id }: { slug: string; id: string }) {
  const [inc, setInc] = useState<Incident | null | 'notfound'>(null)
  const [incidentError, setIncidentError] = useState<string | null>(null)
  const [incidentRetry, setIncidentRetry] = useState(0)

  useEffect(() => {
    let alive = true
    setInc(null)
    setIncidentError(null)
    api.get<unknown>(`/api/public/pages/${encodeURIComponent(slug)}/incidents/${encodeURIComponent(id)}`)
      .then(d => { if (alive) setInc(normIncident(d)) })
      .catch(err => {
        if (!alive) return
        if (err instanceof ApiError && err.status === 404) setInc('notfound')
        else setIncidentError(errorText(err, 'Could not load this incident.'))
      })
    return () => { alive = false }
  }, [slug, id, incidentRetry])

  return incidentError ? <ErrorRetry message={incidentError} onRetry={() => setIncidentRetry(n => n + 1)} />
    : inc === null ? <p role="status" className="text-[13px] text-muted">Loading incident…</p>
    : inc === 'notfound' ? <p className="text-[14px] text-muted">This incident isn’t on this status page.</p>
    : <IncidentDetail inc={inc} />
}

function IncidentDetail({ inc }: { inc: Incident }) {
  const { tz } = useZone()
  const timeline = useMemo(() => [...inc.updates].sort((a, b) => b.createdAt - a.createdAt), [inc])
  const timelineMore = useShowMore(timeline, 10, 10, inc.id)
  const resolved = inc.status === 'resolved' && inc.resolvedAt
  return (
    <article className="flex flex-col gap-7">
      <header className={`flex flex-col gap-3 rounded-[14px] border p-5 ${inc.status === 'resolved' ? 'border-default bg-surface' : impactTone(inc.impact) === 'danger' ? 'border-danger/40 bg-danger-bg/60' : impactTone(inc.impact) === 'accent' ? 'border-accent-border bg-accent-subtle/60' : 'border-warn/40 bg-warn-bg/60'}`}>
        <div className="flex flex-wrap items-center justify-between gap-2">
          {inc.impact ? <Badge tone={impactTone(inc.impact)}>{IMPACT_LABEL[inc.impact] ?? inc.impact} impact</Badge> : <span />}
          <StatusTag tone={inc.status === 'resolved' ? 'ok' : impactTone(inc.impact)}>{INCIDENT_STATUS[inc.status] ?? inc.status}</StatusTag>
        </div>
        <h1 className="text-[28px] sm:text-[34px] leading-tight font-semibold tracking-tight text-body text-balance">{inc.title}</h1>
        <p className="text-[13px] text-muted tnum">
          Started {dateTimeIn(inc.createdAt, tz)}
          {resolved ? ` · resolved ${dateTimeIn(inc.resolvedAt!, tz)} · lasted ${duration(inc.resolvedAt! - inc.createdAt)}` : ` · ongoing for ${duration(Date.now() - inc.createdAt)}`}
        </p>
      </header>
      <section aria-label="Progress" className="rounded-[14px] border border-default bg-surface px-4 sm:px-6 py-5">
        <IncidentSteps status={inc.status} />
      </section>
      <div className="min-w-0 flex flex-col gap-4">
        <h2 className="text-[18px] font-semibold text-body">Updates</h2>
        <ol className="flex flex-col gap-3">
          {timelineMore.shown.map((u, i) => <UpdateBox key={u.id} update={u} when={dateTimeIn(u.createdAt, tz)} latest={i === 0} />)}
        </ol>
        <ShowMoreButton hidden={timelineMore.hidden} step={timelineMore.step} onClick={timelineMore.more} noun="older updates" />
      </div>
      <dl className="rounded-[14px] border border-default bg-surface p-5 grid sm:grid-cols-3 gap-4 text-[13px]">
        <div><dt className="text-muted">Current phase</dt><dd className="mt-1 font-medium">{INCIDENT_STATUS[inc.status] ?? inc.status}</dd></div>
        <div><dt className="text-muted">Impact</dt><dd className="mt-1 font-medium">{IMPACT_LABEL[inc.impact] ?? inc.impact}</dd></div>
        <div><dt className="text-muted">Published updates</dt><dd className="mt-1 tnum">{timeline.length}</dd></div>
      </dl>
    </article>
  )
}

function tokenFromURL(): string {
  return new URLSearchParams(window.location.search).get('token') ?? ''
}

/** /status/:slug/subscribe/confirm?token= — confirms on open (the link in
 * the opt-in email is the user's explicit action). */
export function StatusConfirmSubscription({ slug }: { slug: string }) {
  const [retry, setRetry] = useState(0)
  const meta = usePageMeta(slug, retry)
  const [state, setState] = useState<'working' | 'ok' | 'bad'>('working')
  useEffect(() => {
    let alive = true
    api.post('/api/public/subscriptions/confirm', { token: tokenFromURL() })
      .then(() => { if (alive) setState('ok') })
      .catch(() => { if (alive) setState('bad') })
    return () => { alive = false }
  }, [])
  return (
    <Gate meta={meta} onRetry={() => setRetry(n => n + 1)}>
      {m => (
        <Frame slug={slug} meta={m} canonical={false}>
          <h1 className="text-[27px] leading-tight font-semibold tracking-tight text-body">Email updates</h1>
          {state === 'working' && <p className="text-[14px] text-muted">Confirming…</p>}
          {state === 'ok' && (
            <p className="flex items-start gap-2 text-[14px] text-body">
              <Icon.Check className="w-5 h-5 text-ok shrink-0" />
              You’re subscribed. We’ll email you when an incident on {m.page.title} is posted or updated — every email has an unsubscribe link.
            </p>
          )}
          {state === 'bad' && <p className="text-[14px] text-muted">This confirmation link is invalid or has expired (links work for 48 hours). Subscribe again from the status page.</p>}
        </Frame>
      )}
    </Gate>
  )
}

/** /status/:slug/unsubscribe?token= — unsubscribes on a click, not on
 * open, so mail scanners that prefetch links can't unsubscribe anyone. */
export function StatusUnsubscribe({ slug }: { slug: string }) {
  const [retry, setRetry] = useState(0)
  const meta = usePageMeta(slug, retry)
  const [state, setState] = useState<'ask' | 'busy' | 'ok' | 'bad'>('ask')
  async function go() {
    setState('busy')
    try {
      await api.post('/api/public/subscriptions/unsubscribe', { token: tokenFromURL() })
      setState('ok')
    } catch {
      setState('bad')
    }
  }
  return (
    <Gate meta={meta} onRetry={() => setRetry(n => n + 1)}>
      {m => (
        <Frame slug={slug} meta={m} canonical={false}>
          <h1 className="text-[27px] leading-tight font-semibold tracking-tight text-body">Unsubscribe</h1>
          {(state === 'ask' || state === 'busy') && (
            <>
              <p className="text-[14px] text-muted">Stop getting incident updates for {m.page.title}?</p>
              <Button variant="primary" busy={state === 'busy'} onClick={go} className="self-start">Unsubscribe</Button>
            </>
          )}
          {state === 'ok' && <p className="flex items-start gap-2 text-[14px] text-body"><Icon.Check className="w-5 h-5 text-ok shrink-0" />Done — you won’t get any more updates.</p>}
          {state === 'bad' && <p className="text-[14px] text-muted">This link is invalid or you’re already unsubscribed.</p>}
        </Frame>
      )}
    </Gate>
  )
}
