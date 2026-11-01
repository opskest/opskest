// Tags: created, renamed and deleted from Settings, and created from the monitor form.

import assert from 'node:assert/strict'
import { it } from 'node:test'
import { e2eSuite, unique } from './harness.mjs'

const NEW_TAG = { placeholder: /^New tag/ }
// The tags API returns Go field names (ID, Name, Color); the UI accepts both spellings.
const tag = x => ({ id: x.id ?? x.ID, name: x.name ?? x.Name, color: x.color ?? x.Color })
const tagsOf = async e => ((await e.app.apiOk('GET', '/api/tags')).tags ?? []).map(tag)

e2eSuite('tags', e => {
  it('Settings: create, rename and delete a tag', async t => {
    const name = unique('tag')
    const page = await e.session(t, { goto: '/settings?tab=tags' })
    await page.waitForElement({ role: 'heading', name: 'Tags' })

    await page.fill(NEW_TAG, name)
    await page.click({ role: 'radio', name: '#2563eb' })
    await page.click({ role: 'button', name: 'Add tag' })
    await page.waitForElement({ role: 'listitem', text: name })
    let list = await tagsOf(e)
    const made = list.find(x => x.name === name)
    assert.ok(made, `the tag was stored: ${JSON.stringify(list).slice(0, 300)}`)
    assert.equal(made.color, '#2563eb')

    // The same name twice is refused with a message, not a crash.
    await page.fill(NEW_TAG, name)
    await page.click({ role: 'button', name: 'Add tag' })
    await page.waitForElement({ role: 'alert', text: /already (taken|exist)/i })

    // Rename and recolour.
    const renamed = `${name}-r`
    await page.click({ role: 'button', name: `Edit tag ${name}` })
    await page.fill({ css: 'input[aria-label^="Name of"]' }, renamed)
    await page.click({ css: `[role=radiogroup][aria-label="Colour of ${name}"] [role=radio][aria-label="#059669"]` })
    await page.click({ role: 'button', name: 'Save' })
    await page.waitForElement({ role: 'listitem', text: renamed })
    list = await tagsOf(e)
    const saved = list.find(x => x.id === made.id)
    assert.equal(saved?.name, renamed)
    assert.equal(saved?.color, '#059669')

    // Delete asks first.
    await page.click({ role: 'button', name: `Delete tag ${renamed}` })
    await page.waitForElement({ role: 'dialog' })
    await page.click({ role: 'button', name: 'Delete tag', within: { role: 'dialog' } })
    await page.waitFor(n => !document.body.innerText.includes(n), { args: [renamed], message: 'the tag disappears from the list' })
    list = await tagsOf(e)
    assert.equal(list.some(x => x.id === made.id), false, 'the tag was deleted')
  })

  it('Monitor form: create a tag in the dialog and it is selected', async t => {
    const name = unique('ftag')
    const page = await e.session(t, { goto: '/monitors/new' })
    await page.waitForElement({ role: 'heading', name: 'Add monitor' })
    await page.click({ role: 'button', name: /^(Create a tag|Manage tags)$/ })
    await page.waitForElement({ role: 'dialog' })
    await page.fill(NEW_TAG, name)
    await page.click({ role: 'button', name: 'Add tag' })
    await page.waitForElement({ role: 'listitem', text: name })
    await page.click({ role: 'button', name: 'Done' })
    // The new tag is already picked on the form.
    await page.waitFor(n => [...document.querySelectorAll('button[aria-pressed]')].some(b => b.textContent.trim() === n && b.getAttribute('aria-pressed') === 'true'),
      { args: [name], message: 'the new tag is selected on the form' })
    const list = await tagsOf(e)
    for (const x of list.filter(x => x.name === name)) await e.app.apiOk('DELETE', `/api/tags/${x.id}`)
  })

  it('the API refuses an invalid name or colour', async () => {
    for (const body of [{ name: '   ' }, { name: 'x'.repeat(41) }, { name: 'ok', color: 'red' }]) {
      const r = await e.app.api('POST', '/api/tags', body)
      assert.equal(r.status, 400, `expected 400 for ${JSON.stringify(body).slice(0, 40)}`)
    }
  })
})
