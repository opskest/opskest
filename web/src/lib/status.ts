// Status vocabulary shared by every view: labels, severity ordering and
// the compact beat characters of MonitorSummary.beats.

import type { Status } from './types'

export const STATUS_LABEL: Record<Status, string> = {
  up: 'Up',
  degraded: 'Degraded',
  pending: 'Pending',
  down: 'Down',
  maintenance: 'Maintenance',
  unknown: 'Unknown',
  paused: 'Paused',
}

/** Severity order: DOWN 6 › PENDING 5 › DEGRADED 4 › UNKNOWN 3 ›
 * MAINTENANCE 2 › UP 1 › PAUSED 0. */
export const SEVERITY: Record<Status, number> = {
  down: 6, pending: 5, degraded: 4, unknown: 3, maintenance: 2, up: 1, paused: 0,
}

/** Every status, worst first. */
export const SEVERITY_ORDER: Status[] = ['down', 'pending', 'degraded', 'unknown', 'maintenance', 'up', 'paused']

/** Non-UP, non-paused statuses, worst first ("2 down · 1 degraded"). */
export const PROBLEM_ORDER: Status[] = ['down', 'pending', 'degraded', 'unknown', 'maintenance']

export function beatStatus(ch: string): Status {
  switch (ch) {
    case 'u': return 'up'
    case 'd': return 'down'
    case 'p': return 'pending'
    case 'g': return 'degraded'
    case 'm': return 'maintenance'
    default: return 'unknown'
  }
}

export function emptyCounts(): Record<Status, number> {
  return { up: 0, degraded: 0, pending: 0, down: 0, unknown: 0, maintenance: 0, paused: 0 }
}

export function countByStatus(monitors: Iterable<{ status: Status }>): Record<Status, number> {
  const counts = emptyCounts()
  for (const m of monitors) counts[m.status]++
  return counts
}

/** The worst status in `statuses` (paused ranks lowest, so it only wins
 * when every one is paused); null for none. */
export function worstOf(statuses: Iterable<Status>): Status | null {
  let worst: Status | null = null
  for (const s of statuses) if (worst == null || SEVERITY[s] > SEVERITY[worst]) worst = s
  return worst
}
