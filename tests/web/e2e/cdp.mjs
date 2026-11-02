// A small Chrome DevTools Protocol driver. Zero dependencies: Node's global
// WebSocket and fetch talk to a headless Chrome/Edge that this module launches
// itself with a throw-away profile directory.
//
//   const browser = await Browser.launch()
//   const ctx = await browser.newContext()        // isolated cookies/storage
//   const page = await ctx.newPage({ viewport: 'desktop', theme: 'dark' })
//   await page.goto(url)
//   await page.click({ role: 'button', name: 'Sign in' })
//   await page.fill({ label: 'Username' }, 'admin')
//   await browser.close()
//
// Locators ("specs") are plain objects resolved inside the page:
//   { css, role, name, label, placeholder, text, exact, within, nth, visible }
// A bare string is a CSS selector. RegExp values are allowed for name/label/
// text/placeholder. Every wait has a timeout and fails with a message that
// says what was awaited, the URL, recent console errors and a body excerpt.

import { spawn, spawnSync } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'

const sleep = ms => new Promise(r => setTimeout(r, ms))

// ── Finding a browser ───────────────────────────────────────────────────────

/** The first Chrome/Edge binary found, or null (callers then skip the suite). */
export function findChrome() {
  const env = process.env.CHROME_PATH
  if (env) return existsSync(env) ? env : null
  const pf = process.env.ProgramFiles || 'C:\\Program Files'
  const pf86 = process.env['ProgramFiles(x86)'] || 'C:\\Program Files (x86)'
  const local = process.env.LOCALAPPDATA || ''
  const candidates = [
    path.join(pf, 'Google/Chrome/Application/chrome.exe'),
    path.join(pf86, 'Google/Chrome/Application/chrome.exe'),
    local && path.join(local, 'Google/Chrome/Application/chrome.exe'),
    path.join(pf86, 'Microsoft/Edge/Application/msedge.exe'),
    path.join(pf, 'Microsoft/Edge/Application/msedge.exe'),
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    '/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge',
    '/usr/bin/google-chrome', '/usr/bin/google-chrome-stable', '/usr/bin/chromium', '/usr/bin/chromium-browser',
    '/usr/bin/microsoft-edge',
  ].filter(Boolean)
  return candidates.find(existsSync) ?? null
}

export function freePort() {
  return new Promise((resolve, reject) => {
    const srv = net.createServer()
    srv.on('error', reject)
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address()
      srv.close(() => resolve(port))
    })
  })
}

/** Kills a process and its children (Chrome has many). */
export function killTree(pid) {
  if (!pid) return
  try {
    if (process.platform === 'win32') spawnSync('taskkill', ['/PID', String(pid), '/T', '/F'], { stdio: 'ignore' })
    else process.kill(pid, 'SIGKILL')
  } catch { /* already gone */ }
}

export async function removeDir(dir) {
  for (let i = 0; i < 20; i++) {
    try { rmSync(dir, { recursive: true, force: true }); return } catch { await sleep(150) }
  }
  try { rmSync(dir, { recursive: true, force: true }) } catch { /* best effort: a locked profile dir in %TEMP% is harmless */ }
}

// ── Artifacts (screenshots) ─────────────────────────────────────────────────

let artifactDir = null
/** Screenshots go to a temp dir, never the repository. Created lazily. */
export function artifactsDir() {
  if (!artifactDir) {
    const base = process.env.OPSKEST_E2E_ARTIFACTS || path.join(os.tmpdir(), 'opskest-e2e-artifacts')
    mkdirSync(base, { recursive: true })
    artifactDir = mkdtempSync(path.join(base, 'run-'))
  }
  return artifactDir
}
export function artifactsCreated() { return artifactDir }

// ── Connection ──────────────────────────────────────────────────────────────

class Connection {
  constructor(ws) {
    this.ws = ws
    this.nextId = 1
    this.pending = new Map()
    this.sessions = new Map() // sessionId -> (method, params) => void
    this.closed = false
    ws.addEventListener('message', ev => this.#onMessage(ev.data))
    ws.addEventListener('close', () => {
      this.closed = true
      for (const { reject, method } of this.pending.values()) reject(new Error(`CDP connection closed while waiting for ${method}`))
      this.pending.clear()
    })
  }

  static open(url) {
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(url)
      ws.addEventListener('open', () => resolve(new Connection(ws)), { once: true })
      ws.addEventListener('error', () => reject(new Error(`could not open the DevTools websocket ${url}`)), { once: true })
    })
  }

  #onMessage(data) {
    const msg = JSON.parse(typeof data === 'string' ? data : Buffer.from(data).toString('utf8'))
    if (msg.id) {
      const p = this.pending.get(msg.id)
      if (!p) return
      this.pending.delete(msg.id)
      if (msg.error) p.reject(new Error(`CDP ${p.method} failed: ${msg.error.message}${msg.error.data ? ` (${msg.error.data})` : ''}`))
      else p.resolve(msg.result ?? {})
      return
    }
    const handler = this.sessions.get(msg.sessionId ?? '')
    if (handler) handler(msg.method, msg.params ?? {})
  }

  send(method, params = {}, sessionId, timeout = 30000) {
    if (this.closed) return Promise.reject(new Error(`CDP connection is closed (calling ${method})`))
    const id = this.nextId++
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id)
        reject(new Error(`CDP ${method} timed out after ${timeout}ms`))
      }, timeout)
      this.pending.set(id, {
        method,
        resolve: v => { clearTimeout(timer); resolve(v) },
        reject: e => { clearTimeout(timer); reject(e) },
      })
      this.ws.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }))
    })
  }

  close() { try { this.ws.close() } catch { /* ignore */ } }
}

// ── Browser ─────────────────────────────────────────────────────────────────

export class Browser {
  static async launch({ chromePath = findChrome(), headless = true } = {}) {
    if (!chromePath) throw new Error('No Chrome/Edge binary found. Set CHROME_PATH.')
    const port = await freePort()
    const profile = mkdtempSync(path.join(os.tmpdir(), 'opskest-e2e-chrome-'))
    const args = [
      ...(headless ? ['--headless=new'] : []),
      `--remote-debugging-port=${port}`,
      '--remote-debugging-address=127.0.0.1',
      `--user-data-dir=${profile}`,
      '--no-first-run', '--no-default-browser-check', '--disable-extensions', '--disable-sync',
      '--disable-background-networking', '--disable-component-update', '--disable-default-apps',
      '--disable-features=Translate,MediaRouter,OptimizationHints', '--mute-audio', '--force-color-profile=srgb',
      '--disable-renderer-backgrounding', '--disable-background-timer-throttling', '--disable-backgrounding-occluded-windows',
      '--window-size=1280,900',
      ...(process.platform === 'linux' ? ['--no-sandbox', '--disable-dev-shm-usage'] : []),
      'about:blank',
    ]
    const child = spawn(chromePath, args, { stdio: 'ignore', windowsHide: true })
    let exited = false
    child.on('exit', () => { exited = true })
    child.on('error', () => { exited = true })
    const browser = new Browser(child, profile, port)
    process.once('exit', () => killTree(child.pid))
    try {
      let version
      const deadline = Date.now() + 20000
      while (!version) {
        if (exited) throw new Error('the browser exited during start-up')
        if (Date.now() > deadline) throw new Error('the browser did not expose the DevTools endpoint within 20s')
        try {
          const res = await fetch(`http://127.0.0.1:${port}/json/version`)
          if (res.ok) version = await res.json()
        } catch { await sleep(100) }
      }
      browser.version = version.Browser
      browser.conn = await Connection.open(version.webSocketDebuggerUrl)
      return browser
    } catch (err) {
      await browser.close()
      throw err
    }
  }

  constructor(child, profile, port) {
    this.child = child
    this.profile = profile
    this.port = port
    this.contexts = new Set()
  }

  /** An isolated browser context: its own cookies, localStorage and cache. */
  async newContext() {
    const { browserContextId } = await this.conn.send('Target.createBrowserContext', { disposeOnDetach: false })
    const ctx = new BrowserContext(this, browserContextId)
    this.contexts.add(ctx)
    return ctx
  }

  async close() {
    for (const ctx of [...this.contexts]) await ctx.close().catch(() => {})
    this.conn?.close()
    killTree(this.child.pid)
    await removeDir(this.profile)
  }
}

export class BrowserContext {
  constructor(browser, id) {
    this.browser = browser
    this.id = id
    this.pages = new Set()
  }

  async newPage(opts = {}) {
    const page = await Page.create(this, opts)
    this.pages.add(page)
    return page
  }

  async cookies() {
    const { cookies } = await this.browser.conn.send('Storage.getCookies', { browserContextId: this.id })
    return cookies
  }

  async close() {
    for (const p of [...this.pages]) await p.close().catch(() => {})
    this.browser.contexts.delete(this)
    await this.browser.conn.send('Target.disposeBrowserContext', { browserContextId: this.id }).catch(() => {})
  }
}

// ── In-page helper library ──────────────────────────────────────────────────
// Injected into every document as window.__e2e. Kept as a function so the
// source is syntax-checked with the rest of this file.

function inPageLibrary() {
  const norm = s => (s ?? '').replace(/\s+/g, ' ').trim()
  const reOf = v => (v && typeof v === 'object' && '__re' in v ? new RegExp(v.__re, v.flags) : null)
  const matches = (actual, want, exact) => {
    if (want === undefined || want === null) return true
    const re = reOf(want)
    if (re) return re.test(actual)
    return exact ? norm(actual) === norm(want) : norm(actual).toLowerCase().includes(norm(want).toLowerCase())
  }

  function visible(el) {
    if (!(el instanceof Element)) return false
    if (typeof el.checkVisibility === 'function' && !el.checkVisibility({ checkVisibilityCSS: true })) return false
    const r = el.getBoundingClientRect()
    return r.width > 0 && r.height > 0
  }

  function textOf(node, skipControls) {
    if (node.nodeType === 3) return node.nodeValue
    if (node.nodeType !== 1) return ''
    const el = node
    if (el.getAttribute('aria-hidden') === 'true') return ''
    const cs = getComputedStyle(el)
    if (cs.display === 'none') return ''
    if (el.tagName === 'IMG') return el.getAttribute('alt') || ''
    if (el.tagName === 'SVG' || el instanceof SVGElement) {
      const t = el.querySelector(':scope > title')
      return t ? t.textContent : ''
    }
    if (skipControls && /^(INPUT|SELECT|TEXTAREA)$/.test(el.tagName)) return ''
    let out = ''
    for (const c of el.childNodes) out += textOf(c, skipControls) + (c.nodeType === 1 && getComputedStyle(c).display !== 'inline' ? ' ' : '')
    return out
  }

  const IMPLICIT = (el) => {
    const t = el.tagName
    if (t === 'A') return el.hasAttribute('href') ? 'link' : null
    if (t === 'BUTTON') return 'button'
    if (t === 'SELECT') return el.multiple || el.size > 1 ? 'listbox' : 'combobox'
    if (t === 'TEXTAREA') return 'textbox'
    if (t === 'INPUT') {
      const ty = (el.getAttribute('type') || 'text').toLowerCase()
      if (ty === 'checkbox') return 'checkbox'
      if (ty === 'radio') return 'radio'
      if (['submit', 'button', 'reset', 'image'].includes(ty)) return 'button'
      if (ty === 'range') return 'slider'
      if (ty === 'number') return 'spinbutton'
      if (ty === 'search') return 'searchbox'
      if (ty === 'hidden') return null
      return 'textbox'
    }
    if (/^H[1-6]$/.test(t)) return 'heading'
    if (t === 'NAV') return 'navigation'
    if (t === 'MAIN') return 'main'
    if (t === 'DIALOG') return 'dialog'
    if (t === 'TABLE') return 'table'
    if (t === 'UL' || t === 'OL') return 'list'
    if (t === 'LI') return 'listitem'
    if (t === 'IMG') return 'img'
    if (t === 'FORM') return 'form'
    if (t === 'ASIDE') return 'complementary'
    if (t === 'HEADER') return 'banner'
    return null
  }
  const roleOf = el => (el.getAttribute('role') || '').split(' ')[0] || IMPLICIT(el)

  function labelsOf(el) {
    const out = []
    if (el.labels) for (const l of el.labels) out.push(norm(textOf(l, true)))
    return out
  }

  /** A simplified accessible-name computation (aria-labelledby, aria-label, labels, content, title, placeholder). */
  function accName(el) {
    const by = el.getAttribute('aria-labelledby')
    if (by) {
      const t = by.split(/\s+/).map(id => document.getElementById(id)).filter(Boolean).map(n => norm(textOf(n))).join(' ')
      if (t) return t
    }
    const al = el.getAttribute('aria-label')
    if (al && norm(al)) return norm(al)
    if (/^(INPUT|SELECT|TEXTAREA)$/.test(el.tagName)) {
      const type = (el.getAttribute('type') || '').toLowerCase()
      if (['submit', 'button', 'reset'].includes(type)) return norm(el.value) || (type === 'submit' ? 'Submit' : '')
      if (type === 'image') return norm(el.getAttribute('alt'))
      const ls = labelsOf(el).filter(Boolean)
      if (ls.length) return ls.join(' ')
    }
    const role = roleOf(el)
    if (['button', 'link', 'heading', 'tab', 'menuitem', 'menuitemradio', 'menuitemcheckbox', 'radio', 'checkbox', 'option', 'dialog', 'img', 'listitem', 'cell', 'columnheader', 'row', 'switch'].includes(role) && !/^(INPUT|SELECT|TEXTAREA)$/.test(el.tagName)) {
      const t = norm(textOf(el))
      if (t) return t
    }
    const title = el.getAttribute('title')
    if (title && norm(title)) return norm(title)
    if (el.getAttribute('placeholder')) return norm(el.getAttribute('placeholder'))
    if (/^(INPUT|SELECT|TEXTAREA)$/.test(el.tagName)) return ''
    return norm(textOf(el))
  }

  function describe(el) {
    if (!el) return 'nothing'
    let s = el.tagName.toLowerCase()
    if (el.id) s += '#' + el.id
    const cls = typeof el.className === 'string' ? el.className.trim().split(/\s+/).slice(0, 3).join('.') : ''
    if (cls) s += '.' + cls
    const t = norm(el.textContent).slice(0, 40)
    return t ? `${s} "${t}"` : s
  }

  function resolveWithin(w) {
    if (!w) return document
    const spec = typeof w === 'string' ? { css: w } : w
    return findAll(spec)[0] ?? null
  }

  function findAll(spec) {
    if (typeof spec === 'string') spec = { css: spec }
    const root = spec.within ? resolveWithin(spec.within) : document
    if (!root) return []
    let els = Array.from(root.querySelectorAll(spec.css || '*'))
    if (spec.role) els = els.filter(e => roleOf(e) === spec.role)
    if (spec.name !== undefined) els = els.filter(e => matches(accName(e), spec.name, spec.exact))
    if (spec.label !== undefined) els = els.filter(e => (e.labels && e.labels.length) && Array.from(e.labels).some(l => matches(textOf(l, true), spec.label, spec.exact)))
    if (spec.placeholder !== undefined) els = els.filter(e => matches(e.getAttribute('placeholder') || '', spec.placeholder, spec.exact))
    if (spec.text !== undefined) {
      const hit = new Set(els.filter(e => matches(textOf(e), spec.text, spec.exact)))
      // Keep the innermost matches only.
      els = els.filter(e => { for (const h of hit) if (h !== e && e.contains(h)) return false; return hit.has(e) })
    }
    if (spec.visible !== false) els = els.filter(visible)
    if (spec.enabled === true) els = els.filter(e => !e.disabled && e.getAttribute('aria-disabled') !== 'true')
    if (spec.nth !== undefined) els = els[spec.nth] ? [els[spec.nth]] : []
    return els
  }

  function probe(spec) {
    const el = findAll(spec)[0]
    if (!el) return { state: 'missing' }
    el.scrollIntoView({ block: 'center', inline: 'center' })
    const r = el.getBoundingClientRect()
    if (el.disabled || el.getAttribute('aria-disabled') === 'true') return { state: 'disabled', desc: describe(el) }
    const x = Math.min(Math.max(r.left + r.width / 2, 1), innerWidth - 1)
    const y = Math.min(Math.max(r.top + r.height / 2, 1), innerHeight - 1)
    const hit = document.elementFromPoint(x, y)
    const ok = hit && (el === hit || el.contains(hit) || (hit.tagName === 'LABEL' && hit.control === el))
    return { state: ok ? 'ok' : 'obscured', x, y, w: r.width, h: r.height, desc: describe(el), by: ok ? '' : describe(hit) }
  }

  window.__e2e = { norm, matches, visible, roleOf, accName, textOf, describe, findAll, probe, labelsOf }
}

const LIB_SOURCE = `(${inPageLibrary.toString()})()`

/** RegExp-safe serialisation of a locator for the in-page library. */
const ser = spec => JSON.stringify(spec, (_k, v) => (v instanceof RegExp ? { __re: v.source, flags: v.flags } : v))
export function specText(spec) {
  if (typeof spec === 'string') return spec
  return JSON.stringify(spec, (_k, v) => (v instanceof RegExp ? String(v) : v))
}

// ── Keys ────────────────────────────────────────────────────────────────────

const KEYS = {
  Enter: { key: 'Enter', code: 'Enter', vk: 13, text: '\r' },
  Escape: { key: 'Escape', code: 'Escape', vk: 27 },
  Tab: { key: 'Tab', code: 'Tab', vk: 9 },
  Backspace: { key: 'Backspace', code: 'Backspace', vk: 8 },
  Delete: { key: 'Delete', code: 'Delete', vk: 46 },
  ArrowLeft: { key: 'ArrowLeft', code: 'ArrowLeft', vk: 37 },
  ArrowUp: { key: 'ArrowUp', code: 'ArrowUp', vk: 38 },
  ArrowRight: { key: 'ArrowRight', code: 'ArrowRight', vk: 39 },
  ArrowDown: { key: 'ArrowDown', code: 'ArrowDown', vk: 40 },
  Home: { key: 'Home', code: 'Home', vk: 36 },
  End: { key: 'End', code: 'End', vk: 35 },
  PageUp: { key: 'PageUp', code: 'PageUp', vk: 33 },
  PageDown: { key: 'PageDown', code: 'PageDown', vk: 34 },
  Space: { key: ' ', code: 'Space', vk: 32, text: ' ' },
  ' ': { key: ' ', code: 'Space', vk: 32, text: ' ' },
}
const MODS = { Alt: 1, Control: 2, Ctrl: 2, Meta: 4, Shift: 8 }

function keyInfo(name) {
  if (KEYS[name]) return KEYS[name]
  if (name.length === 1) {
    const up = name.toUpperCase()
    const isLetter = /[a-z]/i.test(name)
    return { key: name, code: isLetter ? `Key${up}` : /\d/.test(name) ? `Digit${name}` : '', vk: up.charCodeAt(0), text: name }
  }
  throw new Error(`press(): unknown key "${name}"`)
}

export const VIEWPORTS = {
  desktop: { width: 1280, height: 800, mobile: false },
  mobile: { width: 375, height: 812, mobile: true },
}

// ── Page ────────────────────────────────────────────────────────────────────

export class Page {
  static async create(context, opts) {
    const conn = context.browser.conn
    const { targetId } = await conn.send('Target.createTarget', { url: 'about:blank', browserContextId: context.id })
    const { sessionId } = await conn.send('Target.attachToTarget', { targetId, flatten: true })
    const page = new Page(context, conn, targetId, sessionId)
    await page.#init(opts)
    return page
  }

  constructor(context, conn, targetId, sessionId) {
    this.context = context
    this.conn = conn
    this.targetId = targetId
    this.sessionId = sessionId
    this.consoleMessages = [] // { type, text }
    this.exceptions = [] // uncaught exceptions
    this.failedRequests = [] // { url, error }
    this.responses = [] // { url, status, method, mime }
    this.dialogs = [] // { type, message }
    this.dialogAction = 'accept'
    this.inflight = new Map()
    this.lastNetworkActivity = Date.now()
    this.loadWaiters = []
    this.requestWaiters = []
    this.closed = false
    conn.sessions.set(sessionId, (m, p) => this.#onEvent(m, p))
  }

  send(method, params = {}, timeout) { return this.conn.send(method, params, this.sessionId, timeout) }

  async #init({ viewport = 'desktop', theme, colorScheme } = {}) {
    await Promise.all([
      this.send('Page.enable'), this.send('Runtime.enable'), this.send('Network.enable'), this.send('Log.enable'),
    ])
    await this.send('Page.addScriptToEvaluateOnNewDocument', { source: LIB_SOURCE })
    if (theme) {
      // Applied once per tab, before the first paint, so a later toggle in the app survives reloads.
      const src = `try{if(location.protocol==='http:'&&!sessionStorage.getItem('__e2e_theme')){localStorage.setItem('opskest-theme',${JSON.stringify(theme)});sessionStorage.setItem('__e2e_theme','1')}}catch(e){}`
      await this.send('Page.addScriptToEvaluateOnNewDocument', { source: src })
    }
    await this.setViewport(viewport)
    if (colorScheme) await this.emulateColorScheme(colorScheme)
  }

  #onEvent(method, p) {
    switch (method) {
      case 'Runtime.consoleAPICalled': {
        const text = p.args.map(a => a.value !== undefined ? String(a.value) : a.description ?? a.type).join(' ')
        this.consoleMessages.push({ type: p.type, text })
        break
      }
      case 'Runtime.exceptionThrown': {
        const d = p.exceptionDetails
        this.exceptions.push(`${d.exception?.description ?? d.text} (${d.url ?? ''}:${d.lineNumber})`)
        break
      }
      case 'Log.entryAdded':
        if (p.entry.level === 'error') this.consoleMessages.push({ type: 'error', text: `${p.entry.text}${p.entry.url ? ` ${p.entry.url}` : ''}`, source: p.entry.source })
        break
      case 'Network.requestWillBeSent':
        // A new document abandons whatever the previous one was still loading; those requests never report back.
        if (p.type === 'Document') { this.inflight.clear(); this.loader = p.loaderId }
        if (p.loaderId && this.loader && p.loaderId !== this.loader) { this.lastNetworkActivity = Date.now(); break }
        this.inflight.set(p.requestId, { url: p.request.url, method: p.request.method })
        this.lastNetworkActivity = Date.now()
        break
      case 'Network.responseReceived': {
        const r = this.inflight.get(p.requestId) ?? {}
        const rec = { url: p.response.url, status: p.response.status, method: r.method, mime: p.response.mimeType, requestId: p.requestId }
        this.responses.push(rec)
        for (const w of [...this.requestWaiters]) if (w.test(rec)) { this.requestWaiters.splice(this.requestWaiters.indexOf(w), 1); w.resolve(rec) }
        break
      }
      case 'Network.loadingFinished':
        this.inflight.delete(p.requestId)
        this.lastNetworkActivity = Date.now()
        break
      case 'Network.loadingFailed': {
        const r = this.inflight.get(p.requestId)
        this.inflight.delete(p.requestId)
        this.lastNetworkActivity = Date.now()
        if (p.errorText !== 'net::ERR_ABORTED' && !p.canceled) this.failedRequests.push({ url: r?.url ?? '?', error: p.errorText })
        break
      }
      case 'Page.javascriptDialogOpening':
        this.dialogs.push({ type: p.type, message: p.message })
        this.send('Page.handleJavaScriptDialog', { accept: this.dialogAction === 'accept', ...(p.type === 'prompt' ? { promptText: '' } : {}) }).catch(() => {})
        break
      case 'Page.loadEventFired':
        for (const w of this.loadWaiters.splice(0)) w()
        break
      default:
    }
  }

  // ── environment ───────────────────────────────────────────────────────────

  /** 'desktop' (1280x800), 'mobile' (375x812, touch-less mobile emulation) or { width, height, mobile }. */
  async setViewport(v) {
    const vp = typeof v === 'string' ? VIEWPORTS[v] : v
    if (!vp) throw new Error(`unknown viewport ${v}`)
    this.viewport = vp
    await this.send('Emulation.setDeviceMetricsOverride', { width: vp.width, height: vp.height, deviceScaleFactor: 1, mobile: !!vp.mobile })
  }

  /** Emulates the OS-level prefers-color-scheme ('light' | 'dark'). */
  async emulateColorScheme(scheme) {
    await this.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: scheme }] })
  }

  async setOffline(offline) {
    await this.send('Network.emulateNetworkConditions', { offline, latency: 0, downloadThroughput: -1, uploadThroughput: -1 })
  }

  // ── navigation ────────────────────────────────────────────────────────────

  /** Navigates and waits for the load event and (by default) a quiet network. */
  async goto(url, { idle = true, timeout = 30000 } = {}) {
    const loaded = new Promise(resolve => this.loadWaiters.push(resolve))
    const res = await this.send('Page.navigate', { url })
    if (res.errorText) throw new Error(`navigation to ${url} failed: ${res.errorText}`)
    if (!res.isDownload) {
      await Promise.race([loaded, sleep(timeout).then(() => { throw new Error(`timed out after ${timeout}ms waiting for ${url} to load`) })])
    }
    if (idle) await this.settle()
  }

  async reload(opts) {
    const loaded = new Promise(resolve => this.loadWaiters.push(resolve))
    await this.send('Page.reload')
    await Promise.race([loaded, sleep(30000).then(() => { throw new Error('timed out waiting for reload') })])
    if (opts?.idle !== false) await this.settle()
  }

  /** Waits until no (non-stream) request has been in flight for `quiet` ms. */
  async settle({ quiet = 300, timeout = 10000 } = {}) {
    const deadline = Date.now() + timeout
    for (;;) {
      const busy = [...this.inflight.values()].some(r => !/\/stream(\?|$)/.test(r.url) && !/\.atom(\?|$)/.test(r.url))
      if (!busy && Date.now() - this.lastNetworkActivity >= quiet) return
      if (Date.now() > deadline) return // a chatty page is not an error; waitFor conditions decide
      await sleep(50)
    }
  }

  url() { return this.evaluate(() => location.href) }
  async path() { return this.evaluate(() => location.pathname + location.search) }

  // ── evaluation ────────────────────────────────────────────────────────────

  /** Runs a function (or expression string) in the page and returns its JSON-serialisable result. */
  async evaluate(fn, ...args) {
    const expression = typeof fn === 'function' ? `(${fn.toString()})(...${JSON.stringify(args)})` : String(fn)
    const res = await this.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true, userGesture: true })
    if (res.exceptionDetails) {
      const d = res.exceptionDetails
      throw new Error(`page.evaluate threw: ${d.exception?.description ?? d.text}`)
    }
    return res.result.value
  }

  /** Locator helper run in the page: lib(spec, 'findAll'|'probe'). */
  #lib(method, spec) {
    return this.evaluate(`(()=>{const s=${ser(spec)};const L=window.__e2e;if(!L)return null;${
      method === 'count' ? 'return L.findAll(s).length'
        : method === 'probe' ? 'return L.probe(s)'
          : method === 'text' ? 'const e=L.findAll(s)[0];return e?e.innerText:null'
            : method === 'texts' ? 'return L.findAll(s).map(e=>e.innerText)'
              : method === 'value' ? 'const e=L.findAll(s)[0];return e?e.value:null'
                : method === 'attr' ? 'return null'
                  : 'return null'
    }})()`)
  }

  async count(spec) { return (await this.#lib('count', spec)) ?? 0 }
  async exists(spec) { return (await this.count(spec)) > 0 }
  async text(spec) { return this.#lib('text', spec) }
  async texts(spec) { return (await this.#lib('texts', spec)) ?? [] }
  async value(spec) { return this.#lib('value', spec) }
  bodyText() { return this.evaluate(() => document.body.innerText) }

  /** Runs `fn(el, ...args)` on the first element matching spec. */
  async onElement(spec, fn, ...args) {
    const r = await this.evaluate(`(()=>{const e=window.__e2e.findAll(${ser(spec)})[0];if(!e)return {missing:true};return {v:(${fn.toString()})(e,...${JSON.stringify(args)})}})()`)
    if (r?.missing) throw new Error(`onElement: no element matches ${specText(spec)}`)
    return r.v
  }

  /** Like onElement but across every match. */
  async onElements(spec, fn, ...args) {
    return this.evaluate(`window.__e2e.findAll(${ser(spec)}).map(e=>(${fn.toString()})(e,...${JSON.stringify(args)}))`)
  }

  // ── waiting ───────────────────────────────────────────────────────────────

  async diagnostics() {
    let url = '?'
    let body = ''
    try { url = await this.url(); body = (await this.bodyText()).replace(/\s+/g, ' ').slice(0, 400) } catch { /* page may be gone */ }
    const errs = this.problems().slice(-4)
    return `\n  url:  ${url}\n  body: ${body}${errs.length ? `\n  recent problems:\n    - ${errs.join('\n    - ')}` : ''}`
  }

  /** Polls `fn` (a function run in the page, or a locator spec) until truthy. */
  async waitFor(what, { timeout = 10000, interval = 100, message, args = [] } = {}) {
    const deadline = Date.now() + timeout
    const isFn = typeof what === 'function'
    let last
    for (;;) {
      try {
        last = isFn ? await this.evaluate(what, ...args) : await this.count(what) > 0
        if (last) return last
      } catch (err) {
        last = err
        if (this.closed) throw err
      }
      if (Date.now() > deadline) break
      await sleep(interval)
    }
    const shot = await this.screenshot('timeout').catch(() => null)
    throw new Error(`timed out after ${timeout}ms waiting for ${message ?? (isFn ? 'condition' : `element ${specText(what)}`)}${last instanceof Error ? ` (${last.message})` : ''}${await this.diagnostics()}${shot ? `\n  screenshot: ${shot}` : ''}`)
  }

  /** Waits for location.pathname to equal a string (or, with '?' in it, pathname+search), or match a RegExp. */
  waitForPath(expected, opts) {
    const isRe = expected instanceof RegExp
    return this.waitFor((src, flags, exp, isRe) => {
      const cur = isRe || exp.includes('?') ? location.pathname + location.search : location.pathname
      return isRe ? new RegExp(src, flags).test(cur) : cur === exp
    }, { message: `location to become ${expected}`, ...opts, args: isRe ? [expected.source, expected.flags, '', true] : ['', '', expected, false] })
  }

  waitForElement(spec, opts) { return this.waitFor(spec, { message: `element ${specText(spec)} to appear`, ...opts }) }

  async waitForGone(spec, { timeout = 10000 } = {}) {
    const deadline = Date.now() + timeout
    while (await this.count(spec) > 0) {
      if (Date.now() > deadline) {
        const shot = await this.screenshot('still-present').catch(() => null)
        throw new Error(`timed out after ${timeout}ms waiting for ${specText(spec)} to disappear${await this.diagnostics()}${shot ? `\n  screenshot: ${shot}` : ''}`)
      }
      await sleep(100)
    }
  }

  /** Waits for the next response whose URL matches `urlPattern` (RegExp|string) and optional method. Register before the action. */
  waitForResponse(urlPattern, { method, timeout = 15000 } = {}) {
    const test = r => (urlPattern instanceof RegExp ? urlPattern.test(r.url) : r.url.includes(urlPattern)) && (!method || r.method === method)
    return new Promise((resolve, reject) => {
      const w = { test, resolve: v => { clearTimeout(t); resolve(v) } }
      const t = setTimeout(() => {
        this.requestWaiters.splice(this.requestWaiters.indexOf(w), 1)
        reject(new Error(`timed out after ${timeout}ms waiting for a ${method ?? ''} response matching ${urlPattern}`))
      }, timeout)
      this.requestWaiters.push(w)
    })
  }

  // ── input ─────────────────────────────────────────────────────────────────

  /** Input to a background tab waits seconds for a frame; make sure this page is the one in front. */
  async #front() {
    if (Page.front === this) return
    Page.front = this
    await this.send('Page.bringToFront').catch(() => {})
  }

  async #mouse(type, x, y, extra = {}) {
    await this.send('Input.dispatchMouseEvent', { type, x, y, button: 'left', buttons: type === 'mouseReleased' ? 0 : 1, clickCount: 1, ...extra })
  }

  /** Scrolls the element into view and resolves its click point, waiting until it is visible, enabled, unobscured and still. */
  async #target(spec, verb, timeout) {
    const deadline = Date.now() + timeout
    let info
    let prev = ''
    for (;;) {
      info = await this.#lib('probe', spec)
      if (info?.state === 'ok') {
        const key = `${Math.round(info.x)},${Math.round(info.y)}`
        if (key === prev) return info
        prev = key
      } else prev = ''
      if (Date.now() > deadline) break
      await sleep(70)
    }
    const shot = await this.screenshot(`${verb}-failed`).catch(() => null)
    const why = !info || info.state === 'missing' ? 'no visible element matches'
      : info.state === 'disabled' ? `the element is disabled (${info.desc})`
        : info.state === 'obscured' ? `${info.desc} is covered by ${info.by}` : 'the element is not stable'
    throw new Error(`${verb}(${specText(spec)}) failed after ${timeout}ms: ${why}${await this.diagnostics()}${shot ? `\n  screenshot: ${shot}` : ''}`)
  }

  /** A real mouse click (move, press, release) at the centre of the element. */
  async click(spec, { timeout = 8000, clickCount = 1, modifiers = 0 } = {}) {
    await this.#front()
    const t = await this.#target(spec, 'click', timeout)
    await this.#mouse('mouseMoved', t.x, t.y, { buttons: 0 })
    for (let i = 1; i <= clickCount; i++) {
      await this.#mouse('mousePressed', t.x, t.y, { clickCount: i, modifiers })
      await this.#mouse('mouseReleased', t.x, t.y, { clickCount: i, modifiers })
    }
  }

  async hover(spec, { timeout = 8000 } = {}) {
    await this.#front()
    const t = await this.#target(spec, 'hover', timeout)
    await this.#mouse('mouseMoved', t.x, t.y, { buttons: 0 })
  }

  /** Focuses (by click) and replaces the contents of an input with `text`, typed through the input pipeline. */
  async fill(spec, text, { timeout = 8000 } = {}) {
    await this.click(spec, { timeout })
    await this.evaluate(`(()=>{const a=document.activeElement;if(a&&a.select)a.select();else if(a&&a.isContentEditable)document.execCommand('selectAll')})()`)
    if (text === '') await this.press('Backspace')
    else await this.send('Input.insertText', { text })
    const got = await this.onElement(spec, e => e.value ?? e.textContent)
    if (got !== text) throw new Error(`fill(${specText(spec)}): expected the field to hold ${JSON.stringify(text)} but it holds ${JSON.stringify(got)} (maxlength or a controlled input rejecting the value?)`)
  }

  /** Types text into whatever has focus. */
  async type(text) { await this.#front(); await this.send('Input.insertText', { text }) }

  /** Presses a key or combination, e.g. 'Enter', 'Shift+Tab', 'Control+k'. */
  async press(combo) {
    await this.#front()
    const parts = combo.split('+')
    const keyName = parts.pop() || '+'
    const modifiers = parts.reduce((m, p) => m | (MODS[p] ?? 0), 0)
    const k = keyInfo(keyName)
    const shifted = modifiers & 8 && k.text && /[a-z]/.test(k.text) ? k.text.toUpperCase() : k.text
    const text = modifiers & ~8 ? undefined : shifted
    await this.send('Input.dispatchKeyEvent', { type: text ? 'keyDown' : 'rawKeyDown', modifiers, key: k.key, code: k.code, windowsVirtualKeyCode: k.vk, nativeVirtualKeyCode: k.vk, ...(text ? { text, unmodifiedText: k.text } : {}) })
    await this.send('Input.dispatchKeyEvent', { type: 'keyUp', modifiers, key: k.key, code: k.code, windowsVirtualKeyCode: k.vk, nativeVirtualKeyCode: k.vk })
  }

  /** Selects an <option> of a <select> by value or visible label (sets the value and fires input/change like the browser does). */
  async select(spec, valueOrLabel) {
    await this.#target(spec, 'select', 8000)
    const ok = await this.onElement(spec, (el, want) => {
      const opt = Array.from(el.options).find(o => o.value === want || o.textContent.trim() === want)
      if (!opt) return false
      el.focus()
      const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value').set
      setter.call(el, opt.value)
      el.dispatchEvent(new Event('input', { bubbles: true }))
      el.dispatchEvent(new Event('change', { bubbles: true }))
      return true
    }, valueOrLabel)
    if (!ok) {
      const opts = await this.onElement(spec, el => Array.from(el.options).map(o => `${o.value}|${o.textContent.trim()}`))
      throw new Error(`select(${specText(spec)}): no option "${valueOrLabel}"; options: ${opts.join(', ')}`)
    }
  }

  async focus(spec) {
    await this.onElement(spec, e => e.focus())
  }

  /** Returns a description of the focused element. */
  activeElement() {
    return this.evaluate(() => {
      const a = document.activeElement
      if (!a || a === document.body) return null
      return { tag: a.tagName.toLowerCase(), id: a.id, text: (a.innerText || a.value || '').trim().slice(0, 60), name: window.__e2e.accName(a), href: a.getAttribute('href'), role: window.__e2e.roleOf(a), cls: String(a.className) }
    })
  }

  // ── problems ──────────────────────────────────────────────────────────────

  /** Console errors, uncaught exceptions, failed requests and HTTP>=500 responses seen so far. */
  problems() {
    const out = []
    for (const m of this.consoleMessages) if (m.type === 'error') out.push(`console.error: ${m.text}`)
    for (const e of this.exceptions) out.push(`uncaught: ${e}`)
    for (const f of this.failedRequests) out.push(`request failed: ${f.url} (${f.error})`)
    for (const r of this.responses) if (r.status >= 500) out.push(`HTTP ${r.status}: ${r.method} ${r.url}`)
    return out
  }

  /** Throws if any problem was recorded, except those matching `ignore` (RegExp[]). */
  assertNoProblems({ ignore = [], label = '' } = {}) {
    const bad = this.problems().filter(p => !ignore.some(re => re.test(p)))
    if (bad.length) throw new Error(`${label ? `${label}: ` : ''}${bad.length} browser problem(s):\n  - ${bad.join('\n  - ')}`)
  }

  clearProblems() {
    this.consoleMessages = []
    this.exceptions = []
    this.failedRequests = []
    this.responses = []
  }

  // ── API access from inside the page session ───────────────────────────────

  /**
   * fetch() inside the page, so the session cookies apply; mutating calls carry the opskest.csrf cookie
   * as X-CSRF-Token exactly like the app does. Returns { status, ok, json, text, contentType }.
   */
  async api(method, urlPath, body) {
    return this.evaluate(async (method, urlPath, body) => {
      const headers = {}
      if (method !== 'GET') {
        const c = document.cookie.split(';').map(s => s.trim()).find(s => s.startsWith('opskest.csrf='))
        if (c) headers['X-CSRF-Token'] = decodeURIComponent(c.slice('opskest.csrf='.length))
      }
      let init = { method, credentials: 'include', headers }
      if (body !== undefined && body !== null) { headers['Content-Type'] = 'application/json'; init.body = JSON.stringify(body) }
      const res = await fetch(urlPath, init)
      const text = await res.text()
      let json = null
      try { json = JSON.parse(text) } catch { /* not JSON */ }
      return { status: res.status, ok: res.ok, json, text, contentType: res.headers.get('content-type') || '' }
    }, method, urlPath, body ?? null)
  }

  /** api() that throws unless the status is 2xx. Returns the parsed JSON. */
  async apiOk(method, urlPath, body) {
    const r = await this.api(method, urlPath, body)
    if (!r.ok) throw new Error(`${method} ${urlPath} -> ${r.status}: ${r.text.slice(0, 300)}`)
    return r.json
  }

  // ── screenshots ───────────────────────────────────────────────────────────

  async screenshot(name = 'shot') {
    const { data } = await this.send('Page.captureScreenshot', { format: 'png' }, 15000)
    const file = path.join(artifactsDir(), `${Date.now()}-${name.replace(/[^\w.-]+/g, '_')}.png`)
    writeFileSync(file, Buffer.from(data, 'base64'))
    return file
  }

  async close() {
    if (this.closed) return
    this.closed = true
    this.context.pages.delete(this)
    this.conn.sessions.delete(this.sessionId)
    await this.conn.send('Target.closeTarget', { targetId: this.targetId }).catch(() => {})
  }
}
