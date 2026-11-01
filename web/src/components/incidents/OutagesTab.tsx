// Incidents → Outages: the summary row, then search and filters
// (status, note, minimum duration, monitor, group, server date range) over the
// paginated outage table. The main job here is writing down why each outage
// happened, so "Needs a note" is one click away. Filters live in the URL so a
// filtered view can be shared.

import { useEffect, useMemo, useState } from 'react'
import { useLocation, useSearch } from 'wouter'
import { serverDayStartMs, serverDayEndMs, serverDayOffsetMs, ymdServer } from '../../lib/format'
import { serverTimeZone } from '../../lib/runtime'
import { useLive } from '../../lib/live'
import type { Me } from '../../lib/types'
import { OutageSummary } from '../OutageSummary'
import { OutageTable, type OutageFilters } from '../OutageTable'
import { DateRangeInput } from '../DateInputs'
import { Icon, Input, SegmentedControl, Select } from '../ui'

type StatusF = '' | 'ongoing' | 'resolved'
type NoteF = '' | 'missing' | 'present'

/** Adapts the {k, label} option shape used below to the shared segmented control. */
function Segmented<K extends string>({ value, options, onChange, label }: {
  value: K
  options: { k: K; label: string }[]
  onChange: (k: K) => void
  label: string
}) {
  return <SegmentedControl size="sm" label={label} value={value} onChange={onChange} options={options.map(o => ({ key: o.k, label: o.label }))} />
}

const PRESETS: { k: string; label: string; days: number }[] = [
  { k: 'today', label: 'Today', days: 1 },
  { k: '7d', label: '7 days', days: 7 },
  { k: '30d', label: '30 days', days: 30 },
]

export function OutagesTab({ user }: { user: Me }) {
  const live = useLive()
  const search = useSearch()
  const [location, navigate] = useLocation()
  const params = useMemo(() => new URLSearchParams(search), [search])
  const get = (k: string) => params.get(k) ?? ''

  const [text, setText] = useState(get('q'))
  const [refreshKey, setRefreshKey] = useState(0)

  const status = (['ongoing', 'resolved'].includes(get('status')) ? get('status') : '') as StatusF
  const note = (['missing', 'present'].includes(get('note')) ? get('note') : '') as NoteF
  const monitorId = get('monitorId')
  const groupId = get('groupId')
  const minDuration = get('min')
  const from = get('from')
  const to = get('to')
  const q = get('q')

  /** Writes filters into the URL (replace, so Back leaves the page). */
  function set(patch: Record<string, string>) {
    const next = new URLSearchParams(params)
    next.set('tab', 'outages')
    for (const [k, v] of Object.entries(patch)) {
      if (v) next.set(k, v)
      else next.delete(k)
    }
    navigate(`${location}?${next}`, { replace: true })
  }

  // Search is debounced into the URL.
  useEffect(() => {
    if (text.trim() === q) return
    const t = window.setTimeout(() => set({ q: text.trim() }), 300)
    return () => window.clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [text])

  const monitorOptions = useMemo(
    () => [...live.monitors.values()].filter(m => !m.vendor).map(m => ({ id: m.id, name: m.name })).sort((a, b) => a.name.localeCompare(b.name)),
    [live.monitors],
  )
  const monitorName = (id: number) => live.monitors.get(id)?.name ?? 'Deleted monitor'
  const monitorGroup = (id: number) => live.monitors.get(id)?.groupId

  const filters: OutageFilters = useMemo(() => {
    // Calendar boundaries follow the server, including short and long DST days.
    const fromMs = from ? serverDayStartMs(from) : null
    const toStart = to ? serverDayStartMs(to) : null
    return {
      active: status === 'ongoing' || undefined,
      monitorId: monitorId ? Number(monitorId) : undefined,
      groupId: groupId ? Number(groupId) : undefined,
      from: fromMs ?? undefined,
      to: toStart != null ? serverDayEndMs(toStart) - 1 : undefined,
      q: q || undefined,
      note: note || undefined,
      minDuration: minDuration ? Number(minDuration) : undefined,
      resolvedOnly: status === 'resolved' || undefined,
    }
  }, [status, monitorId, groupId, from, to, q, note, minDuration])

  const today = ymdServer(Date.now())
  const preset = PRESETS.find(p => to === today && from === ymdServer(serverDayOffsetMs(Date.now(), -(p.days - 1))))?.k ?? (from || to ? 'custom' : '')
  const filtered = status || note || monitorId || groupId || minDuration || from || to || q

  return (
    <div className="flex flex-col gap-5 mt-4">
      <OutageSummary
        monitorName={monitorName}
        refreshKey={refreshKey}
        needsNoteActive={note === 'missing'}
        onNeedsNote={() => set(note === 'missing' ? { note: '', status: '' } : { note: 'missing', status: 'resolved', from: ymdServer(serverDayOffsetMs(Date.now(), -29)), to: today })}
      />

      <div className="flex flex-col gap-2.5" role="group" aria-label="Outage filters">
        <div className="flex flex-wrap items-center gap-2">
          <label className="relative flex-1 min-w-[220px] max-w-md">
            <Icon.Search className="w-4 h-4 text-faint absolute left-2.5 top-1/2 -translate-y-1/2 pointer-events-none" />
            <Input
              type="search"
              value={text}
              onChange={e => setText(e.target.value)}
              placeholder="Search monitor, reason or note…"
              aria-label="Search outages"
              className="pl-8 w-full"
            />
          </label>
          <Segmented<StatusF> label="Status" value={status} onChange={v => set({ status: v })}
            options={[{ k: '', label: 'All' }, { k: 'ongoing', label: 'Ongoing' }, { k: 'resolved', label: 'Resolved' }]} />
          <Segmented<NoteF> label="Note" value={note} onChange={v => set({ note: v })}
            options={[{ k: '', label: 'Any note' }, { k: 'missing', label: 'Needs note' }, { k: 'present', label: 'Has note' }]} />
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Select aria-label="Filter by monitor" value={monitorId} onChange={e => set({ monitorId: e.target.value })} className="max-w-[220px]">
            <option value="">All monitors</option>
            {monitorOptions.map(m => <option key={m.id} value={m.id}>{m.name}</option>)}
          </Select>
          <Select aria-label="Filter by group" value={groupId} onChange={e => set({ groupId: e.target.value })} className="max-w-[190px]">
            <option value="">All groups</option>
            {[...live.groups].sort((a, b) => a.sort - b.sort).map(g => <option key={g.id} value={g.id}>{g.name}</option>)}
          </Select>
          <Select aria-label="Minimum duration" value={minDuration} onChange={e => set({ min: e.target.value })} className="max-w-[170px]">
            <option value="">Any duration</option>
            <option value="1">Longer than 1 min</option>
            <option value="5">Longer than 5 min</option>
            <option value="15">Longer than 15 min</option>
            <option value="60">Longer than 1 hour</option>
          </Select>
          <span aria-hidden="true" className="w-px h-6 bg-default mx-1 hidden sm:block" />
          <Segmented<string> label="Date range" value={preset === 'custom' ? '' : preset} onChange={k => {
            const p = PRESETS.find(x => x.k === k)
            set(p ? { from: ymdServer(serverDayOffsetMs(Date.now(), -(p.days - 1))), to: today } : { from: '', to: '' })
          }} options={[{ k: '', label: 'All time' }, ...PRESETS.map(p => ({ k: p.k, label: p.label }))]} />
          <DateRangeInput
            start={from}
            end={to}
            startLabel={`From date (${serverTimeZone()})`}
            endLabel={`To date (${serverTimeZone()})`}
            onChange={(a, b) => set({ from: a, to: b })}
          />
          {filtered && (
            <button type="button" onClick={() => { setText(''); navigate('/incidents?tab=outages', { replace: true }) }}
              className="inline-flex items-center gap-1 text-[12px] text-muted hover:text-body">
              <Icon.X className="w-3.5 h-3.5" /> Clear filters
            </button>
          )}
        </div>
      </div>

      <OutageTable
        filters={filters}
        viewerLabel={user.name || user.email}
        showMonitor
        monitorName={monitorName}
        monitorGroup={monitorGroup}
        onNoted={() => setRefreshKey(k => k + 1)}
      />
    </div>
  )
}
