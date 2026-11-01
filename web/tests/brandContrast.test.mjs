import test from 'node:test'
import assert from 'node:assert/strict'
import { accessibleBrand, contrastRatio } from '../src/lib/brandContrast.ts'

test('custom branding retains readable links and button text in both appearances', () => {
  for (const color of ['#000000', '#ffffff', '#0000ff', '#ff0000', '#00ff00', '#888888', '#abcdef', '#012345']) {
    for (const dark of [true, false]) {
      const result = accessibleBrand(color, dark)
      assert.ok(contrastRatio(result.accent, dark ? '#18212b' : '#f4f6f8') >= 4.5, `${color} links`)
      assert.ok(contrastRatio(result.accent, result.foreground) >= 4.5, `${color} button text`)
    }
  }
})
test('invalid brand colors do not override semantic tokens', () => {
  for (const value of ['', '#fff', 'red', 'url(https://example.com)', '#gggggg']) assert.equal(accessibleBrand(value, true), undefined)
})
test('a sufficiently legible owner color is preserved exactly', () => {
  assert.equal(accessibleBrand('#2fd9a8', true).accent, '#2fd9a8')
  assert.equal(accessibleBrand('#08795b', false).accent, '#08795b')
})
test('every hue, saturation and lightness stays legible in both appearances', () => {
  const hex = (h, s, l) => {
    const a = s * Math.min(l, 1 - l)
    const f = n => { const k = (n + h / 30) % 12; return Math.round(255 * (l - a * Math.max(-1, Math.min(k - 3, 9 - k, 1)))) }
    return '#' + [f(0), f(8), f(4)].map(v => v.toString(16).padStart(2, '0')).join('')
  }
  const failures = []
  for (let h = 0; h < 360; h += 7) for (const s of [0.1, 0.5, 1]) for (const l of [0.05, 0.25, 0.5, 0.75, 0.95]) for (const dark of [true, false]) {
    const color = hex(h, s, l)
    const r = accessibleBrand(color, dark)
    if (!r) { failures.push(`${color} rejected`); continue }
    if (contrastRatio(r.accent, r.foreground) < 4.5) failures.push(`${color} ${dark ? 'dark' : 'light'}: text on brand`)
    if (contrastRatio(r.accent, dark ? '#18212b' : '#f4f6f8') < 4.5) failures.push(`${color} ${dark ? 'dark' : 'light'}: brand on page`)
  }
  assert.deepEqual(failures.slice(0, 5), [], `${failures.length} failing combinations`)
})
