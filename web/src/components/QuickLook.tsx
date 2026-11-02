import { useEffect, useState, type ReactNode } from 'react'
import { Link } from 'wouter'
import { BeatBar } from './Status'
import { Badge, Button, Drawer, Icon, SkeletonBlock, Timestamp } from './ui'
import { api } from '../lib/api'
import { dateServer, duration, ms, pct } from '../lib/format'
import { useLive } from '../lib/live'
import { normIncident } from '../lib/normalize'
import { severityLabel } from '../hooks/useTeam'
import type { Incident } from '../lib/types'

/** What the drawer needs from a "Needs attention" row. */
export interface QuickRow {
  title: string
  kind: 'outage' | 'monitor' | 'incident'
  href: string
  status: ReactNode
  detail: string
  owner?: string
  since: number
  monitorId?: number
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return <section className="flex flex-col gap-2.5"><h3 className="text-[12px] font-medium text-muted">{title}</h3>{children}</section>
}

function Facts({ rows }: { rows: [string, ReactNode][] }) {
  return (
    <dl className="grid grid-cols-[112px_minmax(0,1fr)] gap-x-4 gap-y-2.5 text-[13px]">
      {rows.map(([k, v]) => <div key={k} className="contents"><dt className="text-muted">{k}</dt><dd className="min-w-0 text-body-2 [overflow-wrap:anywhere]">{v}</dd></div>)}
    </dl>
  )
}

function Stat({ label, value }: { label: string; value: string }) {
  return <div className="rounded-[10px] border border-default bg-ground px-3 py-2"><span className="block text-[11px] text-muted leading-tight">{label}</span><strong className="block text-[15px] font-semibold tnum text-body">{value}</strong></div>
}

/**
 * A side panel for one "Needs attention" item: what is wrong and since when,
 * who has it, how the service has behaved and its key settings. The row
 * itself still links to the full page.
 */
export function QuickLook({ row, open, onClose }: { row: QuickRow | null; open: boolean; onClose: () => void }) {
  const live = useLive()
  const monitor = row?.monitorId != null ? live.monitors.get(row.monitorId) : undefined
  const groups = new Map(live.groups.map(g => [g.id, g.name]))
  const outage = monitor ? live.outages.find(o => o.monitorId === monitor.id && !o.endedAt) : undefined
  const incidentId = row?.kind === 'incident' ? Number(row.href.split('/').pop()) : 0
  const [incident, setIncident] = useState<Incident | null>(null)

  useEffect(() => {
    if (!open || !incidentId) { setIncident(null); return }
    let alive = true
    api.get<unknown>(`/api/incidents/${incidentId}`).then(d => { if (alive) setIncident(normIncident(d)) }).catch(() => { if (alive) setIncident(null) })
    return () => { alive = false }
  }, [open, incidentId])

  const lat = monitor ? monitor.lat.filter(v => v >= 0) : []
  const avg = lat.length ? lat.reduce((a, b) => a + b, 0) / lat.length : null
  const failures = monitor ? monitor.beats.split('').filter(c => c === 'd').length : 0

  const response: [string, ReactNode][] = [['Owner', row?.owner ?? 'Not acknowledged']]
  if (outage) {
    response.push(['Outage started', dateServer(outage.startedAt)])
    const note = outage.note ? ` Latest: “${outage.note.length > 90 ? outage.note.slice(0, 90) + '…' : outage.note}”` : ''
    response.push(['Notes', outage.noteCount ? `${outage.noteCount} ${outage.noteCount === 1 ? 'note.' : 'notes.'}${note}` : 'No notes yet'])
    if (outage.regions) response.push(['Regions', `${outage.regions.reachable} of ${outage.regions.total} probes reach it`])
  }
  if (monitor?.errClass) response.push(['Failure type', monitor.errClass.replace(/_/g, ' ')])

  const settings: [string, ReactNode][] = monitor ? [
    ['Type', monitor.type.toUpperCase()],
    ['Target', <span key="t" className="font-mono text-[12px]">{monitor.target}</span>],
    ['Project', groups.get(monitor.groupId ?? 0) ?? 'Unassigned'],
    ['Checks every', duration(monitor.intervalS * 1000)],
    ['Last check', <Timestamp key="l" at={monitor.lastCheckAt} future={false} />],
    ['Response', monitor.lastCheckAt && !monitor.paused && monitor.latencyMs >= 0 ? ms(monitor.latencyMs) : '—'],
  ] : []
  if (monitor?.certDays != null) settings.push(['Certificate', `expires in ${monitor.certDays} days`])
  if (monitor?.domainDays != null) settings.push(['Domain', `expires in ${monitor.domainDays} days`])
  if (monitor?.runbook) {
    settings.push(['Runbook', /^https?:\/\//i.test(monitor.runbook)
      ? <a key="r" href={monitor.runbook} target="_blank" rel="noopener noreferrer" className="text-accent hover:underline">{monitor.runbook}</a>
      : monitor.runbook])
  }

  return (
    <Drawer
      open={open && row != null}
      onClose={onClose}
      title={row?.title ?? ''}
      description={row?.kind === 'incident' ? 'Incident' : 'Monitor'}
      footer={row && <Link href={row.href} onClick={onClose}><Button variant="primary" icon={<Icon.ArrowRight className="h-3.5 w-3.5" />}>Open full page</Button></Link>}
    >
      {row && (
        <div className="flex flex-col gap-6">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            {row.status}
            <span className="text-[12px] text-muted"><Timestamp at={row.since} prefix="since" bare future={false} /></span>
            <span className="text-[12px] text-muted tnum">· {dateServer(row.since)}</span>
          </div>
          {row.detail && <p className="rounded-[10px] border border-default bg-surface-2/50 px-3.5 py-3 text-[13px] leading-relaxed text-body-2 [overflow-wrap:anywhere]">{row.detail}</p>}

          {monitor && (
            <>
              <Section title="Response"><Facts rows={response} /></Section>
              <Section title="Uptime">
                <div className="grid grid-cols-4 gap-2">
                  <Stat label="24 hours" value={pct(monitor.uptime24h)} />
                  <Stat label="7 days" value={pct(monitor.uptime7d)} />
                  <Stat label="30 days" value={pct(monitor.uptime30d)} />
                  <Stat label="90 days" value={pct(monitor.uptime90d)} />
                </div>
              </Section>
              <Section title="Recent checks">
                <BeatBar beats={monitor.beats} count={48} size="sm" intervalS={monitor.intervalS} lastCheckAt={monitor.lastCheckAt} />
                <p className="text-[12px] text-muted tnum">
                  {failures} failed of the last {monitor.beats.length}
                  {avg != null && <> · average {ms(avg)}, fastest {ms(Math.min(...lat))}, slowest {ms(Math.max(...lat))}</>}
                </p>
              </Section>
              <Section title="Monitor"><Facts rows={settings} /></Section>
            </>
          )}

          {row.kind === 'incident' && (
            incident ? (
              <>
                <Section title="Incident">
                  <Facts rows={[
                    ['Status', <span key="s" className="capitalize">{incident.status}</span>],
                    ['Severity', severityLabel(incident.severity)],
                    ['Impact', <span key="i" className="capitalize">{incident.impact}</span>],
                    ['Owner', incident.commanderName || 'No owner'],
                    ['Opened', dateServer(incident.createdAt)],
                    ['Last update', <Timestamp key="u" at={incident.updatedAt} future={false} />],
                    ['Affected', incident.monitorIds.length ? incident.monitorIds.map(id => live.monitors.get(id)?.name ?? `Monitor ${id}`).join(', ') : 'No monitors linked'],
                  ]} />
                </Section>
                {incident.updates.length > 0 && (
                  <Section title="Latest updates">
                    <ol className="flex flex-col gap-2.5">
                      {[...incident.updates].sort((a, b) => b.createdAt - a.createdAt).slice(0, 4).map(u => (
                        <li key={u.id} className="rounded-[10px] border border-default px-3.5 py-3">
                          <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[12px] text-muted">
                            <span className="font-medium text-body-2 capitalize">{u.status}</span><span>·</span><Timestamp at={u.createdAt} future={false} />
                            {u.internal && <Badge tone="neutral">Team only</Badge>}
                          </p>
                          <p className="mt-1.5 text-[13px] leading-relaxed text-body-2 [overflow-wrap:anywhere] line-clamp-4">{u.body}</p>
                          {u.createdByName && <p className="mt-1.5 text-[11px] text-faint">{u.createdByName}</p>}
                        </li>
                      ))}
                    </ol>
                  </Section>
                )}
              </>
            ) : <SkeletonBlock className="h-32" />
          )}
        </div>
      )}
    </Drawer>
  )
}
