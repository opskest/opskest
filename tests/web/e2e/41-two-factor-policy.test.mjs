// Two-factor policy: the Account security page, the admin policy switch, reset from the team table, and the
// enforced set-up flow (every other route sends the user to Account security until an authenticator is on).

import assert from 'node:assert/strict'
import { createHmac, randomBytes } from 'node:crypto'
import { it } from 'node:test'
import { e2eSuite } from './harness.mjs'

const B32 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
function totp(secret, stepOffset = 0) {
  let bits = ''
  for (const ch of secret.replace(/=+$/, '')) bits += B32.indexOf(ch).toString(2).padStart(5, '0')
  const key = Buffer.from(bits.match(/.{8}/g).map(b => parseInt(b, 2)))
  const counter = Buffer.alloc(8)
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000) + stepOffset))
  const h = createHmac('sha1', key).update(counter).digest()
  const o = h[h.length - 1] & 15
  return String((h.readUInt32BE(o) & 0x7fffffff) % 1000000).padStart(6, '0')
}

/** Signs a teammate in over HTTP and turns two-factor on for them. */
async function teammateWithTwoFactor(e, username, password) {
  const base = `http://127.0.0.1:${e.app.port}`
  const jar = new Map()
  const call = async (path, body) => {
    const headers = { Origin: e.app.baseUrl, 'Content-Type': 'application/json', Cookie: [...jar].map(([k, v]) => `${k}=${v}`).join('; ') }
    const csrf = jar.get('opskest.csrf')
    if (csrf) headers['X-CSRF-Token'] = decodeURIComponent(csrf)
    const res = await fetch(base + path, { method: 'POST', headers, body: JSON.stringify(body) })
    for (const c of res.headers.getSetCookie()) { const [kv] = c.split(';'); const i = kv.indexOf('='); jar.set(kv.slice(0, i), kv.slice(i + 1)) }
    const text = await res.text()
    assert.ok(res.ok, `${path} -> ${res.status} ${text.slice(0, 200)}`)
    return text ? JSON.parse(text) : null
  }
  await call('/api/auth/login', { username, password })
  const { secret } = await call('/api/auth/2fa/enroll', { password })
  await call('/api/auth/2fa/enable', { code: totp(secret) })
}

e2eSuite('two-factor policy', e => {
  it('the team table shows who has two-factor on and an admin can reset it', async t => {
    const password = randomBytes(12).toString('hex')
    await e.app.apiOk('POST', '/api/users', { username: 'lost_phone', password, name: 'Lost Phone', role: 'user' })
    await e.app.apiOk('POST', '/api/users', { username: 'no_second_step', password, name: 'No Second', role: 'viewer' })
    await teammateWithTwoFactor(e, 'lost_phone', password)
    const page = await e.session(t, { goto: '/settings?tab=security' })
    await page.waitForElement({ css: 'th', text: /two-factor/i })
    const rowOf = name => ({ css: `tr:has(button[aria-label="Remove ${name}"])` })
    assert.match(await page.text(rowOf('lost_phone')), /\bOn\b/)
    assert.match(await page.text(rowOf('no_second_step')), /\bOff\b/)
    assert.equal(await page.exists({ role: 'button', name: 'Reset two-factor for no_second_step' }), false, 'nothing to reset for a user without two-factor')
    assert.equal(await page.exists({ role: 'button', name: /Reset two-factor for e2e_admin/ }), false, 'no reset action on your own row')
    await page.click({ role: 'button', name: 'Reset two-factor for lost_phone' })
    await page.waitForElement({ role: 'dialog' })
    assert.match(await page.text({ role: 'dialog' }), /asked to set it up again the next time they sign in/)
    await page.click({ role: 'button', name: /^Reset two-factor$/ })
    await page.waitForGone({ role: 'dialog' })
    await page.waitFor(() => /\bOff\b/.test(document.querySelector('tr:has(button[aria-label="Remove lost_phone"])')?.innerText ?? ''), { message: 'lost_phone to show Off' })
    page.assertNoProblems()
  })

  it('requiring two-factor funnels everyone to Account security until it is set up, and cannot be turned off by the user', async t => {
    const page = await e.session(t, { goto: '/settings?tab=security' })
    await page.waitForElement({ role: 'switch', name: /Require two-factor authentication/ })
    await page.waitFor(() => /\d+ of \d+ users have two-factor on/.test(document.body.innerText), { message: 'the two-factor summary' })
    await page.click({ role: 'switch', name: /Require two-factor authentication/ })
    await page.click({ role: 'button', name: 'Save settings' })
    await page.waitFor(async () => (await (await fetch('/api/settings')).json()).requireTwoFactor === true, { message: 'the policy to be saved' })

    // Any other page now lands on Account security with the notice, and the navigation is gone.
    await page.goto(e.url('/monitors'))
    await page.waitForPath('/account/security')
    await page.waitForElement({ css: 'main [role=status]' })
    assert.match(await page.bodyText(), /Your administrator requires two-factor authentication\. Set it up to continue\./)
    assert.equal(await page.exists({ css: 'nav[aria-label=Workspace]' }), false, 'no workspace navigation while locked')
    assert.equal(await page.exists({ role: 'button', name: 'Search workspace' }), false, 'no search while locked')

    // Set it up through the page.
    await page.click({ role: 'button', name: 'Enable two-factor authentication' })
    await page.fill({ css: 'input[name=password]' }, e.password)
    await page.click({ role: 'button', name: 'Continue' })
    await page.waitForElement({ label: 'Setup key' })
    const secret = await page.value({ label: 'Setup key' })
    await page.fill({ css: 'input[name=code]' }, totp(secret))
    await page.click({ role: 'button', name: 'Verify and enable' })
    await page.waitForElement({ role: 'list', name: 'Recovery codes' })
    await page.click({ role: 'checkbox', name: /I have saved these recovery codes/ })
    await page.click({ role: 'button', name: 'Done' })
    await page.waitForPath('/')
    await page.waitForElement({ css: 'nav[aria-label=Workspace]' })

    // With the policy on, the user cannot switch it off: the 409 becomes a disabled button and a note.
    await page.goto(e.url('/account/security'))
    await page.waitForElement({ role: 'button', name: 'Disable two-factor authentication' })
    assert.match(await page.text({ css: 'main' }), /1[0-9]? recovery codes left|recovery codes left/)
    await page.click({ role: 'button', name: 'Disable two-factor authentication' })
    await page.fill({ css: 'input[name=password]' }, e.password)
    await page.fill({ css: 'input[name=code]' }, totp(secret, 1))
    await page.click({ css: 'form button[type=submit]' })
    await page.waitFor(() => /can.t be turned off/.test(document.body.innerText), { message: 'the policy note' })
    assert.equal(await page.onElement({ role: 'button', name: 'Disable two-factor authentication' }, b => b.disabled), true)
    // The runtime call at page load and the refused disable are expected refusals, not defects.
    page.assertNoProblems({ ignore: [/status of 403.*\/api\/runtime/, /status of 409/] })
  })
})
