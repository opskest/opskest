import test from 'node:test'
import assert from 'node:assert/strict'
import { qrModules } from '../src/lib/qr.ts'

// Decoding is checked against a real reader when the encoder changes (see the
// project notes); these guard the structure the readers rely on.
const url = 'otpauth://totp/Opskest%3Aadmin?algorithm=SHA1&digits=6&issuer=Opskest&period=30&secret=JBSWY3DPEHPK3PXP'

test('a short text fits version 1 (21 modules) and an otpauth address stays small enough to scan', () => {
  assert.equal(qrModules('hello').length, 21)
  const m = qrModules(url)
  assert.ok(m.length >= 41 && m.length <= 57, `size ${m.length}`)
  assert.equal(m.length, m[0].length)
})

test('the three finder patterns and the timing lines are in place', () => {
  const m = qrModules(url)
  const n = m.length
  for (const [ox, oy] of [[0, 0], [n - 7, 0], [0, n - 7]]) {
    for (let i = 0; i < 7; i++) {
      assert.equal(m[oy][ox + i], true); assert.equal(m[oy + 6][ox + i], true)
      assert.equal(m[oy + i][ox], true); assert.equal(m[oy + i][ox + 6], true)
    }
    assert.equal(m[oy + 3][ox + 3], true)
    assert.equal(m[oy + 1][ox + 1], false)
  }
  for (let i = 8; i < n - 8; i++) { assert.equal(m[6][i], i % 2 === 0); assert.equal(m[i][6], i % 2 === 0) }
})

test('the same text always gives the same code, and too much text is refused', () => {
  assert.deepEqual(qrModules(url), qrModules(url))
  assert.throws(() => qrModules('x'.repeat(400)), RangeError)
})
