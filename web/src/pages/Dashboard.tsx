import { useState, useMemo, useEffect } from 'react'
import { Link, useLocation } from 'wouter'
import { useLive } from '../lib/live'
import { api, unwrapList } from '../lib/api'
import { ms, pct, dateServer, duration } from '../lib/format'
import { normHeartbeat, normResult } from '../lib/normalize'
import { canWrite } from '../lib/roles'
import type { Heartbeat, Me, MonitorOut, MonitorSummary, Status } from '../lib/types'
import { draftFromOut, toMonitorIn } from '../lib/monitorForm'
import { StatusBadge, BeatBar } from '../components/Status'
import { BeatInspector } from '../components/BeatInspector'
import { Badge, Button, ConfirmModal, EmptyState, Input, Modal, Field, Icon, SkeletonRows, Tabs } from '../components/ui'
import { LIST_ROW } from '../components/table'
import { PageTitle } from '../components/PageShell'
import { Pager, usePaging } from '../components/Pager'
import { DiagnosticsTab } from '../components/detail/DiagnosticsTab'
import { ManualCard } from '../components/detail/ManualCard'
import { ResponseTimeCard } from '../components/detail/ResponseTimeCard'
import { SlaCard } from '../components/detail/SlaCard'
import { MonitorHeaderCard, MonitorStatStrip, UptimeHistoryCard } from '../components/detail/MonitorOverview'
import { isThirdPartyMonitor } from '../lib/vendor'
import { OutageNotesToggle } from '../components/OutageNotes'

interface OutageRow { id?: number; monitorId?: number; startedAt?: number; endedAt?: number; durationSeconds?: number; firstReason?: string; reason?: string; noteCount?: number; note?: string | null }
const TIMELINE_SIZES = [10, 25, 50] as const
function OutageTimeline({ events, name }: { events: OutageRow[]; name?: (id: number) => string }) {
  const paging = usePaging(events, { pageSize: 10 })
  return <><ol className="divide-y divide-default">{paging.rows.map((event,i) => <li key={event.id ?? i} className="grid sm:grid-cols-[180px_minmax(0,1fr)] gap-4 py-3 first:pt-0"><div className="space-y-3"><p className="text-sm text-muted">{event.startedAt ? dateServer(event.startedAt) : 'Start time unavailable'}</p><StatusBadge status={event.endedAt ? 'up' : 'down'} size="sm" /></div><div><h3 className="text-sm font-semibold">{name && event.monitorId ? name(event.monitorId) : event.endedAt ? 'Service recovered' : 'Outage in progress'}</h3><p className="text-sm text-body-2 break-words mt-2">{event.firstReason || event.reason || 'Check failed'}</p><p className="text-xs text-muted mt-3">{event.endedAt ? `Recovered ${dateServer(event.endedAt)}` : 'Still open'}{event.durationSeconds != null ? ` · ${duration(event.durationSeconds * 1000)}` : event.startedAt ? ` · ${duration((event.endedAt || Date.now()) - event.startedAt)}` : ''}</p>{event.id != null && <OutageNotesToggle outageId={event.id} count={event.noteCount ?? 0} latest={event.note} />}</div></li>)}</ol><Pager paging={paging} noun="Events" sizes={TIMELINE_SIZES} className="mt-3" /></>
}

function InvestigationTabs({ items, value, onChange }: { items: string[]; value: string; onChange: (next: string) => void }) {
  return <div className="mb-6"><Tabs label="Detail views" active={value} onChange={onChange} tabs={items.map(item => ({ key: item, label: item }))} /></div>
}

interface DashboardProps {
  user: Me
  selectedId?: number
  selectedGroupId?: number
}

export default function Dashboard({ user, selectedId, selectedGroupId }: DashboardProps) {
  const isAdminUser = canWrite(user) // admin or user: may edit, pause and run checks
  const live = useLive()
  const [, setLocation] = useLocation()
  const [detailView, setDetailView] = useState('Checks')
  const [projectView, setProjectView] = useState('Services')
  const [busyAction, setBusyAction] = useState<string | null>(null)
  const [showEditGroup, setShowEditGroup] = useState(false)
  const [editGroupName, setEditGroupName] = useState('')
  const [editGroupDescription, setEditGroupDescription] = useState('')
  const [showDeleteGroup, setShowDeleteGroup] = useState(false)
  const [groupOutages, setGroupOutages] = useState<any[]>([])
  const [groupOutagesLoading, setGroupOutagesLoading] = useState(false)
  const [groupOutagesError, setGroupOutagesError] = useState(false)
  const [testResult, setTestResult] = useState<any | null>(null)
  const [latestBeat, setLatestBeat] = useState<Heartbeat | null>(null)
  const [recentBeats, setRecentBeats] = useState<Heartbeat[]>([])
  const [copiedNotice, setCopiedNotice] = useState<string | null>(null)
  const [monitorOutages, setMonitorOutages] = useState<any[]>([])
  const [outagesLoading, setOutagesLoading] = useState(false)
  const [outagesError, setOutagesError] = useState(false)
  const [historyReload, setHistoryReload] = useState(0)
  const [actionError, setActionError] = useState('')
  useEffect(() => { setDetailView('Checks'); setProjectView('Services'); setTestResult(null); setActionError(''); setCopiedNotice(null) }, [selectedId, selectedGroupId])
  // Provider feeds are kept separate from owned services.
  const thirdPartyGroupIds = useMemo(() => {
    const ids = new Set<number>()
    for (const g of live.groups) {
      const n = g.name.toLowerCase()
      if (n.includes('3rd') || n.includes('third') || n.includes('vendor') || n.includes('provider') || n.includes('external')) {
        ids.add(g.id)
      }
    }
    return ids
  }, [live.groups])

  // Filter out 3rd party monitors from main dashboard:
  const monitorsList = useMemo(() => {
    return Array.from(live.monitors.values()).filter(m => !isThirdPartyMonitor(m, live.groups))
  }, [live.monitors, live.groups])

  const internalGroups = useMemo(() => {
    return live.groups.filter(g => !thirdPartyGroupIds.has(g.id))
  }, [live.groups, thirdPartyGroupIds])

  // Selected monitor
  // 3rd-party status feeds are alert-only: their incidents aren't your outages.
  const activeMonitor = useMemo(() => {
    if (!selectedId) return null
    return live.monitors.get(selectedId) ?? null
  }, [selectedId, live.monitors])

  // Active Group (when a group is selected and no single monitor is selected)
  const activeGroup = useMemo(() => {
    if (selectedId != null) return null
    if (selectedGroupId == null) return null
    if (selectedGroupId === -1) {
      return { id: -1, name: 'Unassigned', description: 'Monitors not assigned to any project', sort: 9999 }
    }
    return internalGroups.find(g => g.id === selectedGroupId) ?? null
  }, [selectedId, selectedGroupId, internalGroups])

  // Monitors belonging to the active group
  const activeGroupMonitors = useMemo(() => {
    if (!activeGroup) return []
    if (activeGroup.id === -1) {
      return monitorsList.filter(m => m.groupId == null)
    }
    return monitorsList.filter(m => m.groupId === activeGroup.id)
  }, [activeGroup, monitorsList])

  // A project can hold thousands of services: render them a page at a time.
  const servicePaging = usePaging(activeGroupMonitors, { resetKey: activeGroup?.id })

  // Telemetry and aggregates for the active group
  const groupStats = useMemo(() => {
    if (!activeGroup) return null
    let up = 0
    let down = 0
    let degraded = 0
    let paused = 0
    let totalLatency = 0
    let latencyCount = 0
    let total24h = 0
    let count24h = 0
    let total7d = 0
    let count7d = 0
    let total30d = 0
    let count30d = 0
    let total90d = 0
    let count90d = 0

    for (const m of activeGroupMonitors) {
      if (m.paused) paused++
      else if (m.status === 'up') up++
      else if (m.status === 'down') down++
      else if (m.status === 'degraded' || m.status === 'pending') degraded++

      if (m.latencyMs != null && m.latencyMs >= 0) {
        totalLatency += m.latencyMs
        latencyCount++
      }
      if (m.uptime24h != null) {
        total24h += m.uptime24h
        count24h++
      }
      if (m.uptime7d != null) {
        total7d += m.uptime7d
        count7d++
      }
      if (m.uptime30d != null) {
        total30d += m.uptime30d
        count30d++
      }
      if (m.uptime90d != null) {
        total90d += m.uptime90d
        count90d++
      }
    }

    const avgLatency = latencyCount > 0 ? Math.round(totalLatency / latencyCount) : null
    const avg24h = count24h > 0 ? total24h / count24h : null
    const avg7d = count7d > 0 ? total7d / count7d : null
    const avg30d = count30d > 0 ? total30d / count30d : null
    const avg90d = count90d > 0 ? total90d / count90d : null

    let groupStatus: Status = activeGroupMonitors.length ? 'up' : 'unknown'
    if (down > 0) groupStatus = 'down'
    else if (degraded > 0) groupStatus = 'degraded'
    else if (paused === activeGroupMonitors.length && activeGroupMonitors.length > 0) groupStatus = 'paused'
    else if (activeGroupMonitors.some(m => !m.paused && (m.maint || m.status === 'maintenance'))) groupStatus = 'maintenance'
    else if (activeGroupMonitors.some(m => !m.paused && m.status !== 'up')) groupStatus = 'unknown'

    return {
      total: activeGroupMonitors.length,
      up,
      down,
      degraded,
      paused,
      avgLatency,
      avg24h,
      avg7d,
      avg30d,
      avg90d,
      status: groupStatus,
      allPaused: paused === activeGroupMonitors.length && activeGroupMonitors.length > 0,
    }
  }, [activeGroup, activeGroupMonitors])

  // Fetch outage history for active group
  useEffect(() => {
    if (!activeGroup || activeGroupMonitors.length === 0) {
      setGroupOutages([])
      return
    }
    setGroupOutagesLoading(true)
    setGroupOutagesError(false)
    let alive = true
    const monitorIds = new Set(activeGroupMonitors.map(m => m.id))
    api.get<{ outages?: any[] } | any[]>('/api/outages?limit=50')
      .then(res => {
        const list = Array.isArray(res) ? res : res?.outages || []
        const filtered = list.filter((o: any) => monitorIds.has(o.monitorId))
        if (alive) setGroupOutages(filtered)
      })
      .catch(() => { if (alive) setGroupOutagesError(true) })
      .finally(() => { if (alive) setGroupOutagesLoading(false) })
    return () => { alive = false }
  }, [activeGroup?.id, activeGroupMonitors.length, historyReload])

  // Group bulk & management action handlers
  async function handleBulkPauseResume() {
    if (!activeGroup || activeGroupMonitors.length === 0) return
    setBusyAction('bulk-pause')
    try {
      const isResuming = activeGroupMonitors.every(m => m.paused)
      await Promise.all(
        activeGroupMonitors.map(m =>
          isResuming
            ? api.post(`/api/monitors/${m.id}/resume`)
            : api.post(`/api/monitors/${m.id}/pause`)
        )
      )
    } catch (err: any) {
      setActionError(err.message || 'Action failed')
    } finally {
      setBusyAction(null)
    }
  }

  async function handleBulkTest() {
    if (!activeGroup || activeGroupMonitors.length === 0) return
    setBusyAction('bulk-test')
    try {
      const results = await Promise.allSettled(
        activeGroupMonitors.map(m =>
          api.post('/api/monitors/test', {
            id: m.id,
            name: m.name,
            type: m.type,
            target: m.target,
            intervalS: m.intervalS,
            settings: {},
          })
        )
      )
      const failed = results.filter(r => r.status === 'rejected' || !normResult(r.value).ok).length
      setCopiedNotice(`${results.length - failed} checks passed; ${failed} failed or could not run.`)
    } catch (err: any) {
      setActionError(err.message || 'Action failed')
    } finally {
      setBusyAction(null)
    }
  }

  function openEditGroupModal() {
    if (!activeGroup || activeGroup.id <= 0) return
    setEditGroupName(activeGroup.name)
    setEditGroupDescription(activeGroup.description || '')
    setShowEditGroup(true)
  }

  async function handleUpdateGroup(e: React.FormEvent) {
    e.preventDefault()
    if (!activeGroup || activeGroup.id <= 0) return
    if (!editGroupName.trim()) return
    setBusyAction('update-group')
    try {
      await api.put(`/api/groups/${activeGroup.id}`, {
        name: editGroupName.trim(),
        description: editGroupDescription.trim(),
      })
      setShowEditGroup(false)
    } catch (err: any) {
      setActionError(err.message || 'Failed to update group')
    } finally {
      setBusyAction(null)
    }
  }

  async function handleDeleteGroup() {
    if (!activeGroup || activeGroup.id <= 0) return
    setBusyAction('delete-group')
    try {
      await api.del(`/api/groups/${activeGroup.id}`)
      setShowDeleteGroup(false)
      setLocation('/projects')
    } catch (err: any) {
      setActionError(err.message || 'Failed to delete project')
    } finally {
      setBusyAction(null)
    }
  }

  // Fetch latest heartbeat timing breakdown when active monitor changes
  useEffect(() => {
    if (!activeMonitor) {
      setLatestBeat(null)
      return
    }
    let alive = true
    setLatestBeat(null); setRecentBeats([])
    api.get<unknown>(`/api/monitors/${activeMonitor.id}/beats?limit=50`)
      .then(res => {
        if (!alive) return
        const beats = unwrapList<unknown>(res, 'beats').map(normHeartbeat)
        setRecentBeats(beats)
        setLatestBeat(beats[0] ?? null)
      })
      .catch(() => { if (alive) { setLatestBeat(null); setRecentBeats([]) } })
    return () => { alive = false }
  }, [activeMonitor?.id])

  // Load the recorded downtime for the selected monitor.
  useEffect(() => {
    if (!activeMonitor) {
      setMonitorOutages([])
      return
    }
    let alive = true
    setOutagesLoading(true)
    setOutagesError(false)
    api.get<{ outages?: any[] } | any[]>(`/api/monitors/${activeMonitor.id}/outages?limit=25`)
      .then(res => {
        const list = Array.isArray(res) ? res : res?.outages || []
        if (alive) setMonitorOutages(list)
      })
      .catch(() => { if (alive) setOutagesError(true) })
      .finally(() => { if (alive) setOutagesLoading(false) })
    return () => { alive = false }
  }, [activeMonitor?.id, historyReload])

  function handleCopy(text: string, label: string) {
    if (navigator.clipboard) {
      navigator.clipboard.writeText(text).then(() => {
        setCopiedNotice(`${label} copied.`)
        setTimeout(() => setCopiedNotice(null), 2500)
      }).catch(() => setActionError('Could not copy. Select the endpoint and copy it manually.'))
    }
  }

  async function handlePauseResume(m: MonitorSummary) {
    setBusyAction(`pause-${m.id}`)
    try {
      if (m.paused) {
        await api.post(`/api/monitors/${m.id}/resume`)
      } else {
        await api.post(`/api/monitors/${m.id}/pause`)
      }
    } catch (err: any) {
      setActionError(err.message || 'Action failed')
    } finally {
      setBusyAction(null)
    }
  }

  async function handleTestNow(m: MonitorSummary) {
    setBusyAction('test')
    setTestResult(null)
    try {
      // Test with the monitor's saved settings (keyword, accepted codes, SQL
      // query, ...). Bare settings would run a plain default check and pass
      // while the scheduled check fails.
      const full = await api.get<MonitorOut>(`/api/monitors/${m.id}`)
      const raw = await api.post('/api/monitors/test', { ...toMonitorIn(draftFromOut(full)), id: m.id })
      const r = normResult(raw)
      setTestResult(r)
    } catch (err: any) {
      setTestResult({ ok: false, reason: err.message || 'Check failed', latencyMs: -1, code: 0, at: Date.now(), errClass: '' as any, timing: null, cert: null, lossPct: 0, degraded: false })
    } finally {
      setBusyAction(null)
    }
  }

  async function handleDuplicate(m: MonitorSummary) {
    setBusyAction('dup')
    try {
      const res = await api.post<{ id: number }>(`/api/monitors/${m.id}/duplicate`)
      if (res?.id) {
        setLocation(`/monitors/${res.id}`)
      }
    } catch (err: any) {
      setActionError(err.message || 'Duplicate failed')
    } finally {
      setBusyAction(null)
    }
  }

  const [confirmDelete, setConfirmDelete] = useState<MonitorSummary | null>(null)

  async function handleDelete(m: MonitorSummary) {
    setBusyAction('del')
    try {
      await api.del(`/api/monitors/${m.id}`)
      setConfirmDelete(null)
      setLocation('/monitors')
    } catch (err: any) {
      setConfirmDelete(null)
      setActionError(err.message || 'Delete failed')
    } finally {
      setBusyAction(null)
    }
  }

  // Once the first data snapshot has arrived, an id that isn't in it doesn't exist.
  const missingMonitor = live.connected && selectedId != null && !activeMonitor
  const missingProject = live.connected && selectedId == null && selectedGroupId != null && selectedGroupId !== -1 && !activeGroup
  if (missingMonitor || missingProject) {
    return <div className="w-full">
      <h1 className="sr-only">{missingMonitor ? 'Monitor not found' : 'Project not found'}</h1>
      <EmptyState
        title={missingMonitor ? 'Monitor not found' : 'Project not found'}
        action={<Link href={missingMonitor ? '/monitors' : '/projects'}><Button>{missingMonitor ? 'Back to monitors' : 'Back to projects'}</Button></Link>}
      >
        It may have been deleted, or the address is wrong.
      </EmptyState>
    </div>
  }

  return <div className="w-full">
    <ConfirmModal
      open={confirmDelete !== null}
      title={`Delete ${confirmDelete?.name ?? 'this monitor'}?`}
      description="Its check history and outages are deleted with it. This can't be undone."
      confirmLabel="Delete monitor"
      onConfirm={async () => { if (confirmDelete) await handleDelete(confirmDelete) }}
      onClose={() => setConfirmDelete(null)}
    />
    {actionError && <div role="alert" className="text-sm text-danger bg-danger-bg rounded-[10px] px-3.5 py-2.5 mb-5">{actionError}</div>}
    {activeMonitor ? <div>            
            <PageTitle
              back={{ href: '/monitors', label: 'All monitors' }}
              title={activeMonitor.name}
              badges={<Badge>{activeMonitor.type}</Badge>}
              actions={isAdminUser && <>
                  <Button
                    variant="secondary"
                    
                    onClick={() => handleTestNow(activeMonitor)}
                    busy={busyAction === 'test'}
                    icon={<Icon.Bolt className="w-4 h-4 text-accent" />}
                  >
                    Run check
                  </Button>
                  <Button
                    variant="secondary"
                    
                    onClick={() => handlePauseResume(activeMonitor)}
                    busy={busyAction === `pause-${activeMonitor.id}`}
                    icon={activeMonitor.paused ? <Icon.Play className="w-4 h-4 text-ok" /> : <Icon.Pause className="w-4 h-4 text-muted" />}
                  >
                    {activeMonitor.paused ? 'Resume' : 'Pause'}
                  </Button>
                  <Link href={`/monitors/${activeMonitor.id}/edit`}>
                    <Button variant="secondary" size="sm" icon={<Icon.Edit className="w-4 h-4 text-muted" />}>
                      Edit
                    </Button>
                  </Link>
                  <Button
                    variant="secondary"
                    
                    onClick={() => handleDuplicate(activeMonitor)}
                    busy={busyAction === 'dup'}
                    icon={<Icon.Copy className="w-4 h-4 text-muted" />}
                  >
                    Duplicate
                  </Button>
                  <Button
                    variant="danger"
                    
                    onClick={() => setConfirmDelete(activeMonitor)}
                    busy={busyAction === 'del'}
                    icon={<Icon.Trash className="w-4 h-4" />}
                  >
                    Delete
                  </Button>
                </>}
            />

            {copiedNotice && (
              <div className="px-3 py-2 rounded-lg bg-accent-subtle border border-accent-border text-accent text-xs flex items-center justify-between animate-fade-in font-medium">
                <span className="flex items-center gap-1.5">
                  <Icon.Check className="w-3.5 h-3.5" />
                  {copiedNotice}
                </span>
                <button onClick={() => setCopiedNotice(null)} className="text-accent/60 hover:text-accent font-semibold ml-2" aria-label="Dismiss"><Icon.X className="w-3.5 h-3.5" /></button>
              </div>
            )}

            
            {testResult && (
              <div className={`p-3 rounded-xl border text-xs flex items-center justify-between ${
                testResult.ok ? 'bg-ok-bg border-ok/30 text-ok' : 'bg-danger-bg border-danger/30 text-danger'
              }`}>
                <div className="flex items-center gap-2">
                  {testResult.ok ? <Icon.Check className="w-4 h-4 text-ok shrink-0" /> : <Icon.AlertTriangle className="w-4 h-4 text-danger shrink-0" />}
                  <div>
                    <strong>{testResult.ok ? 'Check passed' : 'Check failed'}</strong>{' '}
                    <span className="text-body font-mono ml-1">
                      {testResult.ok ? `HTTP ${testResult.code ?? 200} in ${testResult.latencyMs ?? 0}ms` : testResult.reason || 'Check failed'}
                    </span>
                  </div>
                </div>
                <button onClick={() => setTestResult(null)} className="text-muted hover:text-body ml-2" aria-label="Dismiss"><Icon.X className="w-3.5 h-3.5" /></button>
              </div>
            )}

<div className="flex flex-col gap-4 mb-6">
              <MonitorHeaderCard m={activeMonitor} project={live.groups.find(g => g.id === activeMonitor.groupId)?.name} onCopy={handleCopy} refreshing={outagesLoading} onRefresh={() => setHistoryReload(n => n + 1)} />
              <MonitorStatStrip m={activeMonitor} outages={monitorOutages} loading={outagesLoading} />
            </div>
{activeMonitor.type === 'manual' && isAdminUser && <ManualCard monitorId={activeMonitor.id} current={activeMonitor.status} paused={activeMonitor.paused} />}<InvestigationTabs items={['Checks','Diagnostics','Reliability','Events','Runbook']} value={detailView} onChange={setDetailView} /><div className="grid xl:grid-cols-[minmax(0,1fr)_280px] gap-7 items-start"><div className="min-w-0 space-y-6">{detailView === 'Checks' && <>            
            <div className="p-5 rounded-[14px] bg-surface border border-default  space-y-3.5">
              <div className="flex items-center justify-between">
                <div>
                  <h3 className="text-base font-semibold text-body">Check results</h3>
                  <p className="text-sm text-muted mt-0.5">Recent outcomes and response times</p>
                </div>
                <span className="text-sm text-muted font-mono px-2 py-0.5 rounded-md bg-surface-2 border border-default/50">Last 100 checks</span>
              </div>
              <BeatInspector
                monitorId={activeMonitor.id}
                beats={activeMonitor.beats}
                count={100}
                lat={activeMonitor.lat}
                size="lg"
                intervalS={activeMonitor.intervalS}
                lastCheckAt={activeMonitor.lastCheckAt}
                onViewOutage={() => setDetailView('Events')}
              />
              <div className="flex items-center justify-between gap-3 text-xs text-muted border-t border-subtle/50 pt-2.5 font-mono">
                <span className="shrink-0">Older</span>
                <span className="min-w-0 text-right">Latest check: {activeMonitor.lastCheckAt ? dateServer(activeMonitor.lastCheckAt) : 'Never'}</span>
              </div>
            </div>

            
            {!activeMonitor.vendor && <UptimeHistoryCard monitorId={activeMonitor.id} uptime90d={activeMonitor.uptime90d} refreshKey={historyReload} />}
            <ResponseTimeCard monitorId={activeMonitor.id} />

</>}{detailView === 'Diagnostics' && <DiagnosticsTab monitor={activeMonitor} isAdmin={isAdminUser} fallbackBeats={latestBeat ? recentBeats : []} />}{detailView === 'Reliability' && <>            
            {activeMonitor.vendor ? <p className="p-5 rounded-[14px] bg-surface border border-default text-sm text-muted">Third-party feed: alerts only, not tracked as outages.</p> : <SlaCard scope="monitor" id={activeMonitor.id} />}

</>}{detailView === 'Events' && <>            
            {activeMonitor.vendor ? <p className="p-5 rounded-[14px] bg-surface border border-default text-sm text-muted">Third-party feed: alerts only, not tracked as outages.</p> : <div id="outage-history" className="p-5 rounded-[14px] bg-surface border border-default space-y-3 scroll-mt-20">
              <div className="flex items-center justify-between">
                <div>
                  <h3 className="text-base font-semibold text-body flex items-center gap-2">
                    <Icon.AlertTriangle className="w-3.5 h-3.5 text-warn" />
                    Outage timeline
                  </h3>
                  <p className="text-sm text-muted mt-0.5">Failures and recoveries recorded by your checks.</p>
                </div>
                <span className="text-xs text-muted font-mono">{monitorOutages.length} recorded</span>
              </div>

              {outagesError ? <div role="alert" className="text-sm text-danger">Outage history could not load. <Button size="sm" onClick={() => setHistoryReload(n => n + 1)}>Retry</Button></div> : outagesLoading ? (
                <SkeletonRows rows={3} framed={false} label="Loading incident history" />
              ) : monitorOutages.length === 0 ? (
                <EmptyState compact title="No outages recorded">No outages were found in the available history.</EmptyState>
              ) : (
                <OutageTimeline events={monitorOutages} />
              )}
            </div>}
</>}{detailView === 'Runbook' && <>            
            {activeMonitor.runbook && (
              <div className="p-5 rounded-[14px] bg-surface border border-default space-y-2">
                <h3 className="text-base font-semibold text-body">Operating notes</h3>
                <div className="text-xs text-muted whitespace-pre-wrap font-sans bg-surface-2 p-3 rounded-lg border border-default">
                  {activeMonitor.runbook}
                </div>
              </div>
            )}

{!activeMonitor.runbook && <EmptyState title="No runbook yet">Edit this monitor to add response steps and useful links.</EmptyState>}</>}</div><aside className="space-y-6"><section className="rounded-[14px] bg-surface border border-default px-5 py-2"><dl className="divide-y divide-default">{[
['Check type',activeMonitor.type.toUpperCase()],['Interval',`${activeMonitor.intervalS}s`],['Response',activeMonitor.lastCheckAt && !activeMonitor.paused && activeMonitor.latencyMs >= 0 ? ms(activeMonitor.latencyMs) : '—'],['Uptime · 24h',pct(activeMonitor.uptime24h)],['Uptime · 7d',pct(activeMonitor.uptime7d)],['Uptime · 30d',pct(activeMonitor.uptime30d)],['Uptime · 90d',pct(activeMonitor.uptime90d)],['TLS expires',activeMonitor.certDays == null ? 'Not available' : `${activeMonitor.certDays} days`],['Domain expires',activeMonitor.domainDays == null ? 'Not available' : `${activeMonitor.domainDays} days`]
].map(([label,value]) => <div key={label} className="flex justify-between gap-4 py-3 text-sm"><dt className="text-muted">{label}</dt><dd className="text-body tabular-nums text-right">{value}</dd></div>)}</dl></section>            
            {activeMonitor.status === 'down' && (
              <div className="p-4 rounded-[14px] bg-danger-bg border border-danger/30 text-sm space-y-2">
                <div className="font-bold text-danger text-sm flex items-center gap-1.5">
                  <span className="w-2 h-2 rounded-full bg-danger " />
                  Currently Down
                </div>
                <p className="text-body font-mono [overflow-wrap:anywhere]">{activeMonitor.reason || 'Check failed'}</p>
                {activeMonitor.suppressedBy != null && (
                  <p className="text-[12px] text-body-2">
                    Alerts held: its parent <strong>{live.monitors.get(activeMonitor.suppressedBy)?.name ?? `#${activeMonitor.suppressedBy}`}</strong> is down.
                    If the parent recovers first, this monitor's alert goes out then.
                  </p>
                )}
                <div className="text-muted text-[11px]">
                  Down since: {activeMonitor.since ? dateServer(activeMonitor.since) : 'Unknown'}
                </div>
              </div>
            )}

</aside></div></div> : activeGroup && groupStats ? <div>            
            <PageTitle
              back={{ href: '/projects', label: 'All projects' }}
              title={activeGroup.name}
              badges={<><StatusBadge status={groupStats.status} /><Badge>{groupStats.total} {groupStats.total === 1 ? 'Monitor' : 'Monitors'}</Badge></>}
              actions={isAdminUser && <>
                  <Button
                    variant="secondary"
                    
                    onClick={handleBulkTest}
                    busy={busyAction === 'bulk-test'}
                    disabled={activeGroupMonitors.length === 0}
                    icon={<Icon.Bolt className="w-4 h-4 text-accent" />}
                    title="Test all monitors in this project"
                  >
                    Check all
                  </Button>
                  <Button
                    variant="secondary"
                    
                    onClick={handleBulkPauseResume}
                    busy={busyAction === 'bulk-pause'}
                    disabled={activeGroupMonitors.length === 0}
                    icon={groupStats.allPaused ? <Icon.Play className="w-4 h-4 text-ok" /> : <Icon.Pause className="w-4 h-4 text-muted" />}
                    title={groupStats.allPaused ? 'Resume all monitors in this project' : 'Pause all monitors in this project'}
                  >
                    {groupStats.allPaused ? 'Resume all' : 'Pause all'}
                  </Button>
                  <Link href={activeGroup.id > 0 ? `/monitors/new?groupId=${activeGroup.id}` : '/monitors/new'}>
                    <Button variant="secondary" size="sm" icon={<Icon.Plus className="w-4 h-4" />}>
                      Add monitor
                    </Button>
                  </Link>
                  {activeGroup.id > 0 && (
                    <Button
                      variant="secondary"
                      
                      onClick={openEditGroupModal}
                      icon={<Icon.Edit className="w-4 h-4 text-muted" />}
                    >
                      Edit
                    </Button>
                  )}
                  {activeGroup.id > 0 && (
                    <Button
                      variant="danger"
                      
                      onClick={() => setShowDeleteGroup(true)}
                      icon={<Icon.Trash className="w-4 h-4" />}
                    >
                      Delete
                    </Button>
                  )}
                </>}
            >
              {activeGroup.description || 'Services, reliability and operating history for this project.'}
            </PageTitle>

<div className="flex flex-wrap gap-7 border-y border-default py-5 mb-4 text-sm"><span><strong>{groupStats.total}</strong> services</span><span className="text-ok">{groupStats.up} operational</span><span className="text-danger">{groupStats.down} down</span><span className="text-muted">{groupStats.paused} paused</span></div><InvestigationTabs items={['Services','Reliability','Events']} value={projectView} onChange={setProjectView} />{projectView === 'Services' && <><div className="grid xl:grid-cols-[minmax(0,1fr)_280px] gap-7 items-start"><section className="min-w-0 rounded-[14px] bg-surface border border-default overflow-hidden">{activeGroupMonitors.length === 0 ? <EmptyState bare title="No services yet">Add a monitor to start collecting results for this project.</EmptyState> : <ul className="divide-y divide-default">{servicePaging.rows.map(m => <li key={m.id} className={`flex items-center gap-4 ${LIST_ROW}`}><StatusBadge status={m.paused ? 'paused' : m.status} size="sm" /><Link href={`/monitors/${m.id}`} className="min-w-0 flex-1"><span className="text-sm font-medium hover:text-accent block">{m.name}</span><span className="text-[12px] text-muted mt-0.5 block font-mono truncate">{m.target}</span></Link><div className="hidden md:block w-32"><BeatBar beats={m.beats} lat={m.lat} count={20} size="sm" intervalS={m.intervalS} lastCheckAt={m.lastCheckAt} /></div><span className="text-sm text-muted tabular-nums hidden sm:block">{pct(m.uptime24h)}</span>{isAdminUser && <Button size="sm" disabled={!!busyAction} aria-label={`${m.paused ? 'Resume' : 'Pause'} ${m.name}`} onClick={() => handlePauseResume(m)}>{m.paused ? <Icon.Play className="w-4 h-4" /> : <Icon.Pause className="w-4 h-4" />}</Button>}</li>)}</ul>}{activeGroupMonitors.length > 25 && <div className="border-t border-default p-4"><Pager paging={servicePaging} noun="Services" /></div>}</section><aside className="text-sm"><h2 className="text-base font-semibold mb-3">Project summary</h2><dl className="divide-y divide-default"><div className="flex justify-between py-3"><dt className="text-muted">Average response</dt><dd>{groupStats.avgLatency == null ? '—' : ms(groupStats.avgLatency)}</dd></div><div className="flex justify-between py-3"><dt className="text-muted">Uptime · 24h</dt><dd>{pct(groupStats.avg24h)}</dd></div></dl><p className="text-muted leading-6 mt-5">Everyone in your workspace can view this project. Administrators manage its services.</p></aside></div></>}{projectView === 'Reliability' && <div className="grid xl:grid-cols-[minmax(0,1fr)_300px] gap-6"><div>            {activeGroup.id > 0 && <SlaCard scope="group" id={activeGroup.id} />}

</div><dl className="rounded-[14px] bg-surface border border-default px-5 py-2 divide-y divide-default">{[['24 hours',pct(groupStats.avg24h)],['7 days',pct(groupStats.avg7d)],['30 days',pct(groupStats.avg30d)],['90 days',pct(groupStats.avg90d)],['Average response',groupStats.avgLatency == null ? '—' : ms(groupStats.avgLatency)]].map(([label,value]) => <div key={label} className="flex justify-between py-3 text-sm"><dt className="text-muted">{label}</dt><dd>{value}</dd></div>)}</dl></div>}{projectView === 'Events' && <>            
            <div className="p-5 rounded-[14px] bg-surface border border-default space-y-3">
              <div className="flex items-center justify-between">
                <div>
                  <h3 className="text-base font-semibold text-body flex items-center gap-2">
                    <Icon.AlertTriangle className="w-3.5 h-3.5 text-warn" />
                    Project outage history
                  </h3>
                  <p className="text-sm text-muted mt-0.5">Downtime records across monitors in {activeGroup.name}</p>
                </div>
                <span className="text-xs text-muted font-mono">{groupOutages.length} recorded</span>
              </div>

              {groupOutagesError ? <p role="alert" className="text-sm text-danger">Outage history could not load. <Button size="sm" onClick={() => setHistoryReload(n => n + 1)}>Retry</Button></p> : groupOutagesLoading ? (
                <SkeletonRows rows={3} framed={false} label="Loading incident history" />
              ) : groupOutages.length === 0 ? (
                <EmptyState compact title="No outages recorded in this project">No outages were found in the available history.</EmptyState>
              ) : (
                <OutageTimeline events={groupOutages} name={id => live.monitors.get(id)?.name ?? `Monitor ${id}`} />
              )}
            </div>
</>}</div> : <EmptyState heading title="Select a service" action={<Link href="/monitors"><Button>Open monitors</Button></Link>}>Open the monitor inventory or a project to investigate service health.</EmptyState>}
          
      {showEditGroup && activeGroup && (
        <Modal open={showEditGroup} size="lg" title={`Edit project: ${activeGroup.name}`} onClose={() => setShowEditGroup(false)} footer={<>
          <Button type="button" variant="secondary" onClick={() => setShowEditGroup(false)}>Cancel</Button>
          <Button type="submit" form="edit-group-form" variant="primary" busy={busyAction === 'update-group'}>Save project</Button>
        </>}>
          <form id="edit-group-form" onSubmit={handleUpdateGroup} className="grid grid-cols-1 items-start gap-x-5 gap-y-5 sm:grid-cols-2">
            <Field label="Project name">
              <Input
                value={editGroupName}
                onChange={e => setEditGroupName(e.target.value)}
                placeholder="Project name"
                required
                autoFocus
              />
            </Field>
            <Field label="Description (Optional)" hint="Brief description or purpose of this project">
              <Input
                value={editGroupDescription}
                onChange={e => setEditGroupDescription(e.target.value)}
                placeholder="e.g. Core backend APIs and authentication services"
              />
            </Field>

          </form>
        </Modal>
      )}

      
      {showDeleteGroup && activeGroup && (
        <Modal open={showDeleteGroup} title={`Delete project: ${activeGroup.name}`} onClose={() => setShowDeleteGroup(false)} footer={<>
          <Button type="button" variant="secondary" onClick={() => setShowDeleteGroup(false)}>Cancel</Button>
          <Button type="button" variant="danger" onClick={handleDeleteGroup} busy={busyAction === 'delete-group'} icon={<Icon.Trash className="w-4 h-4" />}>Delete project</Button>
        </>}>
          <div className="space-y-4">
            <div className="px-3.5 py-2.5 rounded-[10px] bg-danger-bg text-[13px] text-danger space-y-1">
              <div className="font-bold">Are you sure you want to delete this project?</div>
              <p className="text-body font-normal">
                Monitors assigned to this project will <strong>not</strong> be deleted. They will simply be moved to Unassigned.
              </p>
            </div>

          </div>
        </Modal>
      )}

  </div>
}
