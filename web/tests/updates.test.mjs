import test from 'node:test'
import assert from 'node:assert/strict'
import {
  IDLE_GRACE_MS, RESTART_TIMEOUT_MS, compareVersions, formatBytes, isActivePhase, isDevVersion, linkText,
  normalizePayload, parseInline, parseNotes, phaseLabel, pollAction, shouldShowBanner, stepStates, validFeedUrl, versionChanged,
} from '../src/lib/updates.ts'

test('compareVersions follows semver precedence, with v prefixes and build metadata', () => {
  assert.equal(compareVersions('1.2.3', '1.2.3'), 0)
  assert.equal(compareVersions('v1.2.3', '1.2.3'), 0)
  assert.equal(compareVersions('1.2.3+abc', '1.2.3+def'), 0)
  assert.equal(compareVersions('1.2.3', '1.10.0'), -1)
  assert.equal(compareVersions('2.0.0', '1.99.99'), 1)
  assert.equal(compareVersions('1.4', '1.4.0'), 0)
  // The semver.org precedence chain.
  const chain = ['1.0.0-alpha', '1.0.0-alpha.1', '1.0.0-alpha.beta', '1.0.0-beta', '1.0.0-beta.2', '1.0.0-beta.11', '1.0.0-rc.1', '1.0.0']
  for (let i = 0; i < chain.length - 1; i++) {
    assert.equal(compareVersions(chain[i], chain[i + 1]), -1, `${chain[i]} < ${chain[i + 1]}`)
    assert.equal(compareVersions(chain[i + 1], chain[i]), 1)
  }
  assert.equal(compareVersions('v1.4.0-rc.1', '1.3.9'), 1)
})

test('unparsable versions sort below real ones', () => {
  assert.equal(compareVersions('dev', '0.0.1'), -1)
  assert.equal(compareVersions('1.0.0', 'dev'), 1)
  assert.equal(compareVersions('dev', 'dev'), 0)
  assert.equal(compareVersions('', '1.0.0'), -1)
})

test('isDevVersion', () => {
  for (const v of ['dev', '', '(devel)', 'abc1234', 'latest', undefined, null]) assert.equal(isDevVersion(v), true, String(v))
  for (const v of ['1.2.3', 'v1.2.3', '0.1.0-rc.1', '2.0.0+build.5', '1.2']) assert.equal(isDevVersion(v), false, v)
})

test('versionChanged ignores the v prefix and failed reads', () => {
  assert.equal(versionChanged('1.2.3', 'v1.2.3'), false)
  assert.equal(versionChanged('1.2.3', '1.3.0'), true)
  assert.equal(versionChanged('1.3.0', '1.2.3'), true)
  assert.equal(versionChanged('1.2.3', ''), false)
})

test('phase labels are plain words and unknown phases do not leak identifiers', () => {
  assert.equal(phaseLabel('backing_up'), 'Backing up the database')
  assert.equal(phaseLabel('downloading'), 'Downloading')
  assert.equal(phaseLabel('verifying'), 'Verifying the download')
  assert.equal(phaseLabel('installing'), 'Installing')
  assert.equal(phaseLabel('restarting'), 'Restarting')
  assert.equal(phaseLabel('something_new'), 'Working')
  assert.equal(phaseLabel(undefined), 'Working')
  assert.equal(isActivePhase('idle'), false)
  assert.equal(isActivePhase('failed'), false)
  assert.equal(isActivePhase('restarting'), true)
  assert.equal(isActivePhase('checking'), true)
})

test('formatBytes', () => {
  assert.equal(formatBytes(0), '0 B')
  assert.equal(formatBytes(512), '512 B')
  assert.equal(formatBytes(1024), '1.0 KB')
  assert.equal(formatBytes(1536), '1.5 KB')
  assert.equal(formatBytes(12.3 * 1024 * 1024), '12.3 MB')
  assert.equal(formatBytes(1024 * 1024 - 1), '1.0 MB')
  assert.equal(formatBytes(3 * 1024 ** 3), '3.0 GB')
  assert.equal(formatBytes(-1), '-')
  assert.equal(formatBytes(Number.NaN), '-')
  assert.equal(formatBytes(null), '-')
})

test('parseNotes handles headings, lists, paragraphs, code and ignores rules', () => {
  const md = [
    '## What is new', '', 'Faster **checks** and `--flag` support.', 'Second line of the same paragraph.', '',
    '- one', '- two', '  continues', '* three', '', '1. first', '2. second', '', '---', '', '```', 'go run .', '```', '', '> quoted',
  ].join('\r\n')
  const blocks = parseNotes(md)
  assert.deepEqual(blocks.map(b => b.kind), ['heading', 'paragraph', 'list', 'list', 'code', 'paragraph'])
  assert.equal(blocks[0].level, 2)
  assert.equal(blocks[0].inlines[0].text, 'What is new')
  assert.deepEqual(blocks[1].inlines.map(i => i.kind), ['text', 'bold', 'text', 'code', 'text'])
  assert.match(blocks[1].inlines[4].text, /Second line of the same paragraph\.$/)
  assert.equal(blocks[2].ordered, false)
  assert.equal(blocks[2].items.length, 3)
  assert.equal(blocks[2].items[1][0].text, 'two continues')
  assert.equal(blocks[3].ordered, true)
  assert.equal(blocks[4].text, 'go run .')
  assert.equal(blocks[5].inlines[0].text, 'quoted')
})

test('parseNotes tolerates empty, unterminated and hostile input without producing markup', () => {
  assert.deepEqual(parseNotes(''), [])
  assert.deepEqual(parseNotes(null), [])
  assert.deepEqual(parseNotes('   \n\n'), [])
  const open = parseNotes('```\nnever closed')
  assert.deepEqual(open, [{ kind: 'code', text: 'never closed' }])
  const html = parseNotes('<script>alert(1)</script> <img src=x onerror=alert(1)>')
  assert.equal(html.length, 1)
  assert.equal(html[0].inlines[0].kind, 'text') // literal text; React escapes it
  assert.ok(parseNotes('x'.repeat(200_000))[0].inlines[0].text.length <= 50_000)
})

test('links become plain text with the URL shown', () => {
  const inl = parseInline('See [the guide](https://example.com/g) and [https://a.example](https://a.example).')
  const links = inl.filter(i => i.kind === 'link')
  assert.equal(links.length, 2)
  assert.equal(linkText(links[0]), 'the guide (https://example.com/g)')
  assert.equal(linkText(links[1]), 'https://a.example')
  const js = parseInline('[x](javascript:alert(1))')
  assert.equal(js[0].kind, 'link') // never an href in the UI; only displayed as text
})

const payload = (over = {}) => ({
  current: '1.3.0', installMethod: 'binary', canInstall: true,
  settings: { enabled: true, feedUrl: 'https://x', mode: 'notify', autoHour: 3, publicKeySet: true },
  check: { checkedAt: 1, error: '', latest: '1.4.0', available: true, notes: '', publishedAt: null, signatureVerified: true, asset: null },
  state: { phase: 'idle', percent: 0, error: '' }, canRollback: false,
  ...over,
})

test('shouldShowBanner', () => {
  assert.equal(shouldShowBanner(payload(), null), true)
  assert.equal(shouldShowBanner(payload(), '1.4.0'), false)
  assert.equal(shouldShowBanner(payload(), 'v1.4.0'), false)
  assert.equal(shouldShowBanner(payload(), '1.3.5'), true) // a newer release comes back after a dismissal
  assert.equal(shouldShowBanner(payload({ current: 'dev' }), null), false)
  assert.equal(shouldShowBanner(payload({ current: '1.4.0' }), null), false)
  assert.equal(shouldShowBanner(payload({ current: '1.5.0' }), null), false)
  assert.equal(shouldShowBanner(payload({ settings: { ...payload().settings, mode: 'off' } }), null), false)
  assert.equal(shouldShowBanner(payload({ settings: { ...payload().settings, enabled: false } }), null), false)
  assert.equal(shouldShowBanner(payload({ check: { ...payload().check, available: false } }), null), false)
  assert.equal(shouldShowBanner(null, null), false)
})

test('pollAction rides out the restart, then reloads into the new version', () => {
  const base = { startVersion: '1.3.0', sawActive: true, lostAt: null, startedAt: 1_000, now: 5_000 }
  assert.equal(pollAction(base, { ok: true, current: '1.3.0', phase: 'downloading' }), 'continue')
  assert.equal(pollAction(base, { ok: true, current: '1.3.0', phase: 'restarting' }), 'continue')
  assert.equal(pollAction(base, { ok: true, current: '1.3.0', phase: 'failed' }), 'failed')
  assert.equal(pollAction(base, { ok: true, current: '1.4.0', phase: 'idle' }), 'reload')
  assert.equal(pollAction(base, { ok: true, current: 'v1.3.0', phase: 'restarting' }), 'continue')
  // Server gone: expected for 90 s from the first failure, then a timeout.
  assert.equal(pollAction({ ...base, lostAt: null }, { ok: false }), 'continue')
  assert.equal(pollAction({ ...base, lostAt: 10_000, now: 10_000 + RESTART_TIMEOUT_MS }, { ok: false }), 'continue')
  assert.equal(pollAction({ ...base, lostAt: 10_000, now: 10_001 + RESTART_TIMEOUT_MS }, { ok: false }), 'timeout')
  // Back up on the same version and idle after activity: it finished without changing.
  assert.equal(pollAction({ ...base, lostAt: 9_000 }, { ok: true, current: '1.3.0', phase: 'idle' }), 'stalled')
})

test('pollAction allows a short idle window right after the request was accepted', () => {
  const ctx = { startVersion: '1.3.0', sawActive: false, lostAt: null, startedAt: 1_000, now: 1_000 + IDLE_GRACE_MS - 1 }
  assert.equal(pollAction(ctx, { ok: true, current: '1.3.0', phase: 'idle' }), 'continue')
  assert.equal(pollAction({ ...ctx, now: 1_000 + IDLE_GRACE_MS }, { ok: true, current: '1.3.0', phase: 'idle' }), 'stalled')
})

test('stepStates highlights the running step and leaves unknown phases pending', () => {
  assert.deepEqual(stepStates('downloading'), ['done', 'current', 'pending', 'pending', 'pending'])
  assert.deepEqual(stepStates('restarting'), ['done', 'done', 'done', 'done', 'current'])
  assert.deepEqual(stepStates('failed'), ['pending', 'pending', 'pending', 'pending', 'pending'])
  assert.deepEqual(stepStates('starting'), ['pending', 'pending', 'pending', 'pending', 'pending'])
})

test('validFeedUrl takes https anywhere and http only for loopback', () => {
  assert.equal(validFeedUrl('https://example.com/manifest.json'), true)
  assert.equal(validFeedUrl('http://127.0.0.1:18990/manifest.json'), true)
  assert.equal(validFeedUrl('http://localhost:8000/m.json'), true)
  assert.equal(validFeedUrl('http://[::1]:8000/m.json'), true)
  assert.equal(validFeedUrl('http://example.com/m.json'), false)
  assert.equal(validFeedUrl('ftp://localhost/m.json'), false)
  assert.equal(validFeedUrl('not a url'), false)
})

test('normalizePayload fills the newer fields an older server leaves out', () => {
  const n = normalizePayload({ current: '1.0.0', installMethod: 'binary', canInstall: true, canRollback: false })
  assert.equal(n.restartMode, 'self')
  assert.equal(n.nextCheckAt, null)
  assert.equal(n.autoSkipVersion, '')
  assert.equal(n.previousVersion, '')
  assert.equal(n.state.backupName, '')
  assert.equal(n.check.minVersionBlocked, false)
  assert.equal(n.settings.publicKeySource, '')
  assert.equal(normalizePayload({ ...n, restartMode: 'exit', nextCheckAt: 5 }).restartMode, 'exit')
})
