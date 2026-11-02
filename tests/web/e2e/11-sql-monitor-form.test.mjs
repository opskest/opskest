// The SQL monitors (postgres_sql, mysql_sql): every probe shows its own fields, the form explains what is
// wrong with an empty username / a disallowed query / a bad threshold, and a write statement is refused
// before anything is saved (and again by the server).

import assert from 'node:assert/strict'
import { it } from 'node:test'
import { e2eSuite } from './harness.mjs'
import { monitorBody } from './util.mjs'
import { field, fieldState, openForm, pickType, postsToMonitors, submitCreate, submitInvalid, unique } from './forms.mjs'

const TYPES = [
  { type: 'postgres_sql', label: 'PostgreSQL queries', addr: '127.0.0.1:5432', grant: /pg_monitor/, databaseHint: /account's own database/ },
  { type: 'mysql_sql', label: 'MySQL / MariaDB queries', addr: '127.0.0.1:3306', grant: /PROCESS privilege/, databaseHint: /no default database/i },
]

// Every "What to check" option, with the fields it must show and the fields of the other probes it must not.
const PROBES = [
  { value: 'query', label: 'Custom query', fields: [/^Query$/, /^The first value returned$/] },
  { value: 'long_running', label: 'Slow queries', fields: [/^Running longer than$/, /^Allowed at once$/] },
  { value: 'deadlocks', label: 'Deadlocks', fields: [/^New deadlocks allowed per check$/] },
  { value: 'blocked', label: 'Lock waits', fields: [/^Waiting sessions allowed$/] },
  { value: 'connections', label: 'Connection use', fields: [/^Alert above$/] },
]
const ALL_PROBE_FIELDS = PROBES.flatMap(p => p.fields)

const BAD_QUERY = /must start with SELECT, WITH, SHOW, EXPLAIN, VALUES or TABLE/
const probeSelect = field(/^What to check$/)

/** Fills the always-required parts: a name, a reachable-looking address and a database login. */
async function fillBasics(page, addr, { user = 'readonly_e2e' } = {}) {
  await page.fill(field('Name'), unique('sql'))
  await page.fill(field('Host and port'), addr)
  if (user) await page.fill(field('Username'), user)
}

e2eSuite('sql monitors', e => {
    for (const T of TYPES) {
      it(`${T.type}: the login fields, TLS options and the five probes each show their own fields`, async t => {
        const page = await openForm(e, t)
        await pickType(page, T.type, T.label)
        // Login section.
        for (const re of [/^Username$/, /^Database/, /TLS — encrypt the connection/]) assert.ok(await page.exists(field(re)), `${re} is missing`)
        assert.ok(await page.exists({ css: 'input[type=password]' }), 'a password input')
        assert.match(await page.bodyText(), T.databaseHint)
        // The "Ignore TLS errors" box only exists once TLS is on.
        assert.equal(await page.exists({ role: 'switch', name: /Ignore TLS errors/ }), false)
        await page.click({ role: 'switch', name: /TLS — encrypt the connection/ })
        await page.waitForElement({ role: 'switch', name: /Ignore TLS errors/ })
        await page.click({ role: 'switch', name: /TLS — encrypt the connection/ })
        await page.waitForGone({ role: 'switch', name: /Ignore TLS errors/ })

        // The probe selector offers exactly the five probes, Custom query first (the default).
        const options = await page.onElement(probeSelect, el => Array.from(el.options).map(o => `${o.value}|${o.textContent.trim()}`))
        assert.deepEqual(options, PROBES.map(p => `${p.value}|${p.label}`))
        assert.equal(await page.value(probeSelect), 'query')

        for (const probe of PROBES) {
          await page.select(probeSelect, probe.label)
          await page.waitForElement(field(probe.fields[0]))
          for (const re of probe.fields) assert.ok(await page.exists(field(re)), `${probe.label}: field ${re} is missing`)
          for (const re of ALL_PROBE_FIELDS.filter(re => !probe.fields.includes(re))) {
            assert.equal(await page.exists(field(re)), false, `${probe.label}: field ${re} belongs to another probe`)
          }
          // The built-in probes need a server-side grant and say which one; a custom query does not.
          const body = await page.bodyText()
          if (probe.value === 'query') {
            assert.doesNotMatch(body, T.grant, 'the custom query needs no special grant')
            assert.match(body, /One read-only statement/)
          } else {
            assert.match(body, T.grant, `${probe.label}: names the grant the account needs`)
            assert.match(body, /never query text/)
            assert.equal(await page.exists(field(/^Query$/)), false)
          }
        }
        // The comparison "Value" box appears only once an operator is chosen.
        await page.select(probeSelect, 'Custom query')
        assert.equal(await page.exists(field(/^Value$/)), false, 'no Value box for "Just run it"')
        await page.select(field(/^The first value returned$/), 'is greater than')
        await page.waitForElement(field(/^Value$/))
        await page.select(field(/^The first value returned$/), 'Just run it (no comparison)')
        await page.waitForGone(field(/^Value$/))
        page.assertNoProblems()
      })

      it(`${T.type}: an empty form names every missing field; a write statement is refused with the error on Query`, async t => {
        const page = await openForm(e, t)
        await pickType(page, T.type, T.label)
        await submitInvalid(page)
        let user = await fieldState(page, /^Username$/)
        assert.equal(user.error, 'required', 'empty username')
        assert.equal(user.invalid, true, 'the username input is aria-invalid')
        assert.equal((await fieldState(page, /^Query$/)).error, 'required', 'empty query')
        assert.equal(postsToMonitors(page), 0, 'nothing is posted while the form is invalid')

        await fillBasics(page, T.addr, { user: null })
        const queryCases = [
          ['DROP TABLE users', BAD_QUERY],
          ['DELETE FROM orders WHERE id > 0', BAD_QUERY],
          ['UPDATE orders SET total = 0', BAD_QUERY],
          ['INSERT INTO orders VALUES (1)', BAD_QUERY],
          ['TRUNCATE orders', BAD_QUERY],
          ['SELECT 1; DROP TABLE users', /one statement only/],
        ]
        for (const [sql, want] of queryCases) {
          await page.fill(field(/^Query$/), sql)
          await submitInvalid(page)
          const q = await fieldState(page, /^Query$/)
          assert.match(q.error ?? '', want, `query ${JSON.stringify(sql)} must be refused on the Query field (got ${JSON.stringify(q.error)})`)
          assert.equal(q.invalid, true)
          assert.equal(postsToMonitors(page), 0, `${sql}: must not reach the server`)
        }
        // Statements that do read are accepted (the trailing semicolon is fine), with the username still missing.
        for (const sql of ['SELECT 1;', 'with x as (select 1) select * from x', 'SHOW max_connections', 'EXPLAIN SELECT 1', 'VALUES (1)', 'TABLE orders']) {
          await page.fill(field(/^Query$/), sql)
          await submitInvalid(page)
          assert.equal((await fieldState(page, /^Query$/)).error, null, `${sql} is a read-only statement`)
        }
        user = await fieldState(page, /^Username$/)
        assert.equal(user.error, 'required', 'still missing the username')
        assert.equal(postsToMonitors(page), 0)
        page.assertNoProblems()
      })

      it(`${T.type}: comparison values and thresholds are validated like the server does`, async t => {
        const page = await openForm(e, t)
        await pickType(page, T.type, T.label)
        await fillBasics(page, T.addr, { user: null }) // the empty username keeps the form invalid, so nothing is created
        await page.fill(field(/^Query$/), 'SELECT count(*) FROM orders')
        const submit = () => submitInvalid(page)

        // Custom query: an operator needs a value, and ordering operators need a number.
        await page.select(field(/^The first value returned$/), 'is greater than')
        await submit()
        assert.equal((await fieldState(page, /^Value$/)).error, 'required')
        await page.fill(field(/^Value$/), 'many')
        await submit()
        assert.equal((await fieldState(page, /^Value$/)).error, 'must be a number')
        await page.select(field(/^The first value returned$/), 'equals')
        await submit()
        assert.equal((await fieldState(page, /^Value$/)).error, null, 'equals compares text too, so "many" is fine')
        await page.select(field(/^The first value returned$/), 'is at most')
        await page.fill(field(/^Value$/), '12.5')
        await submit()
        assert.equal((await fieldState(page, /^Value$/)).error, null)

        // Slow queries: the limit is 1-86400 seconds, the allowance 0-1000000.
        await page.select(probeSelect, 'Slow queries')
        await page.fill(field(/^Running longer than$/), '0')
        await submit()
        assert.equal((await fieldState(page, /^Running longer than$/)).error, 'must be 1-86400')
        await page.fill(field(/^Running longer than$/), '86401')
        await submit()
        assert.equal((await fieldState(page, /^Running longer than$/)).error, 'must be 1-86400')
        await page.fill(field(/^Running longer than$/), '30')
        await page.fill(field(/^Allowed at once$/), '1000001')
        await submit()
        assert.equal((await fieldState(page, /^Allowed at once$/)).error, 'must be 0-1000000')

        // Deadlocks and lock waits share the same allowance rule.
        for (const [label, name] of [['Deadlocks', /^New deadlocks allowed per check$/], ['Lock waits', /^Waiting sessions allowed$/]]) {
          await page.select(probeSelect, label)
          await page.fill(field(name), '-1')
          await submit()
          assert.equal((await fieldState(page, name)).error, 'must be 0-1000000', `${label}: -1`)
        }

        // Connection use is a percentage: 0 and 101 are both wrong.
        await page.select(probeSelect, 'Connection use')
        for (const bad of ['0', '101']) {
          await page.fill(field(/^Alert above$/), bad)
          await submit()
          assert.equal((await fieldState(page, /^Alert above$/)).error, 'must be 1-100 (percent)', `connections ${bad}`)
        }
        assert.equal(postsToMonitors(page), 0, 'no invalid combination reached the server')

        // The server agrees about each of these.
        const base = { name: unique('bad-sql'), type: T.type, target: T.addr }
        const ok = { basicUser: 'u', probe: 'query', query: 'select 1' }
        const cases = [
          [{ ...ok, basicUser: '' }, 'settings.basicUser'],
          [{ ...ok, query: 'DROP TABLE x' }, 'settings.query'],
          [{ ...ok, query: 'select 1; select 2' }, 'settings.query'],
          [{ ...ok, queryOp: 'gt', queryExpected: 'abc' }, 'settings.queryExpected'],
          [{ ...ok, probe: 'connections', threshold: 150 }, 'settings.threshold'],
          [{ ...ok, probe: 'long_running', thresholdSecs: 86401 }, 'settings.thresholdSecs'],
          [{ ...ok, probe: 'deadlocks', threshold: -1 }, 'settings.threshold'],
        ]
        for (const [settings, key] of cases) {
          const r = await page.api('POST', '/api/monitors', monitorBody({ ...base, settings }))
          assert.equal(r.status, 400, `${JSON.stringify(settings)} should be rejected (got ${r.status})`)
          assert.ok(r.json?.fields?.[key], `the server reports ${key} (got ${JSON.stringify(r.json?.fields)})`)
        }
        page.assertNoProblems({ ignore: [/status of 400/] })
      })

      it(`${T.type}: a custom query monitor saves, keeps its password write-only and edits cleanly`, async t => {
        const page = await openForm(e, t)
        await pickType(page, T.type, T.label)
        const name = unique('sql-save')
        const password = `pw-${unique('sql')}-secret`
        await page.fill(field('Name'), name)
        await page.fill(field('Host and port'), T.addr)
        await page.fill(field('Username'), 'readonly_e2e')
        await page.fill({ css: 'input[type=password]' }, password)
        await page.fill(field(/^Database/), 'appdb')
        await page.fill(field(/^Query$/), 'SELECT count(*) FROM orders;')
        await page.select(field(/^The first value returned$/), 'is at least')
        await page.fill(field(/^Value$/), '0')
        const id = await submitCreate(page)

        const stored = await e.app.apiOk('GET', `/api/monitors/${id}`)
        assert.equal(stored.type, T.type)
        assert.equal(stored.settings.basicUser, 'readonly_e2e')
        assert.equal(stored.settings.database, 'appdb')
        assert.equal(stored.settings.probe, 'query')
        assert.equal(stored.settings.query, 'SELECT count(*) FROM orders;')
        assert.equal(stored.settings.queryOp, 'gte')
        assert.equal(stored.secrets.basicPass.isSet, true)
        assert.ok(!JSON.stringify(stored).includes(password), 'the API never returns the password')

        // Edit: the login loads, the password is a mask, and nothing in the page holds it.
        await page.goto(e.url(`/monitors/${id}/edit`))
        await page.waitForElement({ role: 'heading', name: `Edit ${name}` })
        await page.click({ css: 'nav[aria-label="Monitor configuration"] button', text: 'All settings' })
        assert.equal(await page.value(field('Username')), 'readonly_e2e')
        assert.equal(await page.value(field(/^Query$/)), 'SELECT count(*) FROM orders;')
        await page.waitForElement({ role: 'group', name: 'Password: a value is set' })
        assert.equal(await page.exists({ css: 'input[type=password]' }), false, 'no password input while the secret is kept')
        const html = await page.evaluate(() => document.documentElement.outerHTML + [...document.querySelectorAll('input,textarea')].map(i => i.value).join('|'))
        assert.ok(!html.includes(password), 'the password is nowhere in the page')

        // Replace with nothing is refused; Cancel brings the mask back; Clear is undoable.
        await page.click({ role: 'button', name: 'Replace' })
        await page.waitForElement({ css: 'input[type=password]' })
        await submitInvalid(page, 'Save changes')
        assert.match(await page.bodyText(), /enter the new password, or cancel Replace/)
        await page.click({ css: 'div:has(> input[type=password]) > button', text: 'Cancel' })
        await page.waitForElement({ role: 'group', name: 'Password: a value is set' })
        await page.click({ role: 'button', name: 'Clear' })
        await page.waitForElement({ role: 'group', name: 'Password: will be removed' })
        await page.click({ role: 'button', name: 'Undo' })
        await page.waitForElement({ role: 'group', name: 'Password: a value is set' })

        // Saving a refused statement in an existing monitor shows the error on the Query field.
        await page.fill(field(/^Query$/), 'DELETE FROM orders')
        await submitInvalid(page, 'Save changes')
        assert.match((await fieldState(page, /^Query$/)).error ?? '', BAD_QUERY)
        assert.equal((await e.app.apiOk('GET', `/api/monitors/${id}`)).settings.query, 'SELECT count(*) FROM orders;', 'the stored query is unchanged')
        // Fix it and save.
        await page.fill(field(/^Query$/), 'SELECT 2')
        await page.click({ role: 'button', name: 'Save changes' })
        await page.waitForPath(`/monitors/${id}`)
        const after = await e.app.apiOk('GET', `/api/monitors/${id}`)
        assert.equal(after.settings.query, 'SELECT 2')
        assert.equal(after.secrets.basicPass.isSet, true, 'the password survives an edit that does not touch it')
        page.assertNoProblems()
      })

      it(`${T.type}: each built-in probe saves with its own settings`, async t => {
        const page = await openForm(e, t)
        await pickType(page, T.type, T.label)
        const name = unique('sql-probe')
        await fillBasics(page, T.addr)
        await page.select(probeSelect, 'Connection use')
        await page.fill(field(/^Alert above$/), '90')
        await page.fill(field('Name'), name)
        const id = await submitCreate(page)
        const stored = await e.app.apiOk('GET', `/api/monitors/${id}`)
        assert.equal(stored.settings.probe, 'connections')
        assert.equal(stored.settings.threshold, 90)
        assert.ok(!stored.settings.query, 'a built-in probe stores no query')
        page.assertNoProblems()
      })
    }
})
