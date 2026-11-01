// Third-party feeds (HTTP monitors with settings.vendor, made on /third-party) are alert-only: when the feed
// reports an incident the dev team's channel is alerted, but the feed is never an outage, never counts toward
// uptime / down counts, and cannot be a status-page component, dependency or maintenance target.
// A local RSS feed (flipped during the test) and a local gotify-style alert receiver keep everything on this machine.

import assert from 'node:assert/strict'
import http from 'node:http'
import { it } from 'node:test'
import { e2eSuite, unique } from './harness.mjs'
import { Receiver, until, holds } from './20-kit.mjs'
import { monitorBody } from './util.mjs'

const rss = (title, description = '') => `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>E2E status</title><item><title>${title}</title><description>${description}</description><pubDate>Mon, 01 Jan 2024 00:00:00 GMT</pubDate></item></channel></rss>`
const INCIDENT = rss('Major outage: API unavailable', 'Customers cannot reach the API.')
const RESOLVED = rss('Resolved: elevated API errors', 'Everything is back to normal.')

async function startFeed() {
  const feed = { body: INCIDENT, server: null }
  feed.server = http.createServer((req, res) => { res.writeHead(200, { 'Content-Type': 'application/rss+xml' }); res.end(feed.body) })
  await new Promise(r => feed.server.listen(0, '127.0.0.1', r))
  feed.url = `http://127.0.0.1:${feed.server.address().port}/feed.rss`
  feed.stop = () => new Promise(r => { feed.server.closeAllConnections?.(); feed.server.close(r) })
  return feed
}

e2eSuite('vendor feeds are alert-only', e => {
  it('a feed incident alerts the channel once but is never an outage, uptime or a down count', async t => {
    const rx = await Receiver.start()
    const feed = await startFeed()
    t.after(async () => { await rx.stop(); await feed.stop() })

    const key = unique('vendor')
    const ch = await e.app.apiOk('POST', '/api/channels', { name: `rx-${key}`, type: 'gotify', config: { url: `${rx.base}/${key}`, apiKey: 'test-token' } })
    const name = unique('Provider feed')
    const vendor = await e.app.apiOk('POST', '/api/monitors', monitorBody({
      name, type: 'http', target: feed.url, intervalS: 10, channelIds: [ch.id], settings: { acceptedCodes: '200-299', vendor: true },
    }))
    // Contrast: one of the team's own monitors failing does open an outage.
    const ownName = unique('own-svc')
    const own = await e.app.apiOk('POST', '/api/monitors', monitorBody({
      name: ownName, type: 'http', target: e.target.httpUrl, intervalS: 10, settings: { keyword: 'this-text-is-not-served' },
    }))

    const get = async id => (await e.app.apiOk('GET', '/api/monitors')).monitors.find(m => m.id === id)
    await until('the feed to go down', async () => (await get(vendor.id))?.status === 'down', { timeout: 40000 })
    await until('the own monitor to go down', async () => (await get(own.id))?.status === 'down', { timeout: 40000 })

    // The channel is told, once, that the provider is down.
    await until('the Down alert', async () => rx.kinds(key, name).includes('down'), { timeout: 15000 })
    await holds('no second Down alert for the feed', 12000, async () => rx.kinds(key, name).filter(k => k === 'down').length > 1 && 'Down sent twice')

    const v = await get(vendor.id)
    assert.equal(v.vendor, true, '/api/monitors still returns the feed, flagged')
    assert.equal(v.status, 'down')
    assert.ok(v.reason, 'the feed keeps its own reason')
    assert.equal(v.outageId, null, 'no outage is linked to the feed')
    assert.equal(v.uptime24h, null, 'a feed has no uptime')

    // No outage anywhere on the server.
    const outages = (await e.app.apiOk('GET', '/api/outages?limit=200')).outages
    assert.ok(outages.some(o => o.monitorId === own.id), 'the own monitor has an outage (control)')
    assert.ok(!outages.some(o => o.monitorId === vendor.id), 'the feed has none')
    const mine = (await e.app.apiOk('GET', `/api/monitors/${vendor.id}/outages`)).outages ?? []
    assert.equal(mine.length, 0)
    const report = await e.app.apiOk('GET', '/api/reports/sla')
    assert.ok(!report.rows.some(r => r.id === vendor.id), 'the SLA report leaves the feed out')
    assert.equal((await e.app.api('GET', `/api/reports/sla?scope=monitor&id=${vendor.id}`)).status, 404)
    const events = (await e.app.apiOk('GET', '/api/events?limit=200')).events
    assert.ok(!events.some(ev => ev.monitorId === vendor.id), 'the global events feed leaves the feed out')

    // The server refuses to treat it as one of the team's own services.
    const page = await e.app.api('POST', '/api/pages', { slug: unique('vp'), title: 'x', visibility: 'public', layout: 'list', items: [{ monitorId: vendor.id, sort: 0 }] })
    assert.equal(page.status, 400, 'not a status-page component')
    assert.match(page.text, /3rd-party/)
    const dep = await e.app.api('POST', '/api/monitors', monitorBody({ name: unique('child'), type: 'http', target: e.target.httpUrl, settings: { dependsOn: [vendor.id] } }))
    assert.equal(dep.status, 400, 'not a dependency parent')
    const start = Date.now() + 3600e3
    const maint = await e.app.api('POST', '/api/maintenance', { title: 'w', kind: 'once', startsAt: start, endsAt: start + 3600e3, active: true, tz: 'UTC', monitorIds: [vendor.id] })
    assert.equal(maint.status, 400, 'not a maintenance target')

    // The UI: not on the dashboard's overview as an outage, and its own page says what it is.
    const overview = await e.session(t, { goto: '/' })
    await overview.waitForElement({ text: ownName })
    assert.ok(!(await overview.bodyText()).includes(name), 'the feed is not listed on the overview')
    overview.assertNoProblems()

    const detail = await e.session(t, { goto: `/monitors/${vendor.id}` })
    await detail.waitForElement({ role: 'heading', name })
    await detail.click({ css: '[role=tablist][aria-label="Detail views"] [role=tab]', text: 'Events' })
    await detail.waitForElement({ text: 'Third-party feed: alerts only, not tracked as outages.' })
    assert.equal(await detail.exists({ text: 'Outage timeline' }), false, 'no outage timeline for a feed')
    await detail.click({ css: '[role=tablist][aria-label="Detail views"] [role=tab]', text: 'Reliability' })
    await detail.waitForElement({ text: 'Third-party feed: alerts only, not tracked as outages.' })
    detail.assertNoProblems()

    // Recovery is announced once, and still no outage appears.
    feed.body = RESOLVED
    await until('the feed to recover', async () => (await get(vendor.id))?.status === 'up', { timeout: 40000 })
    await until('the Up alert', async () => rx.kinds(key, name).includes('recovered'), { timeout: 15000 })
    assert.deepEqual(rx.kinds(key, name), ['down', 'recovered'])
    const after = (await e.app.apiOk('GET', '/api/outages?limit=200')).outages
    assert.ok(!after.some(o => o.monitorId === vendor.id), 'still no outage for the feed')
  })
})
