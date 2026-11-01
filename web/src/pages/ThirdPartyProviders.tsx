import { useEffect, useMemo, useState } from 'react'
import { PageTitle } from '../components/PageShell'
import ProviderLogo from '../components/ProviderLogo'
import { Pager, usePaging } from '../components/Pager'
import { Badge, Button, ConfirmModal, DropdownMenu, EmptyState, FilterChips, Icon, Input, Modal, Select, Tabs, Timestamp, useToast } from '../components/ui'
import { draftFromOut, toMonitorIn } from '../lib/monitorForm'
import { api, errorText } from '../lib/api'
import { channelLabel } from '../lib/channels'
import { normResult } from '../lib/normalize'
import { useLive } from '../lib/live'
import { canWrite } from '../lib/roles'
import type { Channel, Me, MonitorOut, MonitorSummary } from '../lib/types'

import { isThirdPartyMonitor } from '../lib/vendor'
import { PROVIDER_PRESETS as PRESETS, PROVIDER_CATEGORIES, providerSourceLabel, type ProviderCategory } from '../lib/providers'


const CARD_SIZES = [12, 24, 48] as const

/** The reason the checker reports for a vendor feed it could not fetch or understand (internal/check.VendorUnreadableReason). */
const UNREADABLE = 'Could not read this feed'

export default function ThirdPartyProviders({ user }: { user: Me }) {
  const live = useLive()
  const admin = canWrite(user) // admin or user
  const [view, setView] = useState<'connected' | 'catalog'>('connected')
  const [channels, setChannels] = useState<Channel[]>([])
  const [search, setSearch] = useState('')
  const [presetSearch, setPresetSearch] = useState('')
  const [category, setCategory] = useState<ProviderCategory | 'All'>('All')
  const [modalOpen, setModalOpen] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [testResult, setTestResult] = useState<{ id: number; ok: boolean; reason?: string; latency?: number } | null>(null)
  const [testingId, setTestingId] = useState<number | null>(null)
  const toast = useToast()
  const [removing, setRemoving] = useState<MonitorSummary | null>(null)
  const [editing, setEditing] = useState<MonitorOut | null>(null)

  // Form draft
  const [providerUrl, setProviderUrl] = useState('')
  const [providerName, setProviderName] = useState('')
  const [intervalS, setIntervalS] = useState(60)
  const [selectedChannelIds, setSelectedChannelIds] = useState<number[]>([])
  const [formError, setFormError] = useState<string | null>(null)

  useEffect(() => { if (modalOpen) setPresetSearch('') }, [modalOpen])

  function closeModal() { setModalOpen(false); setEditing(null); setFormError(null) }

  async function openEdit(m: MonitorSummary) {
    try {
      const full = await api.get<MonitorOut>(`/api/monitors/${m.id}`)
      setEditing(full)
      setProviderUrl(full.target); setProviderName(full.name); setIntervalS(full.intervalS ?? 60)
      setSelectedChannelIds(full.channelIds ?? []); setFormError(null); setModalOpen(true)
    } catch (err) { toast.push(errorText(err, 'Could not open the feed'), 'danger') }
  }

  // Fetch channels for alert routing
  useEffect(() => {
    api.get<{ channels?: Channel[] } | Channel[]>('/api/channels')
      .then(res => {
        if (Array.isArray(res)) setChannels(res)
        else if (res?.channels) setChannels(res.channels)
      })
      .catch(() => {})
  }, [])

  // Filter 3rd party monitors
  const providerMonitors = useMemo(() => {
    return Array.from(live.monitors.values()).filter(m => isThirdPartyMonitor(m, live.groups))
  }, [live.monitors, live.groups])

  const filtered = useMemo(() => {
    if (!search.trim()) return providerMonitors
    const q = search.toLowerCase()
    return providerMonitors.filter(m => m.name.toLowerCase().includes(q) || m.target.toLowerCase().includes(q))
  }, [providerMonitors, search])

  function isVendorIncident(m: { status: string; reason?: string | null; paused?: boolean }): boolean {
    if (m.paused) return false
    // Ignore HTTP latency warnings: network latency downloading a vendor's status page is NOT a provider incident!
    if (m.reason && (m.reason.startsWith('slow:') || m.reason.includes('ms >'))) return false
    if (m.status === 'down') return true
    if (m.status === 'degraded') return true
    if (m.reason && (m.reason.toLowerCase().includes('outage') || m.reason.toLowerCase().includes('disrupt') || m.reason.toLowerCase().includes('degraded'))) {
      return true
    }
    return false
  }

  // Active provider incidents
  const activeIncidents = useMemo(() => {
    return providerMonitors.filter(m => isVendorIncident(m))
  }, [providerMonitors])

  const catalog = useMemo(() => {
    const q = search.trim().toLowerCase()
    return PRESETS.filter(p => (category === 'All' || p.category === category) && (!q || (p.name + ' ' + p.desc + ' ' + p.category).toLowerCase().includes(q)))
  }, [search, category])

  const catalogPaging = usePaging(catalog, { pageSize: 12, resetKey: `${search}|${category}` })
  const feedPaging = usePaging(filtered, { pageSize: 12, resetKey: search })

  // Auto suggest name from URL
  function handleUrlChange(url: string) {
    setProviderUrl(url)
    if (!editing && (!providerName || PRESETS.some(p => p.name === providerName))) {
      const preset = PRESETS.find(p => p.url.toLowerCase() === url.toLowerCase())
      if (preset) {
        setProviderName(preset.name)
        return
      }
      try {
        const u = new URL(url)
        const host = u.hostname.replace(/^www\./, '')
        if (host.includes('githubstatus')) setProviderName('GitHub Status')
        else if (host.includes('metastatus')) {
          const part = u.pathname.replace(/^\/+|\/+$/g, '').replace(/-/g, ' ')
          setProviderName(`Meta ${part ? part.toUpperCase() : 'Status'}`)
        } else if (host.includes('firebase')) setProviderName('Google Firebase')
        else if (host.includes('cloudflarestatus')) setProviderName('Cloudflare Status')
        else if (host.includes('slack')) setProviderName('Slack Status')
        else if (host.startsWith('status.')) {
          const brand = host.split('.')[1]
          setProviderName(`${brand.charAt(0).toUpperCase() + brand.slice(1)} Status`)
        } else {
          setProviderName(host)
        }
      } catch {
        // ignore incomplete url
      }
    }
  }

  function handleSelectPreset(p: typeof PRESETS[0]) {
    setProviderUrl(p.url)
    setProviderName(p.name)
    setIntervalS(60)
  }

  async function handleAddProvider(e: React.FormEvent) {
    e.preventDefault()
    if (!providerUrl.trim() || !providerName.trim()) {
      setFormError('Provider URL and Name are required.')
      return
    }
    setSubmitting(true)
    setFormError(null)

    try {
      if (editing) {
        // Keep everything the feed already has (vendor flag, provider, components, group) and change only what this form edits.
        await api.put(`/api/monitors/${editing.id}`, { ...toMonitorIn(draftFromOut(editing)), name: providerName.trim(), target: providerUrl.trim(), intervalS, channelIds: selectedChannelIds })
        toast.push(`Saved ${providerName.trim()}.`, 'ok')
        closeModal()
        return
      }
      // 1. Ensure 3rd Party group exists
      const existing3rdPartyGroup = live.groups.find(g => {
        const n = g.name.toLowerCase()
        return n.includes('3rd') || n.includes('third') || n.includes('vendor') || n.includes('provider')
      })
      let targetGroupId = existing3rdPartyGroup?.id
      if (!targetGroupId) {
        const createdGroup = await api.post<{ id: number }>('/api/groups', {
          name: '3rd Party Providers',
        })
        targetGroupId = createdGroup.id
      }

      // 2. Create the monitor
      await api.post('/api/monitors', {
        name: providerName.trim(),
        type: 'http',
        target: providerUrl.trim(),
        intervalS: intervalS,
        groupId: targetGroupId,
        channelIds: selectedChannelIds,
        settings: {
          acceptedCodes: '200-299',
          vendor: true, // alert-only 3rd-party status feed
        },
      })

      closeModal()
      setProviderUrl('')
      setProviderName('')
      setSelectedChannelIds([])
    } catch (err: any) {
      setFormError(errorText(err, 'Failed to add provider monitor.'))
    } finally {
      setSubmitting(false)
    }
  }

  async function handleTestFeed(m: MonitorSummary) {
    setTestingId(m.id)
    setTestResult(null)
    try {
      // Test with the feed's saved settings (vendor flag, provider, components):
      // sending bare settings would run a plain HTTP check of the feed URL and
      // say "no incident" while the real check reports one.
      const full = await api.get<MonitorOut>(`/api/monitors/${m.id}`)
      const raw = await api.post<unknown>('/api/monitors/test', { ...toMonitorIn(draftFromOut(full)), id: m.id })
      const r = normResult(raw)
      const unreadable = r.reason === UNREADABLE
      setTestResult({
        id: m.id,
        ok: r.ok && !unreadable,
        reason: unreadable ? 'Could not fetch or understand this feed. Check the address.' : r.reason || (r.ok ? (r.degraded ? 'Performance degraded on vendor status feed' : 'No incident reported by this feed') : 'Outage detected on vendor status feed'),
        latency: r.latencyMs,
      })
    } catch (err: any) {
      setTestResult({
        id: m.id,
        ok: false,
        reason: err.message || 'Check failed',
      })
    } finally {
      setTestingId(null)
    }
  }

  async function handleTogglePause(m: MonitorSummary) {
    try {
      if (m.paused) {
        await api.post(`/api/monitors/${m.id}/resume`)
      } else {
        await api.post(`/api/monitors/${m.id}/pause`)
      }
    } catch (err) {
      toast.push(errorText(err, 'Could not toggle pause'), 'danger')
    }
  }

  async function handleDelete(m: MonitorSummary) {
    try {
      await api.del(`/api/monitors/${m.id}`)
      toast.push(`Removed ${m.name}.`, 'ok')
    } catch (err) {
      toast.push(errorText(err, 'Could not remove the feed'), 'danger')
    } finally {
      setRemoving(null)
    }
  }

  return (
    <div className="space-y-7">
      <PageTitle title="External services" actions={admin ? <Button variant="primary" icon={<Icon.Plus className="w-4 h-4" />} onClick={() => setModalOpen(true)}>Connect a feed</Button> : undefined}>Follow the providers your infrastructure depends on.</PageTitle>
      <div className="flex flex-wrap items-center justify-between gap-4">
        <Tabs label="Provider views" active={view} onChange={setView} tabs={[{ key: 'connected', label: <>Connected feeds <span className="tnum ml-1">{providerMonitors.length}</span></> }, { key: 'catalog', label: 'Browse providers' }]} />
        <Input aria-label="Search providers" value={search} onChange={e => setSearch(e.target.value)} placeholder="Search provider or service" className="w-full sm:w-72" />
      </div>
      <div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-[12px] text-muted">
        <span>{view === 'catalog' ? catalog.length + (catalog.length === PRESETS.length ? '' : ' of ' + PRESETS.length) + ' available providers' : providerMonitors.length + ' connected feeds'}</span>
        {view !== 'catalog' && <>
          <span className={activeIncidents.length ? 'text-danger' : ''}>{activeIncidents.length} reporting incidents</span>
          <span>{providerMonitors.filter(m => m.paused).length} paused</span>
        </>}
      </div>
      {view === 'catalog' ? <>
        <FilterChips label="Provider category" value={category} onChange={setCategory} options={(['All', ...PROVIDER_CATEGORIES] as const).map(c => ({ key: c, label: c }))} />
        <section aria-label="Provider catalog" className="grid grid-cols-2 lg:grid-cols-3 gap-2.5 sm:gap-3">
          {catalogPaging.rows.map(p => <article key={p.url} className="min-w-0 flex flex-col gap-2 rounded-[14px] border border-default bg-surface p-5">
            <div className="flex items-center gap-2.5"><ProviderLogo url={p.url} name={p.name} size="sm" /><h2 className="min-w-0 flex-1 text-sm font-semibold leading-snug [overflow-wrap:anywhere]">{p.name}</h2></div>
            <p className="text-[12px] text-muted leading-snug line-clamp-2 min-h-[2.1rem]">{p.desc}</p>
            <div className="mt-auto flex items-center justify-between gap-2 pt-1"><span className="min-w-0 truncate text-[12px] text-muted">{providerSourceLabel(p.url)}</span>{admin && <Button size="sm" onClick={() => { handleSelectPreset(p); setModalOpen(true) }}>Connect</Button>}</div>
          </article>)}
        </section>
        <Pager paging={catalogPaging} sizes={CARD_SIZES} noun="Providers" />
        {catalog.length === 0 && <EmptyState title="No matching providers">Try another provider or service name, or connect a custom feed.</EmptyState>}
      </> : filtered.length === 0 ? <EmptyState title={search ? 'No matching feeds' : 'No connected providers'} action={admin ? <Button onClick={() => setView('catalog')}>Browse providers</Button> : undefined}>Connect a provider status page or RSS feed to follow its reported incidents.</EmptyState> : <section aria-label="Connected provider feeds" className={`grid gap-4 ${feedPaging.rows.length === 1 ? 'mx-auto w-full max-w-md grid-cols-1' : feedPaging.rows.length === 2 ? 'mx-auto w-full max-w-3xl grid-cols-1 sm:grid-cols-2' : 'grid-cols-1 sm:grid-cols-2 lg:grid-cols-3'}`}>{feedPaging.rows.map(m => {
        const incident = isVendorIncident(m)
        const checked = m.lastCheckAt != null && m.lastCheckAt > 0
        const unreadable = !m.paused && !incident && m.reason === UNREADABLE
        return <article key={m.id} className="min-w-0 flex flex-col gap-3 rounded-[14px] border border-default bg-surface p-5">
          <div className="flex items-start gap-2.5"><ProviderLogo url={m.target} name={m.name} size="sm" /><div className="min-w-0 flex-1"><h2 className="text-sm font-semibold leading-snug [overflow-wrap:anywhere]">{m.name}</h2><a href={m.target} target="_blank" rel="noopener noreferrer" className="mt-0.5 flex items-center gap-1 text-[12px] text-muted hover:text-accent"><span className="truncate">{m.target.replace(/^https?:\/\//, '').replace(/\/$/, '')}</span><Icon.External className="w-3 h-3 shrink-0" /></a></div></div>
          <div><Badge tone={m.paused ? 'neutral' : !checked ? 'neutral' : incident ? 'danger' : unreadable ? 'warn' : 'ok'}>{m.paused ? 'Paused' : !checked ? 'Awaiting check' : incident ? 'Provider incident' : unreadable ? 'Feed unreachable' : 'No reported incidents'}</Badge></div>
          {unreadable && <p className="text-[12px] text-body-2 leading-relaxed">Opskest could not fetch or understand this feed, so it cannot tell whether the provider has an incident. Check the address and try again. No alert is sent for this.</p>}
          {incident && <p className="text-[12px] text-body-2 leading-relaxed">{m.reason || 'The provider feed reports a service disruption.'}</p>}
          {testResult?.id === m.id && <div role="status" className={'flex items-start gap-3 rounded-lg p-3 text-[12px] ' + (testResult.ok ? 'bg-ok-bg text-ok' : 'bg-danger-bg text-danger')}><p className="flex-1 break-words">{testResult.reason || (testResult.ok ? 'Feed checked successfully.' : 'Feed check failed.')}</p><button aria-label="Dismiss check result" onClick={() => setTestResult(null)}><Icon.X className="w-4 h-4" /></button></div>}
          <div className="mt-auto border-t border-default pt-2.5 space-y-2.5"><p className="text-[12px] text-muted tnum leading-snug">Every {m.intervalS}s · {checked ? <Timestamp at={m.lastCheckAt} prefix="Checked" future={false} /> : 'No checks received'}{m.uptime24h != null ? ' · 24h ' + m.uptime24h.toFixed(2) + '%' : ''}</p>{admin && <div className="flex flex-wrap items-center gap-2"><Button size="sm" busy={testingId === m.id} onClick={() => handleTestFeed(m)}>Check now</Button><Button size="sm" onClick={() => openEdit(m)}>Edit</Button><DropdownMenu className="ml-auto" label={`More actions for ${m.name}`} items={[
            { key: 'pause', label: m.paused ? 'Resume' : 'Pause', icon: m.paused ? <Icon.Play className="h-3 w-3" /> : <Icon.Pause className="h-3 w-3" />, onSelect: () => handleTogglePause(m) },
            { key: 'sep', separator: true },
            { key: 'remove', label: 'Remove feed', icon: <Icon.Trash className="h-3.5 w-3.5" />, tone: 'danger', onSelect: () => setRemoving(m) },
          ]} /></div>}</div>
        </article>
      })}</section>}
      {view !== 'catalog' && filtered.length > 0 && <Pager paging={feedPaging} sizes={CARD_SIZES} noun="Feeds" />}
      <p className="text-[12px] text-muted max-w-[80ch] leading-relaxed">Provider feeds report the service owner's incidents. They complement direct checks of your own services. Alert delivery follows the channels assigned to each feed.</p>

      <ConfirmModal
        open={removing !== null}
        title={`Remove ${removing?.name ?? 'this feed'}?`}
        description="It stops following this provider and its alerts. The feed's history is deleted with it."
        confirmLabel="Remove feed"
        onConfirm={async () => { if (removing) await handleDelete(removing) }}
        onClose={() => setRemoving(null)}
      />

      {/* Add Provider Modal */}
      {modalOpen && (
        <Modal open={modalOpen} onClose={closeModal} size="xl" title={editing ? 'Edit provider feed' : 'Connect provider feed'}
          footer={<>
            <Button type="button" variant="ghost" onClick={closeModal}>Cancel</Button>
            <Button type="submit" form="provider-form" variant="primary" disabled={submitting}>
              {submitting ? (editing ? 'Saving…' : 'Adding Provider…') : (editing ? 'Save changes' : 'Add Provider')}
            </Button>
          </>}>
          <form id="provider-form" onSubmit={handleAddProvider} className="grid items-start gap-x-5 gap-y-4 text-[12px] md:grid-cols-2">
            {formError && <div className="md:col-span-2 rounded-[10px] bg-danger-bg px-3.5 py-2.5 text-danger">{formError}</div>}

            <div className="flex flex-col gap-1 md:col-span-2">
              <label htmlFor="provider-url" className="font-semibold text-body">Status page or RSS feed URL</label>
              <Input
                id="provider-url"
                value={providerUrl}
                onChange={e => handleUrlChange(e.target.value)}
                placeholder="https://www.githubstatus.com/ or https://metastatus.com/graph-api or .rss"
                required
                className="font-mono text-xs"
              />
              <span className="text-[11px] text-faint">Paste any Statuspage.io, MetaStatus, Firebase, Slack, or RSS/Atom feed URL.</span>
            </div>

            <div className="flex flex-col gap-1">
              <label htmlFor="provider-name" className="font-semibold text-body">Provider name</label>
              <Input id="provider-name" value={providerName} onChange={e => setProviderName(e.target.value)} placeholder="e.g. GitHub Actions & Git" required />
            </div>

            <div className="flex flex-col gap-1">
              <label htmlFor="provider-interval" className="font-semibold text-body">Check interval</label>
              <Select id="provider-interval" value={intervalS} onChange={e => setIntervalS(Number(e.target.value))}>
                {![30, 60, 120, 300].includes(intervalS) && <option value={intervalS}>Every {intervalS} seconds (current)</option>}
                <option value={30}>Every 30 seconds (High frequency)</option>
                <option value={60}>Every 1 minute (Recommended)</option>
                <option value={120}>Every 2 minutes</option>
                <option value={300}>Every 5 minutes</option>
              </Select>
            </div>

            {!editing && <div className="flex flex-col gap-1.5 md:col-span-2">
              <label htmlFor="provider-preset-search" className="font-semibold text-muted text-[11px]">Provider presets</label>
              <Input id="provider-preset-search" dense value={presetSearch} onChange={e => setPresetSearch(e.target.value)} placeholder="Find a provider…" />
              <div className="grid h-40 grid-cols-2 content-start gap-1.5 overflow-y-auto rounded-lg border border-default bg-surface-2/30 p-1 sm:grid-cols-4">
                {PRESETS.filter(p => (p.name + ' ' + p.desc).toLowerCase().includes(presetSearch.toLowerCase())).map(p => (
                  <button key={p.name} type="button" onClick={() => handleSelectPreset(p)}
                    className="flex h-[76px] flex-col items-center justify-center gap-1 rounded-lg border border-transparent p-1.5 text-center transition-colors hover:border-default hover:bg-surface">
                    <ProviderLogo url={p.url} name={p.name} size="sm" />
                    <span className="w-full text-[11px] font-medium leading-tight text-body [overflow-wrap:anywhere] line-clamp-2">{p.name}</span>
                  </button>
                ))}
              </div>
            </div>}

            {channels.length > 0 && (
              <div className="flex flex-col gap-1.5 md:col-span-2">
                <span className="font-semibold text-body">Alert channels</span>
                <span className="text-[11px] text-faint">Choose where to send provider incident alerts ({channels.length} available).</span>
                <div className="grid max-h-36 grid-cols-1 gap-1 overflow-y-auto rounded-lg border border-default bg-surface-2/30 p-2 sm:grid-cols-2">
                  {channels.map(ch => {
                    const checked = selectedChannelIds.includes(ch.id)
                    return (
                      <label key={ch.id} className="flex min-w-0 cursor-pointer items-center gap-2 rounded-lg p-1.5 hover:bg-surface">
                        <input
                          type="checkbox"
                          checked={checked}
                          onChange={e => {
                            if (e.target.checked) setSelectedChannelIds(prev => [...prev, ch.id])
                            else setSelectedChannelIds(prev => prev.filter(id => id !== ch.id))
                          }}
                          className="shrink-0 rounded-md text-accent"
                        />
                        <span className="min-w-0 flex-1 truncate font-medium text-body" title={ch.name}>{ch.name}</span>
                        <span className="shrink-0 whitespace-nowrap text-[11px] text-muted">{channelLabel(ch.type)}</span>
                      </label>
                    )
                  })}
                </div>
              </div>
            )}
          </form>
        </Modal>
      )}
    </div>
  )
}
