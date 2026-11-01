// Unknown ids show a clear "not found" page with a way back; destructive actions ask in an in-app dialog
// (never window.confirm); dialogs return focus to what opened them; and the renamed account/overview entries.

import assert from 'node:assert/strict'
import { it } from 'node:test'
import { e2eSuite, unique } from './harness.mjs'
import { monitorBody } from './util.mjs'

const dialog = { role: 'dialog' }
const inDialog = spec => ({ ...spec, within: dialog })

e2eSuite('not found and dialogs', e => {
  const NOT_FOUND = [
    { path: '/monitors/99999', title: 'Monitor not found', back: 'Back to monitors', backTo: '/monitors' },
    { path: '/monitors/99999/edit', title: 'Monitor not found', back: 'Back to monitors', backTo: '/monitors' },
    { path: '/projects/99999', title: 'Project not found', back: 'Back to projects', backTo: '/projects' },
    { path: '/incidents/99999', title: 'Incident not found', back: 'Back to incidents', backTo: '/incidents' },
    { path: '/pages/99999', title: 'Status page not found', back: 'Back to status pages', backTo: '/pages' },
  ]
  for (const c of NOT_FOUND) {
    it(`${c.path} says "${c.title}" and offers a way back`, async t => {
      const page = await e.session(t, { goto: c.path })
      await page.waitForElement({ text: c.title })
      const main = await page.text('main')
      assert.match(main, new RegExp(c.title))
      assert.doesNotMatch(main, /Something went wrong|undefined|NaN|\[object/i, 'a clear message, not a crash or a half-rendered page')
      assert.ok(main.trim().length > 20)
      // It is not an empty frame: the shell still has its navigation.
      assert.ok(await page.exists({ role: 'navigation' }), 'the app shell is still there')
      await page.click({ role: 'button', name: c.back })
      await page.waitForPath(c.backTo)
      await page.waitForElement('main')
      assert.doesNotMatch(await page.text('main'), /not found/i, 'the way back lands on a working page')
      page.assertNoProblems({ ignore: [/status of 404/] })
    })
  }

  for (const c of NOT_FOUND) {
    it(`${c.path}: the "${c.title}" message is a heading, so screen-reader users land on it`, async t => {
      const page = await e.session(t, { goto: c.path })
      await page.waitForElement({ text: c.title })
      const headings = await page.onElements({ role: 'heading' }, h => h.textContent.trim())
      assert.ok(headings.includes(c.title), `a heading says "${c.title}" (headings on the page: ${JSON.stringify(headings)})`)
      assert.equal(await page.count({ css: 'h1' }), 1, 'exactly one <h1>, like every other page')
    })
  }

  it('non-numeric ids (/monitors/abc, /projects/abc, /incidents/abc) do not crash either', async t => {
    const page = await e.session(t, { goto: '/' })
    for (const p of ['/monitors/abc', '/projects/abc', '/incidents/abc', '/pages/abc']) {
      await page.goto(e.url(p))
      await page.waitForElement('main')
      await page.waitFor(() => /not found/i.test(document.querySelector('main')?.innerText ?? ''), { message: `${p} to say that it was not found` })
      assert.doesNotMatch(await page.text('main'), /Something went wrong|NaN|undefined/i, p)
    }
    page.assertNoProblems({ ignore: [/status of 4\d\d/] })
  })

  it('unknown ids are also "not found" for viewers, and a deleted monitor\'s page turns into one', async t => {
    const mon = await e.app.apiOk('POST', '/api/monitors', monitorBody({ name: unique('vanishing'), type: 'tcp', target: e.target.tcpAddr, intervalS: 10 }))
    const page = await e.session(t, { goto: `/monitors/${mon.id}` })
    await page.waitForElement({ role: 'heading', name: mon.name })
    await e.app.apiOk('DELETE', `/api/monitors/${mon.id}`)
    await page.goto(e.url(`/monitors/${mon.id}`))
    await page.waitForElement({ text: 'Monitor not found' })
    page.assertNoProblems({ ignore: [/status of 404/] })
  })

  it('deleting a monitor from its page asks in an in-app dialog with a Delete button, never window.confirm', async t => {
    const mon = await e.app.apiOk('POST', '/api/monitors', monitorBody({ name: unique('delete-me'), type: 'tcp', target: e.target.tcpAddr, intervalS: 10 }))
    const page = await e.session(t, { goto: `/monitors/${mon.id}` })
    page.dialogAction = 'dismiss' // if a native confirm() ever appears it is dismissed, and recorded in page.dialogs
    await page.waitForElement({ role: 'heading', name: mon.name })
    const opener = { role: 'button', name: 'Delete', exact: true }
    await page.click(opener)
    await page.waitForElement({ role: 'dialog', name: `Delete ${mon.name}?` })
    assert.equal(page.dialogs.length, 0, 'no native dialog')
    assert.equal(await page.onElement(dialog, d => d.getAttribute('aria-modal')), 'true')
    const buttons = await page.onElements(inDialog({ css: 'button' }), b => b.textContent.trim() || b.getAttribute('aria-label'))
    assert.ok(buttons.includes('Cancel'), 'has Cancel')
    assert.ok(buttons.some(b => /^Delete/.test(b)), `has a Delete button (got ${JSON.stringify(buttons)})`)
    assert.match(await page.text(dialog), /can't be undone|cannot be undone/i, 'says it is permanent')
    // Focus is inside the dialog and Escape closes it without deleting.
    assert.equal(await page.evaluate(() => document.querySelector('[role=dialog]').contains(document.activeElement)), true)
    await page.press('Escape')
    await page.waitForGone(dialog)
    assert.equal((await e.app.api('GET', `/api/monitors/${mon.id}`)).status, 200, 'Escape does not delete')
    // Cancel keeps it too.
    await page.click(opener)
    await page.click(inDialog({ role: 'button', name: 'Cancel' }))
    await page.waitForGone(dialog)
    assert.equal((await e.app.api('GET', `/api/monitors/${mon.id}`)).status, 200)
    // Confirming deletes it and leaves for the list.
    await page.click(opener)
    await page.click(inDialog({ role: 'button', name: /^Delete/ }))
    await page.waitForPath('/monitors')
    assert.equal((await e.app.api('GET', `/api/monitors/${mon.id}`)).status, 404)
    assert.equal(page.dialogs.length, 0, 'window.confirm was never used')
    page.assertNoProblems({ ignore: [/status of 404/] })
  })

  it('deleting a status page asks in an in-app dialog; Cancel keeps it, "Delete page" removes it', async t => {
    const slug = unique('del-page')
    const created = await e.app.apiOk('POST', '/api/pages', { slug, title: `Doomed ${slug}`, description: '', visibility: 'public', layout: 'list', showLatency: false, alarmMuted: true, alarmDown: 'police-siren', alarmDegraded: 'modern-chime', logoUrl: '', brandColor: '', websiteUrl: '', supportUrl: '', footerText: '', showResponseTime: false, historyDays: 90, subscriptionsEnabled: false, subscriberChannelId: null, items: [] })
    const id = created.id ?? (await e.app.apiOk('GET', '/api/pages')).pages.find(p => p.slug === slug).id
    const page = await e.session(t, { goto: `/pages/${id}` })
    page.dialogAction = 'dismiss'
    await page.waitForElement({ role: 'heading', name: `Doomed ${slug}` })
    await page.click({ role: 'button', name: /^Delete/ })
    await page.waitForElement({ role: 'dialog', name: 'Delete this status page?' })
    assert.match(await page.text(dialog), new RegExp(`/status/${slug}`), 'names the address that will stop working')
    await page.click(inDialog({ role: 'button', name: 'Cancel' }))
    await page.waitForGone(dialog)
    assert.equal((await e.app.api('GET', `/api/pages/${id}`)).status, 200)
    await page.click({ role: 'button', name: /^Delete/ })
    await page.click(inDialog({ role: 'button', name: 'Delete page' }))
    await page.waitForPath('/pages')
    assert.equal((await e.app.api('GET', `/api/pages/${id}`)).status, 404)
    assert.equal((await fetch(`http://127.0.0.1:${e.app.port}/api/public/pages/${slug}`)).status, 404, 'the public page is gone')
    assert.equal(page.dialogs.length, 0, 'window.confirm was never used')
    page.assertNoProblems({ ignore: [/status of 404/] })
  })

  // Focus must go back to the control that opened a dialog, whether it is closed with Escape or with Cancel.
  const OPENERS = [
    { name: 'Declare incident on /incidents', path: '/incidents', opener: { role: 'button', name: 'Declare incident' }, title: 'New incident' },
    { name: 'New window on /maintenance', path: '/maintenance', opener: { role: 'button', name: /^New window/ }, title: 'New maintenance window' },
    { name: 'Delete on a monitor page', path: null, opener: { role: 'button', name: 'Delete', exact: true }, title: /^Delete .+\?$/ },
  ]
  for (const c of OPENERS) {
    for (const how of ['Escape', 'Cancel']) {
      it(`${c.name}: focus returns to the opener after ${how}`, async t => {
        let path = c.path
        if (!path) {
          const mon = await e.app.apiOk('POST', '/api/monitors', monitorBody({ name: unique('focus-svc'), type: 'tcp', target: e.target.tcpAddr, intervalS: 10 }))
          path = `/monitors/${mon.id}`
        }
        const page = await e.session(t, { goto: path })
        await page.waitForElement(c.opener)
        await page.click(c.opener)
        await page.waitForElement({ role: 'dialog', name: c.title })
        assert.equal(await page.evaluate(() => document.querySelector('[role=dialog]').contains(document.activeElement)), true, 'focus moves into the dialog')
        if (how === 'Escape') await page.press('Escape')
        else await page.click(inDialog({ role: 'button', name: 'Cancel' }))
        await page.waitForGone(dialog)
        const active = await page.activeElement()
        const want = await page.onElement(c.opener, el => el.innerText.trim())
        assert.equal(active?.tag, 'button', `focus returns to a button, got ${JSON.stringify(active)}`)
        assert.equal(active?.text, want, `focus returns to the opener "${want}", got ${JSON.stringify(active)}`)
        page.assertNoProblems()
      })
    }
  }

  it('/account/profile redirects to /account/security and the account menu has no Profile entry', async t => {
    const page = await e.session(t, { goto: '/account/profile' })
    await page.waitForPath('/account/security')
    await page.waitForElement({ role: 'heading', name: /security/i })
    // The redirect replaced the history entry: Back does not bounce through /account/profile again.
    await page.goto(e.url('/monitors'))
    await page.goto(e.url('/account/profile'))
    await page.waitForPath('/account/security')
    await page.evaluate(() => history.back())
    await page.waitForPath('/monitors')
    // The account menu.
    await page.waitForElement('main')
    await page.click({ role: 'button', name: /^Account:/ })
    await page.waitForElement({ css: '[role=menu][aria-label=Account]' })
    const items = await page.onElements({ css: '[role=menu][aria-label=Account] [role=menuitem]' }, el => el.textContent.trim())
    assert.deepEqual(items, ['Account security', 'Workspace settings', 'Sign out'])
    assert.ok(!items.some(i => /profile/i.test(i)), 'no Profile item')
    await page.press('Escape')
    await page.waitForGone({ css: '[role=menu][aria-label=Account]' })
    assert.match((await page.activeElement())?.name ?? '', /^Account:/, 'Escape returns focus to the account button')
    page.assertNoProblems()
  })

  it('the Overview summary counts "Active maintenance", "Active outages" and "Open incidents"', async t => {
    const page = await e.session(t, { goto: '/' })
    await page.waitForElement({ css: 'nav[aria-label="Operations summary"]' })
    const text = await page.text({ css: 'nav[aria-label="Operations summary"]' })
    for (const label of ['Active outages', 'Open incidents', 'Active maintenance']) assert.match(text, new RegExp(label), `the summary has "${label}"`)
    assert.doesNotMatch(text, /Maintenance now|In maintenance\b/, 'the old wording is gone')
    // Each tile links to where the numbers come from.
    const links = await page.onElements({ css: 'nav[aria-label="Operations summary"] a' }, a => ({ text: a.innerText.replace(/\s+/g, ' ').trim(), href: a.getAttribute('href') }))
    assert.equal(links.find(l => /Active maintenance/.test(l.text))?.href, '/maintenance')
    assert.equal(links.find(l => /Open incidents/.test(l.text))?.href, '/incidents')
    assert.equal(links.find(l => /Active outages/.test(l.text))?.href, '/incidents?tab=outages')
    page.assertNoProblems()
  })
})
