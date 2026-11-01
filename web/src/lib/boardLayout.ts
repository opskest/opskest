// Pure maths and data reduction behind the Monitoror-style wallboard
// (spec §12), shared by /wallboard and /status/:slug/board: the grid fit,
// the per-card type scale, the "stale" rule and the group → card reduction
// (worst status, counts, the sorted list of unhealthy members). No DOM, no
// React.
//
// Grid maths follow Monitoror's App.vue (`--columns`, `--rows`, the 750/500
// px breakpoints), with the column count derived from the card count.

import { emptyCounts, PROBLEM_ORDER, SEVERITY } from './status'
import type { AckInfo, MonitorType, Status } from './types'

/**
 * The fields the board reads. Both MonitorSummary (the private stream) and
 * PublicMonitor (a status page's sanitized stream) satisfy it; whatever a
 * public summary lacks is optional and switches that feature off.
 */
export interface BoardMonitor {
  id: number
  name: string
  status: Status
  since: number
  uptime24h: number | null
  uptime30d?: number | null
  beats?: string
  type?: MonitorType
  latencyMs?: number
  reason?: string
  lastCheckAt?: number
  intervalS?: number
  ack?: AckInfo | null
  runbook?: string
  paused?: boolean
}

export interface BoardGroup {
  key: string
  name: string
  monitorIds: number[]
}

// ── Staleness ───────────────────────────────────────────────────────────────

/** The `+ timeout` slack of the stale rule (the summary carries no timeout,
 * so this is the default one). */
export const STALE_GRACE_MS = 10_000

/** Older than two intervals plus the timeout slack: the engine should have
 * checked it by now. Paused and push monitors are never stale. */
export function isStale(m: BoardMonitor, now: number): boolean {
  if (m.paused || m.status === 'paused' || m.type === 'push') return false
  if (!m.lastCheckAt || !m.intervalS) return false
  return m.lastCheckAt < now - (2 * m.intervalS * 1000 + STALE_GRACE_MS)
}

export interface Effective {
  status: Status
  stale: boolean
}

/** Stale only escalates: a quiet UP becomes UNKNOWN, a stale DOWN stays DOWN. */
export function effectiveStatus(m: BoardMonitor, now: number): Effective {
  if (m.paused || m.status === 'paused') return { status: 'paused', stale: false }
  const stale = isStale(m, now)
  if (stale && SEVERITY[m.status] < SEVERITY.unknown) return { status: 'unknown', stale }
  return { status: m.status, stale }
}

// ── Cards ───────────────────────────────────────────────────────────────────

export interface ProblemLine {
  id: number
  name: string
  status: Status
  stale: boolean
  since: number
  reason?: string
  latencyMs?: number
  ack?: AckInfo | null
  runbook?: string
}

export interface CardModel {
  key: string
  name: string
  /** Worst effective status among non-paused members; 'paused' if all are. */
  worst: Status
  total: number
  /** Non-paused members. */
  active: number
  up: number
  paused: number
  counts: Record<Status, number>
  /** Mean 24 h uptime over active members that have data. */
  uptime24h: number | null
  /** Every active member that isn't UP, worst first, longest-standing first. */
  problems: ProblemLine[]
  members: BoardMonitor[]
  /** Changes whenever the card's time-dependent state changes. */
  sig: string
}

export function buildCard(group: BoardGroup, monitors: ReadonlyMap<number, BoardMonitor>, now: number): CardModel | null {
  const members: BoardMonitor[] = []
  for (const id of group.monitorIds) {
    const m = monitors.get(id)
    if (m) members.push(m)
  }
  if (members.length === 0) return null

  const counts = emptyCounts()
  const problems: ProblemLine[] = []
  let worst: Status = 'paused'
  let uptimeSum = 0
  let uptimeN = 0

  for (const m of members) {
    const eff = effectiveStatus(m, now)
    counts[eff.status]++
    if (eff.status === 'paused') continue
    if (SEVERITY[eff.status] > SEVERITY[worst]) worst = eff.status
    if (m.uptime24h != null && Number.isFinite(m.uptime24h)) {
      uptimeSum += m.uptime24h
      uptimeN++
    }
    if (eff.status !== 'up') {
      problems.push({
        id: m.id,
        name: m.name,
        status: eff.status,
        stale: eff.stale,
        since: eff.stale && eff.status !== m.status ? (m.lastCheckAt ?? m.since) : m.since,
        reason: m.reason,
        latencyMs: m.latencyMs,
        ack: m.ack,
        runbook: m.runbook,
      })
    }
  }

  problems.sort((a, b) => (SEVERITY[b.status] - SEVERITY[a.status]) || (a.since - b.since) || (a.id - b.id))

  return {
    key: group.key,
    name: group.name,
    worst,
    total: members.length,
    active: members.length - counts.paused,
    up: counts.up,
    paused: counts.paused,
    counts,
    uptime24h: uptimeN > 0 ? uptimeSum / uptimeN : null,
    problems,
    members,
    sig: `${worst}|${problems.map(p => `${p.id}:${p.status}:${p.stale ? 1 : 0}`).join(',')}`,
  }
}

/** One card per non-empty group; `severity` floats the worst to the front. */
export function buildCards(
  groups: BoardGroup[],
  monitors: ReadonlyMap<number, BoardMonitor>,
  now: number,
  sort: 'fixed' | 'severity' = 'fixed',
): CardModel[] {
  const cards: CardModel[] = []
  for (const g of groups) {
    const c = buildCard(g, monitors, now)
    if (c) cards.push(c)
  }
  if (sort === 'severity') {
    const order = new Map(cards.map((c, i) => [c.key, i]))
    cards.sort((a, b) => (SEVERITY[b.worst] - SEVERITY[a.worst]) || ((order.get(a.key) ?? 0) - (order.get(b.key) ?? 0)))
  }
  return cards
}

/** "14/14 up", "13/13 up · 1 paused", or "2 down · 1 degraded · 14". */
export function countLabel(c: CardModel): string {
  if (c.active === 0) return `${c.paused} paused`
  if (c.worst === 'up') return `${c.up}/${c.active} up${c.paused ? ` · ${c.paused} paused` : ''}`
  const parts = PROBLEM_ORDER.filter(s => c.counts[s] > 0).map(s => `${c.counts[s]} ${s}`)
  return `${parts.join(' · ')} · ${c.total}`
}

/** Totals across a set of monitors, by effective status. */
export function totalsByStatus(monitors: Iterable<BoardMonitor>, now: number): Record<Status, number> {
  const counts = emptyCounts()
  for (const m of monitors) counts[effectiveStatus(m, now).status]++
  return counts
}

// ── Grid fit ────────────────────────────────────────────────────────────────

export const BOARD_GAP = 8
/** Narrow screens scroll instead of squeezing cards below this height. */
export const MIN_CARD_H = 180
const FLOOR_CARD_H = 72
const MAX_COLS_PARAM = 12

export interface BoardLayout {
  cols: number
  rows: number
  cardH: number
  /** The grid is taller than the box (narrow screens only, in practice). */
  scroll: boolean
}

/**
 * Fit `nCards` into width × height. Columns default to ceil(sqrt(n × 16/9))
 * clamped to 1–6 and then evened out (9 cards → 3×3, not 4+4+1), or come
 * from `colsParam`. ≤ 750 px → 2 columns, ≤ 500 px → 1; those may scroll.
 */
export function layout(nCards: number, width: number, height: number, colsParam?: number): BoardLayout {
  const n = Math.max(0, Math.floor(nCards))
  if (n === 0 || width <= 0 || height <= 0) return { cols: 1, rows: 0, cardH: 0, scroll: false }

  let cols: number
  let narrow = false
  if (width <= 500) {
    cols = 1
    narrow = true
  } else if (width <= 750) {
    cols = Math.min(2, n)
    narrow = true
  } else if (colsParam != null && Number.isFinite(colsParam) && colsParam >= 1) {
    cols = Math.min(MAX_COLS_PARAM, Math.floor(colsParam))
  } else {
    cols = Math.min(n, Math.max(1, Math.min(6, Math.ceil(Math.sqrt((n * 16) / 9)))))
    cols = Math.ceil(n / Math.ceil(n / cols))
  }

  const rows = Math.ceil(n / cols)
  const fitH = Math.floor((height - BOARD_GAP * (rows + 1)) / rows)
  const cardH = Math.max(narrow ? MIN_CARD_H : FLOOR_CARD_H, fitH)
  const scroll = cardH * rows + BOARD_GAP * (rows + 1) > height + 0.5
  return { cols, rows, cardH, scroll }
}

export function cardWidth(l: BoardLayout, width: number): number {
  return Math.max(0, (width - BOARD_GAP * (l.cols + 1)) / l.cols)
}

// ── Type scale ──────────────────────────────────────────────────────────────

export interface CardScale {
  pad: number
  title: number
  headerH: number
  line: number
  lineH: number
  check: number
  big: number
}

const SCALES: { min: number; s: CardScale }[] = [
  { min: 400, s: { pad: 20, title: 32, headerH: 50, line: 22, lineH: 38, check: 96, big: 64 } },
  { min: 280, s: { pad: 16, title: 26, headerH: 42, line: 18, lineH: 31, check: 72, big: 48 } },
  { min: 190, s: { pad: 14, title: 20, headerH: 34, line: 15, lineH: 26, check: 52, big: 36 } },
  { min: 0, s: { pad: 10, title: 16, headerH: 28, line: 13, lineH: 22, check: 36, big: 26 } },
]

/** Bigger cards get bigger type, so a 4-card TV reads from across the room. */
export function cardScale(cardW: number, cardH: number): CardScale {
  const size = Math.min(cardH, cardW * 0.75)
  return (SCALES.find(t => size >= t.min) ?? SCALES[SCALES.length - 1]).s
}

/** Below this card width (in line-font ems) a reason gets its own row. */
export const TWO_LINE_BELOW_EM = 36

export function problemRowH(scale: CardScale, withReasonRow: boolean): number {
  return withReasonRow ? Math.round(scale.lineH * 1.7) : scale.lineH
}

/** How many problem rows fit, reserving room for "+N more". */
export function visibleRows(cardH: number, scale: CardScale, rowHeights: number[]): { shown: number; more: number } {
  const avail = cardH - scale.pad * 2 - scale.headerH
  let used = 0
  let i = 0
  for (; i < rowHeights.length; i++) {
    if (used + rowHeights[i] > avail) break
    used += rowHeights[i]
  }
  if (i === rowHeights.length) return { shown: i, more: 0 }
  while (i > 0 && used + scale.lineH > avail) {
    i--
    used -= rowHeights[i]
  }
  return { shown: i, more: rowHeights.length - i }
}
