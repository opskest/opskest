import { useEffect, useMemo, useState } from 'react'
import { Link, useLocation } from 'wouter'
import { PageTitle } from '../components/PageShell'
import { BeatBar, StatusBadge } from '../components/Status'
import { Button, ConfirmModal, DropdownMenu, EmptyState, FilterChips, HoverCard, Icon, Input, Select, SkeletonRows, Timestamp, useToast } from '../components/ui'
import { LIST_FRAME, LIST_HEAD, LIST_ROW, LIST_SCROLL } from '../components/table'
import { Pager, usePaging } from '../components/Pager'
import { api, errorText } from '../lib/api'
import { ms, pct } from '../lib/format'
import { useLive } from '../lib/live'
import { canWrite } from '../lib/roles'
import type { Channel, Me, MonitorSummary } from '../lib/types'

const HEALTH = [
  { key: 'all', label: 'All states' }, { key: 'issues', label: 'Needs attention' }, { key: 'up', label: 'Operational' }, { key: 'down', label: 'Down' },
  { key: 'degraded', label: 'Degraded' }, { key: 'pending', label: 'Pending' }, { key: 'paused', label: 'Paused' }, { key: 'maintenance', label: 'Maintenance' }, { key: 'unknown', label: 'Unknown' },
]
export default function Monitors({ user }: { user: Me }) {
  const live = useLive()
  const [, navigate] = useLocation()
  const { push } = useToast()
  const admin = canWrite(user) // admin or user
  const [removing, setRemoving] = useState<{ id: number; name: string } | null>(null)
  const [query, setQuery] = useState('')
  const [health, setHealth] = useState('all')
  const [project, setProject] = useState('all')
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [deleting, setDeleting] = useState(false)
  const [channels, setChannels] = useState<Channel[]>([])
  const [channelId, setChannelId] = useState('')
  useEffect(() => {
    if (!admin) return
    api.get<{ channels?: Channel[] }>('/api/channels').then(r => setChannels(r.channels ?? [])).catch(() => {})
  }, [admin])
  const monitors = useMemo(() => [...live.monitors.values()].filter(m => !m.vendor), [live.monitors])
  const groups = useMemo(() => new Map(live.groups.map(g => [g.id,g.name])), [live.groups])
  const filtered = useMemo(() => monitors.filter(m => {
    const state = m.paused ? 'paused' : m.status
    return (health === 'all' || (health === 'issues' ? ['down','degraded','pending'].includes(state) : state === health)) &&
      (project === 'all' || String(m.groupId ?? '') === project) &&
      (m.name + ' ' + m.target + ' ' + (groups.get(m.groupId ?? 0) ?? '')).toLowerCase().includes(query.trim().toLowerCase())
  }).sort((a,b) => {
    const rank = (m: typeof a) => m.paused ? 4 : m.status === 'down' ? 0 : m.status === 'degraded' ? 1 : m.status === 'up' ? 3 : 2
    return rank(a) - rank(b) || a.name.localeCompare(b.name)
  }), [monitors,health,project,query,groups])
  const paging = usePaging(filtered, { pageSize: 25, resetKey: `${health}|${project}|${query}` })
  const visible = paging.rows
  const ids = [...selected].filter(id => live.monitors.has(id))
  function toggle(id: number) { setSelected(prev => { const next = new Set(prev); next.has(id) ? next.delete(id) : next.add(id); return next }) }
  async function bulk(action: string) {
    setBusy(true); setError('')
    try {
      await api.post('/api/monitors/bulk', action.endsWith('_channel') ? { ids, action, channelId: Number(channelId) } : { ids, action })
      setSelected(new Set()); setDeleting(false)
    } catch (err) { setError(errorText(err, 'The action could not finish. Review current monitor states before retrying.')); setDeleting(false) }
    finally { setBusy(false) }
  }
  async function rowAction(label: string, run: () => Promise<unknown>, done?: string) {
    try { await run(); if (done) push(done, 'ok') } catch (err) { push(errorText(err, `Could not ${label}.`), 'danger') }
  }
  async function removeOne() {
    if (!removing) return
    const target = removing
    setRemoving(null)
    await rowAction(`delete ${target.name}`, () => api.del(`/api/monitors/${target.id}`), `Deleted ${target.name}.`)
  }
  const stateOf = (m: MonitorSummary) => m.paused ? 'paused' : m.status
  const healthCount = (key: string) => monitors.filter(m => (project === 'all' || String(m.groupId ?? '') === project) && (key === 'all' || (key === 'issues' ? ['down', 'degraded', 'pending'].includes(stateOf(m)) : stateOf(m) === key))).length
  const facets = [{ id: 'all', name: 'All projects', count: monitors.length }, { id: '', name: 'Unassigned', count: monitors.filter(m => m.groupId == null).length }, ...live.groups.map(g => ({ id: String(g.id), name: g.name, count: monitors.filter(m => m.groupId === g.id).length }))]
  return <div>
    <PageTitle title="Monitor inventory" actions={admin && <Link href="/monitors/new"><Button variant="primary" icon={<Icon.Plus className="w-4 h-4" />}>Add monitor</Button></Link>}>Inspect endpoints, review recent checks and manage monitoring.</PageTitle>
    <div className={`grid gap-7 items-start ${monitors.length > 0 ? 'lg:grid-cols-[220px_minmax(0,1fr)]' : ''}`}>
      {monitors.length > 0 && <aside className="min-w-0 lg:sticky lg:top-24"><h2 className="sr-only">Projects</h2><FilterChips variant="rail" label="Filter by project" value={project} onChange={id => { setProject(id) }} options={facets.map(f => ({ key: f.id, label: f.name, count: f.count }))} className="lg:max-h-[calc(100dvh-16rem)] lg:overflow-y-auto scroll-slim" /><div className="hidden lg:block mt-6 pt-5 px-3 border-t border-default"><p className="text-[12px] leading-6 text-muted">Results update as checks arrive. Services with issues appear first.</p><Link href="/projects" className="inline-block text-[13px] text-accent mt-3">Manage projects</Link></div></aside>}
      <div className="min-w-0">
        {monitors.length > 0 && <div className="mb-5 space-y-3"><div className="flex flex-wrap gap-3 items-center"><div className="relative min-w-[180px] flex-1"><Icon.Search className="absolute left-3 top-3 w-4 h-4 text-muted" /><Input aria-label="Search monitors" className="w-full pl-9" placeholder="Search services or endpoints" value={query} onChange={e => { setQuery(e.target.value) }} /></div><span className="text-[13px] text-muted">{filtered.length} results</span></div><FilterChips label="Filter by state" value={health} onChange={k => { setHealth(k) }} options={HEALTH.filter(h => h.key === 'all' || healthCount(h.key) > 0 || health === h.key).map(h => ({ key: h.key, label: h.label, count: healthCount(h.key) }))} /></div>}
        {error && <p role="alert" className="text-sm text-danger bg-danger-bg rounded-[10px] px-3.5 py-2.5 mb-4">{error}</p>}
        {admin && ids.length > 0 && <div role="toolbar" aria-label="Bulk actions for selected monitors" className="sticky top-20 z-10 flex flex-wrap items-center gap-2 mb-4 p-4 rounded-[14px] border border-accent-border bg-accent-subtle"><span role="status" className="text-sm text-body mr-auto font-medium">{ids.length} selected</span><Button size="sm" busy={busy} onClick={() => bulk('pause')}>Pause</Button><Button size="sm" disabled={busy} onClick={() => bulk('resume')}>Resume</Button>{channels.length > 0 && <><Select aria-label="Alert channel" value={channelId} onChange={e => setChannelId(e.target.value)} className="max-w-[200px]"><option value="">Alert channel…</option>{channels.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</Select><Button size="sm" disabled={busy || !channelId} onClick={() => bulk('add_channel')}>Add channel</Button><Button size="sm" disabled={busy || !channelId} onClick={() => bulk('remove_channel')}>Remove channel</Button></>}<Button size="sm" disabled={busy} onClick={() => setDeleting(true)}>Delete</Button><Button size="sm" disabled={busy} onClick={() => setSelected(new Set())}>Clear</Button></div>}
        {!live.lastUpdate ? <SkeletonRows rows={6} label="Connecting to monitors" /> : filtered.length === 0 ? <EmptyState title={monitors.length ? 'No matching services' : 'No monitors'}>{monitors.length ? 'Try a different project, search or state.' : admin ? 'Add a monitor to start collecting uptime and response data.' : 'Monitors will appear here when an administrator adds them.'}</EmptyState> : <section aria-label="Monitors" className={LIST_FRAME}>
          <div className={`flex items-center gap-4 overflow-y-hidden scroll-slim [scrollbar-gutter:stable] ${LIST_HEAD}`}>{admin && <input aria-label="Select visible monitors" type="checkbox" disabled={busy} checked={visible.length > 0 && visible.every(m => selected.has(m.id))} onChange={e => setSelected(prev => { const next = new Set(prev); visible.forEach(m => e.target.checked ? next.add(m.id) : next.delete(m.id)); return next })} />}<span className="flex-1">Service / endpoint</span><span className="hidden xl:block w-44 shrink-0">Recent checks</span><span className="hidden sm:block w-20 shrink-0 text-right">Response</span><span className="hidden md:block w-20 shrink-0 text-right">24h uptime</span>{admin && <span aria-hidden="true" className="w-9 shrink-0" />}</div>
          <ul className={`divide-y divide-default [scrollbar-gutter:stable] ${LIST_SCROLL}`}>{visible.map(m => <li key={m.id} className={`flex items-center gap-4 ${LIST_ROW}`}>{admin && <input aria-label={`Select ${m.name}`} type="checkbox" disabled={busy} checked={selected.has(m.id)} onChange={() => toggle(m.id)} />}<div className="min-w-0 flex-1"><div className="flex flex-wrap items-center gap-3"><HoverCard label={`${m.name} summary`} card={<MonitorPreview m={m} project={groups.get(m.groupId ?? 0) ?? 'Unassigned'} />}><Link href={`/monitors/${m.id}`} className="font-medium text-body hover:text-accent text-sm break-words">{m.name}</Link></HoverCard><StatusBadge status={m.paused ? 'paused' : m.status} size="sm" /></div><p className="mt-1 flex min-w-0 items-center gap-2 text-[12px] text-muted"><span className="min-w-0 truncate font-mono" title={m.target}>{m.target}</span><span aria-hidden="true" className="text-faint">·</span><span className="max-w-[14rem] shrink-0 truncate">{groups.get(m.groupId ?? 0) ?? 'Unassigned'} · {m.type.toUpperCase()}</span></p></div><div className="hidden xl:block w-44 shrink-0 overflow-hidden"><BeatBar beats={m.beats} count={24} size="sm" intervalS={m.intervalS} lastCheckAt={m.lastCheckAt} /></div><span className="hidden sm:block w-20 shrink-0 text-right text-sm tabular-nums text-body-2">{m.lastCheckAt && !m.paused && m.latencyMs >= 0 ? ms(m.latencyMs) : '—'}</span><span className={`hidden md:block w-20 shrink-0 text-right text-sm tabular-nums ${!m.paused && m.uptime24h != null && m.uptime24h < 95 ? 'font-medium text-danger' : 'text-body-2'}`}>{pct(m.uptime24h)}</span>{admin && <DropdownMenu label={`Actions for ${m.name}`} items={[
            { key: 'open', label: 'Open', icon: <Icon.ArrowRight className="h-3.5 w-3.5" />, onSelect: () => navigate(`/monitors/${m.id}`) },
            { key: 'edit', label: 'Edit', icon: <Icon.Edit className="h-3.5 w-3.5" />, onSelect: () => navigate(`/monitors/${m.id}/edit`) },
            m.paused
              ? { key: 'pause', label: 'Resume', icon: <Icon.Play className="h-3 w-3" />, onSelect: () => rowAction(`resume ${m.name}`, () => api.post(`/api/monitors/${m.id}/resume`), `Resumed ${m.name}.`) }
              : { key: 'pause', label: 'Pause', icon: <Icon.Pause className="h-3 w-3" />, onSelect: () => rowAction(`pause ${m.name}`, () => api.post(`/api/monitors/${m.id}/pause`), `Paused ${m.name}.`) },
            { key: 'dup', label: 'Duplicate', icon: <Icon.Copy className="h-3.5 w-3.5" />, onSelect: () => rowAction(`duplicate ${m.name}`, async () => { const r = await api.post<{ id: number }>(`/api/monitors/${m.id}/duplicate`); if (r?.id) navigate(`/monitors/${r.id}`) }) },
            { key: 'sep', separator: true },
            { key: 'delete', label: 'Delete', icon: <Icon.Trash className="h-3.5 w-3.5" />, tone: 'danger', onSelect: () => setRemoving({ id: m.id, name: m.name }) },
          ]} />}</li>)}</ul>
          <div className="border-t border-default px-5 py-3 empty:hidden"><Pager paging={paging} noun="Monitors" /></div>
        </section>}
      </div>
    </div>
    <ConfirmModal open={removing != null} title={`Delete ${removing?.name ?? 'this monitor'}?`} description="This deletes its checks and outage history. This cannot be undone." onClose={() => setRemoving(null)} onConfirm={removeOne} />
    <ConfirmModal open={deleting} title={`Delete ${ids.length} monitors?`} description="This deletes their checks and outage history. This cannot be undone." onClose={() => setDeleting(false)} onConfirm={() => bulk('delete')} />
  </div>
}

/** The summary shown when a monitor name is hovered or keyboard-focused in a list. */
function MonitorPreview({ m, project }: { m: MonitorSummary; project: string }) {
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2"><StatusBadge status={m.paused ? 'paused' : m.status} size="sm" /><span className="text-[12px] text-muted">{project} · {m.type.toUpperCase()}</span></div>
      {m.reason && !m.paused && m.status !== 'up' && <p className="text-[12px] leading-relaxed text-body-2 [overflow-wrap:anywhere]">{m.reason}</p>}
      <p className="font-mono text-[11px] text-muted [overflow-wrap:anywhere]">{m.target}</p>
      <BeatBar beats={m.beats} count={32} size="sm" intervalS={m.intervalS} lastCheckAt={m.lastCheckAt} />
      <dl className="grid grid-cols-3 gap-2 text-[12px]">
        <div><dt className="text-muted">Response</dt><dd className="tnum text-body">{m.lastCheckAt && !m.paused && m.latencyMs >= 0 ? ms(m.latencyMs) : '\u2014'}</dd></div>
        <div><dt className="text-muted">24h uptime</dt><dd className="tnum text-body">{pct(m.uptime24h)}</dd></div>
        <div><dt className="text-muted">Last check</dt><dd className="text-body"><Timestamp at={m.lastCheckAt} future={false} /></dd></div>
      </dl>
    </div>
  )
}
