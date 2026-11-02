import test from 'node:test'
import assert from 'node:assert/strict'
import {
  outcomeOf, isFailedCheck, classKey, explainClass, failureBreakdown, quantile, jitter, latencySummary,
  pingLoss, cadence, certDaysLeft, certSeverity, describeMonitor, timeReporting, sqlProbeInfo, spanText, typeInfo,
} from '../../web/src/lib/diagnostics.ts'

const T0 = Date.UTC(2026, 9, 26, 12, 0, 0)

// Beats are newest first, like the API returns them.
function beat(i, over = {}) {
  return {
    monitorId: 1, ts: T0 - i * 60_000, status: 'up', latencyMs: 100, code: 0,
    dnsMs: -1, tcpMs: -1, tlsMs: -1, ttfbMs: -1, waitMs: -1, appMs: -1,
    edge: '', conn: '', errClass: '', msg: '', maint: false, ...over,
  }
}

test('outcomeOf separates failing, retrying, degraded, maintenance and not-judged beats', () => {
  assert.equal(outcomeOf(beat(0)), 'passed')
  assert.equal(outcomeOf(beat(0, { status: 'degraded' })), 'degraded')
  assert.equal(outcomeOf(beat(0, { status: 'pending' })), 'retrying')
  assert.equal(outcomeOf(beat(0, { status: 'down' })), 'failed')
  assert.equal(outcomeOf(beat(0, { status: 'unknown' })), 'offline')
  assert.equal(outcomeOf(beat(0, { status: 'down', maint: true })), 'maintenance')
  assert.equal(isFailedCheck(beat(0, { status: 'pending' })), true)
  assert.equal(isFailedCheck(beat(0, { status: 'degraded' })), false)
  assert.equal(isFailedCheck(beat(0, { status: 'down', maint: true })), false)
})

test('classKey maps unknown classes to other and empty to unclassified', () => {
  assert.equal(classKey('timeout'), 'timeout')
  assert.equal(classKey(''), 'unclassified')
  assert.equal(classKey(undefined), 'unclassified')
  assert.equal(classKey('something_new'), 'other')
  for (const c of ['timeout', 'refused', 'dns', 'tls', 'assertion', 'network', 'http_status']) {
    const info = explainClass(c)
    assert.ok(info.meaning.length > 40 && info.next.length >= 2, c)
  }
})

test('failureBreakdown counts classes, keeps the newest reason and excludes maintenance', () => {
  const beats = [
    beat(0, { status: 'down', errClass: 'timeout', msg: 'timeout after 5000 ms' }),
    beat(1, { status: 'down', errClass: 'refused', msg: 'connection refused' }),
    beat(2, { status: 'pending', errClass: 'timeout', msg: 'older timeout' }),
    beat(3, { status: 'pending', errClass: 'timeout', msg: 'oldest timeout' }),
    beat(4, { status: 'degraded', msg: 'slow: 900 ms > 500 ms' }),
    beat(5),
    beat(6, { status: 'down', errClass: 'dns', msg: 'dns: no such host', maint: true }),
    beat(7, { status: 'unknown', msg: 'opskest lost internet connectivity' }),
  ]
  const b = failureBreakdown(beats)
  assert.equal(b.judged, 6)
  assert.equal(b.failed, 4)
  assert.equal(b.degraded, 1)
  assert.equal(b.excludedMaintenance, 1)
  assert.equal(b.excludedOffline, 1)
  assert.deepEqual(b.groups.map(g => [g.key, g.count]), [['timeout', 3], ['refused', 1]])
  assert.equal(b.groups[0].latestReason, 'timeout after 5000 ms')
  assert.equal(b.groups[0].share, 75)
  assert.equal(b.from, T0 - 7 * 60_000)
  assert.equal(b.to, T0)
})

test('failureBreakdown with no failures has no groups; empty list is safe', () => {
  assert.equal(failureBreakdown([beat(0), beat(1)]).groups.length, 0)
  const e = failureBreakdown([])
  assert.equal(e.judged, 0)
  assert.equal(e.failed, 0)
  assert.equal(e.from, 0)
})

test('quantile uses nearest rank like the server', () => {
  const v = Array.from({ length: 100 }, (_, i) => i + 1)
  assert.equal(quantile(v, 0.5), 50)
  assert.equal(quantile(v, 0.95), 95)
  assert.equal(quantile(v, 0.99), 99)
  assert.equal(quantile([7], 0.99), 7)
  assert.equal(quantile([], 0.5), 0)
})

test('jitter is the mean absolute consecutive difference and breaks at gaps', () => {
  assert.equal(jitter([10, 20, 15]), 7.5) // |10| and |5|
  assert.equal(jitter([10]), null)
  assert.equal(jitter([]), null)
  assert.equal(jitter([10, null, 50]), null) // a failure between: no comparison
  assert.equal(jitter([10, 14, null, 50, 56]), 5) // (4 + 6) / 2
  assert.equal(jitter([5, 5, 5]), 0)
})

test('latencySummary works from real latencies only, ignoring failures and maintenance', () => {
  const beats = [
    beat(0, { latencyMs: 30 }), // newest
    beat(1, { status: 'down', latencyMs: -1, errClass: 'timeout' }),
    beat(2, { latencyMs: 10 }),
    beat(3, { latencyMs: 20 }),
    beat(4, { latencyMs: 9999, maint: true }),
  ]
  const s = latencySummary(beats)
  assert.equal(s.count, 3)
  assert.equal(s.min, 10)
  assert.equal(s.max, 30)
  assert.equal(s.avg, 20)
  assert.equal(s.p50, 20)
  assert.equal(s.jitter, 10) // chronological 20, 10, then a failure breaks the chain: only |20-10|
  assert.equal(latencySummary([beat(0, { latencyMs: -1, status: 'down' })]), null)
  assert.equal(latencySummary([]), null)
})

test('pingLoss reads loss only from reasons that carry it', () => {
  const beats = [
    beat(0), // passed: loss unknown (below the limit or none)
    beat(1, { status: 'degraded', msg: 'packet loss 67% > 34%' }),
    beat(2, { status: 'down', errClass: 'timeout', msg: '100% packet loss' }),
    beat(3, { status: 'degraded', msg: 'packet loss 50% > 34%' }),
    beat(4, { status: 'unknown', msg: 'opskest lost internet connectivity' }),
  ]
  const l = pingLoss(beats)
  assert.equal(l.checks, 4)
  assert.equal(l.totalLoss, 1)
  assert.equal(l.aboveLimit, 2)
  assert.equal(l.worst, 100)
  assert.equal(l.latest, 67)
  const none = pingLoss([beat(0), beat(1)])
  assert.equal(none.worst, null)
  assert.equal(none.latest, null)
})

test('cadence flags late and missing checks from timestamps', () => {
  const cfg = { type: 'http', intervalS: 60, retryIntervalS: 20, timeoutMs: 10_000 }
  // chronological gaps: 60s, 60s, 85s (late: > 60 + max(10, 15)), 400s (missing), 61s
  const ts = [0, 60_000, 120_000, 205_000, 605_000, 666_000]
  const beats = ts.map(t => beat(0, { ts: T0 + t })).reverse()
  const c = cadence(beats, cfg)
  assert.equal(c.gaps, 5)
  assert.equal(c.late.length, 1)
  assert.equal(c.late[0].gapMs, 85_000)
  assert.equal(c.missing.length, 1)
  assert.equal(c.missing[0].gapMs, 400_000)
  assert.equal(c.onTimePct, 60)
  assert.equal(c.medianMs, 61_000)
  assert.equal(c.maxMs, 400_000)
})

test('cadence expects the retry interval after a pending check', () => {
  const cfg = { type: 'tcp', intervalS: 300, retryIntervalS: 20, timeoutMs: 5_000 }
  // up, then a failed check 300s later, retried after 20s and 21s
  const beats = [
    beat(0, { ts: T0 + 341_000 }),
    beat(0, { ts: T0 + 320_000, status: 'pending', errClass: 'refused' }),
    beat(0, { ts: T0 + 300_000, status: 'pending', errClass: 'refused' }),
    beat(0, { ts: T0 }),
  ]
  const c = cadence(beats, cfg)
  assert.equal(c.late.length, 0)
  assert.equal(c.missing.length, 0)
  assert.equal(c.onTimePct, 100)
})

test('cadence: a retry that overshoots is late, a silent stretch is missing, and tiny inputs are safe', () => {
  const cfg = { type: 'tcp', intervalS: 300, retryIntervalS: 20, timeoutMs: 5_000 }
  const late = cadence([beat(0, { ts: T0 + 40_000 }), beat(0, { ts: T0, status: 'pending' })], cfg)
  assert.equal(late.late.length, 1) // 40s after a pending check that should retry in 20s (+5s)
  assert.equal(late.missing.length, 0)
  const lost = cadence([beat(0, { ts: T0 + 120_000 }), beat(0, { ts: T0, status: 'pending' })], cfg)
  assert.equal(lost.missing.length, 1) // beyond three retry intervals plus the timeout
  const silent = cadence([beat(0, { ts: T0 + 2_000_000 }), beat(0, { ts: T0 })], cfg)
  assert.equal(silent.missing.length, 1)
  assert.equal(cadence([], cfg).gaps, 0)
  assert.equal(cadence([beat(0)], cfg).onTimePct, null)
})

test('cadence for push monitors tolerates the grace period', () => {
  const cfg = { type: 'push', intervalS: 60, retryIntervalS: 20, timeoutMs: 10_000, graceS: 30 }
  const ok = cadence([beat(0, { ts: T0 + 85_000 }), beat(0, { ts: T0 })], cfg)
  assert.equal(ok.late.length, 0)
  const late = cadence([beat(0, { ts: T0 + 100_000 }), beat(0, { ts: T0 })], cfg)
  assert.equal(late.late.length, 1)
})

test('certDaysLeft floors and certSeverity follows sslWarnDays', () => {
  const now = Date.UTC(2026, 9, 26, 12)
  assert.equal(certDaysLeft(now + 30 * 86_400_000 + 5, now), 30)
  assert.equal(certDaysLeft(now + 86_400_000 - 1, now), 0)
  assert.equal(certDaysLeft(now - 1, now), -1)
  assert.equal(certSeverity(-1, 14), 'expired')
  assert.equal(certSeverity(0, 14), 'critical')
  assert.equal(certSeverity(7, 14), 'critical')
  assert.equal(certSeverity(8, 14), 'warning')
  assert.equal(certSeverity(14, 14), 'warning')
  assert.equal(certSeverity(15, 14), 'ok')
  assert.equal(certSeverity(3, 3), 'critical') // short warn window: all critical
  assert.equal(certSeverity(10, 30), 'warning')
  assert.equal(certSeverity(2, 0), 'ok') // warnings off
  assert.equal(certSeverity(-5, 0), 'expired')
})

test('timeReporting distinguishes phases, a single duration and nothing', () => {
  assert.equal(timeReporting([beat(0, { dnsMs: 3, tcpMs: 5, ttfbMs: 40 })]), 'phases')
  assert.equal(timeReporting([beat(0)]), 'single')
  assert.equal(timeReporting([beat(0, { latencyMs: -1, status: 'down' })]), 'none')
  assert.equal(timeReporting([]), 'none')
})

test('describeMonitor never exposes secrets and covers every type', () => {
  const base = { intervalS: 60, retryIntervalS: 20, retries: 2, timeoutMs: 10000, degradedMs: 0, sslWarnDays: 14, upsideDown: false, paused: false }
  const types = ['http', 'tcp', 'ping', 'dns', 'push', 'websocket', 'tls', 'smtp', 'ssh', 'redis', 'postgres', 'mysql', 'mongodb', 'mqtt', 'ntp', 'udp', 'amqp', 'memcached', 'banner', 'grpc', 'postgres_sql', 'mysql_sql']
  for (const type of types) {
    const lines = describeMonitor({ ...base, type, probe: 'deadlocks', threshold: 0 }, 'db.example.com:5432')
    assert.ok(lines.length >= (type === 'push' ? 1 : 2) && lines[0].length > 20, type)
    assert.ok(typeInfo(type).label && typeInfo(type).timeCovers, type)
  }
  const http = describeMonitor({ ...base, type: 'http', method: 'GET', acceptedCodes: '200-299', hasKeyword: true, keywordMode: 'contains', ignoreTls: true }, 'https://x.test')
  assert.match(http.join(' '), /GET request to https:\/\/x\.test every 1 min/)
  assert.match(http.join(' '), /must contain a configured keyword/)
  assert.match(http.join(' '), /ignored/)
  const dns = describeMonitor({ ...base, type: 'dns', record: 'A', expectedValues: ['1.2.3.4'], match: 'exact', resolver: '1.1.1.1' }, 'a.test')
  assert.match(dns.join(' '), /1\.1\.1\.1/)
  assert.match(dns.join(' '), /exactly the expected values/)
  assert.match(describeMonitor({ ...base, type: 'tcp', retries: 0 }, 'h:1').join(' '), /first failed check/)
})

test('sqlProbeInfo describes each probe and rejects unknown ones', () => {
  assert.match(sqlProbeInfo({ probe: 'deadlocks', threshold: 0 }).downWhen, /More than 0 new deadlocks/)
  assert.match(sqlProbeInfo({ probe: 'long_running', threshold: 1, thresholdSecs: 30 }).measures, /30 s/)
  assert.match(sqlProbeInfo({ probe: 'connections', threshold: 80 }).downWhen, /80%/)
  assert.match(sqlProbeInfo({ probe: 'query', queryOp: 'gt' }).downWhen, /greater than/)
  assert.equal(sqlProbeInfo({ probe: 'nope' }), null)
})

test('spanText formats durations without trailing zeros', () => {
  assert.equal(spanText(230), '230 ms')
  assert.equal(spanText(1400), '1.4 s')
  assert.equal(spanText(9_999), '10 s')
  assert.equal(spanText(10_004), '10 s')
  assert.equal(spanText(59_600), '1 min')
  assert.equal(spanText(60_000), '1 min')
  assert.equal(spanText(75_000), '1 min 15 s')
  assert.equal(spanText(3_600_000), '1 h')
  assert.equal(spanText(5_400_000), '1 h 30 min')
  assert.equal(spanText(-1), '—')
})

test('explainClass words push, ping and dns failures for what they are', () => {
  assert.match(explainClass('timeout', 'push').meaning, /never connects/)
  assert.match(explainClass('timeout', 'ping').meaning, /echo reply/)
  assert.match(explainClass('assertion', 'dns').meaning, /answers differ/)
  assert.equal(explainClass('timeout', 'http').key, 'timeout')
  assert.match(explainClass('timeout', 'http').meaning, /did not answer within/)
  assert.equal(explainClass('', 'push').key, 'unclassified')
})
