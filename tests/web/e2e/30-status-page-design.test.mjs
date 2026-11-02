// Status pages look like the rest of the product (shared radii, control heights, type scale), stay inside
// 375px, keep the owner's brand colour readable in both themes, and honour the safe theming, markdown,
// announcement and show/hide options without ever turning owner text into markup.

import assert from 'node:assert/strict'
import { it } from 'node:test'
import { e2eSuite, unique } from './harness.mjs'
import { monitorBody } from './util.mjs'

const pageBody = (slug, title, over = {}) => ({
  slug, title, description: 'Plain description', visibility: 'public', layout: 'list', showLatency: false,
  alarmMuted: true, alarmDown: 'police-siren', alarmDegraded: 'modern-chime',
  logoUrl: '', brandColor: '', websiteUrl: '', supportUrl: '', footerText: '', showResponseTime: false, historyDays: 90,
  subscriptionsEnabled: false, subscriberChannelId: null, items: [], ...over,
})

e2eSuite('status page design and settings', e => {
  async function makePage(over = {}, settings) {
    const mon = await e.app.apiOk('POST', '/api/monitors', monitorBody({ name: unique('svc'), type: 'tcp', target: e.target.tcpAddr, intervalS: 10, timeoutMs: 5000 }))
    const slug = unique('design')
    const body = pageBody(slug, `Design ${slug}`, { items: [{ sort: 0, groupId: null, monitorId: mon.id, displayName: 'Checkout', description: 'Takes payments' }], ...over })
    if (settings) body.settings = settings
    const created = await e.app.apiOk('POST', '/api/pages', body)
    return { slug, id: created.id, mon, title: body.title }
  }
  /** Wait until the page's service has been checked at least once, so last-check details exist. */
  async function checked(slug) {
    for (let i = 0; i < 60; i++) {
      const r = await fetch(e.url(`/api/public/pages/${slug}`))
      const body = await r.json()
      if (Object.keys(body.monitorExtras ?? {}).length > 0) return
      await new Promise(res => setTimeout(res, 500))
    }
    assert.fail('the service was never checked')
  }
  const radiusOf = (page, css) => page.onElement({ css }, el => getComputedStyle(el).borderTopLeftRadius)

  it('uses the app radii and control heights: 14px cards, 10px controls, 40px header controls', async t => {
    const { slug } = await makePage()
    const page = await e.anonymous(t, { goto: `/status/${slug}`, theme: 'dark', scheme: 'dark' })
    await page.waitForElement({ role: 'heading', name: /Design/, level: 1 })
    await page.waitFor(() => !!document.querySelector('section[aria-label="Overall status"]'))
    assert.equal(await radiusOf(page, 'section[aria-label="Overall status"]'), '14px', 'the banner is a standard card')
    const header = await page.evaluate(() => {
      const box = el => { const r = el.getBoundingClientRect(); return { h: Math.round(r.height), radius: getComputedStyle(el).borderTopLeftRadius } }
      return {
        subscribe: box(document.querySelector('header button[aria-label^="Subscribe"]')),
        appearance: box(document.querySelector('header button[aria-label^="Appearance"]')),
      }
    })
    assert.equal(header.subscribe.h, 40, 'Subscribe is a medium Button')
    assert.equal(header.subscribe.radius, '10px')
    assert.equal(header.appearance.h, 40)
    assert.equal(await page.evaluate(() => document.querySelector('header nav')), null, 'the header carries no page tabs: views are linked from the page body')
    // No card on the page uses an off-system radius (the old 12px cards); 7px is the shared badge.
    const radii = await page.evaluate(() => {
      const found = new Set()
      for (const el of document.querySelectorAll('main *, header *')) {
        const cs = getComputedStyle(el)
        if (cs.borderTopWidth !== '0px' && cs.borderTopLeftRadius !== '0px') found.add(cs.borderTopLeftRadius)
      }
      return [...found]
    })
    for (const r of radii) assert.ok(['2px', '6px', '7px', '8px', '10px', '12px', '14px', '9999px'].includes(r) || parseFloat(r) > 100, `unexpected radius ${r}`)
    page.assertNoProblems()
  })

  it('fits 375px with no sideways scroll on the overview, history, incident, 404 and board', async t => {
    const { slug, mon } = await makePage()
    const inc = await e.app.apiOk('POST', '/api/incidents', { title: 'Slow checkout', impact: 'minor', body: 'Looking into it', pageIds: [(await e.app.apiOk('GET', '/api/pages')).pages.find(p => p.slug === slug).id], monitorIds: [mon.id] })
    for (const theme of ['dark', 'light']) {
      const page = await e.anonymous(t, { viewport: 'mobile', theme, scheme: theme })
      for (const path of [`/status/${slug}`, `/status/${slug}/history`, `/status/${slug}/incidents/${inc.id}`, `/status/${slug}/board`, '/status/no-such-page-here']) {
        await page.goto(e.url(path))
        await page.waitFor(() => !!document.querySelector('h1, h2'), { message: `content at ${path}` })
        await page.settle()
        const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
        assert.ok(overflow <= 0, `${path} (${theme}) scrolls sideways by ${overflow}px`)
      }
    }
  })

  it('the not-found page is a status page: same header height, heading and footer', async t => {
    const page = await e.session(t, { goto: '/status/no-such-page-here' }) // signed in: a missing page says so
    await page.waitForElement({ role: 'heading', name: 'Page not found', level: 1 })
    const m = await page.evaluate(() => ({
      header: Math.round(document.querySelector('header').getBoundingClientRect().height),
      footer: !!document.querySelector('footer'),
      card: getComputedStyle(document.querySelector('main > div > div')).borderTopLeftRadius,
    }))
    assert.ok(m.header >= 64, `header is at least 64px (${m.header})`)
    assert.ok(m.footer)
    assert.equal(m.card, '14px')
  })

  it('theme options: font, corners, header style and first-visit appearance, from a closed list', async t => {
    const { slug } = await makePage({ brandColor: '#335c91' }, { font: 'serif', corners: 'square', headerStyle: 'solid', colorMode: 'light' })
    const page = await e.anonymous(t, { goto: `/status/${slug}`, scheme: 'dark' })
    await page.waitFor(() => !!document.querySelector('section[aria-label="Overall status"]'))
    const state = await page.evaluate(() => ({
      font: document.documentElement.dataset.statusFont, corners: document.documentElement.dataset.statusCorners,
      cls: document.documentElement.className, family: getComputedStyle(document.body).fontFamily,
      card: getComputedStyle(document.querySelector('section[aria-label="Overall status"]')).borderTopLeftRadius,
      header: document.querySelector('header').className,
    }))
    assert.equal(state.font, 'serif')
    assert.equal(state.corners, 'square')
    assert.match(state.family, /Georgia|Charter|Cambria/)
    assert.equal(state.card, '2px', 'square corners')
    assert.match(state.header, /status-header-solid/)
    assert.ok(state.cls.includes('light'), 'the owner default applies to a visitor who never chose')
    // A visitor's own choice wins: the stored preference is never overwritten by the page default.
    const returning = await e.anonymous(t)
    await returning.goto(e.url('/login'))
    await returning.evaluate(() => localStorage.setItem('opskest-theme', 'dark'))
    await returning.goto(e.url(`/status/${slug}`))
    await returning.waitFor(() => !!document.querySelector('section[aria-label="Overall status"]'))
    assert.ok(await returning.evaluate(() => document.documentElement.classList.contains('dark')))
    // Leaving the status page removes the page's font and corner attributes.
    assert.equal(await returning.evaluate(() => localStorage.getItem('opskest-theme')), 'dark')
  })

  it('a custom brand colour keeps header text readable (4.5:1) in the solid header, light and dark', async t => {
    const { slug } = await makePage({ brandColor: '#f2c500' }, { headerStyle: 'solid' })
    for (const theme of ['light', 'dark']) {
      const page = await e.anonymous(t, { goto: `/status/${slug}`, theme, scheme: theme })
      await page.waitFor(() => !!document.querySelector('header button[aria-label^="Subscribe"]'))
      const worst = await page.evaluate(inPageContrastAll)
      for (const [name, ratio] of Object.entries(worst)) assert.ok(ratio >= 4.5, `${theme}: ${name} contrast ${ratio.toFixed(2)}`)
    }
  })

  it('owner markdown and the announcement render as text and safe links, never as markup', async t => {
    const { slug } = await makePage({}, {
      descriptionMd: 'We **care** about [uptime](https://example.com/up).\n\n- one\n- two',
      footerMd: 'Questions? [Mail us](mailto:ops@example.com)',
      banner: { text: 'Planned work *tonight*: [details](https://example.com/n)', severity: 'warning' },
      showUptime: false, showLastCheck: true,
    })
    await checked(slug)
    const page = await e.anonymous(t, { goto: `/status/${slug}` })
    await page.waitFor(() => !!document.querySelector('aside[aria-label="Notice"]'))
    const dom = await page.evaluate(() => ({
      banner: document.querySelector('aside[aria-label="Notice"]').innerText,
      links: [...document.querySelectorAll('aside a, footer a, main a[rel*=nofollow]')].map(a => ({ href: a.getAttribute('href'), rel: a.rel, target: a.target })),
      lis: document.querySelectorAll('main ul li').length,
      body: document.body.innerText,
      scripts: document.querySelectorAll('main script, footer script').length,
    }))
    assert.match(dom.banner, /Notice: Planned work tonight: details/)
    assert.ok(dom.links.some(l => l.href === 'https://example.com/up' && /noopener/.test(l.rel) && l.target === '_blank'))
    assert.ok(dom.links.some(l => l.href === 'mailto:ops@example.com'))
    assert.equal(dom.scripts, 0)
    assert.ok(dom.lis >= 2, 'list items rendered')
    assert.ok(!/% uptime/.test(dom.body), 'uptime percentages are hidden')
    assert.match(dom.body, /Last checked/)
    // The API refuses anything that is not a safe link or that contains a tag.
    for (const bad of ['[x](javascript:alert(1))', '<img src=x onerror=alert(1)>', '![x](http://a.example/i.png)']) {
      const r = await e.app.api('POST', '/api/pages', pageBody(unique('bad'), 'Bad', { settings: { descriptionMd: bad } }))
      assert.equal(r.status, 400, bad)
    }
    page.assertNoProblems()
  })

  it('the editor saves theme, markdown, announcement, switches, refresh and public address', async t => {
    const { id, slug } = await makePage()
    const admin = await e.session(t, { goto: `/pages/${id}` })
    await admin.waitForElement({ role: 'link', name: 'Basics' })
    await admin.click({ role: 'link', name: /^Content$/ })
    await admin.fill({ label: /^Formatted description/ }, 'Hello **world**')
    await admin.fill({ label: /^Pinned announcement/ }, 'Heads up')
    await admin.select({ label: /^Announcement style/ }, 'Important')
    await admin.fill({ label: /^Formatted footer/ }, 'Footer **text**')
    await admin.click({ role: 'link', name: 'Appearance' })
    await admin.select({ label: /^Font/ }, 'Serif (Georgia)')
    await admin.select({ label: /^Corners/ }, 'Square')
    await admin.select({ label: /^Header style/ }, 'Tinted')
    await admin.click({ role: 'link', name: 'Access' })
    await admin.click({ role: 'switch', name: /^Show uptime percentages/ })
    await admin.click({ role: 'switch', name: /^Show when each service was last checked/ })
    await admin.select({ label: /^Auto-refresh/ }, 'Every 30 seconds')
    // A bad address is refused and the editor jumps to the tab holding it.
    await admin.fill({ label: /^Public address/ }, 'http://status.example.com/path')
    await admin.click({ role: 'button', name: 'Save changes' })
    await admin.waitForElement({ text: /use an https:\/\/ address with no path/ })
    await admin.fill({ label: /^Public address/ }, 'https://Status.Example.com/')
    await admin.click({ role: 'button', name: 'Save changes' })
    await admin.waitForElement({ text: 'All changes saved' })
    const saved = await e.app.apiOk('GET', `/api/pages/${id}`)
    assert.deepEqual(
      { font: saved.settings.font, corners: saved.settings.corners, header: saved.settings.headerStyle, sev: saved.settings.banner.severity, up: saved.settings.showUptime, last: saved.settings.showLastCheck, refresh: saved.settings.refreshSeconds, url: saved.settings.canonicalUrl, md: saved.settings.descriptionMd, foot: saved.settings.footerMd },
      { font: 'serif', corners: 'square', header: 'tinted', sev: 'critical', up: false, last: true, refresh: 30, url: 'https://status.example.com', md: 'Hello **world**', foot: 'Footer **text**' },
    )
    // And the public page uses them: canonical link, feed discovery link, refresh interval.
    const pub = await e.anonymous(t, { goto: `/status/${slug}` })
    await pub.waitFor(() => !!document.querySelector('aside[role=alert]'))
    const head = await pub.evaluate(() => ({
      canonical: document.querySelector('link[rel=canonical]')?.href,
      feed: document.querySelector('link[rel=alternate][type="application/atom+xml"]')?.href,
    }))
    assert.equal(head.canonical, `https://status.example.com/status/${slug}`)
    assert.equal(head.feed, `https://status.example.com/api/public/pages/${slug}/feed.atom`)
  })

  it('summary.json is a Statuspage-shaped, cached, cross-origin readable public summary', async () => {
    const { slug } = await makePage()
    const r = await fetch(e.url(`/api/public/pages/${slug}/summary.json`))
    assert.equal(r.status, 200)
    assert.equal(r.headers.get('access-control-allow-origin'), '*')
    assert.match(r.headers.get('cache-control') ?? '', /public/)
    assert.match(r.headers.get('x-robots-tag') ?? '', /noindex/)
    const body = await r.json()
    assert.deepEqual(Object.keys(body).sort(), ['components', 'incidents', 'page', 'scheduled_maintenances', 'status'])
    assert.equal(body.page.id, slug)
    assert.equal(body.components[0].name, 'Checkout')
    assert.ok(!JSON.stringify(body).includes(e.target.tcpAddr), 'no target in the summary')
  })
})

/** Runs in the page: contrast of the header's text controls, colours normalised through a canvas (color-mix() computes to color(srgb ...)). */
function inPageContrastAll() {
  const cx = Object.assign(document.createElement('canvas'), { width: 1, height: 1 }).getContext('2d', { willReadFrequently: true })
  const rgb = c => { cx.clearRect(0, 0, 1, 1); cx.fillStyle = '#000'; cx.fillStyle = c; cx.fillRect(0, 0, 1, 1); return [...cx.getImageData(0, 0, 1, 1).data].slice(0, 3) }
  const lum = ([r, g, b]) => { const f = v => { v /= 255; return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4 }; return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b) }
  const ratio = (a, b) => { const [hi, lo] = [lum(rgb(a)), lum(rgb(b))].sort((x, y) => y - x); return (hi + 0.05) / (lo + 0.05) }
  const header = document.querySelector('header')
  const base = getComputedStyle(header).backgroundColor
  const bgOf = el => { const b = getComputedStyle(el).backgroundColor; return rgb(b).join() === rgb('rgba(0,0,0,0)').join() && /0\)$/.test(b) ? base : b }
  const items = {
    title: header.querySelector('a span'),
    appearance: header.querySelector('button[aria-label^="Appearance"]'),
    subscribe: header.querySelector('button[aria-label^="Subscribe"]'),
  }
  const out = {}
  for (const [k, el] of Object.entries(items)) {
    // The tab rail and the Subscribe button sit on their own surface tokens.
    const own = bgOf(el)
    const rail = el.closest('nav') ? getComputedStyle(el.closest('nav')).backgroundColor : base
    out[k] = ratio(getComputedStyle(el).color, own === base && el.closest('nav') ? rail : own)
  }
  return out
}
