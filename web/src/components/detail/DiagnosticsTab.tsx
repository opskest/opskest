import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, errorText, unwrapList } from '../../lib/api'
import { ago, dateServer } from '../../lib/format'
import { normHeartbeat } from '../../lib/normalize'
import type { Heartbeat, MonitorSummary } from '../../lib/types'
import {
  cadence, describeMonitor, failureBreakdown, isFailedCheck, latencySummary, pingLoss, timeReporting, typeInfo,
  type DiagCert, type DiagConfig, type DiagnosticsData,
} from '../../lib/diagnostics'
import { Button, EmptyState, ErrorRetry, Icon, SkeletonBlock } from '../ui'
import { LatencyBreakdown } from './LatencyBreakdown'
import { WorldwideCard } from './WorldwideCard'
import { DnsRotateCard } from './DnsRotateCard'
import {
  CadenceCard, CertCard, DescribeCard, DnsCard, FailuresCard, LastCheckCard, NoCertCard, PingCard, Section, SingleTimeCard, SqlCard,
} from './DiagnosticsCards'

// The Diagnostics tab: what the monitor checks, what the last check said, why
// recent checks failed, where the time went, and how steady the schedule is,
// shaped by the monitor's type. It reads GET /api/monitors/{id}/diagnostics
// (the last 200 checks, non-secret settings and the remembered certificate)
// and falls back to the 50 checks the page already loaded when that fails.
// A section appears only when it has real data; nothing here is estimated.

interface Loaded { id: number; data: DiagnosticsData; fetchedAt: number }

function parse(raw: unknown): DiagnosticsData {
  const o = (raw && typeof raw === 'object' ? raw : {}) as Record<string, unknown>
  const cert = o.cert && typeof o.cert === 'object' ? o.cert as Record<string, unknown> : null
  return {
    now: typeof o.now === 'number' ? o.now : Date.now(),
    monitor: (o.monitor ?? {}) as DiagConfig,
    cert: cert && typeof cert.notAfter === 'number'
      ? { notAfter: cert.notAfter, issuer: String(cert.issuer ?? ''), subject: String(cert.subject ?? '') } as DiagCert
      : null,
    beats: unwrapList<unknown>(raw, 'beats').map(normHeartbeat),
  }
}

const hasPhases = (b: Heartbeat) => b.dnsMs >= 0 || b.tcpMs >= 0 || b.ttfbMs >= 0

export function DiagnosticsTab({ monitor, isAdmin, fallbackBeats }: { monitor: MonitorSummary; isAdmin: boolean; fallbackBeats: Heartbeat[] }) {
  const [loaded, setLoaded] = useState<Loaded | null>(null)
  const [failure, setFailure] = useState<{ id: number; message: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const [reload, setReload] = useState(0)

  // A new check arrives (lastCheckAt moves) or Refresh is pressed: read again.
  // The previous data stays on screen meanwhile, so the tab never blanks.
  useEffect(() => {
    let alive = true
    setBusy(true)
    api.get<unknown>(`/api/monitors/${monitor.id}/diagnostics`)
      .then(raw => { if (alive) { setLoaded({ id: monitor.id, data: parse(raw), fetchedAt: Date.now() }); setFailure(null) } })
      .catch(e => { if (alive) setFailure({ id: monitor.id, message: errorText(e, 'Diagnostics could not load.') }) })
      .finally(() => { if (alive) setBusy(false) })
    return () => { alive = false }
  }, [monitor.id, monitor.lastCheckAt, reload])

  const refresh = useCallback(() => setReload(n => n + 1), [])
  const current = loaded && loaded.id === monitor.id ? loaded : null
  const error = failure && failure.id === monitor.id ? failure.message : ''
  const data = current?.data ?? null
  const cfg = data && data.monitor.type ? data.monitor : null
  const beats = useMemo(() => (data ? data.beats : fallbackBeats), [data, fallbackBeats])
  const type = cfg?.type ?? monitor.type
  // Server clock, carried forward from when the response was read.
  const now = data && current ? data.now + (Date.now() - current.fetchedAt) : Date.now()

  const view = useMemo(() => {
    const last = beats[0] ?? null
    const chrono = [...beats].reverse()
    const phaseBeat = beats.find(hasPhases) ?? null
    const failed = beats.filter(isFailedCheck)
    return {
      last,
      breakdown: failureBreakdown(beats),
      summary: latencySummary(beats),
      reporting: timeReporting(beats),
      chrono: chrono.slice(-100),
      phaseBeat,
      loss: pingLoss(beats),
      cadence: cfg ? cadence(beats, cfg) : null,
      lastFailure: failed[0] ?? null,
    }
  }, [beats, cfg])

  if (!current && !error && fallbackBeats.length === 0) return <SkeletonBlock lines={5} label="Loading diagnostics" />

  const description = cfg ? describeMonitor(cfg, monitor.target) : null
  const { last, breakdown, summary, reporting, chrono, phaseBeat, loss, cadence: cad, lastFailure } = view
  const info = typeInfo(type)
  const worldwide = !monitor.vendor && (type === 'http' || type === 'ping' || type === 'dns')

  return (
    <div className="space-y-6 min-w-0">
      {error && (
        <ErrorRetry message={current ? `Could not refresh diagnostics (${error}). Showing the last data that loaded.` : `${/[.!?]$/.test(error) ? error : `${error}.`} Showing the most recent checks the page already has.`} onRetry={refresh} />
      )}

      <div className="flex flex-wrap items-center justify-between gap-3 text-sm text-muted">
        <p>
          {beats.length > 0
            ? <>Based on the last {beats.length} recorded {beats.length === 1 ? 'check' : 'checks'}, {dateServer(beats[beats.length - 1].ts)} to {dateServer(beats[0].ts)}.</>
            : 'No checks have been recorded yet.'}
        </p>
        <Button size="sm" busy={busy} onClick={refresh} icon={<Icon.Refresh className="w-3.5 h-3.5" />}>Refresh</Button>
      </div>

      {description && <DescribeCard type={type} lines={description} />}

      {beats.length === 0 ? (
        <EmptyState icon={<Icon.Activity className="w-5 h-5" />} title="No checks recorded yet">
          Diagnostics are built from the checks this monitor runs. They appear after its first check{monitor.paused ? ', which will run once the monitor is resumed' : ''}.
        </EmptyState>
      ) : (
        <>
          {last && <LastCheckCard beat={last} cfg={cfg} type={type} ago={ago(last.ts)} />}

          <FailuresCard b={breakdown} type={type} />

          {type === 'http' && !monitor.vendor && (phaseBeat
            ? <LatencyBreakdown beat={phaseBeat} recent={beats} newestTs={last?.ts} />
            : (
              <Section id="diag-time" title="Time spent">
                <p className="text-sm text-body-2">No request timings are recorded for the last {beats.length} checks. DNS, connect, TLS and first-byte times exist only for requests that got far enough to measure them.</p>
              </Section>
            ))}
          {type !== 'http' && type !== 'ping' && summary && reporting === 'single' && <SingleTimeCard type={type} s={summary} chrono={chrono} />}
          {type === 'http' && monitor.vendor && summary && <SingleTimeCard type={type} s={summary} chrono={chrono} />}
          {type !== 'http' && type !== 'ping' && type !== 'push' && !summary && info.timeCovers && (
            <Section id="diag-time" title="Time spent">
              <p className="text-sm text-body-2">None of the last {beats.length} checks recorded a duration, so there is nothing to chart. This monitor type reports one total time per check: {info.timeCovers}.</p>
            </Section>
          )}

          {data?.cert && info.cert && cfg && <CertCard cert={data.cert} warnDays={cfg.sslWarnDays} now={now} />}
          {!data?.cert && cfg && type === 'tls' && <NoCertCard />}

          {type === 'ping' && <PingCard s={summary} loss={loss} cfg={cfg} />}
          {type === 'dns' && cfg && <DnsCard cfg={cfg} lastMismatch={lastFailure} />}
          {(type === 'postgres_sql' || type === 'mysql_sql') && cfg && <SqlCard cfg={cfg} last={last} lastFailure={lastFailure} />}

          {cfg && cad && cad.gaps > 0 && <CadenceCard cfg={cfg} c={cad} />}
        </>
      )}

      {worldwide && <WorldwideCard monitorId={monitor.id} isAdmin={isAdmin} />}
      {type === 'dns' && <DnsRotateCard monitorId={monitor.id} isAdmin={isAdmin} down={monitor.status === 'down'} reason={monitor.reason} />}
    </div>
  )
}
