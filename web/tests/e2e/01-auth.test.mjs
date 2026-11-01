// First-run setup, sign out, sign in, wrong password, session persistence.

import assert from 'node:assert/strict'
import { it } from 'node:test'
import { App, e2eSuite, randomPassword } from './harness.mjs'

const MISMATCH = /do not match/i
const BAD_LOGIN = /invalid username, password or verification code/i

e2eSuite('auth', e => {
  it('first run: the setup screen validates input, creates the administrator once, and signs them in', async t => {
    // This test needs a pristine instance, so it starts its own (the suite instance already has an admin).
    const app = await App.start()
    t.after(() => app.stop())
    const ctx = await e.browser.newContext()
    t.after(() => ctx.close())
    const page = await ctx.newPage()
    const password = randomPassword()

    await page.goto(app.baseUrl)
    await page.waitForPath('/login')
    await page.waitForElement({ role: 'heading', name: 'Create your workspace' })
    assert.equal(await page.exists({ role: 'heading', name: 'Sign in' }), false, 'a fresh instance must offer setup, not sign-in')
    assert.match(await page.bodyText(), /administrator account/i)
    assert.equal(await page.count({ css: 'input[name=confirm-password]' }), 1)

    // Native constraint validation: nothing is submitted while fields are empty or too short.
    const submits = () => page.responses.filter(r => r.method === 'POST' && r.url.endsWith('/api/auth/setup')).length
    await page.click({ role: 'button', name: 'Create workspace' })
    assert.equal(submits(), 0, 'empty form must not be submitted')
    assert.equal(await page.onElement({ css: 'form' }, f => f.checkValidity()), false)

    await page.fill({ css: 'input[name=name]' }, 'Pat Admin')
    await page.fill({ css: 'input[name=username]' }, 'a b')
    await page.fill({ css: 'input[name=password]' }, 'short')
    await page.fill({ css: 'input[name=confirm-password]' }, 'short')
    assert.equal(await page.onElement({ css: 'input[name=username]' }, i => i.validity.patternMismatch || i.validity.tooShort), true, 'username with a space is rejected client-side')
    assert.equal(await page.onElement({ css: 'input[name=password]' }, i => i.validity.tooShort), true, 'short password is rejected client-side (minLength 12)')
    await page.click({ role: 'button', name: 'Create workspace' })
    assert.equal(submits(), 0)

    // Mismatching confirmation is reported by the app.
    await page.fill({ css: 'input[name=username]' }, 'pat_admin')
    await page.fill({ css: 'input[name=password]' }, password)
    await page.fill({ css: 'input[name=confirm-password]' }, `${password}x`)
    await page.click({ role: 'button', name: 'Create workspace' })
    const alert = await page.waitForElement({ role: 'alert' })
    assert.ok(alert)
    assert.match(await page.text({ css: '[role=alert]' }), MISMATCH)
    assert.equal(submits(), 0, 'mismatched confirmation must not reach the server')

    // The show/hide toggle works and is labelled.
    await page.click({ role: 'button', name: 'Show password', nth: 0 })
    assert.equal(await page.onElement({ css: 'input[name=password]' }, i => i.type), 'text')
    await page.click({ role: 'button', name: 'Hide password', nth: 0 })
    assert.equal(await page.onElement({ css: 'input[name=password]' }, i => i.type), 'password')

    // Valid: lands on the Overview, signed in, with a hardened session cookie.
    await page.fill({ css: 'input[name=confirm-password]' }, password)
    await page.click({ role: 'button', name: 'Create workspace' })
    await page.waitForPath('/')
    await page.waitForElement({ role: 'heading', name: 'Operations' })
    const cookies = await ctx.cookies()
    const sid = cookies.find(c => c.name === 'opskest.sid')
    const csrf = cookies.find(c => c.name === 'opskest.csrf')
    assert.ok(sid, 'session cookie opskest.sid is set')
    assert.ok(csrf, 'CSRF cookie opskest.csrf is set')
    assert.equal(sid.httpOnly, true, 'the session cookie must be HttpOnly')
    assert.equal(csrf.httpOnly, false, 'the CSRF cookie is readable by the SPA (double submit)')
    assert.match(sid.sameSite ?? '', /Lax|Strict/)
    const me = await page.apiOk('GET', '/api/me')
    assert.equal(me.username, 'pat_admin')
    assert.equal(me.role, 'admin')

    // Setup is one-shot, even for the signed-in admin, and the login page no longer offers it.
    const second = await page.api('POST', '/api/auth/setup', { username: 'second_admin', name: 'x', password })
    assert.equal(second.status, 409, 'a second setup must be refused')
    assert.deepEqual((await page.apiOk('GET', '/api/auth/status')), { setupRequired: false })

    // Credentials never reach the server log.
    assert.ok(!app.logs().includes(password), 'the password must never appear in the server log')
    page.assertNoProblems({ ignore: [/status of (401|409)/] })
  })

  it('sign out, wrong password, sign in, and the session surviving a reload', async t => {
    const page = await e.anonymous(t)
    await page.goto(e.url('/'))
    await page.waitForPath('/login')
    await page.waitForElement({ role: 'heading', name: 'Sign in' })

    // Wrong password: a clear message, still on the login page, no session.
    await page.fill({ css: 'input[name=username]' }, e.user)
    await page.fill({ css: 'input[name=password]' }, `${e.password}-wrong`)
    await page.click({ css: 'button[type=submit]' })
    await page.waitForElement({ role: 'alert' })
    assert.match(await page.text({ css: '[role=alert]' }), BAD_LOGIN)
    assert.equal(new URL(await page.url()).pathname, '/login')
    assert.equal((await page.api('GET', '/api/me')).status, 401)

    // An unknown user gets the very same message (no account enumeration).
    await page.fill({ css: 'input[name=username]' }, 'nobody_here')
    await page.click({ css: 'button[type=submit]' })
    await page.waitFor(() => /invalid username/i.test(document.querySelector('[role=alert]')?.textContent ?? ''), { message: 'the unknown-user error' })
    assert.match(await page.text({ css: '[role=alert]' }), BAD_LOGIN)

    // Correct credentials.
    await page.fill({ css: 'input[name=username]' }, e.user)
    await page.fill({ css: 'input[name=password]' }, e.password)
    await page.click({ css: 'button[type=submit]' })
    await page.waitForPath('/')
    await page.waitForElement({ role: 'heading', name: 'Operations' })
    assert.equal((await page.apiOk('GET', '/api/me')).username, e.user)

    // The session survives a reload, on / and on a deep link.
    await page.reload()
    await page.waitForElement({ role: 'heading', name: 'Operations' })
    assert.equal(new URL(await page.url()).pathname, '/')
    await page.goto(e.url('/monitors'))
    await page.waitForElement({ role: 'heading', name: 'Monitor inventory' })
    await page.reload()
    await page.waitForElement({ role: 'heading', name: 'Monitor inventory' })
    assert.equal(new URL(await page.url()).pathname, '/monitors')

    // While signed in, /login bounces to the Overview.
    await page.goto(e.url('/login'))
    await page.waitForPath('/')

    // Sign out through the account menu.
    await page.click({ role: 'button', name: /^Account:/ })
    await page.click({ role: 'menuitem', name: 'Sign out' })
    await page.waitForPath('/login')
    await page.waitForElement({ role: 'heading', name: 'Sign in' })
    assert.equal((await page.api('GET', '/api/me')).status, 401, 'the session must be invalid after sign out')
    // ...and a protected page no longer opens.
    await page.goto(e.url('/settings'))
    await page.waitForPath('/login')
    // The page reload must not resurrect the session either.
    await page.reload()
    await page.waitForElement({ role: 'heading', name: 'Sign in' })
  })

  it('sign out in one tab signs out the other tab of the same browser', async t => {
    const a = await e.anonymous(t)
    await a.goto(e.url('/login'))
    await a.fill({ css: 'input[name=username]' }, e.user)
    await a.fill({ css: 'input[name=password]' }, e.password)
    await a.click({ css: 'button[type=submit]' })
    await a.waitForPath('/')
    // Second tab in the same context shares the cookies.
    const b = await a.context.newPage()
    await b.goto(e.url('/monitors'))
    await b.waitForElement({ role: 'heading', name: 'Monitor inventory' })
    await a.click({ role: 'button', name: /^Account:/ })
    await a.click({ role: 'menuitem', name: 'Sign out' })
    await a.waitForPath('/login')
    // The BroadcastChannel (opskest_auth) tells the other tab; if not, the next API failure does. Either way it must end on /login.
    await b.waitFor(() => location.pathname === '/login', { timeout: 15000, message: 'the second tab to leave the app after sign out in the first' })
  })
})
