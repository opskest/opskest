// Small helpers shared by the e2e test files.

import { readFileSync } from 'node:fs'
import path from 'node:path'
import { repoRoot } from './harness.mjs'

/** A complete, valid MonitorIn body (what the form sends), with overrides. */
export function monitorBody(over = {}) {
  const { settings, ...rest } = over
  return {
    name: 'e2e monitor', type: 'http', target: 'http://127.0.0.1:9/', groupId: null, tagIds: [], channelIds: [],
    intervalS: 60, retryIntervalS: 20, retries: 0, timeoutMs: 5000, degradedMs: 0, sslWarnDays: 14, reminderMin: 0,
    upsideDown: false, notifyDegraded: false, paused: false, description: '', runbook: '',
    headers: [], secrets: { basicPass: null, bearerToken: null },
    settings: { ...(settings ?? {}) },
    ...rest,
  }
}

/** The monitor types the server accepts, parsed from internal/model/monitor.go (AllTypes), in Go order. */
export function goMonitorTypes() {
  const src = readFileSync(path.join(repoRoot, 'internal/model/monitor.go'), 'utf8')
  const consts = new Map()
  for (const m of src.matchAll(/^\s*(Type\w+)\s+MonitorType\s*=\s*"([^"]+)"/gm)) consts.set(m[1], m[2])
  const block = /var AllTypes = \[\]MonitorType\{([\s\S]*?)\n\}/.exec(src)
  if (!block) throw new Error('could not find AllTypes in internal/model/monitor.go')
  const names = block[1].match(/Type\w+/g) ?? []
  const types = names.map(n => {
    if (!consts.has(n)) throw new Error(`AllTypes references unknown constant ${n}`)
    return consts.get(n)
  })
  return types
}

/**
 * Real sign-in through the login form of a page that is not signed in.
 */
export async function signInViaForm(page, baseUrl, user, password) {
  await page.waitForElement({ css: 'input[name=username]' })
  await page.fill({ css: 'input[name=username]' }, user)
  await page.fill({ css: 'input[name=password]' }, password)
  await page.click({ role: 'button', name: 'Sign in', css: 'button[type=submit]' })
}

/** Relative luminance contrast ratio between two [r,g,b] colours. */
export function contrastRatio(a, b) {
  const lum = ([r, g, bl]) => {
    const f = v => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4 }
    return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(bl)
  }
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

/**
 * Runs in the page: the computed text colour of `el` composited over the first opaque ancestor background,
 * and the contrast ratio between them. Alpha channels are honoured.
 */
export function inPageContrast(el) {
  const parse = s => {
    const m = s.match(/rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)(?:\s*[,/]\s*([\d.]+%?))?\s*\)/)
    if (!m) return null
    let a = m[4] === undefined ? 1 : m[4].endsWith('%') ? parseFloat(m[4]) / 100 : parseFloat(m[4])
    return [parseFloat(m[1]), parseFloat(m[2]), parseFloat(m[3]), a]
  }
  const over = (top, bottom) => {
    const a = top[3] + bottom[3] * (1 - top[3])
    if (a === 0) return [0, 0, 0, 0]
    return [0, 1, 2].map(i => (top[i] * top[3] + bottom[i] * bottom[3] * (1 - top[3])) / a).concat(a)
  }
  const layers = []
  for (let n = el; n; n = n.parentElement) {
    const bg = parse(getComputedStyle(n).backgroundColor)
    if (bg && bg[3] > 0) { layers.push(bg); if (bg[3] >= 1) break }
  }
  let bg = [255, 255, 255, 1]
  if (layers.length && layers[layers.length - 1][3] >= 1) bg = layers.pop()
  else if (document.documentElement.classList.contains('dark') || matchMedia('(prefers-color-scheme: dark)').matches) {
    bg = parse(getComputedStyle(document.body).backgroundColor) ?? bg
  }
  while (layers.length) bg = over(layers.pop(), bg)
  const cs = getComputedStyle(el)
  let fg = parse(cs.color) ?? [0, 0, 0, 1]
  const opacity = parseFloat(cs.opacity)
  if (opacity < 1) fg = [fg[0], fg[1], fg[2], fg[3] * opacity]
  const eff = over(fg, bg)
  const lum = ([r, g, b]) => {
    const f = v => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4 }
    return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b)
  }
  const [hi, lo] = [lum(eff), lum(bg)].sort((x, y) => y - x)
  return { ratio: (hi + 0.05) / (lo + 0.05), fg: eff.slice(0, 3).map(Math.round), bg: bg.slice(0, 3).map(Math.round), size: parseFloat(cs.fontSize), weight: cs.fontWeight }
}
