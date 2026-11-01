// Monitor templates: save current settings, apply on a new monitor, delete, empty state; identity/secrets never stored.

import assert from 'node:assert/strict'
import { it } from 'node:test'
import { e2eSuite, unique } from './harness.mjs'

const field = label => ({ label: label instanceof RegExp ? label : new RegExp(`^${label}$`) })
const FORM = '/monitors/new'

/** The templates panel is collapsed by default; open it. */
async function openTemplates(page) {
  await page.click({ role: 'button', name: /^Templates/ })
}

e2eSuite('templates', e => {
  it('empty state, save as template, apply it, then delete it', async t => {
    // Clean slate for the empty-state assertion, independent of other tests in this file.
    for (const tpl of (await e.app.apiOk('GET', '/api/templates')).templates ?? []) await e.app.apiOk('DELETE', `/api/templates/${tpl.id}`)
    const page = await e.session(t, { goto: FORM })
    await page.waitForElement({ role: 'heading', name: 'Add monitor' })
    // The templates panel starts collapsed: nothing of it is visible until it is opened.
    assert.equal(await page.exists({ role: 'button', name: 'Save these settings as a template' }), false)
    await openTemplates(page)
    await page.waitForElement({ text: /No templates yet/ })

    // Configure a TCP monitor with distinctive settings, plus a name/target that must NOT be saved.
    const name = unique('tpl-source'), tplName = unique('Tpl')
    await page.click({ css: '[role=radio][data-type="tcp"]' })
    await page.fill(field('Name'), name)
    await page.fill(field('Host and port'), '10.9.8.7:4321')
    await page.click({ css: 'nav[aria-label="Monitor configuration"] button', text: 'Check schedule' })
    await page.fill(field('Check every'), '45')
    await page.fill(field('Timeout'), '4000')
    await page.fill(field('Confirmations before down'), '4')
    await page.click({ css: 'nav[aria-label="Monitor configuration"] button', text: 'Service & endpoint' })
    await openTemplates(page) // switching sections collapses the panel again
    await page.click({ role: 'button', name: 'Save these settings as a template' })
    await page.fill(field('Template name'), tplName)
    await page.fill({ placeholder: 'What it is for' }, 'e2e template')
    await page.click({ role: 'button', name: 'Save template' })
    await page.waitForElement({ role: 'button', name: new RegExp(tplName) })

    // The stored template has the settings but not the identity.
    const list = (await e.app.apiOk('GET', '/api/templates')).templates
    const saved = list.find(x => x.name === tplName)
    assert.ok(saved, 'template was stored')
    assert.equal(saved.type, 'tcp')
    assert.equal(saved.config.intervalS, 45)
    assert.equal(saved.config.retries, 4)
    const raw = JSON.stringify(saved)
    assert.ok(!raw.includes('10.9.8.7'), 'the target must not be saved in a template')
    assert.ok(!raw.includes(name), 'the monitor name must not be saved in a template')
    assert.ok(!('target' in saved.config) || !saved.config.target, 'no target in config')
    assert.ok(!('name' in saved.config) || !saved.config.name, 'no name in config')
    assert.ok(!('secrets' in saved.config), 'no secrets in config')

    // Apply it on a fresh form: type, schedule come from the template; name/target stay empty.
    const page2 = await e.session(t, { goto: FORM })
    await openTemplates(page2)
    await page2.click({ role: 'button', name: new RegExp(tplName) })
    await page2.waitFor(() => document.querySelector('[role=radio][data-type=tcp]')?.getAttribute('aria-checked') === 'true', { message: 'type TCP after applying the template' })
    assert.equal(await page2.value(field('Name')), '')
    assert.equal(await page2.value(field('Host and port')), '')
    await page2.click({ css: 'nav[aria-label="Monitor configuration"] button', text: 'Check schedule' })
    assert.equal(await page2.value(field('Check every')), '45')
    assert.equal(await page2.value(field('Confirmations before down')), '4')

    // Rename through the API is reflected in the picker after a reload.
    await e.app.apiOk('PUT', `/api/templates/${saved.id}`, { name: `${tplName}-r`, description: saved.description, config: saved.config })
    await page2.goto(e.url(FORM))
    await openTemplates(page2)
    await page2.waitForElement({ role: 'button', name: new RegExp(`${tplName}-r`) })

    // Delete from the picker.
    await page2.click({ role: 'button', name: new RegExp(`Delete template ${tplName}-r`) })
    await page2.click({ role: 'button', name: 'Delete', css: 'span button' })
    await page2.waitFor(() => /No templates yet/.test(document.body.innerText), { message: 'empty state after deleting the only template' })
    assert.equal(((await e.app.apiOk('GET', '/api/templates')).templates ?? []).length, 0)
    page2.assertNoProblems()
  })

  it('secrets typed into the form are not written to the template', async t => {
    const page = await e.session(t, { goto: FORM })
    await openTemplates(page)
    const secret = `s3cret-${unique('tok')}`
    await page.fill(field('Name'), unique('sec'))
    await page.fill(field('URL'), 'http://127.0.0.1:9/')
    await page.click({ css: 'nav[aria-label="Monitor configuration"] button', text: 'All settings' })
    await page.click({ role: 'button', name: /Advanced request options/ })
    const sel = { css: 'select', label: /Authentication|Auth/ }
    if (await page.exists(sel)) {
      await page.select(sel, 'bearer')
      await page.fill({ css: 'input[type=password]' }, secret)
    }
    const tplName = unique('TplSecret')
    await page.click({ role: 'button', name: 'Save these settings as a template' })
    await page.fill(field('Template name'), tplName)
    await page.click({ role: 'button', name: 'Save template' })
    await page.waitForElement({ role: 'button', name: new RegExp(tplName) })
    const text = JSON.stringify((await e.app.apiOk('GET', '/api/templates')).templates)
    assert.ok(!text.includes(secret), 'a bearer token must never be stored in a template')
  })
})
