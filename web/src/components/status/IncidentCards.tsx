// Incident and maintenance presentation for status pages, and Linkified —
// plain text with bare URLs turned into links (bodies are plain
// text, links auto-linked; never HTML).
//
// An incident reads as a timeline: its title with impact and state, then one
// entry per public update, newest first, each with the state in words and the
// time in the page's chosen zone. Colour only ever marks the state; the state
// is always also written out.

import type { ReactNode } from 'react'
import { Link } from 'wouter'
import { dateLocal, duration } from '../../lib/format'
import type { Incident, IncidentStatus, IncidentUpdate, PublicMaintenance } from '../../lib/types'
import { Badge, Icon } from '../ui'
import { ShowMoreButton, useShowMore } from '../Pager'
import { dateTimeIn, timeIn, ymdIn } from './zone'
import { FoldToggle, useFold } from './Fold'

export const INCIDENT_STATUS: Record<IncidentStatus, string> = {
  investigating: 'Investigating',
  identified: 'Identified',
  monitoring: 'Monitoring',
  resolved: 'Resolved',
}

export const IMPACT_LABEL: Record<string, string> = { minor: 'Minor', major: 'Major', maintenance: 'Maintenance' }

export function impactTone(impact: string): 'warn' | 'danger' | 'accent' | 'neutral' {
  if (impact === 'major') return 'danger'
  if (impact === 'minor') return 'warn'
  if (impact === 'maintenance') return 'accent'
  return 'neutral'
}

const FRAME: Record<string, string> = {
  danger: 'border-danger/40 bg-danger-bg/60',
  warn: 'border-warn/40 bg-warn-bg/60',
  accent: 'border-accent-border bg-accent-subtle/60',
  neutral: 'border-default bg-surface',
}
const TAG: Record<string, string> = {
  danger: 'border-danger/40 text-danger',
  warn: 'border-warn/40 text-warn',
  accent: 'border-accent-border text-accent',
  ok: 'border-ok/40 text-ok',
  neutral: 'border-default text-muted',
}
const TITLE_TEXT: Record<string, string> = { danger: 'text-danger', warn: 'text-warn', accent: 'text-accent', neutral: 'text-body' }

/** The incident's state as a small uppercase tag, right-aligned in a banner. */
export function StatusTag({ tone, children }: { tone: keyof typeof TAG; children: ReactNode }) {
  return <span className={`inline-flex items-center rounded-md border bg-surface/60 px-2 py-0.5 text-[11px] font-semibold uppercase tracking-wide whitespace-nowrap ${TAG[tone]}`}>{children}</span>
}

/** One public update as a box: state chip, time stamp, then the text. */
export function UpdateBox({ update, when, latest, past }: { update: IncidentUpdate; when: string; latest: boolean; past?: boolean }) {
  const tone = update.status === 'resolved' ? 'ok' : latest ? 'warn' : 'neutral'
  return (
    <li className="rounded-[10px] border border-default bg-surface-2/60 px-3.5 py-3 text-[13px]">
      <p className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
        <Badge tone={tone} dot={latest && update.status !== 'resolved'}>{INCIDENT_STATUS[update.status] ?? update.status}</Badge>
        <time dateTime={new Date(update.createdAt).toISOString()} className="font-mono text-[12px] text-muted tnum">{when}</time>
      </p>
      {update.body && (
        <p className={`mt-2 leading-relaxed whitespace-pre-wrap break-words max-w-prose ${latest && !past ? 'text-body' : 'text-body-2'}`}>
          <Linkified text={update.body} />
        </p>
      )}
    </li>
  )
}

const STEPS: IncidentStatus[] = ['investigating', 'identified', 'monitoring', 'resolved']

/** Numbered circular steps joined by a progress bar: where the incident is in
 * Investigating, Identified, Monitoring, Resolved. Reached steps are filled,
 * the current one is ringed and marked aria-current. */
export function IncidentSteps({ status }: { status: IncidentStatus }) {
  const at = Math.max(0, STEPS.indexOf(status))
  const resolved = status === 'resolved'
  const fill = resolved ? 'bg-ok text-surface' : 'bg-warn text-surface'
  const bar = resolved ? 'bg-ok' : 'bg-warn'
  const ring = resolved ? 'ring-ok' : 'ring-warn'
  return (
    <ol aria-label="Incident progress" className="relative grid grid-cols-4">
      <span aria-hidden="true" className="absolute top-[15px] left-[12.5%] right-[12.5%] h-1 rounded-full bg-surface-2">
        <span className={`block h-full rounded-full ${bar}`} style={{ width: `${(at / (STEPS.length - 1)) * 100}%` }} />
      </span>
      {STEPS.map((step, i) => {
        const reached = i <= at
        return (
          <li key={step} aria-current={i === at ? 'step' : undefined} className="relative flex flex-col items-center gap-2 text-center">
            <span aria-hidden="true" className={`w-[34px] h-[34px] rounded-full flex items-center justify-center text-[13px] font-semibold tnum border-2 ${reached ? `${fill} border-transparent` : 'bg-surface border-default text-muted'} ${i === at ? `ring-2 ring-offset-2 ring-offset-surface ${ring}` : ''}`}>
              {i < at || (resolved && i === at) ? <Icon.Check className="w-4 h-4" /> : i + 1}
            </span>
            <span className={`text-[12px] ${i === at ? 'font-semibold text-body' : reached ? 'text-body-2' : 'text-muted'}`}>{INCIDENT_STATUS[step]}</span>
          </li>
        )
      })}
    </ol>
  )
}

const URL_RE = /(https?:\/\/[^\s<>"']+[^\s<>"'.,)])/g

export function Linkified({ text }: { text: string }) {
  const parts = text.split(URL_RE)
  return (
    <>
      {parts.map((part, i) => (i % 2 === 1
        ? <a key={i} href={part} target="_blank" rel="noopener noreferrer nofollow" className="text-accent underline underline-offset-2 break-all">{part}</a>
        : <span key={i}>{part}</span>))}
    </>
  )
}

/** An incident with its public timeline. `href` links the title to the
 * incident's permalink; `past` keeps only the last update. `variant="flat"`
 * drops the card frame for use inside a day's list. The timeline starts
 * collapsed (the headline and the latest state are always visible) unless the
 * page opens everything; `foldable={false}` keeps it open. */
export function IncidentCard({ incident, past, href, variant = 'card', tz, foldable = true }: { incident: Incident; past?: boolean; href?: string; variant?: 'card' | 'flat'; tz?: string; foldable?: boolean }) {
  const timeline = [...incident.updates].sort((a, b) => b.createdAt - a.createdAt)
  const latest = timeline[0]
  // An incident can collect hundreds of updates: show the newest few.
  const updates = useShowMore(past ? timeline.slice(0, 1) : timeline, 5, 10)
  const fold = useFold(`incident-${incident.id}`)
  const open = !foldable || fold.open
  const when = (at: number) => (tz ? dateTimeIn(at, tz) : dateLocal(at))
  const tone = incident.status === 'resolved' ? 'neutral' : impactTone(incident.impact)
  const frame = variant === 'flat' ? 'py-1' : `rounded-[14px] border p-5 mb-3 ${FRAME[tone]}`
  const shownCount = past ? Math.min(1, timeline.length) : timeline.length
  return (
    <article id={`incident-${incident.id}`} className={`flex flex-col gap-3 scroll-mt-20 ${frame}`}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <h3 className={`text-[16px] font-semibold mr-auto break-words min-w-0 ${incident.status === 'resolved' ? 'text-body' : TITLE_TEXT[tone]}`}>
          {href ? <Link href={href} className="hover:underline underline-offset-2">{incident.title}</Link> : incident.title}
        </h3>
        {incident.impact && <Badge tone={impactTone(incident.impact)}>{IMPACT_LABEL[incident.impact] ?? incident.impact} impact</Badge>}
        <StatusTag tone={incident.status === 'resolved' ? 'ok' : tone}>{INCIDENT_STATUS[incident.status] ?? incident.status}</StatusTag>
      </div>
      {!open && latest && !(past && incident.resolvedAt) && (
        <p className="text-[12px] text-muted tnum">Latest: <span className="font-medium text-body-2">{INCIDENT_STATUS[latest.status] ?? latest.status}</span> · <time dateTime={new Date(latest.createdAt).toISOString()}>{when(latest.createdAt)}</time></p>
      )}
      {past && incident.resolvedAt && (
        <p className="text-[12px] text-muted tnum">
          Resolved {when(incident.resolvedAt)} · lasted {duration(incident.resolvedAt - incident.createdAt)}
          {href && timeline.length > 1 && <> · <Link href={href} className="text-accent hover:underline">All {timeline.length} updates</Link></>}
        </p>
      )}
      {timeline.length > 0 && (
        <div ref={foldable ? fold.bodyRef : undefined} id={fold.bodyId} className="flex flex-col gap-3">
          <ol className="flex flex-col gap-2.5">
            {updates.shown.map(u => <UpdateBox key={u.id} update={u} when={when(u.createdAt)} latest={u === latest} past={past} />)}
          </ol>
          <ShowMoreButton hidden={updates.hidden} step={updates.step} onClick={updates.more} noun="updates" />
        </div>
      )}
      {foldable && timeline.length > 0 && (
        <FoldToggle open={fold.open} controls={fold.bodyId} onClick={fold.toggle}>
          {fold.open ? 'Hide updates' : `Show ${past ? 'last update' : shownCount === 1 ? 'update' : `${shownCount} updates`}`}
        </FoldToggle>
      )}
    </article>
  )
}

/** When a window runs, written for a person: "Today 14:00 – 16:00 GMT+1". */
export function maintenanceSpan(m: PublicMaintenance, active: boolean): { start: number | null; end: number | null } {
  if (active) return { start: m.startsAt ?? null, end: m.currentEnd ?? m.endsAt ?? null }
  return { start: m.nextStart ?? m.startsAt ?? null, end: m.endsAt ?? null }
}

export function MaintenanceItem({ m, active, tz }: { m: PublicMaintenance; active: boolean; tz: string }) {
  const { start, end } = maintenanceSpan(m, active)
  const startText = start ? dateTimeIn(start, tz) : null
  // The end repeats the date only when it is on another day.
  const endText = end ? (start && ymdIn(start, tz) === ymdIn(end, tz) ? timeIn(end, tz) : dateTimeIn(end, tz)) : null
  const fold = useFold(`maintenance-${m.id}`)
  const when = active
    ? (endText ? `In progress until ${endText}` : 'In progress')
    : startText ? (endText && m.kind === 'once' ? `${startText} to ${endText}` : `Starts ${startText}`) : m.kind === 'weekly' ? 'Weekly window' : 'Scheduled'
  return (
    <article id={`maintenance-${m.id}`} className="rounded-[14px] border border-default bg-surface p-4 flex flex-col gap-2 scroll-mt-20">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1.5">
        <Icon.Wrench className="w-4 h-4 text-accent shrink-0" />
        <h3 className="text-[14px] font-semibold text-body mr-auto min-w-0 break-words">{m.title}</h3>
        <Badge tone="accent" dot={active}>{active ? 'In progress' : m.kind === 'weekly' ? 'Recurring' : 'Scheduled'}</Badge>
      </div>
      <p className="text-[12px] text-muted tnum">{when}</p>
      {m.description && (
        <>
          <p ref={fold.bodyRef} id={fold.bodyId} className="text-[13px] text-body-2 leading-relaxed whitespace-pre-wrap break-words max-w-prose"><Linkified text={m.description} /></p>
          <FoldToggle open={fold.open} controls={fold.bodyId} onClick={fold.toggle}>{fold.open ? 'Hide details' : 'Show details'}</FoldToggle>
        </>
      )}
    </article>
  )
}
