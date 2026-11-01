// Status pages: create through the UI, view signed out, badges, private pages answer 404 signed out.

import assert from 'node:assert/strict'
import { it } from 'node:test'
import { e2eSuite, unique } from './harness.mjs'
import { monitorBody } from './util.mjs'

/** An unauthenticated GET straight from Node (no cookies at all). */
const anonGet = async (e, p) => { const r = await fetch(e.url(p)); return { status: r.status, contentType: r.headers.get('content-type') ?? '', text: await r.text() } }

const pageBody = (slug, title, over = {}) => ({
  slug, title, description: 'e2e page', visibility: 'public', layout: 'list', showLatency: false,
  alarmMuted: true, alarmDown: 'police-siren', alarmDegraded: 'modern-chime',
  logoUrl: '', brandColor: '', websiteUrl: '', supportUrl: '', footerText: '', showResponseTime: false, historyDays: 90,
  subscriptionsEnabled: false, subscriberChannelId: null, items: [], ...over,
})

e2eSuite('status pages', e => {
  it('create a public page in the editor, then view it signed out with its badges', async t => {
    const mon = await e.app.apiOk('POST', '/api/monitors', monitorBody({ name: unique('svc'), type: 'tcp', target: e.target.tcpAddr, intervalS: 10, timeoutMs: 5000 }))
    const slug = unique('pub')
    const title = `Public ${slug}`
    const admin = await e.session(t, { goto: '/pages/new' })
    await admin.waitForElement({ role: 'heading', name: 'Create a status page' })
    await admin.fill({ label: /^Title/ }, title)
    // The slug follows the title until edited; set it explicitly.
    await admin.fill({ label: /Slug|Address|URL/i }, slug)
    // The guided flow: name and type, theme, services, publish; it lands on the editor.
    await admin.click({ role: 'button', name: 'Continue' })
    await admin.waitForElement({ role: 'heading', name: 'Brand identity' })
    await admin.click({ role: 'button', name: 'Continue' })
    await admin.waitForElement({ role: 'heading', name: /Services and service groups/ })
    await admin.click({ role: 'button', name: 'Continue' })
    await admin.click({ role: 'button', name: 'Create page' })
    await admin.waitForPath(/^\/pages\/\d+/)
    await admin.waitForElement({ role: 'heading', name: title })
    await admin.waitForElement({ role: 'link', name: 'Appearance' })
    const pages = (await e.app.apiOk('GET', '/api/pages')).pages
    const created = pages.find(p => p.slug === slug)
    assert.ok(created, 'the page was stored')
    // Attach the monitor via the API (the item picker is covered by the editor itself).
    await e.app.apiOk('PUT', `/api/pages/${created.id}`, pageBody(slug, title, { items: [{ sort: 0, groupId: null, monitorId: mon.id, displayName: 'Checkout', description: '' }] }))

    const anon = await e.anonymous(t)
    await anon.goto(e.url(`/status/${slug}`))
    await anon.waitForElement({ text: title })
    await anon.waitForElement({ text: 'Checkout' })
    const body = await anon.bodyText()
    assert.ok(!body.includes(e.target.tcpAddr), 'the public page must not reveal the monitored target')
    assert.ok(!body.includes(mon.name), 'the real monitor name is replaced by the display name')
    assert.equal(await anon.exists({ role: 'link', name: /Sign in/ }), false, 'public pages do not ask for sign-in')
    anon.assertNoProblems({ ignore: [/status of 401/] })

    // Badges: SVG with a <title>.
    for (const path of [`/api/public/pages/${slug}/badge.svg`, `/api/public/pages/${slug}/services/${mon.id}/badge.svg`]) {
      const r = await anonGet(e, path)
      assert.equal(r.status, 200, path)
      assert.match(r.contentType, /image\/svg\+xml/, path)
      assert.match(r.text, /<title>[^<]+<\/title>/, `${path} has an accessible <title>`)
      assert.ok(!r.text.includes(e.target.tcpAddr), 'badge must not leak the target')
    }
  })

  it('a private page is 404 signed out and readable signed in', async t => {
    const slug = unique('priv')
    await e.app.apiOk('POST', '/api/pages', pageBody(slug, `Private ${slug}`, { visibility: 'private' }))
    const anon = await e.anonymous(t)
    for (const p of [`/api/public/pages/${slug}`, `/api/public/pages/${slug}/badge.svg`, `/api/public/pages/${slug}/feed.atom`]) {
      assert.equal((await anonGet(e, p)).status, 404, `${p} must be 404 (never 403) for a private page signed out`)
    }
    await anon.goto(e.url(`/status/${slug}`))
    await anon.waitForElement({ text: /private|not found/i })
    assert.ok(!(await anon.bodyText()).includes(`Private ${slug}`), 'the private title is not revealed')
    // Unknown slugs are indistinguishable.
    assert.equal((await anonGet(e, '/api/public/pages/no-such-page-xyz')).status, 404)
    // Signed in: the page renders.
    const admin = await e.session(t, { goto: `/status/${slug}` })
    await admin.waitForElement({ text: `Private ${slug}` })
  })

  it('the editor validates the slug and title', async t => {
    const page = await e.session(t, { goto: '/pages/new' })
    await page.waitForElement({ role: 'heading', name: 'Create a status page' })
    await page.click({ role: 'button', name: 'Continue' })
    await page.waitForElement({ text: /required|use 2.48 lowercase/i })
    assert.equal(page.responses.filter(r => r.method === 'POST' && /\/api\/pages$/.test(r.url)).length, 0)
  })
})
