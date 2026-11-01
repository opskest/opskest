// Lists never render more rows than their page size.

import assert from 'node:assert/strict'
import { it } from 'node:test'
import { e2eSuite, unique } from './harness.mjs'
import { monitorBody } from './util.mjs'

const pageBody = (slug, title) => ({
  slug, title, description: '', visibility: 'public', layout: 'list', showLatency: false, alarmMuted: true, alarmDown: 'police-siren', alarmDegraded: 'modern-chime',
  logoUrl: '', brandColor: '', websiteUrl: '', supportUrl: '', footerText: '', showResponseTime: false, historyDays: 90, subscriptionsEnabled: false, subscriberChannelId: null, items: [],
})

e2eSuite('pagination', e => {
  it('the monitor list shows at most 25 rows and pages through the rest', async t => {
    const prefix = unique('pg')
    for (let i = 0; i < 120; i++) await e.app.apiOk('POST', '/api/monitors', monitorBody({ name: `${prefix}-${String(i).padStart(3, '0')}`, type: 'push', target: '', intervalS: 3600, retryIntervalS: 3600 }))
    const page = await e.session(t, { goto: '/monitors' })
    await page.waitForElement({ text: /Showing 1-25 of 120/ })
    const rows = () => page.evaluate(() => document.querySelectorAll('section[aria-label=Monitors] ul > li').length)
    assert.equal(await rows(), 25)
    await page.click({ role: 'button', name: 'Next page' })
    await page.waitForElement({ text: /Showing 26-50 of 120/ })
    assert.equal(await rows(), 25)
    await page.click({ role: 'button', name: 'Next page' })
    await page.waitForElement({ text: /Showing 51-75 of 120/ })
    assert.equal(await rows(), 25)
  })

  it('channels, status pages and incidents render at most 25 rows per page', async t => {
    for (let i = 0; i < 30; i++) await e.app.apiOk('POST', '/api/channels', { name: `ch-${i}`, type: 'webhook', isDefault: false, config: { url: `https://example.com/h/${i}` } })
    for (let i = 0; i < 30; i++) await e.app.apiOk('POST', '/api/pages', pageBody(`pg-${i}-${unique('s')}`, `Page ${i}`))
    for (let i = 0; i < 30; i++) await e.app.apiOk('POST', '/api/incidents', { title: `inc-${i}`, impact: 'minor', status: 'investigating', monitorIds: [], message: 'e2e' })
    const page = await e.session(t, { goto: '/settings?tab=notifications' })
    await page.waitForElement({ text: /Showing 1-25 of 30/ })
    assert.equal(await page.evaluate(() => document.querySelectorAll('main [data-channel-row]').length), 25)
    await page.goto(e.url('/pages'))
    await page.waitForElement({ text: /Showing 1-25 of 30/ })
    assert.ok(await page.evaluate(() => document.querySelectorAll('main ul > li').length) <= 25)
    await page.goto(e.url('/incidents'))
    await page.waitForElement({ text: /Showing 1-25 of 30/ })
    const count = await page.evaluate(() => document.querySelectorAll('section[aria-label="Incident inbox"] a[href^="/incidents/"]').length)
    assert.ok(count <= 25, `incident inbox rendered ${count} rows`)
  })
})
