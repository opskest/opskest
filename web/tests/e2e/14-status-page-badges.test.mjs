// The "Embed" box of the status-page editor: choose a badge, preview it, and copy Markdown / HTML / image-URL
// snippets whose addresses point at this server's public badge endpoints.

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

const ACCESS_TAB = { role: 'link', name: 'Access' }
/** The text of the three copy rows (each is a <code title=...> next to a Copy button). */
const snippets = page => page.evaluate(() => {
  const out = {}
  for (const btn of document.querySelectorAll('button[aria-label^="Copy "]')) {
    const code = btn.parentElement.querySelector('code')
    if (code) out[btn.getAttribute('aria-label').replace(/^Copy /, '')] = code.getAttribute('title')
  }
  return out
})

async function openEmbed(e, t, id) {
  const page = await e.session(t, { goto: `/pages/${id}` })
  await page.waitForElement(ACCESS_TAB)
  await page.click(ACCESS_TAB)
  await page.waitForElement({ role: 'heading', name: 'Embed' })
  return page
}

e2eSuite('status page badges', e => {
  async function setup(over = {}) {
    const a = await e.app.apiOk('POST', '/api/monitors', monitorBody({ name: unique('svc-a'), type: 'tcp', target: e.target.tcpAddr, intervalS: 10 }))
    const b = await e.app.apiOk('POST', '/api/monitors', monitorBody({ name: unique('svc-b'), type: 'tcp', target: e.target.tcpAddr, intervalS: 10 }))
    const slug = unique('badge')
    const title = over.title ?? `Badges ${slug}`
    const created = await e.app.apiOk('POST', '/api/pages', pageBody(slug, title, {
      visibility: 'public',
      items: [{ sort: 0, groupId: null, monitorId: a.id, displayName: 'Checkout', description: '' }, { sort: 1, groupId: null, monitorId: b.id, displayName: 'API', description: '' }],
      ...over,
    }))
    return { slug, title, a, b, id: created.id ?? (await e.app.apiOk('GET', '/api/pages')).pages.find(p => p.slug === slug).id }
  }
  const origin = () => e.app.baseUrl

  it('the page-status badge: snippets use this server\'s address and the page\'s slug', async t => {
    const { slug, title, id } = await setup()
    const page = await openEmbed(e, t, id)
    assert.equal(await page.value({ label: /^Badge$/ }), 'page')
    const s = await snippets(page)
    const img = `${origin()}/api/public/pages/${slug}/badge.svg`
    assert.equal(s['Image URL'], img)
    assert.equal(s['Markdown snippet'], `[![${title} status](${img})](${origin()}/status/${slug})`)
    assert.equal(s['HTML snippet'], `<a href="${origin()}/status/${slug}"><img src="${img}" alt="${title} status"></a>`)
    // The preview image really is that badge, on the light and the dark swatch.
    const previews = await page.evaluate(() => [...document.querySelectorAll('[role=group][aria-label="Badge preview"] img')].map(i => ({ src: i.src, w: i.naturalWidth, alt: i.alt })))
    assert.equal(previews.length, 2)
    for (const p of previews) { assert.equal(p.src, img); assert.ok(p.w > 20, 'the badge image loaded') }
    // The address in the snippet serves an SVG with a title, to anyone.
    const r = await fetch(s['Image URL'].replace(origin(), `http://127.0.0.1:${e.app.port}`))
    assert.equal(r.status, 200)
    assert.match(r.headers.get('content-type') ?? '', /image\/svg\+xml/)
    assert.match(await r.text(), /<title>[^<]+<\/title>/)
    page.assertNoProblems()
  })

  it('service badges: pick a service, status or uptime, and a window; the snippets follow', async t => {
    const { slug, title, a, b, id } = await setup()
    const page = await openEmbed(e, t, id)
    const base = `${origin()}/api/public/pages/${slug}`
    const serviceField = { label: /^Service$/ }
    await page.select({ label: /^Badge$/ }, 'Service status')
    await page.waitForElement(serviceField)
    // Services appear under the names visitors see (display names), sorted.
    const options = await page.onElement(serviceField, el => [...el.options].map(o => o.textContent.trim()))
    assert.deepEqual(options, ['API', 'Checkout'])
    let s = await snippets(page)
    assert.equal(s['Image URL'], `${base}/services/${b.id}/badge.svg`, 'the first service alphabetically (API) is chosen')
    assert.equal(s['Markdown snippet'], `[![API status](${base}/services/${b.id}/badge.svg)](${origin()}/status/${slug})`)
    await page.select(serviceField, 'Checkout')
    s = await snippets(page)
    assert.equal(s['Image URL'], `${base}/services/${a.id}/badge.svg`)
    assert.equal(s['HTML snippet'], `<a href="${origin()}/status/${slug}"><img src="${base}/services/${a.id}/badge.svg" alt="Checkout status"></a>`)
    assert.ok(!JSON.stringify(s).includes(e.target.tcpAddr), 'a snippet never contains the monitored address')
    assert.ok(!JSON.stringify(s).includes(a.name), 'nor the private monitor name')

    // Uptime badge with a window.
    await page.select({ label: /^Badge$/ }, 'Service uptime')
    await page.waitForElement({ label: /^Window$/ })
    assert.equal(await page.value({ label: /^Window$/ }), '30d', '30 days is the default window')
    const windows = await page.onElement({ label: /^Window$/ }, el => [...el.options].map(o => o.value))
    assert.deepEqual(windows, ['24h', '7d', '30d', '90d'])
    s = await snippets(page)
    assert.equal(s['Image URL'], `${base}/services/${a.id}/uptime.svg?window=30d`)
    assert.equal(s['Markdown snippet'], `[![Checkout uptime 30d](${base}/services/${a.id}/uptime.svg?window=30d)](${origin()}/status/${slug})`)
    for (const w of windows) {
      await page.select({ label: /^Window$/ }, w)
      s = await snippets(page)
      assert.equal(s['Image URL'], `${base}/services/${a.id}/uptime.svg?window=${w}`)
      const r = await fetch(s['Image URL'].replace(origin(), `http://127.0.0.1:${e.app.port}`))
      assert.equal(r.status, 200, `the ${w} uptime badge is served`)
      assert.match(r.headers.get('content-type') ?? '', /image\/svg\+xml/)
    }
    // Back to the page badge: the service and window pickers go away.
    await page.select({ label: /^Badge$/ }, 'Page status')
    await page.waitForGone(serviceField)
    assert.equal(await page.exists({ label: /^Window$/ }), false)
    assert.equal((await snippets(page))['Image URL'], `${base}/badge.svg`)
    assert.ok(title)
    page.assertNoProblems()
  })

  it('a title with brackets, quotes or angle brackets cannot break out of the Markdown or HTML snippet', async t => {
    const { slug, id } = await setup({ title: 'R&D [core] (EU) <team> "alpha" *beta*' })
    const page = await openEmbed(e, t, id)
    const s = await snippets(page)
    const img = `${origin()}/api/public/pages/${slug}/badge.svg`
    // Markdown: no bracket or parenthesis inside the alt text, so the link syntax stays intact.
    const md = /^\[!\[([^\]]*)\]\(([^)]*)\)\]\(([^)]*)\)$/.exec(s['Markdown snippet'])
    assert.ok(md, `well-formed Markdown: ${s['Markdown snippet']}`)
    assert.match(md[1], /^R&D core EU team "alpha" beta status$/)
    assert.equal(md[2], img)
    assert.equal(md[3], `${origin()}/status/${slug}`)
    // HTML: the alt attribute is escaped; parsing the snippet gives back exactly one link with one image.
    const html = s['HTML snippet']
    assert.match(html, /alt="R&amp;D core EU team &quot;alpha&quot; beta status"/)
    const parsed = await page.evaluate(h => { const d = document.createElement('div'); d.innerHTML = h; return { links: d.querySelectorAll('a').length, imgs: d.querySelectorAll('img').length, scripts: d.querySelectorAll('script').length, alt: d.querySelector('img')?.alt, href: d.querySelector('a')?.getAttribute('href'), src: d.querySelector('img')?.getAttribute('src') } }, html)
    assert.deepEqual({ links: parsed.links, imgs: parsed.imgs, scripts: parsed.scripts }, { links: 1, imgs: 1, scripts: 0 })
    assert.equal(parsed.alt, 'R&D core EU team "alpha" beta status')
    assert.equal(parsed.href, `${origin()}/status/${slug}`)
    assert.equal(parsed.src, img)
    page.assertNoProblems()
  })

  it('Copy puts the snippet on the clipboard and says so', async t => {
    const { id, slug } = await setup()
    const page = await e.session(t, { goto: `/pages/${id}` })
    await e.browser.conn.send('Browser.grantPermissions', { permissions: ['clipboardReadWrite', 'clipboardSanitizedWrite'], origin: origin(), browserContextId: page.context.id }).catch(() => {})
    await page.waitForElement(ACCESS_TAB)
    await page.click(ACCESS_TAB)
    await page.waitForElement({ role: 'button', name: 'Copy Markdown snippet' })
    await page.click({ role: 'button', name: 'Copy Markdown snippet' })
    await page.waitForElement({ text: /Markdown snippet copied|Could not copy/ })
    const toast = await page.bodyText()
    assert.match(toast, /Markdown snippet copied/, 'the copy succeeded (clipboard permission is granted to the test browser)')
    const clip = await page.evaluate(() => navigator.clipboard.readText())
    assert.equal(clip, `[![Badges ${slug} status](${origin()}/api/public/pages/${slug}/badge.svg)](${origin()}/status/${slug})`)
    await page.click({ role: 'button', name: 'Copy Image URL' })
    await page.waitForElement({ text: /Image URL copied/ })
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), `${origin()}/api/public/pages/${slug}/badge.svg`)
    page.assertNoProblems()
  })

  it('a private page, a new page and a page without services explain why there is no badge', async t => {
    // Private: badges are only served for public pages.
    const priv = await setup({ visibility: 'private' })
    let page = await openEmbed(e, t, priv.id)
    assert.match(await page.bodyText(), /Badges are only served for public status pages/)
    assert.equal(Object.keys(await snippets(page)).length, 0, 'no snippets for a private page')
    const r = await fetch(`http://127.0.0.1:${e.app.port}/api/public/pages/${priv.slug}/badge.svg`)
    assert.equal(r.status, 404, 'and the server agrees')
    // A new page has no Embed section at all: the create flow ends on the editor, where it appears.
    page = await e.session(t, { goto: '/pages/new' })
    await page.waitForElement({ role: 'heading', name: 'Create a status page' })
    assert.ok(!(await page.exists({ role: 'heading', name: 'Embed' })), 'nothing to embed before the page exists')
    // Public page with no services: the page badge works, service badges say there is nothing to pick.
    const slug = unique('empty')
    const created = await e.app.apiOk('POST', '/api/pages', pageBody(slug, `Empty ${slug}`))
    const id = created.id ?? (await e.app.apiOk('GET', '/api/pages')).pages.find(p => p.slug === slug).id
    page = await openEmbed(e, t, id)
    assert.equal((await snippets(page))['Image URL'], `${origin()}/api/public/pages/${slug}/badge.svg`)
    await page.select({ label: /^Badge$/ }, 'Service status')
    await page.waitForElement({ text: /No saved services on this page yet|Add services to the page and save first/ })
    assert.equal(Object.keys(await snippets(page)).length, 0)
    page.assertNoProblems()
  })

  it('the snippets track the saved page: changing the address in the editor does not change them until saved', async t => {
    const { slug, id } = await setup()
    const page = await e.session(t, { goto: `/pages/${id}` })
    await page.waitForElement({ role: 'link', name: 'Basics' })
    const newSlug = `${slug}-renamed`
    await page.fill({ label: /^Page address$/ }, newSlug)
    await page.click(ACCESS_TAB)
    await page.waitForElement({ role: 'button', name: 'Copy Image URL' })
    assert.ok((await snippets(page))['Image URL'].includes(`/pages/${slug}/`), 'unsaved edits do not change the badge address (it follows the saved page)')
    assert.match(await page.bodyText(), /follow the saved page, so save changes first/)
    page.assertNoProblems()
  })
})
