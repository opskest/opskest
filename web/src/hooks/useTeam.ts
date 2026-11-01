import { useEffect, useState } from 'react'
import { api, unwrapList } from '../lib/api'

export interface TeamMember { id: number; label: string }

/** Active users, for picking an incident owner. Admin-only endpoint; a
 * viewer simply gets an empty list. */
export function useTeam(enabled: boolean): TeamMember[] {
  const [team, setTeam] = useState<TeamMember[]>([])
  useEffect(() => {
    if (!enabled) return
    api.get<unknown>('/api/users')
      .then(r => setTeam(unwrapList<{ id: number; name?: string; email: string; disabled?: boolean }>(r, 'users')
        .filter(u => !u.disabled)
        .map(u => ({ id: u.id, label: u.name?.trim() || u.email }))
        .sort((a, b) => a.label.localeCompare(b.label))))
      .catch(() => setTeam([]))
  }, [enabled])
  return team
}

export const SEVERITIES = [
  { id: 'critical', label: 'Critical (Sev 1)' },
  { id: 'major', label: 'Major (Sev 2)' },
  { id: 'minor', label: 'Minor (Sev 3)' },
] as const

export function severityTone(s: string): 'danger' | 'warn' | 'neutral' {
  return s === 'critical' ? 'danger' : s === 'major' ? 'warn' : 'neutral'
}

export function severityLabel(s: string): string {
  return s === 'critical' ? 'Sev 1 · Critical' : s === 'major' ? 'Sev 2 · Major' : 'Sev 3 · Minor'
}
