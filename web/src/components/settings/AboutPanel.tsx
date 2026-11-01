import { useCallback, useEffect, useState } from 'react'
import { api } from '../../lib/api'
import { duration } from '../../lib/format'
import { PROJECT } from '../../lib/project'
import { serverRegion, serverTimeZone } from '../../lib/runtime'
import type { UpdatesPayload } from '../../lib/updates'
import { Collapsible } from '../controls'
import KestrelMark from '../KestrelMark'
import { Badge } from '../ui'
import { CopyField, SettingsSection, SettingsStats } from './SettingsPanel'
import { METHOD_TEXT, UpdatesPanel } from './UpdatesPanel'

// Settings → About & updates, kept calm: one identity block (name, version,
// how it is installed and for how long), three live engine figures, and a
// closed "Technical details" list for everything else. Updates follow.

interface SystemStats {
  version?: string
  monitors?: number
  lagP50Ms?: number
  lagP99Ms?: number
  inFlight?: number
  startedAt?: number
  writerDropped?: number
  connectivity?: { online?: boolean }
  backups?: { dir?: string }
}

const figure = (n: number | undefined, unit = '') => (n == null ? '—' : `${n}${unit}`)

function bytes(b: number): string {
  if (!b || b <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(units.length - 1, Math.floor(Math.log(b) / Math.log(1024)))
  return `${(b / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

export function AboutPanel({ stats, scrollToUpdates = false }: { stats: SystemStats | null; scrollToUpdates?: boolean }) {
  const [updates, setUpdates] = useState<UpdatesPayload | null>(null)
  const [dbBytes, setDbBytes] = useState<number | null>(null)
  const onData = useCallback((p: UpdatesPayload) => setUpdates(p), [])
  useEffect(() => {
    api.get<{ usage?: { fileBytes?: number } }>('/api/data/usage').then(r => setDbBytes(r?.usage?.fileBytes ?? null)).catch(() => {})
  }, [])

  const version = stats?.version || updates?.current || '—'
  const method = updates ? (METHOD_TEXT[updates.installMethod] ?? METHOD_TEXT.other) : null
  const online = stats?.connectivity?.online
  const line = [
    method ? `Installed as ${method.label.toLowerCase()}` : '',
    stats?.startedAt ? `running for ${duration(Date.now() - stats.startedAt)}` : '',
  ].filter(Boolean).join(' · ')

  const details: { label: string; value: string; tone?: string }[] = [
    { label: 'Time zone', value: serverTimeZone() },
    { label: 'Region', value: serverRegion() },
    { label: 'Address', value: window.location.host },
    { label: 'Database size', value: dbBytes != null ? bytes(dbBytes) : '—' },
    { label: 'Backup folder', value: stats?.backups?.dir || '—' },
    { label: 'Connectivity', value: online == null ? '—' : online ? 'Online' : 'Offline', tone: online == null ? undefined : online ? 'text-ok' : 'text-danger' },
    { label: 'Scheduler lag p50', value: figure(stats?.lagP50Ms, ' ms') },
    { label: 'Writes dropped', value: figure(stats?.writerDropped ?? 0) },
  ]

  return (
    <>
      <SettingsSection
        title={<span className="inline-flex items-center gap-2.5"><KestrelMark className="h-6 w-6 text-accent" />Opskest</span>}
        description={line || undefined}
        actions={<Badge tone="accent" className="font-mono">{version}</Badge>}>
        <SettingsStats columns="sm:grid-cols-3" items={[
          { label: 'Scheduler lag p99', value: figure(stats?.lagP99Ms, ' ms') },
          { label: 'Checks in flight', value: figure(stats?.inFlight) },
          { label: 'Monitors scheduled', value: figure(stats?.monitors) },
        ]} />

        <Collapsible title="Technical details">
          <dl className="grid gap-x-8 gap-y-2.5 sm:grid-cols-2">
            {details.map(d => (
              <div key={d.label} className="flex min-w-0 items-baseline justify-between gap-3 text-[13px]">
                <dt className="shrink-0 text-muted">{d.label}</dt>
                <dd className={`min-w-0 truncate text-right font-medium ${d.tone ?? 'text-body'}`} title={d.value}>{d.value}</dd>
              </div>
            ))}
            <div className="flex min-w-0 flex-col gap-1.5 sm:col-span-2">
              <dt className="text-[13px] text-muted">Prometheus metrics <span className="text-[12px] text-faint">· scrapers send METRICS_TOKEN as a bearer token</span></dt>
              <dd><CopyField label="Metrics path" text="/metrics" /></dd>
            </div>
          </dl>
        </Collapsible>

        <p className="text-[12px] leading-relaxed text-muted">
          Opskest is open source under the {PROJECT.license}.
          {PROJECT.repoUrl && <> <a href={PROJECT.repoUrl} target="_blank" rel="noreferrer noopener" className="font-medium text-accent underline-offset-2 hover:underline">Source and issues</a>.</>}
          {PROJECT.sponsorUrl && <> Sponsors keep it maintained and up to date: <a href={PROJECT.sponsorUrl} target="_blank" rel="noreferrer noopener" className="font-medium text-accent underline-offset-2 hover:underline">sponsor Opskest</a>.</>}
        </p>
      </SettingsSection>

      <UpdatesPanel onData={onData} scrollHere={scrollToUpdates} />
    </>
  )
}
