// The "Test now" result card: one check run with the current settings,
// nothing saved (POST /api/monitors/test). Outcome, reason, status code,
// latency, and — where measured — the DNS/TCP/TLS/TTFB breakdown and the
// certificate.

import { dateServer, dateOnlyServer, ms } from '../lib/format'
import type { CertInfo, Result, Timing } from '../lib/types'
import { errClassLabel } from './Clusters'
import { Badge } from './ui'

const PHASES: { key: keyof Timing; label: string; cls: string }[] = [
  { key: 'dnsMs', label: 'DNS', cls: 'bg-accent' },
  { key: 'tcpMs', label: 'TCP', cls: 'bg-ok' },
  { key: 'tlsMs', label: 'TLS', cls: 'bg-warn' },
  { key: 'ttfbMs', label: 'TTFB', cls: 'bg-faint' },
]

function TimingBreakdown({ timing }: { timing: Timing }) {
  const phases = PHASES.map(p => ({ ...p, v: timing[p.key] })).filter(p => p.v >= 0)
  if (phases.length === 0) return null
  const total = phases.reduce((a, p) => a + p.v, 0) || 1
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex h-2 rounded-full overflow-hidden bg-surface-2" role="img" aria-label={phases.map(p => `${p.label} ${ms(p.v)}`).join(', ')}>
        {phases.map(p => <span key={p.key} className={p.cls} style={{ width: `${(p.v / total) * 100}%` }} aria-hidden="true" />)}
      </div>
      <dl className="flex flex-wrap gap-x-4 gap-y-1 text-[12px]">
        {phases.map(p => (
          <div key={p.key} className="flex items-center gap-1.5">
            <span aria-hidden="true" className={`w-2 h-2 rounded-sm ${p.cls}`} />
            <dt className="text-muted">{p.label}</dt>
            <dd className="tnum text-body-2">{ms(p.v)}</dd>
          </div>
        ))}
      </dl>
    </div>
  )
}

function CertLine({ cert }: { cert: CertInfo }) {
  const days = Math.floor((cert.notAfter - Date.now()) / 86_400_000)
  return (
    <p className={`text-[12px] ${days < 0 ? 'text-danger' : days <= 14 ? 'text-warn' : 'text-body-2'}`}>
      Certificate{cert.subject ? <> for <span className="font-mono">{cert.subject}</span></> : null}
      {cert.issuer ? <> · issued by {cert.issuer}</> : null}
      {' · '}
      {days < 0
        ? <>expired {dateOnlyServer(cert.notAfter)} ({-days} day{-days === 1 ? '' : 's'} ago)</>
        : <>expires {dateOnlyServer(cert.notAfter)} ({days} day{days === 1 ? '' : 's'})</>}
    </p>
  )
}

export function TestResult({ result, error, onDismiss }: { result: Result | null; error: string | null; onDismiss?: () => void }) {
  if (error) {
    return (
      <div role="status" className="rounded-lg border border-danger/40 bg-danger-bg px-4 py-3 text-[13px] text-danger">
        <p className="font-semibold">Test could not run</p>
        <p className="mt-0.5">{error}</p>
      </div>
    )
  }
  if (!result) return null

  const tone = !result.ok ? 'danger' : result.degraded ? 'warn' : 'ok'
  const title = !result.ok ? 'Check failed' : result.degraded ? 'Up, but degraded' : 'Check passed'
  const frame = { ok: 'border-ok/40 bg-ok-bg', warn: 'border-warn/40 bg-warn-bg', danger: 'border-danger/40 bg-danger-bg' }[tone]
  const text = { ok: 'text-ok', warn: 'text-warn', danger: 'text-danger' }[tone]

  return (
    <section role="status" aria-label="Test result" className={`rounded-lg border px-4 py-3 flex flex-col gap-2.5 ${frame}`}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className={`text-[14px] font-semibold ${text}`}>
          <span aria-hidden="true">{result.ok ? (result.degraded ? '◐ ' : '✓ ') : '✕ '}</span>{title}
        </span>
        {result.latencyMs >= 0 && <span className="text-[13px] tnum text-body-2">{ms(result.latencyMs)}</span>}
        {result.code > 0 && <Badge tone={result.code < 400 ? 'neutral' : 'danger'}>HTTP {result.code}</Badge>}
        {result.errClass && <Badge tone="neutral">{errClassLabel(result.errClass)}</Badge>}
        {result.lossPct > 0 && <Badge tone="warn">{Math.round(result.lossPct)}% packet loss</Badge>}
        <span className="ml-auto text-[11px] text-faint tnum">{dateServer(result.at)}</span>
        {onDismiss && (
          <button type="button" onClick={onDismiss} aria-label="Dismiss test result" className="text-muted hover:text-body leading-none text-base">&times;</button>
        )}
      </div>
      {result.reason && <p className="text-[13px] text-body leading-relaxed break-words">{result.reason}</p>}
      {result.timing && <TimingBreakdown timing={result.timing} />}
      {result.cert && <CertLine cert={result.cert} />}
      <p className="text-[11px] text-faint">A one-off run with these settings — nothing was saved.</p>
    </section>
  )
}
