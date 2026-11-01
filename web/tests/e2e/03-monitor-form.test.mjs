// The monitor form: every monitor type (picker, target label/placeholder, type-specific fields, client-side
// validation), keyboard navigation, and create / test / edit / delete of real monitors against local targets.

import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { e2eSuite, unique } from './harness.mjs'
import { goMonitorTypes, monitorBody } from './util.mjs'

const HOST_PORT_BAD = { bad: 'localhost', msg: /must be host:port/, bigPort: 'localhost:70000', bigMsg: /port must be 1-65535/ }

// What the user sees for each type. Written by hand from the product spec (README / Docs.md, "Monitors"), NOT
// derived from the app's own tables, so a wrong label or a missing field is caught.
const TYPES = {
  http: { label: 'HTTP(S)', group: 'Web', target: ['URL', 'https://api.example.com/health'], bad: 'not a url', msg: /absolute http\(s\) URL with a host/, good: 'http://127.0.0.1:9/ok',
    fields: [/^Keyword$/, /^Mode$/, /^Path$/, /^Operator$/, /Case sensitive/, /Check domain expiry/], buttons: ['Add check'] },
  websocket: { label: 'WebSocket', group: 'Web', target: ['WebSocket URL', 'wss://live.example.com/socket'], bad: 'http://example.com', msg: /must be a ws\(s\) URL/, good: 'ws://127.0.0.1:9/socket',
    fields: [/Send after connecting/, /Expect in the first reply/], buttons: ['Add header'] },
  tls: { label: 'SSL certificate', group: 'Web', target: ['Host (and port)', 'example.com'], bad: 'bad/host', msg: /must be a host or host:port/, good: 'example.com:443', fields: [/Ignore TLS errors/] },
  tcp: { label: 'TCP port', group: 'Network', target: ['Host and port', 'db.internal:5432'], ...HOST_PORT_BAD, good: '127.0.0.1:9', fields: [/TLS — do a handshake/] },
  ping: { label: 'Ping', group: 'Network', target: ['Host or IP', '10.0.0.12'], empty: /host or IP required/, good: '127.0.0.1', fields: [/Packets per check/, /Max packet loss/] },
  dns: { label: 'DNS', group: 'Network', target: ['Hostname', 'example.com'], empty: /hostname required/, good: 'example.com', fields: [/^Record$/, /^Resolver/, /^Match$/, /Expected answers/], buttons: ['Use current answers'] },
  udp: { label: 'UDP', group: 'Network', target: ['Host and port', 'game.example.com:27015'], ...HOST_PORT_BAD, good: '127.0.0.1:9', fields: [/^Send/, /Expect in the reply/], required: /required: UDP has no handshake/ },
  ntp: { label: 'NTP', group: 'Network', target: ['Host (and port)', 'pool.ntp.org'], bad: 'a b', msg: /must be a host or host:port/, good: 'pool.ntp.org', fields: [/Max clock offset/] },
  postgres_sql: { label: 'PostgreSQL queries', group: 'Databases', target: ['Host and port', 'db.internal:5432'], ...HOST_PORT_BAD, good: '127.0.0.1:5432',
    fields: [/^Username$/, /^Password$/, /^Database/, /TLS — encrypt the connection/, /^Query$/], text: [/What to check/], required: /required/ },
  mysql_sql: { label: 'MySQL / MariaDB queries', group: 'Databases', target: ['Host and port', 'db.internal:3306'], ...HOST_PORT_BAD, good: '127.0.0.1:3306',
    fields: [/^Username$/, /^Password$/, /^Database/, /TLS — encrypt the connection/, /^Query$/], text: [/What to check/], required: /required/ },
  ssh: { label: 'SSH', group: 'Services', target: ['Host and port', 'server.example.com:22'], ...HOST_PORT_BAD, good: '127.0.0.1:22', text: [/SSH banner/] },
  smtp: { label: 'SMTP', group: 'Services', target: ['Host and port', 'mail.example.com:587'], ...HOST_PORT_BAD, good: '127.0.0.1:25', fields: [/^Security$/] },
  redis: { label: 'Redis', group: 'Services', target: ['Host and port', 'cache.internal:6379'], ...HOST_PORT_BAD, good: '127.0.0.1:6379', fields: [/^Username/, /^Password$/, /TLS — encrypt the connection/] },
  postgres: { label: 'PostgreSQL', group: 'Services', target: ['Host and port', 'db.internal:5432'], ...HOST_PORT_BAD, good: '127.0.0.1:5432', text: [/No login is attempted/] },
  mysql: { label: 'MySQL / MariaDB', group: 'Services', target: ['Host and port', 'db.internal:3306'], ...HOST_PORT_BAD, good: '127.0.0.1:3306', text: [/No login is attempted/] },
  mongodb: { label: 'MongoDB', group: 'Services', target: ['Host and port', 'db.internal:27017'], ...HOST_PORT_BAD, good: '127.0.0.1:27017', fields: [/TLS — encrypt the connection/] },
  mqtt: { label: 'MQTT', group: 'Services', target: ['Host and port', 'broker.internal:1883'], ...HOST_PORT_BAD, good: '127.0.0.1:1883', fields: [/^Username/, /^Password$/, /TLS — encrypt the connection/] },
  amqp: { label: 'RabbitMQ / AMQP', group: 'Services', target: ['Host and port', 'rabbit.internal:5672'], ...HOST_PORT_BAD, good: '127.0.0.1:5672', fields: [/TLS — encrypt the connection \(amqps\)/] },
  memcached: { label: 'Memcached', group: 'Services', target: ['Host and port', 'cache.internal:11211'], ...HOST_PORT_BAD, good: '127.0.0.1:11211', text: [/VERSION/] },
  grpc: { label: 'gRPC health', group: 'Services', target: ['Host and port', 'api.internal:50051'], ...HOST_PORT_BAD, good: '127.0.0.1:50051', fields: [/Service name/, /TLS — use HTTPS/] },
  banner: { label: 'Banner (FTP, IMAP, POP3…)', group: 'Services', target: ['Host and port', 'ftp.example.com:21'], ...HOST_PORT_BAD, good: '127.0.0.1:21', fields: [/First line must contain/, /TLS — connect with TLS/], required: /required: the text the first line should contain/ },
  push: { label: 'Push', group: 'Heartbeat', target: null, fields: [/Grace period/], text: [/no target/] },
  manual: { label: 'Manual status', group: 'Heartbeat', target: null, text: [/no target/, /set it up, degraded or down/] },
  group: { label: 'Group', group: 'Logic', target: null, fields: [/^The group is up when$/], text: [/no target/], required: /pick 1 to 100 monitors/ },
}

const BAD_NAME = /must be 1-120 characters/
const NAV = 'nav[aria-label="Monitor configuration"]'
const radio = type => ({ css: `[role=radio][data-type="${type}"]` })
const field = label => ({ label: label instanceof RegExp ? label : new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`) })
const checkedTypes = page => page.evaluate(() => [...document.querySelectorAll('[role=radio][data-type][aria-checked=true]')].map(r => r.dataset.type))
const postsToMonitors = page => page.responses.filter(r => r.method === 'POST' && /\/api\/monitors(\?|$)/.test(r.url)).length

async function openForm(e, t, path = '/monitors/new') {
  const page = await e.session(t, { goto: path })
  await page.waitForElement({ role: 'heading', name: /Add monitor|Edit/ })
  await page.click({ css: `${NAV} button`, text: 'All settings' })
  return page
}

e2eSuite('monitor form', e => {
  const goTypes = goMonitorTypes()

  it('the type picker offers exactly the 24 types the server accepts, in labelled groups', async t => {
    assert.equal(goTypes.length, 24, 'internal/model/monitor.go AllTypes')
    assert.deepEqual([...goTypes].sort(), Object.keys(TYPES).sort(), 'this test table must cover every server type')
    const page = await openForm(e, t)
    const shown = await page.evaluate(() => [...document.querySelectorAll('[role=radio][data-type]')].map(r => ({ type: r.dataset.type, text: r.innerText.replace(/\s+/g, ' ').trim() })))
    assert.deepEqual(shown.map(s => s.type).sort(), [...goTypes].sort(), 'picker types === server AllTypes')
    for (const s of shown) assert.ok(s.text.startsWith(TYPES[s.type].label), `${s.type} card is labelled "${TYPES[s.type].label}" (got "${s.text}")`)
    const groups = await page.evaluate(() => [...document.querySelectorAll('[role=radiogroup] h3')].map(h => h.textContent.trim().toLowerCase()))
    assert.deepEqual(groups, ['web', 'network', 'databases', 'services', 'heartbeat', 'logic'])
    assert.deepEqual(await checkedTypes(page), ['http'], 'HTTP is the default type')
    assert.equal(await page.onElement({ css: '[role=radiogroup]' }, g => g.getAttribute('aria-label')), 'Monitor type')
  })

  describe('each type', () => {
    for (const [type, spec] of Object.entries(TYPES)) {
      it(`${type}: picker, target field, type settings and validation`, async t => {
        const page = await openForm(e, t)
        // Select with a real click.
        await page.click(radio(type))
        assert.deepEqual(await checkedTypes(page), [type], 'exactly the clicked type is selected')
        await page.waitForElement({ role: 'heading', name: `${spec.label} settings` })

        // Target label and placeholder.
        if (spec.target) {
          const [label, placeholder] = spec.target
          await page.waitForElement(field(label))
          assert.equal(await page.onElement(field(label), i => i.placeholder), placeholder, `${type} target placeholder`)
        } else {
          assert.equal(await page.exists({ css: 'input[placeholder]', label: /^(URL|Host|Hostname)/ }), false, 'push has no target field')
          assert.match(await page.bodyText(), /no target/i)
        }

        // Type-specific fields.
        for (const re of spec.fields ?? []) assert.ok(await page.exists(field(re)), `${type}: field matching ${re} is missing`)
        for (const re of spec.text ?? []) assert.match(await page.bodyText(), re, `${type}: text ${re}`)
        for (const b of spec.buttons ?? []) assert.ok(await page.exists({ role: 'button', name: b }), `${type}: button "${b}" is missing`)
        // Fields of other types must not leak in (e.g. HTTP's Keyword on a TCP monitor).
        if (type !== 'http') assert.equal(await page.exists(field(/^Keyword$/)), false, `${type} must not show HTTP's Keyword field`)

        // Submitting the empty form is refused client-side with a message per problem field.
        const before = postsToMonitors(page)
        await page.click({ role: 'button', name: 'Create monitor' })
        await page.waitForElement({ text: BAD_NAME })
        assert.equal(await page.exists({ css: '[aria-invalid=true]' }), true, 'invalid fields are marked aria-invalid')
        assert.match(await page.bodyText(), /Fix the highlighted fields first/)
        await page.click({ css: NAV + ' button', text: 'All settings' }) // the form jumps to the first error's section
        const bodyAfterEmpty = await page.bodyText()
        if (spec.target && !spec.bad) assert.match(bodyAfterEmpty, spec.empty, `${type}: empty target message`)
        if (spec.target && spec.bad) assert.match(bodyAfterEmpty, spec.msg, `${type}: empty target message`)
        if (spec.required) assert.match(bodyAfterEmpty, spec.required, `${type}: required type-specific field message`)
        assert.equal(postsToMonitors(page), before, 'nothing may be posted while the form is invalid')
        const focused = await page.activeElement()
        assert.ok(focused && focused.tag !== 'body', 'focus moves to the first invalid field')

        // Invalid (non-empty) target: the inline message appears when the field is left.
        if (spec.target && spec.bad) {
          await page.fill(field(spec.target[0]), spec.bad)
          await page.press('Tab')
          await page.waitForElement({ text: spec.msg })
          if (spec.bigPort) {
            await page.fill(field(spec.target[0]), spec.bigPort)
            await page.press('Tab')
            await page.waitForElement({ text: spec.bigMsg })
          }
          // The server agrees with the client about this value.
          const r = await page.api('POST', '/api/monitors', monitorBody({ name: unique('bad'), type, target: spec.bad, settings: type === 'udp' || type === 'banner' ? { expect: 'x' } : type.endsWith('_sql') ? { basicUser: 'u', probe: 'query', query: 'select 1' } : {} }))
          assert.equal(r.status, 400, `${type}: the server must also reject target ${JSON.stringify(spec.bad)} (got ${r.status} ${r.text.slice(0, 120)})`)
          assert.ok(r.json?.fields?.target, `${type}: the server reports the problem on "target"`)
        }
        if (spec.target) {
          // A good target clears the message.
          await page.fill(field(spec.target[0]), spec.good)
          await page.press('Tab')
          const badMsgs = [spec.msg, spec.empty, spec.bigMsg].filter(Boolean)
          const body = await page.bodyText()
          for (const re of badMsgs) assert.doesNotMatch(body, re, `${type}: the target message should be gone after entering ${spec.good}`)
        }
        page.assertNoProblems({ ignore: [/status of 400/] })
      })
    }
  })

  it('arrow keys move through the picker in order, select as they go, and wrap around', async t => {
    const page = await openForm(e, t)
    const order = await page.evaluate(() => [...document.querySelectorAll('[role=radio][data-type]')].map(r => r.dataset.type))
    assert.equal(order.length, 24)
    // Tab into the group: the checked radio is the only tab stop.
    await page.focus(radio('http'))
    assert.equal((await page.activeElement()).id === '' && (await page.activeElement()).tag, 'button')
    for (let i = 1; i < order.length; i++) {
      await page.press('ArrowRight')
      assert.deepEqual(await checkedTypes(page), [order[i]], `ArrowRight #${i} selects ${order[i]}`)
      assert.equal(await page.evaluate(() => document.activeElement?.dataset?.type), order[i], 'focus follows the selection')
    }
    await page.press('ArrowRight')
    assert.deepEqual(await checkedTypes(page), [order[0]], 'ArrowRight wraps from the last type to the first')
    await page.press('ArrowLeft')
    assert.deepEqual(await checkedTypes(page), [order[order.length - 1]], 'ArrowLeft wraps from the first type to the last')
    await page.press('ArrowDown')
    assert.deepEqual(await checkedTypes(page), [order[0]], 'ArrowDown behaves like ArrowRight')
    await page.press('ArrowUp')
    assert.deepEqual(await checkedTypes(page), [order[order.length - 1]], 'ArrowUp behaves like ArrowLeft')
    // Only the checked radio is in the tab order.
    assert.equal(await page.evaluate(() => document.querySelectorAll('[role=radio][data-type][tabindex="0"]').length), 1)
    page.assertNoProblems()
  })

  it('switching type keeps the name, clears type-specific errors and shows the new fields', async t => {
    const page = await openForm(e, t)
    const name = unique('switch')
    await page.fill(field('Name'), name)
    await page.click(radio('tcp'))
    await page.fill(field('Host and port'), 'nohostport')
    await page.press('Tab')
    await page.waitForElement({ text: /must be host:port/ })
    await page.click(radio('dns'))
    assert.equal(await page.value(field('Name')), name, 'the name survives a type change')
    assert.doesNotMatch(await page.bodyText(), /must be host:port/, 'the old type\'s target error is cleared')
    await page.waitForElement(field('Hostname'))
    // Summary card tracks the draft.
    assert.match(await page.text('aside:has(h2)') ?? '', /DNS/)
  })

  describe('create, test, edit, delete', () => {
    it('HTTP: test now (keyword pass and fail), create, detail page, list, edit, cancel-then-confirm delete', async t => {
      const page = await openForm(e, t)
      const name = unique('http-e2e')
      await page.fill(field('Name'), name)
      await page.fill(field('URL'), e.target.httpUrl)
      await page.fill(field('Check every'), '10') // first scheduled check runs one interval after creation
      await page.fill(field('Timeout'), '5000')

      // "Test now" runs one real check and saves nothing.
      await page.click({ role: 'button', name: 'Test now' })
      await page.waitForElement({ role: 'status', name: 'Test result' })
      assert.match(await page.text({ role: 'status', name: 'Test result' }), /Check passed/)
      assert.match(await page.text({ role: 'status', name: 'Test result' }), /HTTP 200/)
      assert.equal((await e.app.apiOk('GET', '/api/monitors')).monitors.some(m => m.name === name), false, 'Test now must not create the monitor')

      // A keyword that is not in the body fails the check with a reason.
      await page.fill(field(/^Keyword$/), 'definitely-not-in-the-body')
      await page.click({ role: 'button', name: 'Test now' })
      await page.waitFor(() => /Check failed/.test(document.querySelector('[aria-label="Test result"]')?.innerText ?? ''), { message: 'the keyword test to fail' })
      assert.match(await page.text({ role: 'status', name: 'Test result' }), /keyword|contain|body/i)
      await page.fill(field(/^Keyword$/), 'hello from the e2e target')
      await page.click({ role: 'button', name: 'Test now' })
      await page.waitFor(() => /Check passed/.test(document.querySelector('[aria-label="Test result"]')?.innerText ?? ''), { message: 'the keyword test to pass' })

      // Create.
      await page.click({ role: 'button', name: 'Create monitor' })
      await page.waitForPath(/^\/monitors\/\d+$/)
      const id = Number((await page.path()).split('/').pop())
      await page.waitForElement({ role: 'heading', name })
      const stored = await e.app.apiOk('GET', `/api/monitors/${id}`)
      assert.equal(stored.name, name)
      assert.equal(stored.type, 'http')
      assert.equal(stored.target, e.target.httpUrl)
      assert.equal(stored.settings.keyword, 'hello from the e2e target')
      assert.match(await page.bodyText(), new RegExp(e.target.httpUrl.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')))
      // The scheduler runs the first check on its own.
      await page.waitFor(() => /\b(Up|Operational)\b/.test(document.querySelector('section[aria-label="Monitor status"]')?.innerText ?? ''), { timeout: 30000, message: 'the new monitor to report Up' })

      // It shows up in the monitor list.
      await page.goto(e.url('/monitors'))
      await page.waitForElement({ role: 'link', name })
      assert.match(await page.text({ css: 'li', text: name }), /HTTP/)
      await page.fill({ role: 'textbox', name: 'Search monitors' }, name.slice(0, 8))
      assert.ok(await page.exists({ role: 'link', name }))
      await page.fill({ role: 'textbox', name: 'Search monitors' }, 'zz-no-match-zz')
      await page.waitForElement({ text: 'No matching services' })

      // Edit: the type is locked, the values load, a change is saved.
      await page.goto(e.url(`/monitors/${id}/edit`))
      await page.waitForElement({ role: 'heading', name: `Edit ${name}` })
      assert.equal(await page.value(field('Name')), name)
      assert.equal(await page.value(field('URL')), e.target.httpUrl)
      assert.deepEqual(await checkedTypes(page), ['http'])
      assert.equal(await page.evaluate(() => [...document.querySelectorAll('[role=radio][data-type]:not([aria-checked=true])')].every(r => r.disabled)), true, 'other types are disabled when editing')
      assert.match(await page.bodyText(), /type can't change after a monitor is created/i)
      const renamed = `${name}-renamed`
      await page.fill(field('Name'), renamed)
      await page.click({ role: 'button', name: 'Save changes' })
      await page.waitForPath(`/monitors/${id}`)
      await page.waitForElement({ role: 'heading', name: renamed })
      assert.equal((await e.app.apiOk('GET', `/api/monitors/${id}`)).name, renamed)

      // Delete asks in an in-app dialog (never window.confirm): Cancel keeps the monitor, the Delete button removes it.
      await page.click({ role: 'button', name: 'Delete' })
      await page.waitForElement({ role: 'dialog', name: `Delete ${renamed}?` })
      assert.equal(page.dialogs.length, 0, "the browser's own confirm() must not be used")
      await page.click({ css: '[role=dialog] button', text: 'Cancel' })
      await page.waitForGone({ role: 'dialog' })
      assert.equal((await e.app.api('GET', `/api/monitors/${id}`)).status, 200, 'cancelled delete keeps the monitor')
      await page.click({ role: 'button', name: 'Delete' })
      await page.waitForElement({ role: 'dialog' })
      await page.click({ css: '[role=dialog] button', text: 'Delete monitor' })
      await page.waitForPath('/monitors')
      assert.equal((await e.app.api('GET', `/api/monitors/${id}`)).status, 404, 'monitor is gone after delete')
      await page.waitForGone({ role: 'link', name: renamed })
      assert.equal(page.dialogs.length, 0, 'no native dialog appeared at any point')
      page.assertNoProblems({ ignore: [/status of 404/] })
    })

    it('TCP, UDP and banner monitors: a real "Test now" passes against the local target, a closed port fails', async t => {
      const cases = [
        { type: 'tcp', addr: e.target.tcpAddr, extra: async () => {} },
        { type: 'udp', addr: e.target.udpAddr, extra: async page => { await page.fill(field(/^Send/), 'ping'); await page.fill(field(/Expect in the reply/), 'pong') } },
        { type: 'banner', addr: e.target.tcpAddr, extra: async page => { await page.fill(field(/First line must contain/), '220') } },
      ]
      for (const c of cases) {
        const page = await openForm(e, t)
        await page.click(radio(c.type))
        await page.fill(field('Name'), unique(c.type))
        await page.fill(field('Host and port'), c.addr)
        await c.extra(page)
        await page.click({ role: 'button', name: 'Test now' })
        await page.waitFor(() => /Check (passed|failed)/.test(document.querySelector('[aria-label="Test result"]')?.innerText ?? ''), { message: `the ${c.type} test result` })
        const text = await page.text({ role: 'status', name: 'Test result' })
        assert.match(text, /Check passed/, `${c.type} against ${c.addr}: ${text}`)
        // Point it at a port nobody listens on: the check must fail with an explanation.
        await page.fill(field('Host and port'), '127.0.0.1:1')
        await page.click({ role: 'button', name: 'Test now' })
        await page.waitFor(() => /Check failed/.test(document.querySelector('[aria-label="Test result"]')?.innerText ?? ''), { message: `the failing ${c.type} test` })
        assert.ok((await page.text({ role: 'status', name: 'Test result' })).length > 30, 'a failure carries a reason')
        page.assertNoProblems()
      }
    })

    it('TCP: create through the form, goes Up, appears in the list, edit and delete through the UI', async t => {
      const page = await openForm(e, t)
      const name = unique('tcp-e2e')
      await page.click(radio('tcp'))
      await page.fill(field('Name'), name)
      await page.fill(field('Host and port'), e.target.tcpAddr)
      await page.fill(field('Check every'), '10')
      await page.fill(field('Timeout'), '5000')
      await page.click({ role: 'button', name: 'Create monitor' })
      await page.waitForPath(/^\/monitors\/\d+$/)
      const id = Number((await page.path()).split('/').pop())
      await page.waitFor(() => /\b(Up|Operational)\b/.test(document.querySelector('section[aria-label="Monitor status"]')?.innerText ?? ''), { timeout: 30000, message: 'the TCP monitor to report Up' })
      await page.goto(e.url('/monitors'))
      await page.waitForElement({ role: 'link', name })
      assert.match(await page.text({ css: 'li', text: name }), new RegExp(e.target.tcpAddr.replace('.', '\\.')))
      assert.equal((await e.app.apiOk('GET', `/api/monitors/${id}`)).type, 'tcp')
      // Bulk delete from the list with the multi-select and confirmation dialog.
      await page.click({ role: 'checkbox', name: `Select ${name}` })
      await page.waitForElement({ text: '1 selected' })
      await page.click({ role: 'button', name: 'Delete' })
      await page.waitForElement({ role: 'dialog' })
      await page.click({ css: '[role=dialog]', text: 'Cancel' }).catch(() => {})
      await page.click({ role: 'button', name: 'Cancel' }).catch(() => {})
      assert.equal((await e.app.api('GET', `/api/monitors/${id}`)).status, 200)
      await page.click({ role: 'button', name: 'Delete', nth: 0 })
      await page.waitForElement({ role: 'dialog' })
      await page.click({ css: '[role=dialog] button', text: 'Delete' })
      await page.waitForGone({ role: 'link', name })
      assert.equal((await e.app.api('GET', `/api/monitors/${id}`)).status, 404)
    })

    it('Push: the form needs no target, the URL appears after saving, and a heartbeat turns the monitor Up', async t => {
      const page = await openForm(e, t)
      const name = unique('push-e2e')
      await page.click(radio('push'))
      await page.fill(field('Name'), name)
      assert.match(await page.bodyText(), /push URL is generated when you save/i)
      await page.click({ role: 'button', name: 'Create monitor' })
      await page.waitForPath(/^\/monitors\/\d+$/)
      const id = Number((await page.path()).split('/').pop())
      const stored = await e.app.apiOk('GET', `/api/monitors/${id}`)
      assert.ok(stored.pushUrl, 'the push monitor has a URL')
      const pushPath = new URL(stored.pushUrl, e.app.baseUrl).pathname
      assert.match(pushPath, /^\/push\/[A-Za-z0-9_-]{8,}$/)
      // Edit page shows the URL.
      await page.goto(e.url(`/monitors/${id}/edit`))
      await page.waitForElement({ role: 'heading', name: `Edit ${name}` })
      await page.click({ css: NAV + ' button', text: 'Check configuration' })
      assert.ok(await page.evaluate(() => document.body.innerText.includes('/push/') || [...document.querySelectorAll('input')].some(i => i.value.includes('/push/'))), 'the edit page shows the push URL')
      // Call it like a cron job would.
      const hit = await fetch(`http://127.0.0.1:${e.app.port}${pushPath}?status=up&msg=ok`)
      assert.equal(hit.status, 200)
      await page.goto(e.url(`/monitors/${id}`))
      await page.waitFor(() => /\b(Up|Operational)\b/.test(document.querySelector('section[aria-label="Monitor status"]')?.innerText ?? ''), { timeout: 20000, message: 'the push monitor to report Up after a heartbeat' })
      // An unknown token is a plain 404 (it must not reveal anything).
      const bad = await fetch(`http://127.0.0.1:${e.app.port}/push/this-token-does-not-exist`)
      assert.equal(bad.status, 404)
    })

    it('timing fields are validated like the server does (interval, timeout, confirmations)', async t => {
      const page = await openForm(e, t)
      await page.fill(field('Name'), unique('timing'))
      await page.fill(field('URL'), 'http://127.0.0.1:9/')
      await page.fill(field('Check every'), '5')
      await page.click({ role: 'button', name: 'Create monitor' })
      await page.waitForElement({ text: /must be 10-86400/ })
      assert.equal(postsToMonitors(page), 0, 'an interval below the 10 s minimum must not be posted')
      await page.fill(field('Check every'), '30')
      await page.fill(field('Timeout'), '30000')
      await page.fill(field('Confirmations before down'), '11')
      await page.click({ role: 'button', name: 'Create monitor' })
      await page.waitForElement({ text: /must be 1000 to 29500/ })
      await page.waitForElement({ text: /must be 0-10/ })
      assert.equal(postsToMonitors(page), 0)
      // The server enforces the same bounds (never trust the client alone).
      const bad = [
        { intervalS: 5 }, { intervalS: 30, timeoutMs: 30000 }, { retries: 11 }, { degradedMs: -1 }, { sslWarnDays: 91 },
      ]
      for (const over of bad) {
        const r = await page.api('POST', '/api/monitors', monitorBody({ name: unique('bad'), target: 'http://127.0.0.1:9/', ...over }))
        assert.equal(r.status, 400, `server must reject ${JSON.stringify(over)} (got ${r.status})`)
      }
    })
  })
  it('a new monitor checks from this server only by default, and saves that choice', async t => {
    const page = await e.session(t, { goto: '/monitors/new' })
    await page.waitForElement({ role: 'heading', name: 'Add monitor' })
    await page.click({ css: 'nav[aria-label="Monitor configuration"] button', text: 'Check locations' })
    await page.waitForElement({ role: 'radiogroup', name: 'Monitoring region' })
    const checked = await page.onElements({ css: '[role=radiogroup][aria-label="Monitoring region"] [role=radio]' }, b => [b.textContent.trim().split(/\s/)[0], b.getAttribute('aria-checked')])
    assert.deepEqual(checked.filter(([, on]) => on === 'true').map(([label]) => label), ['This'], 'only "This server only" is selected')
    // The same default holds for monitors created from a URL list.
    const imported = await page.api('POST', '/api/import/monitors?dryRun=false', { urls: ['https://example.com'], type: 'http', intervalS: 60 })
    assert.equal(imported.status, 200)
    const row = (await e.app.apiOk('GET', '/api/monitors')).monitors.find(m => m.target === 'https://example.com')
    assert.ok(row, 'the imported monitor exists')
    const made = await e.app.apiOk('GET', `/api/monitors/${row.id}`)
    assert.equal(made.settings?.regionMode, 'local', 'imported monitors default to this server only')
  })
})
