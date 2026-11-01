// Black-box proof that the MONITORING ITSELF is correct: state machine, retries, alerts, outages, uptime numbers,
// response-time statistics, scheduling and clean-up, observed only through the real HTTP API of a real server.
// See 20-kit.mjs for how the world is faked (controllable local targets, a local alert receiver).
//
// Scenarios run concurrently on one instance (each with its own monitors, target and alert channel), so the
// whole file takes about as long as its slowest scenario (the reminder test, roughly three minutes).
//
//   node --experimental-strip-types --test tests/e2e/20-monitoring-correctness.test.mjs
//   (set OPSKEST_E2E_BIN to a prebuilt binary while sources are being edited)

import assert from 'node:assert/strict'
import { after, before, describe, it } from 'node:test'
import { existsSync } from 'node:fs'
import { Fixture, Instance, OFFLINE_REASON, Receiver, fmt, guardOnline, holds, outageTable, seq, sleep, until, uniq, wasDownMinutes, downMatches } from './20-kit.mjs'
import { spawnSync } from 'node:child_process'

const SKIP = process.env.OPSKEST_E2E_BIN
  ? (existsSync(process.env.OPSKEST_E2E_BIN) ? false : 'OPSKEST_E2E_BIN does not exist')
  : (spawnSync('go', ['version'], { stdio: 'ignore' }).status === 0 ? false : 'Go toolchain not found and OPSKEST_E2E_BIN not set')

const GOOD = new Set(['up', 'degraded', 'pending'])
/** Uptime exactly as docs define it: (up + degraded + pending) / (that + down); maintenance and unknown beats count for nothing. */
function uptimeOf(beats) {
  let good = 0, bad = 0
  for (const b of beats) {
    if (b.maint) continue
    if (GOOD.has(b.status)) good++
    else if (b.status === 'down') bad++
  }
  return { good, bad, pct: good + bad ? (good / (good + bad)) * 100 : null }
}
const near = (a, b, tol, msg) => assert.ok(Math.abs(a - b) <= tol, `${msg}: got ${a}, expected ${b} +-${tol}`)

describe('monitoring correctness (black box, real server)', { skip: SKIP, concurrency: true }, () => {
  /** @type {Instance} */ let inst
  /** @type {Receiver} */ let rx
  let online = true

  before(async () => {
    online = await guardOnline()
    inst = await Instance.start({ env: { METRICS_TOKEN: 'metrics-test-token' } })
    rx = await Receiver.start()
  }, { timeout: 180000 })
  after(async () => {
    await rx?.stop().catch(() => {})
    await inst?.stop().catch(() => {})
  }, { timeout: 60000 })

  /** The internet-loss guard turns every failure into UNKNOWN when the host cannot reach 1.1.1.1/8.8.8.8:53. */
  const needsInternet = t => {
    if (online) return false
    t.skip(OFFLINE_REASON)
    return true
  }

  // ── helpers bound to the instance ────────────────────────────────────────
  const waitStatus = (id, status, timeout = 30000, diag) =>
    until(`monitor ${id} to be ${status}`, async () => (await inst.mon(id))?.status === status && (await inst.mon(id)), { timeout, diag })
  const waitBeats = (id, pred, what, timeout = 15000) =>
    until(what, async () => { const b = await inst.beats(id); return pred(b) ? b : null }, { timeout, every: 250 })
  const dump = (id, fx, key, name) => async () => {
    const m = await inst.mon(id)
    const b = await inst.beats(id, 60)
    return [
      `monitor: status=${m?.status} reason=${m?.reason} errClass=${m?.errClass} uptime24h=${m?.uptime24h} lastCheckAt=${m?.lastCheckAt}`,
      `beats: ${seq(b)}`,
      `last beats: ${b.slice(-6).map(x => `${x.ts}:${x.status}${x.maint ? '(maint)' : ''}:${x.msg}`).join(' | ')}`,
      `outages:\n${outageTable(await inst.outages(id))}`,
      `alerts received: ${rx.got(key, name).map(a => `${a.kind}@${a.t}`).join(', ') || '(none)'}`,
      fx ? `fixture hits: ${fx.hits.length} (${fx.hits.slice(-5).map(h => h.status).join(',')})` : '',
    ].join('\n')
  }
  const noUnknown = (beats, what) => assert.ok(!beats.some(b => b.status === 'unknown'), `${what}: found UNKNOWN beats (internet-loss guard fired?): ${seq(beats)}`)

  // ═══════════════════════════════════════════════════════════════════════
  it('1. up -> down after exactly retries+1 failed checks, one DOWN alert, one recovery alert with the right duration', { timeout: 150000 }, async t => {
    if (needsInternet(t)) return
    const fx = await Fixture.start(); t.after(() => fx.stop())
    const key = uniq('s1'), name = uniq('s1-svc')
    const ch = await inst.channel(rx, key)
    const mon = await inst.create({ name, target: fx.target, channelIds: [ch.id], retries: 2, intervalS: 10, retryIntervalS: 12 })
    const diag = dump(mon.id, fx, key, name)

    await waitStatus(mon.id, 'up', 30000, diag)
    await sleep(1500)
    assert.equal(rx.got(key).length, 0, 'no alert may be sent while the monitor is healthy')

    fx.fail(500)
    await waitStatus(mon.id, 'pending', 30000, diag)
    assert.equal(rx.got(key).length, 0, 'no alert may be sent while the monitor is only PENDING (retries not exhausted)')
    await waitStatus(mon.id, 'down', 60000, diag)
    const beats = await waitBeats(mon.id, b => b.filter(x => x.status === 'down').length >= 2, 'at least two DOWN beats stored', 25000)
    noUnknown(beats, 'scenario 1')

    let lastUp = -1
    beats.forEach((b, i) => { if (b.status === 'up') lastUp = i })
    const failing = beats.slice(lastUp + 1)
    assert.deepEqual(failing.slice(0, 3).map(b => b.status), ['pending', 'pending', 'down'],
      `retries=2 means two PENDING beats and DOWN on the 3rd failed check (=retries+1). beats: ${seq(beats)}`)
    assert.ok(failing.slice(3).every(b => b.status === 'down'), `after DOWN every failing check stays DOWN: ${seq(beats)}`)
    const f500 = fx.hits.filter(h => h.status === 500).length
    assert.ok(f500 >= 3 && f500 <= failing.length + 1, `failed target requests (${f500}) must match failing beats (${failing.length})`)
    // retry interval is used between failures while PENDING, the normal interval once DOWN
    near(failing[1].ts - failing[0].ts, 12000, 2200, 'gap failure1->failure2 (retryIntervalS=12)')
    near(failing[2].ts - failing[1].ts, 12000, 2200, 'gap failure2->failure3 (retryIntervalS=12)')
    near(failing[3].ts - failing[2].ts, 10000, 2200, 'gap after DOWN (intervalS=10)')
    assert.match(failing[0].msg, /HTTP 500 \(expected 200-299\)/)
    t.diagnostic(`beats ${seq(beats)}; gaps ${failing[1].ts - failing[0].ts}/${failing[2].ts - failing[1].ts} ms between retries (retryIntervalS 12), ${failing[3].ts - failing[2].ts} ms after DOWN (intervalS 10)`)
    assert.equal(failing[0].errClass, 'http_status')
    assert.equal(failing[0].code, 500)

    const tDown = failing[2].ts
    await until('exactly one DOWN message', async () => rx.got(key).length >= 1, { timeout: 15000, diag })
    await sleep(11000) // a further scheduled check must not produce a second alert
    const down = rx.got(key)
    assert.deepEqual(down.map(m => m.kind), ['down'], `exactly one alert while down; got ${fmt(down.map(m => m.title))}`)
    assert.ok(down[0].t >= tDown, `the alert (${down[0].t}) must not be sent before the DOWN transition (${tDown})`)
    assert.ok(down[0].title.includes(name), `title names the monitor: ${down[0].title}`)
    assert.ok(down[0].message.includes(fx.target), 'alert body carries the target')
    assert.match(down[0].message, /HTTP 500/, 'alert body carries the HTTP status')
    assert.equal(down[0].key_hdr, 'test-token')

    let outs = await inst.outages(mon.id)
    assert.equal(outs.length, 1, `one outage for one failure episode:\n${outageTable(outs)}`)
    assert.equal(outs[0].endedAt, null)
    assert.equal(outs[0].startedAt, tDown, 'outage starts at the DOWN beat (the retries+1-th failed check)')
    assert.match(outs[0].firstReason, /HTTP 500/)
    assert.equal(outs[0].errClass, 'http_status')
    assert.equal((await inst.mon(mon.id)).outageId, outs[0].id, 'monitor summary points at the open outage')

    // recovery
    const tUp = Date.now()
    fx.up()
    await waitStatus(mon.id, 'up', 30000, diag)
    await until('exactly one recovery message', async () => rx.got(key).length >= 2, { timeout: 15000, diag })
    await sleep(11000)
    const kinds = rx.kinds(key)
    assert.deepEqual(kinds, ['down', 'recovered'], `alerts over the whole episode: ${fmt(rx.got(key).map(m => m.title))}`)
    outs = await inst.outages(mon.id)
    assert.equal(outs.length, 1)
    assert.ok(outs[0].endedAt, 'outage is closed')
    const after = await inst.beats(mon.id)
    const firstUp = after.find(b => b.ts > failing[2].ts && b.status === 'up')
    assert.equal(outs[0].endedAt, firstUp.ts, 'outage ends at the first successful check')
    const dur = outs[0].endedAt - outs[0].startedAt
    assert.ok(dur >= tUp - tDown - 1500 && dur <= tUp - tDown + 13000, `outage duration ${dur} ms vs observed ${tUp - tDown} ms (+<=1 interval)`)
    const rec = rx.got(key)[1]
    assert.ok(downMatches(rec.title, dur), `recovery title "${rec.title}" must state the outage duration rounded to minutes (${dur} ms)`)
    assert.ok(rec.t >= firstUp.ts, 'recovery alert is sent after the recovering check')
    t.diagnostic(`outage ${dur} ms; DOWN alert ${down[0].t - tDown} ms after the DOWN beat; recovery alert ${rec.t - firstUp.ts} ms after the recovering beat; title "${rec.title}"`)

    const log = await inst.alertLog(mon.id)
    assert.deepEqual(log.map(e => `${e.event}:${e.ok}:${e.attempt}`), ['down:true:1', 'recovered:true:1'], `alert log (independent witness): ${fmt(log)}`)
  })

  // ═══════════════════════════════════════════════════════════════════════
  it('2. retries=0 goes DOWN on the very first failed check (no PENDING), one alert each way', { timeout: 120000 }, async t => {
    if (needsInternet(t)) return
    const fx = await Fixture.start(); t.after(() => fx.stop())
    const key = uniq('s2'), name = uniq('s2-svc')
    const ch = await inst.channel(rx, key)
    const mon = await inst.create({ name, target: fx.target, channelIds: [ch.id], retries: 0, intervalS: 3600, retryIntervalS: 3600, timeoutMs: 3000 })
    const diag = dump(mon.id, fx, key, name)

    assert.equal((await inst.checkNow(mon.id)).status, 'up')
    fx.fail(503)
    const m = await inst.checkNow(mon.id)
    assert.equal(m.status, 'down', 'the first failed check with retries=0 is DOWN immediately')
    const beats = await waitBeats(mon.id, b => b.length >= 2, 'two beats stored')
    assert.equal(seq(beats), 'up > down', 'never PENDING with retries=0')
    assert.equal(beats[1].code, 503)
    await until('DOWN alert', async () => rx.got(key).length === 1, { timeout: 12000, diag })
    assert.ok(rx.got(key)[0].t >= beats[1].ts)
    const outs = await inst.outages(mon.id)
    assert.equal(outs.length, 1)
    assert.equal(outs[0].startedAt, beats[1].ts)
    await inst.checkNow(mon.id); await inst.checkNow(mon.id)
    await sleep(4500)
    assert.equal(rx.got(key).length, 1, 'further failed checks while DOWN send nothing (reminders are off)')
    assert.equal((await inst.outages(mon.id)).length, 1, 'still one outage for the same episode')
    fx.up()
    assert.equal((await inst.checkNow(mon.id)).status, 'up')
    await until('recovery alert', async () => rx.got(key).length === 2, { timeout: 12000, diag })
    assert.deepEqual(rx.kinds(key), ['down', 'recovered'])
  })

  // ═══════════════════════════════════════════════════════════════════════
  it('3. flapping within the retry budget never goes DOWN and never alerts; a third consecutive failure does', { timeout: 120000 }, async t => {
    if (needsInternet(t)) return
    const fx = await Fixture.start(); t.after(() => fx.stop())
    const key = uniq('s3'), name = uniq('s3-svc')
    const ch = await inst.channel(rx, key)
    const mon = await inst.create({ name, target: fx.target, channelIds: [ch.id], retries: 2, intervalS: 3600, retryIntervalS: 3600, timeoutMs: 3000 })
    const diag = dump(mon.id, fx, key, name)
    // F = fail, . = ok. Runs of failures never exceed the retry budget of 2.
    const pattern = '..F.FF.F.FF.FF..'
    fx.script(n => (pattern[n] === 'F' ? { status: 500, body: 'boom' } : null))
    const seen = []
    for (let i = 0; i < pattern.length; i++) {
      const m = await inst.checkNow(mon.id)
      seen.push(m.status)
    }
    const expected = []
    let run = 0
    for (const c of pattern) { run = c === 'F' ? run + 1 : 0; expected.push(c === 'F' ? 'pending' : 'up') }
    assert.deepEqual(seen, expected, `status after each check for pattern ${pattern}`)
    await sleep(4500)
    const beats = await waitBeats(mon.id, b => b.length >= pattern.length, 'all beats stored')
    assert.ok(!beats.some(b => b.status === 'down'), `never DOWN: ${seq(beats)}`)
    assert.equal(beats.filter(b => b.status === 'pending').length, pattern.split('').filter(c => c === 'F').length)
    assert.equal(rx.got(key).length, 0, `flapping inside the retry budget must not alert: ${fmt(rx.got(key).map(m => m.title))}`)
    assert.equal((await inst.outages(mon.id)).length, 0, 'no outage was recorded')
    const u = uptimeOf(beats)
    near((await inst.mon(mon.id)).uptime24h, u.pct, 0.01, 'PENDING beats count as good in uptime (docs/uptime.go): uptime24h')

    // control: three consecutive failures DO go down (proves the test could have caught a problem)
    fx.script(null); fx.fail(500)
    assert.equal((await inst.checkNow(mon.id)).status, 'pending')
    assert.equal((await inst.checkNow(mon.id)).status, 'pending')
    assert.equal((await inst.checkNow(mon.id)).status, 'down', 'third consecutive failure = retries+1')
    await until('DOWN alert', async () => rx.kinds(key).join() === 'down', { timeout: 12000, diag })
  })

  // ═══════════════════════════════════════════════════════════════════════
  it('4. degraded: slow responses give DEGRADED (not down) and exactly one alert when enabled, none when not; cleared alert on speed-up', { timeout: 120000 }, async t => {
    if (needsInternet(t)) return
    const fx = await Fixture.start(); t.after(() => fx.stop())
    const key = uniq('s4')
    const nameA = uniq('s4-notify'), nameB = uniq('s4-quiet')
    const ch = await inst.channel(rx, key)
    const common = { target: fx.target, channelIds: [ch.id], retries: 2, intervalS: 3600, retryIntervalS: 3600, timeoutMs: 5000, degradedMs: 300 }
    const A = await inst.create({ ...common, name: nameA, notifyDegraded: true })
    const B = await inst.create({ ...common, name: nameB, notifyDegraded: false })
    fx.up()
    assert.equal((await inst.checkNow(A.id)).status, 'up')
    assert.equal((await inst.checkNow(B.id)).status, 'up')
    fx.slow(700)
    const a1 = await inst.checkNow(A.id)
    assert.equal(a1.status, 'degraded', 'a successful but slow check is DEGRADED, not PENDING/DOWN despite retries=2')
    assert.match(a1.reason, /slow: \d+ ms > 300 ms/)
    t.diagnostic(`degraded after one slow check: latency ${a1.latencyMs} ms, reason "${a1.reason}"`)
    assert.ok(a1.latencyMs >= 650 && a1.latencyMs <= 1500, `latency ${a1.latencyMs}`)
    assert.equal((await inst.checkNow(B.id)).status, 'degraded')
    assert.equal((await inst.checkNow(A.id)).status, 'degraded')
    await sleep(3500)
    assert.deepEqual(rx.kinds(key, nameA), ['degraded'], 'exactly one degraded alert for A although two slow checks ran')
    assert.deepEqual(rx.kinds(key, nameB), [], 'notifyDegraded=false: no alert at all for B')
    fx.up()
    assert.equal((await inst.checkNow(A.id)).status, 'up')
    assert.equal((await inst.checkNow(B.id)).status, 'up')
    await sleep(3500)
    assert.deepEqual(rx.kinds(key, nameA), ['degraded', 'degraded_cleared'], `A alerts: ${fmt(rx.got(key, nameA).map(m => m.title))}`)
    assert.deepEqual(rx.kinds(key, nameB), [], 'B still silent')
    const beatsA = await waitBeats(A.id, b => b.length >= 4, 'A beats stored')
    assert.equal(seq(beatsA), 'up > degraded x2 > up')
    assert.equal((await inst.outages(A.id)).length, 0, 'degraded is not an outage')
    near((await inst.mon(A.id)).uptime24h, 100, 0.001, 'DEGRADED counts as good: uptime24h')

    // degraded -> down -> back (slow): recovery alert AND degraded alert (it is up again, but still slow)
    fx.fail(500)
    assert.equal((await inst.checkNow(A.id)).status, 'pending')
    fx.up(); fx.slow(700)
    assert.equal((await inst.checkNow(A.id)).status, 'degraded', 'PENDING + slow success -> DEGRADED')
    assert.deepEqual((await inst.outages(A.id)).length, 0)
  })

  // ═══════════════════════════════════════════════════════════════════════
  it('5. upside-down monitors invert: reachable = DOWN with an explanatory reason, unreachable = UP', { timeout: 120000 }, async t => {
    if (needsInternet(t)) return
    const fx = await Fixture.start(); t.after(() => fx.stop())
    const key = uniq('s5'), name = uniq('s5-svc')
    const ch = await inst.channel(rx, key)
    const mon = await inst.create({ name, target: fx.target, channelIds: [ch.id], retries: 0, upsideDown: true, intervalS: 3600, retryIntervalS: 3600, timeoutMs: 3000 })
    const diag = dump(mon.id, fx, key, name)

    fx.up()
    let m = await inst.checkNow(mon.id)
    assert.equal(m.status, 'down', 'target answers 200, upside-down expects it to be unreachable')
    assert.match(m.reason, /upside-down mode expects it to be down/)
    assert.equal(m.errClass, 'assertion')
    await until('DOWN alert', async () => rx.kinds(key).join() === 'down', { timeout: 12000, diag })

    fx.fail(500)
    m = await inst.checkNow(mon.id)
    assert.equal(m.status, 'up', 'an HTTP 500 is a failed check; upside-down turns it into UP')
    assert.equal(m.reason, '')
    await until('recovery alert', async () => rx.kinds(key).join() === 'down,recovered', { timeout: 12000, diag })

    await fx.close()
    m = await inst.checkNow(mon.id)
    assert.equal(m.status, 'up', 'connection refused is UP for an upside-down monitor')
    await fx.open()
    fx.up()
    m = await inst.checkNow(mon.id)
    assert.equal(m.status, 'down')
    await until('second DOWN alert', async () => rx.kinds(key).join() === 'down,recovered,down', { timeout: 12000, diag })
    const outs = await inst.outages(mon.id)
    assert.equal(outs.length, 2, `two episodes, two outages:\n${outageTable(outs)}`)
    assert.ok(outs[0].endedAt && !outs[1].endedAt)
    assert.equal(outs[0].errClass, 'assertion')
  })

  // ═══════════════════════════════════════════════════════════════════════
  it('6. HTTP assertions (keyword, accepted codes, JSON path, header/body/status/latency assertions) each flip the status correctly', { timeout: 150000 }, async t => {
    if (needsInternet(t)) return
    const fx = await Fixture.start(); t.after(() => fx.stop())
    let dyn = 'nothing special here'
    const json = '{"status":"ok","count":7,"nested":{"v":"abc"},"items":["a","b"]}'
    fx.script((n, req) => {
      const u = req.url.split('?')[0]
      if (u === '/text') return { body: 'Welcome to Opskest, status OK', headers: { 'Content-Type': 'text/plain', 'X-Env': 'prod' } }
      if (u === '/json') return { body: json, headers: { 'Content-Type': 'application/json', 'X-Env': 'prod' } }
      if (u === '/404') return { status: 404, body: 'not here' }
      if (u === '/slow') return { delay: 450, body: 'slow but fine' }
      if (u === '/dyn') return { body: dyn }
      return null
    })
    const A = (source, op, value, property) => ({ source, op, value, ...(property ? { property } : {}) })
    // [label, path, settings, expected status, expected errClass, reason regexp]
    const cases = [
      ['keyword present', '/text', { keyword: 'Welcome' }, 'up'],
      ['keyword absent', '/text', { keyword: 'goodbye' }, 'down', 'assertion', /keyword "goodbye" not found/],
      ['keyword is case-insensitive by default', '/text', { keyword: 'WELCOME' }, 'up'],
      ['keyword case-sensitive mismatch', '/text', { keyword: 'WELCOME', caseSensitive: true }, 'down', 'assertion', /not found/],
      ['keyword not_contains but present', '/text', { keyword: 'Welcome', keywordMode: 'not_contains' }, 'down', 'assertion', /keyword "Welcome" found/],
      ['keyword not_contains and absent', '/text', { keyword: 'goodbye', keywordMode: 'not_contains' }, 'up'],
      ['keyword regex match', '/text', { keyword: 'Welcome to \\w+', keywordMode: 'regex' }, 'up'],
      ['keyword regex no match', '/text', { keyword: '^nomatch$', keywordMode: 'regex' }, 'down', 'assertion', /did not match/],
      ['404 with default codes', '/404', {}, 'down', 'http_status', /HTTP 404 \(expected 200-299\)/],
      ['404 accepted explicitly', '/404', { acceptedCodes: '404' }, 'up'],
      ['404 accepted in a list', '/404', { acceptedCodes: '200-299,404' }, 'up'],
      ['200 but only 201 accepted', '/text', { acceptedCodes: '201' }, 'down', 'http_status', /HTTP 200 \(expected 201\)/],
      ['json == match', '/json', { jsonPath: 'status', jsonOp: '==', jsonExpected: 'ok' }, 'up'],
      ['json == mismatch', '/json', { jsonPath: 'status', jsonOp: '==', jsonExpected: 'bad' }, 'down', 'assertion', /status is "ok", expected == "bad"/],
      ['json > true', '/json', { jsonPath: 'count', jsonOp: '>', jsonExpected: '5' }, 'up'],
      ['json > false', '/json', { jsonPath: 'count', jsonOp: '>', jsonExpected: '10' }, 'down', 'assertion', /count is 7, expected > 10/],
      ['json < true', '/json', { jsonPath: 'count', jsonOp: '<', jsonExpected: '8' }, 'up'],
      ['json nested contains', '/json', { jsonPath: 'nested.v', jsonOp: 'contains', jsonExpected: 'b' }, 'up'],
      ['json array contains', '/json', { jsonPath: 'items', jsonOp: 'contains', jsonExpected: 'b' }, 'up'],
      ['json missing path', '/json', { jsonPath: 'missing', jsonOp: 'exists' }, 'down', 'assertion', /missing not found/],
      ['json on a non-JSON body', '/text', { jsonPath: 'status', jsonOp: 'exists' }, 'down', 'assertion', /not valid JSON/],
      ['assertion: header eq (match)', '/text', { assertions: [A('header', 'eq', 'prod', 'X-Env')] }, 'up'],
      ['assertion: header eq (mismatch)', '/text', { assertions: [A('header', 'eq', 'staging', 'X-Env')] }, 'down', 'assertion', /header X-Env is "prod", expected == "staging"/],
      ['assertion: header contains', '/text', { assertions: [A('header', 'contains', 'pro', 'X-Env')] }, 'up'],
      ['assertion: header missing', '/text', { assertions: [A('header', 'exists', '', 'X-Missing')] }, 'down', 'assertion', /header X-Missing is missing/],
      ['assertion: status eq (match)', '/text', { assertions: [A('status', 'eq', '200')] }, 'up'],
      ['assertion: status eq (mismatch)', '/text', { assertions: [A('status', 'eq', '201')] }, 'down', 'assertion', /status code is 200, expected == 201/],
      ['assertion: body contains', '/text', { assertions: [A('body', 'contains', 'Opskest')] }, 'up'],
      ['assertion: body not_contains', '/text', { assertions: [A('body', 'not_contains', 'Opskest')] }, 'down', 'assertion', /body is/],
      ['assertion: json gte (match)', '/json', { assertions: [A('json', 'gte', '7', 'count')] }, 'up'],
      ['assertion: json lt (mismatch)', '/json', { assertions: [A('json', 'lt', '7', 'count')] }, 'down', 'assertion', /count is 7, expected < 7/],
      ['assertion: latency lt (generous)', '/text', { assertions: [A('latency', 'lt', '2000')] }, 'up'],
      ['assertion: latency lt (too strict for a 450 ms target)', '/slow', { assertions: [A('latency', 'lt', '100')] }, 'down', 'assertion', /response time \(ms\) is \d+, expected < 100/],
      ['assertion: latency gt (450 ms target)', '/slow', { assertions: [A('latency', 'gt', '100')] }, 'up'],
      ['codes are evaluated before keyword', '/404', { keyword: 'not here' }, 'down', 'http_status', /HTTP 404/],
      ['every assertion must pass (second fails)', '/text', { assertions: [A('status', 'eq', '200'), A('body', 'contains', 'nope')] }, 'down', 'assertion', /body is/],
    ]
    const run = async ([label, p, settings, wantStatus, wantClass, wantReason]) => {
      const m = await inst.create({ name: uniq('s6'), target: fx.url(p), retries: 0, intervalS: 3600, retryIntervalS: 3600, timeoutMs: 5000, settings })
      const r = await inst.checkNow(m.id)
      const problems = []
      if (r.status !== wantStatus) problems.push(`status ${r.status}, expected ${wantStatus} (reason "${r.reason}")`)
      if (wantClass && r.errClass !== wantClass) problems.push(`errClass ${r.errClass}, expected ${wantClass}`)
      if (wantStatus === 'up' && r.errClass) problems.push(`errClass should be empty when up, got ${r.errClass}`)
      if (wantReason && !wantReason.test(r.reason)) problems.push(`reason "${r.reason}" does not match ${wantReason}`)
      return problems.length ? `${label}: ${problems.join('; ')}` : null
    }
    const results = []
    for (let i = 0; i < cases.length; i += 8) results.push(...await Promise.all(cases.slice(i, i + 8).map(run)))
    const bad = results.filter(Boolean)
    assert.deepEqual(bad, [], `${bad.length} of ${cases.length} assertion cases misbehaved`)

    // the same monitor flips when the page changes (keyword appears, then disappears again)
    const m = await inst.create({ name: uniq('s6-flip'), target: fx.url('/dyn'), retries: 0, intervalS: 3600, retryIntervalS: 3600, timeoutMs: 5000, settings: { keyword: 'all systems go' } })
    assert.equal((await inst.checkNow(m.id)).status, 'down')
    dyn = 'banner: ALL SYSTEMS GO'
    assert.equal((await inst.checkNow(m.id)).status, 'up', 'keyword appeared')
    dyn = 'maintenance page'
    const r = await inst.checkNow(m.id)
    assert.equal(r.status, 'down', 'keyword disappeared')
    const beats = await waitBeats(m.id, b => b.length >= 3, 'flip beats stored')
    assert.equal(seq(beats), 'down > up > down')
    assert.equal((await inst.outages(m.id)).length, 2, 'two failure episodes -> two outages')
  })

  // ═══════════════════════════════════════════════════════════════════════
  it('7. paused monitors neither check nor alert, and resume cleanly', { timeout: 150000 }, async t => {
    if (needsInternet(t)) return
    const fx = await Fixture.start(); t.after(() => fx.stop())
    const key = uniq('s7'), name = uniq('s7-svc')
    const ch = await inst.channel(rx, key)
    const mon = await inst.create({ name, target: fx.target, channelIds: [ch.id], retries: 1, intervalS: 10, retryIntervalS: 10 })
    const diag = dump(mon.id, fx, key, name)
    await waitStatus(mon.id, 'up', 30000, diag)
    await sleep(1500)

    const tPause = Date.now()
    await inst.ok('POST', `/api/monitors/${mon.id}/pause`)
    await waitStatus(mon.id, 'paused', 5000, diag)
    const beatsAtPause = (await waitBeats(mon.id, b => b.length >= 1, 'beats'))
    await sleep(1500)
    const nBeats = (await inst.beats(mon.id)).length
    const uptimeAtPause = (await inst.mon(mon.id)).uptime24h
    fx.fail(500)
    const checkNow = await inst.api('POST', `/api/monitors/${mon.id}/check`)
    assert.equal(checkNow.status, 409, 'a paused monitor refuses an on-demand check')
    await holds('a paused monitor must not be checked, change status or alert', 26000, async () => {
      const late = fx.hits.filter(h => h.t > tPause + 800)
      if (late.length) return `${late.length} request(s) reached the target while paused`
      const m = await inst.mon(mon.id)
      if (m.status !== 'paused') return `status became ${m.status}`
      if (rx.got(key).length) return 'an alert was sent'
      return null
    })
    assert.equal((await inst.beats(mon.id)).length, nBeats, 'no beats are written while paused')
    assert.equal((await inst.mon(mon.id)).uptime24h, uptimeAtPause, 'paused time does not change uptime')
    assert.ok(beatsAtPause.length >= 1)

    await inst.ok('POST', `/api/monitors/${mon.id}/resume`)
    inst._mlAt = 0
    const resumed = await inst.mon(mon.id)
    assert.ok(['unknown', 'pending'].includes(resumed.status), `right after resume the state is fresh (UNKNOWN until the first check), got ${resumed.status}`)
    await waitStatus(mon.id, 'pending', 30000, diag)
    await waitStatus(mon.id, 'down', 30000, diag)
    await until('single DOWN alert after resume', async () => rx.kinds(key).join() === 'down', { timeout: 12000, diag })
    await sleep(3000)
    assert.deepEqual(rx.kinds(key), ['down'])
    const events = (await inst.ok('GET', `/api/events?monitorId=${mon.id}&limit=50`)).events
    const trail = events.slice().reverse().map(e => `${e.from}>${e.to}`)
    assert.ok(trail.includes('up>paused'), `status events record the pause: ${trail}`)
    assert.ok(trail.some(s => s.startsWith('paused>')), `status events record the resume: ${trail}`)
    const outs = await inst.outages(mon.id)
    assert.equal(outs.length, 1)
    assert.ok(outs[0].startedAt > tPause + 25000, 'the outage starts after the resume, not during the pause')

    // a monitor created paused is never checked until resumed
    const fx2 = await Fixture.start(); t.after(() => fx2.stop())
    const born = await inst.create({ name: uniq('s7-born-paused'), target: fx2.target, paused: true, retries: 0, intervalS: 10, retryIntervalS: 10 })
    assert.equal((await inst.fresh(born.id)).status, 'paused')
    await sleep(12500)
    assert.equal(fx2.hits.length, 0, 'created paused: no check within more than one interval')
    await inst.ok('POST', `/api/monitors/${born.id}/resume`)
    await until('first check after resuming a monitor that was created paused', async () => fx2.hits.length >= 1, { timeout: 15000 })
    await waitStatus(born.id, 'up', 10000)
  })

  // ═══════════════════════════════════════════════════════════════════════
  it('8. maintenance window: no alert and no outage while active, uptime excludes it, recovery still alerts, DOWN fires when it ends', { timeout: 170000 }, async t => {
    if (needsInternet(t)) return
    const key = uniq('s8')
    const ch = await inst.channel(rx, key)
    const mk = async label => {
      const fx = await Fixture.start(); t.after(() => fx.stop())
      const name = uniq(`s8-${label}`)
      const mon = await inst.create({ name, target: fx.target, channelIds: [ch.id], retries: 0, intervalS: 10, retryIntervalS: 10 })
      return { fx, name, mon, id: mon.id }
    }
    const [A, B, C] = [await mk('stays-down'), await mk('blip'), await mk('already-down')]
    const diag = async () => `${await dump(A.id, A.fx, key, A.name)()}\n--B--\n${await dump(B.id, B.fx, key, B.name)()}\n--C--\n${await dump(C.id, C.fx, key, C.name)()}`
    await Promise.all([A, B, C].map(x => waitStatus(x.id, 'up', 30000, diag)))
    await sleep(1200)
    // C is already down (and alerted) when the window opens
    C.fx.fail(500)
    await waitStatus(C.id, 'down', 30000, diag)
    await until('C DOWN alert', async () => rx.kinds(key, C.name).join() === 'down', { timeout: 12000, diag })
    const upBeatsBeforeA = (await inst.beats(A.id)).length

    const now = Date.now()
    const win = await inst.ok('POST', '/api/maintenance', {
      title: uniq('s8-window'), description: '', kind: 'once', startsAt: now - 2000, endsAt: now + 48000, tz: 'UTC', active: true,
      weekdays: 0, startMinute: 0, durationMin: 0, monitorIds: [A.id, B.id, C.id], groupIds: [],
    })
    await Promise.all([A, B, C].map(x => waitStatus(x.id, 'maintenance', 8000, diag)))
    const list = (await inst.ok('GET', '/api/maintenance')).maintenance.find(w => w.id === win.id)
    assert.equal(list.state, 'active')
    assert.equal(list.currentEnd, win.endsAt)

    A.fx.fail(500)
    B.fx.fail(500)
    C.fx.up() // the already-alerted outage recovers inside the window
    await until('A has two failing beats inside the window', async () => (await inst.beats(A.id)).filter(b => b.status === 'down' && b.maint).length >= 2, { timeout: 30000, diag })
    await until('B has a failing beat inside the window', async () => (await inst.beats(B.id)).filter(b => b.status === 'down' && b.maint).length >= 1, { timeout: 15000, diag })
    B.fx.up()
    await until('C recovered during the window', async () => { const o = await inst.outages(C.id); return o.length === 1 && o[0].endedAt }, { timeout: 20000, diag })
    await until('C recovery alert', async () => rx.kinds(key, C.name).join() === 'down,recovered', { timeout: 15000, diag })

    // inside the window
    const mA = await inst.mon(A.id)
    assert.equal(mA.status, 'maintenance')
    assert.equal(mA.maint, true)
    assert.equal(rx.kinds(key, A.name).length, 0, 'no alert for A while in maintenance')
    assert.equal(rx.kinds(key, B.name).length, 0, 'no alert for B')
    assert.equal((await inst.outages(A.id)).length, 0, 'documented behaviour: no outage is opened while a monitor is in maintenance (api/reports.go: "outages never open during maintenance")')
    assert.equal((await inst.outages(B.id)).length, 0)
    near(mA.uptime24h, 100, 0.001, 'maintenance-time failures do not reduce uptime (A)')
    const beatsA = await inst.beats(A.id)
    assert.ok(beatsA.filter(b => b.maint).every(b => b.status === 'down' || b.status === 'up'), 'maintenance beats keep the underlying status')
    assert.ok(beatsA.length > upBeatsBeforeA)
    // rollup: maintenance beats are counted separately, never as down
    const rollB = (await inst.ok('GET', `/api/monitors/${B.id}/series?range=90d`)).series
    assert.equal(rollB.reduce((s, r) => s + r.Down, 0), 0, 'B never counted a DOWN in uptime (its failures were all in maintenance)')
    assert.ok(rollB.reduce((s, r) => s + r.Maint, 0) >= 1, 'B has maintenance beats in the rollup')
    near((await inst.mon(B.id)).uptime24h, 100, 0.001, 'uptime B')

    // after the window ends
    const tEnd = win.endsAt
    await until('window to end and A to show DOWN', async () => (await inst.mon(A.id)).status === 'down', { timeout: 60000, diag })
    await waitStatus(B.id, 'up', 20000, diag)
    await until('A DOWN alert after the window', async () => rx.kinds(key, A.name).join() === 'down', { timeout: 20000, diag })
    const downA = rx.got(key, A.name)[0]
    assert.ok(downA.t >= tEnd, `the DOWN alert (${downA.t}) must come after the window end (${tEnd})`)
    // the outage row commits with the writer's next batch (<= 1 s), which can trail the alert
    await until('A outage row committed', async () => (await inst.outages(A.id)).length > 0, { timeout: 5000, diag })
    const outsA = await inst.outages(A.id)
    assert.equal(outsA.length, 1, 'one outage, opened when maintenance ended while still down')
    assert.ok(outsA[0].startedAt >= tEnd && outsA[0].startedAt <= tEnd + 17000, `outage starts at the maintenance tick after the window end: ${outsA[0].startedAt - tEnd} ms after the end (tick is every 15 s)`)
    assert.equal(outsA[0].errClass, 'http_status')
    await sleep(2000)
    t.diagnostic(`maintenance: A went DOWN ${outsA[0].startedAt - tEnd} ms after the window end (tick every 15 s); its DOWN alert came ${downA.t - tEnd} ms after the end`)
    assert.equal(rx.kinds(key, B.name).length, 0, 'B (blip fully inside the window) never alerted')
    assert.equal((await inst.outages(B.id)).length, 0)
    const aBeats = await inst.beats(A.id)
    const u = uptimeOf(aBeats)
    near((await inst.mon(A.id)).uptime24h, u.pct, 0.01, `A uptime counts only beats outside the window (${u.good} good, ${u.bad} bad)`)
    A.fx.up()
    await waitStatus(A.id, 'up', 30000, diag)
    await until('A recovery alert', async () => rx.kinds(key, A.name).join() === 'down,recovered', { timeout: 15000, diag })
  })

  // ═══════════════════════════════════════════════════════════════════════
  it('9. dependencies: a child of a down parent has its DOWN alert held; released when the parent recovers; a held outage that closes first never alerts', { timeout: 170000 }, async t => {
    if (needsInternet(t)) return
    const key = uniq('s9')
    const ch = await inst.channel(rx, key)
    const mk = async (label, dependsOn) => {
      const fx = await Fixture.start(); t.after(() => fx.stop())
      const name = uniq(`s9-${label}`)
      const mon = await inst.create({ name, target: fx.target, channelIds: [ch.id], retries: 0, intervalS: 10, retryIntervalS: 10, settings: dependsOn ? { dependsOn } : {} })
      return { fx, name, id: mon.id }
    }
    const P = await mk('parent')
    const C = await mk('child-stays-down', [P.id])
    const C2 = await mk('child-recovers-first', [P.id])
    const diag = async () => `alerts: ${rx.got(key).map(m => `${m.kind}:${m.title}`).join(' | ')}\n` +
      (await Promise.all([P, C, C2].map(async x => `${x.name}: ${(await inst.mon(x.id))?.status} supp=${(await inst.mon(x.id))?.suppressedBy}\n${outageTable(await inst.outages(x.id))}`))).join('\n')
    await Promise.all([P, C, C2].map(x => waitStatus(x.id, 'up', 30000, diag)))

    P.fx.fail(500)
    await waitStatus(P.id, 'down', 30000, diag)
    await until('P DOWN alert', async () => rx.kinds(key, P.name).join() === 'down', { timeout: 12000, diag })
    C.fx.fail(500); C2.fx.fail(500)
    await waitStatus(C.id, 'down', 30000, diag)
    await waitStatus(C2.id, 'down', 30000, diag)
    assert.equal((await inst.mon(C.id)).suppressedBy, P.id, 'the child outage records which parent holds it')
    const oC = await until('the held outage to be stored', async () => { const o = await inst.outages(C.id); return o.length ? o : null }, { timeout: 8000, diag })
    assert.equal(oC.length, 1)
    assert.equal(oC[0].suppressedBy, P.id, 'outage is recorded with suppressedBy')
    assert.equal(oC[0].endedAt, null)
    await sleep(5000)
    assert.equal(rx.kinds(key, C.name).length + rx.kinds(key, C2.name).length, 0, `alerts of children are held while the parent is down: ${fmt(rx.all.map(a => a.title))}`)

    C2.fx.up()
    await waitStatus(C2.id, 'up', 20000, diag)
    const o2 = await until('the closed held outage to be stored', async () => { const o = await inst.outages(C2.id); return o.length && o[0].endedAt ? o : null }, { timeout: 8000, diag })
    assert.equal(o2.length, 1)
    assert.ok(o2[0].endedAt, 'held outage closes when the child recovers')
    assert.equal(o2[0].suppressedBy, P.id, 'and keeps the record of why it was silent')

    const tParentUp = Date.now()
    P.fx.up()
    await waitStatus(P.id, 'up', 20000, diag)
    await until('P recovery alert', async () => rx.kinds(key, P.name).join() === 'down,recovered', { timeout: 15000, diag })
    await until('C DOWN alert released once the parent is back (<= one 15 s maintenance tick + 3 s hold)', async () => rx.kinds(key, C.name).join() === 'down', { timeout: 30000, diag })
    const held = rx.got(key, C.name)[0]
    assert.ok(held.t >= tParentUp, 'released only after the parent recovered')
    assert.match(held.message, /still down after its parent recovered/)
    t.diagnostic(`dependencies: the held DOWN alert of the child went out ${held.t - tParentUp} ms after the parent's recovery was triggered`)
    // (the alert can leave before the writer's next 1 s commit of the cleared hold, so wait for the stored row)
    const oC2 = await until('the stored outage to lose its hold', async () => { const o = await inst.outages(C.id); return o.length === 1 && o[0].suppressedBy === null ? o : null }, { timeout: 8000, diag })
    assert.equal(oC2.length, 1, 'still one outage')
    C.fx.up()
    await waitStatus(C.id, 'up', 20000, diag)
    await until('C recovery alert', async () => rx.kinds(key, C.name).join() === 'down,recovered', { timeout: 15000, diag })
    await sleep(3000)
    assert.deepEqual(rx.kinds(key, C2.name), [], 'a held outage that closed before the parent recovered never alerts at all (neither down nor recovered)')
    assert.deepEqual(rx.kinds(key, P.name), ['down', 'recovered'])
  })

  // ═══════════════════════════════════════════════════════════════════════
  it('10. reminders repeat every reminderMin while down, stop on acknowledge and on recovery', { timeout: 300000 }, async t => {
    if (needsInternet(t)) return
    const key = uniq('s10')
    const ch = await inst.channel(rx, key)
    const mk = async label => {
      const fx = await Fixture.start(); t.after(() => fx.stop())
      const name = uniq(`s10-${label}`)
      const mon = await inst.create({ name, target: fx.target, channelIds: [ch.id], retries: 0, intervalS: 10, retryIntervalS: 10, reminderMin: 1 })
      return { fx, name, id: mon.id }
    }
    const R = await mk('reminded'), K = await mk('acked'), Q = await mk('quick-recovery')
    const diag = async () => `alerts: ${rx.all.filter(a => a.key === key).map(m => `${m.kind}@${((m.t - t0) / 1000).toFixed(1)}s`).join(' ')}`
    const t0 = Date.now()
    await Promise.all([R, K, Q].map(x => waitStatus(x.id, 'up', 30000, diag)))
    R.fx.fail(500); K.fx.fail(500); Q.fx.fail(500)
    await Promise.all([R, K, Q].map(x => waitStatus(x.id, 'down', 30000, diag)))
    await until('DOWN alerts', async () => [R, K, Q].every(x => rx.kinds(key, x.name).join() === 'down'), { timeout: 15000, diag })
    const outR = (await inst.outages(R.id))[0]
    const tStart = outR.startedAt
    // acknowledge K's outage right away; Q recovers right away
    const oK = (await inst.outages(K.id))[0]
    await inst.ok('POST', `/api/outages/${oK.id}/ack`)
    Q.fx.up()
    await waitStatus(Q.id, 'up', 20000, diag)

    // first reminder: >= 60 s after the outage began, at most one 30 s tick later
    await until('first reminder of R', async () => rx.kinds(key, R.name).filter(k => k === 'reminder').length >= 1, { timeout: 110000, diag })
    const r1 = rx.got(key, R.name).filter(m => m.kind === 'reminder')[0]
    const d1 = r1.t - tStart
    assert.ok(d1 >= 58000 && d1 <= 93000, `first reminder ${d1} ms after the outage started (expected 60-90 s: reminderMin=1 checked every 30 s)`)
    assert.match(r1.title, /Still down/)
    t.diagnostic(`reminders (reminderMin=1): first ${d1} ms after the outage began`)
    assert.match(r1.title, /for [12]m/)
    await until('second reminder of R', async () => rx.kinds(key, R.name).filter(k => k === 'reminder').length >= 2, { timeout: 110000, diag })
    const r2 = rx.got(key, R.name).filter(m => m.kind === 'reminder')[1]
    const gap = r2.t - r1.t
    assert.ok(gap >= 58000 && gap <= 93000, `second reminder ${gap} ms after the first (expected ~reminderMin)`)
    t.diagnostic(`reminders: second ${gap} ms after the first; acknowledged and recovered monitors sent none`)
    assert.equal(rx.kinds(key, K.name).join(), 'down', `acknowledged outage: no reminders (${fmt(rx.got(key, K.name).map(m => m.title))})`)
    assert.equal(rx.kinds(key, Q.name).join(), 'down,recovered', 'a recovered monitor sends no reminders')
    const ack = (await inst.mon(K.id)).ack
    assert.ok(ack && ack.by === 'Monitoring Admin', `ack is attributed: ${fmt(ack)}`)

    // reminders stop at recovery (Q recovered early and was never reminded, see above); R's recovery still alerts
    R.fx.up()
    await waitStatus(R.id, 'up', 20000, diag)
    await until('R recovery', async () => rx.kinds(key, R.name).slice(-1)[0] === 'recovered', { timeout: 15000, diag })
  })

  // ═══════════════════════════════════════════════════════════════════════
  it('11. push monitor: heartbeats keep it up, silence beyond interval+grace goes down on time, ?status=down works, recovery alerts', { timeout: 170000 }, async t => {
    if (needsInternet(t)) return
    const key = uniq('s11'), name = uniq('s11-job')
    const ch = await inst.channel(rx, key)
    const mon = await inst.create({ name, type: 'push', target: '', channelIds: [ch.id], retries: 0, intervalS: 10, retryIntervalS: 10, settings: { graceS: 3 } })
    assert.ok(mon.pushToken, 'a push monitor gets a token')
    assert.ok(mon.pushUrl.endsWith(`/push/${mon.pushToken}`))
    const push = async (q = '') => { const r = await fetch(`${inst.base}/push/${mon.pushToken}${q}`); return r.status }
    const diag = dump(mon.id, null, key, name)
    assert.equal(await (async () => (await fetch(`${inst.base}/push/not-a-real-token`)).status)(), 404, 'unknown token -> 404')

    assert.equal(await push('?ping=123'), 200)
    const m1 = await waitStatus(mon.id, 'up', 8000, diag)
    assert.equal(m1.latencyMs, 123, 'ping= is recorded as the response time')
    for (let i = 0; i < 5; i++) {
      await sleep(4500) // interval(10)+grace(3) = 13 s window; pushing every 4.5 s keeps it alive
      assert.equal(await push(), 200)
      assert.equal((await inst.mon(mon.id)).status, 'up', `still up after push ${i + 2}`)
    }
    assert.equal(rx.got(key).length, 0, 'no alert while heartbeats arrive')

    // silence: DOWN must come at lastPush + interval + grace = 13 s
    const tLast = Date.now()
    await waitStatus(mon.id, 'down', 40000, diag)
    const beats = await waitBeats(mon.id, b => b.some(x => x.status === 'down'), 'DOWN beat stored')
    const down = beats.find(b => b.status === 'down')
    near(down.ts - tLast, 13000, 2000, 'detection time after the last push (interval 10 s + grace 3 s)')
    assert.match(down.msg, /no push received for 13 s/)
    t.diagnostic(`push: DOWN beat ${down.ts - tLast} ms after the last heartbeat (interval 10 s + grace 3 s = 13 s)`)
    assert.equal(down.errClass, 'timeout')
    await until('DOWN alert', async () => rx.kinds(key).join() === 'down', { timeout: 12000, diag })
    await sleep(14000)
    assert.deepEqual(rx.kinds(key), ['down'], 'continued silence does not repeat the alert')
    assert.equal((await inst.outages(mon.id)).length, 1)

    // a push recovers it
    assert.equal(await push(), 200)
    await waitStatus(mon.id, 'up', 8000, diag)
    await until('recovery alert', async () => rx.kinds(key).join() === 'down,recovered', { timeout: 12000, diag })
    // rate limit: a second call inside the same second is accepted (200) but ignored
    assert.equal(await push(), 200)
    assert.equal(await push('?status=down&msg=ignored'), 200)
    await sleep(300)
    assert.equal((await inst.mon(mon.id)).status, 'up', 'push rate limit: 1 call/second/token, the extra call is ignored')

    // explicit failure report
    await sleep(1500)
    assert.equal(await push('?status=down&msg=nightly%20job%20failed'), 200)
    const m3 = await waitStatus(mon.id, 'down', 8000, diag)
    assert.match(m3.reason, /nightly job failed/)
    assert.equal(m3.errClass, 'other')
    await until('2nd DOWN alert', async () => rx.kinds(key).join() === 'down,recovered,down', { timeout: 12000, diag })
    assert.match(rx.got(key)[2].message, /nightly job failed/)
    await sleep(1500)
    assert.equal(await push(), 200)
    await waitStatus(mon.id, 'up', 8000, diag)
    await until('2nd recovery', async () => rx.kinds(key).join() === 'down,recovered,down,recovered', { timeout: 12000, diag })
    const outs = await inst.outages(mon.id)
    assert.equal(outs.length, 2, `two episodes:\n${outageTable(outs)}`)
    assert.ok(outs.every(o => o.endedAt))
  })

  // ═══════════════════════════════════════════════════════════════════════
  it('12. outage records: start/end/duration, first reason and errClass per episode, one outage per episode, ack and note', { timeout: 150000 }, async t => {
    if (needsInternet(t)) return
    const fx = await Fixture.start(); t.after(() => fx.stop())
    const key = uniq('s12'), name = uniq('s12-svc')
    const ch = await inst.channel(rx, key)
    const mon = await inst.create({ name, target: fx.target, channelIds: [ch.id], retries: 2, intervalS: 3600, retryIntervalS: 3600, timeoutMs: 1500, settings: { keyword: 'healthy' } })
    const diag = dump(mon.id, fx, key, name)
    fx.set({ body: 'all healthy' })
    assert.equal((await inst.checkNow(mon.id)).status, 'up')

    // episode 1: HTTP 500, then the port closes: first reason/class are from the FIRST failure
    fx.fail(500)
    assert.equal((await inst.checkNow(mon.id)).status, 'pending')
    await fx.close()
    assert.equal((await inst.checkNow(mon.id)).status, 'pending')
    const m = await inst.checkNow(mon.id)
    assert.equal(m.status, 'down')
    assert.equal(m.errClass, 'refused', 'the monitor shows the latest failure class')
    let outs = await waitBeats(mon.id, b => b.some(x => x.status === 'down')).then(() => inst.outages(mon.id))
    assert.equal(outs.length, 1)
    assert.match(outs[0].firstReason, /HTTP 500/, 'outage keeps the reason of the FIRST failed check')
    assert.equal(outs[0].errClass, 'http_status')
    await inst.checkNow(mon.id); await inst.checkNow(mon.id)
    assert.equal((await inst.outages(mon.id)).length, 1, 'more failed checks inside one episode do not open more outages')
    // acknowledge + note on the open outage
    const o1 = outs[0]
    await inst.ok('POST', `/api/outages/${o1.id}/ack`)
    await inst.ok('POST', `/api/outages/${o1.id}/note`, { note: 'DB failover in progress' })
    const sum = await inst.fresh(mon.id)
    assert.equal(sum.ack?.by, 'Monitoring Admin')
    assert.equal(sum.outageId, o1.id)
    let listed = (await inst.outages(mon.id))[0]
    assert.equal(listed.ackedBy, 'Monitoring Admin')
    assert.ok(listed.ackedAt >= o1.startedAt && listed.ackedAt <= Date.now() + 250, `ackedAt (the server's and node's clocks may differ by a few ms on Windows) ${listed.ackedAt} must lie between the outage start ${o1.startedAt} and now ${Date.now()}`)
    assert.equal(listed.note, 'DB failover in progress')
    assert.equal(listed.noteBy, 'Monitoring Admin')
    // the single-note call lands in the outage's note thread (multi-note support keeps it working)
    assert.equal(listed.noteCount, 1)
    const thread = (await inst.ok('GET', `/api/outages/${o1.id}/notes`)).notes
    assert.deepEqual(thread.map(n => n.body), ['DB failover in progress'])
    const active = (await inst.ok('GET', `/api/outages?monitorId=${mon.id}&active=true`)).outages
    assert.equal(active.length, 1)
    assert.equal((await inst.api('POST', '/api/outages/999999/ack')).status, 404)
    await fx.open(); fx.set({ body: 'all healthy' })
    assert.equal((await inst.checkNow(mon.id)).status, 'up')

    // (alerts for Down/Recovered are held 3 s to detect storms, and a monitor that flaps inside that window is folded into one
    // message per kind, so keep the episodes more than 3 s apart to count them separately)
    await sleep(3300)
    // episode 2: timeout
    fx.slow(4000, { body: 'all healthy' })
    assert.equal((await inst.checkNow(mon.id)).status, 'pending')
    await inst.checkNow(mon.id)
    const m2 = await inst.checkNow(mon.id)
    assert.equal(m2.status, 'down')
    assert.equal(m2.errClass, 'timeout')
    assert.match(m2.reason, /timeout after 1500 ms/)
    fx.up(); fx.set({ body: 'all healthy' })
    assert.equal((await inst.checkNow(mon.id)).status, 'up')

    await sleep(3300)
    // episode 3: assertion failure (keyword missing)
    fx.set({ body: 'maintenance page' })
    await inst.checkNow(mon.id); await inst.checkNow(mon.id)
    const m3 = await inst.checkNow(mon.id)
    assert.equal(m3.status, 'down')
    assert.equal(m3.errClass, 'assertion')
    fx.set({ body: 'all healthy' })
    assert.equal((await inst.checkNow(mon.id)).status, 'up')

    await sleep(2200)
    outs = await inst.outages(mon.id)
    assert.equal(outs.length, 3, `one outage per failure episode:\n${outageTable(outs)}`)
    assert.deepEqual(outs.map(o => o.errClass), ['http_status', 'timeout', 'assertion'])
    assert.match(outs[1].firstReason, /timeout after 1500 ms/)
    assert.match(outs[2].firstReason, /keyword "healthy" not found/)
    for (const o of outs) {
      assert.ok(o.endedAt && o.endedAt > o.startedAt, `closed with a positive duration: ${fmt(o)}`)
      assert.ok(o.startedAt > Date.now() - 120000 && o.endedAt <= Date.now() + 250, 'timestamps are epoch milliseconds')
    }
    for (let i = 1; i < outs.length; i++) assert.ok(outs[i].startedAt >= outs[i - 1].endedAt, 'episodes do not overlap')
    const closedAck = outs[0]
    assert.equal(closedAck.ackedBy, 'Monitoring Admin', 'acknowledgement survives the recovery')
    assert.equal(closedAck.note, 'DB failover in progress')
    await until('six alerts', async () => rx.got(key).length >= 6, { timeout: 15000, diag: async () => `received: ${rx.got(key).map(m => `${m.kind}@${m.t - outs[0].startedAt}ms`).join(', ')}
${outageTable(outs)}
log: ${fmt((await inst.alertLog(mon.id)).map(e => `${e.event}:${e.ok}:${e.ts - outs[0].startedAt}`))}` })
    assert.deepEqual(rx.kinds(key), ['down', 'recovered', 'down', 'recovered', 'down', 'recovered'], `alerts: ${fmt(rx.got(key).map(m => m.title))}`)
    // duration in the recovery alert agrees with the record
    rx.got(key).filter(m => m.kind === 'recovered').forEach((m, i) => {
      const o = outs[i]
      assert.ok(downMatches(m.title, o.endedAt - o.startedAt), `recovery #${i + 1}: ${m.title}`)
    })
    // the global outage list and summary agree
    const all = (await inst.ok('GET', `/api/outages?monitorId=${mon.id}&limit=50`)).outages
    assert.equal(all.length, 3)
    const sumry = await inst.ok('GET', '/api/outages/summary')
    assert.ok(sumry && typeof sumry === 'object')
  })

  // ═══════════════════════════════════════════════════════════════════════
  it('13. uptime numbers: after a known sequence, uptime24h/7d/30d/90d equal the expected percentage and agree with the daily rollups', { timeout: 150000 }, async t => {
    if (needsInternet(t)) return
    const fx = await Fixture.start(); t.after(() => fx.stop())
    const name = uniq('s13-svc')
    const mon = await inst.create({ name, target: fx.target, retries: 0, intervalS: 3600, retryIntervalS: 3600, timeoutMs: 3000 })
    // 12 good, 3 bad, 5 good = 17/20 = 85 %
    const plan = 'g'.repeat(12) + 'b'.repeat(3) + 'g'.repeat(5)
    for (const c of plan) {
      if (c === 'g') fx.up(); else fx.fail(500)
      await inst.checkNow(mon.id)
    }
    const beats = await waitBeats(mon.id, b => b.length >= plan.length, 'all 20 beats stored')
    assert.equal(beats.length, 20, `exactly the 20 checks that were driven: ${seq(beats)}`)
    assert.equal(seq(beats), 'up x12 > down x3 > up x5')
    await sleep(1500)
    const m = await inst.mon(mon.id)
    for (const k of ['uptime24h', 'uptime7d', 'uptime30d', 'uptime90d']) near(m[k], 85, 0.001, `${k} after 17 good / 3 bad`)
    t.diagnostic(`uptime after 17 good + 3 bad checks: 24h ${m.uptime24h}, 7d ${m.uptime7d}, 30d ${m.uptime30d}, 90d ${m.uptime90d}`)

    // the daily and hourly rollups hold the same counts
    const daily = (await inst.ok('GET', `/api/monitors/${mon.id}/series?range=90d`)).series
    const totals = daily.reduce((a, r) => ({ up: a.up + r.Up, down: a.down + r.Down, deg: a.deg + r.Degraded, pend: a.pend + r.Pending }), { up: 0, down: 0, deg: 0, pend: 0 })
    assert.deepEqual(totals, { up: 17, down: 3, deg: 0, pend: 0 }, 'daily rollup counts')
    const good = totals.up + totals.deg + totals.pend
    near((good / (good + totals.down)) * 100, m.uptime90d, 0.001, 'uptime90d vs daily rollups')
    const dayList = await inst.ok('GET', `/api/monitors/${mon.id}/daily?days=7`)
    assert.equal(dayList.length, 1, 'all checks fall on one DISPLAY_TZ day')
    near(dayList[0].uptime, 85, 0.001, '/daily uptime')
    assert.equal(dayList[0].down, 3)
    assert.equal(dayList[0].outages, 1, 'the 3 consecutive failures are one outage')
    const hourly = (await inst.ok('GET', `/api/monitors/${mon.id}/series?range=7d`)).series
    assert.equal(hourly.reduce((s, r) => s + r.Up + r.Down, 0), 20, 'hourly rollups count every check once (also across an hour boundary)')

    // PENDING counts as good, DOWN as bad (retries=2): fail x3 = pending, pending, down
    const mon2 = await inst.create({ name: uniq('s13-pending'), target: fx.target, retries: 2, intervalS: 3600, retryIntervalS: 3600, timeoutMs: 3000 })
    fx.up(); await inst.checkNow(mon2.id); await inst.checkNow(mon2.id)
    fx.fail(500); await inst.checkNow(mon2.id); await inst.checkNow(mon2.id); await inst.checkNow(mon2.id)
    await waitBeats(mon2.id, b => b.length >= 5, '5 beats')
    await sleep(500)
    const m2 = await inst.mon(mon2.id)
    assert.equal(seq(await inst.beats(mon2.id)), 'up x2 > pending x2 > down')
    near(m2.uptime24h, (4 / 5) * 100, 0.001, 'uptime with 2 up, 2 pending (good) and 1 down (bad)')

    // a monitor with nothing countable has no uptime, not 0 or 100
    const mon3 = await inst.create({ name: uniq('s13-fresh'), target: fx.target, retries: 0, intervalS: 3600, retryIntervalS: 3600 })
    const fresh = await inst.fresh(mon3.id)
    assert.equal(fresh.uptime24h, null)
    assert.equal(fresh.status, 'unknown')
  })

  // ═══════════════════════════════════════════════════════════════════════
  it('14. response-time analysis: avg/min/max/percentiles of known delays match for every window (1h, 24h, 7d, 30d, 90d)', { timeout: 150000 }, async t => {
    if (needsInternet(t)) return
    const fx = await Fixture.start(); t.after(() => fx.stop())
    const mon = await inst.create({ name: uniq('s14-svc'), target: fx.target, retries: 0, intervalS: 3600, retryIntervalS: 3600, timeoutMs: 6000 })
    // 20 checks: 10 x 100 ms, 5 x 300 ms, 4 x 600 ms, 1 x 1500 ms (in a shuffled but fixed order)
    const delays = [100, 300, 100, 600, 100, 100, 300, 1500, 100, 600, 100, 300, 100, 600, 100, 300, 600, 100, 300, 100]
    const sorted = [...delays].sort((a, b) => a - b)
    const rank = q => sorted[Math.max(0, Math.ceil(q * sorted.length) - 1)]
    fx.script(n => ({ delay: delays[n % delays.length] }))
    const measured = []
    for (let i = 0; i < delays.length; i++) {
      const m = await inst.checkNow(mon.id)
      measured.push(m.latencyMs)
    }
    await waitBeats(mon.id, b => b.length >= 20, '20 beats stored', 20000)
    await sleep(1500)
    assert.equal(fx.hits.length, 20, 'exactly 20 requests reached the target')
    const sumMs = measured.reduce((a, b) => a + b, 0)
    // every measured latency is the injected delay plus a small overhead
    measured.forEach((l, i) => assert.ok(l >= delays[i] - 5 && l <= delays[i] + 200, `check ${i}: measured ${l} ms for an injected ${delays[i]} ms`))
    const expAvg = delays.reduce((a, b) => a + b, 0) / delays.length
    const tol = 70 // the overhead of a local check plus timer granularity under CPU contention

    const stats = {}
    for (const range of ['1h', '24h', '7d', '30d', '90d']) {
      const j = await inst.ok('GET', `/api/monitors/${mon.id}/latency?range=${range}`)
      stats[range] = j.summary
      const s = j.summary
      assert.equal(s.count, 20, `${range}: sample count`)
      near(s.avg, sumMs / 20, 1, `${range}: avg equals the mean of the measured latencies`)
      near(s.avg, expAvg, tol, `${range}: avg vs injected mean`)
      assert.ok(s.min >= 95 && s.min <= 100 + tol, `${range}: min ${s.min}`)
      assert.ok(s.max >= 1495 && s.max <= 1500 + 120, `${range}: max ${s.max}`)
      const exact = range === '1h' || range === '24h'
      const relTol = exact ? 0 : 0.12 // log-bucket histogram: ~5 % per bin edge, 10 % bins
      for (const [k, q] of [['p50', 0.5], ['p90', 0.9], ['p95', 0.95], ['p99', 0.99]]) {
        const want = rank(q)
        near(s[k], want, Math.max(tol, want * relTol) + (exact ? 0 : 20), `${range}: ${k} for injected ${want} ms (nearest-rank over ${sorted.join(',')})`)
      }
      assert.ok(Array.isArray(j.points) && j.points.reduce((a, p) => a + p.count, 0) === 20, `${range}: chart points hold all 20 samples`)
      assert.equal(j.source, { '1h': 'raw', '24h': 'raw', '7d': 'hourly', '30d': 'hourly', '90d': 'daily' }[range])
    }
    // raw windows are exact: they equal the nearest-rank percentiles of what was measured
    const ms = [...measured].sort((a, b) => a - b)
    for (const [k, q] of [['p50', 0.5], ['p90', 0.9], ['p95', 0.95], ['p99', 0.99]]) {
      assert.equal(stats['1h'][k], ms[Math.max(0, Math.ceil(q * 20) - 1)], `1h ${k} is the exact nearest-rank value of the measured samples`)
    }
    assert.equal((await inst.mon(mon.id)).latencyMs, measured[19], 'summary shows the latest latency')
    assert.equal((await inst.api('GET', `/api/monitors/${mon.id}/latency?range=2h`)).status, 400, 'unknown range is rejected')
    t.diagnostic(`latency (injected mean ${expAvg} ms, nearest-rank p50/p90/p95/p99 ${rank(0.5)}/${rank(0.9)}/${rank(0.95)}/${rank(0.99)}): ` + Object.entries(stats).map(([r, x]) => `${r}: avg ${x.avg} p50 ${x.p50} p90 ${x.p90} p95 ${x.p95} p99 ${x.p99} min ${x.min} max ${x.max}`).join(' | '))
  })

  // ═══════════════════════════════════════════════════════════════════════
  it('15. scheduling: checks per interval, retry interval, and edit-while-running applies on the next check with no double scheduling', { timeout: 220000 }, async t => {
    if (needsInternet(t)) return
    const fx1 = await Fixture.start(), fx2 = await Fixture.start(), fx3 = await Fixture.start()
    t.after(() => Promise.all([fx1.stop(), fx2.stop(), fx3.stop()]))
    const gaps = hits => hits.slice(1).map((h, i) => h.t - hits[i].t)
    const monA = await inst.create({ name: uniq('s15-a'), target: fx1.target, retries: 0, intervalS: 10, retryIntervalS: 10, timeoutMs: 3000 })
    const monB = await inst.create({ name: uniq('s15-b'), target: fx3.target, retries: 0, intervalS: 30, retryIntervalS: 10, timeoutMs: 3000 })

    // (a) steady state: ~one check per 10 s
    await until('3 checks of A', async () => fx1.hits.length >= 3, { timeout: 60000 })
    await sleep(12000)
    const base = fx1.hits.slice()
    const g = gaps(base)
    assert.ok(g.every(x => x >= 9000 && x <= 11500), `intervalS=10: gaps between consecutive checks must be 10 s +-1.5 s, got ${g.join(', ')}`)
    t.diagnostic(`scheduling: intervalS=10 gaps ${g.join(', ')} ms`)
    const span = base[base.length - 1].t - base[0].t
    near(base.length - 1, Math.round(span / 10000), 0, `number of intervals in ${span} ms`)

    // (b) edit target and interval of a running monitor: B 30 s -> 10 s
    const editAt = Date.now()
    await inst.edit(monB.id, { intervalS: 10 })
    // (c) A: new target, 20 s
    const nHits1 = fx1.hits.length
    const editA = Date.now()
    await inst.edit(monA.id, { target: fx2.target, intervalS: 20 })
    await until('first check of the new target, right after the edit', async () => fx2.hits.length >= 1, { timeout: 5000 })
    near(fx2.hits[0].t - editA, 0, 2500, 'the changed monitor is re-checked immediately (target changed)')
    await sleep(42000)
    const oldLate = fx1.hits.filter(h => h.t > editA + 1500)
    assert.equal(oldLate.length, 0, `no check may go to the OLD target after the edit (${oldLate.length} did)`)
    assert.ok(fx1.hits.length - nHits1 <= 1, 'at most one in-flight request to the old target')
    const h2 = fx2.hits
    const g2 = gaps(h2)
    assert.ok(h2.length >= 3 && h2.length <= 4, `20 s interval over ~46 s: expected 3-4 checks (immediate + 2), got ${h2.length} (gaps ${g2.join(', ')})`)
    assert.ok(g2.slice(1).every(x => x >= 18500 && x <= 21500), `intervalS=20 gaps after the immediate check: ${g2.join(', ')} (a double schedule would show ~10 s gaps)`)
    t.diagnostic(`edit while running: first check of the new target ${fx2.hits[0].t - editA} ms after the edit; intervalS=20 gaps ${g2.join(', ')} ms; ${oldLate.length} late requests to the old target`)

    // B: the shortened interval takes effect without waiting for the old 30 s timer to expire twice
    const hb = fx3.hits.filter(h => h.t >= editAt - 100)
    const gb = gaps(hb)
    assert.ok(hb.length >= 4, `after 30 s -> 10 s, B should have been checked at least 4 times in ~50 s (got ${hb.length})`)
    assert.ok(gb.slice(1).every(x => x >= 9000 && x <= 11500), `B intervalS=10 gaps: ${gb.join(', ')}`)
    assert.ok(gb.every(x => x > 4000), 'no duplicate (double-scheduled) checks of B')
    assert.equal((await inst.mon(monA.id)).intervalS, 20)
    assert.equal((await inst.mon(monA.id)).target, fx2.target)

    // (d) retry interval is used while pending
    fx2.fail(500)
    await inst.edit(monA.id, { retries: 2, retryIntervalS: 10, intervalS: 60 })
    const tFlip = Date.now()
    await until('A pending after a failing check', async () => (await inst.mon(monA.id)).status === 'pending', { timeout: 70000 })
    await until('A to fail twice more in 10 s steps', async () => fx2.hits.filter(h => h.t >= tFlip && h.status === 500).length >= 3, { timeout: 40000 })
    const failHits = fx2.hits.filter(h => h.t >= tFlip && h.status === 500)
    const gf = gaps(failHits)
    assert.ok(gf.slice(0, 2).every(x => x >= 9000 && x <= 11500), `while PENDING the retry interval (10 s) applies, not intervalS=60: ${gf.join(', ')}`)
  })

  // ═══════════════════════════════════════════════════════════════════════
  it('16. deleting a monitor mid-flight stops its checks, removes its outages and leaves nothing behind', { timeout: 120000 }, async t => {
    if (needsInternet(t)) return
    const fx = await Fixture.start(); t.after(() => fx.stop())
    const key = uniq('s16'), name = uniq('s16-svc')
    const ch = await inst.channel(rx, key)
    const mon = await inst.create({ name, target: fx.target, channelIds: [ch.id], retries: 0, intervalS: 10, retryIntervalS: 10, timeoutMs: 8000 })
    const diag = dump(mon.id, fx, key, name)
    await waitStatus(mon.id, 'up', 30000, diag)
    fx.fail(500)
    await waitStatus(mon.id, 'down', 30000, diag)
    await until('DOWN alert', async () => rx.kinds(key).join() === 'down', { timeout: 12000, diag })
    await until('the outage to be stored', async () => (await inst.outages(mon.id)).length === 1, { timeout: 8000, diag })
    assert.equal((await inst.ok('GET', `/api/outages?active=true&limit=200`)).outages.filter(o => o.monitorId === mon.id).length, 1)

    // delete while a check is in flight (the target holds the request for 4 s)
    fx.slow(4000, { status: 500 })
    const n0 = fx.hits.length
    await until('a check to be in flight', async () => fx.hits.length > n0, { timeout: 15000 })
    const tDel = Date.now()
    const del = await inst.api('DELETE', `/api/monitors/${mon.id}`)
    assert.equal(del.status, 204)
    assert.equal((await inst.api('GET', `/api/monitors/${mon.id}`)).status, 404)
    assert.equal(await inst.fresh(mon.id), undefined, 'gone from the live list')
    fx.up()
    const nAfter = fx.hits.length
    await holds('a deleted monitor must not be checked, alert or reappear', 32000, async () => {
      const late = fx.hits.filter(h => h.t > tDel + 500)
      if (late.length > 1) return `${late.length} requests reached the target after the delete`
      if (await inst.mon(mon.id)) return 'monitor reappeared in the list'
      if ((await inst.beats(mon.id)).length) return 'heartbeats were written for the deleted monitor'
      if (rx.got(key).length > 1) return `alerts after the delete: ${rx.kinds(key)}`
      return null
    }, 500)
    assert.equal((await inst.ok('GET', `/api/outages?monitorId=${mon.id}`)).outages.length, 0, 'its outages are gone')
    assert.equal((await inst.ok('GET', '/api/outages?active=true&limit=200')).outages.filter(o => o.monitorId === mon.id).length, 0, 'no active outage left behind')
    assert.equal((await inst.ok('GET', `/api/events?monitorId=${mon.id}`)).events.length, 0, 'status events are gone')
    assert.ok(fx.hits.length - nAfter <= 1, `target requests after the delete: ${fx.hits.length - nAfter}`)
    assert.deepEqual(rx.kinds(key), ['down'], 'no recovery/other alert for a deleted monitor')
    assert.ok((await fetch(`${inst.base}/healthz`)).ok)
  })

  // ═══════════════════════════════════════════════════════════════════════
  it('X1. alert delivery: every channel of a monitor is told, a failing channel is retried and logged without delaying the others, errors never show the URL', { timeout: 120000 }, async t => {
    if (needsInternet(t)) return
    const fx = await Fixture.start(); t.after(() => fx.stop())
    const keyA = uniq('x1a'), keyB = uniq('x1b'), name = uniq('x1-svc')
    const chA = await inst.channel(rx, keyA), chB = await inst.channel(rx, keyB)
    const mon = await inst.create({ name, target: fx.target, channelIds: [chA.id, chB.id], retries: 0, intervalS: 3600, retryIntervalS: 3600, timeoutMs: 3000 })
    const diag = async () => `A: ${rx.kinds(keyA)} refused=${rx.refused.filter(r => r.key === keyA).length}  B: ${rx.kinds(keyB)}\nlog: ${fmt(await inst.alertLog(mon.id))}`
    assert.equal((await inst.checkNow(mon.id)).status, 'up')
    rx.failing.add(keyA)
    fx.fail(500)
    assert.equal((await inst.checkNow(mon.id)).status, 'down')
    await until('B (healthy channel) gets the DOWN alert', async () => rx.kinds(keyB).join() === 'down', { timeout: 12000, diag })
    const tB = rx.got(keyB)[0].t
    await until('first (failed) delivery attempt to A', async () => rx.refused.some(r => r.key === keyA), { timeout: 12000, diag })
    const tFirst = rx.refused.find(r => r.key === keyA).t
    near(tFirst, tB, 1500, 'both channels are served at the same time (a failing channel does not delay the healthy one)')
    rx.failing.delete(keyA) // A is healthy again before the 5 s retry
    await until('retried delivery to A', async () => rx.kinds(keyA).join() === 'down', { timeout: 15000, diag })
    const tRetry = rx.got(keyA)[0].t
    assert.ok(tRetry - tFirst >= 4500 && tRetry - tFirst <= 8000, `retry delay ${tRetry - tFirst} ms (docs: first retry after 5 s)`)
    await sleep(1500)
    const log = await inst.alertLog(mon.id)
    const forA = log.filter(e => e.channelId === chA.id), forB = log.filter(e => e.channelId === chB.id)
    assert.deepEqual(forA.map(e => `${e.event}:${e.attempt}:${e.ok}`), ['down:1:false', 'down:2:true'], `channel A log: ${fmt(forA)}`)
    assert.deepEqual(forB.map(e => `${e.event}:${e.attempt}:${e.ok}`), ['down:1:true'], `channel B log: ${fmt(forB)}`)
    assert.match(forA[0].error, /^http 500/, 'the log says why it failed')
    assert.ok(!/127\.0\.0\.1|http:\/\//.test(forA[0].error), `the error text must not expose the channel URL: "${forA[0].error}"`)
    assert.equal(forA[0].monitorName, name)
    fx.up()
    assert.equal((await inst.checkNow(mon.id)).status, 'up')
    await until('recovery on both channels', async () => rx.kinds(keyA).join() === 'down,recovered' && rx.kinds(keyB).join() === 'down,recovered', { timeout: 12000, diag })
    await sleep(2000)
    assert.equal(rx.kinds(keyA).length + rx.kinds(keyB).length, 4, 'no duplicates')
  })

  // ═══════════════════════════════════════════════════════════════════════
  it('X2. the HTTP check sends what is configured (method, headers, body, auth, redirects) and keeps secrets out of the API and the logs', { timeout: 90000 }, async t => {
    if (needsInternet(t)) return
    const fx = await Fixture.start(); t.after(() => fx.stop())
    fx.script((n, req) => {
      const u = req.url.split('?')[0]
      if (u === '/r1') return { status: 302, headers: { Location: '/r2' }, body: '' }
      if (u === '/r2') return { status: 302, headers: { Location: '/final' }, body: '' }
      if (u === '/final') return { body: 'arrived' }
      return null
    })
    const base = { retries: 0, intervalS: 3600, retryIntervalS: 3600, timeoutMs: 4000 }
    const lastHit = () => fx.hits[fx.hits.length - 1]

    // method + body + header + secret header
    const post = await inst.create({
      ...base, name: uniq('x2-post'), target: fx.url('/api/ping'),
      settings: { method: 'POST', bodyType: 'json', body: '{"ping":true}' },
      headers: [{ name: 'X-Api-Key', value: 'abc123', secret: false }, { name: 'X-Hook-Secret', value: 'S3CR3T-HDR', secret: true }],
    })
    assert.equal((await inst.checkNow(post.id)).status, 'up')
    const h = lastHit()
    assert.equal(h.method, 'POST')
    assert.equal(h.url, '/api/ping')
    assert.equal(h.body, '{"ping":true}')
    assert.equal(h.headers['content-type'], 'application/json')
    assert.equal(h.headers['x-api-key'], 'abc123')
    assert.equal(h.headers['x-hook-secret'], 'S3CR3T-HDR', 'a secret header is sent')
    assert.match(h.headers['user-agent'], /^opskest\//)

    // basic + bearer auth
    const basic = await inst.create({ ...base, name: uniq('x2-basic'), target: fx.url('/b'), settings: { authType: 'basic', basicUser: 'bob' }, secrets: { basicPass: 'PW-BASIC-9', bearerToken: null } })
    await inst.checkNow(basic.id)
    assert.equal(lastHit().headers.authorization, `Basic ${Buffer.from('bob:PW-BASIC-9').toString('base64')}`)
    const bearer = await inst.create({ ...base, name: uniq('x2-bearer'), target: fx.url('/c'), settings: { authType: 'bearer' }, secrets: { basicPass: null, bearerToken: 'TOKEN-BEARER-7' } })
    await inst.checkNow(bearer.id)
    assert.equal(lastHit().headers.authorization, 'Bearer TOKEN-BEARER-7')

    // HEAD
    const head = await inst.create({ ...base, name: uniq('x2-head'), target: fx.url('/h'), settings: { method: 'HEAD' } })
    assert.equal((await inst.checkNow(head.id)).status, 'up')
    assert.equal(lastHit().method, 'HEAD')

    // redirects
    const follow = await inst.create({ ...base, name: uniq('x2-follow'), target: fx.url('/r1'), settings: {} })
    const n0 = fx.hits.length
    assert.equal((await inst.checkNow(follow.id)).status, 'up', 'redirects are followed by default')
    assert.deepEqual(fx.hits.slice(n0).map(x => x.url), ['/r1', '/r2', '/final'])
    const limited = await inst.create({ ...base, name: uniq('x2-limit'), target: fx.url('/r1'), settings: { maxRedirects: 1 } })
    const lm = await inst.checkNow(limited.id)
    assert.equal(lm.status, 'down')
    assert.match(lm.reason, /stopped after 1 redirects/)
    const none = await inst.create({ ...base, name: uniq('x2-none'), target: fx.url('/r1'), settings: { maxRedirects: 0 } })
    const nm = await inst.checkNow(none.id)
    assert.equal(nm.status, 'down')
    assert.match(nm.reason, /HTTP 302 \(expected 200-299\)/)
    const okRedirect = await inst.create({ ...base, name: uniq('x2-302ok'), target: fx.url('/r1'), settings: { maxRedirects: 0, acceptedCodes: '300-399' } })
    assert.equal((await inst.checkNow(okRedirect.id)).status, 'up', '302 accepted explicitly')

    // secrets stay out of the API and out of the server log
    for (const id of [post.id, basic.id, bearer.id]) {
      const text = (await inst.api('GET', `/api/monitors/${id}`)).text
      for (const secret of ['S3CR3T-HDR', 'PW-BASIC-9', 'TOKEN-BEARER-7']) assert.ok(!text.includes(secret), `GET /api/monitors/${id} must not return ${secret}`)
    }
    const stored = await inst.ok('GET', `/api/monitors/${post.id}`)
    assert.deepEqual(stored.headers.map(x => [x.name, x.secret, x.isSet ?? false, x.value ?? null]), [['X-Api-Key', false, false, 'abc123'], ['X-Hook-Secret', true, true, null]])
    const basicOut = await inst.ok('GET', `/api/monitors/${basic.id}`)
    assert.equal(basicOut.secrets.basicPass.isSet, true)
    for (const secret of ['S3CR3T-HDR', 'PW-BASIC-9', 'TOKEN-BEARER-7', 'abc123']) assert.ok(!inst.logs().includes(secret), `the server log must never contain ${secret}`)
  })


  // ═══════════════════════════════════════════════════════════════════════
  it('X3. a newly created monitor is checked within seconds, whatever its interval; boot-time jitter does not apply to it', { timeout: 60000 }, async t => {
    if (needsInternet(t)) return
    const fx = await Fixture.start(); t.after(() => fx.stop())
    const t0 = Date.now()
    const mon = await inst.create({ name: uniq('x3-svc'), target: fx.target, retries: 0, intervalS: 3600, retryIntervalS: 3600, timeoutMs: 3000 }, { quiet: false })
    const m = await until('the first check of a brand-new 1-hour monitor', async () => { const x = await inst.fresh(mon.id); return x?.status === 'up' && x }, { timeout: 12000, every: 100 })
    const first = fx.hits[0]
    assert.ok(first && first.t - t0 < 6000, `first check ${first ? first.t - t0 : 'never'} ms after creation (a 3600 s interval must not mean up to an hour of UNKNOWN)`)
    assert.equal(fx.hits.length, 1, 'exactly one check')
    assert.ok(m.lastCheckAt >= t0)
    // and the next one is a full interval later, not another early one
    await sleep(4000)
    assert.equal(fx.hits.length, 1, 'no second check soon after the first')
    // a push monitor is never actively checked, so it stays UNKNOWN until its first call
    const push = await inst.create({ name: uniq('x3-push'), type: 'push', target: '', retries: 0, intervalS: 3600, retryIntervalS: 3600, settings: { graceS: 30 } }, { quiet: false })
    await sleep(3500)
    assert.equal((await inst.fresh(push.id)).status, 'unknown')
  })


  // ═══════════════════════════════════════════════════════════════════════
  it('X4. the live stream (SSE) and /metrics report the same truth as the API: states, outage, removal', { timeout: 90000 }, async t => {
    if (needsInternet(t)) return
    const fxUp = await Fixture.start(), fxDown = await Fixture.start(), fxDeg = await Fixture.start(), fxPaused = await Fixture.start()
    t.after(() => Promise.all([fxUp, fxDown, fxDeg, fxPaused].map(f => f.stop())))
    fxDown.fail(500); fxDeg.slow(500)
    const base = { retries: 0, intervalS: 3600, retryIntervalS: 3600, timeoutMs: 3000 }
    const mUp = await inst.create({ ...base, name: uniq('x4-up'), target: fxUp.target })
    const mDown = await inst.create({ ...base, name: uniq('x4-down'), target: fxDown.target })
    const mDeg = await inst.create({ ...base, name: uniq('x4-deg'), target: fxDeg.target, degradedMs: 200 })
    const mPaused = await inst.create({ ...base, name: uniq('x4-paused'), target: fxPaused.target })
    await inst.checkNow(mUp.id); await inst.checkNow(mDown.id); await inst.checkNow(mDeg.id); await inst.checkNow(mPaused.id)
    await inst.ok('POST', `/api/monitors/${mPaused.id}/pause`)

    // open the live stream
    const ctl = new AbortController()
    t.after(() => ctl.abort())
    const events = []
    const res = await fetch(`${inst.base}/api/stream`, { signal: ctl.signal, headers: { Cookie: inst.cookies.map(c => `${c.name}=${c.value}`).join('; '), Origin: inst.origin } })
    assert.equal(res.status, 200)
    assert.match(res.headers.get('content-type') ?? '', /text\/event-stream/)
    ;(async () => {
      const dec = new TextDecoder()
      let buf = ''
      try {
        for await (const chunk of res.body) {
          buf += dec.decode(chunk, { stream: true })
          let i
          while ((i = buf.indexOf('\n\n')) >= 0) {
            const frame = buf.slice(0, i); buf = buf.slice(i + 2)
            const ev = /^event: (.+)$/m.exec(frame)?.[1], data = /^data: (.+)$/m.exec(frame)?.[1]
            if (ev && data) events.push({ t: Date.now(), ev, data: JSON.parse(data) })
          }
        }
      } catch { /* aborted */ }
    })()
    const snap = await until('the snapshot', async () => events.find(e => e.ev === 'snapshot'), { timeout: 8000 })
    const byId = Object.fromEntries(snap.data.monitors.map(m => [m.id, m]))
    assert.deepEqual([mUp, mDown, mDeg, mPaused].map(m => byId[m.id]?.status), ['up', 'down', 'degraded', 'paused'], 'the snapshot has every monitor in its true state')
    assert.ok(snap.data.outages.some(o => o.monitorId === mDown.id), 'the snapshot lists the open outage')

    // /metrics (token-protected) agrees
    assert.equal((await fetch(`${inst.base}/metrics`)).status, 401, 'metrics need the token')
    const text = await (await fetch(`${inst.base}/metrics`, { headers: { Authorization: 'Bearer metrics-test-token' } })).text()
    const metric = (name, id) => { const m = new RegExp(`^${name}\\{id="${id}",[^}]*\\} (\\S+)$`, 'm').exec(text); return m ? Number(m[1]) : undefined }
    assert.equal(metric('opskest_monitor_up', mUp.id), 1)
    assert.equal(metric('opskest_monitor_up', mDeg.id), 1, 'degraded still counts as up')
    assert.equal(metric('opskest_monitor_up', mDown.id), 0)
    assert.equal(metric('opskest_monitor_up', mPaused.id), -1, 'paused is neither up nor down')
    near(metric('opskest_monitor_uptime_24h', mUp.id), 100, 0.001, 'metrics uptime of the up monitor')
    near(metric('opskest_monitor_uptime_24h', mDown.id), 0, 0.001, 'metrics uptime of the down monitor')
    assert.ok(metric('opskest_monitor_latency_ms', mDeg.id) >= 450, 'metrics latency of the degraded monitor')

    // a change is pushed within about a second of the check that caused it
    const before = events.length
    fxUp.fail(500)
    const t0 = Date.now()
    await inst.checkNow(mUp.id)
    const delta = await until('a delta with the new state', async () => events.slice(before).find(e => e.ev === 'delta' && e.data.monitors?.some(m => m.id === mUp.id && m.status === 'down')), { timeout: 6000, every: 50 })
    assert.ok(delta.t - t0 < 4000, `the dashboard learns about it in ${delta.t - t0} ms`)
    const pushed = delta.data.monitors.find(m => m.id === mUp.id)
    assert.ok(pushed.outageId, 'the delta carries the outage')
    assert.ok(delta.data.outages.some(o => o.monitorId === mUp.id), 'and the active outage list')

    // deleting is announced as a removal
    const b2 = events.length
    await inst.ok('DELETE', `/api/monitors/${mDown.id}`)
    await until('the removal', async () => events.slice(b2).find(e => e.data.removed?.includes(mDown.id)), { timeout: 6000, every: 50 })
  })

})
