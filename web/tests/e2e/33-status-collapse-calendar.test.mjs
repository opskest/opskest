// Public status pages start collapsed, open on request (buttons, Expand all, a #group link, find in
// page), and show maintenance and incident days in a keyboard-friendly calendar in a chosen time zone.

import assert from 'node:assert/strict'
import { it } from 'node:test'
import { e2eSuite, unique } from './harness.mjs'
import { monitorBody } from './util.mjs'

const pageBody = (slug, title, over = {}) => ({
  slug, title, description: 'collapse page', visibility: 'public', layout: 'list', showLatency: false,
  alarmMuted: true, alarmDown: 'police-siren', alarmDegraded: 'modern-chime',
  logoUrl: '', brandColor: '', websiteUrl: '', supportUrl: '', footerText: '', showResponseTime: false, historyDays: 90,
  subscriptionsEnabled: false, subscriberChannelId: null, items: [], ...over,
})

/** In the page: is a paragraph containing `text` rendered (not inside a collapsed, hidden part)? */
function shown(text) {
  const el = [...document.querySelectorAll('p')].find(p => p.textContent.includes(text))
  return !!el && !el.closest('[hidden]') && el.offsetParent !== null
}

e2eSuite('status page collapsed by default, calendar', e => {
  async function make(settings) {
    const gA = await e.app.apiOk('POST', '/api/groups', { name: unique('GroupA') })
    const gB = await e.app.apiOk('POST', '/api/groups', { name: unique('GroupB') })
    const mons = []
    for (const [name, group] of [['Alpha service', gA], ['Beta service', gB]]) {
      mons.push(await e.app.apiOk('POST', '/api/monitors', monitorBody({ name: unique(name), type: 'tcp', target: e.target.tcpAddr, intervalS: 10, timeoutMs: 5000, groupId: group.id })))
    }
    const slug = unique('fold')
    const body = pageBody(slug, `Fold ${slug}`, { items: [{ sort: 0, groupId: gA.id, monitorId: null, displayName: '', description: '' }, { sort: 1, groupId: gB.id, monitorId: null, displayName: '', description: '' }] })
    if (settings) body.settings = settings
    const created = await e.app.apiOk('POST', '/api/pages', body)
    return { slug, id: created.id, gA, gB, mons }
  }
  const groupButtons = '[aria-expanded][aria-controls^="fold-"]'

  it('every group starts collapsed, even with a problem; Expand all and Collapse all work', async t => {
    const { slug, gA } = await make()
    const page = await e.anonymous(t, { goto: `/status/${slug}` })
    await page.waitFor(sel => document.querySelectorAll(sel).length >= 2, { args: [groupButtons], message: 'two group headers' })
    const state = () => page.evaluate(sel => [...document.querySelectorAll(sel)].map(b => b.getAttribute('aria-expanded')), groupButtons)
    assert.deepEqual(await state(), ['false', 'false'], 'collapsed on load')
    // The collapsed header still says what the status is, in words, and shows a summary strip.
    const head = await page.evaluate(sel => document.querySelector(sel).innerText, groupButtons)
    assert.match(head, /1 service/)
    assert.match(head, /Operational|outage|Degraded|Under maintenance|operational/i)
    assert.ok(await page.exists({ css: '[role=img][aria-label*="daily uptime"]' }))
    // A group opens with its own button and closes again.
    await page.click({ css: `${groupButtons}` })
    assert.deepEqual(await state(), ['true', 'false'])
    await page.click({ role: 'button', name: 'Expand all' })
    await page.waitFor(sel => [...document.querySelectorAll(sel)].every(b => b.getAttribute('aria-expanded') === 'true'), { args: [groupButtons] })
    await page.click({ role: 'button', name: 'Collapse all' })
    await page.waitFor(sel => [...document.querySelectorAll(sel)].every(b => b.getAttribute('aria-expanded') === 'false'), { args: [groupButtons] })
    // Collapsed bodies stay in the page for find-in-page.
    const kept = await page.evaluate(sel => [...document.querySelectorAll(sel)].map(b => document.getElementById(b.getAttribute('aria-controls'))?.getAttribute('hidden')), groupButtons)
    assert.deepEqual(kept, ['until-found', 'until-found'])
    // A deep link opens and scrolls to its group.
    await page.evaluate(g => { location.hash = `group-${g}` }, gA.id)
    await page.waitFor(sel => document.querySelector(sel)?.getAttribute('aria-expanded') === 'true', { args: [groupButtons], message: 'the linked group to open' })
    assert.deepEqual(await state(), ['true', 'false'])
    page.assertNoProblems()
  })

  it('the owner can open everything on load', async t => {
    const { slug } = await make({ defaultExpanded: true })
    const page = await e.anonymous(t, { goto: `/status/${slug}` })
    await page.waitFor(sel => document.querySelectorAll(sel).length >= 2, { args: [groupButtons] })
    assert.deepEqual(await page.evaluate(sel => [...document.querySelectorAll(sel)].map(b => b.getAttribute('aria-expanded')), groupButtons), ['true', 'true'])
  })

  it('incident updates and maintenance details are collapsed; the headline and state stay visible', async t => {
    const { slug, id, mons } = await make()
    await e.app.apiOk('POST', '/api/incidents', { title: 'Fold incident', impact: 'major', body: 'First public note', pageIds: [id], monitorIds: [mons[0].id] })
    const now = Date.now()
    await e.app.apiOk('POST', '/api/maintenance', { title: 'Fold window', description: 'Detail text for the window', kind: 'once', startsAt: now - 60_000, endsAt: now + 3_600_000, tz: 'UTC', active: true, weekdays: 0, startMinute: 0, durationMin: 0, monitorIds: [mons[0].id], groupIds: [] })
    const page = await e.anonymous(t, { goto: `/status/${slug}` })
    await page.waitForElement({ role: 'heading', name: 'Fold incident' })
    const visible = text => page.evaluate(shown, text)
    assert.equal(await visible('First public note'), false, 'update text is collapsed')
    assert.equal(await visible('Detail text for the window'), false, 'maintenance details are collapsed')
    assert.ok(await page.exists({ role: 'button', name: /Show update/ }))
    assert.match(await page.text({ css: 'article' }), /Latest:/, 'the latest state is visible while collapsed')
    await page.click({ role: 'button', name: /Show update/ })
    await page.waitFor(shown, { args: ['First public note'] })
    // The whole maintenance block is a collapsed headline too: it opens on request, then each window folds its details.
    await page.click({ role: 'button', name: /^Scheduled maintenance/ })
    assert.equal(await visible('Detail text for the window'), false, 'opening the block still keeps the details folded')
    await page.click({ role: 'button', name: 'Show details' })
    await page.waitFor(shown, { args: ['Detail text for the window'] })
    assert.equal(await visible('Detail text for the window'), true)
  })

  it('maintenance appears on a month calendar in a named zone, and the calendar works from the keyboard', async t => {
    const { slug, mons } = await make()
    const start = Date.now() + 26 * 3_600_000
    await e.app.apiOk('POST', '/api/maintenance', { title: 'Calendar window', description: '', kind: 'once', startsAt: start, endsAt: start + 7_200_000, tz: 'UTC', active: true, weekdays: 0, startMinute: 0, durationMin: 0, monitorIds: [mons[0].id], groupIds: [] })
    const page = await e.anonymous(t, { goto: `/status/${slug}` })
    await page.waitForElement({ role: 'heading', name: /^Scheduled maintenance/ })
    assert.equal(await page.exists({ role: 'grid' }), false, 'the calendar is folded until the block is opened')
    await page.click({ role: 'button', name: /^Scheduled maintenance/ })
    await page.waitForElement({ role: 'grid' })
    assert.match(await page.text({ css: '#maint-h + div, section[aria-labelledby=maint-h] > div' }), /Times in/, 'the zone is named next to the dates')
    const markedDays = () => page.evaluate(() => [...document.querySelectorAll('[role=grid] button[aria-label*="maintenance window"]:not([aria-label*="no maintenance"])')].map(b => b.getAttribute('aria-label')))
    let marked = await markedDays()
    // The calendar opens on the current month; a window 26 hours out falls in the next one near a month end.
    if (marked.length === 0) {
      await page.click({ role: 'button', name: /^Next month/ })
      await page.waitFor(() => !!document.querySelector('[role=grid] button[aria-label*="maintenance window"]:not([aria-label*="no maintenance"])'), { message: 'the window day is marked in the next month' })
      marked = await markedDays()
    }
    assert.ok(marked.length >= 1, `a day is marked (${marked})`)
    assert.match(marked[0], /1 maintenance window/)
    // One tab stop; arrows move by day and week; Enter selects and filters the agenda.
    await page.focus({ css: '[role=grid] button[tabindex="0"]' })
    const first = await page.evaluate(() => document.activeElement.getAttribute('data-date'))
    await page.press('ArrowRight')
    const second = await page.evaluate(() => document.activeElement.getAttribute('data-date'))
    assert.notEqual(first, second)
    await page.press('ArrowDown')
    const third = await page.evaluate(() => document.activeElement.getAttribute('data-date'))
    assert.ok(third > second)
    const monthOf = () => page.evaluate(() => document.querySelector('section[aria-labelledby=maint-h] h3[aria-live]').textContent)
    const before = await monthOf()
    await page.press('PageDown')
    await page.waitFor(b => document.querySelector('section[aria-labelledby=maint-h] h3[aria-live]').textContent !== b, { args: [before], message: 'the month changed' })
    page.assertNoProblems()
  })

  it('incident history has a month calendar; choosing a day filters its incidents', async t => {
    const { slug, id, mons } = await make()
    await e.app.apiOk('POST', '/api/incidents', { title: 'Calendar incident', impact: 'major', body: 'x', pageIds: [id], monitorIds: [mons[0].id] })
    await e.app.apiOk('POST', '/api/incidents', { title: 'Second incident', impact: 'minor', body: 'y', pageIds: [id], monitorIds: [mons[0].id] })
    const page = await e.anonymous(t, { goto: `/status/${slug}/history` })
    await page.waitForElement({ role: 'grid' })
    const today = await page.evaluate(() => document.querySelector('[role=grid] button[aria-label*="today"]')?.getAttribute('aria-label'))
    assert.match(today, /2 incidents/, 'today is marked with its count')
    assert.match(await page.text({ css: '[role=status]' }), /2 incidents in/)
    await page.click({ css: '[role=grid] button[aria-label*="today"]' })
    await page.waitFor(() => /2 incidents on/.test(document.querySelector('[role=status]')?.textContent ?? ''), { message: 'the day filter' })
    assert.ok(await page.exists({ role: 'button', name: 'Clear day filter' }))
    // A day with nothing says so.
    await page.click({ role: 'button', name: 'Clear day filter' })
    await page.click({ css: '[role=grid] button[aria-label*="no incidents"]' })
    await page.waitFor(() => /No incidents reported on/.test(document.querySelector('[role=status]')?.textContent ?? ''))
    page.assertNoProblems()
  })
})
