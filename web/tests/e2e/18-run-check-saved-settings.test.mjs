// "Run check" on a monitor's page must run the check with the monitor's SAVED settings (keyword, expected
// status codes, UDP expectation, vendor feed...), like the scheduler does. Running a bare check of the target
// would say "Check passed" for a monitor that is really failing.

import assert from 'node:assert/strict'
import { it } from 'node:test'
import { e2eSuite, unique } from './harness.mjs'
import { monitorBody } from './util.mjs'

const result = { role: 'status' }

e2eSuite('run check uses saved settings', e => {
  it('an HTTP monitor whose keyword is not on the page fails "Run check", exactly as its scheduled check does', async t => {
    const mon = await e.app.apiOk('POST', '/api/monitors', monitorBody({
      name: unique('keyword-svc'), type: 'http', target: e.target.httpUrl, intervalS: 10, settings: { keyword: 'this-text-is-not-served' },
    }))
    // The scheduled check (the truth) goes down because of the keyword.
    for (let i = 0; i < 80; i++) {
      const m = (await e.app.apiOk('GET', '/api/monitors')).monitors.find(x => x.id === mon.id)
      if (m?.status === 'down') break
      await new Promise(r => setTimeout(r, 500))
    }
    assert.equal((await e.app.apiOk('GET', '/api/monitors')).monitors.find(x => x.id === mon.id)?.status, 'down', 'the scheduled check fails on the missing keyword')

    const page = await e.session(t, { goto: `/monitors/${mon.id}` })
    await page.waitForElement({ role: 'heading', name: mon.name })
    await page.click({ role: 'button', name: 'Run check' })
    await page.waitFor(() => /Check (passed|failed)/.test(document.body.innerText), { message: 'the Run check result' })
    const text = await page.bodyText()
    assert.match(text, /Check failed/, 'Run check must agree with the scheduler: the keyword is missing')
    assert.doesNotMatch(text, /Check passed/)
    page.assertNoProblems()
  })

  it('an HTTP monitor with expected status codes that the target does not return fails "Run check"', async t => {
    const mon = await e.app.apiOk('POST', '/api/monitors', monitorBody({
      name: unique('codes-svc'), type: 'http', target: e.target.httpUrl, intervalS: 10, settings: { acceptedCodes: '418' },
    }))
    const page = await e.session(t, { goto: `/monitors/${mon.id}` })
    await page.waitForElement({ role: 'heading', name: mon.name })
    await page.click({ role: 'button', name: 'Run check' })
    await page.waitFor(() => /Check (passed|failed)/.test(document.body.innerText), { message: 'the Run check result' })
    assert.match(await page.bodyText(), /Check failed/, 'the target answers 200, only 418 is accepted')
    page.assertNoProblems()
  })

  it('a UDP monitor whose expected reply never comes fails "Run check"', async t => {
    const mon = await e.app.apiOk('POST', '/api/monitors', monitorBody({
      name: unique('udp-svc'), type: 'udp', target: e.target.udpAddr, intervalS: 10, settings: { send: 'ping', expect: 'definitely-not-the-reply' },
    }))
    const page = await e.session(t, { goto: `/monitors/${mon.id}` })
    await page.waitForElement({ role: 'heading', name: mon.name })
    await page.click({ role: 'button', name: 'Run check' })
    await page.waitFor(() => /Check (passed|failed)/.test(document.body.innerText), { message: 'the Run check result' })
    assert.match(await page.bodyText(), /Check failed/, 'the echo server answers "pong", not the expected text')
    page.assertNoProblems()
  })
})
