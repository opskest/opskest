// TypeScript mirrors of the Go API/domain types. JSON is camelCase; every
// timestamp is unix milliseconds. A few store structs still go over the
// wire without json tags (PascalCase keys); lib/normalize.ts maps those onto
// the shapes below, so pages only ever see these types.

// ── Status & enums ──────────────────────────────────────────────────────────

/** model.Status, serialized as its String() form. */
export type Status = 'up' | 'degraded' | 'pending' | 'down' | 'unknown' | 'maintenance' | 'paused'

/** `vendor` reads a 3rd-party status feed (Statuspage, Google, RSS) instead of pinging it. */
/** equals/contains: any answer matches any value; all: every value present; exact: answers are exactly the values. */
export type DnsMatch = 'equals' | 'contains' | 'all' | 'exact'

export type MonitorType =
  | 'http' | 'tcp' | 'ping' | 'dns' | 'push' | 'websocket' | 'vendor'
  | 'tls' | 'smtp' | 'ssh' | 'redis' | 'postgres' | 'mysql' | 'mongodb' | 'mqtt' | 'ntp' | 'udp'
  | 'amqp' | 'memcached' | 'banner' | 'grpc' | 'postgres_sql' | 'mysql_sql'
  /** `group` follows its children; `manual` is set by hand or the API. Neither has a target. */
  | 'group' | 'manual'

export type VendorProvider = 'statuspage' | 'google' | 'rss'

export type ErrClass = '' | 'timeout' | 'dns' | 'refused' | 'tls' | 'http_status' | 'assertion' | 'network' | 'other'

export type Role = 'admin' | 'user' | 'viewer' | 'disabled'

export type ChannelType =
  | 'slack' | 'email' | 'teams' | 'gchat' | 'webhook' | 'pagerduty' | 'opsgenie'
  | 'discord' | 'telegram' | 'ntfy' | 'gotify' | 'pushover' | 'matrix' | 'mattermost' | 'rocketchat'
  | 'zulip' | 'webex' | 'pushbullet' | 'ifttt' | 'apprise' | 'twilio' | 'homeassistant' | 'bark' | 'signal'

export type IncidentStatus = 'investigating' | 'identified' | 'monitoring' | 'resolved'

export type MaintenanceKind = 'once' | 'weekly'

export type MaintenanceState = 'active' | 'upcoming' | 'past' | 'inactive'

export type PageVisibility = 'public' | 'private'

export type PageLayout = 'list' | 'board'

export type OverallStatus = 'operational' | 'degraded' | 'partial' | 'major' | 'maintenance'

/** model.FieldErrors, as returned in the `fields` key of an error body. */
export type FieldErrors = Record<string, string>

// ── Building blocks ─────────────────────────────────────────────────────────

/** A masked secret: never the value, only whether it is set. */
export interface SecretState {
  isSet: boolean
}

export interface Group {
  id: number
  name: string
  sort: number
  description: string
}

export interface Tag {
  id: number
  name: string
  color: string
}

// ── Monitors ────────────────────────────────────────────────────────────────

export interface AckInfo {
  by: string
  at: number
}

/** engine.MonitorSummary — the live view carried over SSE and GET /api/monitors. */
export interface MonitorSummary {
  id: number
  name: string
  type: MonitorType
  groupId: number | null
  tagIds: number[]
  target: string
  status: Status
  since: number
  latencyMs: number
  uptime24h: number | null
  uptime7d: number | null
  uptime30d: number | null
  uptime90d: number | null
  /** Last 100 statuses (50 on status pages), one char each: u d p g k m. */
  beats: string
  /** Last 100 latencies, aligned with `beats`. */
  lat: number[]
  lastCheckAt: number
  intervalS: number
  certDays: number | null
  domainDays: number | null
  ack: AckInfo | null
  reason: string
  errClass: ErrClass
  paused: boolean
  maint: boolean
  outageId: number | null
  runbook: string
  /** 3rd-party status feed: alert-only, not counted among your monitors. */
  vendor?: boolean
  assertions?: Assertion[]
  regionMode?: '' | 'local' | 'india' | 'custom'
  regionCountries?: string[]
  /** A down parent holding this monitor's outage alerts. */
  suppressedBy?: number | null
}

/** engine.Cluster — a likely common cause (§12.1). */
export interface Cluster {
  kind: 'host' | 'errClass'
  key: string
  monitorIds: number[]
  since: number
  errClass: ErrClass
}

/** model.Settings — per-type check configuration. */
/** One extra check on an HTTP response (model.Assertion). */
export interface Assertion {
  source: 'status' | 'latency' | 'header' | 'body' | 'json'
  property?: string
  op: string
  value?: string
}

/** model.LatencyAlert: alert when a percentile over a window exceeds a limit. */
export interface LatencyAlertCfg {
  percentile: 50 | 90 | 95 | 99
  /** 0 = no absolute limit. */
  thresholdMs: number
  /** 0 = no relative limit; else × the usual value. */
  factor: number
  windowMin: number
}

export interface Settings {
  /** Parent monitors: while one is down, this monitor's alerts are held. */
  dependsOn?: number[]
  latencyAlert?: LatencyAlertCfg | null
  method?: string
  body?: string
  bodyType?: 'none' | 'raw' | 'json' | 'form'
  authType?: 'none' | 'basic' | 'bearer' | 'oauth2'
  basicUser?: string
  /** http: outbound proxy (http, https, socks5), login in proxyUser + the proxyPass secret. */
  proxyUrl?: string
  proxyUser?: string
  /** http: PEM of extra trusted CA certificates. */
  caCert?: string
  /** http, authType oauth2: client-credentials grant. */
  oauthTokenUrl?: string
  oauthClientId?: string
  oauthScope?: string
  oauthAudience?: string
  oauthAuth?: 'basic' | 'post'
  /** Days before expiry at which a certificate alert is sent, once each. */
  certThresholds?: number[]
  /** group: the monitors it follows and how. */
  children?: number[]
  groupMode?: 'all' | 'any' | 'min'
  groupMin?: number
  /** tcp: how `expect` is matched. */
  expectMode?: 'contains' | 'regex'
  acceptedCodes?: string
  maxRedirects?: number | null
  ignoreTls?: boolean
  keyword?: string
  keywordMode?: 'contains' | 'not_contains' | 'regex'
  caseSensitive?: boolean
  jsonPath?: string
  jsonOp?: '==' | '!=' | 'contains' | '>' | '<' | 'exists'
  jsonExpected?: string
  checkDomainExpiry?: boolean
  vendor?: boolean
  tls?: boolean
  /** smtp: none | starttls | tls */
  security?: 'none' | 'starttls' | 'tls'
  /** grpc: service name for grpc.health.v1 (empty = the server overall). */
  grpcService?: string
  /** postgres_sql / mysql_sql */
  database?: string
  probe?: 'query' | 'deadlocks' | 'long_running' | 'blocked' | 'connections'
  query?: string
  queryOp?: 'eq' | 'ne' | 'gt' | 'gte' | 'lt' | 'lte' | 'contains'
  queryExpected?: string
  threshold?: number
  thresholdSecs?: number
  /** ntp: fail when this host's clock is further off (ms); 0 = off. */
  maxOffsetMs?: number
  count?: number
  maxLossPct?: number
  record?: 'A' | 'AAAA' | 'CNAME' | 'MX' | 'TXT' | 'NS'
  resolver?: string
  /** Legacy single expected value; folded into expectedValues on save. */
  expected?: string
  expectedValues?: string[]
  match?: DnsMatch
  graceS?: number
  send?: string
  expect?: string
  vendorProvider?: VendorProvider
  /** Optional comma-separated component names to watch (empty = all). */
  vendorComponents?: string
}

/** GET /api/monitors/{id}/vendor → one active vendor incident. */
export interface VendorIncident {
  title: string
  /** Vendor-side status, e.g. investigating, identified, monitoring. */
  status: string
  /** Vendor-side impact, e.g. minor, major, critical, maintenance. */
  impact: string
  startedAt: number | null
  updatedAt: number | null
  url: string
  components: string[]
}

/** api.HeaderIn — value === null keeps the stored secret. */
export interface HeaderIn {
  name: string
  value: string | null
  secret: boolean
}

/** api.MonitorIn — the create/update/test request body. */
export interface MonitorIn {
  name: string
  type: MonitorType
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
  settings: Settings
  headers: HeaderIn[]
  secrets: {
    basicPass: string | null
    bearerToken: string | null
    proxyPass: string | null
    clientCert: string | null
    clientKey: string | null
    oauthSecret: string | null
  }
}

/** A header as returned by GET: the value is present unless it is secret. */
export interface MonitorHeaderOut {
  name: string
  value?: string
  secret: boolean
  isSet?: boolean
}

/** Exact cert/domain expiry, when the server sends it. */
export interface MonitorRuntime {
  certNotAfter: number | null
  certIssuer?: string | null
  domainExpiresAt: number | null
}

/** api.MonitorOut — full monitor config with secrets masked. */
export interface MonitorOut {
  id: number
  name: string
  type: MonitorType
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
  settings: Settings
  pushToken?: string
  pushUrl?: string
  headers: MonitorHeaderOut[]
  secrets: {
    basicPass: SecretState
    bearerToken: SecretState
    proxyPass?: SecretState
    clientCert?: SecretState
    clientKey?: SecretState
    oauthSecret?: SecretState
  }
  createdAt: number
  updatedAt: number
  runtime?: MonitorRuntime
}

// ── History ─────────────────────────────────────────────────────────────────

/** store.Heartbeat as returned by /beats. Sub-timings < 0 = not measured. */
export interface Heartbeat {
  monitorId: number
  ts: number
  status: Status
  latencyMs: number
  code: number
  dnsMs: number
  tcpMs: number
  tlsMs: number
  ttfbMs: number
  /** Request sent → first byte (edge + app); -1 when unknown. */
  waitMs: number
  /** App-reported processing time (Server-Timing, X-Response-Time…); -1 when not reported. */
  appMs: number
  /** Who answered: CDN/POP, cache status, load balancer, web server. */
  edge: string
  /** Connection used: "ip · HTTP/2 · TLS 1.3[ · resumed]". */
  conn: string
  errClass: string
  msg: string
  maint: boolean
}

/** One bucket of the response-time chart (raw series or a rollup). */
export interface SeriesPoint {
  ts: number
  avg: number
  min: number
  max: number
  up: number
  down: number
}

/** One day of the 90-day bars. `day` is server calendar midnight, unix ms. */
export interface DailyBar {
  day: number
  /** 0–100, or null when nothing was measured that day. */
  uptime: number | null
  down: number
  outages?: number
  /** Status pages: approximate minutes of downtime that day. */
  downMinutes?: number
  /** Status pages: incidents that touched this component that day. */
  incidents?: { id: number; title: string; impact: string }[]
}

/** One change of a monitor's displayed status (GET /api/events). */
export interface StatusEvent {
  id: number
  monitorId: number
  ts: number
  from: Status
  to: Status
  reason: string
  errClass: string
}

// ── Outages ─────────────────────────────────────────────────────────────────

export interface Outage {
  id: number
  monitorId: number
  startedAt: number
  endedAt: number | null
  firstReason: string
  errClass: string
  ackedBy: string | null
  ackedAt: number | null
  note: string | null
  noteBy: string | null
  /** When the note was last written (null for older notes). */
  noteAt: number | null
  /** How many notes the outage's thread holds (the fields above summarise the newest). */
  noteCount: number
  regions: RegionCheck | null
  /** Parent that was down when it opened: its alerts were held. */
  suppressedBy: number | null
}

/** Multi-region confirmation of an outage (Globalping), when enabled. */
export interface RegionCheck {
  checkedAt: number
  reachable: number
  total: number
  error?: string
  probes: { country: string; city: string; ok: boolean; code?: number; latencyMs: number; detail?: string }[]
}

/** One entry in an outage's note thread. */
export interface OutageNote {
  id: number
  outageId: number
  body: string
  authorName: string
  createdAt: number
  /** Set once the note was changed after posting. */
  editedAt: number | null
  /** Whether the signed-in user may edit or delete it (author or admin). */
  canEdit: boolean
}

export interface OutageSummary {
  active: number
  last30d: number
  /** Ended in the last 30 days with no note saying why. */
  needsNote: number
  mttrSeconds: number
  mostAffectedMonitorId: number | null
  mostAffectedCount: number
}

// ── Incidents ───────────────────────────────────────────────────────────────

export interface IncidentUpdate {
  id: number
  incidentId: number
  status: IncidentStatus
  body: string
  createdBy: number | null
  createdByName: string
  createdAt: number
  /** Team-only note: never shown on status pages. */
  internal: boolean
  /** Set once the entry was changed after posting. */
  editedAt: number | null
}

export interface Incident {
  id: number
  title: string
  status: IncidentStatus
  impact: string
  createdBy: number | null
  createdAt: number
  updatedAt: number
  resolvedAt: number | null
  severity: string
  commanderId: number | null
  commanderName: string
  postmortem: string
  monitorIds: number[]
  pageIds: number[]
  updates: IncidentUpdate[]
}

/** POST /api/incidents. The server always starts an incident as Investigating. */
export interface IncidentIn {
  title: string
  impact: string
  body: string
  monitorIds: number[]
  pageIds: number[]
  severity?: string
  commanderId?: number | null
}

// ── Maintenance ─────────────────────────────────────────────────────────────

export interface Maintenance {
  id: number
  title: string
  description: string
  kind: MaintenanceKind
  startsAt: number | null
  endsAt: number | null
  /** Weekly windows: bitmask, Mon = bit 0 (spec §9). */
  weekdays: number
  startMinute: number
  durationMin: number
  tz: string
  active: boolean
  monitorIds: number[]
  groupIds: number[]
  createdAt: number
  updatedAt: number
  /** The user id that created the window, when known. */
  createdBy?: number | null
  /** Computed by GET /api/maintenance. */
  state?: MaintenanceState
  nextStart?: number | null
  currentEnd?: number | null
}

/** A maintenance window as a status page sees it — no targets or author. */
export type PublicMaintenance = Pick<Maintenance, 'id' | 'title' | 'description' | 'kind' | 'startsAt' | 'endsAt' | 'nextStart' | 'currentEnd'>

export interface MaintenanceIn {
  title: string
  description: string
  kind: MaintenanceKind
  startsAt: number | null
  endsAt: number | null
  weekdays: number
  startMinute: number
  durationMin: number
  tz: string
  active: boolean
  monitorIds: number[]
  groupIds: number[]
}

// ── Channels ────────────────────────────────────────────────────────────────

/** A channel config as returned by GET: secret-bearing fields are masked. */
export interface MaskedChannelConfig {
  host?: string
  port?: number
  username?: string
  from?: string
  to?: string[]
  tlsMode?: string
  region?: string
  /** Telegram chat id, Pushover user key or Matrix room id. */
  target?: string
  url?: SecretState
  hmacSecret?: SecretState
  routingKey?: SecretState
  apiKey?: SecretState
  password?: SecretState
  /** The whole header map is masked as one {isSet}. */
  headers?: SecretState
  topic?: string
  template?: string
  quiet?: QuietHours
}

/** A recurring window in the display time zone in which a channel stays silent. */
export interface QuietHours {
  enabled: boolean
  /** 0 (Sunday) to 6 (Saturday), the day the window starts on; empty = every day. */
  days: number[]
  start: string
  end: string
  /** Down alerts, reminders and recoveries still go through. */
  bypassCritical: boolean
}

export interface Channel {
  id: number
  name: string
  type: ChannelType
  isDefault: boolean
  config: MaskedChannelConfig
  createdAt: number
}

/** Create/update config: a secret is a string to set it, null to keep it. */
export interface ChannelConfigIn {
  host?: string
  port?: number
  username?: string
  from?: string
  to?: string[]
  tlsMode?: string
  region?: string
  target?: string
  url?: string | null
  hmacSecret?: string | null
  routingKey?: string | null
  apiKey?: string | null
  password?: string | null
  headers?: Record<string, string>
  topic?: string
  template?: string
  quiet?: QuietHours
}

export interface ChannelIn {
  name: string
  type: ChannelType
  isDefault: boolean
  config: ChannelConfigIn
}

// ── Status pages ────────────────────────────────────────────────────────────

export interface PageItem {
  sort: number
  groupId: number | null
  monitorId: number | null
  displayName: string
  /** One line shown on the public page. */
  description: string
}

/** Customer-facing branding for a status page. */
export interface PageBranding {
  logoUrl: string
  brandColor: string
  websiteUrl: string
  supportUrl: string
  footerText: string
  showResponseTime: boolean
  historyDays: 30 | 60 | 90
  /** Public page only: which subscriptions visitors can sign up for. */
  subscribeEmail?: boolean
  subscribeSlack?: boolean
}

/** A status page's board alarm (stored on the page, so every screen agrees). */
export interface AlarmConfig {
  muted: boolean
  down: 'police-siren' | 'industrial-klaxon' | 'retro-panic' | ''
  degraded: 'modern-chime' | 'tech-radar' | ''
}

export interface Page {
  id: number
  slug: string
  title: string
  description: string
  visibility: PageVisibility
  layout: PageLayout
  showLatency: boolean
  alarm: AlarmConfig
  branding: PageBranding
  subscriptions: { enabled: boolean; channelId: number | null }
  items: PageItem[]
  createdAt: number
  updatedAt: number
}

/** hub.PublicMonitor — a sanitized summary for status pages. */
export interface PublicMonitor {
  id: number
  name: string
  groupId: number | null
  status: Status
  since: number
  uptime24h: number | null
  uptime30d?: number | null
  beats: string
  latencyMs?: number
  reason?: string
}

export interface PublicPageGroup {
  id: number
  name: string
  description: string
  monitorIds: number[]
}

/** GET /api/public/pages/{slug}, after normalization. */
export interface PublicPage {
  page: {
    title: string
    description: string
    layout: PageLayout
    visibility: PageVisibility
    showLatency: boolean
    alarm: AlarmConfig
    branding: PageBranding
  }
  groups: PublicPageGroup[]
  monitorDescriptions: Map<number, string>
  monitors: PublicMonitor[]
  daily: Map<number, DailyBar[]>
  /** Uptime % per monitor over the page's history window. */
  uptime: Map<number, number>
  incidents: { active: Incident[]; past: Incident[] }
  maintenance: { active: PublicMaintenance[]; upcoming: PublicMaintenance[] }
  overall: OverallStatus
}

// ── Users, settings, logs, system ───────────────────────────────────────────

export interface User {
  username?: string
  id: number
  email: string
  name: string
  avatarUrl: string
  role: Role
  createdAt: number
  lastLoginAt: number | null
  /** Whether the account has an authenticator app set up (absent on older servers). */
  twoFactorEnabled?: boolean
}

/** GET /api/me. */
export interface Me {
  username?: string
  id: number
  email: string
  name: string
  avatarUrl: string
  role: Role
  /** This account has an authenticator app set up. */
  twoFactorEnabled?: boolean
  /** The workspace requires two-factor and this account has none yet: the app is limited to Account security. */
  twoFactorRequired?: boolean
}

export interface AppSettings {
  retentionRawDays: number
  stormThreshold: number
  stormWindowS: number
  backupHour: number
  backupKeep: number
  defaultReminderMin: number
  regionConfirm: number
  /** Everyone must set up an authenticator app before using the workspace. */
  requireTwoFactor?: boolean
}

export interface AlertLogEntry {
  id: number
  ts: number
  channelId: number | null
  channelName?: string
  monitorId: number | null
  monitorName?: string
  event: string
  ok: boolean
  attempt: number
  error: string
}

export interface AuditEntry {
  id: number
  ts: number
  userId: number | null
  userEmail: string
  action: string
  entity: string
  entityId: number | null
  detail: string
}

export interface BackupFile {
  name: string
  size: number
  modTime: number
}

/** GET /api/system. */
export interface SystemInfo {
  version: string
  startedAt: number
  monitors: number
  lagP50Ms: number
  lagP99Ms: number
  inFlight: number
  writerDropped: number
  connectivity: { online: boolean; since: number }
  backups: { dir: string; files: BackupFile[] }
}

// ── Checks ──────────────────────────────────────────────────────────────────

/** model.Timing — sub-timings in ms; -1 = not measured. */
export interface Timing {
  dnsMs: number
  tcpMs: number
  tlsMs: number
  ttfbMs: number
}

export interface CertInfo {
  notAfter: number
  issuer: string
  subject: string
}

/** model.Result — the outcome of one check (POST /api/monitors/test). */
export interface Result {
  at: number
  ok: boolean
  degraded: boolean
  latencyMs: number
  code: number
  timing: Timing | null
  reason: string
  errClass: ErrClass
  cert: CertInfo | null
  lossPct: number
}

// ── Latency percentiles ─────────────────────────────────────────────────────

/** Stats over a set of latency samples; every value is null when count is 0. */
export interface LatencyStats {
  count: number
  avg: number | null
  min: number | null
  max: number | null
  p50: number | null
  p90: number | null
  p95: number | null
  p99: number | null
}

export interface LatencyPoint extends LatencyStats {
  /** Start of the point's window, unix ms. */
  t: number
}

export type LatencyRange = '1h' | '24h' | '7d' | '30d' | '90d'

/** GET /api/monitors/{id}/latency */
export interface LatencySeries {
  range: LatencyRange
  source: 'raw' | 'hourly' | 'daily'
  summary: LatencyStats
  points: LatencyPoint[]
}

