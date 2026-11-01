// The service monitor forms that have their own options: SMTP security, Redis / MQTT login with a write-only
// password, NTP clock offset, UDP and banner "expect" (required), and gRPC. Each saves a real monitor and the
// edit page loads the values back.

import assert from 'node:assert/strict'
import { it } from 'node:test'
import { e2eSuite } from './harness.mjs'
import { monitorBody } from './util.mjs'
import { field, fieldState, labelText, openForm, pickType, postsToMonitors, submitCreate, submitInvalid, unique } from './forms.mjs'

const ignoreTls = { role: 'switch', name: /Ignore TLS errors/ }
const tlsBox = re => ({ role: 'switch', name: re })
const SECRET_SET = { role: 'group', name: 'Password: a value is set' }

/** Opens a stored monitor's edit page with every section visible. */
async function openEdit(e, t, id, name) {
  const page = await e.session(t, { goto: `/monitors/${id}/edit` })
  await page.waitForElement({ role: 'heading', name: `Edit ${name}` })
  await page.click({ css: 'nav[aria-label="Monitor configuration"] button', text: 'All settings' })
  return page
}

e2eSuite('service monitor forms', e => {
  it('SMTP: the three security modes, the TLS-error box only with encryption, and the choice is saved', async t => {
    const page = await openForm(e, t)
    await pickType(page, 'smtp', 'SMTP')
    const security = field(/^Security$/)
    const options = await page.onElement(security, el => Array.from(el.options).map(o => `${o.value}|${o.textContent.trim()}`))
    assert.deepEqual(options, ['|None — plain connection', 'starttls|STARTTLS — upgrade after greeting', 'tls|TLS — encrypted from the start'])
    assert.equal(await page.value(security), '', 'plain is the default')
    assert.match(await page.bodyText(), /Port 587 usually starts plain and upgrades with STARTTLS; 465 is TLS from the first byte/)
    assert.equal(await page.exists(ignoreTls), false, 'plain connections have no certificate to ignore')
    for (const mode of ['STARTTLS — upgrade after greeting', 'TLS — encrypted from the start']) {
      await page.select(security, mode)
      await page.waitForElement(ignoreTls)
    }
    await page.select(security, 'None — plain connection')
    await page.waitForGone(ignoreTls)

    const name = unique('smtp-e2e')
    await page.fill(field('Name'), name)
    await page.fill(field('Host and port'), '127.0.0.1:2525')
    await page.select(security, 'STARTTLS — upgrade after greeting')
    await page.click(ignoreTls)
    const id = await submitCreate(page)
    const stored = await e.app.apiOk('GET', `/api/monitors/${id}`)
    assert.equal(stored.settings.security, 'starttls')
    assert.equal(stored.settings.ignoreTls, true)

    const edit = await openEdit(e, t, id, name)
    assert.equal(await edit.value(field(/^Security$/)), 'starttls', 'the edit page loads the saved mode')
    assert.equal(await edit.onElement(ignoreTls, el => el.checked), true)
    await edit.select(field(/^Security$/), 'TLS — encrypted from the start')
    await edit.click({ role: 'button', name: 'Save changes' })
    await edit.waitForPath(`/monitors/${id}`)
    assert.equal((await e.app.apiOk('GET', `/api/monitors/${id}`)).settings.security, 'tls')
    // The server refuses an unknown mode.
    const bad = await edit.api('POST', '/api/monitors', monitorBody({ name: unique('smtp-bad'), type: 'smtp', target: '127.0.0.1:25', settings: { security: 'ssl3' } }))
    assert.equal(bad.status, 400)
    assert.ok(bad.json?.fields?.['settings.security'], 'the server reports settings.security')
    edit.assertNoProblems({ ignore: [/status of 400/] })
  })

  for (const T of [
    { type: 'redis', label: 'Redis', addr: '127.0.0.1:6379', userHint: /Redis 6\+ ACL user/ },
    { type: 'mqtt', label: 'MQTT', addr: '127.0.0.1:1883', userHint: /connect anonymously/ },
  ]) {
    it(`${T.type}: username and password are optional, the password is write-only and masked on edit`, async t => {
      const page = await openForm(e, t)
      await pickType(page, T.type, T.label)
      assert.match(await page.bodyText(), T.userHint)
      assert.ok(await page.exists(field(/^Username/)))
      assert.ok(await page.exists({ css: 'input[type=password]' }))
      assert.match(await labelText(page, /^Username/) ?? '', /Optional/)
      // TLS options.
      assert.equal(await page.exists(ignoreTls), false)
      await page.click(tlsBox(/TLS — encrypt the connection/))
      await page.waitForElement(ignoreTls)
      await page.click(tlsBox(/TLS — encrypt the connection/))
      await page.waitForGone(ignoreTls)

      // Anonymous works: no username, no password.
      const anon = unique(`${T.type}-anon`)
      await page.fill(field('Name'), anon)
      await page.fill(field('Host and port'), T.addr)
      const anonId = await submitCreate(page)
      const anonStored = await e.app.apiOk('GET', `/api/monitors/${anonId}`)
      assert.ok(!anonStored.settings.basicUser)
      assert.equal(anonStored.secrets.basicPass.isSet, false)

      // With a login.
      const page2 = await openForm(e, t)
      await pickType(page2, T.type, T.label)
      const name = unique(`${T.type}-auth`)
      const password = `pw-${unique(T.type)}-hunter2`
      await page2.fill(field('Name'), name)
      await page2.fill(field('Host and port'), T.addr)
      await page2.fill(field(/^Username/), 'e2e-user')
      await page2.fill({ css: 'input[type=password]' }, password)
      const id = await submitCreate(page2)
      const stored = await e.app.apiOk('GET', `/api/monitors/${id}`)
      assert.equal(stored.settings.basicUser, 'e2e-user')
      assert.equal(stored.secrets.basicPass.isSet, true)
      assert.ok(!JSON.stringify(stored).includes(password), 'the API response never carries the password')
      const list = await e.app.apiOk('GET', '/api/monitors')
      assert.ok(!JSON.stringify(list).includes(password), 'neither does the list')

      // Edit: the username is shown, the password is only a mask and is nowhere in the page.
      const edit = await openEdit(e, t, id, name)
      assert.equal(await edit.value(field(/^Username/)), 'e2e-user')
      await edit.waitForElement(SECRET_SET)
      assert.match(await edit.text(SECRET_SET), /•••\s*set/)
      assert.equal(await edit.exists({ css: 'input[type=password]' }), false)
      const dump = await edit.evaluate(() => document.documentElement.outerHTML + [...document.querySelectorAll('input,textarea')].map(i => i.value).join('|'))
      assert.ok(!dump.includes(password), 'the password is not in the DOM')
      // Replace refuses an empty value; Cancel restores the mask.
      await edit.click({ role: 'button', name: 'Replace' })
      await edit.waitForElement({ css: 'input[type=password]' })
      await submitInvalid(edit, 'Save changes')
      assert.match(await edit.bodyText(), /enter the new password, or cancel Replace/)
      await edit.click({ css: 'div:has(> input[type=password]) > button', text: 'Cancel' })
      await edit.waitForElement(SECRET_SET)
      // Saving without touching the password keeps it.
      await edit.fill(field(/^Username/), 'e2e-user-2')
      await edit.click({ role: 'button', name: 'Save changes' })
      await edit.waitForPath(`/monitors/${id}`)
      const kept = await e.app.apiOk('GET', `/api/monitors/${id}`)
      assert.equal(kept.settings.basicUser, 'e2e-user-2')
      assert.equal(kept.secrets.basicPass.isSet, true, 'an edit that leaves the password alone keeps it')
      // Clear removes it.
      const edit2 = await openEdit(e, t, id, name)
      await edit2.click({ role: 'button', name: 'Clear' })
      await edit2.waitForElement({ role: 'group', name: 'Password: will be removed' })
      await edit2.click({ role: 'button', name: 'Save changes' })
      await edit2.waitForPath(`/monitors/${id}`)
      assert.equal((await e.app.apiOk('GET', `/api/monitors/${id}`)).secrets.basicPass.isSet, false, 'Clear removes the stored password')
      edit2.assertNoProblems()
    })
  }

  it('NTP: the clock-offset limit is optional, bounded, saved and loaded back', async t => {
    const page = await openForm(e, t)
    await pickType(page, 'ntp', 'NTP')
    const offset = field(/^Max clock offset/)
    assert.match(await labelText(page, /^Max clock offset/) ?? '', /Optional/)
    assert.match(await page.bodyText(), /0 = only check that it answers/)
    assert.equal(await page.value(offset), '0', 'defaults to "only check that it answers"')
    await page.fill(field('Name'), unique('ntp-bad'))
    await page.fill(field('Host (and port)'), '127.0.0.1:1237')
    for (const bad of ['-1', '86400001']) {
      await page.fill(offset, bad)
      await submitInvalid(page)
      assert.equal((await fieldState(page, /^Max clock offset/)).error, 'must be 0-86400000', `offset ${bad}`)
    }
    await page.fill(offset, '')
    await submitInvalid(page)
    assert.equal((await fieldState(page, /^Max clock offset/)).error, 'must be 0-86400000', 'an empty offset is not a number')
    assert.equal(postsToMonitors(page), 0)

    const name = unique('ntp-e2e')
    await page.fill(field('Name'), name)
    await page.fill(offset, '250')
    const id = await submitCreate(page)
    assert.equal((await e.app.apiOk('GET', `/api/monitors/${id}`)).settings.maxOffsetMs, 250)
    const edit = await openEdit(e, t, id, name)
    assert.equal(await edit.value(field(/^Max clock offset/)), '250')
    await edit.fill(field(/^Max clock offset/), '0')
    await edit.click({ role: 'button', name: 'Save changes' })
    await edit.waitForPath(`/monitors/${id}`)
    assert.ok(!((await e.app.apiOk('GET', `/api/monitors/${id}`)).settings.maxOffsetMs), 'zero means no offset limit')
    const bad = await edit.api('POST', '/api/monitors', monitorBody({ name: unique('ntp-srv'), type: 'ntp', target: 'pool.ntp.org', settings: { maxOffsetMs: 86400001 } }))
    assert.equal(bad.status, 400)
    assert.ok(bad.json?.fields?.['settings.maxOffsetMs'])
    edit.assertNoProblems({ ignore: [/status of 400/] })
  })

  it('UDP: the expected reply is required (client and server), everything else is optional', async t => {
    const page = await openForm(e, t)
    await pickType(page, 'udp', 'UDP')
    assert.match(await labelText(page, /^Send/) ?? '', /Optional/)
    assert.doesNotMatch(await labelText(page, /^Expect in the reply/) ?? 'x', /Optional/, 'Expect is not optional')
    assert.match(await page.bodyText(), /hex:68656c6c6f/)
    const name = unique('udp-e2e')
    await page.fill(field('Name'), name)
    await page.fill(field('Host and port'), e.target.udpAddr)
    await submitInvalid(page)
    const state = await fieldState(page, /^Expect in the reply$/)
    assert.match(state.error ?? '', /required: UDP has no handshake/)
    assert.equal(state.invalid, true)
    // Whitespace is not an answer either.
    await page.fill(field(/^Expect in the reply$/), '   ')
    await submitInvalid(page)
    assert.match((await fieldState(page, /^Expect in the reply$/)).error ?? '', /required/)
    assert.equal(postsToMonitors(page), 0)
    const srv = await page.api('POST', '/api/monitors', monitorBody({ name: unique('udp-srv'), type: 'udp', target: e.target.udpAddr, settings: {} }))
    assert.equal(srv.status, 400, 'the server also requires it')
    assert.ok(srv.json?.fields?.['settings.expect'])

    await page.fill(field(/^Send/), 'hex:70696e67')
    await page.fill(field(/^Expect in the reply$/), 'pong')
    await page.click({ role: 'button', name: 'Test now' })
    await page.waitFor(() => /Check (passed|failed)/.test(document.querySelector('[aria-label="Test result"]')?.innerText ?? ''), { message: 'the UDP test result' })
    assert.match(await page.text({ role: 'status', name: 'Test result' }), /Check passed/, 'hex payload "ping" is echoed as "pong" by the local target')
    const id = await submitCreate(page)
    const stored = await e.app.apiOk('GET', `/api/monitors/${id}`)
    assert.equal(stored.settings.send, 'hex:70696e67')
    assert.equal(stored.settings.expect, 'pong')
    page.assertNoProblems({ ignore: [/status of 400/] })
  })

  it('banner: the first-line text is required (client and server) and TLS adds the certificate option', async t => {
    const page = await openForm(e, t)
    await pickType(page, 'banner', 'Banner (FTP, IMAP, POP3…)')
    const expect = field(/^First line must contain$/)
    assert.match(await page.bodyText(), /220 for FTP, \* OK for IMAP, \+OK for POP3/)
    const name = unique('banner-e2e')
    await page.fill(field('Name'), name)
    await page.fill(field('Host and port'), e.target.tcpAddr)
    await submitInvalid(page)
    const state = await fieldState(page, /^First line must contain$/)
    assert.match(state.error ?? '', /required: the text the first line should contain/)
    assert.equal(state.invalid, true)
    assert.equal(postsToMonitors(page), 0)
    const srv = await page.api('POST', '/api/monitors', monitorBody({ name: unique('banner-srv'), type: 'banner', target: e.target.tcpAddr, settings: {} }))
    assert.equal(srv.status, 400)
    assert.ok(srv.json?.fields?.['settings.expect'])

    assert.equal(await page.exists(ignoreTls), false)
    await page.click(tlsBox(/TLS — connect with TLS from the start/))
    await page.waitForElement(ignoreTls)
    await page.click(tlsBox(/TLS — connect with TLS from the start/))
    await page.waitForGone(ignoreTls)

    await page.fill(expect, '220')
    await page.click({ role: 'button', name: 'Test now' })
    await page.waitFor(() => /Check (passed|failed)/.test(document.querySelector('[aria-label="Test result"]')?.innerText ?? ''), { message: 'the banner test result' })
    assert.match(await page.text({ role: 'status', name: 'Test result' }), /Check passed/)
    await page.fill(expect, 'definitely-not-the-banner')
    await page.click({ role: 'button', name: 'Test now' })
    await page.waitFor(() => /Check failed/.test(document.querySelector('[aria-label="Test result"]')?.innerText ?? ''), { message: 'the mismatching banner to fail' })
    await page.fill(expect, '220')
    const id = await submitCreate(page)
    assert.equal((await e.app.apiOk('GET', `/api/monitors/${id}`)).settings.expect, '220')
    page.assertNoProblems({ ignore: [/status of 400/] })
  })

  it('gRPC: optional service name, TLS options, saved and loaded back', async t => {
    const page = await openForm(e, t)
    await pickType(page, 'grpc', 'gRPC health')
    assert.match(await labelText(page, /^Service name/) ?? '', /Optional/)
    assert.match(await page.bodyText(), /Empty = the server overall\. Otherwise the fully qualified service, e\.g\. orders\.v1\.Orders/)
    assert.equal(await page.exists(ignoreTls), false)
    await page.click(tlsBox(/TLS — use HTTPS/))
    await page.waitForElement(ignoreTls)
    const name = unique('grpc-e2e')
    await page.fill(field('Name'), name)
    await page.fill(field('Host and port'), '127.0.0.1:50051')
    await page.fill(field(/^Service name/), 'orders.v1.Orders')
    await page.click(ignoreTls)
    const id = await submitCreate(page)
    const stored = await e.app.apiOk('GET', `/api/monitors/${id}`)
    assert.equal(stored.settings.grpcService, 'orders.v1.Orders')
    assert.equal(stored.settings.tls, true)
    assert.equal(stored.settings.ignoreTls, true)

    const edit = await openEdit(e, t, id, name)
    assert.equal(await edit.value(field(/^Service name/)), 'orders.v1.Orders')
    assert.equal(await edit.onElement(tlsBox(/TLS — use HTTPS/), el => el.checked), true)
    assert.equal(await edit.onElement(ignoreTls, el => el.checked), true)
    // Turning TLS off hides the certificate option and stores plain HTTP/2; an empty service means "the server overall".
    await edit.click(tlsBox(/TLS — use HTTPS/))
    await edit.waitForGone(ignoreTls)
    await edit.fill(field(/^Service name/), '')
    await edit.click({ role: 'button', name: 'Save changes' })
    await edit.waitForPath(`/monitors/${id}`)
    const after = await e.app.apiOk('GET', `/api/monitors/${id}`)
    assert.ok(!after.settings.tls)
    assert.ok(!after.settings.grpcService)
    edit.assertNoProblems()
  })
})
