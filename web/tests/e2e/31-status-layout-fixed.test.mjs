// A status page is a status page or a wallboard from the day it is created. The two are never
// interchangeable: the other view's address is "not found", nothing links across, the API refuses
// a layout change, and the editor only offers the choice when creating.

import assert from 'node:assert/strict'
import { it } from 'node:test'
import { e2eSuite, unique } from './harness.mjs'
import { monitorBody } from './util.mjs'

const pageBody = (slug, title, over = {}) => ({
  slug, title, description: 'layout page', visibility: 'public', layout: 'list', showLatency: false,
  alarmMuted: true, alarmDown: 'police-siren', alarmDegraded: 'modern-chime',
  logoUrl: '', brandColor: '', websiteUrl: '', supportUrl: '', footerText: '', showResponseTime: false, historyDays: 90,
  subscriptionsEnabled: false, subscriberChannelId: null, items: [], ...over,
})

/** In the page: is the "Wallboard audio" section visible? */
function audioShown() {
  const h = [...document.querySelectorAll('h2, h3')].find(x => x.textContent.trim() === 'Wallboard audio')
  return !!h && h.offsetParent !== null
}

e2eSuite('status page layout is fixed', e => {
  async function make(layout) {
    const mon = await e.app.apiOk('POST', '/api/monitors', monitorBody({ name: unique('svc'), type: 'tcp', target: e.target.tcpAddr, intervalS: 10, timeoutMs: 5000 }))
    const slug = unique(layout)
    const title = `Layout ${slug}`
    const created = await e.app.apiOk('POST', '/api/pages', pageBody(slug, title, { layout, items: [{ sort: 0, groupId: null, monitorId: mon.id, displayName: 'Checkout', description: '' }] }))
    return { slug, title, id: created.id, mon }
  }

  it('a list page has no wallboard: the board address is not found and nothing links to it', async t => {
    const { slug, title } = await make('list')
    const status = (await fetch(e.url(`/status/${slug}/board`))).status
    assert.equal(status, 404, 'the board address is a real 404 for a list page')
    const page = await e.anonymous(t, { goto: `/status/${slug}/board` })
    await page.waitForElement({ role: 'heading', name: 'Page not found', level: 1 })
    assert.ok(!(await page.exists({ css: '[aria-label="Overall status"]' })), 'no board or status content is shown')

    await page.goto(e.url(`/status/${slug}`))
    await page.waitForElement({ role: 'heading', name: title, level: 1 })
    const links = await page.evaluate(() => [...document.querySelectorAll('a')].map(a => a.getAttribute('href') ?? '').filter(h => /\/board(\/|$)/.test(h)))
    assert.deepEqual(links, [], 'a list page links to no wallboard')
    assert.ok(!(await page.exists({ role: 'link', name: 'Wallboard' })))
    for (const sub of ['history']) {
      await page.goto(e.url(`/status/${slug}/${sub}`))
      await page.waitForElement({ role: 'heading', name: 'Incident history' })
      assert.equal(await page.evaluate(() => [...document.querySelectorAll('a')].filter(a => /\/board/.test(a.getAttribute('href') ?? '')).length), 0)
    }
  })

  it('a board page serves only the board: the status address moves to it, with no way back', async t => {
    const { slug } = await make('board')
    assert.equal((await fetch(e.url(`/status/${slug}/board`))).status, 200)
    const page = await e.anonymous(t, { goto: `/status/${slug}` })
    await page.waitFor(() => location.pathname.endsWith('/board'), { message: 'a client-side replace to the board' })
    await page.waitForElement({ css: 'main' })
    assert.ok(!(await page.exists({ role: 'link', name: 'Status page' })), 'the board has no link to a status page view')
    // Replaced, not pushed: Back leaves the page rather than bouncing between the two.
    assert.equal(await page.evaluate(() => history.length <= 2), true)
    // Its incident history is a document, not the list view; it works and points back to the board.
    await page.goto(e.url(`/status/${slug}/history`))
    await page.waitForElement({ role: 'heading', name: 'Incident history' })
    const hrefs = await page.evaluate(() => [...document.querySelectorAll('header a, main > a')].map(a => a.getAttribute('href')))
    assert.ok(hrefs.some(h => h?.endsWith(`/status/${slug}/board`)), 'the history page links back to the wallboard')
    assert.ok(!hrefs.includes(`/status/${slug}`), 'and never to the list view')
  })

  it('the update API refuses a layout change, keeps the page, and still saves everything else', async () => {
    const list = await make('list')
    const board = await make('board')
    for (const [p, want] of [[list, 'board'], [board, 'list']]) {
      const r = await e.app.api('PUT', `/api/pages/${p.id}`, pageBody(p.slug, 'Renamed', { layout: want }))
      assert.equal(r.status, 400)
      assert.match(r.text, /layout cannot be changed/)
      assert.equal((await e.app.apiOk('GET', `/api/pages/${p.id}`)).layout, want === 'board' ? 'list' : 'board')
    }
    const ok = await e.app.api('PUT', `/api/pages/${list.id}`, pageBody(list.slug, 'Renamed title', { layout: 'list' }))
    assert.equal(ok.status, 200)
    const saved = await e.app.apiOk('GET', `/api/pages/${list.id}`)
    assert.equal(saved.title, 'Renamed title')
    assert.equal(saved.layout, 'list')
  })

  it('the editor offers the page type only when creating, and shows it read-only afterwards', async t => {
    const created = await e.session(t, { goto: '/pages/new' })
    await created.waitForElement({ role: 'heading', name: 'Create a status page' })
    assert.ok(await created.exists({ role: 'radio', name: /^Status page/ }) && await created.exists({ role: 'radio', name: /^Wallboard/ }), 'create flow: a choice of the two types')
    const options = await created.onElements({ role: 'radio', name: /^(Status page|Wallboard)/ }, el => el.value)
    assert.deepEqual(options, ['list', 'board'])

    const { id } = await make('list')
    const edit = await e.session(t, { goto: `/pages/${id}` })
    await edit.waitForElement({ role: 'link', name: 'Basics' })
    assert.ok(!(await edit.exists({ role: 'radio', name: /^Wallboard/ })), 'edit: no control to change it')
    await edit.waitForElement({ text: /cannot be changed/ })
    await edit.click({ role: 'link', name: 'Access' })
    assert.equal(await edit.evaluate(audioShown), false, 'wallboard options are for board pages')

    const board = await make('board')
    const editBoard = await e.session(t, { goto: `/pages/${board.id}` })
    await editBoard.waitForElement({ role: 'link', name: 'Access' })
    await editBoard.click({ role: 'link', name: 'Access' })
    await editBoard.waitFor(audioShown, { message: 'the wallboard audio section on a board page' })
  })

  it('the pages directory links to each page in its own view only', async t => {
    const { slug } = await make('board')
    const admin = await e.session(t, { goto: '/pages' })
    await admin.waitForElement({ text: `/status/${slug}/board` })
    const crossLinks = await admin.evaluate(s => [...document.querySelectorAll('a')].filter(a => a.getAttribute('href') === `/status/${s}`).length, slug)
    assert.equal(crossLinks, 0, 'a board page has no link to the status page view')
  })
})
