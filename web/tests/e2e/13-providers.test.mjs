// External services (/third-party): the provider catalog (category chips, search, paging), the prefilled
// "Connect" dialog and its channel list, and "Check now" on a connected feed using the feed's saved settings.
// A local RSS feed with an incident is served from this process, so nothing leaves the machine.

import assert from 'node:assert/strict'
import http from 'node:http'
import { it } from 'node:test'
import { e2eSuite, unique } from './harness.mjs'

const CATALOG_SIZE = 99
const CATEGORIES = ['Cloud & hosting', 'CDN & DNS', 'Payments & commerce', 'Identity & security', 'Developer tools', 'Communication', 'Observability', 'AI & data', 'Productivity & SaaS']

// How each channel type is named in the UI (written by hand, not read from the app).
const CHANNEL_NAMES = {
  slack: 'Slack Webhook', email: 'Email (SMTP)', teams: 'Microsoft Teams', webhook: 'Custom Webhook', pagerduty: 'PagerDuty v2',
  gchat: 'Google Chat', opsgenie: 'Opsgenie', discord: 'Discord', telegram: 'Telegram', ntfy: 'ntfy', gotify: 'Gotify',
  pushover: 'Pushover', matrix: 'Matrix', mattermost: 'Mattermost', rocketchat: 'Rocket.Chat',
}
const CHANNEL_CONFIG = {
  slack: { url: 'https://hooks.example.com/s' }, teams: { url: 'https://hooks.example.com/t' }, gchat: { url: 'https://hooks.example.com/g' },
  webhook: { url: 'https://hooks.example.com/w' }, discord: { url: 'https://hooks.example.com/d' }, mattermost: { url: 'https://hooks.example.com/m' },
  rocketchat: { url: 'https://hooks.example.com/r' }, ntfy: { url: 'https://ntfy.sh/e2e-topic' }, gotify: { url: 'https://gotify.example.com', apiKey: 'tok' },
  telegram: { apiKey: 'tok', target: '123' }, pushover: { apiKey: 'tok', target: 'user' }, matrix: { url: 'https://matrix.example.com', apiKey: 'tok', target: '!r:example.org' },
  pagerduty: { routingKey: 'rk' }, opsgenie: { apiKey: 'ak' }, email: { host: 'smtp.example.com', port: 587, from: 'a@example.com', to: ['b@example.com'] },
}

const rss = (title, description = '') => `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>E2E status</title><item><title>${title}</title><description>${description}</description><pubDate>Mon, 01 Jan 2024 00:00:00 GMT</pubDate></item></channel></rss>`

/** A tiny status-feed server: an active incident, a resolved one and a degraded one. */
async function startFeeds() {
  const feeds = {
    '/incident.rss': rss('Major outage: API unavailable', 'Customers cannot reach the API.'),
    '/resolved.rss': rss('Resolved: elevated API errors', 'Everything is back to normal.'),
    '/degraded.rss': rss('Investigating elevated latency'),
  }
  const hits = []
  const server = http.createServer((req, res) => {
    hits.push(req.url)
    const body = feeds[req.url.split('?')[0]]
    if (!body) { res.writeHead(404); res.end('no'); return }
    res.writeHead(200, { 'Content-Type': 'application/rss+xml' })
    res.end(body)
  })
  await new Promise(r => server.listen(0, '127.0.0.1', r))
  const base = `http://127.0.0.1:${server.address().port}`
  return { base, hits, url: p => `${base}${p}`, stop: () => new Promise(r => { server.closeAllConnections?.(); server.close(r) }) }
}

const catalogCards = 'section[aria-label="Provider catalog"] article'
const chips = '[role=group][aria-label="Provider category"] button'
const search = { role: 'textbox', name: 'Search providers' }
const dialog = { role: 'dialog' }
const inDialog = spec => ({ ...spec, within: dialog })
const feedCard = name => ({ css: 'section[aria-label="Connected provider feeds"] article', text: name })
const inCard = (name, spec) => ({ ...spec, within: feedCard(name) })
/** [matching, total] from "N of M available providers"; total is null when everything is shown ("M available providers"). */
const countText = page => page.evaluate(() => { const m = document.body.innerText.match(/(\d+)(?: of (\d+))? available providers/); return m ? [Number(m[1]), m[2] ? Number(m[2]) : null] : null })

async function openCatalog(e, t, opts = {}) {
  const page = await e.session(t, { goto: '/third-party', ...opts })
  await page.waitForElement({ role: 'heading', name: 'External services' })
  await page.click({ role: 'tab', name: /^Browse providers/ })
  await page.waitForElement({ text: /\d+ available providers/ })
  return page
}

e2eSuite('providers', e => {
  it('the catalog lists 99 providers, 12 to a page, each with a Connect button and a source label', async t => {
    const page = await openCatalog(e, t)
    assert.deepEqual(await countText(page), [CATALOG_SIZE, null], 'all 99 are available')
    assert.equal(await page.count({ css: catalogCards }), 12, 'the first page shows 12 cards')
    const cards = await page.onElements({ css: catalogCards }, a => ({ name: a.querySelector('h2')?.textContent.trim(), source: a.querySelector('span.truncate')?.textContent.trim(), connect: !!Array.from(a.querySelectorAll('button')).find(b => b.textContent.trim() === 'Connect'), desc: a.querySelector('p')?.textContent.trim() }))
    for (const c of cards) {
      assert.ok(c.name, 'every card has a name')
      assert.ok(c.desc && c.desc.length > 5, `${c.name} says what it covers`)
      assert.ok(c.connect, `${c.name} has a Connect button`)
      assert.match(c.source, /^(Status page|Incident feed)$/, `${c.name} says what kind of source it is`)
    }
    const names = Object.fromEntries(cards.map(c => [c.name, c.source]))
    assert.equal(names['Google Firebase'], 'Status page')
    assert.equal(names.DigitalOcean, 'Incident feed', 'a history.rss URL is an incident feed')
    assert.equal(names['GitHub Status'], 'Status page')

    // All 99 at the largest page size (48): three pages, unique names across them, nothing missing a button.
    await page.select({ css: 'select', label: /Providers per page/i }, '48')
    const seen = new Set()
    for (const want of [48, 48, 3]) {
      await page.waitFor(n => document.querySelectorAll('section[aria-label="Provider catalog"] article').length === n, { args: [want], message: `${want} cards on the page` })
      const onPage = await page.onElements({ css: `${catalogCards} h2` }, h => h.textContent.trim())
      for (const n of onPage) { assert.ok(!seen.has(n), `${n} appears on two pages`); seen.add(n) }
      assert.equal(await page.count({ role: 'button', name: 'Connect', exact: true }), want, 'every card on the page has a Connect button')
      if (seen.size < CATALOG_SIZE) await page.click({ role: 'button', name: 'Next page' })
    }
    assert.equal(seen.size, CATALOG_SIZE, 'provider names are unique and the three pages cover the whole catalog')
    page.assertNoProblems()
  })

  it('category chips filter the catalog and add up to the whole catalog', async t => {
    const page = await openCatalog(e, t)
    const labels = await page.onElements({ css: chips }, b => ({ label: b.textContent.trim(), pressed: b.getAttribute('aria-pressed') }))
    assert.deepEqual(labels.map(l => l.label), ['All', ...CATEGORIES])
    assert.deepEqual(labels.map(l => l.pressed), ['true', ...CATEGORIES.map(() => 'false')], 'All is selected at first')
    let sum = 0
    for (const c of CATEGORIES) {
      await page.click({ css: chips, text: c })
      await page.waitFor(label => document.querySelector(`${'[role=group][aria-label="Provider category"]'} button[aria-pressed=true]`)?.textContent.trim() === label, { args: [c], message: `the ${c} chip to be pressed` })
      const [n, total] = await countText(page)
      assert.equal(total, CATALOG_SIZE, `${c}: "N of 99 available providers"`)
      assert.ok(n > 0 && n < CATALOG_SIZE, `${c} has some providers but not all of them (${n})`)
      assert.equal(await page.count({ css: catalogCards }), Math.min(n, 12))
      sum += n
    }
    assert.equal(sum, CATALOG_SIZE, 'every provider is in exactly one category')
    await page.click({ css: chips, text: 'All' })
    await page.waitForElement({ text: '99 available providers' })
    page.assertNoProblems()
  })

  it('search narrows the catalog, combines with a chip, and says so when nothing matches', async t => {
    const page = await openCatalog(e, t)
    await page.fill(search, 'anthropic')
    await page.waitFor(() => document.querySelectorAll('section[aria-label="Provider catalog"] article').length === 1, { message: 'one Anthropic card' })
    assert.equal(await page.text({ css: `${catalogCards} h2` }), 'Anthropic')
    assert.deepEqual(await countText(page), [1, 99])
    // The description is searched too (Claude is only in Anthropic's description).
    await page.fill(search, 'claude api')
    await page.waitForElement({ css: catalogCards, text: 'Anthropic' })
    // A chip and a search together.
    await page.fill(search, 'fly')
    await page.waitForElement({ css: catalogCards, text: 'Fly.io' })
    await page.click({ css: chips, text: 'Developer tools' })
    await page.waitFor(() => /No matching providers/.test(document.body.innerText), { message: 'Fly.io is not a developer tool' })
    await page.click({ css: chips, text: 'Cloud & hosting' })
    await page.waitForElement({ css: catalogCards, text: 'Fly.io' })
    // Nothing matches.
    await page.fill(search, 'zzzz-no-such-provider')
    await page.waitForElement({ text: 'No matching providers' })
    assert.equal(await page.count({ css: catalogCards }), 0)
    assert.equal(await page.exists({ role: 'navigation', name: /pagination/ }), false, 'no pager for an empty list')
    // Clearing the search and the chip brings everything back.
    await page.fill(search, '')
    await page.click({ css: chips, text: 'All' })
    await page.waitForElement({ text: '99 available providers' })
    page.assertNoProblems()
  })

  it('paging walks through all 99 providers and a new search or chip returns to page 1', async t => {
    const page = await openCatalog(e, t)
    const nav = { role: 'navigation', name: 'Providers pagination' }
    const showing = () => page.text({ ...nav, css: 'nav' }).then(s => s.match(/Showing [\d-]+ of \d+/)?.[0])
    assert.equal(await showing(), 'Showing 1-12 of 99')
    assert.equal(await page.onElement({ role: 'button', name: 'Previous page' }, b => b.disabled), true)
    const firstPageNames = await page.onElements({ css: `${catalogCards} h2` }, h => h.textContent.trim())
    const seen = new Set(firstPageNames)
    for (const want of Array.from({ length: 8 }, (_, i) => `Showing ${i * 12 + 13}-${Math.min(99, i * 12 + 24)} of 99`)) {
      await page.click({ role: 'button', name: 'Next page' })
      await page.waitFor(w => new RegExp(w).test(document.querySelector('nav[aria-label="Providers pagination"]')?.innerText ?? ''), { args: [want], message: want })
      for (const n of await page.onElements({ css: `${catalogCards} h2` }, h => h.textContent.trim())) {
        assert.ok(!seen.has(n), `${n} appears on two pages`)
        seen.add(n)
      }
    }
    assert.equal(seen.size, CATALOG_SIZE, 'the nine pages cover the whole catalog exactly once')
    assert.equal(await page.count({ css: catalogCards }), 3, 'the last page has the remaining 3')
    assert.equal(await page.onElement({ role: 'button', name: 'Next page' }, b => b.disabled), true)
    // A search sends you back to page 1.
    await page.fill(search, 'status')
    await page.waitFor(() => /Showing 1-/.test(document.querySelector('nav[aria-label="Providers pagination"]')?.innerText ?? '') || !document.querySelector('nav[aria-label="Providers pagination"]'), { message: 'page 1 after searching' })
    await page.fill(search, '')
    await page.waitFor(() => /Showing 1-12 of 99/.test(document.querySelector('nav[aria-label="Providers pagination"]')?.innerText ?? ''), { message: 'page 1 after clearing the search' })
    // Going back one page works too.
    await page.click({ role: 'button', name: 'Next page' })
    await page.click({ role: 'button', name: 'Previous page' })
    await page.waitFor(() => /Showing 1-12 of 99/.test(document.querySelector('nav[aria-label="Providers pagination"]')?.innerText ?? ''), { message: 'Previous page returns to page 1' })
    page.assertNoProblems()
  })

  it('Connect opens the dialog prefilled with that provider; Escape returns focus to the Connect button', async t => {
    const page = await openCatalog(e, t)
    await page.fill(search, 'Anthropic')
    await page.waitFor(() => document.querySelectorAll('section[aria-label="Provider catalog"] article').length === 1, { message: 'one card' })
    await page.click({ role: 'button', name: 'Connect', exact: true })
    await page.waitForElement(dialog)
    assert.equal(await page.exists({ role: 'dialog', name: 'Connect provider feed' }), true)
    assert.equal(await page.value(inDialog({ label: /Status page or RSS feed URL/ })), 'https://status.claude.com/')
    assert.equal(await page.value(inDialog({ label: /^Provider name$/ })), 'Anthropic')
    assert.equal(await page.value(inDialog({ label: /Check interval/ })), '60', 'one minute is the default')
    // The dialog also offers the other presets, searchable.
    assert.ok((await page.count(inDialog({ css: 'button', text: 'Google Cloud' }))) >= 1)
    await page.fill(inDialog({ label: /Provider presets/ }), 'Cloudflare')
    await page.waitFor(() => document.querySelectorAll('[role=dialog] .grid button').length < 6, { message: 'the preset list narrows' })
    await page.click(inDialog({ css: '.grid button', text: 'Cloudflare Status' }))
    assert.equal(await page.value(inDialog({ label: /Status page or RSS feed URL/ })), 'https://www.cloudflarestatus.com/', 'picking a preset swaps the URL')
    assert.equal(await page.value(inDialog({ label: /^Provider name$/ })), 'Cloudflare Status')
    // Escape closes it and focus goes back to the button that opened it.
    await page.press('Escape')
    await page.waitForGone(dialog)
    const active = await page.activeElement()
    assert.equal(active?.tag, 'button')
    assert.equal(active?.text, 'Connect', `focus returns to the Connect button, got ${JSON.stringify(active)}`)
    assert.equal(await page.exists({ role: 'dialog' }), false)
    page.assertNoProblems()
  })

  for (const viewport of ['desktop', 'mobile']) {
    it(`the Connect dialog lists every alert channel with a readable name on one line (${viewport})`, async t => {
      // All 15 channel types, with names of different lengths including one far too long for the dialog.
      const made = []
      for (const [type, label] of Object.entries(CHANNEL_NAMES)) {
        const long = type === 'pagerduty' ? ' — production on-call escalation policy for the payments, billing and checkout platform teams (EMEA)' : ''
        const name = `${unique('ch')} ${label}${long}`
        const ch = await e.app.apiOk('POST', '/api/channels', { name, type, isDefault: false, config: CHANNEL_CONFIG[type] })
        made.push({ id: ch.id, name, type })
      }
      const all = (await e.app.apiOk('GET', '/api/channels')).channels
      const page = await e.session(t, { goto: '/third-party', viewport })
      await page.waitForElement({ role: 'heading', name: 'External services' })
      await page.settle()
      await page.click({ role: 'button', name: /Connect a feed/ })
      await page.waitForElement(dialog)
      await page.waitForElement(inDialog({ text: 'Alert channels' }))
      assert.match(await page.text(dialog), new RegExp(`${all.length} available`), 'the dialog says how many channels exist')

      const rows = await page.evaluate(() => [...document.querySelectorAll('[role=dialog] label')].filter(l => l.querySelector('input[type=checkbox]')).map(l => {
        const [nameEl, typeEl] = l.querySelectorAll('span')
        const r = x => x.getBoundingClientRect()
        const lineH = el => parseFloat(getComputedStyle(el).lineHeight) || parseFloat(getComputedStyle(el).fontSize) * 1.4
        return {
          name: nameEl?.textContent.trim(), title: nameEl?.getAttribute('title'), type: typeEl?.textContent.trim(),
          rowH: r(l).height, nameH: r(nameEl).height, typeH: r(typeEl).height, nameLine: lineH(nameEl), typeLine: lineH(typeEl),
          sameLine: Math.abs((r(nameEl).top + r(nameEl).height / 2) - (r(typeEl).top + r(typeEl).height / 2)) < 4,
          typeClipped: typeEl.scrollWidth > typeEl.clientWidth + 1, nameClipped: nameEl.scrollWidth > nameEl.clientWidth + 1, nameW: r(nameEl).width,
          white: getComputedStyle(typeEl).whiteSpace,
        }
      }))
      assert.equal(rows.length, all.length, 'every channel is listed (the list scrolls, nothing is cut off)')
      for (const ch of all) {
        const row = rows.find(r => r.title === ch.name)
        assert.ok(row, `channel ${ch.name} is listed under its full name`)
        assert.ok(row.name && row.name !== 'undefined' && !/\[object/.test(row.name), `readable name: ${row.name}`)
        assert.equal(row.type, CHANNEL_NAMES[ch.type], `${ch.name}: the type is shown as "${CHANNEL_NAMES[ch.type]}"`)
        assert.ok(row.nameW >= 40, `${ch.name}: the name keeps a usable width (${Math.round(row.nameW)}px)`)
        assert.ok(row.nameH < row.nameLine * 1.5, `${ch.name}: the name does not wrap (${Math.round(row.nameH)}px tall)`)
        assert.ok(row.typeH < row.typeLine * 1.5, `${ch.name}: the type label does not wrap (${Math.round(row.typeH)}px tall)`)
        assert.ok(row.sameLine, `${ch.name}: name and type sit on one line`)
        assert.equal(row.typeClipped, false, `${ch.name}: the type label is never cut off`)
        assert.ok(row.rowH < 44, `${ch.name}: one line per channel (row is ${Math.round(row.rowH)}px)`)
      }
      // The long name is shortened with an ellipsis rather than wrapped, and its full text stays available.
      const longRow = rows.find(r => r.title === made.find(m => m.type === 'pagerduty').name)
      assert.equal(longRow.nameClipped, true, 'the very long name is truncated, not wrapped')
      // The channel list scrolls inside the dialog: the dialog itself fits the viewport.
      const fits = await page.evaluate(() => { const d = document.querySelector('[role=dialog]').getBoundingClientRect(); return d.bottom <= innerHeight + 1 && d.right <= innerWidth + 1 })
      assert.ok(fits, 'the dialog fits in the window')
      // Choosing channels is saved with the feed.
      const feeds = await startFeeds()
      t.after(() => feeds.stop())
      await page.fill(inDialog({ label: /Status page or RSS feed URL/ }), feeds.url('/resolved.rss'))
      const feedName = unique('feed-ch')
      await page.fill(inDialog({ label: /^Provider name$/ }), feedName)
      const pick = [made[0], made[4], made[14]]
      for (const ch of pick) await page.click(inDialog({ role: 'checkbox', name: ch.name }))
      await page.click(inDialog({ role: 'button', name: 'Add Provider' }))
      await page.waitForGone(dialog)
      await page.waitForElement(feedCard(feedName))
      const monitors = (await e.app.apiOk('GET', '/api/monitors')).monitors
      const mon = monitors.find(m => m.name === feedName)
      assert.ok(mon, 'the feed was created')
      const full = await e.app.apiOk('GET', `/api/monitors/${mon.id}`)
      assert.deepEqual([...full.channelIds].sort((a, b) => a - b), pick.map(c => c.id).sort((a, b) => a - b), 'exactly the ticked channels are attached')
      assert.equal(full.settings.vendor, true, 'it is stored as a vendor feed')
      assert.equal(full.target, feeds.url('/resolved.rss'))
      page.assertNoProblems()
    })
  }

  it('Check now on a connected feed uses its saved settings: an incident in the feed shows as an incident', async t => {
    const feeds = await startFeeds()
    t.after(() => feeds.stop())
    const page = await e.session(t, { goto: '/third-party' })
    await page.waitForElement({ role: 'heading', name: 'External services' })
    const connect = async (path, name) => {
      await page.click({ role: 'button', name: /Connect a feed/ })
      await page.waitForElement(dialog)
      await page.fill(inDialog({ label: /Status page or RSS feed URL/ }), feeds.url(path))
      await page.fill(inDialog({ label: /^Provider name$/ }), name)
      await page.click(inDialog({ role: 'button', name: 'Add Provider' }))
      await page.waitForGone(dialog)
      await page.waitForElement(feedCard(name))
    }
    const bad = unique('feed-incident')
    const good = unique('feed-quiet')
    const meh = unique('feed-degraded')
    await connect('/incident.rss', bad)
    await connect('/resolved.rss', good)
    await connect('/degraded.rss', meh)
    // The dialog created real vendor monitors.
    const monitors = (await e.app.apiOk('GET', '/api/monitors')).monitors
    for (const n of [bad, good, meh]) assert.ok(monitors.find(m => m.name === n && m.vendor), `${n} is a vendor feed`)

    // The feed with an active outage.
    const hitsBefore = feeds.hits.filter(h => h.startsWith('/incident.rss')).length
    await page.click(inCard(bad, { role: 'button', name: 'Check now' }))
    await page.waitForElement(inCard(bad, { role: 'status' }))
    const incident = await page.text(inCard(bad, { role: 'status' }))
    assert.match(incident, /Outage/i, `the card reports the outage: ${incident}`)
    assert.match(incident, /API unavailable/, 'with the incident title')
    assert.doesNotMatch(incident, /no incident/i, 'a feed with an incident must never read "No incident reported"')
    assert.ok(feeds.hits.filter(h => h.startsWith('/incident.rss')).length > hitsBefore, 'the check really fetched the feed')

    // The feed whose latest entry is resolved.
    await page.click(inCard(good, { role: 'button', name: 'Check now' }))
    await page.waitForElement(inCard(good, { role: 'status' }))
    assert.match(await page.text(inCard(good, { role: 'status' })), /No incident reported by this feed/)

    // A degraded entry is not "no incident" either.
    await page.click(inCard(meh, { role: 'button', name: 'Check now' }))
    await page.waitForElement(inCard(meh, { role: 'status' }))
    const degraded = await page.text(inCard(meh, { role: 'status' }))
    assert.match(degraded, /Disruption|degraded/i, degraded)
    assert.doesNotMatch(degraded, /no incident/i)

    // The result can be dismissed.
    await page.click(inCard(meh, { role: 'button', name: 'Dismiss check result' }))
    await page.waitForGone(inCard(meh, { role: 'status' }))
    page.assertNoProblems()
  })

  it('a connected feed that reports an incident shows the incident on its card, a quiet one does not', async t => {
    const feeds = await startFeeds()
    t.after(() => feeds.stop())
    const mk = async (name, path) => (await e.app.apiOk('POST', '/api/monitors', {
      name, type: 'http', target: feeds.url(path), intervalS: 10, retryIntervalS: 10, retries: 0, timeoutMs: 5000, groupId: null,
      channelIds: [], tagIds: [], settings: { acceptedCodes: '200-299', vendor: true },
    })).id
    const badName = unique('live-incident')
    const goodName = unique('live-quiet')
    await mk(badName, '/incident.rss')
    await mk(goodName, '/resolved.rss')
    const page = await e.session(t, { goto: '/third-party' })
    await page.waitForElement(feedCard(badName))
    await page.waitFor(n => /Provider incident/.test([...document.querySelectorAll('section[aria-label="Connected provider feeds"] article')].find(a => a.innerText.includes(n))?.innerText ?? ''), { args: [badName], timeout: 40000, message: 'the incident feed to show "Provider incident"' })
    const bad = await page.text(feedCard(badName))
    assert.match(bad, /API unavailable/, 'the card carries the incident title')
    assert.doesNotMatch(bad, /No reported incidents/)
    await page.waitFor(n => /No reported incidents/.test([...document.querySelectorAll('section[aria-label="Connected provider feeds"] article')].find(a => a.innerText.includes(n))?.innerText ?? ''), { args: [goodName], timeout: 40000, message: 'the quiet feed to say "No reported incidents"' })
    assert.match(await page.bodyText(), /\d+ reporting incidents/)
    // Pausing and removing go through the card's "More actions" menu and an in-app dialog.
    const more = () => page.click(inCard(goodName, { role: 'button', name: /^More actions for/ }))
    await more()
    await page.click({ role: 'menuitem', name: 'Pause' })
    await page.waitFor(n => /Paused/.test([...document.querySelectorAll('section[aria-label="Connected provider feeds"] article')].find(a => a.innerText.includes(n))?.innerText ?? ''), { args: [goodName], message: 'the card to show Paused' })
    await more()
    await page.waitForElement({ role: 'menuitem', name: 'Resume' })
    await page.press('Escape')
    await page.waitForGone({ role: 'menu' })
    await more()
    await page.click({ role: 'menuitem', name: 'Remove feed' })
    await page.waitForElement({ role: 'dialog', name: `Remove ${goodName}?` })
    await page.click(inDialog({ role: 'button', name: 'Cancel' }))
    await page.waitForGone(dialog)
    assert.equal(await page.exists(feedCard(goodName)), true, 'Cancel keeps the feed')
    assert.equal(page.dialogs.length, 0, 'no native confirm()')
    await more()
    await page.click({ role: 'menuitem', name: 'Remove feed' })
    await page.click(inDialog({ role: 'button', name: 'Remove feed' }))
    await page.waitForGone(feedCard(goodName))
    page.assertNoProblems()
  })
})
