package engine

import (
	"time"

	"opskest/internal/model"
	"opskest/internal/store"
)

// Uptime windows (spec §5.3/§5.4), kept in memory so no summary ever reads
// the database. Like Uptime Kuma's opskest-calculator, results are counted
// into fixed buckets — here UTC hours (25 slots, for the 24 h window) and
// DISPLAY_TZ days (91 slots, for 7/30/90 d) — seeded at boot from the
// rollup tables and incremented by every beat. Each slot remembers which
// bucket it holds, so a slot left over from an older hour or day is simply
// ignored by the range sums and overwritten when its index comes round
// again: gaps in checking never distort the result, and no explicit
// rollover pass is needed.
//
// Good = UP + DEGRADED + PENDING; bad = DOWN; UNKNOWN and maintenance
// beats count on neither side. Uptime % = good / (good + bad), or null
// when there is nothing to count.

const (
	hourMs      = int64(time.Hour / time.Millisecond)
	dayMs       = 24 * hourMs
	hourlySlots = 25
	dailySlots  = 91
)

type bucket struct {
	ts        int64 // bucket start, unix ms
	good, bad uint32
}

type windows struct {
	hourly [hourlySlots]bucket
	daily  [dailySlots]bucket
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// slot returns the slot for bucket ts, resetting it if it holds an older
// bucket. It returns nil for a bucket older than the one in the slot (a
// late beat outside the window), which must not clobber newer counts.
func slot(slots []bucket, ts, unit int64) *bucket {
	n := int64(len(slots))
	i := floorDiv(ts, unit) % n
	if i < 0 {
		i += n
	}
	b := &slots[i]
	switch {
	case b.ts == ts:
		return b
	case b.ts > ts:
		return nil
	}
	*b = bucket{ts: ts}
	return b
}

// add counts one beat of status st (maintenance beats never reach here).
func (w *windows) add(hourTs, dayTs int64, st model.Status) {
	var good, bad uint32
	switch {
	case st.Good():
		good = 1
	case st.Bad():
		bad = 1
	default:
		return
	}
	if b := slot(w.hourly[:], hourTs, hourMs); b != nil {
		b.good += good
		b.bad += bad
	}
	if b := slot(w.daily[:], dayTs, dayMs); b != nil {
		b.good += good
		b.bad += bad
	}
}

// addRollup seeds a slot from a stored rollup row at boot.
func addRollup(slots []bucket, unit int64, r store.Rollup) {
	b := slot(slots, r.Bucket, unit)
	if b == nil {
		return
	}
	b.good += uint32(r.Up + r.Degraded + r.Pending)
	b.bad += uint32(r.Down)
}

// sumRange adds up the slots whose bucket start lies in [from, to].
func sumRange(slots []bucket, from, to int64) (good, bad uint64) {
	for i := range slots {
		b := &slots[i]
		if b.ts >= from && b.ts <= to && (b.good|b.bad) != 0 {
			good += uint64(b.good)
			bad += uint64(b.bad)
		}
	}
	return good, bad
}

func pct(good, bad uint64) *float64 {
	if good+bad == 0 {
		return nil
	}
	v := float64(good) / float64(good+bad) * 100
	return &v
}

// uptimeSpans are the window boundaries for one "now", computed once per
// Summaries call rather than per monitor.
type uptimeSpans struct {
	hourNow, from24h              int64
	dayNow, from7, from30, from90 int64
}

func newUptimeSpans(now time.Time, loc *time.Location, dayBucket func(int64) int64) uptimeSpans {
	nowMs := now.UnixMilli()
	day := dayBucket(nowMs)
	midnight := time.UnixMilli(day).In(loc)
	back := func(days int) int64 { return midnight.AddDate(0, 0, -(days - 1)).UnixMilli() }
	return uptimeSpans{
		hourNow: store.HourBucket(nowMs),
		from24h: nowMs - 24*hourMs,
		dayNow:  day,
		from7:   back(7),
		from30:  back(30),
		from90:  back(90),
	}
}

func (w *windows) uptimes(sp uptimeSpans) (u24, u7, u30, u90 *float64) {
	u24 = pct(sumRange(w.hourly[:], sp.from24h, sp.hourNow))
	u7 = pct(sumRange(w.daily[:], sp.from7, sp.dayNow))
	u30 = pct(sumRange(w.daily[:], sp.from30, sp.dayNow))
	u90 = pct(sumRange(w.daily[:], sp.from90, sp.dayNow))
	return
}
