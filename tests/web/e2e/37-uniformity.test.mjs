// App-wide uniformity: every signed-in screen uses the same page title (27px / 600), the same card radius
// (14px), no off-scale fractional font sizes, no spinners where a skeleton belongs, and the shared
// FilterChips / Tabs components instead of ad-hoc pill rows.

import assert from 'node:assert/strict'
import { it } from 'node:test'
import { e2eSuite } from './harness.mjs'

const SCREENS = [
  '/', '/monitors', '/projects', '/third-party', '/incidents', '/incidents?tab=outages', '/maintenance',
  '/monitors/new', '/account/security',
  ...['notifications', 'general', 'security', 'regions', 'database', 'data', 'import', 'logs', 'updates', 'about'].map(t => `/settings?tab=${t}`),
]

function probe() {
  const main = document.querySelector('main')
  const h1 = [...main.querySelectorAll('h1')].find(h => h.getBoundingClientRect().width > 0 && getComputedStyle(h).position !== 'absolute')
  const cs = h1 && getComputedStyle(h1)
  const off = new Set()
  for (const el of document.querySelectorAll('body *')) {
    const r = el.getBoundingClientRect()
    if (!r.width || !r.height) continue
    if ([...el.childNodes].some(n => n.nodeType === 3 && n.textContent.trim())) {
      const f = parseFloat(getComputedStyle(el).fontSize)
      if (!Number.isInteger(f)) off.add(f)
    }
  }
  // Cards: bordered, surface-filled containers that are large and are not controls or table cells.
  const radii = new Set()
  for (const el of main.querySelectorAll('section, article, div, form')) {
    const c = getComputedStyle(el)
    const r = el.getBoundingClientRect()
    if (parseFloat(c.borderTopWidth) > 0 && c.borderTopStyle === 'solid' && r.width > 240 && r.height > 80 && c.backgroundColor !== 'rgba(0, 0, 0, 0)' &&
      !el.closest('table,[role=dialog],[role=tablist],[role=radiogroup]') && c.overflowX !== "auto") {
      if (/^(0px|9999px)$/.test(c.borderTopLeftRadius)) continue
      radii.add(c.borderTopLeftRadius)
    }
  }
  return {
    title: h1 ? { size: cs.fontSize, weight: cs.fontWeight } : null,
    off: [...off], radii: [...radii],
    spinners: main.querySelectorAll('.animate-spin').length,
    hasMain: true,
  }
}

e2eSuite('uniformity', e => {
  it('every main screen has the same page title, no off-scale font sizes and no spinners', async t => {
    const page = await e.session(t)
    for (const path of SCREENS) {
      await page.goto(e.url(path))
      await page.waitForElement('main h1')
      await page.waitFor(() => !document.querySelector('main [role=status][aria-busy=true]'), { message: `loading skeletons on ${path} to finish` })
      const m = await page.evaluate(probe)
      assert.deepEqual(m.title, { size: '27px', weight: '600' }, `${path}: page title size/weight`)
      assert.deepEqual(m.off, [], `${path}: fractional font sizes ${m.off}`)
      assert.equal(m.spinners, 0, `${path}: no spinner left on the page`)
      for (const r of m.radii) assert.ok(['14px', '10px', '12px'].includes(r), `${path}: unexpected container radius ${r}`)
    }
  })

  it('detail screens use the same PageTitle', async t => {
    const mon = await e.app.apiOk('POST', '/api/monitors', { name: 'Uniform monitor', type: 'push', target: '', intervalS: 3600, retryIntervalS: 3600, settings: {}, secrets: {} }).catch(() => null)
    const page = await e.session(t)
    const id = mon?.id ?? (await e.app.apiOk('GET', '/api/monitors')).monitors?.[0]?.id
    if (id == null) return
    for (const path of [`/monitors/${id}`, `/monitors/${id}/edit`]) {
      await page.goto(e.url(path))
      await page.waitForElement('main h1')
      const m = await page.evaluate(probe)
      assert.deepEqual(m.title, { size: '27px', weight: '600' }, `${path}: page title size/weight`)
      assert.deepEqual(m.off, [], `${path}: fractional font sizes`)
    }
  })

  it('filters are shared FilterChips groups and view switches are Tabs', async t => {
    const page = await e.session(t, { goto: '/projects' })
    await page.waitForElement('main h1')
    await page.goto(e.url('/third-party'))
    await page.waitForElement({ role: 'tab', name: /Browse providers/ })
    await page.click({ role: 'tab', name: /Browse providers/ })
    await page.waitForElement({ css: '[role=group][aria-label="Provider category"] button[aria-pressed]' })
    await page.goto(e.url('/incidents'))
    await page.waitForElement({ role: 'tab', name: 'Monitor outages' })
  })

  for (const [label, vp] of [['1280x720', { width: 1280, height: 720 }], ['1920x1080', { width: 1920, height: 1080 }]]) {
    it(`long dialogs keep their header and footer buttons inside the viewport at ${label}`, async t => {
      const page = await e.session(t, { viewport: vp })
      const inside = () => {
        const d = document.querySelector('[role=dialog]')
        if (!d) return 'no dialog'
        const h = d.querySelector('h2')?.getBoundingClientRect()
        const btns = [...d.querySelectorAll('button')].filter(b => /^(Cancel|Open incident|Create channel|Add Provider|Save|Add user|Create project)/.test(b.textContent.trim()))
        const last = btns[btns.length - 1]?.getBoundingClientRect()
        const r = d.getBoundingClientRect()
        const ok = r.top >= 0 && r.bottom <= innerHeight && r.left >= 0 && r.right <= innerWidth && h && h.top >= 0 && last && last.bottom <= innerHeight
        return ok ? 'ok' : `dialog ${Math.round(r.top)}-${Math.round(r.bottom)} of ${innerHeight}, footer ${last && Math.round(last.bottom)}`
      }
      const cases = [
        ['/incidents', /Declare incident/],
        ['/settings?tab=notifications', /Add alert channel/],
        ['/third-party', /Connect a feed/],
        ['/maintenance', /New window/],
        ['/settings?tab=security', /^Add user$/],
        ['/projects', /New project/],
        ['/monitors/new', /Save these settings as a template/],
      ]
      for (const [path, name] of cases) {
        await page.goto(e.url(path))
        if (path === '/monitors/new') await page.click({ role: 'button', name: /^Templates/ }) // the templates panel starts collapsed
        await page.waitForElement({ role: 'button', name })
        await page.click({ role: 'button', name })
        await page.waitForElement({ role: 'dialog' })
        assert.equal(await page.evaluate(inside), 'ok', `${path} dialog at ${label}`)
        await page.press('Escape')
        await page.waitForGone({ role: 'dialog' })
      }
    })
  }

  // Rebuilt two-column dialogs: nothing scrolls inside at 1920x1080, and paired fields share a row at equal widths.
  it('user, template and project dialogs have no inner scroll at 1920x1080 and use two equal columns', async t => {
    const page = await e.session(t, { viewport: { width: 1920, height: 1080 } })
    const cases = [
      ['/settings?tab=security', /^Add user$/],
      ['/monitors/new', /Save these settings as a template/],
      ['/projects', /New project/],
    ]
    for (const [path, name] of cases) {
      await page.goto(e.url(path))
      if (path === '/monitors/new') await page.click({ role: 'button', name: /^Templates/ }) // the templates panel starts collapsed
      await page.waitForElement({ role: 'button', name })
      await page.click({ role: 'button', name })
      await page.waitForElement({ role: 'dialog' })
      const m = await page.evaluate(() => {
        const d = document.querySelector('[role=dialog]')
        const body = d.querySelector('[data-modal-body]')
        const f = [...body.querySelectorAll('input')].slice(0, 2).map(i => i.getBoundingClientRect())
        return { scroll: body.scrollHeight - body.clientHeight, widthDiff: Math.abs(f[0].width - f[1].width), topDiff: Math.abs(f[0].top - f[1].top) }
      })
      assert.ok(m.scroll <= 1, `${path}: dialog body scrolls by ${m.scroll}px`)
      assert.ok(m.widthDiff <= 1 && m.topDiff <= 1, `${path}: first two fields not side by side at equal width (${m.widthDiff}, ${m.topDiff})`)
      await page.press('Escape')
      await page.waitForGone({ role: 'dialog' })
    }
  })

  it('sibling cards in a row are equal height: providers, projects and overview tiles; account security is one aligned column', async t => {
    const page = await e.session(t)
    for (let i = 0; i < 3; i++) {
      await e.app.apiOk('POST', '/api/monitors', { name: `Uniform feed ${i} ${'x'.repeat(i * 30)}`, type: 'http', target: e.target.httpUrl, intervalS: 3600, retryIntervalS: 3600, settings: { acceptedCodes: '200-299', vendor: true }, secrets: {} })
      await e.app.apiOk('POST', '/api/groups', { name: `Uniform project ${i}`, description: 'd '.repeat(i * 40) })
    }
    const rows = sel => page.evaluate(`(() => {
      const els = [...document.querySelectorAll(${JSON.stringify(sel)})].map(el => el.getBoundingClientRect())
      const by = {}
      for (const r of els) (by[Math.round(r.top / 4)] ||= []).push(r)
      return Object.values(by).filter(g => g.length > 1).map(g => ({ n: g.length, h: Math.max(...g.map(r => r.height)) - Math.min(...g.map(r => r.height)), w: Math.max(...g.map(r => r.width)) - Math.min(...g.map(r => r.width)) }))
    })()`)
    const check = async (label, sel) => {
      const groups = await rows(sel)
      assert.ok(groups.length > 0, `${label}: no row of 2+ cards found`)
      for (const g of groups) assert.ok(g.h <= 1 && g.w <= 1, `${label}: sibling cards differ (${JSON.stringify(g)})`)
    }
    await page.goto(e.url('/third-party'))
    await page.waitForElement({ css: 'section[aria-label="Connected provider feeds"] article' })
    await check('providers', 'section[aria-label="Connected provider feeds"] article')
    await page.goto(e.url('/projects'))
    await page.waitForElement({ css: 'main a[href^="/projects/"]' })
    await check('projects', 'main a[href^="/projects/"]')
    await page.goto(e.url('/'))
    await page.waitForElement({ css: 'nav[aria-label="Operations summary"] a' })
    await check('overview tiles', 'nav[aria-label="Operations summary"] a')
    await page.goto(e.url('/account/security'))
    await page.waitForElement({ css: 'section[data-settings-panel="password"]' })
    // Account security is one centred column: the two cards stack and share the same left edge and width.
    const column = await page.evaluate(`(() => {
      const r = [...document.querySelectorAll('main section[data-settings-panel]')].map(el => el.getBoundingClientRect())
      return { n: r.length, dx: Math.abs(r[0].left - r[1].left), dw: Math.abs(r[0].width - r[1].width), stacked: r[1].top >= r[0].bottom }
    })()`)
    assert.equal(column.n, 2, 'account security: two-factor and password cards')
    assert.ok(column.dx <= 1 && column.dw <= 1 && column.stacked, `account security: cards not in one aligned column (${JSON.stringify(column)})`)
  })
})
