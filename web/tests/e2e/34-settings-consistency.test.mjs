// Settings: every tab is drawn with the same scaffold, so switching tabs does not move anything.
// For each tab the left edge, width and top of the panel, the top of its title, the radius and the first
// section's top are measured and compared (within 1px), the scrollbar gutter is stable, nothing scrolls
// sideways, and the cumulative layout shift (PerformanceObserver) stays under 0.02.

import assert from 'node:assert/strict'
import { it } from 'node:test'
import { e2eSuite } from './harness.mjs'

const TABS = ['notifications', 'general', 'security', 'regions', 'database', 'data', 'import', 'logs', 'updates', 'about']

const CLS_SOURCE = `
  window.__cls = 0;
  try {
    new PerformanceObserver(list => {
      for (const e of list.getEntries()) if (!e.hadRecentInput) window.__cls += e.value
    }).observe({ type: 'layout-shift', buffered: true })
  } catch (e) {}
`

function measure() {
  const box = el => { const r = el.getBoundingClientRect(); return { left: r.left, top: r.top + scrollY, width: r.width, height: r.height } }
  const panel = document.querySelector('[data-settings-panel]')
  if (!panel) return null
  const title = panel.querySelector('h2')
  const header = panel.querySelector('header')
  const nav = document.querySelector('nav[aria-label="Settings sections"]')
  const h1 = document.querySelector('main h1')
  const firstSection = panel.querySelector(':scope > div > *')
  return {
    panel: box(panel),
    title: box(title),
    header: box(header),
    h1: box(h1),
    nav: nav ? box(nav) : null,
    radius: getComputedStyle(panel).borderTopLeftRadius,
    firstSectionTop: firstSection ? box(firstSection).top : null,
    firstSectionPad: firstSection ? getComputedStyle(firstSection).paddingLeft : null,
    gutter: innerWidth - document.documentElement.clientWidth,
    hOverflow: document.documentElement.scrollWidth - innerWidth,
    current: nav?.querySelector('[aria-current="page"]')?.textContent?.trim() ?? null,
    busy: document.querySelectorAll('[aria-busy="true"]').length,
  }
}

async function visit(e, t, viewport, tab) {
  const page = await e.session(t, { viewport })
  await page.send('Page.addScriptToEvaluateOnNewDocument', { source: CLS_SOURCE })
  await page.goto(e.url(`/settings?tab=${tab}`))
  await page.waitForElement('main')
  await page.waitFor(() => !!document.querySelector('[data-settings-panel]'), { message: `the ${tab} panel` })
  // Wait for the skeletons to be replaced by content, then give late layout a moment.
  await page.waitFor(() => document.querySelectorAll('[data-settings-panel] [aria-busy="true"]').length === 0, { timeout: 15000, message: `${tab} to finish loading` })
  await new Promise(r => setTimeout(r, 600))
  const m = await page.evaluate(measure)
  const cls = await page.evaluate(() => window.__cls)
  return { m, cls, page }
}

e2eSuite('settings consistency', e => {
  const results = {}

  it('every tab has the same frame and no layout shift (desktop)', async t => {
    for (const tab of TABS) {
      const { m, cls, page } = await visit(e, t, 'desktop', tab)
      assert.ok(m, `${tab}: panel rendered`)
      results[tab] = { m, cls }
      assert.ok(cls <= 0.02, `${tab}: cumulative layout shift ${cls.toFixed(4)} should be at most 0.02`)
      assert.equal(m.hOverflow <= 0, true, `${tab}: no horizontal overflow (${m.hOverflow})`)
      page.assertNoProblems()
    }
    const base = results[TABS[0]].m
    const near = (a, b, what, tab) => assert.ok(Math.abs(a - b) <= 1, `${tab}: ${what} ${a} differs from ${b} by more than 1px`)
    for (const tab of TABS) {
      const { m } = results[tab]
      near(m.panel.left, base.panel.left, 'panel left edge', tab)
      near(m.panel.width, base.panel.width, 'panel width', tab)
      near(m.panel.top, base.panel.top, 'panel top', tab)
      near(m.title.top, base.title.top, 'title top', tab)
      near(m.title.left, base.title.left, 'title left', tab)
      near(m.h1.top, base.h1.top, 'page heading top', tab)
      near(m.header.height, base.header.height, 'title row height', tab)
      near(m.firstSectionTop, base.firstSectionTop, 'first section top', tab)
      near(m.nav.left, base.nav.left, 'rail left', tab)
      near(m.nav.width, base.nav.width, 'rail width', tab)
      assert.equal(m.radius, base.radius, `${tab}: card radius`)
      assert.equal(m.firstSectionPad, base.firstSectionPad, `${tab}: section padding`)
      assert.equal(m.gutter, base.gutter, `${tab}: the scrollbar gutter is the same on every tab`)
      assert.equal(m.busy, 0, `${tab}: no skeleton left`)
    }
  })

  it('the rail marks the current tab and arrow keys move between items', async t => {
    const { page } = await visit(e, t, 'desktop', 'regions')
    assert.equal(await page.evaluate(() => document.querySelector('nav[aria-label="Settings sections"] [aria-current="page"]')?.textContent?.trim()), 'Check regions')
    await page.focus({ css: 'nav[aria-label="Settings sections"] [aria-current="page"]' })
    await page.press('ArrowDown')
    assert.equal(await page.evaluate(() => document.activeElement?.textContent?.trim()), 'Import & export')
    await page.press('Enter')
    await page.waitForPath('/settings?tab=import')
    await page.waitForElement({ role: 'heading', name: 'Import & export' })
  })

  it('on a phone the rail is a row that scrolls inside itself and the page does not', async t => {
    for (const tab of ['notifications', 'about', 'updates']) {
      const { m, cls, page } = await visit(e, t, 'mobile', tab)
      assert.ok(m.hOverflow <= 0, `${tab}@375: the page scrolls sideways by ${m.hOverflow}px`)
      assert.ok(cls <= 0.02, `${tab}@375: layout shift ${cls.toFixed(4)}`)
      assert.ok(m.panel.width <= 375, `${tab}@375: panel width ${m.panel.width}`)
      page.assertNoProblems()
    }
  })
})
