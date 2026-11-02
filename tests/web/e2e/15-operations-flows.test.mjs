// Day-to-day operations in the browser: declaring, updating and resolving an incident, scheduling maintenance
// windows, and the Regions settings tab when an outage has no region results.

import assert from 'node:assert/strict'
import { it } from 'node:test'
import { e2eSuite, unique } from './harness.mjs'
import { monitorBody } from './util.mjs'
import { setNative } from './forms.mjs'

const dialog = { role: 'dialog' }
const inDialog = spec => ({ ...spec, within: dialog })
const alertIn = { role: 'alert', within: dialog }
const pageBody = (slug, title, over = {}) => ({
  slug, title, description: '', visibility: 'public', layout: 'list', showLatency: false,
  alarmMuted: true, alarmDown: 'police-siren', alarmDegraded: 'modern-chime',
  logoUrl: '', brandColor: '', websiteUrl: '', supportUrl: '', footerText: '', showResponseTime: false, historyDays: 90,
  subscriptionsEnabled: false, subscriberChannelId: null, items: [], ...over,
})
/** "YYYY-MM-DDTHH:mm" in UTC, `offsetMin` minutes from now (what a datetime-local input holds for the UTC zone). */
const utcLocal = offsetMin => new Date(Date.now() + offsetMin * 60_000).toISOString().slice(0, 16)
const anonJson = async (e, p) => { const r = await fetch(`http://127.0.0.1:${e.app.port}${p}`); return { status: r.status, text: await r.text() } }

e2eSuite('operations flows', e => {
  it('declare an incident, post an update and a team note, resolve it, find it under Resolved, delete it', async t => {
    const mon = await e.app.apiOk('POST', '/api/monitors', monitorBody({ name: unique('inc-svc'), type: 'tcp', target: e.target.tcpAddr, intervalS: 10 }))
    const slug = unique('incpage')
    await e.app.apiOk('POST', '/api/pages', pageBody(slug, `Incident page ${slug}`, { items: [{ sort: 0, groupId: null, monitorId: mon.id, displayName: 'Checkout', description: '' }] }))
    const title = unique('Payments slow')
    const first = `We are investigating elevated errors ${unique('first')}`
    const page = await e.session(t, { goto: '/incidents' })
    await page.waitForElement({ role: 'heading', name: 'Incident response' })
    assert.match(await page.bodyText(), /No incidents/i, 'a fresh workspace has no incidents')

    // Declare: both a title and a first message are required.
    await page.click({ role: 'button', name: 'Declare incident' })
    await page.waitForElement({ role: 'dialog', name: 'New incident' })
    await page.click(inDialog({ role: 'button', name: 'Open incident' }))
    await page.waitForElement({ text: 'A title and a first message are required.' })
    assert.equal((await e.app.apiOk('GET', '/api/incidents')).incidents?.length ?? 0, 0, 'nothing was created')
    await page.fill(inDialog({ label: /^Title$/ }), title)
    await page.click(inDialog({ role: 'button', name: 'Open incident' }))
    await page.waitForElement({ text: 'A title and a first message are required.' })
    await page.fill(inDialog({ label: /^First message$/ }), first)
    await page.select(inDialog({ label: /^Impact$/ }), 'Major')
    await page.click(inDialog({ role: 'checkbox', name: mon.name }))
    await page.click(inDialog({ role: 'checkbox', name: new RegExp(`Incident page ${slug}`) }))
    await page.click(inDialog({ role: 'button', name: 'Open incident' }))
    await page.waitForGone(dialog)
    await page.waitForElement({ text: 'Incident opened.' })

    // It is in the inbox as open and linked to the monitor.
    const row = { role: 'link', name: title }
    await page.waitForElement(row)
    assert.match(await page.text(row), /Investigating/)
    assert.match(await page.text(row), /Major/)
    assert.match(await page.text(row), new RegExp(mon.name))
    const list = (await e.app.apiOk('GET', '/api/incidents')).incidents
    const inc = list.find(i => i.title === title)
    assert.ok(inc, 'stored')
    assert.equal(inc.status, 'investigating')
    assert.equal(inc.impact, 'major')
    assert.deepEqual(inc.monitorIds, [mon.id])

    // Detail page: first message is the first update.
    await page.click(row)
    await page.waitForPath(`/incidents/${inc.id}`)
    await page.waitForElement({ role: 'heading', name: title })
    assert.match(await page.bodyText(), new RegExp(first))
    assert.match(await page.bodyText(), new RegExp(`INC-${inc.id}`))

    // An empty update is refused with a hint.
    await page.click({ role: 'button', name: 'Post update' })
    await page.waitForElement({ text: 'Write what changed first.' })
    const update = `Cause found: bad deploy ${unique('upd')}`
    await page.select({ label: /^New status$/ }, 'Identified')
    await page.fill({ label: /^Update$/ }, update)
    await page.click({ role: 'button', name: 'Post update' })
    await page.waitForElement({ text: 'Update posted.' })
    await page.waitFor(u => document.body.innerText.includes(u), { args: [update], message: 'the update in the timeline' })
    assert.match(await page.text({ role: 'listitem', text: update }), /Identified/)
    assert.equal((await e.app.apiOk('GET', `/api/incidents/${inc.id}`)).status, 'identified')

    // A team note is labelled as such and never reaches the public page.
    await page.click({ role: 'radio', name: 'Team note' })
    const note = `INTERNAL-ONLY ${unique('note')}`
    await page.fill({ label: /^Note$/ }, note)
    await page.click({ role: 'button', name: 'Add note' })
    await page.waitForElement({ text: 'Note added.' })
    await page.waitFor(n => document.body.innerText.includes(n), { args: [note], message: 'the note in the timeline' })
    assert.match(await page.text({ role: 'listitem', text: note }), /Team note/)
    const pub = await anonJson(e, `/api/public/pages/${slug}`)
    assert.equal(pub.status, 200)
    assert.ok(pub.text.includes(title), 'the public page shows the incident')
    assert.ok(pub.text.includes(update), 'and the public update')
    assert.ok(!pub.text.includes(note), 'but never the internal team note')

    // Resolve with a closing message.
    await page.click({ role: 'radio', name: 'Status update' })
    const closing = `All clear, rolled back ${unique('done')}`
    await page.fill({ label: /^Update$/ }, closing)
    await page.click({ role: 'button', name: 'Resolve with this message' })
    await page.waitForElement({ text: 'Incident resolved.' })
    await page.waitFor(() => /Resolved incident/.test(document.body.innerText), { message: 'the page to say the incident is resolved' })
    assert.equal(await page.exists({ role: 'button', name: 'Post update' }), false, 'a resolved incident takes no more updates')
    await page.waitForElement({ role: 'heading', name: 'Postmortem' })
    assert.equal((await e.app.apiOk('GET', `/api/incidents/${inc.id}`)).status, 'resolved')

    // Inbox filters.
    await page.goto(e.url('/incidents'))
    await page.waitForElement({ role: 'heading', name: 'Incident response' })
    await page.waitFor(() => /No open incidents|No incidents/i.test(document.body.innerText) || !document.querySelector('a[href^="/incidents/"]'), { message: 'a resolved incident is not in the open list' })
    assert.equal(await page.exists(row), false)
    await page.click({ role: 'button', name: /^Resolved/ })
    await page.waitForElement(row)
    assert.match(await page.text(row), /Resolved/)

    // Delete from the detail page with an in-app confirmation.
    await page.click(row)
    await page.waitForPath(`/incidents/${inc.id}`)
    await page.click({ role: 'button', name: 'Delete' })
    await page.waitForElement({ role: 'dialog', name: 'Delete this incident?' })
    await page.click(inDialog({ role: 'button', name: 'Cancel' }))
    await page.waitForGone(dialog)
    assert.equal((await e.app.api('GET', `/api/incidents/${inc.id}`)).status, 200)
    await page.click({ role: 'button', name: 'Delete' })
    await page.click(inDialog({ role: 'button', name: 'Delete incident' }))
    await page.waitForPath('/incidents')
    assert.equal((await e.app.api('GET', `/api/incidents/${inc.id}`)).status, 404)
    assert.equal(page.dialogs.length, 0, 'no native confirm()')
    page.assertNoProblems({ ignore: [/status of 404/] })
  })

  it('schedule maintenance: validation, a one-off window, a weekly window, edit, filters, delete', async t => {
    const mon = await e.app.apiOk('POST', '/api/monitors', monitorBody({ name: unique('maint-svc'), type: 'tcp', target: e.target.tcpAddr, intervalS: 10 }))
    const page = await e.session(t, { goto: '/maintenance' })
    await page.waitForElement({ role: 'heading', name: 'Maintenance schedule' })
    await page.waitForElement({ text: 'No maintenance windows' })

    const open = async () => {
      await page.click({ role: 'button', name: /^New window/ })
      await page.waitForElement({ role: 'dialog', name: 'New maintenance window' })
      await page.select(inDialog({ label: /^Timezone$/ }), 'UTC')
    }
    const create = () => page.click(inDialog({ role: 'button', name: 'Create window' }))

    // Each missing piece is named, in order, and nothing is saved.
    await open()
    await create()
    await page.waitForElement({ ...alertIn, text: 'Title is required.' })
    const title = unique('DB upgrade')
    await page.fill(inDialog({ label: /^Title$/ }), title)
    await create()
    await page.waitForElement({ ...alertIn, text: 'Pick a start and an end.' })
    await setNative(page, inDialog({ label: /^Starts/ }), utcLocal(24 * 60))
    await setNative(page, inDialog({ label: /^Ends/ }), utcLocal(24 * 60 - 30))
    await create()
    await page.waitForElement({ ...alertIn, text: 'The end must be after the start.' })
    await setNative(page, inDialog({ label: /^Ends/ }), utcLocal(24 * 60 + 60))
    await create()
    await page.waitForElement({ ...alertIn, text: 'Pick at least one monitor or group it applies to.' })
    assert.equal((await e.app.apiOk('GET', '/api/maintenance')).maintenance?.length ?? 0, 0, 'nothing was saved yet')
    await page.fill(inDialog({ label: /^Description$/ }), 'Failover rehearsal')
    await page.click(inDialog({ role: 'checkbox', name: mon.name }))
    await create()
    await page.waitForGone(dialog)
    await page.waitForElement({ text: 'Maintenance window created.' })
    // The calendar is the default view; the list is one toggle away.
    assert.equal(await page.onElement({ role: 'radio', name: /Calendar/ }, r => r.getAttribute('aria-checked')), 'true', 'the calendar is the default layout')
    await page.click({ role: 'radio', name: /List/ })

    const card = { css: 'article', text: title }
    await page.waitForElement(card)
    const cardText = await page.text(card)
    assert.match(cardText, /Upcoming/)
    assert.match(cardText, /Once/)
    assert.match(cardText, /Failover rehearsal/)
    assert.match(cardText, /Applies to: 1 monitor/)
    assert.match(cardText, new RegExp(mon.name), 'the target names stay available to assistive technology')
    assert.match(cardText, /Starts in/)
    const stored = (await e.app.apiOk('GET', '/api/maintenance')).maintenance.find(m => m.title === title)
    assert.equal(stored.kind, 'once')
    assert.equal(stored.tz, 'UTC')
    assert.deepEqual(stored.monitorIds, [mon.id])
    assert.ok(stored.endsAt - stored.startsAt === 60 * 60_000, 'one hour long')

    // A weekly window.
    const weekly = unique('Weekly patching')
    await open()
    await page.fill(inDialog({ label: /^Title$/ }), weekly)
    await page.select(inDialog({ label: /^Repeats$/ }), 'Every week')
    await page.click(inDialog({ role: 'checkbox', name: mon.name }))
    await create()
    await page.waitForElement({ ...alertIn, text: 'Pick at least one weekday.' })
    await page.click(inDialog({ role: 'button', name: 'Mon' }))
    await page.click(inDialog({ role: 'button', name: 'Wed' }))
    assert.equal(await page.onElement(inDialog({ role: 'button', name: 'Mon' }), b => b.getAttribute('aria-pressed')), 'true')
    await setNative(page, inDialog({ label: /^Starts at/ }), '02:30')
    await page.fill(inDialog({ label: /^For \(minutes\)/ }), '90')
    await create()
    await page.waitForGone(dialog)
    await page.waitForElement({ css: 'article', text: weekly })
    const wText = await page.text({ css: 'article', text: weekly })
    assert.match(wText, /Weekly/)
    assert.match(wText, /Every Mon, Wed at 02:30 UTC, for 90 min/)
    assert.match(wText, /Upcoming/)

    // An active window: it shows as active, ends soon, and the Overview counts it.
    const now = unique('Emergency patch')
    await open()
    await page.fill(inDialog({ label: /^Title$/ }), now)
    await setNative(page, inDialog({ label: /^Starts/ }), utcLocal(-30))
    await setNative(page, inDialog({ label: /^Ends/ }), utcLocal(30))
    await page.click(inDialog({ role: 'checkbox', name: mon.name }))
    await create()
    await page.waitForGone(dialog)
    await page.waitForElement({ css: 'article', text: now })
    assert.match(await page.text({ css: 'article', text: now }), /In progress/)
    assert.match(await page.text({ css: 'article', text: now }), /Ends /)
    // Filters with counts.
    const nav = { role: 'group', name: 'Filter maintenance windows' }
    assert.match(await page.text(nav), /Full schedule\s*3/)
    await page.click({ role: 'button', name: /^In progress/ })
    await page.waitFor(([a, b]) => !!document.body.innerText.includes(a) && !document.body.innerText.includes(b), { args: [[now, title]], message: 'only the active window under "In progress"' })
    await page.click({ role: 'button', name: /^Upcoming/ })
    await page.waitFor(([a, b]) => !!document.body.innerText.includes(a) && document.body.innerText.includes(b), { args: [[title, weekly]], message: 'both upcoming windows' })
    await page.click({ role: 'button', name: /^Past/ })
    await page.waitForElement({ text: 'No past maintenance' })
    await page.click({ role: 'button', name: 'View full schedule' })
    await page.waitForElement({ css: 'article', text: now })
    const overview = await e.session(t, { goto: '/' })
    await overview.waitFor(() => /Active maintenance\s*[1-9]/.test(document.querySelector('nav[aria-label="Operations summary"]')?.innerText ?? ''), { message: 'the Overview tile "Active maintenance" to count the active window' })
    await overview.close()

    // Edit the one-off window.
    await page.click({ role: 'button', name: 'Edit', within: card })
    await page.waitForElement({ role: 'dialog', name: 'Edit maintenance window' })
    assert.equal(await page.value(inDialog({ label: /^Title$/ })), title)
    const renamed = `${title} (moved)`
    await page.fill(inDialog({ label: /^Title$/ }), renamed)
    await page.click(inDialog({ role: 'button', name: 'Save' }))
    await page.waitForGone(dialog)
    await page.waitForElement({ text: 'Maintenance window saved.' })
    await page.waitForElement({ css: 'article', text: renamed })
    assert.ok((await e.app.apiOk('GET', '/api/maintenance')).maintenance.some(m => m.title === renamed))

    // Delete: Cancel keeps it, Delete window removes it.
    const deleteWindow = async () => {
      await page.click({ role: 'button', name: `More actions for ${renamed}` })
      await page.click({ role: 'menuitem', name: 'Delete window' })
    }
    await deleteWindow()
    await page.waitForElement({ role: 'dialog', name: `Delete "${renamed}"?` })
    await page.click(inDialog({ role: 'button', name: 'Cancel' }))
    await page.waitForGone(dialog)
    assert.equal(await page.exists({ css: 'article', text: renamed }), true)
    await deleteWindow()
    await page.click(inDialog({ role: 'button', name: 'Delete window' }))
    await page.waitForElement({ text: 'Maintenance window deleted.' })
    await page.waitForGone({ css: 'article', text: renamed })
    assert.equal((await e.app.apiOk('GET', '/api/maintenance')).maintenance.some(m => m.title === renamed), false)
    assert.equal(page.dialogs.length, 0, 'no native confirm()')
    page.assertNoProblems()
  })

  it('maintenance opens on the calendar and remembers the list only after it was chosen', async t => {
    const mon = await e.app.apiOk('POST', '/api/monitors', monitorBody({ name: unique('maint-view'), type: 'tcp', target: e.target.tcpAddr, intervalS: 10 }))
    const start = Date.now() + 3_600_000
    await e.app.apiOk('POST', '/api/maintenance', { title: unique('View window'), description: '', kind: 'once', startsAt: start, endsAt: start + 3_600_000, tz: 'UTC', active: true, weekdays: 0, startMinute: 0, durationMin: 0, monitorIds: [mon.id], groupIds: [] })
    const page = await e.session(t, { goto: '/maintenance' })
    await page.waitForElement({ role: 'grid' })
    assert.equal(await page.evaluate(() => localStorage.getItem('opskest-maintenance-view')), null, 'nothing is stored before the person chooses')
    assert.equal(await page.exists({ css: 'article' }), false, 'no list rows on the calendar')
    assert.match(await page.bodyText(), /Times shown in/)
    await page.click({ role: 'radio', name: /List/ })
    await page.waitForElement({ css: 'article' })
    assert.equal(await page.evaluate(() => localStorage.getItem('opskest-maintenance-view')), 'list')
    await page.reload()
    await page.waitForElement({ css: 'article' })
    await page.click({ role: 'radio', name: /Calendar/ })
    await page.waitForElement({ role: 'grid' })
    page.assertNoProblems()
  })

  it('Regions: an outage without region results does not break the tab, the outage list or the monitor page', async t => {
    // A monitor pointed at a closed port goes down on its first check and opens an outage; no worldwide
    // (Globalping) check ever runs here, so the outage has no region results at all.
    const mon = await e.app.apiOk('POST', '/api/monitors', monitorBody({ name: unique('down-svc'), type: 'tcp', target: '127.0.0.1:1', intervalS: 10, timeoutMs: 2000 }))
    let outage
    for (let i = 0; i < 80 && !outage; i++) {
      outage = ((await e.app.apiOk('GET', '/api/outages?limit=50')).outages ?? []).find(o => o.monitorId === mon.id)
      if (!outage) await new Promise(r => setTimeout(r, 500))
    }
    assert.ok(outage, 'the monitor should have opened an outage within 40 seconds')
    assert.ok(!outage.regions || !outage.regions.total, 'this outage has no region results')

    const page = await e.session(t, { goto: '/settings?tab=regions' })
    await page.waitForElement({ text: /Check regions/ })
    await page.waitForElement({ text: /Recent outage confirmations/i })
    await page.waitForElement({ role: 'button', name: 'Save' })
    assert.equal(await page.exists({ text: /Something went wrong|Could not load/i }), false)
    assert.match(await page.bodyText(), /No confirmations yet/, 'an outage without results is simply not listed')
    // The same outage in the other places that show region results.
    await page.goto(e.url('/incidents?tab=outages'))
    await page.waitForElement({ text: mon.name })
    assert.equal(await page.exists({ text: /Something went wrong/i }), false)
    await page.goto(e.url(`/monitors/${mon.id}`))
    await page.waitForElement({ role: 'heading', name: mon.name })
    await page.settle()
    assert.equal(await page.exists({ text: /Something went wrong/i }), false)
    // The API agrees.
    const regions = await e.app.apiOk('GET', '/api/regions')
    assert.ok(Array.isArray(regions.recent))
    assert.equal(regions.recent.some(r => r.monitorId === mon.id), false)
    const per = await e.app.api('GET', `/api/monitors/${mon.id}/regions`)
    assert.equal(per.status, 200)
    page.assertNoProblems()
  })
})
