// No infinite scrolling: with 300+ rows behind every list, each screen renders at most 100 rows and stays within
// 3x the viewport (dashboards that use the one-screen fit layout satisfy this by being exactly one screen tall).
// The measurements are printed as a table (rows rendered, document height / viewport) so a regression is easy to see.

import assert from 'node:assert/strict'
import { before, it } from 'node:test'
import { e2eSuite } from './harness.mjs'
import { seedBounded } from './40-seed.mjs'

const MAX_ROWS = 100
const MAX_SCREENS = 3
// The public overview stacks several bounded sections (banner, incidents, one page of services, maintenance, history), so it gets a looser height budget.
const PUBLIC_SCREENS = 5

/** Measured inside the page: rendered list rows and the document height against the viewport. */
function measure() {
  const root = document.querySelector('main') ?? document.body
  const rows = new Set(root.querySelectorAll('li, tbody tr, [role=row], [role=option], article, [data-row]'))
  // A row that contains another counted row (a list item wrapping a nested list) counts once.
  for (const r of [...rows]) if ([...rows].some(o => o !== r && r.contains(o) && o.tagName === r.tagName)) rows.delete(r)
  const de = document.documentElement
  const scroller = [...root.querySelectorAll('*')].find(el => el.scrollHeight > el.clientHeight + 40 && ['auto', 'scroll'].includes(getComputedStyle(el).overflowY) && el.clientHeight > 100)
  return {
    rows: rows.size,
    docH: Math.max(de.scrollHeight, document.body.scrollHeight),
    vh: innerHeight,
    inner: scroller ? Math.round(scroller.scrollHeight / innerHeight * 10) / 10 : 0,
  }
}

e2eSuite('bounded lists', e => {
  const s = {}
  const results = []

  before(async () => { Object.assign(s, await seedBounded(e)) }, { timeout: 900000 })

  /** Visits `path`, optionally after `prep(page)`, and records the measurement. */
  async function check(t, name, path, { prep, anonymous = false, viewport = 'desktop', screens = MAX_SCREENS } = {}) {
    const page = anonymous ? await e.anonymous(t, { viewport }) : await e.session(t, { viewport })
    await page.goto(e.url(path))
    await page.waitForElement('main, #root > *')
    await page.settle({ quiet: 500 })
    if (prep) { await prep(page); await page.settle({ quiet: 500 }) }
    const m = await page.evaluate(measure)
    results.push({ name, ...m })
    console.log(`# ${name.padEnd(46)} rows=${String(m.rows).padStart(4)}  doc=${m.docH}px / vh=${m.vh} (${(m.docH / m.vh).toFixed(1)}x)${m.inner ? `  inner-scroll=${m.inner}x` : ''}`)
    assert.ok(m.rows <= MAX_ROWS, `${name}: rendered ${m.rows} rows (max ${MAX_ROWS})`)
    assert.ok(m.docH <= m.vh * screens, `${name}: document is ${m.docH}px tall for a ${m.vh}px viewport (max ${screens}x)`)
    return page
  }

  const tab = name => async page => { await page.click({ role: 'tab', name }) }

  it('Overview', async t => { await check(t, 'Overview', '/') })
  it('Monitors', async t => { await check(t, 'Monitors', '/monitors') })
  it('Command palette', async t => {
    await check(t, 'Command palette (query "bl")', '/monitors', { prep: async p => { await p.press('Control+k'); await p.type(s.tag.slice(0, 2)) } })
  })
  for (const view of ['Checks', 'Events', 'Reliability', 'Diagnostics']) {
    it(`Monitor detail / ${view}`, async t => { await check(t, `Monitor detail / ${view}`, `/monitors/${s.manual[0].id}`, { prep: tab(view) }) })
  }
  it('Projects', async t => { await check(t, 'Projects', '/projects') })
  it('Project detail', async t => { await check(t, 'Project detail / Services', `/projects/${s.groups[0]}`) })
  it('Project detail events', async t => { await check(t, 'Project detail / Events', `/projects/${s.groups[0]}`, { prep: tab('Events') }) })
  it('Incidents inbox', async t => { await check(t, 'Incidents inbox', '/incidents') })
  it('Incidents / Monitor outages', async t => { await check(t, 'Incidents / Monitor outages', '/incidents?tab=outages') })
  it('Incident detail', async t => { await check(t, 'Incident detail (150 updates)', `/incidents/${s.incident.id}`) })
  it('Maintenance list', async t => { await check(t, 'Maintenance list', '/maintenance') })
  it('Providers', async t => { await check(t, 'Providers', '/third-party') })
  it('Status pages admin', async t => { await check(t, 'Status pages admin', '/pages') })
  for (const tabId of ['notifications', 'security', 'database', 'logs', 'import', 'data', 'regions', 'updates', 'about', 'general']) {
    it(`Settings / ${tabId}`, async t => { await check(t, `Settings / ${tabId}`, `/settings?tab=${tabId}`) })
  }
  it('Page editor / services', async t => { await check(t, 'Page editor / services (320)', `/pages/${s.pageId}?tab=services`) })
  it('Page editor / updates & subscribers', async t => { await check(t, 'Page editor / updates', `/pages/${s.pageId}?tab=updates`) })
  it('Public status page', async t => { await check(t, 'Public status page (320 services)', `/status/${s.slug}`, { anonymous: true, screens: PUBLIC_SCREENS }) })
  it('Public status page, expanded', async t => {
    await check(t, 'Public status page, groups open', `/status/${s.slug}`, { anonymous: true, screens: PUBLIC_SCREENS, prep: async p => { await p.evaluate(() => document.querySelectorAll('button[aria-expanded=false]').forEach(b => b.click())) } })
  })
  it('Public status page, phone', async t => { await check(t, 'Public status page (375px)', `/status/${s.slug}`, { anonymous: true, viewport: 'mobile', screens: PUBLIC_SCREENS }) })
  it('Public history', async t => { await check(t, 'Public history', `/status/${s.slug}/history`, { anonymous: true }) })
  it('Public incident', async t => { await check(t, 'Public incident (150 updates)', `/status/${s.slug}/incidents/${s.incident.id}`, { anonymous: true }) })
  it('Public wallboard', async t => { await check(t, 'Public wallboard (200 services)', `/status/${s.boardSlug}/board`, { anonymous: true }) })

  it('the list APIs the screens use are bounded', async () => {
    for (const p of ['/api/incidents', '/api/outages', '/api/alert-log', '/api/audit-log', '/api/monitors/' + s.manual[0].id + '/outages', `/api/outages/${s.outageId}/notes`, `/api/pages/${s.pageId}/subscribers`]) {
      const r = await e.app.apiOk('GET', `${p}${p.includes('?') ? '&' : '?'}limit=1000`)
      const arr = Object.values(r).find(Array.isArray) ?? []
      assert.ok(arr.length <= 200, `${p} returned ${arr.length} rows for limit=1000`)
    }
  })

  it('summary', () => {
    console.log(`# ${results.length} screens measured; worst: ${results.sort((a, b) => b.rows - a.rows)[0]?.name} (${results[0]?.rows} rows)`)
  })
})
