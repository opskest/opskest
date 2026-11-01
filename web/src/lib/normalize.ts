// Tolerant decoders for API payloads. A few Go structs still serialize
// without json tags (PascalCase keys, e.g. store.Tag, store.SeriesPoint,
// store.Rollup, store.AlertLogEntry, store.OutageSummary, model.Result),
// and several list fields come back as `null` instead of `[]`. Every page
// reads through these so it never has to care which shape arrived.

import type {
  AlarmConfig,
  AlertLogEntry, CertInfo, DailyBar, ErrClass, Group, Heartbeat, Incident, IncidentUpdate, Maintenance,
  Outage, OutageNote, OutageSummary, OverallStatus, Page, PageBranding, PublicMonitor, PublicPage, Result, SeriesPoint, Status,
  SystemInfo, Tag, Timing,
} from './types'
import { serverTimeZone } from './runtime.ts'
import { serverDayStartMs } from './format.ts'

type Raw = Record<string, unknown>

function obj(v: unknown): Raw {
  return v && typeof v === 'object' ? v as Raw : {}
}

/** First defined value among `keys` (camelCase first, then Go's names). */
function pick(o: Raw, ...keys: string[]): unknown {
  for (const k of keys) if (o[k] !== undefined) return o[k]
  return undefined
}

function num(v: unknown, fallback = 0): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : fallback
}

function numOrNull(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? v : null
}

function str(v: unknown, fallback = ''): string {
  return typeof v === 'string' ? v : fallback
}

function arr(v: unknown): unknown[] {
  return Array.isArray(v) ? v : []
}

function ids(v: unknown): number[] {
  return arr(v).filter((x): x is number => typeof x === 'number')
}

/** A unix-ms number, or an RFC 3339 string (time.Time without a json tag). */
function time(v: unknown): number | null {
  if (typeof v === 'number' && Number.isFinite(v)) return v
  if (typeof v === 'string') {
    const t = Date.parse(v)
    return Number.isNaN(t) ? null : t
  }
  return null
}

const STATUSES = new Set<Status>(['up', 'degraded', 'pending', 'down', 'unknown', 'maintenance', 'paused'])

export function status(v: unknown): Status {
  return typeof v === 'string' && STATUSES.has(v as Status) ? v as Status : 'unknown'
}

// ── Small entities ──────────────────────────────────────────────────────────

export function normTag(raw: unknown): Tag {
  const o = obj(raw)
  return { id: num(pick(o, 'id', 'ID')), name: str(pick(o, 'name', 'Name')), color: str(pick(o, 'color', 'Color')) }
}

export function normGroup(raw: unknown): Group {
  const o = obj(raw)
  return {
    id: num(pick(o, 'id', 'ID')),
    name: str(pick(o, 'name', 'Name')),
    sort: num(pick(o, 'sort', 'Sort')),
    description: str(pick(o, 'description', 'Description')),
  }
}

export function normOutage(raw: unknown): Outage {
  const o = obj(raw)
  const s = (v: unknown) => (typeof v === 'string' ? v : null)
  return {
    id: num(pick(o, 'id', 'ID')),
    monitorId: num(pick(o, 'monitorId', 'MonitorID')),
    startedAt: num(pick(o, 'startedAt', 'StartedAt')),
    endedAt: numOrNull(pick(o, 'endedAt', 'EndedAt')),
    firstReason: str(pick(o, 'firstReason', 'FirstReason', 'Reason')),
    errClass: str(pick(o, 'errClass', 'ErrClass')),
    ackedBy: s(pick(o, 'ackedBy', 'AckedBy')),
    ackedAt: numOrNull(pick(o, 'ackedAt', 'AckedAt')),
    note: s(pick(o, 'note', 'Note')),
    noteBy: s(pick(o, 'noteBy', 'NoteBy')),
    noteAt: numOrNull(pick(o, 'noteAt', 'NoteAt')),
    noteCount: num(pick(o, 'noteCount', 'NoteCount')),
    regions: o.regions && typeof o.regions === 'object' ? (o.regions as Outage['regions']) : null,
    suppressedBy: numOrNull(o.suppressedBy),
  }
}

export function normOutageNote(raw: unknown): OutageNote {
  const o = obj(raw)
  return {
    id: num(o.id), outageId: num(o.outageId), body: str(o.body), authorName: str(o.authorName),
    createdAt: num(o.createdAt), editedAt: numOrNull(o.editedAt), canEdit: o.canEdit === true,
  }
}

export function normOutageSummary(raw: unknown): OutageSummary {
  const o = obj(raw)
  return {
    active: num(pick(o, 'active', 'Active')),
    last30d: num(pick(o, 'last30d', 'Last30d')),
    needsNote: num(pick(o, 'needsNote', 'NeedsNote')),
    mttrSeconds: num(pick(o, 'mttrSeconds', 'MTTRSeconds')),
    mostAffectedMonitorId: numOrNull(pick(o, 'mostAffectedMonitorId', 'MostAffectedMonitorID')),
    mostAffectedCount: num(pick(o, 'mostAffectedCount', 'MostAffectedCount')),
  }
}

export function normHeartbeat(raw: unknown): Heartbeat {
  const o = obj(raw)
  return {
    monitorId: num(o.monitorId),
    ts: num(o.ts),
    status: status(o.status),
    latencyMs: num(o.latencyMs, -1),
    code: num(o.code),
    dnsMs: num(o.dnsMs, -1),
    tcpMs: num(o.tcpMs, -1),
    tlsMs: num(o.tlsMs, -1),
    ttfbMs: num(o.ttfbMs, -1),
    waitMs: num(o.waitMs, -1),
    appMs: num(o.appMs, -1),
    edge: str(o.edge),
    conn: str(o.conn),
    errClass: str(o.errClass),
    msg: str(o.msg),
    maint: Boolean(o.maint),
  }
}

export function normAlertLog(raw: unknown): AlertLogEntry {
  const o = obj(raw)
  return {
    id: num(pick(o, 'id', 'ID')),
    ts: num(pick(o, 'ts', 'TS', 'at')),
    channelId: numOrNull(pick(o, 'channelId', 'ChannelID')),
    monitorId: numOrNull(pick(o, 'monitorId', 'MonitorID')),
    event: str(pick(o, 'event', 'Event')),
    ok: Boolean(pick(o, 'ok', 'OK')),
    attempt: num(pick(o, 'attempt', 'Attempt')),
    error: str(pick(o, 'error', 'Error')),
  }
}

// ── Series & daily bars ─────────────────────────────────────────────────────

/**
 * Both chart sources: raw downsampled points ({ts, avg, min, max, up, down})
 * and hourly/daily rollups ({bucket, latSum, latCount, latMin, latMax, up,
 * degraded, pending, down}). A bucket without latency samples keeps NaN, so
 * the chart draws a gap there.
 */
export function normSeries(rows: unknown[]): SeriesPoint[] {
  const out: SeriesPoint[] = []
  for (const raw of rows) {
    const o = obj(raw)
    const bucket = pick(o, 'bucket', 'Bucket')
    if (bucket !== undefined) {
      const count = num(pick(o, 'latCount', 'LatCount'))
      const sum = num(pick(o, 'latSum', 'LatSum'))
      const good = num(pick(o, 'up', 'Up')) + num(pick(o, 'degraded', 'Degraded')) + num(pick(o, 'pending', 'Pending'))
      out.push({
        ts: num(bucket),
        avg: count > 0 ? sum / count : Number.NaN,
        min: count > 0 ? num(pick(o, 'latMin', 'LatMin'), Number.NaN) : Number.NaN,
        max: count > 0 ? num(pick(o, 'latMax', 'LatMax'), Number.NaN) : Number.NaN,
        up: good,
        down: num(pick(o, 'down', 'Down')),
      })
      continue
    }
    const avg = num(pick(o, 'avg', 'Avg'), Number.NaN)
    out.push({
      ts: num(pick(o, 'ts', 'TS')),
      avg: avg >= 0 ? avg : Number.NaN,
      min: num(pick(o, 'min', 'Min'), Number.NaN),
      max: num(pick(o, 'max', 'Max'), Number.NaN),
      up: num(pick(o, 'up', 'Up')),
      down: num(pick(o, 'down', 'Down')),
    })
  }
  return out.filter(p => p.ts > 0).sort((a, b) => a.ts - b.ts)
}

/** Daily rows: server calendar midnight as unix ms, or a YYYY-MM-DD string. */
export function normDaily(rows: unknown): DailyBar[] {
  return arr(rows).map(raw => {
    const o = obj(raw)
    const d = o.day
    const day = typeof d === 'number' ? d : serverDayStartMs(String(d)) ?? Number.NaN
    const outages = o.outages
    return {
      day,
      uptime: numOrNull(o.uptime),
      down: num(o.down),
      outages: typeof outages === 'number' ? outages : undefined,
      downMinutes: typeof o.downMinutes === 'number' ? o.downMinutes : undefined,
      incidents: Array.isArray(o.incidents)
        ? o.incidents.map(i => { const x = obj(i); return { id: num(x.id), title: str(x.title), impact: str(x.impact) } })
        : undefined,
    }
  }).filter(b => Number.isFinite(b.day)).sort((a, b) => a.day - b.day)
}

// ── Incidents, maintenance, pages ───────────────────────────────────────────

export function normIncident(raw: unknown): Incident {
  const o = obj(raw)
  const updates = arr(o.updates).map(u => {
    const x = obj(u)
    return {
      id: num(x.id), incidentId: num(x.incidentId), status: str(x.status, 'investigating'),
      body: str(x.body), createdBy: numOrNull(x.createdBy), createdByName: str(x.createdByName), createdAt: num(x.createdAt),
      internal: x.internal === true, editedAt: numOrNull(x.editedAt),
    } as IncidentUpdate
  })
  return {
    id: num(o.id), title: str(o.title), status: str(o.status, 'investigating') as Incident['status'], impact: str(o.impact),
    createdBy: numOrNull(o.createdBy), createdAt: num(o.createdAt), updatedAt: num(o.updatedAt),
    resolvedAt: numOrNull(o.resolvedAt), monitorIds: ids(o.monitorIds), pageIds: ids(o.pageIds), updates,
    severity: str(o.severity, 'minor'), commanderId: numOrNull(o.commanderId), commanderName: str(o.commanderName), postmortem: str(o.postmortem),
  }
}

export function normMaintenance(raw: unknown): Maintenance {
  const o = obj(raw)
  return {
    id: num(o.id), title: str(o.title), description: str(o.description),
    kind: o.kind === 'weekly' ? 'weekly' : 'once',
    startsAt: numOrNull(o.startsAt), endsAt: numOrNull(o.endsAt),
    weekdays: num(o.weekdays), startMinute: num(o.startMinute), durationMin: num(o.durationMin),
    tz: str(o.tz, serverTimeZone()), active: Boolean(o.active),
    monitorIds: ids(o.monitorIds), groupIds: ids(o.groupIds),
    createdAt: num(o.createdAt), updatedAt: num(o.updatedAt), createdBy: numOrNull(o.createdBy),
    state: typeof o.state === 'string' ? o.state as Maintenance['state'] : undefined,
    nextStart: numOrNull(o.nextStart), currentEnd: numOrNull(o.currentEnd),
  }
}

function normAlarm(o: Raw): AlarmConfig {
  const d = o.alarmDown, g = o.alarmDegraded
  return {
    muted: Boolean(o.alarmMuted),
    down: d === 'police-siren' || d === 'industrial-klaxon' || d === 'retro-panic' ? d : '',
    degraded: g === 'modern-chime' || g === 'tech-radar' ? g : '',
  }
}

export function normBranding(o: Record<string, unknown>): PageBranding {
  const days = num(o.historyDays)
  return {
    logoUrl: str(o.logoUrl), brandColor: str(o.brandColor), websiteUrl: str(o.websiteUrl), supportUrl: str(o.supportUrl),
    footerText: str(o.footerText), showResponseTime: Boolean(o.showResponseTime),
    historyDays: days === 30 || days === 60 ? days : 90,
    subscribeEmail: Boolean(o.subscribeEmail),
    subscribeSlack: Boolean(o.subscribeSlack),
  }
}

export function normPage(raw: unknown): Page {
  const o = obj(raw)
  return {
    id: num(o.id), slug: str(o.slug), title: str(o.title), description: str(o.description),
    visibility: o.visibility === 'private' ? 'private' : 'public',
    layout: o.layout === 'board' ? 'board' : 'list',
    showLatency: Boolean(o.showLatency),
    alarm: normAlarm(o),
    branding: normBranding(o),
    subscriptions: { enabled: Boolean(o.subscriptionsEnabled), channelId: numOrNull(o.subscriberChannelId) },
    items: arr(o.items).map(it => {
      const x = obj(it)
      return { sort: num(x.sort), groupId: numOrNull(x.groupId), monitorId: numOrNull(x.monitorId), displayName: str(x.displayName), description: str(x.description) }
    }).sort((a, b) => a.sort - b.sort),
    createdAt: num(o.createdAt), updatedAt: num(o.updatedAt),
  }
}

export function normPublicMonitor(raw: unknown): PublicMonitor {
  const o = obj(raw)
  return {
    id: num(o.id), name: str(o.name), groupId: numOrNull(o.groupId), status: status(o.status),
    since: num(o.since), uptime24h: numOrNull(o.uptime24h), uptime30d: numOrNull(o.uptime30d), beats: str(o.beats),
    latencyMs: typeof o.latencyMs === 'number' ? o.latencyMs : undefined,
    reason: typeof o.reason === 'string' ? o.reason : undefined,
  }
}

const OVERALL = new Set<OverallStatus>(['operational', 'degraded', 'partial', 'major', 'maintenance'])

export function normPublicPage(raw: unknown): PublicPage {
  const o = obj(raw)
  const p = obj(o.page)
  const daily = new Map<number, DailyBar[]>()
  for (const [id, rows] of Object.entries(obj(o.daily))) daily.set(Number(id), normDaily(rows))
  const inc = obj(o.incidents)
  const mt = obj(o.maintenance)
  const descriptions = new Map<number, string>()
  for (const [id, text] of Object.entries(obj(o.monitorDescriptions))) if (typeof text === 'string' && text) descriptions.set(Number(id), text)
  const uptime = new Map<number, number>()
  for (const [id, v] of Object.entries(obj(o.uptime))) if (typeof v === 'number') uptime.set(Number(id), v)
  return {
    page: {
      title: str(p.title), description: str(p.description),
      layout: p.layout === 'board' ? 'board' : 'list',
      visibility: p.visibility === 'private' ? 'private' : 'public',
      showLatency: Boolean(p.showLatency),
      alarm: normAlarm(p),
      branding: normBranding(p),
    },
    groups: arr(o.groups).map(g => {
      const x = obj(g)
      return { id: num(x.id), name: str(x.name), description: str(x.description), monitorIds: ids(x.monitorIds) }
    }),
    monitorDescriptions: descriptions,
    monitors: arr(o.monitors).map(normPublicMonitor),
    daily,
    uptime,
    incidents: { active: arr(inc.active).map(normIncident), past: arr(inc.past).map(normIncident) },
    maintenance: { active: arr(mt.active).map(normMaintenance), upcoming: arr(mt.upcoming).map(normMaintenance) },
    overall: OVERALL.has(o.overall as OverallStatus) ? o.overall as OverallStatus : 'operational',
  }
}

// ── System ──────────────────────────────────────────────────────────────────

export function normSystem(raw: unknown): SystemInfo {
  const o = obj(raw)
  const c = obj(o.connectivity)
  const b = obj(o.backups)
  return {
    version: str(o.version, '—'), startedAt: num(o.startedAt), monitors: num(o.monitors),
    lagP50Ms: num(o.lagP50Ms), lagP99Ms: num(o.lagP99Ms), inFlight: num(o.inFlight), writerDropped: num(o.writerDropped),
    connectivity: { online: c.online !== false, since: num(c.since) },
    backups: {
      dir: str(b.dir),
      files: arr(b.files).map(f => {
        const x = obj(f)
        return { name: str(x.name), size: num(x.size), modTime: num(x.modTime) }
      }),
    },
  }
}

// ── Check result ────────────────────────────────────────────────────────────

export function normResult(raw: unknown): Result {
  const o = obj(raw)
  const t = pick(o, 'timing', 'Timing')
  const c = pick(o, 'cert', 'Cert')
  const tt = obj(t)
  const timing: Timing | null = t && typeof t === 'object'
    ? {
      dnsMs: num(pick(tt, 'dnsMs', 'DNSMs'), -1),
      tcpMs: num(pick(tt, 'tcpMs', 'TCPMs'), -1),
      tlsMs: num(pick(tt, 'tlsMs', 'TLSMs'), -1),
      ttfbMs: num(pick(tt, 'ttfbMs', 'TTFBMs'), -1),
    }
    : null
  const cc = obj(c)
  const notAfter = c && typeof c === 'object' ? time(pick(cc, 'notAfter', 'NotAfter')) : null
  const cert: CertInfo | null = notAfter != null
    ? { notAfter, issuer: str(pick(cc, 'issuer', 'Issuer')), subject: str(pick(cc, 'subject', 'Subject')) }
    : null
  return {
    at: time(pick(o, 'at', 'At')) ?? Date.now(),
    ok: Boolean(pick(o, 'ok', 'OK', 'Ok')),
    degraded: Boolean(pick(o, 'degraded', 'Degraded')),
    latencyMs: num(pick(o, 'latencyMs', 'LatencyMs'), -1),
    code: num(pick(o, 'code', 'Code')),
    timing,
    reason: str(pick(o, 'reason', 'Reason')),
    errClass: str(pick(o, 'errClass', 'ErrClass')) as ErrClass,
    cert,
    lossPct: num(pick(o, 'lossPct', 'LossPct')),
  }
}
