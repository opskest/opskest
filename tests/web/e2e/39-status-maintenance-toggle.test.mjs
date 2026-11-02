// "Show scheduled maintenance": on by default (also for pages saved without the option), switched off in the
// editor's Updates & subscribers section. Off means the public page, its API and its summary carry no maintenance.

import assert from 'node:assert/strict'
import { it } from 'node:test'
import { e2eSuite, unique } from './harness.mjs'
import { monitorBody } from './util.mjs'

const pageBody = (slug, title, over = {}) => ({
  slug, title, description: '', visibility: 'public', layout: 'list', showLatency: false,
  alarmMuted: true, alarmDown: 'police-siren', alarmDegraded: 'modern-chime',
  logoUrl: '', brandColor: '', websiteUrl: '', supportUrl: '', footerText: '', showResponseTime: false, historyDays: 90,
  subscriptionsEnabled: false, subscriberChannelId: null, items: [], ...over,
})

e2eSuite('status page maintenance toggle', e => {
  it('is on by default, can be switched off in the editor, and then no maintenance reaches the public page', async t => {
    const mon = await e.app.apiOk('POST', '/api/monitors', monitorBody({ name: unique('svc'), type: 'tcp', target: e.target.tcpAddr, intervalS: 10, timeoutMs: 5000 }))
    const now = Date.now()
    const windowTitle = unique('Toggle window')
    await e.app.apiOk('POST', '/api/maintenance', { title: windowTitle, description: '', kind: 'once', startsAt: now + 3_600_000, endsAt: now + 7_200_000, tz: 'UTC', active: true, weekdays: 0, startMinute: 0, durationMin: 0, monitorIds: [mon.id], groupIds: [] })
    const slug = unique('mt')
    // Created without any settings: a page saved before the option existed.
    const created = await e.app.apiOk('POST', '/api/pages', pageBody(slug, `Maint ${slug}`, { items: [{ sort: 0, groupId: null, monitorId: mon.id, displayName: 'Checkout', description: '' }] }))

    const anon = await e.anonymous(t, { goto: `/status/${slug}` })
    await anon.waitForElement({ role: 'heading', name: /^Scheduled maintenance/ })
    await anon.click({ role: 'button', name: /^Scheduled maintenance/ }) // the block is collapsed by default
    await anon.waitForElement({ text: windowTitle })

    const admin = await e.session(t, { goto: `/pages/${created.id}?tab=updates` })
    const toggle = { role: 'switch', name: 'Show scheduled maintenance' }
    await admin.waitForElement(toggle)
    assert.equal(await admin.onElement(toggle, el => el.checked), true, 'on by default')
    await admin.waitForElement({ text: /Upcoming and in-progress windows that affect this page's services/ })
    await admin.click(toggle)
    await admin.waitForElement({ text: 'Unsaved changes' })
    await admin.click({ role: 'button', name: 'Save changes' })
    await admin.waitForElement({ text: 'All changes saved' })

    const api = await (await fetch(e.url(`/api/public/pages/${slug}`))).json()
    assert.equal(api.maintenance, undefined, 'the public JSON has no maintenance key')
    assert.equal(api.page.settings.showMaintenance, false)
    const summary = await (await fetch(e.url(`/api/public/pages/${slug}/summary.json`))).text()
    assert.ok(!summary.includes(windowTitle), 'the summary has no maintenance window')

    const again = await e.anonymous(t, { goto: `/status/${slug}` })
    await again.waitForElement({ text: 'Checkout' })
    assert.ok(!(await again.bodyText()).includes(windowTitle), 'the page does not show the window')
    assert.equal(await again.exists({ role: 'heading', name: /^Scheduled maintenance/ }), false)
    again.assertNoProblems()
  })
})
