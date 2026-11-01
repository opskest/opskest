// Types and pure helpers for Settings -> Updates and the Overview banner.
// Nothing here touches the DOM, the network or React, so it is unit-tested
// directly (web/tests/updates.test.mjs).

export type InstallMethod = 'binary' | 'docker' | 'other'
export type UpdateMode = 'off' | 'notify' | 'auto'
export type UpdatePhase = 'idle' | 'checking' | 'downloading' | 'verifying' | 'backing_up' | 'installing' | 'restarting' | 'failed'

export interface UpdateAsset { os: string; arch: string; size: number }

export interface UpdateSettings {
  enabled: boolean
  feedUrl: string
  mode: UpdateMode
  autoHour: number
  publicKeySet: boolean
  /** Where the effective release key comes from: "settings" (entered here), "build" (built into the binary) or "". */
  publicKeySource: '' | 'settings' | 'build'
  /** Short fingerprint of the effective key, "" when there is none. */
  publicKeyFingerprint: string
}

export interface UpdateCheck {
  checkedAt: number | null
  error: string
  latest: string
  available: boolean
  notes: string
  publishedAt: number | null
  signatureVerified: boolean
  asset: UpdateAsset | null
  /** The oldest version that may update straight to `latest`, "" when there is no floor. */
  minVersion: string
  /** The running version is older than minVersion: an intermediate release is needed first. */
  minVersionBlocked: boolean
}

export interface UpdateState {
  phase: UpdatePhase
  percent: number
  error: string
  /** The database backup taken before the install, "" until one exists. */
  backupName: string
}

export type RestartMode = 'self' | 'exit'

/** GET /api/updates (and the body of PUT settings and POST check). */
export interface UpdatesPayload {
  current: string
  installMethod: InstallMethod
  canInstall: boolean
  cannotInstallReason?: string
  settings: UpdateSettings
  check: UpdateCheck
  state: UpdateState
  canRollback: boolean
  /** The version a rollback returns to, "" when there is none. */
  previousVersion: string
  dockerHint?: string
  /** "self": Opskest starts the new version itself. "exit": it shuts down and a service manager must start it. */
  restartMode: RestartMode
  /** A sentence for the restarting screen. */
  restartHint: string
  /** When the next automatic check is due (unix ms), null when none is scheduled. */
  nextCheckAt: number | null
  /** A version automatic installs skip (it was rolled back from), "" for none. */
  autoSkipVersion: string
  /** When the last install failed (unix ms), null for never. */
  lastInstallFailedAt: number | null
}

/** PUT /api/updates/settings. `publicKey`: null keeps the stored key, "" clears it. */
export interface UpdateSettingsIn {
  enabled: boolean
  feedUrl: string
  mode: UpdateMode
  autoHour: number
  publicKey: string | null
}

// ── Phases ──────────────────────────────────────────────────────────────────

const PHASE_LABELS: Record<string, string> = {
  idle: 'Idle',
  checking: 'Checking for updates',
  downloading: 'Downloading',
  verifying: 'Verifying the download',
  backing_up: 'Backing up the database',
  installing: 'Installing',
  restarting: 'Restarting',
  failed: 'Update failed',
}

/** Plain-words label for a server phase; unknown phases never show raw identifiers. */
export function phaseLabel(phase: string | null | undefined): string {
  return (phase && PHASE_LABELS[phase]) || 'Working'
}

/** True while the server is doing something and the UI should keep polling. */
export function isActivePhase(phase: string | null | undefined): boolean {
  return !!phase && phase !== 'idle' && phase !== 'failed'
}

// ── Install steps ───────────────────────────────────────────────────────────

/** The visible steps of an install, in order, keyed by the server phase that runs each. */
export const INSTALL_STEPS: { phase: UpdatePhase; label: string }[] = [
  { phase: 'backing_up', label: 'Back up database' },
  { phase: 'downloading', label: 'Download' },
  { phase: 'verifying', label: 'Verify checksum and signature' },
  { phase: 'installing', label: 'Install' },
  { phase: 'restarting', label: 'Restart' },
]

export type StepState = 'done' | 'current' | 'pending'

/**
 * Where each install step stands for a server phase. Phases before the first
 * step (idle, checking, "starting") leave everything pending; `failed` has
 * no known step, so nothing is highlighted.
 */
export function stepStates(phase: string | null | undefined): StepState[] {
  const at = INSTALL_STEPS.findIndex(s => s.phase === phase)
  return INSTALL_STEPS.map((_, i) => (at < 0 ? 'pending' : i < at ? 'done' : i === at ? 'current' : 'pending'))
}

/** Whether a feed address is acceptable in the form: https anywhere, http only for loopback. */
export function validFeedUrl(raw: string): boolean {
  let u: URL
  try { u = new URL(raw) } catch { return false }
  if (u.protocol === 'https:') return !!u.hostname
  if (u.protocol !== 'http:') return false
  const h = u.hostname.toLowerCase()
  return h === 'localhost' || h.endsWith('.localhost') || h === '[::1]' || h === '::1' || /^127(\.\d{1,3}){3}$/.test(h)
}

const EMPTY_CHECK: UpdateCheck = {
  checkedAt: null, error: '', latest: '', available: false, notes: '', publishedAt: null, signatureVerified: false, asset: null, minVersion: '', minVersionBlocked: false,
}
const EMPTY_STATE: UpdateState = { phase: 'idle', percent: 0, error: '', backupName: '' }
const EMPTY_SETTINGS: UpdateSettings = {
  enabled: false, feedUrl: '', mode: 'off', autoHour: 3, publicKeySet: false, publicKeySource: '', publicKeyFingerprint: '',
}

/** Fills anything the server left out, so UI code never meets undefined. */
export function normalizePayload(p: UpdatesPayload): UpdatesPayload {
  return {
    ...p,
    check: { ...EMPTY_CHECK, ...(p.check as Partial<UpdateCheck> | undefined) },
    state: { ...EMPTY_STATE, ...(p.state as Partial<UpdateState> | undefined) },
    settings: { ...EMPTY_SETTINGS, ...(p.settings as Partial<UpdateSettings> | undefined) },
    previousVersion: typeof p.previousVersion === 'string' ? p.previousVersion : '',
    restartMode: p.restartMode === 'exit' ? 'exit' : 'self',
    restartHint: typeof p.restartHint === 'string' ? p.restartHint : '',
    nextCheckAt: typeof p.nextCheckAt === 'number' && p.nextCheckAt > 0 ? p.nextCheckAt : null,
    autoSkipVersion: typeof p.autoSkipVersion === 'string' ? p.autoSkipVersion : '',
    lastInstallFailedAt: typeof p.lastInstallFailedAt === 'number' && p.lastInstallFailedAt > 0 ? p.lastInstallFailedAt : null,
  }
}

// ── Versions ────────────────────────────────────────────────────────────────

interface Semver { core: [number, number, number]; pre: string[] }

const SEMVER = /^v?(\d+)(?:\.(\d+))?(?:\.(\d+))?(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$/

function parseVersion(v: string | null | undefined): Semver | null {
  if (typeof v !== 'string') return null
  const m = SEMVER.exec(v.trim())
  if (!m) return null
  return {
    core: [Number(m[1]), Number(m[2] ?? 0), Number(m[3] ?? 0)],
    pre: m[4] ? m[4].split('.') : [],
  }
}

/** A build without a release version ("dev", "", "(devel)", a git hash): updates are disabled. */
export function isDevVersion(v: string | null | undefined): boolean {
  return parseVersion(v) === null
}

/** Whether `v` is a version number compareVersions understands. */
export function isVersion(v: string | null | undefined): boolean {
  return parseVersion(v) !== null
}

function comparePre(a: string[], b: string[]): number {
  // A release outranks any of its pre-releases.
  if (a.length === 0 && b.length === 0) return 0
  if (a.length === 0) return 1
  if (b.length === 0) return -1
  const n = Math.min(a.length, b.length)
  for (let i = 0; i < n; i++) {
    const x = a[i]
    const y = b[i]
    const xn = /^\d+$/.test(x)
    const yn = /^\d+$/.test(y)
    if (xn && yn) {
      const d = Number(x) - Number(y)
      if (d !== 0) return d < 0 ? -1 : 1
    } else if (xn !== yn) {
      return xn ? -1 : 1 // numeric identifiers sort before alphanumeric ones
    } else if (x !== y) {
      return x < y ? -1 : 1
    }
  }
  return a.length === b.length ? 0 : a.length < b.length ? -1 : 1
}

/**
 * Semver precedence: -1, 0 or 1. Accepts a leading "v", missing minor/patch
 * and build metadata (ignored). A pre-release sorts below its release; an
 * unparsable value sorts below every real version (and equal to another).
 */
export function compareVersions(a: string, b: string): number {
  const x = parseVersion(a)
  const y = parseVersion(b)
  if (!x && !y) return 0
  if (!x) return -1
  if (!y) return 1
  for (let i = 0; i < 3; i++) {
    if (x.core[i] !== y.core[i]) return x.core[i] < y.core[i] ? -1 : 1
  }
  return comparePre(x.pre, y.pre)
}

/** "v1.2.3" and "1.2.3" are the same version when showing or comparing identity. */
export function bareVersion(v: string): string {
  return v.trim().replace(/^v(?=\d)/, '')
}

/** Whether the running version differs from the one an update started on. */
export function versionChanged(start: string, now: string): boolean {
  if (!now) return false
  if (isDevVersion(start) || isDevVersion(now)) return bareVersion(start) !== bareVersion(now)
  return compareVersions(start, now) !== 0
}

// ── Banner / panel decisions ────────────────────────────────────────────────

/** Whether the Overview banner shows, given what the server reported and what was dismissed. */
export function shouldShowBanner(p: UpdatesPayload | null | undefined, dismissed: string | null): boolean {
  if (!p || !p.check || !p.settings) return false
  if (!p.settings.enabled || p.settings.mode === 'off') return false
  if (isDevVersion(p.current)) return false
  if (!p.check.available || !p.check.latest) return false
  if (isVersion(p.check.latest) && compareVersions(p.check.latest, p.current) <= 0) return false
  if (dismissed && bareVersion(dismissed) === bareVersion(p.check.latest)) return false
  return true
}

// ── Waiting for a restart ───────────────────────────────────────────────────

/** How long failed requests are expected while the server restarts. */
export const RESTART_TIMEOUT_MS = 90_000
/** After accepting an install the state may briefly still read idle. */
export const IDLE_GRACE_MS = 6_000

export interface WaitCtx {
  startVersion: string
  /** An active phase has been seen since the request was accepted. */
  sawActive: boolean
  /** When requests first started failing (server gone), or null. */
  lostAt: number | null
  startedAt: number
  now: number
}
export type PollResult = { ok: true; current: string; phase: string } | { ok: false }
export type PollAction = 'continue' | 'reload' | 'failed' | 'stalled' | 'timeout'

/**
 * What to do after one poll of GET /api/updates during an install or rollback:
 * keep polling, reload into the new version, or stop with a failure, a
 * "finished but the version did not change" notice, or a timeout.
 */
export function pollAction(ctx: WaitCtx, poll: PollResult): PollAction {
  if (!poll.ok) {
    const lost = ctx.lostAt ?? ctx.now
    return ctx.now - lost > RESTART_TIMEOUT_MS ? 'timeout' : 'continue'
  }
  if (versionChanged(ctx.startVersion, poll.current)) return 'reload'
  if (poll.phase === 'failed') return 'failed'
  if (poll.phase === 'idle') {
    if (!ctx.sawActive && ctx.now - ctx.startedAt < IDLE_GRACE_MS) return 'continue'
    return 'stalled'
  }
  return 'continue'
}

// ── Sizes ───────────────────────────────────────────────────────────────────

/** 1024-based, one decimal from KB up: "512 B", "1.5 KB", "12.3 MB". Invalid input is "-". */
export function formatBytes(n: number | null | undefined): string {
  if (n == null || !Number.isFinite(n) || n < 0) return '-'
  if (n < 1024) return `${Math.round(n)} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024
  let i = 0
  while (i < units.length - 1 && Number(v.toFixed(1)) >= 1024) { v /= 1024; i++ }
  return `${v.toFixed(1)} ${units[i]}`
}

// ── Release notes (a deliberately tiny, safe markdown subset) ───────────────

export type Inline =
  | { kind: 'text'; text: string }
  | { kind: 'bold'; text: string }
  | { kind: 'code'; text: string }
  | { kind: 'link'; text: string; url: string }

export type Block =
  | { kind: 'heading'; level: number; inlines: Inline[] }
  | { kind: 'paragraph'; inlines: Inline[] }
  | { kind: 'list'; ordered: boolean; items: Inline[][] }
  | { kind: 'code'; text: string }

const INLINE = /\*\*(.+?)\*\*|__(.+?)__|`([^`]+)`|\[([^\]]+)\]\(([^)\s]+)\)/g

/** Bold, inline code and links; everything else (including HTML) stays literal text. */
export function parseInline(src: string): Inline[] {
  const out: Inline[] = []
  let last = 0
  INLINE.lastIndex = 0
  let m: RegExpExecArray | null
  while ((m = INLINE.exec(src)) !== null) {
    if (m.index > last) out.push({ kind: 'text', text: src.slice(last, m.index) })
    if (m[1] !== undefined || m[2] !== undefined) out.push({ kind: 'bold', text: m[1] ?? m[2] })
    else if (m[3] !== undefined) out.push({ kind: 'code', text: m[3] })
    else out.push({ kind: 'link', text: m[4], url: m[5] })
    last = m.index + m[0].length
  }
  if (last < src.length) out.push({ kind: 'text', text: src.slice(last) })
  return out
}

/** A link as the plain text it is shown as: "label (url)", or just the url when they match. */
export function linkText(l: { text: string; url: string }): string {
  return l.text === l.url ? l.url : `${l.text} (${l.url})`
}

const MAX_NOTES_CHARS = 50_000
const BULLET = /^\s*[-*+]\s+(.*)$/
const ORDERED = /^\s*\d{1,3}[.)]\s+(.*)$/
const HEADING = /^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$/
const RULE = /^\s{0,3}([-*_])(?:\s*\1){2,}\s*$/

/** Markdown release notes to a flat list of blocks: headings, paragraphs, lists and code. */
export function parseNotes(markdown: string | null | undefined): Block[] {
  if (typeof markdown !== 'string' || markdown.trim() === '') return []
  const lines = markdown.slice(0, MAX_NOTES_CHARS).replace(/\r\n?/g, '\n').split('\n')
  const blocks: Block[] = []
  let para: string[] = []
  let list: { ordered: boolean; items: string[] } | null = null
  let code: string[] | null = null

  const flushPara = () => {
    if (para.length) blocks.push({ kind: 'paragraph', inlines: parseInline(para.join(' ')) })
    para = []
  }
  const flushList = () => {
    if (list) blocks.push({ kind: 'list', ordered: list.ordered, items: list.items.map(parseInline) })
    list = null
  }

  for (const raw of lines) {
    if (code) {
      if (/^\s*```/.test(raw)) { blocks.push({ kind: 'code', text: code.join('\n') }); code = null } else code.push(raw)
      continue
    }
    if (/^\s*```/.test(raw)) { flushPara(); flushList(); code = []; continue }
    if (raw.trim() === '') { flushPara(); flushList(); continue }

    const h = HEADING.exec(raw)
    if (h) { flushPara(); flushList(); blocks.push({ kind: 'heading', level: h[1].length, inlines: parseInline(h[2]) }); continue }
    if (RULE.test(raw)) { flushPara(); flushList(); continue }

    const b = BULLET.exec(raw)
    const o = b ? null : ORDERED.exec(raw)
    if (b || o) {
      const ordered = !!o
      flushPara()
      if (list && list.ordered !== ordered) flushList()
      if (!list) list = { ordered, items: [] }
      list.items.push((b ?? o)![1])
      continue
    }
    if (list && /^\s{2,}\S/.test(raw)) { list.items[list.items.length - 1] += ' ' + raw.trim(); continue }

    flushList()
    para.push(raw.replace(/^\s*>\s?/, '').trim())
  }
  if (code) blocks.push({ kind: 'code', text: code.join('\n') })
  flushPara()
  flushList()
  return blocks
}
