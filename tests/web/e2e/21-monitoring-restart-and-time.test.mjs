// Black-box proof of two more monitoring guarantees, each on its own server instance:
//   17. state survives a restart of the server process (same data directory): statuses, history, uptime, the
//       open outage, its acknowledgement and the alert state, with no spurious DOWN/recovery/degraded alert for
//       monitors whose situation did not change, and alerts still work afterwards;
//   18. clock and time zone: stored timestamps are UTC epoch milliseconds, daily buckets and day boundaries
//       follow DISPLAY_TZ (including a half-hour offset and a DST zone), hourly buckets stay UTC hours.
//
//   node --experimental-strip-types --test tests/e2e/21-monitoring-restart-and-time.test.mjs

import assert from 'node:assert/strict'
import { existsSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { before, describe, it } from 'node:test'
import { Fixture, Instance, OFFLINE_REASON, Receiver, fmt, guardOnline, holds, seq, sleep, until, uniq, wasDownMinutes, downMatches, outageTable } from './20-kit.mjs'

const SKIP = process.env.OPSKEST_E2E_BIN
  ? (existsSync(process.env.OPSKEST_E2E_BIN) ? false : 'OPSKEST_E2E_BIN does not exist')
  : (spawnSync('go', ['version'], { stdio: 'ignore' }).status === 0 ? false : 'Go toolchain not found and OPSKEST_E2E_BIN not set')

const near = (a, b, tol, msg) => assert.ok(Math.abs(a - b) <= tol, `${msg}: got ${a}, expected ${b} +-${tol}`)

describe('restart persistence and time zones', { skip: SKIP, concurrency: true }, () => {
  let online = true
  before(async () => { online = await guardOnline() })
  // ═══════════════════════════════════════════════════════════════════════
  it('17. restart: status, history, open outage, ack and alert state survive; no spurious alerts; alerts keep working', { timeout: 240000 }, async t => {
    if (!online) return t.skip(OFFLINE_REASON)
    const inst = await Instance.start()
    t.after(() => inst.stop())
    const rx = await Receiver.start()
    t.after(() => rx.stop())
    const key = 's17'
    const ch = await inst.channel(rx, key)
    const mk = async (label, over = {}) => {
      const fx = await Fixture.start(); t.after(() => fx.stop())
      const name = uniq(`s17-${label}`)
      const mon = await inst.create({ name, target: fx.target, channelIds: [ch.id], retries: 0, intervalS: 10, retryIntervalS: 10, timeoutMs: 3000, ...over })
      return { fx, name, id: mon.id, mon }
    }
    const A = await mk('stays-up')
    const B = await mk('down-acked', { reminderMin: 60 })
    const C = await mk('paused')
    const D = await mk('degraded', { degradedMs: 200, notifyDegraded: true })
    const P = await mk('push', { type: 'push', target: '', intervalS: 30, retryIntervalS: 30, settings: { graceS: 30 } })
    const all = [A, B, C, D, P]
    const diag = async () => `alerts: ${rx.all.map(m => `${m.kind}:${m.title}`).join(' | ')}\n` +
      (await Promise.all(all.map(async x => `${x.name}: ${fmt((({ status, since, outageId, ack }) => ({ status, since, outageId, ack }))(await inst.fresh(x.id) ?? {}))}`))).join('\n')

    const push = () => fetch(`${inst.base}/push/${P.mon.pushToken}`).then(r => r.status)
    assert.equal(await push(), 200)
    D.fx.slow(500)
    await Promise.all([A, D].map(x => until(`${x.name} up/degraded`, async () => ['up', 'degraded'].includes((await inst.mon(x.id))?.status), { timeout: 30000, diag })))
    await until('A up', async () => (await inst.mon(A.id))?.status === 'up', { timeout: 30000, diag })
    await until('D degraded', async () => (await inst.mon(D.id))?.status === 'degraded', { timeout: 30000, diag })
    await until('B up', async () => (await inst.mon(B.id))?.status === 'up', { timeout: 30000, diag })
    await until('C up', async () => (await inst.mon(C.id))?.status === 'up', { timeout: 30000, diag })
    await inst.ok('POST', `/api/monitors/${C.id}/pause`)
    B.fx.fail(500)
    await until('B down', async () => (await inst.mon(B.id))?.status === 'down', { timeout: 30000, diag })
    await until('alerts for B (down) and D (degraded)', async () => rx.kinds(key, B.name).join() === 'down' && rx.kinds(key, D.name).join() === 'degraded', { timeout: 15000, diag })
    const outB = (await until('B outage stored', async () => { const o = await inst.outages(B.id); return o.length ? o : null }, { timeout: 8000, diag }))[0]
    await inst.ok('POST', `/api/outages/${outB.id}/ack`)
    await inst.ok('POST', `/api/outages/${outB.id}/note`, { note: 'known, restarting the box' })
    // give every monitor several checks and let the writer commit
    await sleep(25000)
    assert.equal(await push(), 200)
    await sleep(2500)

    const snap = async () => {
      inst._mlAt = 0
      const sums = Object.fromEntries((await inst.monitors()).map(m => [m.id, m]))
      const out = { sums, beats: {}, outages: {}, alertLog: (await inst.ok('GET', '/api/alert-log?limit=200')).entries.length }
      for (const x of all) { out.beats[x.id] = await inst.beats(x.id); out.outages[x.id] = await inst.outages(x.id) }
      return out
    }
    const before = await snap()
    const rxBefore = rx.all.length
    assert.equal(before.sums[B.id].status, 'down')
    assert.equal(before.sums[D.id].status, 'degraded')
    assert.equal(before.sums[C.id].status, 'paused')
    assert.equal(before.sums[P.id].status, 'up')
    assert.ok(before.sums[B.id].ack, 'B is acknowledged before the restart')

    // ── restart (hard kill on Windows, SIGTERM elsewhere) ──
    await inst.restart({ graceful: true })
    const after = await snap()

    for (const x of all) {
      const b = before.sums[x.id], a = after.sums[x.id]
      assert.ok(a, `${x.name} still exists after the restart`)
      assert.equal(a.status, b.status, `${x.name}: status after restart (was ${b.status}); reason="${a.reason}"`)
      assert.equal(a.since, b.since, `${x.name}: "since" of the current status`)
      assert.equal(a.type, b.type); assert.equal(a.target, b.target); assert.equal(a.intervalS, b.intervalS)
      for (const k of ['uptime24h', 'uptime7d', 'uptime30d', 'uptime90d']) {
        if (b[k] === null) assert.equal(a[k], null, `${x.name} ${k}`)
        else near(a[k], b[k], 0.01, `${x.name} ${k} after restart`)
      }
      // history
      const bb = before.beats[x.id], ab = after.beats[x.id]
      assert.ok(ab.length >= bb.length, `${x.name}: heartbeats kept (${bb.length} before, ${ab.length} after)`)
      assert.deepEqual(ab.slice(0, bb.length).map(h => [h.ts, h.status]), bb.map(h => [h.ts, h.status]), `${x.name}: the same beats, in the same order`)
      assert.equal(a.lastCheckAt >= b.lastCheckAt, true, `${x.name}: lastCheckAt did not go backwards`)
    }
    // the open outage and its ack/note survive, and it is the same outage (no second one)
    const oa = after.outages[B.id]
    assert.equal(oa.length, 1, `exactly one outage for B after the restart:\n${outageTable(oa)}`)
    assert.equal(oa[0].id, outB.id)
    assert.equal(oa[0].endedAt, null, 'the outage is still open')
    assert.equal(oa[0].startedAt, outB.startedAt)
    assert.equal(oa[0].ackedBy, 'Monitoring Admin')
    assert.equal(oa[0].note, 'known, restarting the box')
    assert.equal(after.sums[B.id].outageId, outB.id)
    assert.equal(after.sums[B.id].ack?.by, 'Monitoring Admin', 'acknowledgement survives')
    assert.equal(after.alertLog >= before.alertLog, true, 'the notification log survives')
    assert.equal((await inst.ok('GET', '/api/outages?active=true&limit=200')).outages.filter(o => o.monitorId === B.id).length, 1)

    // ── 30 s of normal life after the restart: checks resume, nothing spurious is sent ──
    const nA = after.beats[A.id].length
    await holds('no alert may follow a restart for monitors whose situation did not change', 30000, async () => {
      if (rx.all.length > rxBefore) return `new alert(s): ${rx.all.slice(rxBefore).map(m => m.title).join(' | ')}`
      return null
    }, 500)
    assert.ok((await inst.beats(A.id)).length >= nA + 2, 'checks resumed on schedule after the restart (A got >= 2 new beats in 30 s)')
    assert.equal((await inst.mon(B.id)).status, 'down')
    assert.equal((await inst.mon(D.id)).status, 'degraded')
    assert.equal((await inst.mon(C.id)).status, 'paused')
    assert.equal((await inst.mon(P.id)).status, 'up', 'push monitor: its last push time survived, so it is not declared silent')
    assert.equal(C.fx.hits.length > 0, true)
    const cHits = C.fx.hits.length
    await sleep(100)
    assert.equal(C.fx.hits.length, cHits)
    assert.equal(rx.all.length, rxBefore, 'still nothing new from the notification channel')

    // ── alerting still works after the restart (channel config is decrypted with the persisted key) ──
    A.fx.fail(500)
    await until('A DOWN after restart', async () => (await inst.mon(A.id))?.status === 'down', { timeout: 30000, diag })
    await until('A DOWN alert', async () => rx.kinds(key, A.name).join() === 'down', { timeout: 15000, diag })
    // B recovers: ONE recovery alert whose duration spans the restart
    B.fx.up()
    await until('B up', async () => (await inst.mon(B.id))?.status === 'up', { timeout: 30000, diag })
    await until('B recovery alert', async () => rx.kinds(key, B.name).join() === 'down,recovered', { timeout: 15000, diag })
    const outs = await inst.outages(B.id)
    assert.equal(outs.length, 1)
    assert.ok(outs[0].endedAt > outs[0].startedAt)
    assert.ok(downMatches(rx.got(key, B.name)[1].title, outs[0].endedAt - outs[0].startedAt), 'recovery duration counts from before the restart')
    // D speeds up: the "degraded" alert was sent before the restart, so the all-clear must follow now
    D.fx.up()
    await until('D up', async () => (await inst.mon(D.id))?.status === 'up', { timeout: 30000, diag })
    await until('D all-clear alert (the degraded alert went out before the restart)', async () => rx.kinds(key, D.name).join() === 'degraded,degraded_cleared', { timeout: 15000, diag })
    // resume the paused monitor: it checks again
    await inst.ok('POST', `/api/monitors/${C.id}/resume`)
    await until('C checked after resume', async () => C.fx.hits.length > cHits, { timeout: 30000, diag })
  })

  // ═══════════════════════════════════════════════════════════════════════
  // The wall clock of an instant in a zone, and that zone's local midnight (epoch ms), via Intl only.
  const wall = (ms, tz) => {
    const p = Object.fromEntries(new Intl.DateTimeFormat('en-US', { timeZone: tz, hourCycle: 'h23', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' }).formatToParts(new Date(ms)).map(x => [x.type, x.value]))
    return { y: +p.year, m: +p.month, d: +p.day, h: +p.hour, mi: +p.minute, s: +p.second, asUTC: Date.UTC(+p.year, +p.month - 1, +p.day, +p.hour, +p.minute, +p.second) }
  }
  const localMidnight = (ms, tz) => {
    const w = wall(ms, tz)
    let guess = Date.UTC(w.y, w.m - 1, w.d)
    for (let i = 0; i < 3; i++) guess = Date.UTC(w.y, w.m - 1, w.d) - (wall(guess, tz).asUTC - guess)
    return guess
  }

  for (const tz of ['Asia/Kolkata', 'America/Los_Angeles', 'Pacific/Kiritimati']) {
    it(`18. time zone ${tz}: timestamps are UTC epoch ms, daily buckets are ${tz} days, hourly buckets are UTC hours`, { timeout: 120000 }, async t => {
      if (!online) return t.skip(OFFLINE_REASON)
      const inst = await Instance.start({ env: { DISPLAY_TZ: tz } })
      t.after(() => inst.stop())
      const fx = await Fixture.start(); t.after(() => fx.stop())
      const rx = await Receiver.start(); t.after(() => rx.stop())
      const ch = await inst.channel(rx, 'tz')
      const mon = await inst.create({ name: uniq('s18-svc'), target: fx.target, channelIds: [ch.id], retries: 0, intervalS: 3600, retryIntervalS: 3600, timeoutMs: 3000 })
      const t0 = Date.now()
      await inst.checkNow(mon.id)            // up
      fx.fail(500)
      await inst.checkNow(mon.id)            // down -> outage opens
      await sleep(1100)
      fx.up()
      await inst.checkNow(mon.id)            // up -> outage closes
      const t1 = Date.now()
      const beats = await until('3 beats stored', async () => { const b = await inst.beats(mon.id); return b.length >= 3 ? b : null }, { timeout: 10000 })
      const outs = await until('closed outage stored', async () => { const o = await inst.outages(mon.id); return o.length && o[0].endedAt ? o : null }, { timeout: 10000 })
      assert.equal(seq(beats), 'up > down > up')

      // 1. every stored timestamp is a real UTC epoch (a zone offset of hours would show up immediately)
      for (const b of beats) assert.ok(b.ts >= t0 - 2000 && b.ts <= t1 + 2000, `beat ts ${b.ts} (${new Date(b.ts).toISOString()}) must be the real wall clock between ${t0} and ${t1} regardless of DISPLAY_TZ=${tz}`)
      assert.ok(outs[0].startedAt >= t0 && outs[0].endedAt <= t1 + 2000, `outage ${outs[0].startedAt}..${outs[0].endedAt} within ${t0}..${t1}`)
      assert.equal(outs[0].startedAt, beats[1].ts)
      assert.equal(outs[0].endedAt, beats[2].ts)
      const ev = (await inst.ok('GET', `/api/events?monitorId=${mon.id}`)).events
      assert.ok(ev.length >= 2 && ev.every(e => e.ts >= t0 - 2000 && e.ts <= t1 + 2000), `status event timestamps are UTC epoch ms: ${fmt(ev.map(e => new Date(e.ts).toISOString()))}`)

      // 2. the daily bucket is the zone's local midnight
      const want = new Set([localMidnight(t0, tz), localMidnight(t1, tz)])
      const daily = (await inst.ok('GET', `/api/monitors/${mon.id}/series?range=90d`)).series
      assert.ok(daily.length >= 1 && daily.length <= 2, `one (or two, across local midnight) daily rollup rows, got ${daily.length}`)
      for (const r of daily) {
        assert.ok(want.has(r.Bucket), `daily bucket ${r.Bucket} (${new Date(r.Bucket).toISOString()}) must be local midnight in ${tz}: ${[...want].map(x => new Date(x).toISOString())} (UTC midnight would be ${new Date(Date.UTC(...Object.values((({ y, m, d }) => ({ y, m: m - 1, d }))(wall(t1, 'UTC'))))).toISOString()})`)
      }
      assert.equal(daily.reduce((s, r) => s + r.Up + r.Down, 0), 3)
      const dayList = await inst.ok('GET', `/api/monitors/${mon.id}/daily?days=3`)
      assert.ok(dayList.every(d => want.has(d.day)), `/daily day keys are local midnights: ${fmt(dayList)}`)
      assert.equal(dayList.reduce((s, d) => s + d.outages, 0), 1, 'the outage is attributed to exactly one local day')
      // 3. hourly rollups are UTC hours, whatever the zone offset (30-minute offsets included)
      const hourly = (await inst.ok('GET', `/api/monitors/${mon.id}/series?range=7d`)).series
      for (const r of hourly) assert.equal(r.Bucket % 3600000, 0, `hourly bucket ${new Date(r.Bucket).toISOString()} must sit on a UTC hour`)
      assert.ok(hourly.every(r => r.Bucket <= t1 && r.Bucket > t0 - 3600000))
      // 4. windows count the checks regardless of the zone
      const m = await inst.fresh(mon.id)
      for (const k of ['uptime24h', 'uptime7d', 'uptime30d', 'uptime90d']) near(m[k], (2 / 3) * 100, 0.001, `${k} (2 good, 1 bad)`)
      // 5. the alert's "Time:" line is shown in the display zone
      const downAlert = await until('the DOWN alert', async () => rx.got('tz').find(m => m.kind === 'down'), { timeout: 12000 })
      const tm = /Time: (\d{4})-(\d{2})-(\d{2}) (\d{2}):(\d{2}):(\d{2}) (\S+)/.exec(downAlert.message)
      assert.ok(tm, `the alert has a Time line: ${downAlert.message}`)
      const shown = Date.UTC(+tm[1], +tm[2] - 1, +tm[3], +tm[4], +tm[5], +tm[6])
      near(shown, wall(downAlert.t, tz).asUTC, 8000, `the alert's Time line (${tm[0]}) must be the wall clock in ${tz} when it was sent`)
      assert.notEqual(tm[7], 'UTC', `zone label in the alert for ${tz}`)
      // 6. the monthly report uses the zone's month and sees the checks
      const month = `${String(wall(t1, tz).y).padStart(4, '0')}-${String(wall(t1, tz).m).padStart(2, '0')}`
      const sla = await inst.ok('GET', `/api/reports/sla?month=${month}&scope=monitor&id=${mon.id}`)
      assert.equal(sla.rows.length, 1)
      assert.equal(sla.rows[0].checks, 3, `SLA report for ${month} counts the 3 checks`)
      assert.equal(sla.rows[0].outages, 1)
      assert.ok(sla.from <= t0 && sla.to > t1, 'the report month spans the check times')
    })
  }
})
