// Two alert rules in the monitor form:
//   DependsOnField  — parent monitors whose outage explains this one's
//                     (alerts are held while a parent is down)
//   LatencyAlertFields — alert when a response-time percentile over a
//                     window exceeds a ms limit and/or a multiple of usual

import { useMemo, useState } from 'react'
import type { Draft } from '../../lib/monitorForm'
import { useLive } from '../../lib/live'
import type { FieldErrors } from '../../lib/types'
import { StatusMark } from '../Status'
import { Icon, Input, Select, Switch } from '../ui'
import { Field, FieldGrid, FieldMessage, NumberInput, Subsection } from './primitives'

export function DependsOnField({ draft: d, errors, selfId, onPatch }: {
  draft: Draft
  errors: FieldErrors
  selfId?: number
  onPatch: (p: Partial<Draft>) => void
}) {
  const live = useLive()
  const [q, setQ] = useState('')
  const options = useMemo(() => {
    const needle = q.trim().toLowerCase()
    if (!needle) return []
    return [...live.monitors.values()]
      .filter(m => m.id !== selfId && !d.dependsOn.includes(m.id) && !m.vendor && m.name.toLowerCase().includes(needle))
      .sort((a, b) => a.name.localeCompare(b.name))
      .slice(0, 8)
  }, [live.monitors, q, selfId, d.dependsOn])
  const full = d.dependsOn.length >= 5

  return (
    <Subsection title="Depends on">
      <p className="text-[12px] text-muted leading-relaxed -mt-1">
        If one of these is down (or in maintenance) when this monitor goes down, the outage is still recorded but no alert is sent —
        the parent's alert already explains it. If the parent recovers first, this monitor's alert goes out then.
      </p>
      {d.dependsOn.length > 0 && (
        <ul className="flex flex-wrap gap-2" aria-label="Parent monitors">
          {d.dependsOn.map(id => {
            const m = live.monitors.get(id)
            return (
              <li key={id} className="inline-flex items-center gap-1.5 rounded-md border border-default bg-surface-2 pl-2.5 pr-1 py-1 text-[12px]">
                {m && <StatusMark status={m.status} />}
                <span className="text-body">{m?.name ?? `Monitor ${id}`}</span>
                <button type="button" aria-label={`Remove ${m?.name ?? id}`} onClick={() => onPatch({ dependsOn: d.dependsOn.filter(x => x !== id) })}
                  className="p-1 rounded-lg text-muted hover:text-danger hover:bg-surface-hover">
                  <Icon.X className="w-3 h-3" />
                </button>
              </li>
            )
          })}
        </ul>
      )}
      <div className="relative">
        <Input type="search" value={q} onChange={e => setQ(e.target.value)} disabled={full}
          placeholder={full ? 'At most 5 parents' : 'Search monitors to add as a parent'} aria-label="Add a parent monitor" className="w-full" />
        {options.length > 0 && (
          <ul className="absolute z-20 left-0 right-0 mt-1 rounded-md border border-default bg-surface shadow-lg py-1 max-h-64 overflow-auto">
            {options.map(m => (
              <li key={m.id}>
                <button type="button" onClick={() => { onPatch({ dependsOn: [...d.dependsOn, m.id] }); setQ('') }}
                  className="w-full flex items-center gap-2 px-3 py-1.5 text-left text-[13px] hover:bg-surface-hover">
                  <StatusMark status={m.status} />
                  <span className="truncate">{m.name}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
      <FieldMessage error={errors['settings.dependsOn']} />
    </Subsection>
  )
}

export function LatencyAlertFields({ draft: d, errors, onPatch }: {
  draft: Draft
  errors: FieldErrors
  onPatch: (p: Partial<Draft>) => void
}) {
  const la = d.latencyAlert
  const set = (p: Partial<typeof la>) => onPatch({ latencyAlert: { ...la, ...p } })
  const limits = [
    la.thresholdMs > 0 ? `above ${la.thresholdMs.toLocaleString()} ms` : '',
    la.factor > 0 ? `${la.factor}× its usual p${la.percentile}` : '',
  ].filter(Boolean)
  return (
    <Subsection title="Response-time alert">
      <Switch label="Alert when responses get slow" checked={la.on} onChange={e => set({ on: e.target.checked })} />
      {la.on && (
        <>
          <FieldGrid>
            <Field label="Percentile" error={errors['settings.latencyAlert.percentile']}>
              {fid => (
                <Select id={fid} value={la.percentile} onChange={e => set({ percentile: Number(e.target.value) as typeof la.percentile })}>
                  <option value={50}>p50 (typical)</option>
                  <option value={90}>p90</option>
                  <option value={95}>p95</option>
                  <option value={99}>p99 (tail)</option>
                </Select>
              )}
            </Field>
            <Field label="Over the last" error={errors['settings.latencyAlert.windowMin']}>
              {fid => <NumberInput id={fid} value={la.windowMin} onValue={n => set({ windowMin: n })} min={5} max={60} unit="min" />}
            </Field>
            <Field label="Above" error={errors['settings.latencyAlert.thresholdMs']} hint="0 = no ms limit">
              {fid => <NumberInput id={fid} value={la.thresholdMs} onValue={n => set({ thresholdMs: n })} min={0} max={120000} step={50} unit="ms" />}
            </Field>
            <Field label="Or above" error={errors['settings.latencyAlert.factor']} hint="× usual (7-day). 0 = off">
              {fid => <NumberInput id={fid} value={la.factor} onValue={n => set({ factor: n })} min={0} max={20} step={0.5} unit="×" />}
            </Field>
          </FieldGrid>
          <FieldMessage error={errors['settings.latencyAlert']} />
          {limits.length > 0 && (
            <p className="text-[12px] text-muted">
              Alerts when p{la.percentile} over {la.windowMin} min is {limits.join(' or ')}, and again when it's back under. Needs at least 5 successful checks in the window.
            </p>
          )}
        </>
      )}
    </Subsection>
  )
}
