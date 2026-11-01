// Responsive layout and theming: ~16 routes at 375px and 1280px in light and dark. No horizontal overflow,
// no uncaught exceptions or console errors, a heading on every page, and readable text contrast.

import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { e2eSuite, unique } from './harness.mjs'
import { inPageContrast, monitorBody } from './util.mjs'

const MIN_CONTRAST = 4.5

e2eSuite('responsive + theme', e => {
  let monitorId
  const routes = () => [
    '/', '/monitors', `/monitors/${monitorId}`, '/monitors/new', `/monitors/${monitorId}/edit`, '/projects', '/third-party', '/incidents',
    '/incidents?tab=outages', '/maintenance', '/pages', '/pages/new', '/settings?tab=notifications', '/settings?tab=logs',
    '/settings?tab=general', '/account/security',
  ]

  for (const viewport of ['mobile', 'desktop']) {
    for (const theme of ['light', 'dark']) {
      it(`${viewport} / ${theme}: no overflow, no errors, headings, contrast`, async t => {
        if (!monitorId) monitorId = (await e.app.apiOk('POST', '/api/monitors', monitorBody({ name: unique('resp'), type: 'tcp', target: e.target.tcpAddr, intervalS: 30 }))).id
        const page = await e.session(t, { viewport, theme, scheme: theme })
        const problems = []
        for (const route of routes()) {
          page.clearProblems()
          await page.goto(e.url(route))
          await page.waitForElement('main')
          await page.settle()
          const where = `${route} @${viewport}/${theme}`
          const dims = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, bw: document.body.scrollWidth, iw: innerWidth, dark: document.documentElement.classList.contains('dark') }))
          if (dims.sw > dims.iw || dims.bw > dims.iw) problems.push(`${where}: horizontal overflow (scrollWidth ${dims.sw}/${dims.bw} > ${dims.iw})`)
          if ((theme === 'dark') !== dims.dark) problems.push(`${where}: expected the ${theme} theme class, got dark=${dims.dark}`)
          if (!(await page.exists('h1'))) problems.push(`${where}: no <h1>`)
          for (const p of page.problems()) problems.push(`${where}: ${p}`)
          for (const [what, spec] of [
            ['heading', { css: 'main h1' }], ['body text', { css: 'main p' }], ['muted text', { css: 'main .text-muted' }],
            ['nav link', { css: 'nav a' }], ['primary button', { css: 'button.bg-accent' }],
          ]) {
            if (!(await page.exists(spec))) continue
            const c = await page.onElement(spec, inPageContrast)
            const large = c.size >= 24 || (c.size >= 18.66 && Number(c.weight) >= 700)
            if (c.ratio < (large ? 3 : MIN_CONTRAST)) problems.push(`${where}: ${what} contrast ${c.ratio.toFixed(2)} (fg ${c.fg} on bg ${c.bg})`)
          }
        }
        assert.deepEqual(problems, [], 'responsive/theme problems')
      })
    }
  }

  describe('theme handling', () => {
    it('"system" follows the OS colour scheme; an explicit choice overrides it and persists', async t => {
      const page = await e.session(t, { theme: 'system', scheme: 'dark', goto: '/' })
      await page.waitForElement({ role: 'heading', name: 'Operations' })
      const darkBg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor)
      await page.emulateColorScheme('light')
      await page.waitFor(b => getComputedStyle(document.body).backgroundColor !== b, { args: [darkBg], message: 'the system theme to follow the OS scheme change' })
      await page.click({ role: 'button', name: /^Appearance/ })
      await page.click({ role: 'menuitemradio', name: 'Dark' })
      await page.waitFor(() => document.documentElement.classList.contains('dark'), { message: 'dark class after choosing Dark' })
      assert.equal(await page.evaluate(() => localStorage.getItem('opskest-theme')), 'dark')
      await page.reload()
      await page.waitForElement({ role: 'heading', name: 'Operations' })
      assert.equal(await page.evaluate(() => document.documentElement.classList.contains('dark')), true, 'the choice survives a reload')
    })
  })
})
