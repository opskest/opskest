// One wallboard card per group, built like a service tile: the name and
// "up / total" in the colour of the group's worst state, a strip with one pill
// per service, a footer line, and underneath only the services that are not up.

import { memo } from 'react'
import { Link } from 'wouter'
import { effectiveStatus, type CardModel } from '../../lib/boardLayout'
import { duration, pct } from '../../lib/format'
import type { Status } from '../../lib/types'
import { statusColor } from '../Status'

export interface GroupCardProps {
  card: CardModel
  mode: 'private' | 'public'
  linkMonitors: boolean
  now: number
  /** How many not-up services to list before "+N more". */
  maxLines: number
  /** Rows the tallest card on the board lists, so every card has the same structure. */
  rows?: number
  /** Some group name on the board wraps onto two lines: reserve two so every strip lines up. */
  reserveName?: boolean
}

type Tone = 'ok' | 'warn' | 'danger' | 'muted'

function toneOf(worst: Status): Tone {
  if (worst === 'down') return 'danger'
  if (worst === 'degraded' || worst === 'pending' || worst === 'unknown' || worst === 'maintenance') return 'warn'
  if (worst === 'paused') return 'muted'
  return 'ok'
}

const TEXT: Record<Tone, string> = { ok: 'text-ok', warn: 'text-warn', danger: 'text-danger', muted: 'text-muted' }
const FRAME: Record<Tone, string> = { ok: 'border-default', warn: 'border-warn/60', danger: 'border-danger/60', muted: 'border-default' }
const MAX_PILLS = 48

function GroupCardImpl({ card, mode, linkMonitors, now, maxLines, rows = 0, reserveName = false }: GroupCardProps) {
  const tone = toneOf(card.worst)
  const shown = card.problems.slice(0, maxLines)
  const more = card.problems.length - shown.length
  const pills = card.members.filter(m => !(m.paused || m.status === 'paused')).slice(0, MAX_PILLS)
  const allPaused = card.active === 0
  const summary = allPaused ? 'All services paused' : card.problems.length === 0 ? 'All operational' : `${card.problems.length} of ${card.active} need attention`

  return (
    <section
      aria-label={`${card.name}: ${card.up} of ${card.active} up`}
      className={`relative overflow-hidden flex flex-col min-w-0 rounded-[0.9em] border bg-surface text-body leading-[1.3] h-full justify-center ${FRAME[tone]}`}
      style={{ padding: '0.9em 1em', gap: '0.7em' }}
    >
      <header className="flex items-center justify-between" style={{ gap: '0.8em', minHeight: reserveName ? '2.65em' : undefined }}>
        <h2 className="font-mono font-medium min-w-0 line-clamp-2 [overflow-wrap:anywhere]" style={{ fontSize: '1em', lineHeight: 1.3 }} title={card.name}>{card.name}</h2>
        <span className={`shrink-0 font-medium tnum leading-none ${TEXT[tone]}`} style={{ fontSize: '1.55em' }}>
          {allPaused ? 'Paused' : <>{card.up}<span className="font-normal opacity-70">/{card.active}</span></>}
        </span>
      </header>

      <div className="flex" style={{ gap: '2px', height: '1.7em' }} aria-hidden="true">
        {pills.map(m => {
          const st = effectiveStatus(m, now).status
          return <span key={m.id} title={m.name} className={`flex-1 min-w-0 rounded-full opacity-90 ${st === 'paused' ? 'bg-surface-hover' : statusColor(st).dot}`} style={{ maxWidth: '1.1em' }} />
        })}
      </div>

      <div className="flex flex-wrap items-baseline justify-between font-mono" style={{ gap: '0.15em 0.7em', fontSize: '0.8em' }}>
        <span className={`font-medium whitespace-nowrap ${tone === 'ok' ? 'text-body-2' : TEXT[tone]}`}>{summary}</span>
        <span className="text-muted tnum whitespace-nowrap">24h {pct(card.uptime24h)}</span>
      </div>

      {(shown.length > 0 || rows > 0) && (
        <ul className="flex flex-col border-t border-subtle/40" style={{ gap: '0.3em', fontSize: '0.88em', paddingTop: '0.6em', minHeight: `${rows * 1.6}em` }}>
          {shown.map(p => {
            const c = statusColor(p.status)
            const label = linkMonitors && mode === 'private'
              ? <Link href={`/monitors/${p.id}`} className="hover:underline underline-offset-2">{p.name}</Link>
              : p.name
            return (
              <li key={p.id} className="flex items-start min-w-0" style={{ gap: '0.5em' }}>
                <span aria-hidden="true" className={`rounded-full shrink-0 ${c.dot}`} style={{ width: '0.55em', height: '0.55em', marginTop: '0.5em' }} />
                <span className={`font-medium min-w-0 [overflow-wrap:anywhere] ${c.text}`}>{label}</span>
                <span className="ml-auto shrink-0 text-muted tnum pl-1" style={{ fontSize: '0.86em' }}>{duration(Math.max(0, now - p.since))}</span>
              </li>
            )
          })}
          {more > 0 && <li className="text-muted tnum">+{more} more</li>}
        </ul>
      )}
    </section>
  )
}

function sameMembers(a: CardModel, b: CardModel): boolean {
  if (a.members.length !== b.members.length) return false
  for (let i = 0; i < a.members.length; i++) if (a.members[i] !== b.members[i]) return false
  return true
}

export const GroupCard = memo(GroupCardImpl, (prev, next) =>
  prev.card.sig === next.card.sig
  && prev.card.name === next.card.name
  && sameMembers(prev.card, next.card)
  && prev.maxLines === next.maxLines
  && prev.rows === next.rows
  && prev.reserveName === next.reserveName
  && prev.mode === next.mode
  && prev.linkMonitors === next.linkMonitors
  && (next.card.problems.length === 0 || prev.now === next.now),
)
