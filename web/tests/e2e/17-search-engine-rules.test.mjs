// Search-engine rules: /robots.txt exists, every app route and the API send "X-Robots-Tag: noindex, nofollow",
// and the only pages that may be indexed are the public status pages themselves. Plain HTTP requests, no
// browser needed beyond what the suite starts anyway.

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

e2eSuite('search engine rules', e => {
  const base = () => `http://127.0.0.1:${e.app.port}`
  const cookieHeader = () => e.app.cookies.map(c => `${c.name}=${c.value}`).join('; ')

  /** GET without following redirects; reads only the headers (and a bounded body for HTML), then drops the connection. */
  async function get(path, { signedIn = false, body = false } = {}) {
    const ctl = new AbortController()
    const res = await fetch(`${base()}${path}`, { redirect: 'manual', signal: ctl.signal, headers: signedIn ? { Cookie: cookieHeader() } : {} })
    const robots = res.headers.get('x-robots-tag')
    const out = { status: res.status, robots, type: res.headers.get('content-type') ?? '', text: '' }
    if (body) out.text = await res.text()
    else ctl.abort() // SSE streams never end
    return out
  }

  // Shared fixtures: one public and one private status page.
  const fx = {}
  const setup = async () => {
    if (fx.ready) return fx
    const mon = await e.app.apiOk('POST', '/api/monitors', monitorBody({ name: unique('robots-svc'), type: 'tcp', target: e.target.tcpAddr, intervalS: 10 }))
    fx.pub = unique('seo-pub')
    fx.priv = unique('seo-priv')
    const items = [{ sort: 0, groupId: null, monitorId: mon.id, displayName: 'Checkout', description: '' }]
    await e.app.apiOk('POST', '/api/pages', pageBody(fx.pub, `Public ${fx.pub}`, { items }))
    await e.app.apiOk('POST', '/api/pages', pageBody(fx.priv, `Private ${fx.priv}`, { visibility: 'private', items }))
    fx.mon = mon
    fx.ready = true
    return fx
  }
  const noindex = (r, what) => {
    assert.ok(r.robots, `${what}: X-Robots-Tag header is missing (status ${r.status})`)
    assert.match(r.robots, /noindex/i, `${what}: ${r.robots}`)
    assert.match(r.robots, /nofollow/i, `${what}: ${r.robots}`)
  }

  it('GET /robots.txt exists, is plain text and does not block the public status pages', async () => {
    await setup()
    for (const signedIn of [false, true]) {
      const r = await get('/robots.txt', { signedIn, body: true })
      assert.equal(r.status, 200, `/robots.txt must exist (got ${r.status})`)
      assert.match(r.type, /^text\/plain/i, 'served as text/plain, not as the app shell')
      assert.match(r.text, /^User-agent:\s*\*/im, 'has a rule group for every crawler')
      assert.doesNotMatch(r.text, /<html|<!doctype/i, 'it is not the single-page app HTML')
      assert.doesNotMatch(r.text, /^Disallow:\s*\/\s*$/im, 'a blanket Disallow would stop crawlers from reading the noindex rules and from indexing public status pages')
      assert.doesNotMatch(r.text, /^Disallow:\s*\/status/im, 'public status pages must stay crawlable')
    }
  })

  it('every app route sends X-Robots-Tag: noindex, nofollow, signed in or not', async () => {
    const { priv, pub } = await setup()
    const routes = [
      '/', '/login', '/overview', '/monitors', '/monitors/new', `/monitors/${fx.mon.id}`, `/monitors/${fx.mon.id}/edit`, '/projects', '/incidents', '/incidents/1',
      '/maintenance', '/pages', '/pages/new', '/settings', '/settings?tab=notifications', '/account/security', '/account/profile', '/third-party', '/wallboard',
      '/this-route-does-not-exist', `/status/${priv}`, '/status/no-such-page-xyz',
      `/status/${pub}/subscribe/confirm?token=${'a'.repeat(64)}`, `/status/${pub}/unsubscribe?token=${'b'.repeat(64)}`,
    ]
    for (const signedIn of [false, true]) {
      for (const p of routes) noindex(await get(p, { signedIn }), `${signedIn ? 'signed in' : 'anonymous'} GET ${p}`)
    }
  })

  it('the API, badges, feeds and the live stream are noindex too (including error responses)', async () => {
    const { pub, priv } = await setup()
    const api = [
      '/api/me', '/api/monitors', '/api/monitors/999999', '/api/pages', '/api/incidents', '/api/channels', '/api/settings', '/api/stream',
      '/api/public/pages/' + pub, `/api/public/pages/${pub}/badge.svg`, `/api/public/pages/${pub}/feed.atom`, `/api/public/pages/${pub}/feed.rss`, `/api/public/pages/${pub}/stream`,
      `/api/public/pages/${priv}`, '/api/public/pages/no-such-page-xyz', `/api/public/pages/${pub}/services/${fx.mon.id}/badge.svg`, `/api/public/pages/${pub}/services/${fx.mon.id}/uptime.svg?window=7d`,
      '/api/nothing-here', '/healthz',
    ]
    for (const signedIn of [false, true]) {
      for (const p of api) noindex(await get(p, { signedIn }), `${signedIn ? 'signed in' : 'anonymous'} GET ${p}`)
    }
    // A POST is not a page, but its answer must not be indexable either.
    const post = await fetch(`${base()}/api/login`, { method: 'POST', headers: { 'Content-Type': 'application/json', Origin: e.app.baseUrl }, body: '{}' })
    assert.match(post.headers.get('x-robots-tag') ?? '', /noindex/i, 'POST /api/login')
  })

  it('a PUBLIC status page does not send noindex (anonymous and signed in), and its HTML carries no noindex meta tag', async () => {
    const { pub } = await setup()
    for (const signedIn of [false, true]) {
      const r = await get(`/status/${pub}`, { signedIn, body: true })
      assert.equal(r.status, 200)
      assert.match(r.type, /text\/html/)
      assert.equal(r.robots, null, `/status/${pub} must stay indexable but sends X-Robots-Tag: ${r.robots}`)
      assert.doesNotMatch(r.text, /<meta[^>]+name=["']robots["'][^>]*noindex/i, 'the page shell must not carry a blanket noindex meta tag either')
      assert.doesNotMatch(r.text, /<meta[^>]+name=["']googlebot["'][^>]*noindex/i)
    }
    // With a trailing slash too.
    assert.equal((await get(`/status/${pub}/`)).robots, null, 'trailing slash')
  })

  it('a PRIVATE status page, an unknown one, subscribe-confirm and unsubscribe pages do send noindex', async () => {
    const { pub, priv } = await setup()
    for (const signedIn of [false, true]) {
      const tag = signedIn ? 'signed in' : 'anonymous'
      noindex(await get(`/status/${priv}`, { signedIn }), `${tag} private /status/${priv}`)
      noindex(await get('/status/no-such-page-xyz', { signedIn }), `${tag} unknown status page`)
      noindex(await get(`/status/${pub}/subscribe/confirm?token=${'c'.repeat(64)}`, { signedIn }), `${tag} /status/${pub}/subscribe/confirm`)
      noindex(await get(`/status/${pub}/subscribe/confirm`, { signedIn }), `${tag} /status/${pub}/subscribe/confirm without a token`)
      noindex(await get(`/status/${pub}/unsubscribe?token=${'d'.repeat(64)}`, { signedIn }), `${tag} /status/${pub}/unsubscribe`)
      noindex(await get(`/status/${pub}/unsubscribe`, { signedIn }), `${tag} /status/${pub}/unsubscribe without a token`)
    }
  })

  it('turning a page private makes it noindex, turning it public makes it indexable again', async () => {
    const { pub } = await setup()
    const pages = (await e.app.apiOk('GET', '/api/pages')).pages
    const id = pages.find(p => p.slug === pub).id
    const current = await e.app.apiOk('GET', `/api/pages/${id}`)
    const body = { ...pageBody(pub, current.title, { items: current.items.map((it, i) => ({ sort: i, groupId: it.groupId, monitorId: it.monitorId, displayName: it.displayName, description: it.description ?? '' })) }) }
    await e.app.apiOk('PUT', `/api/pages/${id}`, { ...body, visibility: 'private' })
    noindex(await get(`/status/${pub}`), 'the page after it was made private')
    await e.app.apiOk('PUT', `/api/pages/${id}`, { ...body, visibility: 'public' })
    assert.equal((await get(`/status/${pub}`)).robots, null, 'public again, indexable again')
  })

  it('in a real browser the public page renders and the app shell still loads (the header changes nothing visible)', async t => {
    const { pub } = await setup()
    const anon = await e.anonymous(t)
    await anon.goto(e.url(`/status/${pub}`))
    await anon.waitForElement({ text: `Public ${pub}` })
    const meta = await anon.evaluate(() => [...document.querySelectorAll('meta[name=robots],meta[name=googlebot]')].map(m => m.content))
    assert.deepEqual(meta.filter(c => /noindex/i.test(c)), [], 'no noindex meta tag on the public page')
    anon.assertNoProblems({ ignore: [/status of 401/] })
  })
})
