// Group monitors: one status that follows other monitors. Pick the children
// and the rule (all up, at least one up, or at least N up). The group is
// re-evaluated every interval, so a short interval makes it follow quickly.

import { useMemo, useState } from 'react'
import type { SettingsDraft } from '../../lib/monitorForm'
import { useLive } from '../../lib/live'
import type { FieldErrors } from '../../lib/types'
import { StatusMark } from '../Status'
import { Icon, Input, Select } from '../ui'
import { Field, FieldMessage, NumberInput } from './primitives'

const MAX_CHILDREN = 100

export function GroupFields({ settings: s, onSettings, errors, selfId }: {
  settings: SettingsDraft
  onSettings: (p: Partial<SettingsDraft>) => void
  errors: FieldErrors
  selfId?: number
}) {
  const live = useLive()
  const [q, setQ] = useState('')
  const e = (k: string) => errors[`settings.${k}`]

  const options = useMemo(() => {
    const needle = q.trim().toLowerCase()
    if (!needle) return []
    return [...live.monitors.values()]
      .filter(m => m.id !== selfId && !s.children.includes(m.id) && !m.vendor && m.name.toLowerCase().includes(needle))
      .sort((a, b) => a.name.localeCompare(b.name))
      .slice(0, 8)
  }, [live.monitors, q, selfId, s.children])
  const full = s.children.length >= MAX_CHILDREN

  const rule = s.groupMode === 'all'
    ? 'Down as soon as one of them is down.'
    : s.groupMode === 'any'
      ? 'Down only when every one of them is down; degraded while some are.'
      : `Down when fewer than ${Number.isFinite(s.groupMin) ? s.groupMin : '?'} of them are up; degraded while some are down.`

  return (
    <div className="flex flex-col gap-5">
      <p className="text-[12px] text-muted leading-relaxed">
        A group has no check of its own: it reads the status of the monitors below and shows as one. Monitors that are paused,
        in maintenance or not checked yet are left out, so pausing one never takes the group down.
      </p>

      <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 items-start">
        <Field label="The group is up when" error={e('groupMode')} hint={rule}>
          {id => (
            <Select id={id} value={s.groupMode} onChange={ev => onSettings({ groupMode: ev.target.value as SettingsDraft['groupMode'] })}>
              <option value="all">All of its monitors are up</option>
              <option value="any">At least one is up</option>
              <option value="min">At least N are up</option>
            </Select>
          )}
        </Field>
        {s.groupMode === 'min' && (
          <Field label="Minimum up" error={e('groupMin')} hint="How many must be up.">
            {id => <NumberInput id={id} value={s.groupMin} onValue={n => onSettings({ groupMin: n })} min={1} max={Math.max(s.children.length, 1)} aria-invalid={!!e('groupMin')} />}
          </Field>
        )}
      </div>

      <div className="flex flex-col gap-2.5">
        <span className="text-[12px] font-medium text-body-2">Monitors <span className="font-normal text-faint tnum">{s.children.length} of {MAX_CHILDREN}</span></span>
        {s.children.length > 0 && (
          <ul className="flex flex-wrap gap-2" aria-label="Monitors in this group">
            {s.children.map(id => {
              const m = live.monitors.get(id)
              return (
                <li key={id} className="inline-flex items-center gap-1.5 rounded-md border border-default bg-surface-2 pl-2.5 pr-1 py-1 text-[12px]">
                  {m && <StatusMark status={m.status} />}
                  <span className="text-body">{m?.name ?? `Monitor ${id}`}</span>
                  <button type="button" aria-label={`Remove ${m?.name ?? id}`} onClick={() => onSettings({ children: s.children.filter(x => x !== id) })}
                    className="p-1 rounded-lg text-muted hover:text-danger hover:bg-surface-hover">
                    <Icon.X className="w-3 h-3" />
                  </button>
                </li>
              )
            })}
          </ul>
        )}
        <div className="relative">
          <Input type="search" value={q} onChange={ev => setQ(ev.target.value)} disabled={full}
            placeholder={full ? `At most ${MAX_CHILDREN} monitors` : 'Search monitors to add'} aria-label="Add a monitor to the group" className="w-full" />
          {options.length > 0 && (
            <ul className="absolute z-20 left-0 right-0 mt-1 rounded-md border border-default bg-surface shadow-lg py-1 max-h-64 overflow-auto">
              {options.map(m => (
                <li key={m.id}>
                  <button type="button" onClick={() => { onSettings({ children: [...s.children, m.id] }); setQ('') }}
                    className="w-full flex items-center gap-2 px-3 py-1.5 text-left text-[13px] hover:bg-surface-hover">
                    <StatusMark status={m.status} />
                    <span className="truncate">{m.name}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
        <FieldMessage error={e('children')} />
      </div>
    </div>
  )
}
