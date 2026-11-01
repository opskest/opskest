// The Subscribe dialog on a public status page (Email, RSS, Chat tools): feed and chat instructions, the email double opt-in
// from sign-up to unsubscribe (through a real local SMTP sink), options the page does not offer
// staying hidden, and the dialog behaving on a phone and from the keyboard.

import assert from 'node:assert/strict'
import net from 'node:net'
import { after, it } from 'node:test'
import { e2eSuite, unique } from './harness.mjs'
import { monitorBody } from './util.mjs'

/** A tiny SMTP server that records every message it is given. */
async function smtpSink() {
  const messages = []
  const sockets = new Set()
  const server = net.createServer(sock => {
    sockets.add(sock)
    sock.on('close', () => sockets.delete(sock))
    sock.on('error', () => {})
    let data = null
    let buf = ''
    sock.write('220 sink ESMTP\r\n')
    sock.on('data', d => {
      buf += d.toString('latin1')
      for (;;) {
        if (data !== null) {
          const end = buf.indexOf('\r\n.\r\n')
          if (end < 0) { data = data + buf; buf = ''; return }
          messages.push(data + buf.slice(0, end))
          data = null
          buf = buf.slice(end + 5)
          sock.write('250 queued\r\n')
          continue
        }
        const i = buf.indexOf('\r\n')
        if (i < 0) return
        const line = buf.slice(0, i).toUpperCase()
        buf = buf.slice(i + 2)
        if (line.startsWith('EHLO') || line.startsWith('HELO')) sock.write('250-sink\r\n250 8BITMIME\r\n')
        else if (line.startsWith('MAIL') || line.startsWith('RCPT') || line.startsWith('RSET') || line.startsWith('NOOP')) sock.write('250 ok\r\n')
        else if (line.startsWith('DATA')) { sock.write('354 go\r\n'); data = '' }
        else if (line.startsWith('QUIT')) { sock.write('221 bye\r\n'); sock.end(); return }
        else sock.write('250 ok\r\n')
      }
    })
  })
  await new Promise(r => server.listen(0, '127.0.0.1', r))
  /** The decoded text of every message so far (quoted-printable undone). */
  const texts = () => messages.map(m => m.replace(/=\r\n/g, '').replace(/=([0-9A-F]{2})/g, (_, h) => String.fromCharCode(parseInt(h, 16))))
  return { port: server.address().port, messages, texts, close: () => new Promise(r => { for (const s of sockets) s.destroy(); server.close(() => r()) }) }
}

const pageBody = (slug, title, over = {}) => ({
  slug, title, description: 'subscribe page', visibility: 'public', layout: 'list', showLatency: false,
  alarmMuted: true, alarmDown: 'police-siren', alarmDegraded: 'modern-chime',
  logoUrl: '', brandColor: '', websiteUrl: '', supportUrl: '', footerText: '', showResponseTime: false, historyDays: 90,
  subscriptionsEnabled: true, subscriberChannelId: null, items: [], ...over,
})

e2eSuite('status page subscribe', e => {
  let sink
  after(async () => { await sink?.close() })
  async function setup({ withEmail }) {
    const mon = await e.app.apiOk('POST', '/api/monitors', monitorBody({ name: unique('svc'), type: 'tcp', target: e.target.tcpAddr, intervalS: 10, timeoutMs: 5000 }))
    let channelId = null
    if (withEmail) {
      sink ??= await smtpSink()
      const ch = await e.app.apiOk('POST', '/api/channels', { name: unique('status-mail'), type: 'email', isDefault: false, config: { host: '127.0.0.1', port: sink.port, from: 'status@example.com', to: ['ops@example.com'], tlsMode: 'none' } })
      channelId = ch.id
    }
    const slug = unique('sub')
    const created = await e.app.apiOk('POST', '/api/pages', pageBody(slug, `Sub ${slug}`, { subscriberChannelId: channelId, items: [{ sort: 0, groupId: null, monitorId: mon.id, displayName: 'Checkout', description: '' }] }))
    return { slug, id: created.id, mon }
  }
  const subscribeButton = { role: 'button', name: 'Subscribe to status updates' }

  it('the RSS and Chat tools tabs give the feed URLs with Copy, chat instructions, and the JSON summary', async t => {
    const { slug } = await setup({ withEmail: false })
    const page = await e.anonymous(t, { goto: `/status/${slug}` })
    await page.click(subscribeButton)
    await page.waitForElement({ role: 'dialog', name: 'Subscribe to updates' })
    // Without an email channel there is no Email tab; RSS and Chat tools remain.
    const tabs = await page.evaluate(() => [...document.querySelectorAll('[role=dialog] [role=tab]')].map(x => x.textContent.trim()))
    assert.deepEqual(tabs, ['RSS', 'Chat tools'], `email is hidden when the page cannot send it (tabs: ${tabs})`)
    const rssUrl = e.url(`/api/public/pages/${slug}/feed.rss`)
    const atomUrl = e.url(`/api/public/pages/${slug}/feed.atom`)
    const rssDom = await page.evaluate(() => {
      const d = document.querySelector('[role=dialog]')
      return {
        rss: d.querySelector('input[aria-label="RSS feed URL"]')?.value,
        atom: d.querySelector('input[aria-label="Atom feed URL"]')?.value,
        labels: [...d.querySelectorAll('label')].map(l => l.textContent.trim()),
        links: [...d.querySelectorAll('a')].map(a => ({ text: a.textContent.trim(), href: a.href })),
        text: d.innerText,
      }
    })
    assert.equal(rssDom.rss, rssUrl)
    assert.equal(rssDom.atom, atomUrl)
    assert.ok(rssDom.labels.includes('Get the RSS feed') && rssDom.labels.includes('Get the Atom feed'))
    assert.ok(rssDom.links.some(l => /summary\.json/.test(l.text) && l.href === e.url(`/api/public/pages/${slug}/summary.json`)))
    assert.match(rssDom.text, /Developers/)
    // Copy confirms in words.
    await page.click({ role: 'button', name: 'Copy RSS feed URL' })
    await page.waitForElement({ text: 'Copied' })

    await page.click({ role: 'tab', name: 'Chat tools' })
    const chat = await page.evaluate(() => {
      const d = document.querySelector('[role=dialog]')
      return {
        slack: d.querySelector('input[aria-label="Slack command"]')?.value,
        teams: d.querySelector('input[aria-label="Microsoft Teams feed URL"]')?.value,
        google: d.querySelector('input[aria-label="Google Workspace Chat feed URL"]')?.value,
        text: d.innerText,
      }
    })
    assert.equal(chat.slack, `/feed subscribe ${rssUrl}`)
    assert.equal(chat.teams, rssUrl)
    assert.equal(chat.google, rssUrl)
    for (const word of ['Slack', 'Microsoft Teams', 'Google Workspace Chat', 'RSS app']) assert.match(chat.text, new RegExp(word))
    // The feeds it points at are real RSS and Atom.
    const rss = await fetch(rssUrl)
    assert.match(rss.headers.get('content-type') ?? '', /application\/rss\+xml/)
    assert.match(await rss.text(), /<rss version="2\.0">/)
    const atom = await fetch(atomUrl)
    assert.match(atom.headers.get('content-type') ?? '', /application\/atom\+xml/)
    assert.match(await atom.text(), /<feed xmlns="http:\/\/www\.w3\.org\/2005\/Atom">/)
    page.assertNoProblems()
  })

  it('Escape closes the dialog and gives focus back to the Subscribe button', async t => {
    const { slug } = await setup({ withEmail: false })
    const page = await e.anonymous(t, { goto: `/status/${slug}` })
    await page.click(subscribeButton)
    await page.waitForElement({ role: 'dialog', name: 'Subscribe to updates' })
    await page.press('Escape')
    await page.waitForGone({ role: 'dialog', name: 'Subscribe to updates' })
    assert.match((await page.activeElement())?.name ?? '', /Subscribe to status updates/)
    // Reopening and tabbing never leaves the dialog.
    await page.press('Enter')
    await page.waitForElement({ role: 'dialog', name: 'Subscribe to updates' })
    for (let i = 0; i < 12; i++) await page.press('Tab')
    assert.ok(await page.evaluate(() => !!document.activeElement?.closest('[role=dialog]')), 'focus stays inside the open dialog')
  })

  it('on a phone the dialog is a full-width sheet inside the viewport', async t => {
    const { slug } = await setup({ withEmail: false })
    const page = await e.anonymous(t, { goto: `/status/${slug}`, viewport: 'mobile' })
    await page.click(subscribeButton)
    await page.waitForElement({ role: 'dialog', name: 'Subscribe to updates' })
    await page.click({ role: 'tab', name: 'Chat tools' })
    const m = await page.evaluate(() => {
      const r = document.querySelector('[role=dialog]').getBoundingClientRect()
      return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, iw: innerWidth, ih: innerHeight, over: document.documentElement.scrollWidth - innerWidth }
    })
    assert.ok(m.left >= 0 && m.right <= m.iw, `inside the width: ${m.left}..${m.right} of ${m.iw}`)
    assert.ok(m.top >= 0 && m.bottom <= m.ih, `inside the height: ${m.top}..${m.bottom} of ${m.ih}`)
    assert.ok(m.right - m.left >= m.iw - 32, 'a full-width sheet')
    assert.ok(m.over <= 0, 'no sideways scroll')
  })

  it('email: neutral answer, confirmation link, incident mail, unsubscribe', async t => {
    const { slug, id, mon } = await setup({ withEmail: true })
    const address = `reader-${Date.now()}@example.com`
    const page = await e.anonymous(t, { goto: `/status/${slug}` })
    await page.click(subscribeButton)
    await page.waitForElement({ role: 'dialog', name: 'Subscribe to updates' })
    await page.fill({ css: '[role=dialog] input[type=email]' }, address)
    await page.press('Enter')
    const note = async () => page.waitFor(() => document.querySelector('[role=dialog] [role=status]')?.textContent || '', { message: 'a neutral answer' }).then(() => page.text({ css: '[role=dialog] [role=status]' }))
    const first = await note()
    assert.match(first, /If that address can receive mail/)
    // The same address again, and a different one, get the very same answer.
    const again = await e.app.api('POST', `/api/public/pages/${slug}/subscribe`, { email: address })
    const other = await e.app.api('POST', `/api/public/pages/${slug}/subscribe`, { email: `other-${Date.now()}@example.com` })
    assert.equal(again.status, 202)
    assert.deepEqual(again.json, other.json, 'the answer never reveals whether an address is already known')
    assert.equal((await e.app.api('POST', `/api/public/pages/${slug}/subscribe`, { email: 'not-an-address' })).status, 400)

    // The confirmation mail arrives and its link works.
    const deadline = Date.now() + 20000
    let confirm
    while (!confirm && Date.now() < deadline) {
      confirm = sink.texts().map(x => x.match(/https?:\/\/[^\s"<>]*\/status\/[^\s"<>]*\/subscribe\/confirm\?token=[0-9a-f]{64}/)?.[0]).find(Boolean)
      if (!confirm) await new Promise(r => setTimeout(r, 300))
    }
    assert.ok(confirm, 'the confirmation mail carries a confirm link')
    const origin = new URL(e.url('/')).origin
    const confirmUrl = origin + new URL(confirm).pathname + new URL(confirm).search
    const c = await e.anonymous(t, { goto: confirmUrl.replace(origin, '') })
    await c.waitForElement({ role: 'heading', name: 'Email updates', level: 1 })
    await c.waitForElement({ text: /You’re subscribed/ })
    // A bad token is a clear failure, not a crash.
    const bad = await e.anonymous(t, { goto: `/status/${slug}/subscribe/confirm?token=${'0'.repeat(64)}` })
    await bad.waitForElement({ text: /invalid or has expired/ })

    // An incident on the page reaches the subscriber, with an unsubscribe link that works on click.
    await e.app.apiOk('POST', '/api/incidents', { title: 'Mail test incident', impact: 'minor', body: 'Looking into it', pageIds: [id], monitorIds: [mon.id] })
    let unsub
    const end = Date.now() + 20000
    while (!unsub && end > Date.now()) {
      unsub = sink.texts().map(x => x.match(/https?:\/\/[^\s"<>]*\/status\/[^\s"<>]*\/unsubscribe\?token=[0-9a-f]{64}/)?.[0]).find(Boolean)
      if (!unsub) await new Promise(r => setTimeout(r, 300))
    }
    assert.ok(unsub, 'incident mail carries an unsubscribe link')
    const u = await e.anonymous(t, { goto: new URL(unsub).pathname + new URL(unsub).search })
    await u.waitForElement({ role: 'heading', name: 'Unsubscribe', level: 1 })
    assert.ok(!sink.texts().some(x => /unsubscribed/i.test(x)), 'opening the link alone does not unsubscribe')
    await u.click({ role: 'button', name: 'Unsubscribe' })
    await u.waitForElement({ text: /you won’t get any more updates/ })
    const bad2 = await e.anonymous(t, { goto: `/status/${slug}/unsubscribe?token=${'f'.repeat(64)}` })
    await bad2.click({ role: 'button', name: 'Unsubscribe' })
    await bad2.waitForElement({ text: /invalid or you’re already unsubscribed/ })
  })

  it('subscribing is rate limited', async () => {
    const { slug } = await setup({ withEmail: true })
    let limited = false
    for (let i = 0; i < 40 && !limited; i++) {
      const r = await e.app.api('POST', `/api/public/pages/${slug}/subscribe`, { email: `burst-${i}-${Date.now()}@example.com` })
      limited = r.status === 429
    }
    assert.ok(limited, 'a burst of sign-ups is eventually answered 429')
  })
})
