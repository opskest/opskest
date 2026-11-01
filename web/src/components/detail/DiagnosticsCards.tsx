import type { ReactNode } from 'react'
import type { Heartbeat } from '../../lib/types'
import { dateServer, ms, pct } from '../../lib/format'
import {
  OUTCOME_LABEL, certDaysLeft, certSeverity, dnsMatchWord, explainClass, outcomeOf, spanText, sqlProbeInfo, typeInfo,
  type Cadence, type CertSeverity, type DiagCert, type DiagConfig, type FailureBreakdown, type LatencySummary, type Outcome, type PingLoss,
} from '../../lib/diagnostics'
import { Badge, type Tone } from '../ui'

// The cards of the Diagnostics tab. Each one is shown only when the data it
// reads exists; the numbers all come from lib/diagnostics.ts.

export function Section({ id, title, hint, aside, children }: { id: string; title: string; hint?: ReactNode; aside?: ReactNode; children: ReactNode }) {
  return (
    <section aria-labelledby={id} className="rounded-[14px] bg-surface border border-default p-5 space-y-5 min-w-0">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 id={id} className="text-base font-semibold text-body">{title}</h3>
          {hint && <p className="text-sm text-muted mt-1">{hint}</p>}
        </div>
        {aside}
      </div>
      {children}
    </section>
  )
}

function Stats({ items }: { items: { label: string; value: ReactNode; sub?: string }[] }) {
  return (
    <dl className="flex flex-wrap gap-x-7 gap-y-4 py-4 border-y border-default">
      {items.map(i => (
        <div key={i.label} className="min-w-0">
          <dt className="text-xs text-muted">{i.label}</dt>
          <dd className="text-base font-medium text-body tnum mt-1 [overflow-wrap:anywhere]">{i.value}</dd>
          {i.sub && <p className="text-[11px] text-faint mt-0.5">{i.sub}</p>}
        </div>
      ))}
    </dl>
  )
}

function Reason({ text, label = 'Reason as recorded' }: { text: string; label?: string }) {
  return (
    <div>
      <p className="text-xs text-muted mb-1.5">{label}</p>
      <div className="font-mono text-[13px] leading-relaxed text-body whitespace-pre-wrap [overflow-wrap:anywhere] bg-surface-2 border border-default rounded-lg px-3 py-2.5">{text}</div>
    </div>
  )
}

function NextSteps({ steps }: { steps: string[] }) {
  return (
    <div className="rounded-lg bg-surface-2/60 border border-default px-4 py-3">
      <p className="text-xs font-medium text-muted mb-1.5">What to check next</p>
      <ul className="list-disc pl-4 space-y-1 text-[13px] text-body-2 leading-relaxed">
        {steps.map(s => <li key={s}>{s}</li>)}
      </ul>
    </div>
  )
}

const OUTCOME_TONE: Record<Outcome, Tone> = {
  passed: 'ok', degraded: 'warn', retrying: 'warn', failed: 'danger', offline: 'neutral', maintenance: 'accent',
}

const RCODE: Record<number, string> = { 1: 'FORMERR', 2: 'SERVFAIL', 3: 'NXDOMAIN', 4: 'NOTIMP', 5: 'REFUSED' }

// ── What this monitor checks ────────────────────────────────────────────────

export function DescribeCard({ type, lines }: { type: string; lines: string[] }) {
  return (
    <Section id="diag-what" title="What this monitor checks" aside={<Badge>{typeInfo(type).label}</Badge>}>
      <div className="space-y-2 max-w-3xl">
        {lines.map(l => <p key={l} className="text-sm text-body-2 leading-relaxed [overflow-wrap:anywhere]">{l}</p>)}
      </div>
    </Section>
  )
}

// ── Last check ──────────────────────────────────────────────────────────────

export function LastCheckCard({ beat, cfg, type, ago }: { beat: Heartbeat; cfg: DiagConfig | null; type: string; ago: string }) {
  const o = outcomeOf(beat)
  const failing = o === 'failed' || o === 'retrying'
  const info = failing ? explainClass(beat.errClass, type) : null
  const code = beat.code > 0 ? (type === 'dns' ? `${RCODE[beat.code] ?? 'rcode'} (${beat.code})` : String(beat.code)) : null
  return (
    <Section id="diag-last" title="Last check" hint={<>{dateServer(beat.ts)} · {ago}</>} aside={<Badge tone={OUTCOME_TONE[o]} dot>{OUTCOME_LABEL[o]}</Badge>}>
      <Stats items={[
        { label: 'Duration', value: beat.latencyMs >= 0 ? ms(beat.latencyMs) : type === 'push' ? 'Not reported' : 'Not measured', sub: beat.latencyMs < 0 && failing && type !== 'push' ? 'The check ended before a duration was known' : undefined },
        ...(code ? [{ label: type === 'dns' ? 'DNS response code' : 'Status code', value: code }] : []),
        ...(failing ? [{ label: 'Error class', value: info!.label }] : []),
        ...(cfg && failing && type !== 'push' ? [{ label: 'Timeout limit', value: spanText(cfg.timeoutMs) }] : []),
      ]} />

      {failing && info && (
        <div className="space-y-4">
          {o === 'retrying' && (
            <p className="text-sm text-body-2">This failure is being retried before the monitor is marked down{cfg && cfg.retries > 0 ? ` (up to ${cfg.retries} ${cfg.retries === 1 ? 'retry' : 'retries'}, ${spanText(cfg.retryIntervalS * 1000)} apart)` : ''}.</p>
          )}
          <div>
            <p className="text-xs font-medium text-muted mb-1">What this usually means</p>
            <p className="text-sm text-body-2 leading-relaxed">{info.meaning}</p>
          </div>
          {cfg?.upsideDown && <p className="text-sm text-body-2">Upside-down mode is on, so a reachable target is the failure here.</p>}
          {beat.msg ? <Reason text={beat.msg} /> : <p className="text-sm text-muted">No reason text was recorded for this check.</p>}
          <NextSteps steps={info.next} />
        </div>
      )}

      {o === 'degraded' && (
        <div className="space-y-4">
          <p className="text-sm text-body-2 leading-relaxed">The check succeeded but crossed a warning limit: slower than this monitor&rsquo;s &ldquo;degraded after&rdquo; time, a certificate close to expiry, or ping loss above its limit.</p>
          {beat.msg && <Reason text={beat.msg} />}
        </div>
      )}

      {o === 'offline' && (
        <div className="space-y-4">
          <p className="text-sm text-body-2 leading-relaxed">Opskest could not reach the internet when this check ran, so the result says nothing about the target and was not counted against it.</p>
          {beat.msg && <Reason text={beat.msg} />}
        </div>
      )}

      {o === 'maintenance' && <p className="text-sm text-body-2 leading-relaxed">This check ran inside a maintenance window, so alerts and uptime ignore it.</p>}

      {o === 'passed' && (
        <div className="space-y-3">
          <p className="text-sm text-body-2">The check passed. No failure reason is recorded for a passing check.</p>
          {(beat.conn || beat.edge) && (
            <dl className="grid gap-x-8 gap-y-2 sm:grid-cols-2 text-sm">
              {beat.conn && <div className="min-w-0"><dt className="text-xs text-muted">Connection</dt><dd className="font-mono text-[13px] text-body [overflow-wrap:anywhere]">{beat.conn}</dd></div>}
              {beat.edge && <div className="min-w-0"><dt className="text-xs text-muted">Answered by</dt><dd className="font-mono text-[13px] text-body [overflow-wrap:anywhere]">{beat.edge}</dd></div>}
            </dl>
          )}
        </div>
      )}
    </Section>
  )
}

// ── Why checks failed lately ────────────────────────────────────────────────

export function FailuresCard({ b, type }: { b: FailureBreakdown; type: string }) {
  const span = b.from && b.to ? `${dateServer(b.from)} to ${dateServer(b.to)}` : ''
  const excluded: string[] = []
  if (b.excludedMaintenance) excluded.push(`${b.excludedMaintenance} during maintenance`)
  if (b.excludedOffline) excluded.push(`${b.excludedOffline} while this server was offline`)
  return (
    <Section id="diag-failures" title="Why checks failed lately"
      hint={<>The last {b.judged} judged {b.judged === 1 ? 'check' : 'checks'}{span ? <> · {span}</> : null}</>}
      aside={b.judged > 0 ? <Badge tone={b.failed ? 'danger' : 'ok'} dot>{b.failed} failed of {b.judged}</Badge> : undefined}>
      {b.failed === 0 ? (
        <p className="text-sm text-body-2">{b.judged === 0 ? 'No judged checks yet.' : `None of the last ${b.judged} checks failed.`}{b.degraded > 0 && ` ${b.degraded} ${b.degraded === 1 ? 'was' : 'were'} degraded (slow or near a limit).`}</p>
      ) : (
        <ul className="space-y-3">
          {b.groups.map(g => {
            const info = explainClass(g.key === 'unclassified' ? '' : g.key, type)
            return (
              <li key={g.key} className="rounded-lg border border-default bg-surface-2/30 px-4 py-3.5 space-y-3">
                <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                  <span className="text-[13px] font-semibold text-body">{g.label}</span>
                  <span className="text-sm text-muted tnum">{g.count} {g.count === 1 ? 'check' : 'checks'} · {Math.round(g.share)}% of failures</span>
                  <span className="sm:ml-auto text-xs text-faint">Latest {dateServer(g.latestTs)}</span>
                </div>
                <div aria-hidden="true" className="h-1.5 rounded-full bg-surface-2 overflow-hidden"><div className="h-full rounded-full bg-danger" style={{ width: `${Math.max(3, g.share)}%` }} /></div>
                {g.latestReason ? <Reason text={g.latestReason} label="Most recent reason as recorded" /> : <p className="text-sm text-muted">No reason text was recorded for the latest one.</p>}
                <details>
                  <summary className="cursor-pointer text-[13px] font-medium text-accent hover:underline rounded-md">What this means and what to check</summary>
                  <div className="pt-3 space-y-3">
                    <p className="text-sm text-body-2 leading-relaxed">{info.meaning}</p>
                    <NextSteps steps={info.next} />
                  </div>
                </details>
              </li>
            )
          })}
        </ul>
      )}
      {excluded.length > 0 && <p className="text-xs text-faint">Left out: {excluded.join(', ')}.</p>}
    </Section>
  )
}

// ── Time spent (single total) ───────────────────────────────────────────────

/** One bar per check, oldest to newest; failed checks (no duration) are short red marks. */
function LatencyStrip({ chrono }: { chrono: Heartbeat[] }) {
  const lat = chrono.map(b => (b.latencyMs >= 0 ? b.latencyMs : null))
  const max = Math.max(1, ...lat.filter((v): v is number => v != null))
  const failed = chrono.filter((b, i) => lat[i] == null && (outcomeOf(b) === 'failed' || outcomeOf(b) === 'retrying')).length
  return (
    <div>
      <div role="img" aria-label={`Duration of each of the last ${chrono.length} checks, oldest to newest: up to ${ms(max)}, ${failed} failed without a duration`}
        className="flex items-end gap-px h-16">
        {chrono.map((b, i) => {
          const v = lat[i]
          const o = outcomeOf(b)
          if (v == null) {
            return <span key={b.ts} title={`${dateServer(b.ts)} · ${OUTCOME_LABEL[o]}`} className={`flex-1 min-w-[2px] h-[18%] rounded-sm ${o === 'failed' || o === 'retrying' ? 'bg-danger' : 'bg-muted/40'}`} />
          }
          return <span key={b.ts} title={`${dateServer(b.ts)} · ${ms(v)}`} style={{ height: `${Math.max(4, (v / max) * 100)}%` }} className={`flex-1 min-w-[2px] rounded-sm ${o === 'degraded' ? 'bg-warn' : 'bg-accent'}`} />
        })}
      </div>
      <p className="text-[11px] text-faint mt-1.5">Oldest to newest. Red marks are failed checks that recorded no duration.</p>
    </div>
  )
}

export function SingleTimeCard({ type, s, chrono, extra }: { type: string; s: LatencySummary; chrono: Heartbeat[]; extra?: ReactNode }) {
  const info = typeInfo(type)
  return (
    <Section id="diag-time" title="Time spent" hint={<>This monitor type reports one total time per check: {info.timeCovers}. It is not split into phases.</>}>
      <Stats items={[
        { label: 'Fastest', value: ms(s.min) },
        { label: 'Median', value: ms(s.p50) },
        { label: 'Average', value: ms(s.avg) },
        { label: 'p95', value: ms(s.p95) },
        { label: 'Slowest', value: ms(s.max) },
        { label: 'Jitter', value: s.jitter == null ? '—' : ms(s.jitter), sub: s.jitter == null ? 'Needs two checks in a row' : 'Mean change between checks' },
      ]} />
      <p className="text-xs text-faint -mt-2">From {s.count} {s.count === 1 ? 'check' : 'checks'} that recorded a duration, out of the last {chrono.length} recorded. Exact values, not estimates.</p>
      <LatencyStrip chrono={chrono} />
      {extra}
    </Section>
  )
}

// ── Certificate ─────────────────────────────────────────────────────────────

const SEV: Record<CertSeverity, { tone: Tone; word: string; text: string }> = {
  ok: { tone: 'ok', word: 'Valid', text: 'text-ok' },
  warning: { tone: 'warn', word: 'Expires soon', text: 'text-warn' },
  critical: { tone: 'danger', word: 'Expires very soon', text: 'text-danger' },
  expired: { tone: 'danger', word: 'Expired', text: 'text-danger' },
}

export function CertCard({ cert, warnDays, now }: { cert: DiagCert; warnDays: number; now: number }) {
  const days = certDaysLeft(cert.notAfter, now)
  const sev = certSeverity(days, warnDays)
  const left = days < 0 ? `Expired ${-days} ${-days === 1 ? 'day' : 'days'} ago` : days === 0 ? 'Expires within a day' : `${days} ${days === 1 ? 'day' : 'days'} left`
  return (
    <Section id="diag-cert" title="Certificate" hint="From the most recent check that saw a certificate."
      aside={<Badge tone={SEV[sev].tone} dot>{SEV[sev].word}</Badge>}>
      <Stats items={[
        { label: 'Expires', value: dateServer(cert.notAfter) },
        { label: 'Time left', value: <span className={SEV[sev].text}>{left}</span>, sub: warnDays > 0 ? `Warns ${warnDays} days before expiry` : 'Expiry warnings are off' },
        { label: 'Issuer', value: cert.issuer || 'Not named' },
        { label: 'Subject', value: cert.subject || 'Not named' },
      ]} />
    </Section>
  )
}

export function NoCertCard() {
  return (
    <Section id="diag-cert" title="Certificate">
      <p className="text-sm text-body-2">No certificate has been recorded yet. One is saved after a check completes a TLS handshake.</p>
    </Section>
  )
}

// ── Ping ────────────────────────────────────────────────────────────────────

export function PingCard({ s, loss, cfg }: { s: LatencySummary | null; loss: PingLoss; cfg: DiagConfig | null }) {
  const limit = cfg?.maxLossPct ?? 34
  return (
    <Section id="diag-ping" title="Ping quality" hint={cfg ? `${cfg.count || 3} echo requests per check` : undefined}>
      {s ? (
        <>
          <Stats items={[
            { label: 'Fastest', value: ms(s.min) },
            { label: 'Average', value: ms(s.avg) },
            { label: 'Slowest', value: ms(s.max) },
            { label: 'Jitter', value: s.jitter == null ? '—' : ms(s.jitter), sub: s.jitter == null ? 'Needs two checks in a row' : 'Mean change between checks' },
          ]} />
          <p className="text-xs text-faint -mt-2">Each check records the average round trip of its echoes, so these figures describe {s.count} {s.count === 1 ? 'check' : 'checks'}, not single packets.</p>
        </>
      ) : <p className="text-sm text-body-2">No check in this window got a reply, so there is no round-trip time to report.</p>}
      <Stats items={[
        { label: 'Checks that lost every packet', value: `${loss.totalLoss} of ${loss.checks}` },
        { label: `Checks degraded by loss above ${limit}%`, value: `${loss.aboveLimit} of ${loss.checks}` },
        { label: 'Worst loss reported', value: loss.worst == null ? 'None reported' : `${loss.worst}%` },
      ]} />
      <p className="text-xs text-faint -mt-2">Packet loss is not stored for every check. It is known only for checks that lost everything or crossed the {limit}% limit; smaller loss is recorded as a normal pass, so these counts are a minimum.</p>
    </Section>
  )
}

// ── DNS ─────────────────────────────────────────────────────────────────────

export function DnsCard({ cfg, lastMismatch }: { cfg: DiagConfig; lastMismatch: Heartbeat | null }) {
  const expected = cfg.expectedValues ?? []
  return (
    <Section id="diag-dns" title="DNS lookup" hint="Resolver, record and the answers this monitor expects.">
      <Stats items={[
        { label: 'Resolver', value: cfg.resolver || 'This server’s own resolver' },
        { label: 'Record type', value: cfg.record || 'A' },
        { label: 'Passes when', value: expected.length ? dnsMatchWord(cfg.match) : 'At least one record is returned' },
      ]} />
      {expected.length > 0 && (
        <div>
          <p className="text-xs text-muted mb-1.5">Expected answers</p>
          <ul className="flex flex-wrap gap-2">{expected.map(v => <li key={v} className="font-mono text-[13px] text-body bg-surface-2 border border-default rounded-md px-2 py-1 [overflow-wrap:anywhere]">{v}</li>)}</ul>
        </div>
      )}
      {lastMismatch?.msg ? (
        <Reason text={lastMismatch.msg} label={`Latest failed check, as recorded (${dateServer(lastMismatch.ts)}). When the lookup got answers, they are named here.`} />
      ) : (
        <p className="text-sm text-body-2">Observed answers are recorded only when a check fails, in its reason. None of the recent checks failed.</p>
      )}
    </Section>
  )
}

// ── SQL ─────────────────────────────────────────────────────────────────────

export function SqlCard({ cfg, last, lastFailure }: { cfg: DiagConfig; last: Heartbeat | null; lastFailure: Heartbeat | null }) {
  const probe = sqlProbeInfo(cfg)
  return (
    <Section id="diag-sql" title="Database probe" hint="Read-only. Logs in, runs one probe, never changes data.">
      {probe && (
        <dl className="grid gap-x-8 gap-y-3 sm:grid-cols-3 text-sm">
          <div><dt className="text-xs text-muted">Probe</dt><dd className="text-body font-medium mt-0.5">{probe.label}</dd></div>
          <div><dt className="text-xs text-muted">What it measures</dt><dd className="text-body-2 mt-0.5 leading-relaxed">{probe.measures}</dd></div>
          <div><dt className="text-xs text-muted">Marked down when</dt><dd className="text-body-2 mt-0.5 leading-relaxed">{probe.downWhen}</dd></div>
        </dl>
      )}
      <Stats items={[
        { label: 'Last duration', value: last && last.latencyMs >= 0 ? ms(last.latencyMs) : 'Not measured' },
      ]} />
      {lastFailure?.msg && <Reason text={lastFailure.msg} label={`Latest failure reason (${dateServer(lastFailure.ts)})`} />}
      <p className="text-xs text-faint">The measured value is not stored for passing checks. A failure reason carries counts and durations only; query text, result rows and credentials are never recorded.</p>
    </Section>
  )
}

// ── Cadence ─────────────────────────────────────────────────────────────────

export function CadenceCard({ cfg, c }: { cfg: DiagConfig; c: Cadence }) {
  const push = cfg.type === 'push'
  const rows = [
    ...c.missing.map(g => ({ ...g, kind: 'No checks for a while' })),
    ...c.late.map(g => ({ ...g, kind: 'Late' })),
  ].sort((a, b) => b.ts - a.ts).slice(0, 6)
  return (
    <Section id="diag-cadence" title="Check cadence" hint={`Compares the gaps between the last ${c.gaps + 1} checks with the schedule.`}
      aside={c.onTimePct != null ? <Badge tone={c.onTimePct >= 95 ? 'ok' : c.onTimePct >= 80 ? 'warn' : 'danger'} dot>{pct(c.onTimePct)} on time</Badge> : undefined}>
      <Stats items={[
        { label: push ? 'Expected push every' : 'Interval', value: spanText(cfg.intervalS * 1000) },
        ...(push ? [{ label: 'Grace period', value: spanText((cfg.graceS ?? 0) * 1000) }] : [
          { label: 'Timeout', value: spanText(cfg.timeoutMs) },
          { label: 'Retries', value: cfg.retries > 0 ? `${cfg.retries} every ${spanText(cfg.retryIntervalS * 1000)}` : 'None' },
        ]),
        { label: 'Typical gap', value: c.medianMs == null ? '—' : spanText(c.medianMs) },
        { label: 'Longest gap', value: c.maxMs == null ? '—' : spanText(c.maxMs) },
      ]} />
      {cfg.paused && <p className="text-sm text-body-2">This monitor is paused, so no new checks are being made and the newest gap will keep growing.</p>}
      {rows.length === 0 ? (
        <p className="text-sm text-body-2">Every gap in this window is within the planned interval.</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <caption className="sr-only">Late checks and stretches without checks</caption>
            <thead><tr className="text-left text-xs text-muted"><th scope="col" className="font-medium pb-2 pr-4">Check at</th><th scope="col" className="font-medium pb-2 pr-4">Gap before it</th><th scope="col" className="font-medium pb-2 pr-4">Planned</th><th scope="col" className="font-medium pb-2">What</th></tr></thead>
            <tbody className="divide-y divide-default">
              {rows.map(r => (
                <tr key={r.ts}>
                  <td className="py-2 pr-4 tnum whitespace-nowrap">{dateServer(r.ts)}</td>
                  <td className="py-2 pr-4 tnum whitespace-nowrap">{spanText(r.gapMs)}</td>
                  <td className="py-2 pr-4 tnum whitespace-nowrap text-muted">{spanText(r.expectedMs)}</td>
                  <td className="py-2"><Badge tone={r.kind === 'Late' ? 'warn' : 'danger'}>{r.kind}</Badge></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <details className="text-xs text-faint">
        <summary className="cursor-pointer font-medium text-accent hover:underline rounded-md text-[13px]">How lateness is judged</summary>
        <p className="mt-2 leading-relaxed">Checks are stamped when they finish, so a slow check shifts the next stamp by up to the timeout, and an interval changed recently can make older gaps look early or late. A gap is late once it passes the planned wait by the timeout (or 25% of the wait, whichever is larger), and a stretch without checks once it passes three planned waits plus the timeout. Pauses, restarts of this app and maintenance windows can all leave one.</p>
      </details>
    </Section>
  )
}
