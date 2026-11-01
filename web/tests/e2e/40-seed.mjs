// Seeds an instance with 300+ rows behind every list (used by 40-bounded-lists.test.mjs and by hand for audits).

import { unique } from './harness.mjs'
import { monitorBody } from './util.mjs'

export const N = 320

const pageBody = (slug, title, over = {}) => ({
  slug, title, description: '', visibility: 'public', layout: 'list', showLatency: false, alarmMuted: true, alarmDown: 'police-siren', alarmDegraded: 'modern-chime',
  logoUrl: '', brandColor: '', websiteUrl: '', supportUrl: '', footerText: '', showResponseTime: false, historyDays: 90, subscriptionsEnabled: false, subscriberChannelId: null, items: [], ...over,
})

/** Runs `fn(i)` for 0..n-1 with a bounded number in flight. */
async function many(n, fn, width = 8) {
  let next = 0
  await Promise.all(Array.from({ length: width }, async () => {
    for (;;) {
      const i = next++
      if (i >= n) return
      await fn(i)
    }
  }))
}


export async function seedBounded(e) {
  const s = {}
  const api = (m, p, b) => e.app.apiOk(m, p, b)
  const tag = unique('bl')
  s.tag = tag
  // projects
  const groups = []
  for (let i = 0; i < 45; i++) groups.push((await api('POST', '/api/groups', { name: `${tag} project ${String(i).padStart(2, '0')}`, description: '' })).id)
  s.groups = groups
  // a webhook channel that cannot be reached: every alert it is asked to send lands in the delivery history as failed
  const ch = await api('POST', '/api/channels', { name: `${tag} hook`, type: 'webhook', isDefault: false, config: { url: 'https://bl-unreachable.invalid/hook' } })
  // monitors
  const mons = new Array(N)
  await many(N, async i => {
    mons[i] = await api('POST', '/api/monitors', monitorBody({ name: `${tag}-${String(i).padStart(3, '0')}`, type: 'push', target: '', intervalS: 3600, retryIntervalS: 3600, groupId: groups[i % groups.length] }))
  })
  s.mons = mons
  s.monitor = mons[0]
  // manual monitors flipped down and up again: every flip pair is an outage, and the alerts feed the delivery history
  s.manual = []
  for (const k of ['a', 'b', 'c']) {
    const m = await api('POST', '/api/monitors', monitorBody({ name: `${tag}-manual-${k}`, type: 'manual', target: '', intervalS: 3600, retryIntervalS: 3600, groupId: groups[0], channelIds: [ch.id] }))
    s.manual.push(m)
    for (let i = 0; i < 115; i++) {
      await api('POST', `/api/monitors/${m.id}/status`, { status: 'down', reason: `bounded-lists outage ${i}` })
      await api('POST', `/api/monitors/${m.id}/status`, { status: 'up', reason: '' })
    }
  }
  // third-party feeds (alert-only HTTP monitors), paused so they never fire
  await many(60, i => api('POST', '/api/monitors', monitorBody({ name: `${tag} feed ${i}`, type: 'http', target: `http://127.0.0.1:9/feed-${i}`, intervalS: 3600, retryIntervalS: 3600, paused: true, settings: { acceptedCodes: '200-299', vendor: true } })))
  // delivery history: resend one failed alert over and over (each attempt is one more row)
  const first = (await api('GET', '/api/alert-log?limit=5')).entries?.find(x => x.channelId && x.monitorId)
  if (first) await many(330, () => e.app.api('POST', `/api/alert-log/${first.id}/resend`, {}), 4)
  // a big status page, plus 60 small ones
  const slug = unique('bigpage')
  s.slug = slug
  const big = await api('POST', '/api/pages', pageBody(slug, 'Bounded big page', { items: mons.map((m, i) => ({ sort: i, groupId: null, monitorId: m.id, displayName: m.name, description: '' })) }))
  s.pageId = big.id
  const boardSlug = unique('bigboard')
  s.boardSlug = boardSlug
  await api('POST', '/api/pages', pageBody(boardSlug, 'Bounded big board', { layout: 'board', items: mons.slice(0, 200).map((m, i) => ({ sort: i, groupId: null, monitorId: m.id, displayName: m.name, description: '' })) }))
  await many(60, i => api('POST', '/api/pages', pageBody(`${tag}-p${i}`, `${tag} page ${i}`)))
  // incidents: 320, the first with a long timeline, all shown on the big page, most resolved
  await many(N, async i => {
    const inc = await api('POST', '/api/incidents', { title: `${tag} incident ${i}`, impact: ['minor', 'major', 'maintenance'][i % 3], status: 'investigating', monitorIds: [mons[i % N].id], pageIds: [big.id], message: `public message ${i}` })
    if (i === 0) s.incident = inc
    if (i % 5 !== 0) await api('POST', `/api/incidents/${inc.id}/updates`, { status: 'resolved', body: 'fixed', internal: false })
  })
  for (let i = 0; i < 150; i++) await api('POST', `/api/incidents/${s.incident.id}/updates`, { status: 'monitoring', body: `timeline update ${i}`, internal: false })
  // maintenance windows
  const now = Date.now()
  await many(120, i => api('POST', '/api/maintenance', { title: `${tag} window ${i}`, description: '', kind: 'once', startsAt: now + (i + 1) * 3_600_000, endsAt: now + (i + 1) * 3_600_000 + 1_800_000, tz: 'UTC', active: true, weekdays: 0, startMinute: 0, durationMin: 0, monitorIds: [mons[i].id], groupIds: [] }))
  // users
  await many(40, i => api('POST', '/api/users', { username: `${tag}-u${i}`, password: `Pw-${unique('x')}-9zQ!${i}`, name: `User ${i}`, role: 'viewer' }))
  // backups
  for (let i = 0; i < 12; i++) await e.app.api('POST', '/api/backups', {})
  // notes on one outage
  const outages = await api('GET', `/api/monitors/${s.manual[0].id}/outages?limit=5`)
  const list = outages.outages ?? outages.items ?? outages
  s.outageId = list[0].id
  await many(160, i => api('POST', `/api/outages/${s.outageId}/notes`, { body: `note number ${i}` }), 4)
  // enough audit rows (every create above wrote one) and some more settings saves
  const audit = await api('GET', '/api/audit-log?limit=200')
  s.auditRows = audit.entries.length
return s
}
