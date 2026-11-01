// A status page's ordered items: groups (every monitor in them) and single
// monitors, each with an optional display-name override; add, remove and
// move up/down. Names come from the live store, so no extra fetches.

import { useEffect, useMemo, useRef, useState } from 'react'
import type { Group, MonitorSummary } from '../../lib/types'
import { Pager, usePaging } from '../Pager'
import { LIST_SCROLL } from '../table'
import { StatusMark } from '../Status'
import { Badge, Button, Field, Icon, Input, Select } from '../ui'

export interface ItemDraft {
  key: string
  groupId: number | null
  monitorId: number | null
  displayName: string
  description: string
}

let keySeq = 0
export const nextItemKey = () => `i${++keySeq}`

export function PublicationItemsEditor({
  items, onChange, groups, monitors, error,
}: {
  items: ItemDraft[]
  onChange: (items: ItemDraft[]) => void
  groups: Group[]
  monitors: Map<number, MonitorSummary>
  error?: string
}) {
  const [addGroup, setAddGroup] = useState('')
  const [groupQuery, setGroupQuery] = useState('')
  const [query, setQuery] = useState('')
  const [addMonitor, setAddMonitor] = useState('')

  const groupName = useMemo(() => new Map(groups.map(g => [g.id, g.name])), [groups])
  const groupSize = useMemo(() => {
    const n = new Map<number, number>()
    for (const m of monitors.values()) if (m.groupId != null && !m.vendor) n.set(m.groupId, (n.get(m.groupId) ?? 0) + 1)
    return n
  }, [monitors])

  const usedGroups = new Set(items.filter(i => i.groupId != null).map(i => i.groupId as number))
  const groupOptions = [...groups].sort((a, b) => (a.sort - b.sort) || a.name.localeCompare(b.name)).filter(g => !usedGroups.has(g.id) && g.name.toLowerCase().includes(groupQuery.trim().toLowerCase())).slice(0, 200)
  const monitorOptions = useMemo(() => {
    const q = query.trim().toLowerCase()
    const used = new Set(items.filter(i => i.monitorId != null).map(i => i.monitorId as number))
    return [...monitors.values()]
      .filter(m => !m.vendor && !used.has(m.id) && (!q || m.name.toLowerCase().includes(q)))
      .sort((a, b) => a.name.localeCompare(b.name))
      .slice(0, 200)
  }, [monitors, query, items])

  // The published order can hold every service: edit it a page at a time.
  const paging = usePaging(items)
  const offset = (paging.page - 1) * paging.pageSize
  // A newly added item lands at the end: show the page it is on.
  const lastCount = useRef(items.length)
  useEffect(() => {
    if (items.length === lastCount.current + 1) paging.setPage(Math.ceil(items.length / paging.pageSize))
    lastCount.current = items.length
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items.length])

  function move(i: number, dir: -1 | 1) {
    const j = i + dir
    if (j < 0 || j >= items.length) return
    const next = [...items]
    ;[next[i], next[j]] = [next[j], next[i]]
    onChange(next)
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="grid gap-5 xl:grid-cols-2 border-b border-default pb-6 mb-2">
        <div className="flex flex-col gap-2">
          <Field label="Service group">
            {fid => <Input id={fid} type="search" placeholder="Search service groups by name" value={groupQuery} onChange={e => { setGroupQuery(e.target.value); setAddGroup('') }} />}
          </Field>
          <div className="flex items-center gap-2">
            <Select aria-label="Service group to add" value={addGroup} onChange={e => setAddGroup(e.target.value)} className="flex-1 min-w-0">
              <option value="">{groupOptions.length ? 'Choose a service group…' : 'No matching service groups'}</option>
              {groupOptions.map(g => <option key={g.id} value={g.id}>{g.name}</option>)}
            </Select>
            <Button
              disabled={!addGroup}
              onClick={() => {
                onChange([...items, { key: nextItemKey(), groupId: Number(addGroup), monitorId: null, displayName: '', description: '' }])
                setAddGroup('')
              }}
            >
              Add
            </Button>
          </div>
        </div>
        <div className="flex flex-col gap-2">
          <Field label="Individual service">
            {fid => <Input id={fid} type="search" placeholder="Search services by name" value={query} onChange={e => { setQuery(e.target.value); setAddMonitor('') }} />}
          </Field>
          <div className="flex items-center gap-2">
            <Select aria-label="Service to add" value={addMonitor} onChange={e => setAddMonitor(e.target.value)} className="flex-1 min-w-0">
              <option value="">{monitorOptions.length ? `Choose a service… (${monitorOptions.length}${monitorOptions.length === 200 ? '+' : ''})` : 'No matching services'}</option>
              {monitorOptions.map(m => <option key={m.id} value={m.id}>{m.name}</option>)}
            </Select>
            <Button
              disabled={!addMonitor}
              onClick={() => {
                onChange([...items, { key: nextItemKey(), groupId: null, monitorId: Number(addMonitor), displayName: '', description: '' }])
                setAddMonitor('')
              }}
            >
              Add
            </Button>
          </div>
        </div>
      </div>
      <h3 className="text-[14px] font-semibold">Published order</h3>
      {items.length === 0
        ? <p className="text-[13px] text-muted">No services selected. Choose a service group or individual service above.</p>
        : (
          <ol className={`flex flex-col gap-2 ${LIST_SCROLL}`}>
            {paging.rows.map((it, local) => {
              const i = offset + local
              const isGroup = it.groupId != null
              const monitor = it.monitorId != null ? monitors.get(it.monitorId) : undefined
              const original = isGroup ? (groupName.get(it.groupId as number) ?? `Service group ${it.groupId}`) : (monitor?.name ?? `Service ${it.monitorId}`)
              return (
                <li key={it.key} className="grid grid-cols-[24px_minmax(0,1fr)_auto] items-center gap-3 border border-default rounded-[14px] p-4 bg-surface">
                  <span className="text-[12px] text-faint tnum w-5 text-right col-start-1 row-start-1">{i + 1}</span>
                  <Badge className="col-start-2 row-start-2 justify-self-start" tone={isGroup ? 'accent' : 'neutral'}>{isGroup ? 'Service group' : 'Service'}</Badge>
                  <span className="flex items-center gap-1.5 min-w-0 col-start-2 row-start-1">
                    {monitor && <StatusMark status={monitor.status} />}
                    <span className="text-[13px] font-medium text-body truncate">{original}</span>
                    {isGroup && <span className="text-[12px] text-faint tnum shrink-0">· {groupSize.get(it.groupId as number) ?? 0} services</span>}
                  </span>
                  <Input
                    aria-label={`Display name for ${original}`}
                    placeholder={`Display as… (${original})`}
                    value={it.displayName}
                    onChange={e => onChange(items.map(x => (x.key === it.key ? { ...x, displayName: e.target.value } : x)))}
                    className="w-full col-start-2 col-span-2 row-start-3"
                    maxLength={120}
                  />
                  <Input
                    aria-label={`Public description for ${original}`}
                    placeholder="One-line description shown on the page (optional)"
                    value={it.description}
                    onChange={e => onChange(items.map(x => (x.key === it.key ? { ...x, description: e.target.value } : x)))}
                    className="w-full col-start-2 col-span-2 row-start-4"
                    maxLength={200}
                  />
                  <span className="flex items-center gap-1 col-start-3 row-start-1">
                    <Button size="sm" variant="ghost" aria-label={`Move ${original} up`} disabled={i === 0} onClick={() => move(i, -1)}>
                      <Icon.Chevron className="w-3.5 h-3.5 -rotate-90" />
                    </Button>
                    <Button size="sm" variant="ghost" aria-label={`Move ${original} down`} disabled={i === items.length - 1} onClick={() => move(i, 1)}>
                      <Icon.Chevron className="w-3.5 h-3.5 rotate-90" />
                    </Button>
                    <Button size="sm" variant="ghost" aria-label={`Remove ${original}`} onClick={() => onChange(items.filter(x => x.key !== it.key))}>
                      <Icon.Trash className="w-3.5 h-3.5" />
                    </Button>
                  </span>
                </li>
              )
            })}
          </ol>
        )}
      <Pager paging={paging} noun="Services" />
      {error && <p className="text-[11px] text-danger">{error}</p>}


    </div>
  )
}
