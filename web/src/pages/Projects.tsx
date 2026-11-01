import { useMemo, useState, type FormEvent } from 'react'
import { Link, useLocation } from 'wouter'
import { PageTitle } from '../components/PageShell'
import { Pager, usePaging } from '../components/Pager'
import { Badge, Button, EmptyState, Field, FilterChips, Icon, Input, Modal, SegmentedControl, Select, SkeletonCards, SkeletonRows } from '../components/ui'
import { LIST_FRAME, LIST_HEAD, LIST_ROW } from '../components/table'
import { api, errorText } from '../lib/api'
import { useLive } from '../lib/live'
import { canWrite } from '../lib/roles'
import type { Me } from '../lib/types'

type Counts = { total: number; up: number; down: number; pending: number; paused: number }
type Kind = 'down' | 'pending' | 'paused' | 'ok' | 'empty'
type Filter = 'all' | Kind
type SortKey = 'status' | 'name' | 'size'
type View = 'grid' | 'list'

const EMPTY: Counts = { total: 0, up: 0, down: 0, pending: 0, paused: 0 }
const VIEW_KEY = 'opskest-projects-view'
const LIST_COLS = 'md:grid-cols-[minmax(0,1.6fr)_minmax(120px,0.8fr)_80px_190px]'

function kindOf(c: Counts): Kind {
  if (c.total === 0) return 'empty'
  if (c.down > 0) return 'down'
  if (c.pending > 0) return 'pending'
  if (c.paused === c.total) return 'paused'
  return 'ok'
}
const RANK: Record<Kind, number> = { down: 0, pending: 1, ok: 2, paused: 3, empty: 4 }

function readView(): View {
  try { return localStorage.getItem(VIEW_KEY) === 'list' ? 'list' : 'grid' } catch { return 'grid' }
}

function KindBadge({ kind, count, connected }: { kind: Kind; count: number; connected: boolean }) {
  if (!connected) return <Badge tone="neutral">Updates paused</Badge>
  switch (kind) {
    case 'down': return <Badge tone="danger" dot>{count} need attention</Badge>
    case 'pending': return <Badge tone="neutral">Awaiting results</Badge>
    case 'paused': return <Badge tone="neutral">Paused</Badge>
    case 'empty': return <Badge tone="neutral">No monitors</Badge>
    default: return <Badge tone="ok" dot>Operational</Badge>
  }
}

function HealthBar({ c, className = '' }: { c: Counts; className?: string }) {
  const parts: [string, number, string][] = [['up', c.up, 'bg-ok'], ['down', c.down, 'bg-danger'], ['pending', c.pending, 'bg-warn'], ['paused', c.paused, 'bg-surface-hover']]
  return (
    <div className={`flex gap-0.5 h-1.5 ${className}`} role="img" aria-label={`${c.up} operational, ${c.down} with issues, ${c.pending} awaiting results, ${c.paused} paused`}>
      {c.total ? parts.map(([key, n, color]) => n > 0 && <span key={key} className={`rounded-full ${color}`} style={{ flex: n }} />) : <span className="w-full rounded-full bg-surface-2" />}
    </div>
  )
}

function ViewLabel({ icon, text }: { icon: 'Grid' | 'List'; text: string }) {
  const Glyph = Icon[icon]
  return <span className="flex items-center gap-1.5"><Glyph className="w-4 h-4" /><span className="sr-only sm:not-sr-only">{text}</span></span>
}

export default function Projects({ user }: { user: Me }) {
  const live = useLive()
  const [, navigate] = useLocation()
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<Filter>('all')
  const [sort, setSort] = useState<SortKey>('status')
  const [view, setViewState] = useState<View>(readView)
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const setView = (v: View) => { setViewState(v); try { localStorage.setItem(VIEW_KEY, v) } catch { /* private mode */ } }

  const all = useMemo(() => {
    const counts = new Map<number, Counts>()
    for (const m of live.monitors.values()) {
      if (m.vendor || m.groupId == null) continue
      const c = counts.get(m.groupId) ?? { ...EMPTY }
      c.total++
      if (m.paused) c.paused++
      else if (m.status === 'up') c.up++
      else if (m.status === 'down' || m.status === 'degraded') c.down++
      else c.pending++
      counts.set(m.groupId, c)
    }
    return live.groups.map(g => { const c = counts.get(g.id) ?? EMPTY; return { ...g, counts: c, kind: kindOf(c) } })
  }, [live.groups, live.monitors])

  const tally = useMemo(() => {
    const t: Record<Filter, number> = { all: all.length, down: 0, pending: 0, paused: 0, ok: 0, empty: 0 }
    for (const p of all) t[p.kind]++
    return t
  }, [all])

  const projects = useMemo(() => {
    const q = query.trim().toLowerCase()
    return all
      .filter(p => (filter === 'all' || p.kind === filter) && (p.name + ' ' + p.description).toLowerCase().includes(q))
      .sort((a, b) => sort === 'name' ? a.name.localeCompare(b.name)
        : sort === 'size' ? b.counts.total - a.counts.total || a.name.localeCompare(b.name)
        : RANK[a.kind] - RANK[b.kind] || b.counts.down - a.counts.down || a.name.localeCompare(b.name))
  }, [all, query, filter, sort])
  const paging = usePaging(projects, { resetKey: query + filter + sort })

  async function create(event: FormEvent) {
    event.preventDefault()
    if (!name.trim() || busy) return
    setBusy(true); setError('')
    try {
      const project = await api.post<{ id: number }>('/api/groups', { name: name.trim(), description: description.trim() })
      navigate(`/projects/${project.id}`)
    } catch (err) { setError(errorText(err, 'Could not create the project. Try again.')) }
    finally { setBusy(false) }
  }

  const chips: { key: Filter; label: string }[] = [
    { key: 'all', label: 'All' }, { key: 'down', label: 'Needs attention' }, { key: 'ok', label: 'Operational' },
    { key: 'pending', label: 'Awaiting' }, { key: 'paused', label: 'Paused' }, { key: 'empty', label: 'Empty' },
  ]
  const monitorsTotal = all.reduce((n, p) => n + p.counts.total, 0)
  const filtered = query.trim() !== '' || filter !== 'all'

  return <div>
    <PageTitle title="Projects" actions={canWrite(user) && <Button variant="primary" icon={<Icon.Plus className="w-4 h-4" />} onClick={() => setCreating(v => !v)}>New project</Button>}>
      Organize services by product, environment or team.{all.length > 0 && ` ${all.length} ${all.length === 1 ? 'project' : 'projects'}, ${monitorsTotal} ${monitorsTotal === 1 ? 'monitor' : 'monitors'}.`}
    </PageTitle>
    <Modal
      open={creating}
      onClose={() => { if (!busy) setCreating(false) }}
      title="Create a project"
      description="Everyone in your workspace can view this project. Administrators manage its monitors."
      size="lg"
      footer={<><Button type="button" variant="secondary" onClick={() => setCreating(false)} disabled={busy}>Cancel</Button><Button type="submit" form="create-project-form" variant="primary" busy={busy}>Create project</Button></>}
    >
      <form id="create-project-form" onSubmit={create} className="grid grid-cols-1 items-start gap-x-5 gap-y-5 sm:grid-cols-2">
        <Field label="Project name">{id => <Input id={id} autoFocus required maxLength={120} value={name} onChange={e => setName(e.target.value)} placeholder="Commerce platform" />}</Field>
        <Field label="Description">{id => <Input id={id} maxLength={1000} value={description} onChange={e => setDescription(e.target.value)} placeholder="What this project looks after" />}</Field>
        {error && <p role="alert" className="text-sm text-danger sm:col-span-2">{error}</p>}
      </form>
    </Modal>

    {live.groups.length > 0 && <div className="mb-5 space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative w-full sm:w-72"><Icon.Search className="absolute left-3 top-3 w-4 h-4 text-muted" /><Input aria-label="Search projects" className="w-full pl-9" placeholder="Find a project…" value={query} onChange={e => setQuery(e.target.value)} /></div>
        <div className="flex items-center gap-3 sm:ml-auto">
          <label className="flex items-center gap-2 text-[12px] text-muted">Sort
            <Select aria-label="Sort projects" className="w-36" value={sort} onChange={e => setSort(e.target.value as SortKey)}>
              <option value="status">Status</option><option value="name">Name</option><option value="size">Most monitors</option>
            </Select>
          </label>
          <SegmentedControl<View> label="View" options={[{ key: 'grid', label: <ViewLabel icon="Grid" text="Grid" /> }, { key: 'list', label: <ViewLabel icon="List" text="List" /> }]} value={view} onChange={setView} />
        </div>
      </div>
      <FilterChips label="Filter by status" value={filter} onChange={setFilter} options={chips.filter(c => c.key === 'all' || tally[c.key] > 0 || filter === c.key).map(c => ({ key: c.key, label: c.label, count: tally[c.key] }))} />
    </div>}

    {!live.lastUpdate ? (view === 'grid' ? <SkeletonCards count={6} label="Connecting to projects" className="grid sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4 gap-4" /> : <SkeletonRows rows={6} label="Connecting to projects" />) : projects.length === 0 ? <EmptyState title={filtered ? 'No matching projects' : 'No projects'}>{filtered ? 'Try another name or clear the filter.' : canWrite(user) ? 'Create a project to organize the services your team monitors.' : 'Projects will appear here when a teammate creates them.'}</EmptyState> :
      <>
        {view === 'grid' ? (
          <div className={`grid gap-4 ${paging.rows.length === 1 ? 'mx-auto w-full max-w-md grid-cols-1' : paging.rows.length === 2 ? 'mx-auto w-full max-w-3xl grid-cols-1 sm:grid-cols-2' : 'grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4'}`}>
            {paging.rows.map(p => (
              <Link key={p.id} href={`/projects/${p.id}`} className="group rounded-[14px] border border-default bg-surface p-5 flex flex-col gap-4 min-h-[176px] transition-[border-color,box-shadow] hover:border-strong hover:shadow-md">
                <div className="flex items-start gap-3">
                  <span className="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-[10px] bg-surface-2 text-muted"><Icon.Folder className="w-[18px] h-[18px]" /></span>
                  <div className="min-w-0 flex-1">
                    <h3 className="text-sm font-semibold leading-snug break-words">{p.name}</h3>
                    <p className="mt-1 text-[13px] text-muted leading-5 line-clamp-2">{p.description || 'No description added.'}</p>
                  </div>
                </div>
                <div className="mt-auto space-y-3">
                  <HealthBar c={p.counts} />
                  <div className="flex items-center justify-between gap-3">
                    <KindBadge kind={p.kind} count={p.counts.down} connected={live.connected} />
                    <span className="text-[12px] text-muted tnum">{p.counts.total} {p.counts.total === 1 ? 'monitor' : 'monitors'}</span>
                  </div>
                </div>
              </Link>
            ))}
          </div>
        ) : (
          <div className={LIST_FRAME}>
            <div className={`hidden md:grid ${LIST_COLS} items-center gap-5 ${LIST_HEAD}`}>
              <span>Project</span><span>Health</span><span className="text-right">Monitors</span><span>Status</span>
            </div>
            {paging.rows.map(p => (
              <Link key={p.id} href={`/projects/${p.id}`} className={`group grid ${LIST_COLS} items-center gap-x-5 gap-y-2 ${LIST_ROW}`}>
                <div className="flex items-center gap-3 min-w-0">
                  <Icon.Folder className="w-4 h-4 shrink-0 text-muted group-hover:text-accent" />
                  <div className="min-w-0">
                    <h3 className="text-sm font-semibold truncate group-hover:text-accent">{p.name}</h3>
                    <p className="text-[12px] text-muted truncate">{p.description || 'No description added.'}</p>
                  </div>
                </div>
                <HealthBar c={p.counts} className="hidden md:flex" />
                <span className="hidden md:block text-sm text-muted tnum text-right">{p.counts.total}</span>
                <div className="flex items-center justify-between gap-3">
                  <KindBadge kind={p.kind} count={p.counts.down} connected={live.connected} />
                  <span className="md:hidden text-[12px] text-muted tnum">{p.counts.total} {p.counts.total === 1 ? 'monitor' : 'monitors'}</span>
                </div>
              </Link>
            ))}
          </div>
        )}
        <Pager paging={paging} noun="Projects" />
      </>}
  </div>
}
