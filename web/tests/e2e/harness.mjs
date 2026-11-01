// Shared set-up for the browser end-to-end suite.
//
// Every test file starts its OWN Opskest instance (fresh DATA_DIR/BACKUP_DIR,
// free loopback port, APP_URL=http://localhost:<port>) and its own headless
// Chrome, creates the first administrator through the real setup screen, and
// removes everything again afterwards. It never touches ports 8085/8091/8110
// or anyone's data.
//
//   e2eSuite('monitor form', e => {
//     it('works', async t => {
//       const page = await e.session(t)          // signed-in page, fresh browser context
//       await page.goto(e.url('/monitors/new'))
//     })
//   })
//
// Environment:
//   CHROME_PATH            browser binary (default: Chrome, then Edge)
//   OPSKEST_E2E_BIN        prebuilt opskest binary (skips `go build`)
//   OPSKEST_E2E_ARTIFACTS  where failure screenshots go (default: temp dir)
//   OPSKEST_E2E_HEADFUL=1  show the browser window

import { spawn, spawnSync } from 'node:child_process'
import { createHash, randomBytes } from 'node:crypto'
import dgram from 'node:dgram'
import { existsSync, mkdirSync, mkdtempSync, readdirSync, renameSync, rmSync, statSync } from 'node:fs'
import http from 'node:http'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'
import { after, before, describe } from 'node:test'
import { fileURLToPath } from 'node:url'
import { artifactsCreated, Browser, findChrome, freePort, killTree, removeDir } from './cdp.mjs'

const here = path.dirname(fileURLToPath(import.meta.url))
export const repoRoot = path.resolve(here, '../../..')
const sleep = ms => new Promise(r => setTimeout(r, ms))

export const chromePath = findChrome()

function goAvailable() {
  const r = spawnSync('go', ['version'], { stdio: 'ignore' })
  return r.status === 0
}

/** A reason to skip the whole suite, or false. Skips are reported by node:test, never as failures. */
export const SKIP = !chromePath
  ? 'Chrome/Edge not found (install Chrome or set CHROME_PATH): browser e2e suite skipped'
  : !process.env.OPSKEST_E2E_BIN && !goAvailable()
    ? 'Go toolchain not found and OPSKEST_E2E_BIN not set: browser e2e suite skipped'
    : false

// ── Building the server ─────────────────────────────────────────────────────

function newestMtime(dir, accept) {
  let newest = 0
  const walk = d => {
    for (const ent of readdirSync(d, { withFileTypes: true })) {
      const p = path.join(d, ent.name)
      if (ent.isDirectory()) walk(p)
      else if (accept(p)) newest = Math.max(newest, statSync(p).mtimeMs)
    }
  }
  if (existsSync(dir)) walk(dir)
  return newest
}

function run(cmd, args, opts = {}) {
  // npm is a .cmd shim on Windows, which needs a shell; the command line is fixed, nothing user-supplied.
  if (process.platform === 'win32') return spawnSync([cmd, ...args].join(' '), { encoding: 'utf8', shell: true, ...opts })
  return spawnSync(cmd, args, { encoding: 'utf8', ...opts })
}

/**
 * Makes sure the web UI is built (it is embedded into the binary). Warns when web/src is newer than the
 * built bundle, because the suite would then test an out-of-date UI. OPSKEST_E2E_REBUILD_WEB=1 rebuilds.
 */
function ensureWebBuilt() {
  const dist = path.join(repoRoot, 'internal/webui/dist/index.html')
  const rebuild = process.env.OPSKEST_E2E_REBUILD_WEB === '1'
  if (!existsSync(dist) || rebuild) {
    console.log('# building the web UI (npm --prefix web run build)...')
    const r = run('npm', ['--prefix', 'web', 'run', 'build'], { cwd: repoRoot })
    if (r.status !== 0) throw new Error(`web build failed:\n${r.stdout}\n${r.stderr}`)
    return
  }
  const srcNewest = newestMtime(path.join(repoRoot, 'web/src'), () => true)
  if (srcNewest > statSync(dist).mtimeMs + 1000) {
    console.warn('# WARNING: web/src is newer than internal/webui/dist; the suite tests the BUILT bundle. Run `npm --prefix web run build` (or set OPSKEST_E2E_REBUILD_WEB=1) to test the latest source.')
  }
}

let binaryPromise
/** Builds (or reuses a cached build of) the server binary. Concurrency-safe within one process. */
export function ensureBinary() {
  binaryPromise ??= (async () => {
    if (process.env.OPSKEST_E2E_BIN) {
      if (!existsSync(process.env.OPSKEST_E2E_BIN)) throw new Error(`OPSKEST_E2E_BIN does not exist: ${process.env.OPSKEST_E2E_BIN}`)
      return process.env.OPSKEST_E2E_BIN
    }
    ensureWebBuilt()
    // The build is cached in the temp dir, keyed by the newest relevant source mtime, so a run of ten
    // test files compiles once and a re-run without changes does not compile at all.
    const stamp = Math.max(
      newestMtime(path.join(repoRoot, 'cmd'), p => p.endsWith('.go')),
      newestMtime(path.join(repoRoot, 'internal'), p => p.endsWith('.go') || p.includes(`${path.sep}dist${path.sep}`)),
      statSync(path.join(repoRoot, 'go.mod')).mtimeMs, statSync(path.join(repoRoot, 'go.sum')).mtimeMs,
    )
    const cacheDir = path.join(os.tmpdir(), 'opskest-e2e-bin')
    mkdirSync(cacheDir, { recursive: true })
    const ext = process.platform === 'win32' ? '.exe' : ''
    const key = createHash('sha1').update(`${repoRoot}|${stamp}`).digest('hex').slice(0, 12)
    const out = path.join(cacheDir, `opskest-${key}${ext}`)
    if (existsSync(out)) return out
    const tmpOut = `${out}.${process.pid}.tmp${ext}`
    const r = spawnSync('go', ['build', '-o', tmpOut, './cmd/opskest'], { cwd: repoRoot, encoding: 'utf8', env: { ...process.env, CGO_ENABLED: '0' } })
    if (r.status !== 0) throw new Error(`go build ./cmd/opskest failed (the e2e suite needs a compiling server):\n${r.stdout}\n${r.stderr}`)
    try { renameSync(tmpOut, out) } catch { if (!existsSync(out)) throw new Error('could not move the built binary into the cache') }
    for (const f of readdirSync(cacheDir)) if (f !== path.basename(out) && f.startsWith('opskest-')) { try { rmSync(path.join(cacheDir, f), { force: true }) } catch { /* in use */ } }
    return out
  })()
  return binaryPromise
}

// ── The app under test ──────────────────────────────────────────────────────

export function randomPassword() {
  return `Pw-${randomBytes(12).toString('base64url')}-9z`
}

export class App {
  /** Starts an isolated instance. Resolves once /healthz answers. */
  static async start({ env: extraEnv = {} } = {}) {
    const bin = await ensureBinary()
    const dir = mkdtempSync(path.join(os.tmpdir(), 'opskest-e2e-app-'))
    const dataDir = path.join(dir, 'data')
    const backupDir = path.join(dir, 'backups')
    mkdirSync(dataDir)
    mkdirSync(backupDir)
    const port = await freePort()
    // A minimal environment: nothing from the developer's shell (APP_URL, SECRET_ENCRYPTION_KEY, PORT...) leaks in,
    // and the working directory has no .env file for the binary to pick up.
    const keep = ['PATH', 'Path', 'SystemRoot', 'SYSTEMROOT', 'TEMP', 'TMP', 'USERPROFILE', 'HOME', 'COMSPEC', 'windir']
    const env = Object.fromEntries(keep.filter(k => process.env[k] !== undefined).map(k => [k, process.env[k]]))
    Object.assign(env, {
      PORT: String(port), LISTEN_ADDR: `127.0.0.1:${port}`, APP_URL: `http://localhost:${port}`,
      DATA_DIR: dataDir, BACKUP_DIR: backupDir, LOG_LEVEL: 'info', DISPLAY_TZ: 'UTC', ...extraEnv,
    })
    const child = spawn(bin, ['serve'], { cwd: dir, env, stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true })
    const app = new App(child, dir, port)
    child.stdout.on('data', d => app.#log(d))
    child.stderr.on('data', d => app.#log(d))
    child.on('exit', code => { app.exited = code ?? -1 })
    process.once('exit', () => killTree(child.pid))
    try {
      const deadline = Date.now() + 30000
      for (;;) {
        if (app.exited !== null) throw new Error(`the server exited early with code ${app.exited}:\n${app.logs().slice(-1500)}`)
        try {
          const res = await fetch(`http://127.0.0.1:${port}/healthz`)
          if (res.ok) break
        } catch { /* not up yet */ }
        if (Date.now() > deadline) throw new Error(`the server did not answer /healthz within 30s:\n${app.logs().slice(-1500)}`)
        await sleep(100)
      }
    } catch (err) {
      await app.stop()
      throw err
    }
    return app
  }

  constructor(child, dir, port) {
    this.child = child
    this.dir = dir
    this.port = port
    this.exited = null
    this.baseUrl = `http://localhost:${port}`
    this.output = ''
    this.cookies = null
  }

  #log(chunk) { this.output += chunk.toString(); if (this.output.length > 400000) this.output = this.output.slice(-200000) }
  logs() { return this.output }

  /** Direct, cookie-authenticated API call from Node (after the admin signed in once). */
  async api(method, urlPath, body) {
    const headers = { Origin: this.baseUrl }
    if (this.cookies) {
      headers.Cookie = this.cookies.map(c => `${c.name}=${c.value}`).join('; ')
      const csrf = this.cookies.find(c => c.name === 'opskest.csrf')
      if (csrf && method !== 'GET') headers['X-CSRF-Token'] = decodeURIComponent(csrf.value)
    }
    if (body !== undefined) headers['Content-Type'] = 'application/json'
    const res = await fetch(`http://127.0.0.1:${this.port}${urlPath}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
    const text = await res.text()
    let json = null
    try { json = JSON.parse(text) } catch { /* not JSON */ }
    return { status: res.status, ok: res.ok, json, text, headers: res.headers }
  }

  async apiOk(method, urlPath, body) {
    const r = await this.api(method, urlPath, body)
    if (!r.ok) throw new Error(`${method} ${urlPath} -> ${r.status}: ${r.text.slice(0, 300)}`)
    return r.json
  }

  async stop() {
    killTree(this.child.pid)
    for (let i = 0; i < 40 && this.exited === null; i++) await sleep(50)
    await removeDir(this.dir)
  }
}

// ── A local monitored target: HTTP + TCP banner + UDP echo ──────────────────

export class Target {
  static async start() {
    const t = new Target()
    t.hits = []
    t.http = http.createServer((req, res) => {
      t.hits.push(`${req.method} ${req.url}`)
      if (req.url.startsWith('/fail')) { res.writeHead(500); res.end('boom'); return }
      if (req.url.startsWith('/slow')) { setTimeout(() => { res.writeHead(200); res.end('slow') }, 400); return }
      res.writeHead(200, { 'Content-Type': 'text/plain' })
      res.end('hello from the e2e target')
    })
    await new Promise(r => t.http.listen(0, '127.0.0.1', r))
    t.httpPort = t.http.address().port
    t.tcp = net.createServer(sock => {
      sock.on('error', () => {})
      sock.write('220 opskest-e2e ready\r\n')
      setTimeout(() => sock.destroy(), 1500)
    })
    await new Promise(r => t.tcp.listen(0, '127.0.0.1', r))
    t.tcpPort = t.tcp.address().port
    t.udp = dgram.createSocket('udp4')
    t.udp.on('message', (msg, rinfo) => t.udp.send('pong', rinfo.port, rinfo.address))
    await new Promise(r => t.udp.bind(0, '127.0.0.1', r))
    t.udpPort = t.udp.address().port
    return t
  }
  get httpUrl() { return `http://127.0.0.1:${this.httpPort}/health` }
  get tcpAddr() { return `127.0.0.1:${this.tcpPort}` }
  get udpAddr() { return `127.0.0.1:${this.udpPort}` }
  async stop() {
    this.http.closeAllConnections?.()
    await Promise.allSettled([new Promise(r => this.http.close(r)), new Promise(r => this.tcp.close(r)), new Promise(r => { try { this.udp.close(r) } catch { r() } })])
  }
}

// ── The per-file context ────────────────────────────────────────────────────

export class E2E {
  constructor({ setup = true, target = true, env } = {}) {
    this.opts = { setup, target, env }
    this.user = 'e2e_admin'
    this.password = randomPassword() // throw-away credential for a disposable local instance; never printed
    this.sessions = []
  }

  async start() {
    this.app = await App.start({ env: this.opts.env })
    this.browser = await Browser.launch({ headless: process.env.OPSKEST_E2E_HEADFUL !== '1' })
    if (this.opts.target) this.target = await Target.start()
    if (this.opts.setup) await this.createAdminViaSetupScreen()
  }

  url(p = '/') { return `${this.app.baseUrl}${p}` }

  /** Creates the first administrator through the real first-run screen and keeps the session cookies for later contexts. */
  async createAdminViaSetupScreen() {
    const ctx = await this.browser.newContext()
    const page = await ctx.newPage()
    try {
      await page.goto(this.url('/'))
      await page.waitFor(() => location.pathname === '/login', { message: 'redirect to /login on a fresh instance' })
      await page.waitForElement({ role: 'heading', name: 'Create your workspace' })
      await page.fill({ label: 'Your name' }, 'E2E Admin')
      await page.fill({ label: 'Username' }, this.user)
      await page.fill({ label: 'Password', css: 'input[name=password]' }, this.password)
      await page.fill({ css: 'input[name=confirm-password]' }, this.password)
      await page.click({ role: 'button', name: 'Create workspace' })
      await page.waitFor(() => location.pathname === '/' && !!document.querySelector('main'), { message: 'the app to open after creating the workspace' })
      this.app.cookies = (await ctx.cookies()).filter(c => c.name.startsWith('opskest.'))
      if (!this.app.cookies.some(c => c.name === 'opskest.sid')) throw new Error('setup did not leave an opskest.sid cookie')
    } finally {
      await ctx.close()
    }
  }

  /**
   * A fresh browser context + page. Signed in by default (the setup session's cookies are cloned in, so
   * tests neither share state nor hit the login throttle). Cleans up when the test ends.
   */
  async session(t, { auth = true, viewport = 'desktop', theme, scheme = 'light', goto } = {}) {
    const ctx = await this.browser.newContext()
    const page = await ctx.newPage({ viewport, theme, colorScheme: scheme })
    t?.after?.(async () => { await ctx.close().catch(() => {}) })
    if (auth) {
      await this.browser.conn.send('Storage.setCookies', {
        browserContextId: ctx.id,
        cookies: this.app.cookies.map(c => ({ name: c.name, value: c.value, domain: 'localhost', path: '/', httpOnly: c.httpOnly, sameSite: c.sameSite, secure: false })),
      })
    }
    if (goto) {
      await page.goto(this.url(goto))
      if (auth) await page.waitForElement('main', { message: `the signed-in app shell at ${goto}` })
    }
    return page
  }

  /** Unauthenticated context (no cookies). */
  anonymous(t, opts = {}) { return this.session(t, { ...opts, auth: false }) }

  async stop() {
    await this.target?.stop().catch(() => {})
    await this.browser?.close().catch(() => {})
    await this.app?.stop().catch(() => {})
    const art = artifactsCreated()
    if (art) console.log(`# failure screenshots were written to ${art}`)
  }
}

/**
 * describe() wrapper: starts the app + browser once per file, skips cleanly when Chrome/Go are missing.
 * Tests inside must not depend on one another: create what you need, with unique names.
 */
export function e2eSuite(name, body, opts) {
  describe(name, { skip: SKIP }, () => {
    const e = new E2E(opts)
    before(() => e.start(), { timeout: 180000 })
    after(() => e.stop(), { timeout: 60000 })
    body(e)
  })
}

export const unique = prefix => `${prefix}-${randomBytes(3).toString('hex')}`
