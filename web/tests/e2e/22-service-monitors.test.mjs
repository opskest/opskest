// Every protocol monitor type can be created and, checked once against a local fake service, shows the right
// state: healthy fake = UP, a server speaking another protocol = DOWN (assertion, and never with the server's
// reply in the reason), a closed port = DOWN (refused). See 20-fakes.mjs for the fakes. Types not covered here:
// http / push / websocket-less variants are exercised in 20-monitoring-correctness; postgres_sql and mysql_sql
// need a real database server (internal/check/sqlmon_integration_test.go covers them).
//
//   node --experimental-strip-types --test tests/e2e/22-service-monitors.test.mjs

import assert from 'node:assert/strict'
import { existsSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { after, before, describe, it } from 'node:test'
import { Instance, OFFLINE_REASON, guardOnline, uniq, until } from './20-kit.mjs'
import { closedPort, fakes, grpcServer, makeCert, tlsServer, udpFakes, wsServer } from './20-fakes.mjs'
import { goMonitorTypes } from './util.mjs'

const SKIP = process.env.OPSKEST_E2E_BIN
  ? (existsSync(process.env.OPSKEST_E2E_BIN) ? false : 'OPSKEST_E2E_BIN does not exist')
  : (spawnSync('go', ['version'], { stdio: 'ignore' }).status === 0 ? false : 'Go toolchain not found and OPSKEST_E2E_BIN not set')

describe('service monitor types against local fakes', { skip: SKIP, concurrency: true }, () => {
  /** @type {Instance} */ let inst
  let online = true
  before(async () => { online = await guardOnline(); inst = await Instance.start() }, { timeout: 180000 })
  after(async () => { await inst?.stop().catch(() => {}) }, { timeout: 60000 })

  /** Like it(), but skipped when the host is offline (a failed check is then UNKNOWN by design, never DOWN). */
  const svc = (name, opts, fn) => it(name, opts, async t => { if (!online) return t.skip(OFFLINE_REASON); return fn(t) })

  /** Creates a monitor against `target`, runs one check, returns the resulting summary. */
  const probe = async (type, target, settings = {}, over = {}) => {
    const mon = await inst.create({ name: uniq(`svc-${type}`), type, target, retries: 0, intervalS: 3600, retryIntervalS: 3600, timeoutMs: 3000, ...over, settings })
    const m = await inst.checkNow(mon.id)
    return { mon, m }
  }
  const expectUp = (label, { m }) => {
    assert.equal(m.status, 'up', `${label}: expected UP, got ${m.status} (errClass=${m.errClass}, reason="${m.reason}")`)
    assert.equal(m.errClass, '', `${label}: errClass must be empty when up`)
    assert.ok(m.latencyMs >= 0, `${label}: a latency is recorded (${m.latencyMs})`)
  }
  const expectDown = (label, { m }, errClass, reasonRe) => {
    assert.equal(m.status, 'down', `${label}: expected DOWN, got ${m.status} (reason="${m.reason}")`)
    if (errClass) assert.equal(m.errClass, errClass, `${label}: errClass (reason="${m.reason}")`)
    if (reasonRe) assert.match(m.reason, reasonRe, `${label}: reason`)
    assert.ok(!/GARBAGE|NOT-THIS-PROTOCOL/.test(m.reason), `${label}: the server's reply must never appear in the failure reason: "${m.reason}"`)
  }
  const using = async (t, ...servers) => { t.after(() => servers.forEach(s => s.close())); return servers }

  it('every monitor type of internal/model/monitor.go is accounted for', () => {
    const covered = new Set(['http', 'tcp', 'ping', 'dns', 'push', 'websocket', 'tls', 'smtp', 'ssh', 'redis', 'postgres', 'mysql', 'mongodb', 'mqtt', 'ntp', 'udp', 'amqp', 'memcached', 'banner', 'grpc'])
    const notHere = new Set(['postgres_sql', 'mysql_sql', 'group', 'manual']) // group and manual run no network check (see internal/engine/group_test.go)
    const all = goMonitorTypes()
    assert.deepEqual(all.filter(x => !covered.has(x) && !notHere.has(x)), [], 'a monitor type was added without a scenario')
  })

  // ── plain TCP-speaking services: good, wrong protocol, closed port ─────
  const tcpCases = [
    ['tcp', fakes.tcp, {}, null],
    ['ssh', fakes.ssh, {}, /not an SSH server/],
    ['smtp', fakes.smtp, { security: 'none' }, /SMTP/],
    ['redis', fakes.redis, {}, /unexpected reply to PING/],
    ['memcached', fakes.memcached, {}, /not a memcached server/],
    ['banner', fakes.banner, { expect: '220' }, /did not contain the expected text/],
    ['postgres', fakes.postgres, {}, /not a PostgreSQL server/],
    ['mysql', fakes.mysql, {}, /not a MySQL or MariaDB server/],
    ['mongodb', fakes.mongodb, {}, /not a MongoDB server/],
    ['mqtt', () => fakes.mqtt(0), {}, /not an MQTT broker/],
    ['amqp', fakes.amqp, {}, /not an AMQP 0-9-1 broker/],
  ]
  for (const [type, makeGood, settings, badRe] of tcpCases) {
    svc(`${type}: UP against a healthy fake${badRe ? ', DOWN (assertion) against another protocol' : ''}, DOWN (refused) on a closed port`, { timeout: 60000 }, async t => {
      const [good, bad, closed] = await using(t, await makeGood(), await fakes.garbage(), await closedPort())
      expectUp(`${type} good`, await probe(type, good.addr, settings))
      expectDown(`${type} closed port`, await probe(type, closed.addr, settings), 'refused', /connection refused/)
      if (badRe) expectDown(`${type} wrong protocol`, await probe(type, bad.addr, settings), 'assertion', badRe)
    })
  }

  svc('mqtt: a broker that refuses the login is DOWN with the broker\'s reason', { timeout: 60000 }, async t => {
    const [refusing] = await using(t, await fakes.mqtt(5))
    expectDown('mqtt not authorized', await probe('mqtt', refusing.addr, {}), 'assertion', /broker refused the connection: not authorized/)
  })

  svc('banner: a greeting without the expected text is DOWN and never echoes the greeting', { timeout: 60000 }, async t => {
    const [good] = await using(t, await fakes.banner())
    expectDown('banner mismatch', await probe('banner', good.addr, { expect: 'SSH-2.0' }), 'assertion', /did not contain the expected text/)
    expectUp('banner match', await probe('banner', good.addr, { expect: 'FTP server ready' }))
  })

  // ── UDP services ─────────────────────────────────────────────────────
  svc('udp: UP when the reply contains the expected text, DOWN when it does not or when nothing answers', { timeout: 60000 }, async t => {
    const [good, bad] = await using(t, await udpFakes.udp('pong'), await udpFakes.garbage())
    expectUp('udp good', await probe('udp', good.addr, { send: 'ping', expect: 'pong' }))
    expectDown('udp wrong reply', await probe('udp', bad.addr, { send: 'ping', expect: 'pong' }), 'assertion', /did not contain the expected text/)
    const closed = await closedPort()
    expectDown('udp no listener', await probe('udp', closed.addr, { send: 'ping', expect: 'pong' }, { timeoutMs: 1500 }))
  })

  svc('ntp: UP for a synchronised server, DOWN for stratum 0 and for a clock offset beyond maxOffsetMs', { timeout: 60000 }, async t => {
    const [good, unsynced, skewed] = await using(t, await udpFakes.ntp(), await udpFakes.ntp({ stratum: 0 }), await udpFakes.ntp({ offsetMs: 60000 }))
    expectUp('ntp good', await probe('ntp', good.addr, {}))
    expectDown('ntp stratum 0', await probe('ntp', unsynced.addr, {}), 'assertion', /unsynchronised/)
    expectUp('ntp skewed but no limit', await probe('ntp', skewed.addr, {}))
    expectDown('ntp skewed with maxOffsetMs', await probe('ntp', skewed.addr, { maxOffsetMs: 1000 }), 'assertion', /clock is -?\d+ ms off the server \(limit 1000 ms\)/)
    expectUp('ntp within limit', await probe('ntp', good.addr, { maxOffsetMs: 5000 }))
  })

  svc('dns: UP when the answer matches, DOWN on a different answer and on NXDOMAIN, against a local resolver', { timeout: 60000 }, async t => {
    const [resolver] = await using(t, await udpFakes.dns({ name: 'example.test', ip: '203.0.113.7' }))
    const common = { record: 'A', resolver: resolver.addr }
    expectUp('dns match', await probe('dns', 'example.test', { ...common, expectedValues: ['203.0.113.7'], match: 'equals' }))
    expectUp('dns no expectation (any record)', await probe('dns', 'example.test', common))
    expectDown('dns different answer', await probe('dns', 'example.test', { ...common, expectedValues: ['198.51.100.1'], match: 'equals' }), 'assertion', /203\.0\.113\.7.*expected 198\.51\.100\.1/)
    const nx = await probe('dns', 'missing.test', common)
    expectDown('dns NXDOMAIN', nx, 'dns', /NXDOMAIN/)
    const nxBeats = await until('the NXDOMAIN beat to be stored', async () => { const b = await inst.beats(nx.mon.id); return b.length ? b : null }, { timeout: 8000 })
    assert.equal(nxBeats[0].code, 3, 'the DNS rcode is stored as the code')
    const closed = await closedPort()
    expectDown('dns resolver unreachable', await probe('dns', 'example.test', { record: 'A', resolver: closed.addr }, { timeoutMs: 2000 }))
  })

  svc('websocket: UP when the handshake works (and the first reply contains `expect`), DOWN otherwise', { timeout: 60000 }, async t => {
    const [ws] = await using(t, await wsServer({ greeting: 'welcome to the fake' }))
    const closed = await closedPort()
    const url = `ws://${ws.addr}/`
    expectUp('ws handshake only', await probe('websocket', url, {}))
    expectUp('ws expect', await probe('websocket', url, { expect: 'welcome' }))
    expectDown('ws wrong reply', await probe('websocket', url, { expect: 'goodbye' }), 'assertion', /reply did not contain "goodbye"/)
    expectDown('ws closed', await probe('websocket', `ws://${closed.addr}/`, {}), 'refused')
  })

  svc('grpc: UP when the health service reports SERVING, DOWN for NOT_SERVING, unimplemented and a closed port', { timeout: 60000 }, async t => {
    const [good, notServing, unimpl] = await using(t, await grpcServer(), await grpcServer({ servingStatus: 2 }), await grpcServer({ grpcStatus: 12 }))
    const closed = await closedPort()
    expectUp('grpc serving', await probe('grpc', good.addr, {}))
    expectDown('grpc not serving', await probe('grpc', notServing.addr, {}), 'assertion', /not serving/)
    expectDown('grpc unimplemented', await probe('grpc', unimpl.addr, {}), 'assertion', /does not implement the gRPC health service/)
    expectDown('grpc closed', await probe('grpc', closed.addr, {}), 'refused')
  })

  svc('ping: UP for 127.0.0.1, DOWN for an unreachable documentation address (skips only if the OS refuses unprivileged ICMP)', { timeout: 60000 }, async t => {
    const up = await probe('ping', '127.0.0.1', { count: 1, maxLossPct: 50 }, { timeoutMs: 4000 })
    if (up.m.status !== 'up' && /permission|not permitted|access|privilege|not supported|not implemented|socket/i.test(up.m.reason)) {
      t.skip(`ICMP echo is not available to an unprivileged process on this OS: "${up.m.reason}" (class ${up.m.errClass}); the monitor correctly reported DOWN, not UP`)
      assert.equal(up.m.status, 'down')
      return
    }
    expectUp('ping loopback', up)
    const down = await probe('ping', '192.0.2.1', { count: 1, maxLossPct: 50 }, { timeoutMs: 2000 })
    expectDown('ping unreachable', down)
  })

  // ── TLS ──────────────────────────────────────────────────────────────
  const longCert = makeCert({ days: 365 })
  const shortCert = makeCert({ days: 5 })
  const noOpenssl = !longCert || !shortCert
  svc('tls: UP with a valid-long certificate, DEGRADED when it expires within sslWarnDays, DOWN (tls) when untrusted', { skip: noOpenssl && 'the openssl CLI is not available to create test certificates', timeout: 60000 }, async t => {
    const [longS, shortS] = await using(t, await tlsServer(longCert), await tlsServer(shortCert))
    const lg = await probe('tls', longS.addr, { ignoreTls: true })
    expectUp('tls long cert (ignoreTls)', lg)
    assert.ok(lg.m.certDays >= 363 && lg.m.certDays <= 365, `certDays for a 365-day cert: ${lg.m.certDays}`)
    const sh = await probe('tls', shortS.addr, { ignoreTls: true }, { sslWarnDays: 14 })
    assert.equal(sh.m.status, 'degraded', `a certificate expiring in 5 days with sslWarnDays=14 is DEGRADED, got ${sh.m.status} (${sh.m.reason})`)
    assert.match(sh.m.reason, /certificate expires in [45] days/)
    assert.ok(sh.m.certDays >= 3 && sh.m.certDays <= 5, `certDays ${sh.m.certDays}`)
    const off = await probe('tls', shortS.addr, { ignoreTls: true }, { sslWarnDays: 0 })
    expectUp('sslWarnDays=0 switches the warning off', off)
    const strict = await probe('tls', longS.addr, {})
    expectDown('tls self-signed, verification on', strict, 'tls', /tls: /)
    const tcpTls = await probe('tcp', longS.addr, { tls: true, ignoreTls: true })
    expectUp('tcp with tls', tcpTls)
    assert.ok(tcpTls.m.certDays >= 363, 'tcp+tls records the certificate too')
    expectDown('tcp with tls, verification on', await probe('tcp', longS.addr, { tls: true }), 'tls')
  })
})
