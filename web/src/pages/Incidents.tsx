import { useEffect, useMemo, useState } from 'react'
import { Link, useLocation, useSearch } from 'wouter'
import { NewIncidentModal } from '../components/incidents/NewIncidentModal'
import { OutagesTab } from '../components/incidents/OutagesTab'
import { PageTitle } from '../components/PageShell'
import { Pager, usePaging } from '../components/Pager'
import { IMPACT_LABEL, impactTone, INCIDENT_STATUS } from '../components/status/IncidentCards'
import { Badge, Button, DropdownMenu, EmptyState, ErrorRetry, FilterChips, Icon, Input, SkeletonRows, Tabs, Timestamp, useToast } from '../components/ui'
import { LIST_ROW, LIST_SCROLL } from '../components/table'
import { api, errorText, unwrapList } from '../lib/api'
import { duration } from '../lib/format'
import { useLive } from '../lib/live'
import { normIncident } from '../lib/normalize'
import { canWrite } from '../lib/roles'
import type { Incident, Me } from '../lib/types'

export default function Incidents({ user }: { user: Me }) {
  const search = useSearch()
  const [, navigate] = useLocation()
  const outages = new URLSearchParams(search).get('tab') === 'outages'
  const live = useLive()
  const { push } = useToast()
  const [items, setItems] = useState<Incident[] | null>(null)
  const [error, setError] = useState('')
  const [nonce, setNonce] = useState(0)
  const [filter, setFilter] = useState('active')
  const [query, setQuery] = useState('')
  const [creating, setCreating] = useState(() => new URLSearchParams(search).get('new') === '1')
  const [moreOlder, setMoreOlder] = useState(false)
  const [loadingOlder, setLoadingOlder] = useState(false)
  useEffect(() => {
    let alive = true
    setError('')
    api.get<unknown>('/api/incidents?limit=200').then(d => { if (alive) { const list = unwrapList(d, 'incidents').map(normIncident); setItems(list); setMoreOlder(list.length >= 200) } }).catch(e => { if (alive) setError(errorText(e, 'Could not load incidents.')) })
    return () => { alive = false }
  }, [nonce])
  const visible = useMemo(() => (items ?? []).filter(i => (filter === 'all' || (filter === 'active' ? i.status !== 'resolved' : i.status === filter)) && `${i.title} ${i.monitorIds.map(id => live.monitors.get(id)?.name ?? '').join(' ')}`.toLowerCase().includes(query.toLowerCase())).sort((a, b) => b.updatedAt - a.updatedAt), [items, filter, query, live.monitors])
  const paging = usePaging(visible, { resetKey: `${filter}|${query}` })
  async function loadOlder() {
    const oldest = (items ?? []).reduce((a, i) => Math.min(a, i.createdAt), Number.POSITIVE_INFINITY)
    if (!Number.isFinite(oldest)) return
    setLoadingOlder(true)
    try {
      const d = await api.get<unknown>(`/api/incidents?limit=200&before=${oldest}`)
      const older = unwrapList(d, 'incidents').map(normIncident)
      setItems(prev => { const have = new Set((prev ?? []).map(i => i.id)); return [...(prev ?? []), ...older.filter(i => !have.has(i.id))] })
      setMoreOlder(older.length >= 200)
    } catch (e) { setError(errorText(e, 'Could not load older incidents.')) } finally { setLoadingOlder(false) }
  }
  const count = (key: string) => (items ?? []).filter(i => key === 'all' || (key === 'active' ? i.status !== 'resolved' : i.status === key)).length
  return <div>
    <PageTitle title="Incident response" actions={canWrite(user) ? <Button variant="primary" onClick={() => setCreating(true)} icon={<Icon.Plus className="w-4 h-4" />}>Declare incident</Button> : undefined}>Coordinate the response and keep everyone informed.</PageTitle>
    <div className="mb-6"><Tabs label="Incident views" active={outages ? 'outages' : 'inbox'} onChange={k => navigate(k === 'outages' ? '/incidents?tab=outages' : '/incidents')} tabs={[{ key: 'inbox', label: 'Incident inbox' }, { key: 'outages', label: 'Monitor outages' }]} /></div>
    {outages ? <OutagesTab user={user} /> : <div className="grid gap-6 lg:grid-cols-[190px_minmax(0,1fr)]">
      {(!items || items.length > 0) && <aside className="min-w-0"><FilterChips variant="rail" label="Filter incidents" value={filter} onChange={setFilter} options={[['active', 'Open incidents'], ['investigating', 'Investigating'], ['identified', 'Identified'], ['monitoring', 'Monitoring'], ['resolved', 'Resolved'], ['all', 'All incidents']].map(([key, label]) => ({ key, label, count: items ? count(key) : undefined }))} /></aside>}
      <section className={`min-w-0 rounded-[14px] border border-default bg-surface overflow-hidden ${items?.length === 0 ? 'lg:col-span-2' : ''}`} aria-label="Incident inbox">
        {items?.length !== 0 && <div className="flex flex-wrap items-center justify-between gap-3 border-b border-default px-5 py-3.5"><h2 className="text-base font-semibold">{filter === 'active' ? 'Open incidents' : filter === 'all' ? 'All incidents' : INCIDENT_STATUS[filter as keyof typeof INCIDENT_STATUS] ?? filter}</h2><Input aria-label="Search incidents" value={query} onChange={e => setQuery(e.target.value)} placeholder="Search title or service" className="w-full sm:w-auto sm:max-w-xs" /></div>}
        {error ? <ErrorRetry message={error} onRetry={() => setNonce(n => n + 1)} /> : !items ? <SkeletonRows rows={5} framed={false} label="Loading incidents" /> : !visible.length ? <div><EmptyState bare title={items.length === 0 ? 'No incidents' : query ? 'No matching incidents' : 'No incidents in this view'} action={items.length > 0 ? <Button onClick={() => { setFilter('all'); setQuery('') }}>View all incidents</Button> : undefined}>{items.length === 0 ? 'Declared incidents and their updates will appear here.' : 'Try another status or clear the search to see other incidents.'}</EmptyState></div> : <div className={`divide-y divide-default ${LIST_SCROLL}`}>{paging.rows.map(i => <div key={i.id} className={`flex items-start ${LIST_ROW} !py-0`}><Link href={`/incidents/${i.id}`} className="group grid min-w-0 flex-1 gap-3 py-3.5 pr-2 sm:grid-cols-[minmax(0,1fr)_auto]">
          <div className="min-w-0"><div className="flex flex-wrap items-center gap-2 mb-1.5"><span className="text-[12px] text-faint tnum">INC-{i.id}</span><Badge tone={impactTone(i.impact)}>{IMPACT_LABEL[i.impact] ?? i.impact}</Badge></div><h3 className="text-sm font-semibold group-hover:text-accent break-words">{i.title}</h3><p className="text-[13px] text-muted mt-1">{i.monitorIds.length ? i.monitorIds.slice(0, 3).map(id => live.monitors.get(id)?.name ?? 'Deleted monitor').join(', ') : 'No linked services'}{i.monitorIds.length > 3 ? ` +${i.monitorIds.length - 3}` : ''}</p></div>
          <div className="flex sm:items-end flex-col gap-2"><Badge tone={i.status === 'resolved' ? 'ok' : 'warn'} dot={i.status !== 'resolved'}>{INCIDENT_STATUS[i.status]}</Badge><span className="text-[12px] text-muted"><Timestamp at={i.updatedAt} prefix="Updated" future={false} /></span><span className="text-[12px] text-faint">{i.resolvedAt ? `Resolved in ${duration(i.resolvedAt - i.createdAt)}` : `Open ${duration(Date.now() - i.createdAt)}`}</span></div>
        </Link><div className="pt-3"><DropdownMenu label={`Actions for incident ${i.id}`} items={[
          { key: 'open', label: 'Open incident', icon: <Icon.ArrowRight className="h-3.5 w-3.5" />, onSelect: () => navigate(`/incidents/${i.id}`) },
          { key: 'tab', label: 'Open in new tab', icon: <Icon.External className="h-3.5 w-3.5" />, onSelect: () => { window.open(`/incidents/${i.id}`, '_blank', 'noopener') } },
          { key: 'copy', label: 'Copy link', icon: <Icon.Copy className="h-3.5 w-3.5" />, onSelect: () => { navigator.clipboard?.writeText(`${window.location.origin}/incidents/${i.id}`).then(() => push('Incident link copied.', 'ok'), () => push('Could not copy the link.', 'danger')) } },
        ]} /></div></div>)}</div>}
        {!error && items && (visible.length > 25 || moreOlder) && <div className="border-t border-default px-5 py-3"><Pager paging={paging} noun="Incidents" hasMore={moreOlder} onLoadMore={loadOlder} loadingMore={loadingOlder} /></div>}
      </section>
    </div>}
    <NewIncidentModal open={creating} onClose={() => setCreating(false)} onCreated={i => { setItems(prev => [i, ...(prev ?? [])]); setFilter('active') }} monitors={live.monitors} />
  </div>
}
