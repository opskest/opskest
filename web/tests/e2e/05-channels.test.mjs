// Alert channels in Settings: the Add dialog per type, required fields, create, masked secrets on edit, delete.

import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { e2eSuite, unique } from './harness.mjs'

// type -> expected field labels, and which of them are required.
const CHANNELS = {
  slack: { fields: [/Webhook URL/], required: [/Webhook URL/] },
  email: { fields: [/SMTP host/i, /SMTP port/i, /Username/i, /Password/i, /From address/i, /To recipients/i], required: [/SMTP host/i, /From address/i, /To recipients/i] },
  teams: { fields: [/Webhook URL/], required: [/Webhook URL/] },
  gchat: { fields: [/Webhook URL/], required: [/Webhook URL/] },
  webhook: { fields: [/Webhook URL/], required: [/Webhook URL/] },
  pagerduty: { fields: [/Routing \/ integration key/i], required: [/Routing/i] },
  opsgenie: { fields: [/Routing \/ integration key/i], required: [/Routing/i] },
  discord: { fields: [/Webhook URL/], required: [/Webhook URL/] },
  telegram: { fields: [/Bot token/, /Chat ID/], required: [/Bot token/, /Chat ID/] },
  ntfy: { fields: [/Topic URL/, /Access token/], required: [/Topic URL/] },
  gotify: { fields: [/Server URL/, /Application token/], required: [/Server URL/, /Application token/] },
  pushover: { fields: [/Application token/, /User or group key/], required: [/Application token/, /User or group key/] },
  matrix: { fields: [/Homeserver URL/, /Access token/, /Room ID/], required: [/Homeserver URL/, /Access token/, /Room ID/] },
  mattermost: { fields: [/Incoming webhook URL/], required: [/Incoming webhook URL/] },
  rocketchat: { fields: [/Incoming webhook URL/], required: [/Incoming webhook URL/] },
  zulip: { fields: [/Zulip server/, /Bot email/, /Bot API key/, /^Stream$/, /^Topic$/], required: [/Zulip server/, /Bot email/, /Bot API key/, /^Stream$/] },
  webex: { fields: [/Bot access token/, /Room ID/], required: [/Bot access token/, /Room ID/] },
  pushbullet: { fields: [/Access token/, /Device or email/], required: [/Access token/] },
  ifttt: { fields: [/Webhooks key/, /Event name/], required: [/Webhooks key/, /Event name/] },
  apprise: { fields: [/Apprise API server/, /Configuration key/, /^Username$/, /^Password$/], required: [/Apprise API server/, /Configuration key/] },
  twilio: { fields: [/Account SID/, /Auth token/, /^From$/, /To numbers/], required: [/Account SID/, /Auth token/, /^From$/, /To numbers/] },
  homeassistant: { fields: [/Home Assistant address/, /Long-lived access token/, /Notify service/], required: [/Home Assistant address/, /Long-lived access token/, /Notify service/] },
  bark: { fields: [/Bark server/, /Device key/], required: [/Device key/] },
  signal: { fields: [/signal-cli REST API/, /Sender number/, /Recipients/], required: [/signal-cli REST API/, /Sender number/, /Recipients/] },
}

const SETTINGS = '/settings?tab=notifications'
const dialog = { role: 'dialog' }
const inDialog = spec => ({ ...spec, within: dialog })
const addButton = { role: 'button', name: /Add alert channel|Add First Channel/ }

async function openAdd(e, t) {
  const page = await e.session(t, { goto: SETTINGS })
  await page.click(addButton)
  await page.waitForElement(dialog)
  return page
}

e2eSuite('alert channels', e => {
  it('the Add dialog offers all 24 channel types', async t => {
    const page = await openAdd(e, t)
    const values = await page.onElement(inDialog({ css: 'select' }), s => [...s.options].map(o => o.value))
    assert.deepEqual([...values].sort(), Object.keys(CHANNELS).sort())
  })

  describe('each type', () => {
    for (const [type, spec] of Object.entries(CHANNELS)) {
      it(`${type}: shows its fields and enforces the required ones`, async t => {
        const page = await openAdd(e, t)
        await page.select(inDialog({ css: 'select' }), type)
        for (const re of spec.fields) await page.waitForElement(inDialog({ label: re }))
        if (type !== 'email') assert.equal(await page.exists(inDialog({ label: /SMTP host/i })), false, 'email fields must not leak into other types')
        for (const re of spec.required) assert.equal(await page.onElement(inDialog({ label: re }), i => i.required), true, `${type}: ${re} must be required`)
        assert.equal(await page.onElement(inDialog({ label: /Channel name/ }), i => i.required), true, 'name is required')
        const posts = () => page.responses.filter(r => r.method === 'POST' && /\/api\/channels(\?|$)/.test(r.url)).length
        const before = posts()
        await page.click(inDialog({ role: 'button', name: 'Create channel' }))
        assert.equal(await page.exists(dialog), true, 'dialog stays open on invalid input')
        assert.equal(posts(), before, 'nothing is posted while required fields are empty')
        await page.press('Escape')
        await page.waitForGone(dialog)
        page.assertNoProblems()
      })
    }
  })

  it('create, mask secrets on edit, and delete (webhook, ntfy, telegram, email)', async t => {
    const page = await e.session(t, { goto: SETTINGS })
    const create = async (type, name, values) => {
      await page.click(addButton)
      await page.waitForElement(dialog)
      await page.fill(inDialog({ label: /Channel name/ }), name)
      await page.select(inDialog({ css: 'select' }), type)
      for (const [re, v] of values) await page.fill(inDialog({ label: re }), v)
      await page.click(inDialog({ role: 'button', name: 'Create channel' }))
      await page.waitForGone(dialog)
      await page.waitForElement({ text: name })
    }
    const secretUrl = `https://hooks.example.com/${unique('SECRETPATH')}`
    await create('webhook', unique('wh'), [[/Webhook URL/, secretUrl]])
    await create('ntfy', unique('ntfy'), [[/Topic URL/, 'https://ntfy.sh/e2e-topic']])
    await create('telegram', unique('tg'), [[/Bot token/, '123456:ABC-SECRET-TOKEN'], [/Chat ID/, '-100123']])
    await create('email', unique('mail'), [[/SMTP host/i, 'smtp.example.com'], [/From address/i, 'a@example.com'], [/To recipients/i, 'b@example.com'], [/Password/, 'smtp-secret-pass']])
    const list = (await e.app.apiOk('GET', '/api/channels')).channels
    for (const type of ['webhook', 'ntfy', 'telegram', 'email']) assert.ok(list.some(c => c.type === type), `${type} channel saved`)
    const dump = JSON.stringify(list)
    for (const s of [secretUrl, 'SECRET-TOKEN', 'smtp-secret-pass']) assert.ok(!dump.includes(s), `the API must mask ${s}`)
    assert.ok(!(await page.bodyText()).includes('SECRETPATH'), 'the page must not display the secret URL')
    // Editing the webhook: the secret is not echoed back.
    const idx = list.findIndex(c => c.type === 'webhook')
    await page.click({ role: 'button', name: 'Edit', nth: idx })
    await page.waitForElement(dialog)
    assert.equal(await page.value(inDialog({ label: /Webhook URL/ })), '', 'a stored secret URL is not echoed into the edit form')
    assert.match(await page.onElement(inDialog({ label: /Webhook URL/ }), i => i.placeholder), /Unchanged|•/)
    await page.press('Escape')
    await page.waitForGone(dialog)
    // Delete them all, each behind a confirmation.
    for (let i = 0; i < list.length; i++) {
      await page.click({ role: 'button', name: /^More actions for/, nth: 0 })
      await page.click({ role: 'menuitem', name: 'Delete channel' })
      await page.waitForElement({ role: 'dialog', name: /Delete alert channel/ })
      await page.click(inDialog({ role: 'button', name: /Delete channel/i }))
      await page.waitForGone(dialog)
    }
    await page.waitForElement({ text: 'No alert channels' })
    assert.equal(((await e.app.apiOk('GET', '/api/channels')).channels ?? []).length, 0)
  })
})
