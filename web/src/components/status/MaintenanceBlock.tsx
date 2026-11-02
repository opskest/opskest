// Active and upcoming scheduled maintenance, date-aware: an agenda of the
// windows (in progress first, then by start) beside a compact month with the
// affected days marked. Everything is written in the viewer's chosen display
// zone, whose name is always on screen. Only the public maintenance fields
// are used: a title, a description and when. Never targets or authors.

import { useId, useMemo, useState } from 'react'
import type { PublicMaintenance } from '../../lib/types'
import { ShowMoreButton, useShowMore } from '../Pager'
import { Button, Icon } from '../ui'
import { addDays, Calendar, longDay, monthOfKey, type DayMark, type MonthRef } from './Calendar'
import { MaintenanceItem, maintenanceSpan } from './IncidentCards'
import { ymdIn, ZoneSwitch, type DisplayZone } from './zone'

interface Win { m: PublicMaintenance; active: boolean; days: string[] }

function windowDays(m: PublicMaintenance, active: boolean, tz: string, now: number): string[] {
  const { start, end } = maintenanceSpan(m, active)
  const from = ymdIn(start ?? now, tz)
  const to = ymdIn(Math.max(end ?? start ?? now, start ?? now), tz)
  const out = [from]
  // A window is a few days at most; the guard keeps a bad record from looping.
  for (let d = from, n = 0; d < to && n < 62; n++) { d = addDays(d, 1); out.push(d) }
  return out
}

export function MaintenanceBlock({ active, upcoming, zone }: { active: PublicMaintenance[]; upcoming: PublicMaintenance[]; zone: DisplayZone }) {
  const { tz } = zone
  const now = Date.now()
  const today = ymdIn(now, tz)
  const [month, setMonth] = useState<MonthRef | null>(null)
  const [selected, setSelected] = useState<string | null>(null)
  const shownMonth = month ?? monthOfKey(today)

  const wins = useMemo<Win[]>(() => {
    const all: Win[] = [
      ...active.map(m => ({ m, active: true, days: windowDays(m, true, tz, Date.now()) })),
      ...upcoming.map(m => ({ m, active: false, days: windowDays(m, false, tz, Date.now()) })),
    ]
    return all.sort((a, b) => Number(b.active) - Number(a.active) || (maintenanceSpan(a.m, a.active).start ?? 0) - (maintenanceSpan(b.m, b.active).start ?? 0))
  }, [active, upcoming, tz])

  const marks = useMemo(() => {
    const out = new Map<string, DayMark>()
    for (const w of wins) for (const d of w.days) out.set(d, { count: (out.get(d)?.count ?? 0) + 1, tone: 'accent' })
    return out
  }, [wins])

  const visible = useMemo(() => (selected ? wins.filter(w => w.days.includes(selected)) : wins), [wins, selected])
  const more = useShowMore(visible, 5, 10)
  const [open, setOpen] = useState(() => typeof window !== 'undefined' && window.location.hash.startsWith('#maintenance'))
  const bodyId = useId()
  const activeCount = active.length

  return (
    <section aria-labelledby="maint-h" className="flex flex-col gap-4 rounded-[14px] border border-default bg-surface">
      <h2 id="maint-h" className="text-[16px] font-semibold text-body">
        <button type="button" aria-expanded={open} aria-controls={bodyId} onClick={() => setOpen(o => !o)}
          className="flex w-full flex-wrap items-center gap-x-3 gap-y-1 px-4 py-3.5 text-left rounded-[14px] hover:bg-surface-hover transition-colors">
          <Icon.Chevron className={`w-4 h-4 text-muted shrink-0 transition-transform ${open ? 'rotate-90' : ''}`} />
          <Icon.Wrench className="w-[18px] h-[18px] text-accent shrink-0" />
          Scheduled maintenance
          <span className="ml-1 text-[13px] font-normal text-muted tnum">
            {wins.length} {wins.length === 1 ? 'window' : 'windows'}{activeCount > 0 ? `, ${activeCount} in progress` : ''}
          </span>
        </button>
      </h2>
      {open && (
      <div id={bodyId} className="flex flex-col gap-4 px-4 pb-4">
      <div className="flex justify-end"><ZoneSwitch zone={zone} /></div>
      <div className="grid md:grid-cols-[minmax(0,1fr)_288px] gap-5 items-start">
        <div className="min-w-0 flex flex-col gap-3 order-2 md:order-1">
          {selected && (
            <p role="status" className="flex flex-wrap items-center gap-2 text-[13px] text-body-2">
              <span>{visible.length === 0 ? 'No maintenance on' : 'Maintenance on'} <span className="font-medium text-body">{longDay(selected)}</span></span>
              <Button size="sm" variant="ghost" onClick={() => setSelected(null)}>Show all</Button>
            </p>
          )}
          {more.shown.map(w => <MaintenanceItem key={`${w.active ? 'a' : 'u'}${w.m.id}`} m={w.m} active={w.active} tz={tz} />)}
          <ShowMoreButton hidden={more.hidden} step={more.step} onClick={more.more} noun="windows" />
        </div>
        <div className="order-1 md:order-2 rounded-[14px] border border-default bg-surface p-3.5">
          <Calendar
            month={shownMonth}
            onMonth={setMonth}
            today={today}
            marks={marks}
            selected={selected}
            onSelect={setSelected}
            label="Maintenance calendar"
            noun="maintenance window"
            compact
          />
          <p className="mt-2.5 text-[11px] text-muted flex items-center gap-1.5"><span aria-hidden="true" className="inline-block w-4 h-[3px] rounded-full bg-accent" />Day with maintenance. Select a day to see its windows.</p>
        </div>
      </div>
      </div>
      )}
    </section>
  )
}
