import test from 'node:test'
import assert from 'node:assert/strict'
import {
  draftFromOut, emptySecret, newDraft, parseThresholds, toMonitorIn, validateDraft, MONITOR_TYPES, NO_TARGET_TYPES, TARGET, unplacedErrors,
} from '../src/lib/monitorForm.ts'

const httpDraft = (over = {}, settings = {}) => {
  const d = newDraft(null)
  d.name = 'api'
  d.target = 'https://api.example.com/health'
  return { ...d, ...over, settings: { ...d.settings, ...settings } }
}

test('parseThresholds reads whole days separated by commas or spaces', () => {
  assert.deepEqual(parseThresholds('30, 14 7;1'), [30, 14, 7, 1])
  assert.deepEqual(parseThresholds(''), [])
  assert.equal(parseThresholds('30, x'), null)
  assert.equal(parseThresholds('1.5'), null)
})

test('certificate thresholds are validated and sent only for types that see certificates', () => {
  assert.equal(validateDraft(httpDraft({ certThresholds: '30, 14, 7, 1' }))['settings.certThresholds'], undefined)
  for (const bad of ['0', '400', '7, 7', '1,2,3,4,5,6,7,8,9', 'soon']) {
    assert.ok(validateDraft(httpDraft({ certThresholds: bad }))['settings.certThresholds'], `${bad} should be rejected`)
  }
  assert.deepEqual(toMonitorIn(httpDraft({ certThresholds: '30, 14' })).settings.certThresholds, [30, 14])
  const ping = { ...newDraft(null), type: 'ping', name: 'p', target: '10.0.0.1', certThresholds: '30' }
  assert.deepEqual(toMonitorIn(ping).settings.certThresholds, [])
  assert.equal(validateDraft(ping)['settings.certThresholds'], undefined)
})

test('proxy URL rules mirror the server', () => {
  const err = url => validateDraft(httpDraft({}, { proxyUrl: url }))['settings.proxyUrl']
  for (const ok of ['http://proxy:3128', 'https://proxy', 'socks5://10.0.0.1:1080', 'socks5h://tor:9050']) assert.equal(err(ok), undefined, ok)
  for (const bad of ['proxy:3128', 'ftp://proxy', 'http://u:p@proxy:3128', 'http://proxy:3128/x']) assert.ok(err(bad), bad)
})

test('OAuth2 needs a token URL, a client id and a secret; the secret counts if one is already stored', () => {
  const base = { authType: 'oauth2', oauthTokenUrl: 'https://idp/token', oauthClientId: 'id' }
  let fe = validateDraft(httpDraft({}, base))
  assert.ok(fe['secrets.oauthSecret'], 'a missing secret is reported')
  fe = validateDraft(httpDraft({ oauthSecret: { isSet: true, mode: 'keep', value: '' } }, base))
  assert.equal(fe['secrets.oauthSecret'], undefined)
  fe = validateDraft(httpDraft({ oauthSecret: { isSet: true, mode: 'clear', value: '' } }, base))
  assert.ok(fe['secrets.oauthSecret'], 'clearing the stored secret leaves none')
  fe = validateDraft(httpDraft({ oauthSecret: { isSet: false, mode: 'replace', value: 's' } }, { ...base, oauthTokenUrl: 'nope', oauthClientId: ' ' }))
  assert.ok(fe['settings.oauthTokenUrl'] && fe['settings.oauthClientId'])
})

test('a client certificate and its key go together', () => {
  const pem = { isSet: false, mode: 'replace', value: '-----BEGIN CERTIFICATE-----' }
  assert.ok(validateDraft(httpDraft({ clientCert: pem }))['secrets.clientCert'])
  assert.equal(validateDraft(httpDraft({ clientCert: pem, clientKey: { ...pem, value: 'k' } }))['secrets.clientCert'], undefined)
  assert.ok(validateDraft(httpDraft({}, { caCert: 'not pem' }))['settings.caCert'])
})

test('write-only secrets: kept as null, replaced as a string, and only http carries them', () => {
  const d = httpDraft({ proxyPass: emptySecret(true), oauthSecret: { isSet: false, mode: 'replace', value: 'shh' } }, { proxyUrl: 'http://p:1' })
  const s = toMonitorIn(d).secrets
  assert.equal(s.proxyPass, null)
  assert.equal(s.oauthSecret, 'shh')
  const tcp = { ...newDraft(null), type: 'tcp', name: 't', target: 'db:5432', oauthSecret: { isSet: false, mode: 'replace', value: 'shh' } }
  assert.equal(toMonitorIn(tcp).secrets.oauthSecret, null)
})

test('tcp send and expect', () => {
  const tcp = (settings) => ({ ...newDraft(null), type: 'tcp', name: 't', target: 'db:5432', settings: { ...newDraft(null).settings, ...settings } })
  assert.deepEqual(validateDraft(tcp({ send: 'PING\r\n', expect: '+PONG' })), {})
  assert.ok(validateDraft(tcp({ expect: '(', expectMode: 'regex' }))['settings.expect'])
  assert.ok(validateDraft(tcp({ expectMode: 'regex' }))['settings.expect'])
  assert.ok(validateDraft(tcp({ send: 'hex:zz' }))['settings.send'])
  assert.deepEqual(toMonitorIn(tcp({ send: 'PING', expect: 'PONG', expectMode: 'regex' })).settings.expectMode, 'regex')
})

test('group monitors: no target, children required, a timeout that is always valid', () => {
  const group = (settings, over = {}) => ({ ...newDraft(null), type: 'group', name: 'g', ...over, settings: { ...newDraft(null).settings, ...settings } })
  assert.ok(validateDraft(group({ children: [] }))['settings.children'])
  assert.deepEqual(validateDraft(group({ children: [1, 2] })), {})
  assert.ok(validateDraft(group({ children: [1, 2], groupMode: 'min', groupMin: 3 }))['settings.groupMin'])
  assert.equal(validateDraft(group({ children: [1, 2], groupMode: 'min', groupMin: 2 }))['settings.groupMin'], undefined)
  // Interval 10 s leaves only 9.5 s for a timeout; the hidden field must not fail validation or the server.
  const fast = group({ children: [1] }, { intervalS: 10, timeoutMs: 10000 })
  assert.deepEqual(validateDraft(fast), {})
  const body = toMonitorIn(fast)
  assert.equal(body.target, '')
  assert.ok(body.timeoutMs <= 9500 && body.timeoutMs >= 1000)
  assert.deepEqual(body.settings.children, [1])
  assert.equal(body.upsideDown, false)
})

test('manual monitors have no target and no upside-down mode', () => {
  const m = { ...newDraft(null), type: 'manual', name: 'nightly', upsideDown: true }
  assert.deepEqual(validateDraft(m), {})
  const body = toMonitorIn(m)
  assert.equal(body.target, '')
  assert.equal(body.upsideDown, false)
})

test('every no-target type is offered, and none needs a target entry', () => {
  const offered = MONITOR_TYPES.map(t => t.type)
  for (const t of NO_TARGET_TYPES) {
    assert.ok(offered.includes(t), `${t} is in the type picker`)
    assert.equal(TARGET[t], undefined)
  }
  for (const t of offered) if (!NO_TARGET_TYPES.includes(t)) assert.ok(TARGET[t], `${t} has a target label`)
})

test('saved group and certificate settings come back into the form', () => {
  const d = draftFromOut({
    id: 5, name: 'g', type: 'group', target: '', groupId: null, tagIds: [], channelIds: [], intervalS: 30, retryIntervalS: 20, retries: 0,
    timeoutMs: 9500, degradedMs: 0, sslWarnDays: 14, reminderMin: 0, upsideDown: false, notifyDegraded: false, paused: false,
    description: '', runbook: '', headers: [], secrets: { basicPass: { isSet: false }, bearerToken: { isSet: false } },
    settings: { children: [3, 4], groupMode: 'min', groupMin: 2, certThresholds: [30, 7] }, createdAt: 0, updatedAt: 0,
  })
  assert.deepEqual(d.settings.children, [3, 4])
  assert.equal(d.settings.groupMode, 'min')
  assert.equal(d.settings.groupMin, 2)
  assert.equal(d.certThresholds, '30, 7')
})

test('errors for fields the form shows are not listed as unplaced', () => {
  const errors = { 'secrets.oauthSecret': 'required', 'secrets.clientCert': 'x', 'settings.proxyUrl': 'x', 'settings.certThresholds': 'x', 'settings.children': 'x' }
  assert.deepEqual(unplacedErrors(errors, 'http').map(([k]) => k), ['settings.children'])
  assert.deepEqual(unplacedErrors({ 'settings.children': 'x' }, 'group'), [])
})
