import { useEffect, useMemo, useState } from 'react'
import { Link, useLocation } from 'wouter'
import { PageTitle } from '../components/PageShell'
import { Pager, usePaging } from '../components/Pager'
import { Badge, Button, ConfirmModal, DropdownMenu, EmptyState, ErrorRetry, Icon, Input, SegmentedControl, Select, SkeletonCards, SkeletonRows, useToast, type MenuEntry } from '../components/ui'
import { api, errorText, unwrapList } from '../lib/api'
import { useLive } from '../lib/live'
import { normPage } from '../lib/normalize'
import type { Page } from '../lib/types'

type Filter = 'all' | 'public' | 'private' | 'board' | 'list'
type SortKey = 'name' | 'updated' | 'size'
type View = 'grid' | 'list'

const VIEW_KEY = 'opskest-pages-view'
const LIST_COLS = 'md:grid-cols-[minmax(0,1.6fr)_110px_120px_80px_36px]'
const CHIPS: { key: Filter; label: string }[] = [
  { key: 'all', label: 'All' }, { key: 'public', label: 'Public' }, { key: 'private', label: 'Private' },
  { key: 'board', label: 'Board' }, { key: 'list', label: 'List' },
]

function pagePath(p: Pick<Page, 'slug' | 'layout'>) {
  return `/status/${encodeURIComponent(p.slug)}${p.layout === 'board' ? '/board' : ''}`
}

function readView(): View {
  try { return localStorage.getItem(VIEW_KEY) === 'list' ? 'list' : 'grid' } catch { return 'grid' }
}

function ViewLabel({ icon, text }: { icon: 'Grid' | 'List'; text: string }) {
  const Glyph = Icon[icon]
  return <span className="flex items-center gap-1.5"><Glyph className="w-4 h-4" /><span className="sr-only sm:not-sr-only">{text}</span></span>
}

function Logo({ p, size }: { p: Page; size: 'lg' | 'sm' }) {
  return (
    <span className={`flex shrink-0 items-center justify-center bg-surface-2 text-muted ${size === 'lg' ? 'mt-0.5 h-9 w-9 rounded-[10px]' : 'h-8 w-8 rounded-lg'}`}>
      {p.branding.logoUrl
        ? <img src={p.branding.logoUrl} alt="" referrerPolicy="no-referrer" className="max-h-6 max-w-6 object-contain" />
        : <Icon.Globe className="w-[18px] h-[18px]" />}
    </span>
  )
}

export default function PagesAdmin() {
  const [, navigate] = useLocation()
  const { push } = useToast()
  const live = useLive()
  const [pages, setPages] = useState<Page[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [nonce, setNonce] = useState(0)
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<Filter>('all')
  const [sort, setSort] = useState<SortKey>('name')
  const [view, setViewState] = useState<View>(readView)
  const [doomed, setDoomed] = useState<Page | null>(null)
  const setView = (v: View) => { setViewState(v); try { localStorage.setItem(VIEW_KEY, v) } catch { /* private mode */ } }

  useEffect(() => {
    let alive = true
    setError(null)
    api.get<unknown>('/api/pages')
      .then(data => { if (alive) setPages(unwrapList(data, 'pages').map(normPage)) })
      .catch(err => { if (alive) setError(errorText(err, 'Could not load status pages.')) })
    return () => { alive = false }
  }, [nonce])

  // Unique services per page, including those expanded from a service group.
  const counts = useMemo(() => {
    const out = new Map<number, number>()
    for (const p of pages ?? []) {
      const ids = new Set<number>()
      for (const item of p.items) {
        if (item.monitorId != null) ids.add(item.monitorId)
        if (item.groupId != null) for (const m of live.monitors.values()) if (m.groupId === item.groupId) ids.add(m.id)
      }
      out.set(p.id, ids.size)
    }
    return out
  }, [pages, live.monitors])

  const tally = useMemo(() => {
    const t: Record<Filter, number> = { all: 0, public: 0, private: 0, board: 0, list: 0 }
    for (const p of pages ?? []) { t.all++; t[p.visibility === 'public' ? 'public' : 'private']++; t[p.layout === 'board' ? 'board' : 'list']++ }
    return t
  }, [pages])

  const rows = useMemo(() => {
    const q = query.trim().toLowerCase()
    const n = (p: Page) => counts.get(p.id) ?? 0
    return (pages ?? [])
      .filter(p => (filter === 'all' || p.visibility === filter || p.layout === filter) && (!q || `${p.title} ${p.slug} ${p.description}`.toLowerCase().includes(q)))
      .sort((a, b) => sort === 'updated' ? b.updatedAt - a.updatedAt || a.title.localeCompare(b.title)
        : sort === 'size' ? n(b) - n(a) || a.title.localeCompare(b.title)
        : (a.title || a.slug).localeCompare(b.title || b.slug))
  }, [pages, counts, query, filter, sort])
  const paging = usePaging(rows, { resetKey: query + filter + sort })

  async function copy(p: Page) {
    const url = `${window.location.origin}${pagePath(p)}`
    try { await navigator.clipboard.writeText(url); push('Status page link copied.', 'ok') }
    catch { push(`Could not copy the link. ${url}`, 'danger') }
  }

  async function remove(p: Page) {
    try {
      await api.del(`/api/pages/${p.id}`)
      push('Status page deleted.', 'ok')
      setPages(list => (list ?? []).filter(x => x.id !== p.id))
    } catch (err) {
      push(errorText(err, 'Could not delete this page.'), 'danger')
    } finally {
      setDoomed(null)
    }
  }

  const menu = (p: Page): MenuEntry[] => [
    { key: 'open', label: 'Open page', icon: <Icon.External className="w-4 h-4" />, onSelect: () => { window.open(pagePath(p), '_blank', 'noopener,noreferrer') } },
    { key: 'edit', label: 'Edit', icon: <Icon.Edit className="w-4 h-4" />, onSelect: () => navigate(`/pages/${p.id}`) },
    { key: 'copy', label: 'Copy link', icon: <Icon.Copy className="w-4 h-4" />, onSelect: () => { void copy(p) } },
    { key: 'sep', separator: true },
    { key: 'delete', label: 'Delete', icon: <Icon.Trash className="w-4 h-4" />, tone: 'danger', onSelect: () => setDoomed(p) },
  ]

  const audience = (p: Page) => <Badge tone={p.visibility === 'public' ? 'accent' : 'neutral'}>{p.visibility === 'public' ? 'Public' : 'Private'}</Badge>
  const typeLabel = (p: Page) => (p.layout === 'board' ? 'Wallboard' : 'Status page')
  const address = (p: Page) => (
    <a href={pagePath(p)} target="_blank" rel="noopener noreferrer" className="relative z-10 inline-flex items-center gap-1.5 max-w-full text-xs text-muted hover:text-accent">
      <span className="truncate">/status/{p.slug}{p.layout === 'board' ? '/board' : ''}</span><Icon.External className="w-3 h-3 shrink-0" />
    </a>
  )
  const serviceText = (p: Page) => live.connected ? `${counts.get(p.id) ?? 0}` : '—'
  const filtered = query.trim() !== '' || filter !== 'all'
  const loading = pages == null && !error

  return <div>
    <PageTitle title="Status pages" actions={<Button variant="primary" icon={<Icon.Plus className="w-4 h-4" />} onClick={() => navigate('/pages/new')}>Create status page</Button>}>
      Manage the public and private places where people follow your services.{pages != null && pages.length > 0 && ` ${pages.length} ${pages.length === 1 ? 'page' : 'pages'}, ${tally.public} public.`}
    </PageTitle>
    {error && <ErrorRetry message={error} onRetry={() => setNonce(n => n + 1)} />}

    {pages != null && pages.length > 0 && <div className="mb-5 space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative w-full sm:w-72"><Icon.Search className="absolute left-3 top-3 w-4 h-4 text-muted" /><Input type="search" aria-label="Search status pages" className="w-full pl-9" placeholder="Find a page…" value={query} onChange={e => setQuery(e.target.value)} /></div>
        <div className="flex items-center gap-3 sm:ml-auto">
          <label className="flex items-center gap-2 text-xs text-muted">Sort
            <Select aria-label="Sort status pages" className="w-40" value={sort} onChange={e => setSort(e.target.value as SortKey)}>
              <option value="name">Name</option><option value="updated">Recently updated</option><option value="size">Most services</option>
            </Select>
          </label>
          <SegmentedControl<View> label="View" options={[{ key: 'grid', label: <ViewLabel icon="Grid" text="Grid" /> }, { key: 'list', label: <ViewLabel icon="List" text="List" /> }]} value={view} onChange={setView} />
        </div>
      </div>
      <div className="flex flex-wrap gap-2" role="group" aria-label="Filter status pages">
        {CHIPS.filter(c => c.key === 'all' || tally[c.key] > 0 || filter === c.key).map(c => (
          <button key={c.key} type="button" aria-pressed={filter === c.key} onClick={() => setFilter(c.key)}
            className={'inline-flex items-center gap-2 h-8 rounded-full border px-3 text-[13px] transition-colors ' + (filter === c.key ? 'border-accent bg-accent-subtle text-body' : 'border-default text-muted hover:text-body hover:border-strong')}>
            {c.label}<span className="tnum text-xs text-muted">{tally[c.key]}</span>
          </button>
        ))}
      </div>
    </div>}

    {loading ? (view === 'grid' ? <SkeletonCards count={6} label="Loading status pages" className="grid sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4 gap-4" /> : <SkeletonRows rows={6} label="Loading status pages" />)
      : pages?.length === 0 ? <EmptyState title="No status pages" action={<Button variant="primary" onClick={() => navigate('/pages/new')}>Create status page</Button>}>
        Choose the services to share, add your branding and publish a link. Use a public page for customers or a private page for your team.
      </EmptyState>
      : pages != null && rows.length === 0 ? <EmptyState title="No matching status pages" action={<Button onClick={() => { setQuery(''); setFilter('all') }}>Clear filters</Button>}>{filtered ? 'Try another name or clear the filter.' : ''}</EmptyState>
      : pages != null && <>
        {view === 'grid' ? (
          <ul className="grid sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4 gap-4">
            {paging.rows.map(p => (
              <li key={p.id} className="group relative rounded-[14px] border border-default bg-surface p-5 flex flex-col gap-4 min-h-[176px] transition-[border-color,box-shadow] hover:border-strong hover:shadow-md">
                <div className="flex items-start gap-3">
                  <Logo p={p} size="lg" />
                  <div className="min-w-0 flex-1">
                    <h3 className="font-semibold text-[15px] leading-snug break-words">
                      <Link href={`/pages/${p.id}`} className="after:absolute after:inset-0 after:content-['']">{p.title || p.slug}</Link>
                    </h3>
                    <p className="mt-1 text-[13px] text-muted leading-5 line-clamp-2">{p.description || 'Service health and incident updates.'}</p>
                  </div>
                  <div className="relative z-10 -mr-2 -mt-1"><DropdownMenu label={`Actions for ${p.title || p.slug}`} items={menu(p)} /></div>
                </div>
                <div className="mt-auto space-y-3">
                  {address(p)}
                  <div className="flex items-center justify-between gap-3">
                    {audience(p)}
                    <span className="text-xs text-muted tnum">{typeLabel(p)} · {serviceText(p)} {counts.get(p.id) === 1 && live.connected ? 'service' : 'services'}</span>
                  </div>
                </div>
              </li>
            ))}
          </ul>
        ) : (
          <div className="rounded-[14px] border border-default bg-surface overflow-hidden">
            <div className={`hidden md:grid ${LIST_COLS} items-center gap-5 px-5 py-2.5 text-[11px] font-medium uppercase tracking-wide text-faint border-b border-default`}>
              <span>Page</span><span>Audience</span><span>Type</span><span className="text-right">Services</span><span className="sr-only">Actions</span>
            </div>
            <ul className="divide-y divide-default">
              {paging.rows.map(p => (
                <li key={p.id} className={`group relative grid ${LIST_COLS} items-center gap-x-5 gap-y-2 px-5 py-3.5 hover:bg-surface-hover transition-colors grid-cols-[minmax(0,1fr)_auto]`}>
                  <div className="flex items-center gap-3 min-w-0">
                    <Logo p={p} size="sm" />
                    <div className="min-w-0">
                      <h3 className="text-sm font-semibold truncate group-hover:text-accent">
                        <Link href={`/pages/${p.id}`} className="after:absolute after:inset-0 after:content-['']">{p.title || p.slug}</Link>
                      </h3>
                      {address(p)}
                    </div>
                  </div>
                  <div className="hidden md:block">{audience(p)}</div>
                  <span className="hidden md:block text-sm text-muted">{typeLabel(p)}</span>
                  <span className="hidden md:block text-sm text-muted tnum text-right">{serviceText(p)}</span>
                  <div className="relative z-10 flex items-center gap-3 justify-self-end">
                    <span className="md:hidden flex items-center gap-2">{audience(p)}</span>
                    <DropdownMenu label={`Actions for ${p.title || p.slug}`} items={menu(p)} />
                  </div>
                </li>
              ))}
            </ul>
          </div>
        )}
        <Pager paging={paging} noun="Pages" />
        <p className="mt-4 text-[12px] text-muted">Public pages hide check targets and error details. Private pages require an Opskest account.</p>
      </>}

    <ConfirmModal
      open={doomed != null}
      title={`Delete ${doomed?.title || doomed?.slug || 'this page'}?`}
      description="Its public address stops working and its subscribers are removed. This cannot be undone."
      confirmLabel="Delete page"
      onConfirm={() => (doomed ? remove(doomed) : undefined)}
      onClose={() => setDoomed(null)}
    />
  </div>
}
