import test from 'node:test'
import assert from 'node:assert/strict'
import { configureRuntime, serverRegion, serverTimeZone } from '../../web/src/lib/runtime.ts'
import { dateServer, serverDayStartMs, serverDayEndMs, serverDayOffsetMs, utcMsToZonedDateTime, ymdServer, zonedDateTimeToUtcMs } from '../../web/src/lib/format.ts'
import { normDaily } from '../../web/src/lib/normalize.ts'

test('installation metadata uses neutral fallbacks and accepts an American server', () => {
  configureRuntime({ timeZone: 'Not/AZone', serverRegion: ' ' })
  assert.equal(serverTimeZone(), 'UTC')
  assert.equal(serverRegion(), 'This server')
  configureRuntime({ timeZone: 'America/New_York', serverRegion: 'US East' })
  assert.equal(serverTimeZone(), 'America/New_York')
  assert.equal(serverRegion(), 'US East')
  assert.equal(ymdServer(Date.parse('2026-03-09T02:00:00Z')), '2026-03-08')
  assert.doesNotMatch(dateServer(Date.parse('2026-07-01T12:00:00Z')), /IST|India/)
})

test('maintenance wall times convert with DST, rejecting gaps and choosing the first repeated time', () => {
  assert.equal(zonedDateTimeToUtcMs('2026-03-08T02:30', 'America/New_York'), null)
  assert.equal(zonedDateTimeToUtcMs('2026-03-08T03:30', 'America/New_York'), Date.parse('2026-03-08T07:30:00Z'))
  assert.equal(zonedDateTimeToUtcMs('2026-11-01T01:30', 'America/New_York'), Date.parse('2026-11-01T05:30:00Z'))
  assert.equal(utcMsToZonedDateTime(Date.parse('2026-11-01T06:30:00Z'), 'America/New_York'), '2026-11-01T01:30')
  assert.equal(zonedDateTimeToUtcMs('2026-02-30T12:00', 'UTC'), null)
  assert.equal(zonedDateTimeToUtcMs('2026-05-20T12:00', 'Not/AZone'), null)
})

test('server daily history and inclusive outage ranges use 23- and 25-hour calendar days', () => {
  configureRuntime({ timeZone: 'America/New_York' })
  const spring = serverDayStartMs('2026-03-08')
  const autumn = serverDayStartMs('2026-11-01')
  assert.equal(spring, Date.parse('2026-03-08T05:00:00Z'))
  assert.equal(serverDayEndMs(spring) - spring, 23 * 60 * 60 * 1000)
  assert.equal(serverDayEndMs(autumn) - autumn, 25 * 60 * 60 * 1000)
  assert.equal(serverDayEndMs(spring) - 1, Date.parse('2026-03-09T03:59:59.999Z'))
  assert.equal(normDaily([{ day: '2026-03-08', uptime: 100, down: 0 }])[0].day, spring)
  assert.deepEqual([-2, -1, 0].map(offset => ymdServer(serverDayOffsetMs(Date.parse('2026-11-02T05:30:00Z'), offset))), ['2026-10-31', '2026-11-01', '2026-11-02'])
  assert.deepEqual([-2, -1, 0].map(offset => ymdServer(serverDayOffsetMs(Date.parse('2026-03-09T04:30:00Z'), offset))), ['2026-03-07', '2026-03-08', '2026-03-09'])
})

test('non-hour offset installations round-trip local times without any country-specific assumptions', () => {
  configureRuntime({ timeZone: 'Asia/Kathmandu', serverRegion: 'Kathmandu' })
  const instant = zonedDateTimeToUtcMs('2026-05-20T12:15', serverTimeZone())
  assert.equal(instant, Date.parse('2026-05-20T06:30:00Z'))
  assert.equal(utcMsToZonedDateTime(instant, serverTimeZone()), '2026-05-20T12:15')
})

test('a midnight DST jump starts daily history at the first real minute of that date', () => {
  configureRuntime({ timeZone: 'America/Santiago' })
  const start = serverDayStartMs('2026-09-06')
  assert.equal(start, Date.parse('2026-09-06T04:00:00Z'))
  assert.equal(ymdServer(start), '2026-09-06')
  assert.equal(serverDayEndMs(start), Date.parse('2026-09-07T03:00:00Z'))
  assert.equal(zonedDateTimeToUtcMs('2026-09-06T00:30', 'America/Santiago'), null)
})
