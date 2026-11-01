// Settings tables: the buttons of one action cell share one size and shape, never wrap at 1280px, and
// every table uses the shared header/cell type scale (the same on every tab).

import assert from 'node:assert/strict'
import { it } from 'node:test'
import { e2eSuite } from './harness.mjs'

const TABS = ['notifications', 'security', 'database', 'logs', 'regions', 'data', 'import']

const audit = () => {
  const out = { cells: [], heads: [], bodies: [], tables: 0, hOverflow: document.documentElement.scrollWidth - innerWidth }
  for (const cell of document.querySelectorAll('[data-settings-panel] [data-row-actions], [data-settings-panel] [data-channel-row] > div:last-child')) {
    const items = [...cell.querySelectorAll('button, a')].filter(b => b.offsetParent && !b.closest('[role=menu]'))
    if (items.length === 0) continue
    const box = items.map(b => { const r = b.getBoundingClientRect(); const cs = getComputedStyle(b); return { h: Math.round(r.height), top: Math.round(r.top), radius: cs.borderTopLeftRadius, font: cs.fontSize, text: b.textContent.trim() } })
    out.cells.push(box)
  }
  for (const table of document.querySelectorAll('[data-settings-panel] table')) {
    out.tables++
    const th = table.querySelector('th')
    const td = table.querySelector('tbody td:not([colspan])')
    if (th) out.heads.push(getComputedStyle(th).fontSize + '/' + getComputedStyle(th).textTransform)
    if (td) out.bodies.push(getComputedStyle(td).fontSize)
  }
  return out
}

e2eSuite('settings row actions', e => {
  it('buttons in one action cell are the same size, shape and line; tables share one type scale', async t => {
    await e.app.api('POST', '/api/backups') // one snapshot to act on
    const heads = new Set()
    const bodies = new Set()
    for (const viewport of ['desktop']) {
      for (const tab of TABS) {
        const page = await e.session(t, { viewport })
        await page.goto(e.url(`/settings?tab=${tab}`))
        await page.waitFor(() => !!document.querySelector('[data-settings-panel]'), { message: tab })
        await page.waitFor(() => document.querySelectorAll('[data-settings-panel] [aria-busy="true"]').length === 0, { timeout: 15000, message: `${tab} loaded` })
        await new Promise(r => setTimeout(r, 400))
        const a = await page.evaluate(audit)
        assert.ok(a.hOverflow <= 0, `${tab}: page overflow ${a.hOverflow}`)
        for (const cell of a.cells) {
          assert.equal(new Set(cell.map(b => b.h)).size, 1, `${tab}: buttons in one action cell differ in height ${JSON.stringify(cell)}`)
          assert.equal(new Set(cell.map(b => b.radius)).size, 1, `${tab}: radius differs ${JSON.stringify(cell)}`)
          assert.equal(new Set(cell.map(b => b.font)).size, 1, `${tab}: font size differs ${JSON.stringify(cell)}`)
          assert.equal(new Set(cell.map(b => b.top)).size, 1, `${tab}: an action wrapped to a second line ${JSON.stringify(cell)}`)
        }
        a.heads.forEach(h => heads.add(h))
        a.bodies.forEach(b => bodies.add(b))
        page.assertNoProblems()
      }
    }
    assert.equal(heads.size, 1, `table headers use one style: ${[...heads]}`)
    assert.equal(bodies.size, 1, `table cells use one font size: ${[...bodies]}`)
  })

  it('the snapshot row has Download, Restore and Delete at the same size', async t => {
    await e.app.api('POST', '/api/backups')
    const page = await e.session(t, { goto: '/settings?tab=database' })
    await page.waitForElement({ role: 'button', name: /^Restore$/ })
    const sizes = await page.evaluate(() => {
      const cell = document.querySelector('[data-row-actions]')
      return [...cell.children].map(b => b.getBoundingClientRect().height)
    })
    assert.equal(sizes.length, 3)
    assert.equal(new Set(sizes.map(Math.round)).size, 1, `heights ${sizes}`)
  })
})

// Every modal fits the window: header and footer buttons inside the viewport, height <= viewport - 32px.
e2eSuite('modals fit the window', e => {
  const fit = () => {
    const d = document.querySelector('[data-modal]')
    if (!d) return null
    const r = d.getBoundingClientRect()
    const btns = [...d.querySelectorAll('header button, footer button')].map(b => b.getBoundingClientRect())
    return {
      height: r.height, vh: innerHeight, top: r.top, bottom: r.bottom,
      outside: btns.filter(b => b.top < 0 || b.bottom > innerHeight || b.right > innerWidth || b.left < 0).length,
      buttons: btns.length,
    }
  }
  for (const size of [{ width: 1280, height: 720 }, { width: 1920, height: 1080 }]) {
    it(`the maintenance window form fits ${size.width}x${size.height}`, async t => {
      const page = await e.session(t, { viewport: size })
      await page.goto(e.url('/maintenance'))
      await page.waitForElement({ role: 'heading', name: 'Maintenance' })
      await page.click({ role: 'button', name: /New (maintenance )?window|Schedule|Add/i })
      await page.waitForElement({ role: 'dialog' })
      await new Promise(r => setTimeout(r, 400))
      const m = await page.evaluate(fit)
      assert.ok(m.height <= m.vh - 32 + 1, `modal height ${m.height} exceeds ${m.vh - 32}`)
      assert.ok(m.top >= 0 && m.bottom <= m.vh, `modal spans ${m.top}..${m.bottom} of ${m.vh}`)
      assert.equal(m.outside, 0, 'header and footer buttons are inside the window')
      assert.ok(m.buttons >= 3, `expected close, cancel and save buttons, got ${m.buttons}`)
    })
  }
})
