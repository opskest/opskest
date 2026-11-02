import test from 'node:test'
import assert from 'node:assert/strict'
import { configureRuntime } from '../../web/src/lib/runtime.ts'
import {
  addDays, addMonths, compareYmd, dayEndInZone, dayStartInZone, daysInMonth, daysTouched, diffDays, formatWallText,
  joinWall, monthGrid, occurrences, parseTimeText, parseWallText, parseYmd, splitWall, weekdayOf, ymdInZone,
} from '../../web/src/lib/calendar.ts'

test('parseYmd accepts real dates only', () => {
  assert.deepEqual(parseYmd('2026-02-28'), { y: 2026, m: 2, d: 28 })
  assert.equal(parseYmd('2026-02-29'), null)
  assert.deepEqual(parseYmd('2028-02-29'), { y: 2028, m: 2, d: 29 })
  assert.equal(parseYmd('2026-13-01'), null)
  assert.equal(parseYmd('2026-1-01'), null)
  assert.equal(daysInMonth(2026, 4), 30)
})

test('day and month arithmetic is calendar based', () => {
  assert.equal(addDays('2026-02-28', 1), '2026-03-01')
  assert.equal(addDays('2026-01-01', -1), '2025-12-31')
  assert.equal(addDays('2026-03-08', 1), '2026-03-09')
  assert.equal(addMonths('2026-01-31', 1), '2026-02-28')
  assert.equal(addMonths('2026-03-31', -1), '2026-02-28')
  assert.equal(addMonths('2026-12-15', 1), '2027-01-15')
  assert.equal(addMonths('2026-01-15', -2), '2025-11-15')
  assert.equal(addMonths('2026-01-15', 24), '2028-01-15')
  assert.equal(diffDays('2026-03-07', '2026-03-10'), 3)
  assert.equal(compareYmd('2026-03-07', '2026-03-10'), -1)
})

test('weekdays and the month grid', () => {
  assert.equal(weekdayOf('2026-03-08'), 0) // a Sunday
  assert.equal(weekdayOf('2026-03-09'), 1)
  const grid = monthGrid('2026-03-15', 1)
  assert.equal(grid.length, 6)
  assert.ok(grid.every(row => row.length === 7))
  // March 2026 starts on a Sunday, so a Monday-first grid begins on Monday 23 Feb.
  assert.equal(grid[0][0].ymd, '2026-02-23')
  assert.equal(grid[0][0].inMonth, false)
  assert.equal(grid[0][6].ymd, '2026-03-01')
  assert.equal(grid[0][6].inMonth, true)
  assert.equal(monthGrid('2026-03-15', 0)[0][0].ymd, '2026-03-01')
  const flat = grid.flat()
  assert.equal(flat.filter(d => d.inMonth).length, 31)
  // Every day is exactly one day after the previous: no gap or repeat, whatever the DST calendar says.
  for (let i = 1; i < flat.length; i++) assert.equal(diffDays(flat[i - 1].ymd, flat[i].ymd), 1)
})

test('instants map to the calendar date of the named zone', () => {
  // 03:30 UTC on 9 March is still the 8th on the US east coast and already the 9th in Tokyo.
  const at = Date.parse('2026-03-09T03:30:00Z')
  assert.equal(ymdInZone(at, 'America/New_York'), '2026-03-08')
  assert.equal(ymdInZone(at, 'Asia/Tokyo'), '2026-03-09')
  assert.equal(ymdInZone(at, 'UTC'), '2026-03-09')
  // Half-hour and 45-minute zones.
  assert.equal(ymdInZone(Date.parse('2026-03-09T18:40:00Z'), 'Asia/Kolkata'), '2026-03-10')
  assert.equal(ymdInZone(Date.parse('2026-03-09T18:20:00Z'), 'Asia/Kathmandu'), '2026-03-10')
})

test('calendar days around daylight saving are 23 and 25 hours with no off-by-one', () => {
  const tz = 'America/New_York'
  const spring = dayStartInZone('2026-03-08', tz)
  assert.equal(spring, Date.parse('2026-03-08T05:00:00Z'))
  assert.equal(dayEndInZone('2026-03-08', tz) - spring, 23 * 3_600_000)
  const autumn = dayStartInZone('2026-11-01', tz)
  assert.equal(dayEndInZone('2026-11-01', tz) - autumn, 25 * 3_600_000)
  // The last millisecond of a day belongs to that day; the next one belongs to the next.
  assert.equal(ymdInZone(dayEndInZone('2026-03-08', tz) - 1, tz), '2026-03-08')
  assert.equal(ymdInZone(dayEndInZone('2026-03-08', tz), tz), '2026-03-09')
  assert.equal(ymdInZone(dayEndInZone('2026-11-01', tz) - 1, tz), '2026-11-01')
  assert.equal(ymdInZone(dayEndInZone('2026-11-01', tz), tz), '2026-11-02')
  // A zone that jumps at midnight starts the day at its first real minute (Havana, 2026-03-08 00:00 -> 01:00).
  const havana = dayStartInZone('2026-03-08', 'America/Havana')
  assert.equal(ymdInZone(havana, 'America/Havana'), '2026-03-08')
  assert.equal(ymdInZone(havana - 1, 'America/Havana'), '2026-03-07')
  assert.equal(dayStartInZone('2026-02-30', tz), null)
})

test('wall text round-trips and rejects impossible values', () => {
  assert.equal(parseWallText('2026-05-20 09:05'), '2026-05-20T09:05')
  assert.equal(parseWallText('2026-05-20T09:05'), '2026-05-20T09:05')
  assert.equal(parseWallText('2026-05-20 24:00'), null)
  assert.equal(parseWallText('2026-05-20 09:60'), null)
  assert.equal(parseWallText('2026-02-30 09:00'), null)
  assert.equal(parseWallText('tomorrow'), null)
  assert.equal(formatWallText('2026-05-20T09:05'), '2026-05-20 09:05')
  assert.equal(formatWallText('nope'), '')
  assert.deepEqual(splitWall('2026-05-20T23:59'), { ymd: '2026-05-20', minutes: 23 * 60 + 59 })
  assert.equal(joinWall('2026-05-20', 90), '2026-05-20T01:30')
  assert.equal(parseTimeText('02:30'), 150)
  assert.equal(parseTimeText('2:5'), 125)
  assert.equal(parseTimeText('0230'), 150)
  assert.equal(parseTimeText('7'), 420)
  assert.equal(parseTimeText('24:00'), null)
  assert.equal(parseTimeText('12:61'), null)
  assert.equal(parseTimeText('ab'), null)
})

test('a one-off window marks every day it touches, in the display zone', () => {
  const tz = 'America/New_York'
  // 23:00 on the 7th to 03:30 on the 8th local time crosses the spring-forward gap (02:00 -> 03:00).
  const start = Date.parse('2026-03-08T04:00:00Z') // 23:00 on the 7th EST
  const end = Date.parse('2026-03-08T07:30:00Z') // 03:30 on the 8th EDT
  assert.deepEqual(daysTouched(start, end, tz), ['2026-03-07', '2026-03-08'])
  // Ending exactly at local midnight does not mark the next day.
  const midnight = dayStartInZone('2026-03-09', tz)
  assert.deepEqual(daysTouched(start, midnight, tz), ['2026-03-07', '2026-03-08'])
  // The same instants seen from Tokyo land on different dates, still without gaps.
  assert.deepEqual(daysTouched(start, end, 'Asia/Tokyo'), ['2026-03-08'])
  assert.deepEqual(daysTouched(end, end, tz), ['2026-03-08'])
})

test('weekly windows expand in their own zone and keep their wall time across DST', () => {
  const w = { kind: 'weekly', startsAt: null, endsAt: null, weekdays: 1 << 6, startMinute: 150, durationMin: 60, tz: 'America/New_York' } // Sundays 02:30 for an hour
  const from = Date.parse('2026-03-01T00:00:00Z')
  const to = Date.parse('2026-03-31T00:00:00Z')
  const list = occurrences(w, from, to).map(o => o.start)
  // 8 March 02:30 does not exist in New York (spring forward) and is skipped; the other Sundays fire.
  assert.deepEqual(list.map(at => ymdInZone(at, 'America/New_York')), ['2026-03-01', '2026-03-15', '2026-03-22', '2026-03-29'])
  assert.equal(list[0], Date.parse('2026-03-01T07:30:00Z')) // EST
  assert.equal(list[1], Date.parse('2026-03-15T06:30:00Z')) // EDT: same wall time, an hour earlier in UTC
  // Seen from Tokyo the same windows are on Sunday evening local time: still one per week, dates in Tokyo.
  assert.deepEqual(list.map(at => ymdInZone(at, 'Asia/Tokyo')), ['2026-03-01', '2026-03-15', '2026-03-22', '2026-03-29'])
  // Monday-bit mapping: bit 0 = Monday.
  const monday = { ...w, weekdays: 1, tz: 'UTC', startMinute: 0 }
  assert.deepEqual(occurrences(monday, Date.parse('2026-03-01T00:00:00Z'), Date.parse('2026-03-16T00:00:00Z')).map(o => ymdInZone(o.start, 'UTC')), ['2026-03-02', '2026-03-09'])
  // A window that runs past midnight is still found from the next day's range.
  const late = { ...w, tz: 'UTC', weekdays: 1, startMinute: 23 * 60 + 30, durationMin: 120 }
  assert.equal(occurrences(late, Date.parse('2026-03-03T00:00:00Z'), Date.parse('2026-03-03T01:00:00Z')).length, 1)
  assert.equal(occurrences({ kind: 'once', startsAt: from, endsAt: from + 1000, weekdays: 0, startMinute: 0, durationMin: 0, tz: 'UTC' }, to, to + 1).length, 0)
})

test('server zone configuration does not change pure calendar maths', () => {
  configureRuntime({ timeZone: 'Pacific/Auckland' })
  assert.equal(addDays('2026-09-27', 1), '2026-09-28')
  assert.equal(monthGrid('2026-09-27', 1).flat().length, 42)
  configureRuntime({ timeZone: 'UTC' })
})
