// Problem clusters: up to 2 lines reading e.g. "Likely common
// cause: api.example.com — 5 monitors affected since 10:42 (timeout)". The
// engine ranks candidates each delta tick; this renders the top 2.

import { timeServer } from '../lib/format'
import type { Cluster } from '../lib/types'
import { Icon } from './icons'

const ERR_CLASS_LABEL: Record<string, string> = {
  timeout: 'timeout',
  dns: 'DNS',
  refused: 'connection refused',
  tls: 'TLS',
  http_status: 'HTTP status',
  assertion: 'assertion',
  network: 'network',
  other: 'other',
}

export function errClassLabel(c: string): string {
  return ERR_CLASS_LABEL[c] ?? c
}

function ClusterLine({ c, size }: { c: Cluster; size: 'md' | 'lg' }) {
  const n = c.monitorIds.length
  const label = c.errClass ? errClassLabel(c.errClass) : null
  return (
    <p className={`${size === 'lg' ? 'text-[15px]' : 'text-[13px]'} text-warn leading-snug truncate`}>
      Likely common cause:{' '}
      {c.kind === 'host'
        ? <><strong className="font-semibold">{c.key}</strong> — {n} monitors affected since {timeServer(c.since)}{label ? ` (${label})` : ''}</>
        : <><strong className="font-semibold">{n} × {label ?? c.key}</strong> since {timeServer(c.since)}</>}
    </p>
  )
}

export function Clusters({ clusters, size = 'md', boxed = false }: { clusters: Cluster[]; size?: 'md' | 'lg'; boxed?: boolean }) {
  const top = clusters.slice(0, 2)
  if (top.length === 0) return null
  const lines = top.map((c, i) => <ClusterLine key={`${c.kind}-${c.key}-${i}`} c={c} size={size} />)
  if (!boxed) return <div className="flex flex-col gap-0.5 min-w-0" role="status">{lines}</div>
  return (
    <div role="status" className="flex items-start gap-2.5 rounded-lg border border-warn/30 bg-warn-bg px-3.5 py-2.5 min-w-0">
      <Icon.Bolt className="w-4 h-4 text-warn shrink-0 mt-0.5" />
      <div className="flex flex-col gap-0.5 min-w-0">{lines}</div>
    </div>
  )
}
