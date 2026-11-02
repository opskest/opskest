// Accessibility basics: skip link, accessible names, dialog focus trap and Escape, visible focus ring.

import assert from 'node:assert/strict'
import { it } from 'node:test'
import { e2eSuite, unique } from './harness.mjs'
import { monitorBody } from './util.mjs'

e2eSuite('accessibility', e => {
  it('the skip link is the first tab stop and jumps to the main content', async t => {
    const page = await e.session(t, { goto: '/monitors' })
    await page.waitForElement({ role: 'heading', name: 'Monitor inventory' })
    await page.press('Tab')
    const first = await page.activeElement()
    assert.ok(first, 'something takes focus on the first Tab')
    assert.match(first.name, /skip to (main )?content/i, `first tab stop was ${JSON.stringify(first)}`)
    assert.equal(first.href, '#main')
    const box = await page.onElement({ css: 'a.skip-link', visible: false }, a => { const r = a.getBoundingClientRect(); return { w: r.width, h: r.height, top: r.top, left: r.left } })
    assert.ok(box.w > 10 && box.h > 10 && box.top > -5 && box.left > -5, `skip link must be on screen when focused: ${JSON.stringify(box)}`)
    await page.press('Enter')
    await page.waitFor(() => location.hash === '#main', { message: 'the skip link to navigate to #main' })
  })

  const PAGES = ['/', '/monitors', '/monitors/new', '/projects', '/incidents', '/maintenance', '/pages', '/pages/new', '/settings?tab=notifications', '/settings?tab=general', '/settings?tab=logs', '/account/security', '/third-party']
  it('every visible form control on the main pages has an accessible name', async t => {
    const page = await e.session(t)
    const bad = []
    for (const p of PAGES) {
      await page.goto(e.url(p))
      await page.waitForElement('main')
      const missing = await page.evaluate(() => {
        const L = window.__e2e
        return [...document.querySelectorAll('input, select, textarea, button, [role=button], [role=checkbox], [role=radio]')]
          .filter(el => L.visible(el) && el.type !== 'hidden')
          .filter(el => !L.accName(el))
          .map(el => L.describe(el))
      })
      for (const m of missing) bad.push(`${p}: ${m}`)
    }
    assert.deepEqual(bad, [], 'controls without an accessible name')
  })

  it('dialogs trap focus, close on Escape and return focus to the opener', async t => {
    const page = await e.session(t, { goto: '/settings?tab=notifications' })
    const opener = { role: 'button', name: /Add alert channel|Add First Channel/ }
    await page.waitForElement(opener)
    await page.focus(opener)
    await page.press('Enter')
    await page.waitForElement({ role: 'dialog' })
    assert.equal(await page.onElement({ css: '[role=dialog]' }, d => d.getAttribute('aria-modal')), 'true')
    assert.ok(await page.onElement({ css: '[role=dialog]' }, d => !!document.getElementById(d.getAttribute('aria-labelledby') || '_')?.textContent), 'the dialog is labelled by its title')
    const inside = () => page.evaluate(() => !!document.activeElement?.closest('[role=dialog]'))
    assert.equal(await inside(), true, 'focus moves into the dialog on open')
    for (let i = 0; i < 30; i++) {
      await page.press('Tab')
      assert.equal(await inside(), true, `Tab #${i + 1} escaped the dialog`)
    }
    for (let i = 0; i < 30; i++) {
      await page.press('Shift+Tab')
      assert.equal(await inside(), true, `Shift+Tab #${i + 1} escaped the dialog`)
    }
    await page.press('Escape')
    await page.waitForGone({ role: 'dialog' })
    const active = await page.activeElement()
    assert.match(active?.name ?? '', /Add (alert channel|First Channel)/, `focus returns to the opener, got ${JSON.stringify(active)}`)
  })

  it('a confirmation dialog (bulk delete) also closes on Escape', async t => {
    const name = unique('a11y')
    await e.app.apiOk('POST', '/api/monitors', monitorBody({ name, type: 'tcp', target: e.target.tcpAddr, paused: true }))
    const page = await e.session(t, { goto: '/monitors' })
    await page.click({ role: 'checkbox', name: `Select ${name}` })
    await page.click({ role: 'button', name: 'Delete' })
    await page.waitForElement({ role: 'dialog' })
    await page.press('Escape')
    await page.waitForGone({ role: 'dialog' })
    assert.ok(await page.exists({ role: 'link', name }), 'Escape cancels; nothing was deleted')
  })

  it('keyboard focus shows a visible ring on links, buttons and inputs', async t => {
    const page = await e.session(t, { goto: '/monitors/new' })
    await page.waitForElement({ role: 'heading', name: 'Add monitor' })
    const bad = []
    for (let i = 0; i < 14; i++) {
      await page.press('Tab')
      const r = await page.evaluate(() => {
        const a = document.activeElement
        if (!a || a === document.body) return null
        const cs = getComputedStyle(a)
        const ring = (cs.outlineStyle !== 'none' && parseFloat(cs.outlineWidth) > 0) || (cs.boxShadow && cs.boxShadow !== 'none')
        return { desc: window.__e2e.describe(a), ring: !!ring, focusVisible: a.matches(':focus-visible'), outline: `${cs.outlineStyle} ${cs.outlineWidth}`, shadow: cs.boxShadow }
      })
      if (r && r.focusVisible && !r.ring) bad.push(`${r.desc}: outline "${r.outline}", box-shadow "${r.shadow}"`)
    }
    assert.deepEqual(bad, [], 'focused elements without a visible focus indicator')
  })
})
