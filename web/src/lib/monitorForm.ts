// The monitor form's model (spec §6): the draft it edits, the conversions
// to and from the API (MonitorOut → Draft → MonitorIn) and client-side
// validation mirroring model.Validate's messages. Pure — no React.
//
// Every MonitorIn field is sent explicitly, prefilled with the Global
// Constraints defaults, so an explicit 0 survives. Secrets are write-only:
// null keeps the stored value, a string replaces it, "" clears it.

import type { Assertion, DnsMatch, FieldErrors, HeaderIn, LatencyAlertCfg, MonitorHeaderOut, MonitorIn, MonitorOut, MonitorType, Settings, VendorProvider } from './types'

export type TypeGroup = 'Web' | 'Network' | 'Databases' | 'Services' | 'Heartbeat' | 'Logic'

export const TYPE_GROUPS: TypeGroup[] = ['Web', 'Network', 'Databases', 'Services', 'Heartbeat', 'Logic']

export const MONITOR_TYPES: { type: MonitorType; label: string; desc: string; group: TypeGroup }[] = [
  { type: 'http', label: 'HTTP(S)', desc: 'Website or API — status code, keyword, JSON', group: 'Web' },
  { type: 'websocket', label: 'WebSocket', desc: 'Connects, optionally sends and expects', group: 'Web' },
  { type: 'tls', label: 'SSL certificate', desc: 'Handshake and expiry of any TLS endpoint', group: 'Web' },
  { type: 'tcp', label: 'TCP port', desc: 'A port accepts connections, optionally TLS', group: 'Network' },
  { type: 'ping', label: 'Ping', desc: 'ICMP echo — packet loss and round trip', group: 'Network' },
  { type: 'dns', label: 'DNS', desc: 'A record resolves, optionally to a value', group: 'Network' },
  { type: 'udp', label: 'UDP', desc: 'Send a datagram, expect a reply', group: 'Network' },
  { type: 'ntp', label: 'NTP', desc: 'Time server answers, optionally within an offset', group: 'Network' },
  { type: 'postgres_sql', label: 'PostgreSQL queries', desc: 'Custom query, slow queries, deadlocks, lock waits', group: 'Databases' },
  { type: 'mysql_sql', label: 'MySQL / MariaDB queries', desc: 'Custom query, slow queries, deadlocks, lock waits', group: 'Databases' },
  { type: 'ssh', label: 'SSH', desc: 'Server presents an SSH banner', group: 'Services' },
  { type: 'smtp', label: 'SMTP', desc: 'Mail server greets, optionally STARTTLS', group: 'Services' },
  { type: 'redis', label: 'Redis', desc: 'PING, with optional password', group: 'Services' },
  { type: 'postgres', label: 'PostgreSQL', desc: 'Server answers the protocol handshake', group: 'Services' },
  { type: 'mysql', label: 'MySQL / MariaDB', desc: 'Server sends its handshake', group: 'Services' },
  { type: 'mongodb', label: 'MongoDB', desc: 'Server answers isMaster', group: 'Services' },
  { type: 'mqtt', label: 'MQTT', desc: 'Broker accepts a connection', group: 'Services' },
  { type: 'amqp', label: 'RabbitMQ / AMQP', desc: 'Broker answers the AMQP 0-9-1 handshake', group: 'Services' },
  { type: 'memcached', label: 'Memcached', desc: 'Server answers the version command', group: 'Services' },
  { type: 'grpc', label: 'gRPC health', desc: 'grpc.health.v1 reports SERVING', group: 'Services' },
  { type: 'banner', label: 'Banner (FTP, IMAP, POP3…)', desc: 'First line from the server contains text', group: 'Services' },
  { type: 'push', label: 'Push', desc: 'Your job calls us; alert when it stops', group: 'Heartbeat' },
  { type: 'manual', label: 'Manual status', desc: 'You set up, degraded or down by hand or API', group: 'Heartbeat' },
  { type: 'group', label: 'Group', desc: 'Up while its monitors are: all, any or at least N', group: 'Logic' },
]

/** Types with no target: push gets a URL, group follows its children, manual is set by hand. */
export const NO_TARGET_TYPES: MonitorType[] = ['push', 'group', 'manual']


/**
 * Mirrors check.IsStatusPageTarget: an HTTP target that looks like a
 * vendor's status page or incident feed is read for incidents (degraded or
 * down only when the vendor reports one) instead of being judged by its
 * status code and latency.
 */
export function isStatusPageTarget(target: string): boolean {
  let u: URL
  try { u = new URL(target.trim()) } catch { return false }
  const host = u.host.toLowerCase()
  const path = u.pathname.toLowerCase()
  return ['metastatus.com', 'githubstatus.com', 'cloudflarestatus.com', 'statuspage.io', 'status.firebase.google.com',
    'status.cloud.google.com', 'status.slack.com'].some(h => host.includes(h))
    || (host.includes('google.com') && path.includes('appsstatus'))
    || ((host.includes('openai.com') || host.includes('stripe.com')) && host.includes('status'))
    || host.startsWith('status.')
    || path.includes('status') || path.includes('rss') || path.includes('feed') || path.endsWith('.xml')
    || u.search.slice(1).includes('rss')
}

const HOST_PORT = { label: 'Host and port' }

export const TARGET: Record<Exclude<MonitorType, 'push' | 'group' | 'manual'>, { label: string; placeholder: string }> = {
  http: { label: 'URL', placeholder: 'https://api.example.com/health' },
  tcp: { label: 'Host and port', placeholder: 'db.internal:5432' },
  tls: { label: 'Host (and port)', placeholder: 'example.com' },
  smtp: { ...HOST_PORT, placeholder: 'mail.example.com:587' },
  ssh: { ...HOST_PORT, placeholder: 'server.example.com:22' },
  redis: { ...HOST_PORT, placeholder: 'cache.internal:6379' },
  postgres: { ...HOST_PORT, placeholder: 'db.internal:5432' },
  mysql: { ...HOST_PORT, placeholder: 'db.internal:3306' },
  mongodb: { ...HOST_PORT, placeholder: 'db.internal:27017' },
  mqtt: { ...HOST_PORT, placeholder: 'broker.internal:1883' },
  ntp: { label: 'Host (and port)', placeholder: 'pool.ntp.org' },
  udp: { ...HOST_PORT, placeholder: 'game.example.com:27015' },
  amqp: { ...HOST_PORT, placeholder: 'rabbit.internal:5672' },
  memcached: { ...HOST_PORT, placeholder: 'cache.internal:11211' },
  grpc: { ...HOST_PORT, placeholder: 'api.internal:50051' },
  banner: { ...HOST_PORT, placeholder: 'ftp.example.com:21' },
  postgres_sql: { ...HOST_PORT, placeholder: 'db.internal:5432' },
  mysql_sql: { ...HOST_PORT, placeholder: 'db.internal:3306' },
  ping: { label: 'Host or IP', placeholder: '10.0.0.12' },
  dns: { label: 'Hostname', placeholder: 'example.com' },
  websocket: { label: 'WebSocket URL', placeholder: 'wss://live.example.com/socket' },
  vendor: { label: 'Status page or feed URL', placeholder: 'https://www.githubstatus.com' },
}

export const VENDOR_PROVIDERS: { value: VendorProvider; label: string; hint: string }[] = [
  { value: 'statuspage', label: 'Atlassian Statuspage', hint: 'githubstatus.com, status.openai.com, status.atlassian.com, …' },
  { value: 'google', label: 'Google Cloud / Firebase', hint: 'status.cloud.google.com, status.firebase.google.com' },
  { value: 'rss', label: 'RSS / Atom feed', hint: 'Any status page that publishes an incident feed' },
]

/** Best guess at a vendor feed's provider from its URL. */
export function detectVendorProvider(url: string): VendorProvider {
  const u = url.trim().toLowerCase()
  if (/status\.(cloud|firebase)\.google\.com|google\.com\/.*status|googleapis/.test(u)) return 'google'
  if (/\.(rss|atom|xml)(\?|$)|\/(feed|rss|atom|history\.rss)(\/|\?|$)/.test(u)) return 'rss'
  return 'statuspage'
}

export const CERT_TYPES: MonitorType[] = ['http', 'tcp', 'websocket', 'tls', 'smtp', 'redis', 'mongodb', 'mqtt', 'amqp', 'grpc', 'banner']

/** Types whose target is host:port (the port is required). */
export const HOST_PORT_TYPES: MonitorType[] = ['tcp', 'smtp', 'ssh', 'redis', 'postgres', 'mysql', 'mongodb', 'mqtt', 'udp', 'amqp', 'memcached', 'grpc', 'banner', 'postgres_sql', 'mysql_sql']

/** Types that can carry a password in the monitor's basicPass secret. */
export const PASSWORD_TYPES: MonitorType[] = ['redis', 'mqtt', 'postgres_sql', 'mysql_sql']

/** Types whose timing and degraded-latency settings mean something. */
export const LATENCY_TYPES: MonitorType[] = ['http', 'tcp', 'ping', 'dns', 'websocket', 'tls', 'smtp', 'ssh', 'redis', 'postgres', 'mysql', 'mongodb', 'mqtt', 'ntp', 'udp', 'amqp', 'memcached', 'grpc', 'banner', 'postgres_sql', 'mysql_sql']

// ── Secrets ─────────────────────────────────────────────────────────────────

/** The write-only secrets a monitor can carry. */
export type SecretKey = 'basicPass' | 'bearerToken' | 'proxyPass' | 'clientCert' | 'clientKey' | 'oauthSecret'

/** keep → null, replace → the typed string, clear → "". */
export interface SecretDraft {
  isSet: boolean
  mode: 'keep' | 'replace' | 'clear'
  value: string
}

export function emptySecret(isSet = false): SecretDraft {
  return { isSet, mode: isSet ? 'keep' : 'replace', value: '' }
}

export function secretToIn(s: SecretDraft): string | null {
  if (!s.isSet) return s.value === '' ? null : s.value
  if (s.mode === 'keep') return null
  if (s.mode === 'clear') return ''
  return s.value
}

// ── Headers ─────────────────────────────────────────────────────────────────

export interface HeaderRow {
  key: string
  name: string
  value: string
  secret: boolean
  /** A secret already stored on the server (its value is unknown here). */
  stored: boolean
  /** "Replace" was chosen on a stored secret: `value` is the new one. */
  replacing: boolean
  /** Stored secrets are keyed by name, so a kept one keeps its name. */
  originalName: string
}

export const MAX_HEADERS = 30
const HEADER_NAME_RE = /^[A-Za-z0-9-]{1,100}$/
const MAX_HEADER_VALUE_BYTES = 8 * 1024

let seq = 0
const rowKey = () => `h${++seq}`

export function newHeaderRow(): HeaderRow {
  return { key: rowKey(), name: '', value: '', secret: false, stored: false, replacing: false, originalName: '' }
}

export function headerRowsFromOut(headers: MonitorHeaderOut[] | null | undefined): HeaderRow[] {
  return (headers ?? []).map(h => ({
    key: rowKey(),
    name: h.name,
    value: h.secret ? '' : (h.value ?? ''),
    secret: h.secret,
    stored: h.secret && Boolean(h.isSet),
    replacing: false,
    originalName: h.name,
  }))
}

/** A row with neither name nor value is an unfinished "Add header". */
function isBlank(r: HeaderRow): boolean {
  return !r.stored && r.name.trim() === '' && r.value === ''
}

export function headerRowsToIn(rows: HeaderRow[]): HeaderIn[] {
  return rows.filter(r => !isBlank(r)).map(r => {
    if (r.stored && !r.replacing) return { name: r.originalName, value: null, secret: true }
    return { name: r.name.trim(), value: r.value, secret: r.secret }
  })
}

/** Mirrors model.validateHTTP's header rules, keyed `headers.<i>.name|value`. */
export function validateHeaderRows(rows: HeaderRow[]): FieldErrors {
  const fe: FieldErrors = {}
  if (rows.filter(r => !isBlank(r)).length > MAX_HEADERS) fe.headers = `at most ${MAX_HEADERS} headers`
  rows.forEach((r, i) => {
    if (isBlank(r)) return
    const name = r.stored && !r.replacing ? r.originalName : r.name.trim()
    if (!HEADER_NAME_RE.test(name)) fe[`headers.${i}.name`] = `invalid header name: ${name || '(empty)'}`
    if (r.stored && r.replacing && r.value === '') fe[`headers.${i}.value`] = 'enter the new value, or cancel Replace'
    else if (new TextEncoder().encode(r.value).length > MAX_HEADER_VALUE_BYTES) fe[`headers.${i}.value`] = `header value too long: ${name}`
  })
  return fe
}

// ── Settings ────────────────────────────────────────────────────────────────

/** Every type's fields at once, so switching type never loses typing. */
export interface SettingsDraft {
  method: string
  bodyType: 'none' | 'raw' | 'json' | 'form'
  body: string
  authType: 'none' | 'basic' | 'bearer' | 'oauth2'
  basicUser: string
  proxyUrl: string
  proxyUser: string
  caCert: string
  oauthTokenUrl: string
  oauthClientId: string
  oauthScope: string
  oauthAudience: string
  oauthAuth: 'basic' | 'post'
  expectMode: 'contains' | 'regex'
  children: number[]
  groupMode: 'all' | 'any' | 'min'
  groupMin: number
  acceptedCodes: string
  maxRedirects: number
  ignoreTls: boolean
  keyword: string
  keywordMode: 'contains' | 'not_contains' | 'regex'
  caseSensitive: boolean
  jsonPath: string
  jsonOp: '==' | '!=' | 'contains' | '>' | '<' | 'exists'
  jsonExpected: string
  checkDomainExpiry: boolean
  vendor: boolean
  assertions: Assertion[]
  regionMode: '' | 'local' | 'india' | 'custom'
  regionCountries: string[]
  tls: boolean
  security: '' | 'none' | 'starttls' | 'tls'
  maxOffsetMs: number
  grpcService: string
  database: string
  probe: 'query' | 'deadlocks' | 'long_running' | 'blocked' | 'connections'
  query: string
  queryOp: '' | 'eq' | 'ne' | 'gt' | 'gte' | 'lt' | 'lte' | 'contains'
  queryExpected: string
  threshold: number
  thresholdSecs: number
  count: number
  maxLossPct: number
  record: 'A' | 'AAAA' | 'CNAME' | 'MX' | 'TXT' | 'NS'
  resolver: string
  expectedValues: string[]
  match: DnsMatch
  graceS: number
  send: string
  expect: string
  vendorProvider: VendorProvider
  vendorComponents: string
}

export const DEFAULT_SETTINGS: SettingsDraft = {
  method: 'GET', bodyType: 'none', body: '', authType: 'none', basicUser: '',
  proxyUrl: '', proxyUser: '', caCert: '', oauthTokenUrl: '', oauthClientId: '', oauthScope: '', oauthAudience: '', oauthAuth: 'basic',
  expectMode: 'contains', children: [], groupMode: 'all', groupMin: 1,
  acceptedCodes: '200-299', maxRedirects: 10, ignoreTls: false,
  keyword: '', keywordMode: 'contains', caseSensitive: false,
  jsonPath: '', jsonOp: '==', jsonExpected: '', checkDomainExpiry: false, vendor: false, assertions: [], regionMode: 'local', regionCountries: [],
  tls: false, security: '', maxOffsetMs: 0, grpcService: '',
  database: '', probe: 'query', query: '', queryOp: '', queryExpected: '', threshold: 0, thresholdSecs: 30,
  count: 3, maxLossPct: 34,
  record: 'A', resolver: '', expectedValues: [], match: 'exact',
  graceS: 30,
  send: '', expect: '',
  vendorProvider: 'statuspage', vendorComponents: '',
}

const SQL_KEYS: (keyof SettingsDraft)[] = ['basicUser', 'database', 'tls', 'ignoreTls', 'probe', 'query', 'queryOp', 'queryExpected', 'threshold', 'thresholdSecs']

const TYPE_KEYS: Record<MonitorType, (keyof SettingsDraft)[]> = {
  http: ['method', 'bodyType', 'body', 'authType', 'basicUser', 'acceptedCodes', 'maxRedirects', 'ignoreTls',
    'proxyUrl', 'proxyUser', 'caCert', 'oauthTokenUrl', 'oauthClientId', 'oauthScope', 'oauthAudience', 'oauthAuth',
    'keyword', 'keywordMode', 'caseSensitive', 'jsonPath', 'jsonOp', 'jsonExpected', 'checkDomainExpiry', 'vendor', 'assertions', 'regionMode', 'regionCountries'],
  tcp: ['tls', 'ignoreTls', 'send', 'expect', 'expectMode'],
  ping: ['count', 'maxLossPct', 'regionMode', 'regionCountries'],
  dns: ['record', 'resolver', 'expectedValues', 'match', 'regionMode', 'regionCountries'],
  push: ['graceS'],
  manual: [],
  group: ['children', 'groupMode', 'groupMin'],
  websocket: ['send', 'expect'],
  tls: ['ignoreTls'],
  smtp: ['security', 'ignoreTls'],
  ssh: [],
  redis: ['tls', 'ignoreTls', 'basicUser'],
  postgres: [],
  mysql: [],
  mongodb: ['tls', 'ignoreTls'],
  mqtt: ['tls', 'ignoreTls', 'basicUser'],
  ntp: ['maxOffsetMs'],
  udp: ['send', 'expect'],
  amqp: ['tls', 'ignoreTls'],
  memcached: [],
  grpc: ['tls', 'ignoreTls', 'grpcService'],
  banner: ['tls', 'ignoreTls', 'expect'],
  postgres_sql: SQL_KEYS,
  mysql_sql: SQL_KEYS,
  vendor: ['vendorProvider', 'vendorComponents'],
}

export function typeSettingKeys(type: MonitorType): (keyof SettingsDraft)[] {
  return TYPE_KEYS[type]
}

/** The chosen type's fields only, each explicit. */
export function settingsPayload(type: MonitorType, d: SettingsDraft): Settings {
  const out: Record<string, unknown> = {}
  for (const k of TYPE_KEYS[type]) out[k] = d[k]
  if (type === 'http' && d.bodyType === 'none') out.body = ''
  if (type === 'dns') out.expectedValues = cleanValues(d.expectedValues)
  return out as Settings
}

/**
 * GET uses Go `omitempty`, so false/""/0 come back absent. For the monitor's
 * own type an absent field means its zero value (graceS 0 must not come
 * back as 30), except where a select needs a valid option.
 */
export function settingsFromOut(type: MonitorType, s: Settings | undefined): SettingsDraft {
  const d: SettingsDraft = { ...DEFAULT_SETTINGS }
  const src = (s ?? {}) as Record<string, unknown>
  const zero: Partial<Record<keyof SettingsDraft, unknown>> = {
    body: '', basicUser: '', ignoreTls: false, keyword: '', caseSensitive: false, jsonPath: '', jsonExpected: '',
    proxyUrl: '', proxyUser: '', caCert: '', oauthTokenUrl: '', oauthClientId: '', oauthScope: '', oauthAudience: '', children: [], groupMin: 0,
    checkDomainExpiry: false, vendor: false, assertions: [], regionMode: '', regionCountries: [], tls: false, security: '', maxOffsetMs: 0, grpcService: '', database: '', query: '', queryOp: '', queryExpected: '', threshold: 0, resolver: '', expectedValues: [], graceS: 0, send: '', expect: '',
    vendorComponents: '',
  }
  const target = d as unknown as Record<string, unknown>
  for (const k of TYPE_KEYS[type]) {
    const v = src[k]
    if (v !== undefined && v !== null && v !== '') target[k] = v
    else if (k in zero) target[k] = zero[k]
  }
  // Monitors saved before the list existed carry a single `expected`.
  if (type === 'dns' && d.expectedValues.length === 0 && typeof src.expected === 'string' && src.expected) {
    d.expectedValues = [src.expected]
  }
  return d
}

/** Trimmed, non-empty, de-duplicated expected values. */
export function cleanValues(values: string[]): string[] {
  return [...new Set(values.map(v => v.trim()).filter(Boolean))]
}

// ── Draft ───────────────────────────────────────────────────────────────────

export interface Draft {
  type: MonitorType
  name: string
  target: string
  groupId: number | null
  tagIds: number[]
  channelIds: number[]
  intervalS: number
  retryIntervalS: number
  retries: number
  timeoutMs: number
  degradedMs: number
  sslWarnDays: number
  reminderMin: number
  upsideDown: boolean
  notifyDegraded: boolean
  paused: boolean
  description: string
  runbook: string
  settings: SettingsDraft
  headers: HeaderRow[]
  basicPass: SecretDraft
  bearerToken: SecretDraft
  proxyPass: SecretDraft
  /** PEM of the mTLS client certificate and its private key (write-only). */
  clientCert: SecretDraft
  clientKey: SecretDraft
  oauthSecret: SecretDraft
  /** Certificate alert days as typed, e.g. "30, 14, 7, 1" (empty = the default ladder). */
  certThresholds: string
  /** Parent monitor ids (settings.dependsOn). */
  dependsOn: number[]
  latencyAlert: LatencyAlertDraft
}

/** The form's response-time alert; `on` false sends null. */
export interface LatencyAlertDraft extends LatencyAlertCfg {
  on: boolean
}

export const DEFAULT_LATENCY_ALERT: LatencyAlertDraft = { on: false, percentile: 95, thresholdMs: 1000, factor: 0, windowMin: 15 }

export function newDraft(groupId: number | null): Draft {
  return {
    type: 'http', name: '', target: '', groupId, tagIds: [], channelIds: [],
    intervalS: 60, retryIntervalS: 20, retries: 2, timeoutMs: 10000,
    degradedMs: 0, sslWarnDays: 14, reminderMin: 0,
    upsideDown: false, notifyDegraded: false, paused: false,
    description: '', runbook: '',
    settings: { ...DEFAULT_SETTINGS },
    headers: [],
    basicPass: emptySecret(),
    bearerToken: emptySecret(),
    proxyPass: emptySecret(),
    clientCert: emptySecret(),
    clientKey: emptySecret(),
    oauthSecret: emptySecret(),
    certThresholds: '',
    dependsOn: [],
    latencyAlert: { ...DEFAULT_LATENCY_ALERT },
  }
}

export function draftFromOut(m: MonitorOut): Draft {
  const d = newDraft(m.groupId ?? null)
  return {
    ...d,
    type: m.type,
    name: m.name ?? '',
    target: m.target ?? '',
    tagIds: m.tagIds ?? [],
    channelIds: m.channelIds ?? [],
    intervalS: m.intervalS ?? d.intervalS,
    retryIntervalS: m.retryIntervalS ?? d.retryIntervalS,
    retries: m.retries ?? d.retries,
    timeoutMs: m.timeoutMs ?? d.timeoutMs,
    degradedMs: m.degradedMs ?? 0,
    sslWarnDays: m.sslWarnDays ?? 0,
    reminderMin: m.reminderMin ?? 0,
    upsideDown: Boolean(m.upsideDown),
    notifyDegraded: Boolean(m.notifyDegraded),
    paused: Boolean(m.paused),
    description: m.description ?? '',
    runbook: m.runbook ?? '',
    settings: settingsFromOut(m.type, m.settings),
    headers: headerRowsFromOut(m.headers),
    basicPass: emptySecret(Boolean(m.secrets?.basicPass?.isSet)),
    bearerToken: emptySecret(Boolean(m.secrets?.bearerToken?.isSet)),
    proxyPass: emptySecret(Boolean(m.secrets?.proxyPass?.isSet)),
    clientCert: emptySecret(Boolean(m.secrets?.clientCert?.isSet)),
    clientKey: emptySecret(Boolean(m.secrets?.clientKey?.isSet)),
    oauthSecret: emptySecret(Boolean(m.secrets?.oauthSecret?.isSet)),
    certThresholds: (m.settings?.certThresholds ?? []).join(', '),
    dependsOn: m.settings?.dependsOn ?? [],
    latencyAlert: m.settings?.latencyAlert
      ? { on: true, percentile: m.settings.latencyAlert.percentile || 95, thresholdMs: m.settings.latencyAlert.thresholdMs ?? 0, factor: m.settings.latencyAlert.factor ?? 0, windowMin: m.settings.latencyAlert.windowMin || 15 }
      : { ...DEFAULT_LATENCY_ALERT },
  }
}

export function toMonitorIn(d: Draft): MonitorIn {
  const withHeaders = d.type === 'http' || d.type === 'websocket'
  return {
    name: d.name.trim(),
    type: d.type,
    target: NO_TARGET_TYPES.includes(d.type) ? '' : d.target.trim(),
    groupId: d.groupId,
    tagIds: d.tagIds,
    channelIds: d.channelIds,
    intervalS: d.intervalS,
    retryIntervalS: d.retryIntervalS,
    retries: d.retries,
    // Group and manual monitors run nothing, so their timeout is not asked for: keep it valid.
    timeoutMs: d.type === 'group' || d.type === 'manual' ? Math.max(1000, Math.min(10000, Number.isFinite(d.intervalS) ? d.intervalS * 1000 - 500 : 10000)) : d.timeoutMs,
    degradedMs: d.degradedMs,
    sslWarnDays: d.sslWarnDays,
    reminderMin: d.reminderMin,
    upsideDown: d.upsideDown && d.type !== 'group' && d.type !== 'manual',
    notifyDegraded: d.notifyDegraded,
    paused: d.paused,
    description: d.description,
    runbook: d.runbook.trim(),
    settings: {
      ...settingsPayload(d.type, d.settings),
      dependsOn: d.dependsOn,
      certThresholds: CERT_TYPES.includes(d.type) ? parseThresholds(d.certThresholds) ?? [] : [],
      latencyAlert: d.latencyAlert.on && LATENCY_TYPES.includes(d.type)
        ? { percentile: d.latencyAlert.percentile, thresholdMs: d.latencyAlert.thresholdMs, factor: d.latencyAlert.factor, windowMin: d.latencyAlert.windowMin }
        : null,
    },
    headers: withHeaders ? headerRowsToIn(d.headers) : [],
    secrets: {
      basicPass: d.type === 'http' || PASSWORD_TYPES.includes(d.type) ? secretToIn(d.basicPass) : null,
      bearerToken: d.type === 'http' ? secretToIn(d.bearerToken) : null,
      proxyPass: d.type === 'http' ? secretToIn(d.proxyPass) : null,
      clientCert: d.type === 'http' ? secretToIn(d.clientCert) : null,
      clientKey: d.type === 'http' ? secretToIn(d.clientKey) : null,
      oauthSecret: d.type === 'http' ? secretToIn(d.oauthSecret) : null,
    },
  }
}

/** "30, 14 7" → [30, 14, 7]; null when something is not a whole number of days. */
export function parseThresholds(text: string): number[] | null {
  const parts = text.split(/[\s,;]+/).filter(Boolean)
  const out: number[] = []
  for (const p of parts) {
    if (!/^\d+$/.test(p)) return null
    out.push(Number(p))
  }
  return out
}

/** Whether a write-only secret will hold a value after saving. */
export function secretWillBeSet(s: SecretDraft): boolean {
  return s.isSet ? s.mode === 'keep' || (s.mode === 'replace' && s.value !== '') : s.value !== ''
}

// ── Validation (mirrors model.Validate) ─────────────────────────────────────

function intIn(v: number, lo: number, hi: number): boolean {
  return Number.isInteger(v) && v >= lo && v <= hi
}

function validCodes(s: string): boolean {
  const list = (s.trim() === '' ? '200-299' : s).split(',')
  return list.every(part => {
    const m = /^(\d{3})(?:\s*-\s*(\d{3}))?$/.exec(part.trim())
    if (!m) return false
    const lo = Number(m[1])
    const hi = m[2] ? Number(m[2]) : lo
    return lo >= 100 && hi <= 599 && lo <= hi
  })
}

function hostPortError(t: string): string | null {
  const m = /^(?:\[([^\]]+)\]|([^\s:[\]]+)):(\d+)$/.exec(t)
  if (!m) return 'must be host:port'
  const port = Number(m[3])
  return port >= 1 && port <= 65535 ? null : 'port must be 1-65535'
}

function hostOptionalPortError(t: string): string | null {
  if (!t || /[\s/\\]/.test(t)) return 'must be a host or host:port'
  if (/^(?:\[[^\]]+\]|[^\s:[\]]+)$/.test(t) || /^[0-9a-fA-F:]+$/.test(t)) return null
  return hostPortError(t)
}

function urlOk(t: string, schemes: string[]): boolean {
  try {
    const u = new URL(t)
    return schemes.includes(u.protocol) && u.host !== ''
  } catch {
    return false
  }
}

const RESOLVER_RE = /^(?:\d{1,3}(?:\.\d{1,3}){3}(?::\d{1,5})?|\[[0-9a-fA-F:.]+\](?::\d{1,5})?|[0-9a-fA-F]*:[0-9a-fA-F:.]+)$/

const READ_ONLY_START = /^\s*(select|with|show|explain|values|table)\b/i

/** Mirrors model.QueryProblem: one read-only statement. */
export function queryProblem(q: string): string {
  const t = q.trim()
  if (!t) return 'required'
  if (t.length > 4000) return 'at most 4000 characters'
  if (!READ_ONLY_START.test(t)) return 'must start with SELECT, WITH, SHOW, EXPLAIN, VALUES or TABLE'
  if (t.replace(/;$/, '').includes(';')) return 'one statement only'
  return ''
}

/** Mirrors model.validateSQL. */
function validateHttpTransport(d: Draft): FieldErrors {
  const fe: FieldErrors = {}
  const s = d.settings
  const proxy = s.proxyUrl.trim()
  if (proxy) {
    let u: URL | null = null
    try { u = new URL(proxy) } catch { u = null }
    if (!u || !['http:', 'https:', 'socks5:', 'socks5h:'].includes(u.protocol) || !u.hostname) fe['settings.proxyUrl'] = 'must be http://, https://, socks5:// or socks5h:// host:port'
    else if (u.username || u.password) fe['settings.proxyUrl'] = 'leave the login out of the URL; use the proxy user and password fields'
    else if ((u.pathname !== '/' && u.pathname !== '') || u.search || u.hash) fe['settings.proxyUrl'] = 'must be just scheme, host and port'
  }
  if (s.caCert.trim() && !s.caCert.includes('-----BEGIN CERTIFICATE-----')) fe['settings.caCert'] = 'paste PEM certificates (-----BEGIN CERTIFICATE-----)'
  if (secretWillBeSet(d.clientCert) !== secretWillBeSet(d.clientKey)) fe['secrets.clientCert'] = 'set both the client certificate and its private key'
  for (const [key, label] of [['proxyPass', 'password'], ['clientCert', 'certificate'], ['clientKey', 'key'], ['oauthSecret', 'secret']] as const) {
    const sd = d[key]
    if (sd.isSet && sd.mode === 'replace' && sd.value === '') fe[`secrets.${key}`] = `enter the new ${label}, or cancel Replace`
  }
  if (s.authType === 'oauth2') {
    if (!urlOk(s.oauthTokenUrl.trim(), ['http:', 'https:'])) fe['settings.oauthTokenUrl'] = 'must be an absolute http(s) URL'
    if (!s.oauthClientId.trim()) fe['settings.oauthClientId'] = 'required'
    if (!secretWillBeSet(d.oauthSecret)) fe['secrets.oauthSecret'] = 'required'
  }
  return fe
}

/** Mirrors model.validateTCPExpect and validateGroup. */
function validateTcpAndGroup(d: Draft): FieldErrors {
  const fe: FieldErrors = {}
  const s = d.settings
  if (d.type === 'tcp') {
    if (s.send.length > 1024) fe['settings.send'] = 'at most 1024 characters'
    if (s.send.startsWith('hex:') && !/^([0-9a-fA-F]{2}\s*)*$/.test(s.send.slice(4).trim())) fe['settings.send'] = 'hex: must be followed by valid hex bytes'
    if (s.expect.length > 512) fe['settings.expect'] = 'at most 512 characters'
    else if (s.expectMode === 'regex') {
      if (!s.expect) fe['settings.expect'] = 'required for a regular expression'
      else { try { new RegExp(s.expect) } catch (err) { fe['settings.expect'] = `invalid regex: ${err instanceof Error ? err.message : 'syntax error'}` } }
    } else if (s.expect.startsWith('hex:') && !/^([0-9a-fA-F]{2}\s*)*$/.test(s.expect.slice(4).trim())) fe['settings.expect'] = 'hex: must be followed by valid hex bytes'
  }
  if (d.type === 'group') {
    if (s.children.length < 1 || s.children.length > 100) fe['settings.children'] = 'pick 1 to 100 monitors'
    if (s.groupMode === 'min' && !intIn(s.groupMin, 1, Math.max(s.children.length, 1))) fe['settings.groupMin'] = 'must be 1 to the number of children'
  }
  return fe
}

function validateSql(d: Draft): FieldErrors {
  const fe: FieldErrors = {}
  const s = d.settings
  if (!s.basicUser.trim()) fe['settings.basicUser'] = 'required'
  else if (s.basicUser.length > 128) fe['settings.basicUser'] = 'at most 128 characters'
  if (s.database.length > 128) fe['settings.database'] = 'at most 128 characters'
  if (!intIn(s.threshold, 0, 1_000_000)) fe['settings.threshold'] = 'must be 0-1000000'
  if (s.probe === 'query') {
    const p = queryProblem(s.query)
    if (p) fe['settings.query'] = p
    if (s.queryOp !== '') {
      if (s.queryExpected === '') fe['settings.queryExpected'] = 'required'
      else if (s.queryOp !== 'contains' && s.queryOp !== 'eq' && s.queryOp !== 'ne' && !Number.isFinite(Number(s.queryExpected.trim()))) fe['settings.queryExpected'] = 'must be a number'
    }
  }
  if (s.probe === 'long_running' && !intIn(s.thresholdSecs, 1, 86400)) fe['settings.thresholdSecs'] = 'must be 1-86400'
  if (s.probe === 'connections' && !intIn(s.threshold, 1, 100)) fe['settings.threshold'] = 'must be 1-100 (percent)'
  if (d.basicPass.isSet && d.basicPass.mode === 'replace' && d.basicPass.value === '') fe['secrets.basicPass'] = 'enter the new password, or cancel Replace'
  return fe
}

export function validateDraft(d: Draft): FieldErrors {
  const fe: FieldErrors = {}
  const name = d.name.trim()
  if (name.length < 1 || [...name].length > 120) fe.name = 'must be 1-120 characters'

  const t = d.target.trim()
  switch (d.type) {
    case 'http': if (!urlOk(t, ['http:', 'https:'])) fe.target = 'must be an absolute http(s) URL with a host'; break
    case 'websocket': if (!urlOk(t, ['ws:', 'wss:'])) fe.target = 'must be a ws(s) URL'; break
    case 'tls': case 'ntp': { const e = hostOptionalPortError(t); if (e) fe.target = e; break }
    case 'tcp': case 'smtp': case 'ssh': case 'redis': case 'postgres': case 'mysql': case 'mongodb': case 'mqtt': case 'udp': case 'amqp': case 'memcached': case 'grpc': case 'banner': case 'postgres_sql': case 'mysql_sql': {
      const e = hostPortError(t); if (e) fe.target = e; break
    }
    case 'ping': if (!t) fe.target = 'host or IP required'; break
    case 'dns': if (!t) fe.target = 'hostname required'; break
    case 'vendor': if (!urlOk(t, ['http:', 'https:'])) fe.target = 'must be an absolute http(s) URL with a host'; break
    case 'push': case 'group': case 'manual': break
  }

  if (!intIn(d.intervalS, 10, 86400)) fe.intervalS = 'must be 10-86400'
  if (!intIn(d.retryIntervalS, 10, 86400)) fe.retryIntervalS = 'must be 10-86400'
  if (!intIn(d.retries, 0, 10)) fe.retries = 'must be 0-10'
  const maxTimeout = Number.isFinite(d.intervalS) ? d.intervalS * 1000 - 500 : 0
  if (d.type !== 'group' && d.type !== 'manual' && (!Number.isInteger(d.timeoutMs) || d.timeoutMs < 1000 || d.timeoutMs > maxTimeout)) {
    fe.timeoutMs = maxTimeout >= 1000 ? `must be 1000 to ${maxTimeout}` : 'must be 1000 to intervalS*1000-500'
  }
  if (!(d.degradedMs === 0 || intIn(d.degradedMs, 1, 120000))) fe.degradedMs = 'must be 0 or 1-120000'
  if (!intIn(d.sslWarnDays, 0, 90)) fe.sslWarnDays = 'must be 0-90'
  if (!intIn(d.reminderMin, 0, 1440)) fe.reminderMin = 'must be 0-1440'
  if ([...d.description].length > 1000) fe.description = 'must be at most 1000 characters'
  if ([...d.runbook].length > 1000) fe.runbook = 'must be at most 1000 characters'
  if (d.dependsOn.length > 5) fe['settings.dependsOn'] = 'at most 5 parent monitors'
  const la = d.latencyAlert
  if (la.on && LATENCY_TYPES.includes(d.type)) {
    if (!intIn(la.windowMin, 5, 60)) fe['settings.latencyAlert.windowMin'] = 'must be 5-60 minutes'
    else if (Number.isFinite(d.intervalS) && la.windowMin * 60 > 100 * d.intervalS) fe['settings.latencyAlert.windowMin'] = `at this interval the last 100 checks cover ${Math.floor(100 * d.intervalS / 60)} min — use that or less`
    if (!intIn(la.thresholdMs, 0, 120000)) fe['settings.latencyAlert.thresholdMs'] = 'must be 0-120000'
    if (!(la.factor === 0 || (la.factor >= 1.5 && la.factor <= 20))) fe['settings.latencyAlert.factor'] = 'must be 0 or 1.5-20'
    if (la.thresholdMs === 0 && la.factor === 0) fe['settings.latencyAlert'] = 'set a ms limit, a multiple of the usual value, or both'
  }

  if (CERT_TYPES.includes(d.type)) {
    const t = parseThresholds(d.certThresholds)
    if (t === null || t.some(n => n < 1 || n > 365)) fe['settings.certThresholds'] = 'whole days, 1-365, e.g. 30, 14, 7, 1'
    else if (t.length > 8) fe['settings.certThresholds'] = 'at most 8 thresholds'
    else if (new Set(t).size !== t.length) fe['settings.certThresholds'] = 'each threshold once'
  }

  const s = d.settings
  if (d.type === 'http') {
    Object.assign(fe, validateHttpTransport(d))
    if (!validCodes(s.acceptedCodes)) fe['settings.acceptedCodes'] = 'use codes 100-599 and ranges, e.g. 200-299,301'
    if (!intIn(s.maxRedirects, 0, 20)) fe['settings.maxRedirects'] = 'must be 0-20'
    if (s.bodyType !== 'none' && new TextEncoder().encode(s.body).length > 64 * 1024) fe['settings.body'] = 'must be at most 64 KiB'
    if (s.keywordMode === 'regex' && s.keyword) {
      try { new RegExp(s.keyword) } catch (err) { fe['settings.keyword'] = `invalid regex: ${err instanceof Error ? err.message : 'syntax error'}` }
    }
    if (s.authType === 'basic' && d.basicPass.isSet && d.basicPass.mode === 'replace' && d.basicPass.value === '') {
      fe['secrets.basicPass'] = 'enter the new password, or cancel Replace'
    }
    if (s.authType === 'bearer' && d.bearerToken.isSet && d.bearerToken.mode === 'replace' && d.bearerToken.value === '') {
      fe['secrets.bearerToken'] = 'enter the new token, or cancel Replace'
    }
  }
  if (d.type === 'tcp' || d.type === 'group') Object.assign(fe, validateTcpAndGroup(d))
  if (d.type === 'http' || d.type === 'websocket') Object.assign(fe, validateHeaderRows(d.headers))
  if (d.type === 'ping') {
    if (!intIn(s.count, 1, 10)) fe['settings.count'] = 'must be 1-10'
    if (!intIn(s.maxLossPct, 1, 100)) fe['settings.maxLossPct'] = 'must be 1-100'
  }
  if (d.type === 'dns' && s.resolver.trim() && !RESOLVER_RE.test(s.resolver.trim())) fe['settings.resolver'] = 'must be ip[:port]'
  if (d.type === 'postgres_sql' || d.type === 'mysql_sql') Object.assign(fe, validateSql(d))
  if (d.type === 'banner' && s.expect.trim() === '') fe['settings.expect'] = 'required: the text the first line should contain'
  if (d.type === 'udp' && s.expect.trim() === '') fe['settings.expect'] = 'required: UDP has no handshake, so a matching reply is the only proof of life'
  if (d.type === 'ntp' && !intIn(s.maxOffsetMs, 0, 86_400_000)) fe['settings.maxOffsetMs'] = 'must be 0-86400000'
  if (PASSWORD_TYPES.includes(d.type) && d.basicPass.isSet && d.basicPass.mode === 'replace' && d.basicPass.value === '') {
    fe['secrets.basicPass'] = 'enter the new password, or cancel Replace'
  }
  if (d.type === 'push' && !intIn(s.graceS, 0, 3600)) fe['settings.graceS'] = 'must be 0-3600'
  return fe
}

const TOP_KEYS = new Set([
  'name', 'target', 'groupId', 'tagIds', 'channelIds', 'intervalS', 'retryIntervalS', 'retries', 'timeoutMs',
  'degradedMs', 'sslWarnDays', 'reminderMin', 'description', 'runbook', 'settings.certThresholds',
])

/** Error keys no input on the current form shows — listed at the top. */
export function unplacedErrors(errors: FieldErrors, type: MonitorType): [string, string][] {
  const typeKeys = new Set(typeSettingKeys(type).map(k => `settings.${k}`))
  const headers = type === 'http' || type === 'websocket'
  return Object.entries(errors).filter(([k, v]) => {
    if (!v) return false
    if (TOP_KEYS.has(k) || typeKeys.has(k)) return false
    if (headers && /^(settings\.)?headers(\.|$)/.test(k)) return false
    if (type === 'http' && k.startsWith('secrets.')) return false
    if (PASSWORD_TYPES.includes(type) && k === 'secrets.basicPass') return false
    return true
  })
}
