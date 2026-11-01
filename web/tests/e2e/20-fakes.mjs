// Protocol fakes for the service-monitor scenarios (22-service-monitors.test.mjs). Each one speaks just enough of
// its protocol for the matching checker in internal/check/services*.go to call it healthy; every kind also has a
// "garbage" variant that answers with something that is not that protocol, and `closedPort()` gives a port
// where nothing listens. Pure node:* modules, no dependencies.

import { spawnSync } from 'node:child_process'
import crypto from 'node:crypto'
import dgram from 'node:dgram'
import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import http from 'node:http'
import http2 from 'node:http2'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'
import tls from 'node:tls'

/** A TCP server that hands every connection to handler(socket). */
export async function tcpServer(handler, { tlsOptions } = {}) {
  const sockets = new Set()
  const onConn = sock => {
    sockets.add(sock)
    sock.on('close', () => sockets.delete(sock))
    sock.on('error', () => {})
    handler(sock)
  }
  const server = tlsOptions ? tls.createServer(tlsOptions, onConn) : net.createServer(onConn)
  server.on('tlsClientError', () => {})
  await new Promise(r => server.listen(0, '127.0.0.1', r))
  return { port: server.address().port, addr: `127.0.0.1:${server.address().port}`, close: () => { for (const s of sockets) s.destroy(); server.close() } }
}

/** A port nothing listens on (bound and released). */
export async function closedPort() {
  const s = net.createServer()
  await new Promise(r => s.listen(0, '127.0.0.1', r))
  const port = s.address().port
  await new Promise(r => s.close(r))
  return { port, addr: `127.0.0.1:${port}`, close() {} }
}

export async function udpServer(handler) {
  const sock = dgram.createSocket('udp4')
  sock.on('message', (msg, rinfo) => handler(msg, (data) => sock.send(data, rinfo.port, rinfo.address)))
  await new Promise(r => sock.bind(0, '127.0.0.1', r))
  return { port: sock.address().port, addr: `127.0.0.1:${sock.address().port}`, close: () => { try { sock.close() } catch { /* closed */ } } }
}

const garbage = sock => { sock.write('GARBAGE-NOT-THIS-PROTOCOL\r\n'); setTimeout(() => sock.destroy(), 200) }

// ── TCP-speaking services ────────────────────────────────────────────────

export const fakes = {
  tcp: () => tcpServer(sock => { setTimeout(() => sock.destroy(), 1500) }),
  ssh: () => tcpServer(sock => { sock.write('SSH-2.0-OpenSSH_9.6 opskest-fake\r\n'); setTimeout(() => sock.destroy(), 800) }),
  smtp: () => tcpServer(sock => {
    let buf = ''
    sock.write('220 mail.example.test ESMTP\r\n')
    sock.on('data', d => {
      buf += d
      let i
      while ((i = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, i).trim().toUpperCase(); buf = buf.slice(i + 1)
        if (line.startsWith('EHLO')) sock.write('250-mail.example.test\r\n250 8BITMIME\r\n')
        else if (line.startsWith('QUIT')) { sock.write('221 bye\r\n'); sock.end() }
        else sock.write('502 no\r\n')
      }
    })
  }),
  redis: () => tcpServer(sock => {
    sock.on('data', d => { if (/PING/i.test(d.toString())) sock.write('+PONG\r\n'); else sock.write('-ERR unknown\r\n') })
  }),
  memcached: () => tcpServer(sock => {
    sock.on('data', d => { if (/^version/i.test(d.toString())) sock.write('VERSION 1.6.21\r\n'); else sock.write('ERROR\r\n') })
  }),
  banner: () => tcpServer(sock => { sock.write('220 ftp.example.test FTP server ready\r\n'); setTimeout(() => sock.destroy(), 800) }),
  postgres: () => tcpServer(sock => { sock.once('data', () => { sock.write('N'); sock.end() }) }),
  mysql: () => tcpServer(sock => {
    const payload = Buffer.concat([Buffer.from([0x0a]), Buffer.from('8.0.36-fake\0'), Buffer.alloc(40)])
    const hdr = Buffer.from([payload.length & 0xff, (payload.length >> 8) & 0xff, 0, 0])
    sock.write(Buffer.concat([hdr, payload]))
    setTimeout(() => sock.destroy(), 800)
  }),
  mongodb: () => tcpServer(sock => {
    sock.once('data', () => {
      // OP_MSG reply: flagBits(4) kind(1) + BSON { ok: 1.0 }
      const ok = Buffer.concat([Buffer.from([0x01]), Buffer.from('ok\0'), Buffer.from([0, 0, 0, 0, 0, 0, 0xf0, 0x3f])])
      const bson = Buffer.alloc(4); bson.writeInt32LE(4 + ok.length + 1)
      const body = Buffer.concat([Buffer.alloc(4), Buffer.from([0]), bson, ok, Buffer.from([0])])
      const head = Buffer.alloc(16)
      head.writeInt32LE(16 + body.length, 0); head.writeInt32LE(2, 4); head.writeInt32LE(1, 8); head.writeInt32LE(2013, 12)
      sock.write(Buffer.concat([head, body]))
      setTimeout(() => sock.destroy(), 500)
    })
  }),
  mqtt: (returnCode = 0) => tcpServer(sock => {
    sock.once('data', d => { if (d[0] === 0x10) sock.write(Buffer.from([0x20, 0x02, 0x00, returnCode])); else sock.destroy() })
  }),
  amqp: () => tcpServer(sock => {
    sock.once('data', d => {
      if (!d.toString('latin1').startsWith('AMQP')) { sock.destroy(); return }
      // Connection.Start: frame type 1, channel 0, size, class 10, method 10, then a short body
      const body = Buffer.concat([Buffer.from([0, 10, 0, 10, 0, 9]), Buffer.alloc(12)])
      const head = Buffer.alloc(7); head[0] = 1; head.writeUInt32BE(body.length, 3)
      sock.write(Buffer.concat([head, body, Buffer.from([0xce])]))
      setTimeout(() => sock.destroy(), 500)
    })
  }),
  garbage: () => tcpServer(garbage),
}

// ── UDP services ─────────────────────────────────────────────────────────

export const udpFakes = {
  udp: (reply = 'pong') => udpServer((msg, send) => send(Buffer.from(reply))),
  /** NTP server. offsetMs shifts the reported time; stratum 0 means "unsynchronised". */
  ntp: ({ offsetMs = 0, stratum = 2 } = {}) => udpServer((msg, send) => {
    const out = Buffer.alloc(48)
    out[0] = 0x24 // LI 0, version 4, mode 4 (server)
    out[1] = stratum
    const ms = Date.now() + offsetMs
    const sec = Math.floor(ms / 1000) + 2208988800
    out.writeUInt32BE(sec >>> 0, 40)
    out.writeUInt32BE(Math.floor(((ms % 1000) / 1000) * 2 ** 32) >>> 0, 44)
    send(out)
  }),
  /** A DNS server that knows one name (A record) and answers NXDOMAIN for everything else. */
  dns: ({ name = 'example.test', ip = '203.0.113.7' } = {}) => udpServer((msg, send) => {
    // parse the question
    let off = 12
    const labels = []
    while (msg[off] !== 0 && off < msg.length) { labels.push(msg.slice(off + 1, off + 1 + msg[off]).toString()); off += msg[off] + 1 }
    const qEnd = off + 1 + 4
    const qtype = msg.readUInt16BE(off + 1)
    const known = labels.join('.').toLowerCase() === name
    const head = Buffer.from(msg.slice(0, 12))
    const question = msg.slice(12, qEnd)
    let answer = Buffer.alloc(0)
    let rcode = known ? 0 : 3
    let an = 0
    if (known && qtype === 1) {
      const rr = Buffer.alloc(16)
      rr.writeUInt16BE(0xc00c, 0); rr.writeUInt16BE(1, 2); rr.writeUInt16BE(1, 4); rr.writeUInt32BE(60, 6); rr.writeUInt16BE(4, 10)
      ip.split('.').forEach((o, i) => { rr[12 + i] = Number(o) })
      answer = rr; an = 1
    }
    head.writeUInt16BE(0x8180 | rcode, 2) // response, recursion desired+available
    head.writeUInt16BE(1, 4); head.writeUInt16BE(an, 6); head.writeUInt16BE(0, 8); head.writeUInt16BE(0, 10)
    send(Buffer.concat([head, question, answer]))
  }),
  garbage: () => udpServer((msg, send) => send(Buffer.from('nope'))),
}

// ── gRPC health (cleartext HTTP/2) ──────────────────────────────────────

/** grpc.health.v1.Health/Check: servingStatus 1 = SERVING, 2 = NOT_SERVING; grpcStatus 0 ok, 12 unimplemented, 5 not found. */
export async function grpcServer({ servingStatus = 1, grpcStatus = 0 } = {}) {
  const server = http2.createServer()
  server.on('stream', (stream, headers) => {
    if (headers[':path'] !== '/grpc.health.v1.Health/Check') { stream.respond({ ':status': 404 }); stream.end(); return }
    stream.on('data', () => {})
    stream.on('end', () => {
      stream.respond({ ':status': 200, 'content-type': 'application/grpc' }, { waitForTrailers: true })
      stream.on('wantTrailers', () => stream.sendTrailers({ 'grpc-status': String(grpcStatus) }))
      if (grpcStatus === 0) stream.write(Buffer.from([0, 0, 0, 0, 2, 0x08, servingStatus]))
      stream.end()
    })
  })
  server.on('error', () => {})
  await new Promise(r => server.listen(0, '127.0.0.1', r))
  return { port: server.address().port, addr: `127.0.0.1:${server.address().port}`, close: () => { server.close(); server.closeAllConnections?.() } }
}

// ── WebSocket (minimal RFC 6455 server) ─────────────────────────────────

export async function wsServer({ greeting = 'welcome' } = {}) {
  const server = http.createServer((req, res) => { res.writeHead(426); res.end('upgrade required') })
  const socks = new Set()
  server.on('upgrade', (req, socket) => {
    socks.add(socket); socket.on('close', () => socks.delete(socket)); socket.on('error', () => {})
    const accept = crypto.createHash('sha1').update(`${req.headers['sec-websocket-key']}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`).digest('base64')
    socket.write(`HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`)
    if (greeting) {
      const payload = Buffer.from(greeting)
      socket.write(Buffer.concat([Buffer.from([0x81, payload.length]), payload]))
    }
    socket.on('data', d => { if ((d[0] & 0x0f) === 0x8) socket.end(Buffer.from([0x88, 0x00])) }) // answer a close frame
  })
  await new Promise(r => server.listen(0, '127.0.0.1', r))
  return { port: server.address().port, addr: `127.0.0.1:${server.address().port}`, close: () => { for (const s of socks) s.destroy(); server.close() } }
}

// ── TLS certificates (needs the openssl CLI; callers skip when it is missing) ──

export function makeCert({ days, cn = '127.0.0.1' }) {
  const dir = mkdtempSync(path.join(os.tmpdir(), 'opskest-cert-'))
  try {
    const key = path.join(dir, 'k.pem'), crt = path.join(dir, 'c.pem')
    const r = spawnSync('openssl', ['req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:prime256v1', '-nodes', '-keyout', key, '-out', crt,
      '-days', String(days), '-subj', `/CN=${cn}`, '-addext', 'subjectAltName=IP:127.0.0.1,DNS:localhost'], { encoding: 'utf8' })
    if (r.status !== 0) return null
    return { key: readFileSync(key), cert: readFileSync(crt) }
  } catch {
    return null
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
}

export const tlsServer = (certPair, handler = sock => { sock.end() }) => tcpServer(handler, { tlsOptions: { key: certPair.key, cert: certPair.cert } })
