// Diagnostics: pure calculations behind the monitor Diagnostics tab. No
// React, no fetching and no runtime imports, so the maths can be unit tested
// with `node --experimental-strip-types --test tests/web/diagnostics.test.mjs`.
//
// ─────────────────────────────────────────────────────────────────────────────
// WHAT DATA EXISTS PER CHECK TODAY (read from internal/model/result.go,
// internal/engine/engine.go heartbeat(), internal/store/{heartbeats,rollups,
// runtime,writer}.go, internal/api/{history,latency,diagnostics}.go). Only
// these facts are ever shown; anything not listed here is not recorded.
//
// A BEAT (one row of `heartbeats`; GET /api/monitors/{id}/beats and the
// `beats` of GET /api/monitors/{id}/diagnostics, newest first, at most 200):
//   ts         when the check FINISHED (unix ms). Checks start on a fixed
//              cadence; the stamp adds the check's own duration.
//   status     the monitor's state AFTER folding the check in, not the raw
//              outcome: up | degraded | pending | down | unknown | maintenance.
//              A failed check is `pending` while retries remain, then `down`.
//              `unknown` means this server's own internet guard said it was
//              offline (msg = that reason, errClass cleared): says nothing
//              about the target. `maint` is set for checks run in a window.
//   latencyMs  total duration of the check, -1 when none was measured. Failed
//              checks are -1 except when a response arrived (HTTP status or
//              assertion failures). For every type this is ONE number.
//   code       http: status code. dns: the rcode on lookup failure. else 0.
//   errClass   timeout | dns | refused | tls | http_status | assertion |
//              network | other ("" when the check passed or was `unknown`).
//   msg        the failure/degraded reason: <= 200 runes, secret-free,
//              contains no bodies, queries or credentials. "" when status=up.
//   HTTP ONLY  dnsMs, tcpMs, tlsMs, ttfbMs, waitMs, appMs, edge, conn: the
//              phase timings, who answered, "ip - HTTP/2 - TLS 1.3". All -1/""
//              for every other type.
// NOT on a beat: certificate, ping packet loss, response body, SQL values,
// DNS answers (only the reason text of a FAILED dns check names them).
//
// Per monitor (not per beat), read live:
//   certificate: monitor_runtime keeps the last certificate any check saw
//              (notAfter, issuer CN, subject CN) for http, websocket, tcp(tls),
//              tls and the TLS-capable service types. Exposed by the
//              diagnostics endpoint. The summary SSE carries only certDays.
//   configuration: interval, retry interval, retries, timeout, degraded-after,
//              ssl warn days, and per-type non-secret settings.
// Ping: LossPct exists in memory during the check; it is stored nowhere. Loss
//              only survives as reason text when it made the check fail
//              ("100% packet loss") or degrade ("packet loss 67% > 34%").
//              latencyMs is the average round trip of that check's echoes.
//
// ROLLUPS (rollup_hourly = UTC hours, kept 90 days; rollup_daily = server-zone
// days, kept 730 days): counts of up/degraded/pending/down/unknown/maintenance
// checks, and over every non-maintenance check that HAD a latency (failed
// checks that got a response included): sum, count, min, max and a log-bucket
// histogram (bins grow 1.1x) from which p50/p90/p95/p99 are read back within
// about 5 %. Raw beats are kept for the retention setting; 1h/24h response
// times are exact, 7d and longer are histogram estimates. Rollups hold no
// reasons and no error classes.
//
// OUTAGES (GET /api/monitors/{id}/outages): start, end, the FIRST failing
// reason and error class of each outage, acknowledgement and note.
// ─────────────────────────────────────────────────────────────────────────────

import type { Heartbeat } from './types'

// ── API shape ───────────────────────────────────────────────────────────────

/** Non-secret settings from GET /api/monitors/{id}/diagnostics. */
export interface DiagConfig {
  type: string
  intervalS: number
  retryIntervalS: number
  retries: number
  timeoutMs: number
  degradedMs: number
  sslWarnDays: number
  upsideDown: boolean
  paused: boolean
  method?: string
  acceptedCodes?: string
  maxRedirects?: number | null
  ignoreTls?: boolean
  hasKeyword?: boolean
  keywordMode?: string
  hasJsonQuery?: boolean
  assertionCount?: number
  vendor?: boolean
  tls?: boolean
  security?: string
  grpcService?: string
  maxOffsetMs?: number
  count?: number
  maxLossPct?: number
  record?: string
  resolver?: string
  expectedValues?: string[]
  match?: string
  graceS?: number
  hasSend?: boolean
  hasExpect?: boolean
  probe?: string
  hasQuery?: boolean
  queryOp?: string
  threshold?: number
  thresholdSecs?: number
  dependencyCount?: number
}

export interface DiagCert { notAfter: number; issuer: string; subject: string }

export interface DiagnosticsData {
  /** Server clock when the response was built (unix ms). */
  now: number
  monitor: DiagConfig
  cert: DiagCert | null
  /** Newest first, at most 200. */
  beats: Heartbeat[]
}

const DAY = 86_400_000

// ── Outcomes ────────────────────────────────────────────────────────────────

export type Outcome = 'passed' | 'degraded' | 'retrying' | 'failed' | 'offline' | 'maintenance'

/** What one beat says about the target. */
export function outcomeOf(b: Pick<Heartbeat, 'status' | 'maint'>): Outcome {
  if (b.maint || b.status === 'maintenance') return 'maintenance'
  switch (b.status) {
    case 'up': return 'passed'
    case 'degraded': return 'degraded'
    case 'pending': return 'retrying'
    case 'down': return 'failed'
    default: return 'offline' // unknown / paused: nothing was learned about the target
  }
}

/** A check that failed: still retrying (pending) or confirmed down. */
export function isFailedCheck(b: Pick<Heartbeat, 'status' | 'maint'>): boolean {
  const o = outcomeOf(b)
  return o === 'failed' || o === 'retrying'
}

export const OUTCOME_LABEL: Record<Outcome, string> = {
  passed: 'Passed',
  degraded: 'Degraded',
  retrying: 'Failed, retrying',
  failed: 'Failed',
  offline: 'Not judged',
  maintenance: 'In maintenance',
}

// ── Error classes ───────────────────────────────────────────────────────────

export interface ClassInfo {
  key: string
  label: string
  /** What this class usually means, in plain language. */
  meaning: string
  /** What to check next. */
  next: string[]
}

const CLASS_INFO: Record<string, ClassInfo> = {
  timeout: {
    key: 'timeout',
    label: 'Timeout',
    meaning: 'The target did not answer within the monitor\'s timeout. It is usually overloaded or hung, a firewall is silently dropping packets, or the network path is congested.',
    next: [
      'Is the service up and responding from another machine?',
      'Load, queueing and slow dependencies on the target.',
      'Firewall or security-group rules that drop (rather than reject) traffic from this server.',
      'If the service is legitimately slow, raise the timeout on the monitor.',
    ],
  },
  refused: {
    key: 'refused',
    label: 'Connection refused',
    meaning: 'The host is reachable, but nothing accepted the connection on that port. The service is stopped, listening elsewhere, or a firewall actively rejected the connection.',
    next: [
      'Is the service running and bound to the expected address and port?',
      'The port in the monitor\'s target.',
      'A recent deploy, restart or crash loop.',
      'Firewall rules that reject connections from this server.',
    ],
  },
  dns: {
    key: 'dns',
    label: 'DNS lookup failed',
    meaning: 'The hostname could not be turned into an address, so no connection was attempted.',
    next: [
      'Spelling of the hostname, and that its records still exist and have not expired.',
      'The resolver in use: the one set on the monitor, or this server\'s own.',
      'An outage at the domain\'s DNS provider.',
    ],
  },
  tls: {
    key: 'tls',
    label: 'TLS error',
    meaning: 'The connection was made but the TLS handshake or certificate check failed: an expired, untrusted or mismatched certificate, a missing intermediate, or plain HTTP on a TLS port (or the reverse).',
    next: [
      'Certificate expiry, and that the chain includes its intermediates.',
      'That the certificate covers the hostname in the target.',
      'That the port really speaks TLS, and that this server\'s clock is correct.',
      'Only use "ignore TLS errors" if an untrusted certificate is intentional.',
    ],
  },
  assertion: {
    key: 'assertion',
    label: 'Check did not match',
    meaning: 'The service answered, but what it returned did not match what this monitor expects: a keyword, JSON value, header, response-time limit, protocol reply or DNS answer. In upside-down mode this also means the target was reachable.',
    next: [
      'Fetch the same thing by hand and compare it with the recorded reason.',
      'Whether the content or DNS answers changed on purpose.',
      'The monitor\'s expectations, if the change is intended.',
    ],
  },
  network: {
    key: 'network',
    label: 'Network error',
    meaning: 'The connection failed at a low level: no route to the host, the network is unreachable, or the connection was reset. This is a path problem rather than a clean "no".',
    next: [
      'Routing and firewalls between this server and the target.',
      'Whether the target host or its network is down.',
      'This server\'s own connectivity.',
    ],
  },
  http_status: {
    key: 'http_status',
    label: 'Unexpected HTTP status',
    meaning: 'The server answered, but with a status code outside the codes this monitor accepts. 5xx usually points at the application; 4xx at routing, authentication or a moved URL.',
    next: [
      'Application logs for the failing requests (5xx).',
      'Authentication, redirects and the URL path (4xx).',
      'The monitor\'s accepted status codes, if the response is expected.',
    ],
  },
  other: {
    key: 'other',
    label: 'Other error',
    meaning: 'The check failed in a way that does not fit the other categories. The reason text below is the best clue.',
    next: ['Read the reason text exactly as recorded.', 'Run the same check by hand from this server.'],
  },
  unclassified: {
    key: 'unclassified',
    label: 'No class recorded',
    meaning: 'The check failed but the result carries no error class (for example a push monitor that did not hear from its service).',
    next: ['Read the reason text exactly as recorded.'],
  },
}

export function classKey(errClass: string | undefined | null): string {
  const c = (errClass ?? '').trim()
  if (!c) return 'unclassified'
  return CLASS_INFO[c] ? c : 'other'
}

/** Where the generic wording would mislead for a type, say what the class means for it. */
const TYPE_CLASS: Record<string, Record<string, Pick<ClassInfo, 'meaning' | 'next'>>> = {
  push: {
    timeout: {
      meaning: 'Opskest never connects to a push monitor. It waits for your service to call this monitor\'s push URL, and none arrived within the interval plus the grace period.',
      next: [
        'Did the job, cron entry or service that should push actually run?',
        'That it calls the right push URL (the token is part of the address) and gets a success reply.',
        'The network path from that service to this Opskest server.',
        'The interval and grace period, if the job legitimately runs less often.',
      ],
    },
  },
  ping: {
    timeout: {
      meaning: 'Not one echo reply came back. The host is down or unreachable, or something on the way blocks ICMP.',
      next: [
        'Is the host up, and does it answer ping from another machine?',
        'Firewall or security-group rules that block ICMP echo (many networks do by default).',
        'Whether this server may send ICMP at all (see the ping note in the Opskest README).',
      ],
    },
  },
  dns: {
    assertion: {
      meaning: 'The lookup worked, but the answers differ from the expected set. Load balancers and CDNs rotate addresses, so this is often a change nobody announced.',
      next: [
        'Whether the records changed on purpose, and what the recorded reason says is new or missing.',
        'If the new answers are right, update the expected values (Rotate to current answers, below).',
        'If they are wrong, check the zone with your DNS provider.',
      ],
    },
  },
}

export function explainClass(errClass: string | undefined | null, type?: string): ClassInfo {
  const key = classKey(errClass)
  const base = CLASS_INFO[key]
  const o = type ? TYPE_CLASS[type]?.[key] : undefined
  return o ? { ...base, ...o } : base
}

// ── Last check ──────────────────────────────────────────────────────────────

/** The newest beat (beats are newest first). */
export function lastBeat(beats: Heartbeat[]): Heartbeat | null {
  return beats.length ? beats[0] : null
}

// ── Failure breakdown ───────────────────────────────────────────────────────

export interface FailureGroup {
  key: string
  label: string
  count: number
  /** Share of all failed checks in the window, 0-100. */
  share: number
  latestTs: number
  /** Reason of the most recent failure of this class, exactly as stored. */
  latestReason: string
}

export interface FailureBreakdown {
  /** Beats considered (maintenance and not-judged excluded). */
  judged: number
  failed: number
  degraded: number
  excludedMaintenance: number
  excludedOffline: number
  groups: FailureGroup[]
  /** Oldest and newest beat time in the window (0 when empty). */
  from: number
  to: number
}

/** Failing checks in `beats` grouped by error class, most frequent first. */
export function failureBreakdown(beats: Heartbeat[]): FailureBreakdown {
  const out: FailureBreakdown = { judged: 0, failed: 0, degraded: 0, excludedMaintenance: 0, excludedOffline: 0, groups: [], from: 0, to: 0 }
  const byKey = new Map<string, FailureGroup>()
  for (const b of beats) {
    if (b.ts > 0) {
      if (out.from === 0 || b.ts < out.from) out.from = b.ts
      if (b.ts > out.to) out.to = b.ts
    }
    const o = outcomeOf(b)
    if (o === 'maintenance') { out.excludedMaintenance++; continue }
    if (o === 'offline') { out.excludedOffline++; continue }
    out.judged++
    if (o === 'degraded') { out.degraded++; continue }
    if (o !== 'failed' && o !== 'retrying') continue
    out.failed++
    const key = classKey(b.errClass)
    let g = byKey.get(key)
    if (!g) {
      g = { key, label: CLASS_INFO[key].label, count: 0, share: 0, latestTs: 0, latestReason: '' }
      byKey.set(key, g)
    }
    g.count++
    if (b.ts >= g.latestTs) { g.latestTs = b.ts; g.latestReason = b.msg }
  }
  for (const g of byKey.values()) g.share = out.failed ? (g.count / out.failed) * 100 : 0
  out.groups = [...byKey.values()].sort((a, b) => b.count - a.count || b.latestTs - a.latestTs)
  return out
}

// ── Latency statistics ──────────────────────────────────────────────────────

export interface LatencySummary {
  count: number
  min: number
  avg: number
  max: number
  p50: number
  p95: number
  /** Mean absolute difference between two consecutive measured checks (ms). */
  jitter: number | null
}

/** Nearest-rank quantile of an unsorted list (same rule as the server). */
export function quantile(values: number[], q: number): number {
  if (!values.length) return 0
  const s = [...values].sort((a, b) => a - b)
  const idx = Math.min(s.length - 1, Math.max(0, Math.ceil(q * s.length) - 1))
  return s[idx]
}

/**
 * Jitter of a chronological series: the mean absolute difference between
 * consecutive measured checks. `null` entries (a failed or unmeasured check)
 * break the chain, so two measurements are never compared across a gap.
 * Needs at least two adjacent measurements.
 */
export function jitter(series: (number | null)[]): number | null {
  let sum = 0
  let n = 0
  let prev: number | null = null
  for (const v of series) {
    if (v == null || v < 0) { prev = null; continue }
    if (prev != null) { sum += Math.abs(v - prev); n++ }
    prev = v
  }
  return n ? sum / n : null
}

/** Latency of the checks that measured one (maintenance excluded). */
export function latencySummary(beats: Heartbeat[]): LatencySummary | null {
  const chrono = [...beats].reverse().filter(b => !b.maint && b.status !== 'maintenance')
  const series = chrono.map(b => (b.latencyMs >= 0 ? b.latencyMs : null))
  const values = series.filter((v): v is number => v != null)
  if (!values.length) return null
  const sum = values.reduce((a, v) => a + v, 0)
  return {
    count: values.length,
    min: Math.min(...values),
    avg: sum / values.length,
    max: Math.max(...values),
    p50: quantile(values, 0.5),
    p95: quantile(values, 0.95),
    jitter: jitter(series),
  }
}

// ── Ping loss ───────────────────────────────────────────────────────────────

export interface PingLoss {
  /** Judged checks in the window. */
  checks: number
  /** Checks whose reason reports 100 % loss (every echo lost). */
  totalLoss: number
  /** Checks that passed but degraded on loss above the limit. */
  aboveLimit: number
  /** Highest loss percentage any reason reported, or null. */
  worst: number | null
  /** Loss in the newest check whose reason reported it, or null. */
  latest: number | null
}

/**
 * Loss is not stored per check. It is recoverable only from the reason of a
 * check that lost everything ("100% packet loss") or degraded on it
 * ("packet loss 67% > 34%"). Anything lower than the limit was stored as a
 * plain pass, so this is a floor on what happened, never a total.
 */
export function pingLoss(beats: Heartbeat[]): PingLoss {
  const out: PingLoss = { checks: 0, totalLoss: 0, aboveLimit: 0, worst: null, latest: null }
  for (const b of beats) {
    const o = outcomeOf(b)
    if (o === 'maintenance' || o === 'offline') continue
    out.checks++
    let pctLoss: number | null = null
    const all = /^\s*100%\s+packet loss/i.exec(b.msg)
    const partial = /packet loss\s+(\d{1,3})%/i.exec(b.msg)
    if (all) { pctLoss = 100; out.totalLoss++ } else if (partial) { pctLoss = Math.min(100, Number(partial[1])); out.aboveLimit++ }
    if (pctLoss != null) {
      if (out.worst == null || pctLoss > out.worst) out.worst = pctLoss
      if (out.latest == null) out.latest = pctLoss // beats are newest first
    }
  }
  return out
}

// ── Cadence ─────────────────────────────────────────────────────────────────

export interface CadenceGap {
  /** Time of the later check of the pair. */
  ts: number
  gapMs: number
  expectedMs: number
}

export interface Cadence {
  /** Number of gaps measured (checks - 1, maintenance included). */
  gaps: number
  medianMs: number | null
  maxMs: number | null
  expectedMs: number
  /** Gaps well past what the scheduler plans: a late check. */
  late: CadenceGap[]
  /** Gaps so long that checks were not running (paused, app stopped...). */
  missing: CadenceGap[]
  /** Share of gaps on time, 0-100, or null with no gaps. */
  onTimePct: number | null
}

/**
 * Compare the real gaps between checks with the configured cadence.
 *
 * Beats are stamped when a check finishes, while the scheduler starts checks
 * on a fixed grid, so a gap also carries the difference between two checks'
 * own durations: up to the timeout. A gap is therefore "late" only when it
 * exceeds the planned wait plus max(timeout, 25 % of it), and "missing" when
 * it exceeds three planned waits plus the timeout. The planned wait after a
 * `pending` (retrying) check is the retry interval, otherwise the interval.
 * Push monitors are not scheduled by Opskest: their tolerance is the grace
 * period and a missing push is a gap beyond three intervals.
 */
export function cadence(beats: Heartbeat[], cfg: Pick<DiagConfig, 'type' | 'intervalS' | 'retryIntervalS' | 'timeoutMs' | 'graceS'>): Cadence {
  const chrono = [...beats].sort((a, b) => a.ts - b.ts)
  const interval = cfg.intervalS * 1000
  const retry = (cfg.retryIntervalS || cfg.intervalS) * 1000
  const push = cfg.type === 'push'
  const slack = push ? (cfg.graceS ?? 0) * 1000 : cfg.timeoutMs
  const out: Cadence = { gaps: 0, medianMs: null, maxMs: null, expectedMs: interval, late: [], missing: [], onTimePct: null }
  const gaps: number[] = []
  let onTime = 0
  for (let i = 1; i < chrono.length; i++) {
    const gapMs = chrono[i].ts - chrono[i - 1].ts
    if (gapMs <= 0) continue
    const expectedMs = !push && chrono[i - 1].status === 'pending' ? retry : interval
    gaps.push(gapMs)
    if (gapMs > expectedMs * 3 + slack) out.missing.push({ ts: chrono[i].ts, gapMs, expectedMs })
    else if (gapMs > expectedMs + Math.max(slack, expectedMs * 0.25)) out.late.push({ ts: chrono[i].ts, gapMs, expectedMs })
    else onTime++
  }
  out.gaps = gaps.length
  if (gaps.length) {
    const s = [...gaps].sort((a, b) => a - b)
    const mid = s.length >> 1
    out.medianMs = s.length % 2 ? s[mid] : (s[mid - 1] + s[mid]) / 2
    out.maxMs = s[s.length - 1]
    out.onTimePct = (onTime / gaps.length) * 100
  }
  return out
}

// ── Certificate ─────────────────────────────────────────────────────────────

export type CertSeverity = 'ok' | 'warning' | 'critical' | 'expired'

/** Whole days until `notAfter` (floored, negative once expired); same rule as the server. */
export function certDaysLeft(notAfter: number, now: number): number {
  return Math.floor((notAfter - now) / DAY)
}

/**
 * Colour band for a certificate. `warnDays` is the monitor's sslWarnDays
 * (0 = warnings off, so only an expired certificate stands out). Critical is
 * the last 7 days, or the whole warning window when it is shorter.
 */
export function certSeverity(daysLeft: number, warnDays: number): CertSeverity {
  if (daysLeft < 0) return 'expired'
  if (warnDays <= 0) return 'ok'
  if (daysLeft <= Math.min(7, warnDays)) return 'critical'
  if (daysLeft <= warnDays) return 'warning'
  return 'ok'
}

// ── Monitor description ─────────────────────────────────────────────────────

export interface TypeInfo {
  label: string
  /** What the single reported duration covers. */
  timeCovers: string
  /** Whether the phase timings (DNS/TCP/TLS/first byte) exist for the type. */
  phases: boolean
  /** Whether the type can observe a TLS certificate. */
  cert: boolean
}

const TYPE_INFO: Record<string, TypeInfo> = {
  http: { label: 'HTTP(S)', timeCovers: 'the whole request: connect, TLS, first byte and the response body', phases: true, cert: true },
  tcp: { label: 'TCP port', timeCovers: 'opening the TCP connection (plus the TLS handshake when TLS is on)', phases: false, cert: true },
  ping: { label: 'Ping (ICMP)', timeCovers: 'the average round trip of that check\'s echo requests', phases: false, cert: false },
  dns: { label: 'DNS', timeCovers: 'the lookup, including waiting on the resolver', phases: false, cert: false },
  push: { label: 'Push (heartbeat)', timeCovers: 'the duration your service reported when it pushed, if it sent one', phases: false, cert: false },
  websocket: { label: 'WebSocket', timeCovers: 'the opening handshake, plus the reply when a message is expected', phases: false, cert: true },
  tls: { label: 'TLS certificate', timeCovers: 'connecting and completing the TLS handshake', phases: false, cert: true },
  smtp: { label: 'SMTP', timeCovers: 'connecting, the greeting, EHLO and any STARTTLS upgrade', phases: false, cert: true },
  ssh: { label: 'SSH', timeCovers: 'connecting and receiving the server\'s identification string', phases: false, cert: false },
  redis: { label: 'Redis', timeCovers: 'connecting, authenticating if configured, and the PING reply', phases: false, cert: true },
  postgres: { label: 'PostgreSQL port', timeCovers: 'connecting and the server\'s reply to the SSL request (no login)', phases: false, cert: false },
  mysql: { label: 'MySQL port', timeCovers: 'connecting and receiving the server\'s handshake packet (no login)', phases: false, cert: false },
  mongodb: { label: 'MongoDB', timeCovers: 'connecting and the reply to isMaster (no login)', phases: false, cert: true },
  mqtt: { label: 'MQTT', timeCovers: 'connecting and the broker\'s CONNACK', phases: false, cert: true },
  ntp: { label: 'NTP', timeCovers: 'one request and reply to the time server', phases: false, cert: false },
  udp: { label: 'UDP', timeCovers: 'sending the datagram and receiving the reply', phases: false, cert: false },
  amqp: { label: 'AMQP', timeCovers: 'connecting and the broker\'s reply to the protocol header', phases: false, cert: true },
  memcached: { label: 'Memcached', timeCovers: 'connecting and the reply to a version request', phases: false, cert: false },
  banner: { label: 'Banner', timeCovers: 'connecting and reading the first line the server sends', phases: false, cert: true },
  grpc: { label: 'gRPC health', timeCovers: 'the whole health-check call', phases: false, cert: true },
  postgres_sql: { label: 'PostgreSQL query', timeCovers: 'logging in and running the probe in a read-only transaction', phases: false, cert: false },
  mysql_sql: { label: 'MySQL query', timeCovers: 'logging in and running the probe in a read-only transaction', phases: false, cert: false },
}

export function typeInfo(type: string): TypeInfo {
  return TYPE_INFO[type] ?? { label: type.toUpperCase(), timeCovers: 'the whole check', phases: false, cert: false }
}

export type TimeReporting = 'phases' | 'single' | 'none'

/**
 * Whether the recorded checks carry a phase breakdown. HTTP phases exist only
 * when a request got far enough to record them, so this looks at the data.
 */
export function timeReporting(beats: Heartbeat[]): TimeReporting {
  if (beats.some(b => b.dnsMs >= 0 || b.tcpMs >= 0 || b.ttfbMs >= 0)) return 'phases'
  return beats.some(b => b.latencyMs >= 0) ? 'single' : 'none'
}

export interface ProbeInfo { label: string; measures: string; downWhen: string }

/** What a database monitor's probe looks at (internal/check/sqlmon.go). */
export function sqlProbeInfo(cfg: Pick<DiagConfig, 'probe' | 'queryOp' | 'threshold' | 'thresholdSecs'>): ProbeInfo | null {
  const t = cfg.threshold ?? 0
  switch (cfg.probe) {
    case 'query':
      return {
        label: 'Custom query',
        measures: 'Runs the saved read-only statement and, when a comparison is set, tests the first value of the first row.',
        downWhen: cfg.queryOp ? `The first value fails the comparison (${COMPARE_WORD[cfg.queryOp] ?? cfg.queryOp}) or the statement errors.` : 'The statement fails to run.',
      }
    case 'deadlocks':
      return { label: 'Deadlocks', measures: 'New deadlocks since the previous check. The first check after a start only records a baseline.', downWhen: `More than ${t} new deadlock${t === 1 ? '' : 's'} between two checks.` }
    case 'long_running':
      return { label: 'Long-running queries', measures: `Queries that have been running longer than ${cfg.thresholdSecs ?? 0} s.`, downWhen: `More than ${t} such quer${t === 1 ? 'y' : 'ies'}.` }
    case 'blocked':
      return { label: 'Blocked sessions', measures: 'Sessions currently waiting on a lock.', downWhen: `More than ${t} blocked session${t === 1 ? '' : 's'}.` }
    case 'connections':
      return { label: 'Connection usage', measures: 'Percent of the server\'s connection limit in use.', downWhen: `Usage above ${t}%.` }
    default:
      return null
  }
}

const COMPARE_WORD: Record<string, string> = {
  eq: 'equals the expected value', ne: 'differs from the expected value', gt: 'greater than', gte: 'greater than or equal to',
  lt: 'less than', lte: 'less than or equal to', contains: 'contains the expected text',
}

const MATCH_WORD: Record<string, string> = {
  equals: 'any answer equals an expected value',
  contains: 'any answer contains an expected value',
  all: 'every expected value is among the answers',
  exact: 'the answers are exactly the expected values',
}

export function dnsMatchWord(match: string | undefined): string {
  return MATCH_WORD[match || 'equals'] ?? MATCH_WORD.equals
}

export function everyText(seconds: number): string {
  if (seconds % 3600 === 0 && seconds >= 3600) return `${seconds / 3600} h`
  if (seconds % 60 === 0 && seconds >= 60) return `${seconds / 60} min`
  return `${seconds} s`
}

/**
 * A short plain-language description of what the monitor checks, from its
 * type and non-secret settings. The target is included because the tab is for
 * signed-in users. Never mentions a credential, query, keyword or payload.
 */
export function describeMonitor(cfg: DiagConfig, target: string): string[] {
  const out: string[] = []
  const every = `every ${everyText(cfg.intervalS)}`
  const lines = (...l: string[]) => out.push(...l.filter(Boolean))
  switch (cfg.type) {
    case 'http': {
      if (cfg.vendor) {
        lines(`Reads the third-party status feed at ${target} ${every}. It reports incidents the vendor publishes, not response speed.`)
        break
      }
      lines(`Sends a ${cfg.method || 'GET'} request to ${target} ${every} and expects an HTTP status in ${cfg.acceptedCodes || '200-299'}.`)
      const rules: string[] = []
      if (cfg.hasKeyword) rules.push(cfg.keywordMode === 'not_contains' ? 'the body must not contain a configured keyword' : cfg.keywordMode === 'regex' ? 'the body must match a configured pattern' : 'the body must contain a configured keyword')
      if (cfg.hasJsonQuery) rules.push('a JSON value in the response must match')
      if (cfg.assertionCount) rules.push(`${cfg.assertionCount} extra assertion${cfg.assertionCount === 1 ? '' : 's'} must pass`)
      if (rules.length) lines(`Also: ${rules.join('; ')}.`)
      if (cfg.maxRedirects === 0) lines('Redirects are not followed.')
      else if (cfg.maxRedirects != null) lines(`Follows up to ${cfg.maxRedirects} redirects.`)
      if (cfg.ignoreTls) lines('TLS certificate errors are ignored for this monitor.')
      break
    }
    case 'tcp':
      lines(`Opens a TCP connection to ${target} ${every}${cfg.tls ? ', completes a TLS handshake and records the certificate' : ''}. It does not send any data.`)
      break
    case 'ping':
      lines(`Sends ${cfg.count || 3} ICMP echo requests to ${target} ${every}. Down when none is answered; degraded when more than ${cfg.maxLossPct || 34}% are lost.`)
      break
    case 'dns': {
      const via = cfg.resolver ? `through the resolver ${cfg.resolver}` : 'through this server\'s own resolver'
      lines(`Looks up ${cfg.record || 'A'} records for ${target} ${via} ${every}.`)
      if (cfg.expectedValues && cfg.expectedValues.length) lines(`Passes when ${dnsMatchWord(cfg.match)}.`)
      else lines('Passes when at least one record is returned.')
      break
    }
    case 'push':
      lines(`Waits for your service to call this monitor's push URL. Down when nothing arrives within ${everyText(cfg.intervalS)} plus a ${everyText(cfg.graceS ?? 0)} grace period. Opskest does not connect to anything.`)
      break
    case 'websocket':
      lines(`Opens a WebSocket connection to ${target} ${every}${cfg.hasSend ? ', sends the configured message' : ''}${cfg.hasExpect ? ' and waits for a reply containing the expected text' : ''}.`)
      break
    case 'tls':
      lines(`Completes a TLS handshake with ${target} ${every}, verifies the certificate and tracks its expiry.`)
      break
    case 'smtp':
      lines(`Connects to the mail server at ${target} ${every}, reads its greeting and says EHLO${cfg.security === 'starttls' ? ', then upgrades with STARTTLS' : cfg.security === 'tls' ? ' over implicit TLS' : ''}. No mail is sent.`)
      break
    case 'ssh':
      lines(`Connects to ${target} ${every} and reads the server's SSH identification string. It does not log in.`)
      break
    case 'redis':
      lines(`Connects to Redis at ${target} ${every} and sends PING${cfg.tls ? ' over TLS' : ''}, authenticating first when a password is set.`)
      break
    case 'postgres':
      lines(`Connects to ${target} ${every} and sends the PostgreSQL SSL request. A live server answers; no login is attempted.`)
      break
    case 'mysql':
      lines(`Connects to ${target} ${every} and reads the MySQL handshake packet the server sends first. No login is attempted.`)
      break
    case 'mongodb':
      lines(`Connects to MongoDB at ${target} ${every} and runs isMaster, which needs no login. Passes on ok: 1.`)
      break
    case 'mqtt':
      lines(`Connects to the MQTT broker at ${target} ${every} and requires an accepted CONNACK.`)
      break
    case 'ntp':
      lines(`Asks the NTP server ${target} for the time ${every}${cfg.maxOffsetMs ? `. Fails when this server's clock is more than ${cfg.maxOffsetMs} ms off` : ''}.`)
      break
    case 'udp':
      lines(`Sends a datagram to ${target} ${every} and requires a reply containing the expected text. UDP has no handshake, so a reply is the only proof of life.`)
      break
    case 'amqp':
      lines(`Sends the AMQP protocol header to ${target} ${every} and requires the broker's Connection.Start reply. No login is attempted.`)
      break
    case 'memcached':
      lines(`Connects to ${target} ${every} and asks for the Memcached version.`)
      break
    case 'banner':
      lines(`Connects to ${target} ${every}, reads the first line the server sends and requires it to contain the expected text.`)
      break
    case 'grpc':
      lines(`Calls grpc.health.v1 Health/Check on ${target} ${every}${cfg.grpcService ? ` for service ${cfg.grpcService}` : ' for the server overall'} and requires SERVING.`)
      break
    case 'postgres_sql':
    case 'mysql_sql': {
      const p = sqlProbeInfo(cfg)
      lines(`Logs in to ${target} ${every} and runs ${p ? `the "${p.label.toLowerCase()}" probe` : 'a probe'} in a read-only transaction.`)
      break
    }
    default:
      lines(`Checks ${target} ${every}.`)
  }
  if (cfg.type !== 'push') lines(`A check that takes longer than ${cfg.timeoutMs >= 1000 ? `${cfg.timeoutMs / 1000} s` : `${cfg.timeoutMs} ms`} counts as a timeout.`)
  if (cfg.retries > 0 && cfg.type !== 'push') lines(`A failure is retried ${cfg.retries} time${cfg.retries === 1 ? '' : 's'}, ${everyText(cfg.retryIntervalS)} apart, before the monitor goes down.`)
  else if (cfg.type !== 'push') lines('The monitor goes down on the first failed check (no retries).')
  if (cfg.upsideDown) lines('Upside-down mode: reaching the target counts as a failure.')
  return out
}

// ── Display helpers ─────────────────────────────────────────────────────────

/** "1.4 s" / "230 ms" for a gap or duration in ms. */
export function spanText(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '—'
  if (ms < 1000) return `${Math.round(ms)} ms`
  if (ms < 10_000) return `${Math.round(ms / 100) / 10} s`
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s} s`
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const r = s % 60
  if (h === 0) return r ? `${m} min ${r} s` : `${m} min`
  return m ? `${h} h ${m} min` : `${h} h`
}
