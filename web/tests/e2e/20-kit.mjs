// Shared helpers for the monitoring-correctness suites (20-*, 21-*, 22-*).
//
// These suites prove that the MONITORING ITSELF is right (states, retries, alerts, outages, uptime numbers,
// scheduling, persistence), not the screens. They need no browser: a real `opskest serve` process is started on a
// free loopback port with a fresh data directory, the first administrator is created through the real
// /api/auth/setup call, and everything is driven and observed through the real HTTP API. The "world" is a set of
// local node:http / node:net fixtures whose behaviour a test flips while the monitor is running, plus a local
// alert receiver.
//
// Alert receiver: channel types slack/teams/gchat/webhook only accept https:// URLs, and the dispatcher's HTTP
// client trusts only the system roots, so a local TLS receiver is not possible. Gotify is delivered over plain
// http (self-hosted tools are allowed to be) and is never grouped into storm messages, so a `gotify` channel
// pointing at a local node:http server receives exactly one request per alert, with the same title/body text
// every other channel renders. The receiver records what arrived and when; /api/alert-log is used as a second,
// independent witness.

import { spawn } from 'node:child_process'
import { mkdirSync, mkdtempSync } from 'node:fs'
import http from 'node:http'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'
import { ensureBinary, randomPassword } from './harness.mjs'
import { freePort, killTree, removeDir } from './cdp.mjs'
import { monitorBody } from './util.mjs'

export { monitorBody }
export const sleep = ms => new Promise(r => setTimeout(r, ms))
let uid = 0
export const uniq = prefix => `${prefix}-${(++uid).toString(36)}${Math.random().toString(36).slice(2, 6)}`

// ── polling ─────────────────────────────────────────────────────────────────

/** Polls fn until it returns a truthy value; throws a message naming what was awaited (plus diag() output). */
export async function until(what, fn, { timeout = 30000, every = 150, diag } = {}) {
  const t0 = Date.now()
  let last
  for (;;) {
    last = await fn()
    if (last) return last
    if (Date.now() - t0 > timeout) {
      let extra = ''
      try { extra = diag ? `\n--- state ---\n${await diag()}` : '' } catch (e) { extra = `\n(diag failed: ${e.message})` }
      throw new Error(`timed out after ${((Date.now() - t0) / 1000).toFixed(1)}s waiting for: ${what}${extra}`)
    }
    await sleep(every)
  }
}

/** Fails (rejects) as soon as fn() returns a truthy complaint while sleeping `ms`; used for "nothing happens" proofs. */
export async function holds(what, ms, fn, every = 200) {
  const t0 = Date.now()
  while (Date.now() - t0 < ms) {
    const bad = await fn()
    if (bad) throw new Error(`${what}: ${typeof bad === 'string' ? bad : JSON.stringify(bad)} (after ${Date.now() - t0} ms)`)
    await sleep(every)
  }
  const bad = await fn()
  if (bad) throw new Error(`${what}: ${typeof bad === 'string' ? bad : JSON.stringify(bad)}`)
}

export const fmt = v => JSON.stringify(v, null, 1)

// ── the app under test ──────────────────────────────────────────────────────

export class Instance {
  /** Starts a fresh instance (new data dir) and creates the first administrator through the real setup call. */
  static async start({ env = {} } = {}) {
    const inst = new Instance(env)
    inst.dir = mkdtempSync(path.join(os.tmpdir(), 'opskest-mon-'))
    mkdirSync(path.join(inst.dir, 'data'))
    mkdirSync(path.join(inst.dir, 'backups'))
    inst.port = await freePort()
    await inst.boot()
    await inst.setup()
    return inst
  }

  constructor(env) {
    this.extraEnv = env
    this.user = 'mon_admin'
    this.password = randomPassword()
    this.cookies = []
    this.exited = null
    this.output = ''
    this._ml = null
  }

  get base() { return `http://127.0.0.1:${this.port}` }
  get origin() { return `http://localhost:${this.port}` }
  logs() { return this.output }

  async boot() {
    const bin = await ensureBinary()
    const keep = ['PATH', 'Path', 'SystemRoot', 'SYSTEMROOT', 'TEMP', 'TMP', 'USERPROFILE', 'HOME', 'COMSPEC', 'windir']
    const env = Object.fromEntries(keep.filter(k => process.env[k] !== undefined).map(k => [k, process.env[k]]))
    Object.assign(env, {
      PORT: String(this.port), LISTEN_ADDR: `127.0.0.1:${this.port}`, APP_URL: this.origin,
      DATA_DIR: path.join(this.dir, 'data'), BACKUP_DIR: path.join(this.dir, 'backups'), LOG_LEVEL: 'info', DISPLAY_TZ: 'UTC',
      ...this.extraEnv,
    })
    this.exited = null
    const child = spawn(bin, ['serve'], { cwd: this.dir, env, stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true })
    this.child = child
    const log = d => { this.output += d.toString(); if (this.output.length > 400000) this.output = this.output.slice(-200000) }
    child.stdout.on('data', log)
    child.stderr.on('data', log)
    child.on('exit', code => { if (this.child === child) this.exited = code ?? -1 })
    process.once('exit', () => killTree(child.pid))
    const deadline = Date.now() + 30000
    for (;;) {
      if (this.exited !== null) throw new Error(`the server exited early with code ${this.exited}:\n${this.output.slice(-1500)}`)
      try { if ((await fetch(`${this.base}/healthz`)).ok) return } catch { /* not up yet */ }
      if (Date.now() > deadline) throw new Error(`the server did not answer /healthz within 30s:\n${this.output.slice(-1500)}`)
      await sleep(100)
    }
  }

  async setup() {
    const res = await fetch(`${this.base}/api/auth/setup`, {
      method: 'POST', headers: { 'Content-Type': 'application/json', Origin: this.origin },
      body: JSON.stringify({ username: this.user, name: 'Monitoring Admin', password: this.password }),
    })
    if (!res.ok) throw new Error(`setup failed: ${res.status} ${await res.text()}`)
    this.cookies = res.headers.getSetCookie().map(c => c.split(';')[0]).map(kv => { const i = kv.indexOf('='); return { name: kv.slice(0, i), value: kv.slice(i + 1) } })
    if (!this.cookies.some(c => c.name === 'opskest.sid')) throw new Error('setup did not set the session cookie')
  }

  async api(method, urlPath, body) {
    const headers = { Origin: this.origin, Cookie: this.cookies.map(c => `${c.name}=${c.value}`).join('; ') }
    const csrf = this.cookies.find(c => c.name === 'opskest.csrf')
    if (csrf && method !== 'GET') headers['X-CSRF-Token'] = decodeURIComponent(csrf.value)
    if (body !== undefined) headers['Content-Type'] = 'application/json'
    const res = await fetch(`${this.base}${urlPath}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
    const text = await res.text()
    let json = null
    try { json = JSON.parse(text) } catch { /* not JSON */ }
    return { status: res.status, ok: res.ok, json, text }
  }

  async ok(method, urlPath, body) {
    const r = await this.api(method, urlPath, body)
    if (!r.ok) throw new Error(`${method} ${urlPath} -> ${r.status}: ${r.text.slice(0, 400)}`)
    return r.json
  }

  /** Kills the process. With graceful=true (POSIX) it asks politely (SIGTERM) and waits for the writer to drain. */
  async kill({ graceful = false } = {}) {
    const child = this.child
    if (!child) return
    if (graceful && process.platform !== 'win32') child.kill('SIGTERM')
    else killTree(child.pid)
    for (let i = 0; i < 200 && this.exited === null; i++) await sleep(50)
    this.child = null
  }

  async restart(opts) {
    await this.kill(opts)
    this._ml = null
    await this.boot()
  }

  async stop() {
    await this.kill()
    await removeDir(this.dir)
  }

  // ── reads (cached for 120 ms, so many concurrent pollers cost one request) ──
  async monitors() {
    const now = Date.now()
    if (!this._ml || now - this._mlAt > 120) { this._mlAt = now; this._ml = this.ok('GET', '/api/monitors').then(j => j.monitors) }
    return this._ml
  }
  async mon(id) { return (await this.monitors()).find(m => m.id === id) }
  /** Like mon() but bypasses the 120 ms read cache (use right after a write you want to observe). */
  async fresh(id) { this._mlAt = 0; return this.mon(id) }
  /** Stored heartbeats of a monitor, oldest first (the writer commits once a second). */
  async beats(id, limit = 200) { return (await this.ok('GET', `/api/monitors/${id}/beats?limit=${limit}`)).beats.slice().reverse() }
  /** Outages of a monitor, oldest first. */
  async outages(id) { return (await this.ok('GET', `/api/monitors/${id}/outages?limit=200`)).outages.slice().reverse() }
  async alertLog(id) {
    const j = await this.ok('GET', '/api/alert-log?limit=200')
    return j.entries.filter(e => e.monitorId === id).slice().reverse()
  }

  /**
   * Creates a monitor. A monitor with a very long interval (>= 3600 s) is one a test drives by hand with checkNow(),
   * so it is created paused and resumed: the server checks a freshly created monitor within seconds (see
   * engine.UpsertMonitor), whereas a resumed one waits for its jittered first run, which for 3600 s is "never" here.
   * Pass { quiet: false } to see the real first-check behaviour.
   */
  async create(over, { quiet = (over.intervalS ?? 0) >= 3600 && !over.paused } = {}) {
    const body = monitorBody({ intervalS: 10, retryIntervalS: 10, timeoutMs: 2000, ...over })
    if (!quiet) return this.ok('POST', '/api/monitors', body)
    const mon = await this.ok('POST', '/api/monitors', { ...body, paused: true })
    await this.ok('POST', `/api/monitors/${mon.id}/resume`)
    return { ...mon, paused: false }
  }
  async edit(id, over) {
    const cur = await this.ok('GET', `/api/monitors/${id}`)
    const body = monitorBody({
      name: cur.name, type: cur.type, target: cur.target, channelIds: cur.channelIds, intervalS: cur.intervalS, retryIntervalS: cur.retryIntervalS,
      retries: cur.retries, timeoutMs: cur.timeoutMs, degradedMs: cur.degradedMs, sslWarnDays: cur.sslWarnDays, reminderMin: cur.reminderMin,
      upsideDown: cur.upsideDown, notifyDegraded: cur.notifyDegraded, paused: cur.paused, settings: { ...cur.settings, headers: undefined },
    })
    const merged = { ...body.settings, ...(over.settings ?? {}) }
    Object.assign(body, over)
    body.settings = merged
    return this.ok('PUT', `/api/monitors/${id}`, body)
  }

  /** Runs one check right now and waits until its result is applied (the monitor's lastCheckAt moves). */
  async checkNow(id) {
    this._mlAt = 0
    const before = (await this.mon(id))?.lastCheckAt ?? 0
    await this.ok('POST', `/api/monitors/${id}/check`)
    return until(`monitor ${id} to apply a new check result`, async () => {
      this._mlAt = 0
      const m = await this.mon(id)
      return m && m.lastCheckAt > before ? m : null
    }, { timeout: 15000, every: 60 })
  }

  async channel(rx, key) {
    return this.ok('POST', '/api/channels', { name: `rx-${key}`, type: 'gotify', config: { url: `${rx.base}/${key}`, apiKey: 'test-token' } })
  }
}

// ── the monitored world ─────────────────────────────────────────────────────

/**
 * A controllable HTTP target. set() changes what every following request gets; script(n, req) may decide per
 * request (n = how many requests came before). close() really closes the listening port (connection refused),
 * open() listens on the same port again.
 */
export class Fixture {
  static async start() { const f = new Fixture(); await f.open(0); return f }
  constructor() {
    this.hits = []
    this.cur = { status: 200, body: 'OK from the fixture', headers: { 'Content-Type': 'text/plain' }, delay: 0 }
    this.scriptFn = null
    this.sockets = new Set()
    this.server = null
  }
  get port() { return this._port }
  url(p = '/') { return `http://127.0.0.1:${this._port}${p}` }
  get target() { return this.url('/health') }
  set(over) { this.cur = { status: 200, body: 'OK from the fixture', headers: { 'Content-Type': 'text/plain' }, delay: 0, ...over }; return this }
  up() { return this.set({}) }
  fail(status = 500) { return this.set({ status, body: 'boom' }) }
  slow(delay, over = {}) { return this.set({ delay, ...over }) }
  script(fn) { this.scriptFn = fn; return this }
  hitsSince(t) { return this.hits.filter(h => h.t >= t) }

  open(port = this._port ?? 0) {
    return new Promise((resolve, reject) => {
      const server = http.createServer((req, res) => {
        const n = this.hits.length
        const spec = { ...this.cur, ...(this.scriptFn?.(n, req) ?? {}) }
        const hit = { n, t: Date.now(), url: req.url, method: req.method, headers: req.headers, body: '', status: spec.status, delay: spec.delay }
        this.hits.push(hit)
        req.on('data', d => { hit.body += d })
        const reply = () => { if (!res.destroyed) { res.writeHead(spec.status, spec.headers); res.end(spec.body) } }
        if (spec.delay > 0) setTimeout(reply, spec.delay)
        else reply()
      })
      server.on('connection', s => { this.sockets.add(s); s.on('close', () => this.sockets.delete(s)) })
      server.once('error', reject)
      server.listen(port, '127.0.0.1', () => { this._port = server.address().port; this.server = server; resolve() })
    })
  }
  async close() {
    const s = this.server
    this.server = null
    for (const sock of this.sockets) sock.destroy()
    if (s) await new Promise(r => s.close(r))
  }
  async stop() { await this.close() }
}

/** A local gotify-compatible alert receiver. Everything that arrives is kept with the arrival time. */
export class Receiver {
  static async start() {
    const rx = new Receiver()
    rx.all = []
    rx.failing = new Set() // channel keys whose deliveries are answered with HTTP 500
    rx.refused = []
    rx.server = http.createServer((req, res) => {
      let raw = ''
      req.on('data', d => { raw += d })
      req.on('end', () => {
        const m = /^\/([^/]+)\/message$/.exec(req.url.split('?')[0])
        let j = {}
        try { j = JSON.parse(raw) } catch { /* ignore */ }
        if (!m || req.method !== 'POST') { res.writeHead(404); res.end(); return }
        if (rx.failing.has(m[1])) { rx.refused.push({ t: Date.now(), key: m[1] }); res.writeHead(500, { 'Content-Type': 'text/plain' }); res.end('receiver is down for the test'); return }
        const title = String(j.title ?? '')
        const kind = classify(title)
        rx.all.push({ t: Date.now(), key: m[1], title, message: String(j.message ?? ''), kind, key_hdr: req.headers['x-gotify-key'] })
        res.writeHead(200, { 'Content-Type': 'application/json' })
        res.end('{"id":1}')
      })
    })
    await new Promise(r => rx.server.listen(0, '127.0.0.1', r))
    rx.base = `http://127.0.0.1:${rx.server.address().port}`
    return rx
  }
  /** Messages for a channel key (one per scenario), optionally only for monitors whose name appears in the title. */
  got(key, name) { return this.all.filter(m => m.key === key && (!name || m.title.includes(name))) }
  kinds(key, name) { return this.got(key, name).map(m => m.kind) }
  async stop() { this.server.closeAllConnections?.(); await new Promise(r => this.server.close(r)) }
}

/** Title -> event kind, from the rendered headline (alert.Title in internal/alert/message.go). */
export function classify(title) {
  const t = title.replace(/^[^\p{L}]+/u, '')
  if (/^Still down/.test(t)) return 'reminder'
  if (/^Down\b/.test(t)) return 'down'
  if (/^Up\b/.test(t)) return 'recovered'
  if (/^Degraded\b/.test(t)) return 'degraded'
  if (/^Back to normal\b/.test(t)) return 'degraded_cleared'
  return `other:${t.split(' — ')[0]}`
}

/** "(was down 3m)" -> 3, "(was down 45s)" -> 0.75 (minutes; compare with downMatches), or null. */
export function wasDownMinutes(title) {
  const secs = /was down (\d+)s/.exec(title)
  if (secs) return Number(secs[1]) / 60
  const m = /was down (?:(\d+)d)?(?:(\d+)h)?(\d+)m/.exec(title)
  if (!m) return null
  return (Number(m[1] ?? 0) * 24 + Number(m[2] ?? 0)) * 60 + Number(m[3])
}

/** Statuses of stored beats, compressed for messages: "up x3 > pending x2 > down". */
export function seq(beats) {
  const out = []
  for (const b of beats) {
    const label = b.maint ? `${b.status}(maint)` : b.status
    const last = out[out.length - 1]
    if (last && last.label === label) last.n++
    else out.push({ label, n: 1 })
  }
  return out.map(o => (o.n > 1 ? `${o.label} x${o.n}` : o.label)).join(' > ')
}

export function outageTable(list) {
  return list.map(o => `#${o.id} ${new Date(o.startedAt).toISOString()}..${o.endedAt ? new Date(o.endedAt).toISOString() : 'open'} class=${o.errClass} first="${o.firstReason}" supp=${o.suppressedBy}`).join('\n') || '(none)'
}

/**
 * True when this machine can reach the internet-loss guard's probe addresses. When it cannot, opskest records every
 * failed check as UNKNOWN by design (Docs.md, "Monitors" "Internet-loss guard"), so scenarios that need a monitor to go
 * DOWN cannot run and skip with that reason.
 */
export async function guardOnline() {
  const probe = addr => new Promise(resolve => {
    const [host, port] = addr.split(':')
    const s = net.connect({ host, port: Number(port), timeout: 2000 })
    s.once('connect', () => { s.destroy(); resolve(true) })
    s.once('timeout', () => { s.destroy(); resolve(false) })
    s.once('error', () => resolve(false))
  })
  return (await Promise.all(['1.1.1.1:53', '8.8.8.8:53', '208.67.222.222:53'].map(probe))).some(Boolean)
}

export const OFFLINE_REASON = 'this host cannot reach the internet-loss guard addresses (1.1.1.1:53, 8.8.8.8:53, 208.67.222.222:53), so every failure is recorded as UNKNOWN by design'

/** True when the title's "was down" figure agrees with the outage length in ms (titles round to a minute, or show whole seconds under one). */
export function downMatches(title, ms) {
  const mins = wasDownMinutes(title)
  return mins !== null && Math.abs(mins - ms / 60000) <= 0.51
}
