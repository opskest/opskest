// Routing: home, redirects, sidebar highlighting, not-found, auth redirect and return, role gates.

import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { e2eSuite, randomPassword, unique } from './harness.mjs'

/** Titles of the highlighted desktop-sidebar links. */
const activeNav = page => page.evaluate(() =>
  [...document.querySelectorAll('nav[aria-label="Workspace"] a[aria-current="page"]')].map(a => a.getAttribute('title')))

const h1 = page => page.evaluate(() => [...document.querySelectorAll('h1')].map(h => h.textContent.trim()))
const loc = page => page.evaluate(() => location.pathname + location.search)

e2eSuite('routing', e => {
  it('/ is the Overview and /monitors is the monitor list', async t => {
    const page = await e.session(t, { goto: '/' })
    await page.waitForElement({ role: 'heading', name: 'Operations' })
    assert.deepEqual(await h1(page), ['Operations'])
    assert.deepEqual(await activeNav(page), ['Overview'])
    assert.ok(await page.exists({ text: 'Recent activity' }), 'the Overview has its activity section')

    await page.goto(e.url('/monitors'))
    await page.waitForElement({ role: 'heading', name: 'Monitor inventory' })
    assert.deepEqual(await h1(page), ['Monitor inventory'])
    assert.deepEqual(await activeNav(page), ['Monitors'], 'only Monitors is highlighted on /monitors')
    assert.equal(await page.exists({ role: 'heading', name: 'Operations' }), false)
    page.assertNoProblems()
  })

  it('/overview redirects to / (replacing history) and the old bookmarks land somewhere sensible', async t => {
    const page = await e.session(t, { goto: '/monitors' })
    await page.waitForElement({ role: 'heading', name: 'Monitor inventory' })
    await page.goto(e.url('/overview'))
    await page.waitForPath('/')
    await page.waitForElement({ role: 'heading', name: 'Operations' })
    // `replace`: going back must not return to /overview (which would bounce again, trapping the Back button).
    await page.evaluate(() => history.back())
    await page.waitFor(() => location.pathname !== '/overview', { message: 'Back not to land on /overview' })
    await page.waitForElement('main')
    assert.notEqual(await page.path(), '/overview')

    for (const [from, to] of [['/wallboard', '/monitors'], ['/reports', '/'], ['/latency', '/']]) {
      await page.goto(e.url(from))
      await page.waitForPath(to)
    }
    page.assertNoProblems()
  })

  it('the sidebar highlights exactly one entry per section, and clicking moves the highlight', async t => {
    const page = await e.session(t, { goto: '/' })
    // [path, sidebar title]; null = nothing is highlighted
    const cases = [
      ['/', 'Overview'], ['/monitors', 'Monitors'], ['/monitors/new', 'Monitors'], ['/projects', 'Projects'],
      ['/groups/1', 'Projects'], ['/third-party', 'Providers'], ['/incidents', 'Incidents'], ['/incidents?tab=outages', 'Incidents'],
      ['/maintenance', 'Maintenance'], ['/pages', 'Status Pages'], ['/pages/new', 'Status Pages'], ['/settings', 'Settings'],
      ['/settings?tab=logs', 'Settings'], ['/account/security', null],
    ]
    for (const [path, want] of cases) {
      await page.goto(e.url(path))
      await page.waitForElement('main')
      assert.deepEqual(await activeNav(page), want ? [want] : [], `highlighted sidebar entry on ${path}`)
    }

    // Real clicks on the sidebar.
    await page.goto(e.url('/'))
    await page.waitForElement({ role: 'heading', name: 'Operations' })
    await page.click({ css: 'nav[aria-label="Workspace"] a[title="Monitors"]' })
    await page.waitForPath('/monitors')
    assert.deepEqual(await activeNav(page), ['Monitors'])
    await page.click({ css: 'nav[aria-label="Workspace"] a[title="Incidents"]' })
    await page.waitForPath('/incidents')
    assert.deepEqual(await activeNav(page), ['Incidents'])
    await page.click({ css: 'nav[aria-label="Workspace"] a[title="Overview"]' })
    await page.waitForPath('/')
    assert.deepEqual(await activeNav(page), ['Overview'])
    // The brand mark in the sidebar goes home too.
    await page.goto(e.url('/settings'))
    await page.click({ css: 'a[aria-label="Opskest home"]' })
    await page.waitForPath('/')
    page.assertNoProblems()
  })

  it('an unknown route shows "Not found" inside the app shell', async t => {
    const page = await e.session(t, { goto: '/does-not-exist' })
    await page.waitForElement({ text: 'Not found' })
    const body = await page.bodyText()
    assert.match(body, /Not found/)
    assert.match(body, /doesn't exist/i)
    assert.deepEqual(await activeNav(page), [], 'nothing is highlighted on an unknown page')
    assert.ok(await page.exists({ css: 'nav[aria-label="Workspace"]' }), 'the navigation is still there so the user can get out')
    await page.goto(e.url('/nope/nested/deeper'))
    await page.waitForElement({ text: 'Not found' })
    // The way out works.
    await page.click({ css: 'nav[aria-label="Workspace"] a[title="Monitors"]' })
    await page.waitForPath('/monitors')
    page.assertNoProblems()
  })

  it('unknown ids show a not-found state instead of a blank or crashing page', async t => {
    const page = await e.session(t, { goto: '/' })
    const ids = [
      ['/monitors/999999/edit', /Monitor not found/],
      ['/incidents/999999', /Incident not found/],
      ['/pages/999999', /Status page not found/],
    ]
    for (const [path, re] of ids) {
      await page.goto(e.url(path))
      await page.waitFor(src => new RegExp(src).test(document.querySelector('main')?.innerText ?? ''), { args: [re.source], message: `${path} to explain that it does not exist` })
    }
    // A monitor detail page for a missing monitor says something rather than rendering an empty frame.
    await page.goto(e.url('/monitors/999999'))
    await page.waitForElement('main')
    assert.ok((await page.bodyText()).trim().length > 20, 'monitor detail for an unknown id renders text')
    page.assertNoProblems({ ignore: [/status of 404/] })
  })

  it('the account-menu links all lead somewhere (none is "Not found")', async t => {
    const page = await e.session(t, { goto: '/' })
    await page.click({ role: 'button', name: /^Account:/ })
    const links = await page.onElements({ css: '[role=menu][aria-label=Account] a[role=menuitem]' }, a => ({ href: a.getAttribute('href'), text: a.textContent.trim() }))
    assert.ok(links.length >= 2, 'the account menu has links')
    const dead = []
    for (const l of links) {
      await page.goto(e.url(l.href))
      await page.waitForElement('main')
      if (/Not found/.test(await page.text('main')) && !(await page.exists({ role: 'heading' }))) dead.push(`${l.text} -> ${l.href}`)
    }
    assert.deepEqual(dead, [], 'account-menu entries that open the Not found page')
  })

  describe('signed out', () => {
    it('a protected URL redirects to the login page and returns to it after sign-in', async t => {
      const page = await e.anonymous(t)
      for (const target of ['/monitors', '/settings?tab=logs']) {
        await page.goto(e.url(target))
        await page.waitForPath(/^\/login/)
        await page.waitForElement({ role: 'heading', name: 'Sign in' })
        assert.equal(await page.evaluate(() => new URLSearchParams(location.search).get('next')), target, 'the login URL remembers where the user was going')
        await page.fill({ css: 'input[name=username]' }, e.user)
        await page.fill({ css: 'input[name=password]' }, e.password)
        await page.click({ css: 'button[type=submit]' })
        await page.waitForPath(target.includes('?') ? target : new RegExp(`^${target}$`))
        await page.waitForElement('main')
        // Sign out again for the next round (own session; the shared one stays valid).
        await page.click({ role: 'button', name: /^Account:/ })
        await page.click({ role: 'menuitem', name: 'Sign out' })
        await page.waitForPath('/login')
      }
    })

    it('the "next" parameter cannot be used as an open redirect', async t => {
      for (const next of ['//evil.example/x', 'https://evil.example/x', '/\\evil.example']) {
        const page = await e.anonymous(t)
        await page.goto(`${e.url('/login')}?next=${encodeURIComponent(next)}`)
        await page.fill({ css: 'input[name=username]' }, e.user)
        await page.fill({ css: 'input[name=password]' }, e.password)
        await page.click({ css: 'button[type=submit]' })
        await page.waitForElement({ role: 'heading', name: 'Operations' })
        const url = new URL(await page.url())
        assert.equal(url.hostname, 'localhost', `next=${next} must not leave the app`)
        assert.equal(url.pathname, '/')
      }
    })

    it('public status URLs are not sent to the login page, and the API answers 401 without a session', async t => {
      const page = await e.anonymous(t)
      await page.goto(e.url('/status/does-not-exist'))
      // An unknown slug is indistinguishable from a private page (404, never 403), so either wording is right.
      await page.waitForElement({ text: /not found|is private/i })
      assert.equal(new URL(await page.url()).pathname, '/status/does-not-exist', 'status pages are not redirected to login')
      for (const p of ['/api/me', '/api/monitors', '/api/settings']) {
        const r = await page.api('GET', p)
        assert.equal(r.status, 401, `${p} must be 401 without a session`)
      }
    })
  })

  describe('viewer role', () => {
    it('viewers can read the workspace but admin-only routes send them home', async t => {
      const username = unique('viewer').replace(/-/g, '_')
      const password = randomPassword()
      await e.app.apiOk('POST', '/api/users', { username, password, name: 'Vera Viewer', role: 'viewer' })
      const page = await e.anonymous(t)
      await page.goto(e.url('/login'))
      await page.fill({ css: 'input[name=username]' }, username)
      await page.fill({ css: 'input[name=password]' }, password)
      await page.click({ css: 'button[type=submit]' })
      await page.waitForElement({ role: 'heading', name: 'Operations' })
      // No admin entries in the navigation.
      const titles = await page.evaluate(() => [...document.querySelectorAll('nav[aria-label="Workspace"] a')].map(a => a.getAttribute('title')))
      assert.ok(!titles.includes('Settings') && !titles.includes('Status Pages'), `viewer navigation: ${titles}`)
      for (const p of ['/settings', '/pages', '/pages/new', '/monitors/new']) {
        await page.goto(e.url(p))
        await page.waitForPath('/')
      }
      await page.goto(e.url('/monitors'))
      await page.waitForElement({ role: 'heading', name: 'Monitor inventory' })
      assert.equal(await page.exists({ role: 'link', name: 'Add monitor' }), false, 'viewers get no "Add monitor" button')
      // The server enforces the same rule.
      assert.equal((await page.api('POST', '/api/monitors', { name: 'x', type: 'tcp', target: '127.0.0.1:1' })).status, 403)
      assert.equal((await page.api('GET', '/api/settings')).status, 403)
    })
  })
})
