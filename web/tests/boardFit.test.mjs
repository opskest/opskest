import test from 'node:test'
import assert from 'node:assert/strict'
import { CONFIGS, layoutHeight, orderItems, pageTilesOf, planBoard } from '../src/lib/boardFit.ts'

const item = (i, status = 'up', extra = {}) => ({ key: i, name: `Service ${i}`, status, beats: true, ...extra })
const many = (n, down = 0) => Array.from({ length: n }, (_, i) => item(i, i < down ? 'down' : 'up', i < down ? { reason: 'connection refused: dial tcp 10.0.0.12:443: i/o timeout while connecting to the upstream' } : {}))
const HD = { w: 1896, h: 1000, showReason: true }

test('every page of every plan fits the height it was given (the board never scrolls)', () => {
  for (const [w, h] of [[1920, 1080], [1280, 720], [3840, 2160], [2560, 1080], [1024, 768]]) {
    for (const [n, down] of [[3, 0], [8, 2], [12, 11], [40, 5], [120, 8], [300, 30], [153, 0]]) {
      const opts = { w: w - 24, h: h - 100, showReason: true }
      const p = planBoard(many(n, down), opts)
      const cfg = CONFIGS.find(c => c.fs === p.fs && c.tier === p.tier)
      for (let page = 0; page < p.pages; page++) {
        const tiles = pageTilesOf(p, page)
        assert.ok(layoutHeight(tiles, opts, cfg).height <= opts.h + 1, `${n} items (${down} down) at ${w}x${h}, page ${page + 1}/${p.pages}`)
      }
    }
  }
})

test('few services get a bigger type size than many, on one page', () => {
  const few = planBoard(many(4), HD)
  assert.equal(few.pages, 1)
  const lots = planBoard(many(60), HD)
  assert.ok(few.fs >= lots.fs)
})

test('too many for one readable screen becomes a slideshow that shows every service', () => {
  const p = planBoard(many(153), HD)
  assert.ok(p.pages > 1, 'rotates')
  assert.ok(p.fs >= 14, 'readable text')
  const seen = new Set()
  for (let page = 0; page < p.pages; page++) for (const t of pageTilesOf(p, page)) seen.add(t.key)
  assert.equal(seen.size, 153)
})

test('problems stay on every page of a slideshow', () => {
  const p = planBoard(many(400, 12), HD)
  assert.ok(p.pages > 1)
  for (let page = 0; page < p.pages; page++) {
    const downs = pageTilesOf(p, page).filter(t => t.status === 'down')
    assert.equal(downs.length, 12, `page ${page + 1} lists all 12 problems`)
  }
})

test('more problems than fit rotate in pages instead of scrolling', () => {
  const p = planBoard(many(500, 500), { w: 1000, h: 500, showReason: true })
  assert.ok(p.pages > 1)
  const seen = new Set()
  for (let page = 0; page < p.pages; page++) for (const t of pageTilesOf(p, page)) seen.add(t.key)
  assert.equal(seen.size, 500)
})

test('ordering is worst first then by name', () => {
  const o = orderItems([item(2, 'up'), item(1, 'degraded'), item(3, 'down')])
  assert.deepEqual(o.map(i => i.status), ['down', 'degraded', 'up'])
})

test('empty or zero-size input is safe', () => {
  assert.equal(planBoard([], HD).tiles.length, 0)
  assert.equal(planBoard(many(3), { w: 0, h: 0, showReason: true }).tiles.length, 0)
})
