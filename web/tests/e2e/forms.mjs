// Helpers shared by the monitor-form test files (SQL and service monitors). Not a test file itself.

import { unique } from './harness.mjs'

export const NAV = 'nav[aria-label="Monitor configuration"]'
export const esc = s => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
export const radio = type => ({ css: `[role=radio][data-type="${type}"]` })
/** A form control located by its label: a string is matched exactly, a RegExp as given. */
export const field = label => ({ label: label instanceof RegExp ? label : new RegExp(`^${esc(label)}$`) })
export const checkedTypes = page => page.evaluate(() => [...document.querySelectorAll('[role=radio][data-type][aria-checked=true]')].map(r => r.dataset.type))
export const postsToMonitors = page => page.responses.filter(r => r.method === 'POST' && /\/api\/monitors(\?|$)/.test(r.url)).length

/** Opens the add-monitor form with every settings section visible. */
export async function openForm(e, t, path = '/monitors/new') {
  const page = await e.session(t, { goto: path })
  await page.waitForElement({ role: 'heading', name: /Add monitor|Edit/ })
  await page.click({ css: `${NAV} button`, text: 'All settings' })
  return page
}

/** Picks a type card and waits for its settings section. */
export async function pickType(page, type, label) {
  await page.click(radio(type))
  await page.waitForElement({ role: 'heading', name: `${label} settings` })
}

/**
 * The error message and aria-invalid state of the field whose label matches `re`. The message is the red
 * paragraph the Field component renders under the control (null when there is none).
 */
export function fieldState(page, re) {
  return page.evaluate((src, flags) => {
    const rx = new RegExp(src, flags)
    const label = [...document.querySelectorAll('label')].find(l => rx.test(l.innerText.replace(/\s+/g, ' ').trim()))
    if (!label) return { found: false }
    const box = label.parentElement
    const err = box.querySelector('p.text-danger')
    const control = label.htmlFor ? document.getElementById(label.htmlFor) : null
    return { found: true, error: err ? err.innerText.replace(/\s+/g, ' ').trim() : null, invalid: control?.getAttribute('aria-invalid') === 'true' }
  }, re.source, re.flags)
}

/** The visible text of the first <label> matching `re` (e.g. "Username Optional"), or null. */
export function labelText(page, re) {
  return page.evaluate((src, flags) => {
    const rx = new RegExp(src, flags)
    const label = [...document.querySelectorAll('label')].find(l => rx.test(l.innerText.replace(/\s+/g, ' ').trim()))
    return label ? label.innerText.replace(/\s+/g, ' ').trim() : null
  }, re.source, re.flags)
}

/** Sets an input's value the way a browser does for types that cannot be typed into (datetime-local, time). */
export function setNative(page, spec, value) {
  return page.onElement(spec, (el, v) => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(el, v)
    el.dispatchEvent(new Event('input', { bubbles: true }))
    el.dispatchEvent(new Event('change', { bubbles: true }))
    return el.value
  }, value)
}

/**
 * Clicks the form's submit button expecting the client to refuse it, then reopens "All settings" (a refused
 * submit jumps to the first error's section and hides the others). Resolves once the page has settled.
 */
export async function submitInvalid(page, button = 'Create monitor') {
  await page.click({ role: 'button', name: button })
  await page.waitForElement({ text: /Fix the highlighted fields first/ })
  await page.click({ css: `${NAV} button`, text: 'All settings' })
  await page.settle()
}

/** Creates a monitor through the open form: name + target, then "Create monitor"; resolves with the new id. */
export async function submitCreate(page) {
  await page.click({ role: 'button', name: 'Create monitor' })
  await page.waitForPath(/^\/monitors\/\d+$/)
  return Number((await page.path()).split('/').pop())
}

export { unique }
