// Notes you can change: an outage holds a thread of notes (add several, edit, delete) and an incident's
// timeline entries can be edited in place. The single-note API older clients use keeps working.

import assert from 'node:assert/strict'
import { it } from 'node:test'
import { e2eSuite, unique } from './harness.mjs'
import { monitorBody } from './util.mjs'

const dialog = { role: 'dialog' }
const inDialog = spec => ({ ...spec, within: dialog })

async function downOutage(e) {
  const mon = await e.app.apiOk('POST', '/api/monitors', monitorBody({ name: unique('notes-svc'), type: 'tcp', target: '127.0.0.1:1', intervalS: 10, timeoutMs: 2000 }))
  let outage
  for (let i = 0; i < 80 && !outage; i++) {
    outage = ((await e.app.apiOk('GET', `/api/monitors/${mon.id}/outages?limit=5`)).outages ?? [])[0]
    if (!outage) await new Promise(r => setTimeout(r, 500))
  }
  assert.ok(outage, 'the monitor should have opened an outage within 40 seconds')
  return { mon, outage }
}

e2eSuite('notes', e => {
  it('outage: add several notes, edit one, delete one; the table and the API follow', async t => {
    const { mon, outage } = await downOutage(e)
    const page = await e.session(t, { goto: `/incidents?tab=outages&q=${encodeURIComponent(mon.name)}` })
    await page.waitForElement({ text: mon.name })
    await page.waitForElement({ role: 'button', name: 'Add note' })

    // First note, from the empty thread.
    await page.click({ role: 'button', name: 'Add note' })
    await page.waitForElement({ role: 'dialog', name: 'Outage notes' })
    await page.waitForElement(inDialog({ text: 'No notes yet.' }))
    const n1 = `Connection pool exhausted ${unique('n1')}`
    const n2 = `Restarted the pool ${unique('n2')}`
    const n3 = `Follow-up: add a saturation alert ${unique('n3')}`
    await page.fill(inDialog({ role: 'textbox', name: 'Add a note' }), n1)
    await page.click(inDialog({ role: 'button', name: 'Add note' }))
    await page.waitForElement(inDialog({ text: n1 }))
    // No limit of one: two more.
    await page.fill(inDialog({ role: 'textbox', name: 'Add a note' }), n2)
    await page.click(inDialog({ role: 'button', name: 'Add note' }))
    await page.waitForElement(inDialog({ text: n2 }))
    await page.fill(inDialog({ role: 'textbox', name: 'Add a note' }), n3)
    await page.click(inDialog({ role: 'button', name: 'Add note' }))
    await page.waitForElement(inDialog({ text: n3 }))
    let thread = (await e.app.apiOk('GET', `/api/outages/${outage.id}/notes`)).notes
    assert.deepEqual(thread.map(n => n.body), [n1, n2, n3], 'oldest first')
    assert.ok(thread.every(n => n.authorName === 'E2E Admin' && n.editedAt === null && n.canEdit))
    assert.equal(await page.count(inDialog({ role: 'listitem' })), 3)

    // An empty note cannot be added.
    assert.equal(await page.evaluate(() => [...document.querySelectorAll('[role=dialog] button')].find(b => b.textContent.trim() === 'Add note')?.disabled), true, 'Add note is disabled while the box is empty')

    // Edit the second note in place.
    await page.click(inDialog({ role: 'button', name: 'Actions for note 2' }))
    await page.click({ role: 'menuitem', name: 'Edit' })
    const edited = `${n2} (at 10:42)`
    await page.fill(inDialog({ role: 'textbox', name: 'Edit note 2' }), edited)
    await page.click(inDialog({ role: 'button', name: 'Save' }))
    await page.waitForElement(inDialog({ text: edited }))
    await page.waitForElement(inDialog({ text: /\(edited/ }))
    thread = (await e.app.apiOk('GET', `/api/outages/${outage.id}/notes`)).notes
    assert.equal(thread[1].body, edited)
    assert.ok(thread[1].editedAt > 0, 'edited_at is stamped')
    assert.equal(thread[0].editedAt, null)

    // Escape cancels an edit without saving.
    await page.click(inDialog({ role: 'button', name: 'Actions for note 1' }))
    await page.click({ role: 'menuitem', name: 'Edit' })
    await page.fill(inDialog({ role: 'textbox', name: 'Edit note 1' }), 'discard me')
    await page.press('Escape')
    await page.waitForElement(inDialog({ text: n1 }))
    assert.ok(!(await page.bodyText()).includes('discard me'))
    assert.ok(await page.exists(dialog), 'Escape closed the edit, not the dialog')

    // Delete the first note after a confirmation; Cancel keeps it.
    await page.click(inDialog({ role: 'button', name: 'Actions for note 1' }))
    await page.click({ role: 'menuitem', name: 'Delete' })
    await page.waitForElement({ role: 'dialog', name: 'Delete this note?' })
    await page.click({ role: 'button', name: 'Cancel', within: { role: 'dialog', name: 'Delete this note?' } })
    await page.waitForGone({ role: 'dialog', name: 'Delete this note?' })
    assert.equal((await e.app.apiOk('GET', `/api/outages/${outage.id}/notes`)).notes.length, 3)
    await page.click(inDialog({ role: 'button', name: 'Actions for note 1' }))
    await page.click({ role: 'menuitem', name: 'Delete' })
    await page.click({ role: 'button', name: 'Delete note', within: { role: 'dialog', name: 'Delete this note?' } })
    await page.waitForGone({ role: 'dialog', name: 'Delete this note?' })
    await page.waitFor(n => !document.body.innerText.includes(n), { args: [n1], message: 'the deleted note to disappear' })
    thread = (await e.app.apiOk('GET', `/api/outages/${outage.id}/notes`)).notes
    assert.deepEqual(thread.map(n => n.body), [edited, n3])

    // The table row shows the newest note and the count; the outage list carries both.
    await page.click(inDialog({ role: 'button', name: 'Done' }))
    await page.waitForGone(dialog)
    await page.waitForElement({ text: n3 })
    await page.waitForElement({ role: 'button', name: /Notes for this outage, 2 in total/ })
    const listed = (await e.app.apiOk('GET', `/api/monitors/${mon.id}/outages`)).outages[0]
    assert.equal(listed.noteCount, 2)
    assert.equal(listed.note, n3)

    // The old single-note call still works: it sets "the" note, i.e. rewrites the newest note of the same author.
    await e.app.apiOk('POST', `/api/outages/${outage.id}/note`, { note: 'legacy client note' })
    thread = (await e.app.apiOk('GET', `/api/outages/${outage.id}/notes`)).notes
    assert.equal(thread.length, 2)
    assert.equal(thread[1].body, 'legacy client note')
    assert.ok(thread[1].editedAt > 0)

    // The monitor page's outage timeline reaches the same thread.
    await page.goto(e.url(`/monitors/${mon.id}`))
    await page.waitForElement({ role: 'heading', name: mon.name })
    await page.click({ role: 'tab', name: 'Events', within: { css: '[role=tablist][aria-label="Detail views"]' } })
    await page.waitForElement({ role: 'button', name: /Notes \(2\)/ })
    await page.click({ role: 'button', name: /Notes \(2\)/ })
    await page.waitForElement({ text: 'legacy client note' })
    await page.fill({ role: 'textbox', name: 'Add a note' }, 'added from the monitor page')
    await page.click({ role: 'button', name: 'Add note' })
    await page.waitForElement({ role: 'button', name: 'Hide notes' })
    assert.equal((await e.app.apiOk('GET', `/api/outages/${outage.id}/notes`)).notes.length, 3)
    page.assertNoProblems({ ignore: [/status of 404/] })
  })

  it('outage notes fit a 375px screen', async t => {
    const { mon, outage } = await downOutage(e)
    await e.app.apiOk('POST', `/api/outages/${outage.id}/notes`, { body: 'A long note '.repeat(40) })
    const page = await e.session(t, { viewport: 'mobile', goto: `/incidents?tab=outages&q=${encodeURIComponent(mon.name)}` })
    await page.waitForElement({ text: mon.name })
    await page.click({ role: 'button', name: /Notes for this outage/ })
    await page.waitForElement({ role: 'dialog', name: 'Outage notes' })
    await page.waitForElement(inDialog({ role: 'textbox', name: 'Add a note' }))
    const dims = await page.evaluate(() => {
      const d = document.querySelector('[role=dialog]')
      return { sw: document.documentElement.scrollWidth, iw: innerWidth, dw: d.scrollWidth, dc: d.clientWidth }
    })
    assert.ok(dims.sw <= dims.iw, `page overflows horizontally: ${dims.sw} > ${dims.iw}`)
    assert.ok(dims.dw <= dims.dc + 1, `dialog overflows horizontally: ${dims.dw} > ${dims.dc}`)
    page.assertNoProblems({ ignore: [/status of 404/] })
  })

  it('incident: edit an update in place, flip it to a team note, never on the public page', async t => {
    const mon = await e.app.apiOk('POST', '/api/monitors', monitorBody({ name: unique('inc-note-svc'), type: 'tcp', target: e.target.tcpAddr, intervalS: 10 }))
    const slug = unique('notepage')
    await e.app.apiOk('POST', '/api/pages', {
      slug, title: `Notes page ${slug}`, description: '', visibility: 'public', layout: 'list', showLatency: false,
      alarmMuted: true, alarmDown: 'police-siren', alarmDegraded: 'modern-chime', logoUrl: '', brandColor: '', websiteUrl: '',
      supportUrl: '', footerText: '', showResponseTime: false, historyDays: 90, subscriptionsEnabled: false, subscriberChannelId: null,
      items: [{ sort: 0, groupId: null, monitorId: mon.id, displayName: 'Checkout', description: '' }],
    })
    const pages = await e.app.apiOk('GET', '/api/pages')
    const pageId = (pages.pages ?? pages).find(p => p.slug === slug).id
    const title = unique('Edit me')
    const first = `Looking into it ${unique('first')}`
    const inc = await e.app.apiOk('POST', '/api/incidents', { title, impact: 'major', body: first, monitorIds: [mon.id], pageIds: [pageId] })

    const page = await e.session(t, { goto: `/incidents/${inc.id}` })
    await page.waitForElement({ role: 'heading', name: title })
    await page.waitForElement({ text: first })

    // Edit the first update's text.
    await page.click({ role: 'button', name: /^Actions for the update from/ })
    await page.click({ role: 'menuitem', name: 'Edit' })
    const fixed = `Looking into elevated errors on checkout ${unique('fixed')}`
    await page.fill({ role: 'textbox', name: 'Edit update text' }, fixed)
    await page.click({ role: 'button', name: 'Save' })
    await page.waitForElement({ text: 'Update saved.' })
    await page.waitForElement({ text: fixed })
    await page.waitForElement({ text: /\(edited/ })
    let got = await e.app.apiOk('GET', `/api/incidents/${inc.id}`)
    assert.equal(got.updates[0].body, fixed)
    assert.ok(got.updates[0].editedAt > 0)
    assert.equal(got.status, 'investigating', 'editing never moves the incident status')
    let pub = await fetch(`http://127.0.0.1:${e.app.port}/api/public/pages/${slug}`).then(r => r.text())
    assert.ok(pub.includes(fixed), 'the corrected text is what the public page shows')

    // Flip it to a team note: it leaves the public page.
    await page.click({ role: 'button', name: /^Actions for the update from/ })
    await page.click({ role: 'menuitem', name: 'Edit' })
    await page.click({ role: 'checkbox', name: /Team note/ })
    await page.click({ role: 'button', name: 'Save' })
    await page.waitForElement({ role: 'listitem', text: /Team note/ })
    got = await e.app.apiOk('GET', `/api/incidents/${inc.id}`)
    assert.equal(got.updates[0].internal, true)
    pub = await fetch(`http://127.0.0.1:${e.app.port}/api/public/pages/${slug}`).then(r => r.text())
    assert.ok(!pub.includes(fixed), 'a team note never reaches the public page')

    // Cancel leaves it as it was; the title is editable too.
    await page.click({ role: 'button', name: /^Actions for the team note from/ })
    await page.click({ role: 'menuitem', name: 'Edit' })
    await page.fill({ role: 'textbox', name: 'Edit update text' }, 'scrapped')
    await page.click({ role: 'button', name: 'Cancel' })
    assert.ok(!(await page.bodyText()).includes('scrapped'))
    await page.click({ role: 'button', name: 'Edit', css: 'aside button' })
    await page.waitForElement({ role: 'dialog', name: 'Edit incident' })
    const renamed = unique('Renamed')
    await page.fill(inDialog({ label: /^Title$/ }), renamed)
    await page.click(inDialog({ role: 'button', name: 'Save' }))
    await page.waitForElement({ role: 'heading', name: renamed })
    assert.equal((await e.app.apiOk('GET', `/api/incidents/${inc.id}`)).title, renamed)
    page.assertNoProblems({ ignore: [/status of 404/] })
  })
})
