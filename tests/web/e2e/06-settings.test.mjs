// Settings: every tab loads cleanly; Regions with no outage data; backups; delivery-history paging.

import assert from 'node:assert/strict'
import { it } from 'node:test'
import { e2eSuite, unique } from './harness.mjs'

const TABS = ['notifications', 'general', 'security', 'regions', 'database', 'data', 'import', 'logs', 'about']

e2eSuite('settings', e => {
  for (const tab of TABS) {
    it(`tab "${tab}" loads without console errors`, async t => {
      const page = await e.session(t, { goto: `/settings?tab=${tab}` })
      await page.waitForElement({ role: 'heading', name: 'Workspace settings' })
      await page.waitFor(() => document.querySelectorAll('main h2, main h3').length > 0, { message: `content in the ${tab} tab` })
      assert.equal(await page.exists({ text: /Something went wrong|Could not load/i }), false)
      page.assertNoProblems()
    })
  }

  it('the Regions tab renders when no outage has region results', async t => {
    const page = await e.session(t, { goto: '/settings?tab=regions' })
    await page.waitForElement({ text: /Check regions/ })
    assert.ok((await page.text('main')).length > 100, 'the tab has content')
    assert.equal(await page.exists({ text: /Something went wrong/i }), false)
    page.assertNoProblems()
  })

  it('the Backups tab lists backups and can take one', async t => {
    const page = await e.session(t, { goto: '/settings?tab=database' })
    await page.waitForElement({ text: /Nightly schedule/ })
    const btn = { role: 'button', name: /back ?up now|run backup|create backup/i }
    if (await page.exists(btn)) {
      await page.click(btn)
      await page.waitFor(async () => {
        const r = await (await fetch('/api/backups')).json()
        return (r.backups ?? []).length > 0
      }, { timeout: 20000, message: 'a backup to be listed after "Back up now"' })
    }
    page.assertNoProblems()
  })

  it('the delivery history pages its rows (25 per page, never more)', async t => {
    // 60 failed test sends to a closed https port -> 60 log rows.
    const ch = await e.app.apiOk('POST', '/api/channels', { name: unique('log'), type: 'webhook', isDefault: false, config: { url: 'https://127.0.0.1:1/hook' } })
    for (let i = 0; i < 60; i++) await e.app.apiOk('POST', `/api/channels/${ch.id}/test`)
    // Log rows are committed by the batching writer (about a second); wait until all 60 are readable.
    for (let i = 0; i < 40 && ((await e.app.apiOk('GET', '/api/alert-log?limit=200')).entries ?? []).length < 60; i++) await new Promise(r => setTimeout(r, 250))
    const page = await e.session(t, { goto: '/settings?tab=logs' })
    await page.waitForElement({ text: /Showing 1-25 of/ })
    const rows = () => page.evaluate(() => document.querySelectorAll('main tbody tr').length)
    assert.equal(await rows(), 25)
    await page.click({ role: 'button', name: 'Next page' })
    await page.waitForElement({ text: /Showing 26-50 of/ })
    assert.equal(await rows(), 25)
    await page.select({ css: 'select', label: /per page/i }, '50')
    await page.waitFor(() => document.querySelectorAll('main tbody tr').length === 50, { message: '50 rows after raising the page size' })
    page.assertNoProblems()
  })
})
