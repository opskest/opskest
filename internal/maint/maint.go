// Package maint evaluates maintenance windows purely in memory: a
// one-off window between two instants, or a weekly window on a set of
// weekdays at a local start minute in an IANA time zone. The engine
// calls ActiveSet every 15 s and on each maintenance edit; nothing here
// touches the database.
package maint

import (
	"strings"
	"time"
	"unicode/utf8"

	"opskest/internal/model"
	"opskest/internal/store"
)

const (
	KindOnce   = "once"
	KindWeekly = "weekly"

	maxOnceSpan = 30 * 24 * time.Hour
	// lookahead bounds NextStart for weekly windows.
	lookaheadDays = 8
)

// Invalid persisted zones are evaluated in UTC; mutations reject them.
func location(tz string) *time.Location {
	if loc, err := time.LoadLocation(tz); err == nil {
		return loc
	}
	return time.UTC
}

// weekdayBit returns the bitmask bit for t's weekday, Monday = bit 0.
func weekdayBit(wd time.Weekday) int {
	return (int(wd) + 6) % 7
}

// occurrence returns the start and end of a weekly window's occurrence
// that begins on the local calendar day of day (in loc), and whether the
// window runs on that weekday at all.
func occurrence(w store.Maintenance, day time.Time, loc *time.Location) (start, end time.Time, ok bool) {
	if w.Weekdays&(1<<weekdayBit(day.Weekday())) == 0 {
		return time.Time{}, time.Time{}, false
	}
	y, m, d := day.Date()
	start = time.Date(y, m, d, 0, w.StartMinute, 0, 0, loc)
	// A spring-forward gap has no occurrence at the requested local time.
	// Do not silently move maintenance to a different hour or day.
	wallMatches := func(t time.Time) bool {
		local := t.In(loc)
		ly, lm, ld := local.Date()
		return ly == y && lm == m && ld == d && local.Hour()*60+local.Minute() == w.StartMinute
	}
	if !wallMatches(start) {
		return time.Time{}, time.Time{}, false
	}
	// A fall-back fold runs once, at the first matching instant. IANA zones
	// include half-hour transitions, so inspect minute offsets rather than
	// assuming every DST change is exactly one hour.
	for minutes := 180; minutes > 0; minutes-- {
		candidate := start.Add(-time.Duration(minutes) * time.Minute)
		if wallMatches(candidate) {
			start = candidate
			break
		}
	}
	end = start.Add(time.Duration(w.DurationMin) * time.Minute)
	return start, end, true
}

// CurrentEnd returns the end of the window occurrence that is active at
// now, and false when the window is not active at now. The end is
// exclusive.
func CurrentEnd(w store.Maintenance, now time.Time) (time.Time, bool) {
	if !w.Active {
		return time.Time{}, false
	}
	switch w.Kind {
	case KindOnce:
		if w.StartsAt == nil || w.EndsAt == nil {
			return time.Time{}, false
		}
		n := now.UnixMilli()
		if n >= *w.StartsAt && n < *w.EndsAt {
			return time.UnixMilli(*w.EndsAt), true
		}
		return time.Time{}, false
	case KindWeekly:
		if w.DurationMin <= 0 {
			return time.Time{}, false
		}
		loc := location(w.TZ)
		local := now.In(loc)
		// A window lasts at most 1440 minutes, so only occurrences that
		// began today or yesterday (local) can contain now.
		var best time.Time
		found := false
		for back := 0; back <= 1; back++ {
			start, end, ok := occurrence(w, local.AddDate(0, 0, -back), loc)
			if !ok || now.Before(start) || !now.Before(end) {
				continue
			}
			if !found || end.After(best) {
				best, found = end, true
			}
		}
		return best, found
	}
	return time.Time{}, false
}

// IsActive reports whether the window covers now (start inclusive, end
// exclusive). Inactive (disabled) windows are never active.
func IsActive(w store.Maintenance, now time.Time) bool {
	_, ok := CurrentEnd(w, now)
	return ok
}

// NextStart returns the next start strictly after now: for a weekly window
// the next selected weekday's start within 8 days, for a once window its
// StartsAt when that is still in the future. Disabled windows have none.
func NextStart(w store.Maintenance, now time.Time) (time.Time, bool) {
	if !w.Active {
		return time.Time{}, false
	}
	switch w.Kind {
	case KindOnce:
		if w.StartsAt == nil || *w.StartsAt <= now.UnixMilli() {
			return time.Time{}, false
		}
		return time.UnixMilli(*w.StartsAt), true
	case KindWeekly:
		loc := location(w.TZ)
		local := now.In(loc)
		for d := 0; d <= lookaheadDays; d++ {
			start, _, ok := occurrence(w, local.AddDate(0, 0, d), loc)
			if ok && start.After(now) {
				return start, true
			}
		}
	}
	return time.Time{}, false
}

// ActiveSet returns the monitors under maintenance at now. A monitor is
// covered when an active window targets it directly or targets its group
// (resolved through groupOf at evaluation time, so monitors added to a
// group later are covered too). groupOf returns nil for ungrouped
// monitors. Only ids in monitorIDs can appear in the result.
func ActiveSet(ws []store.Maintenance, monitorIDs []int64, groupOf func(int64) *int64, now time.Time) map[int64]bool {
	var mons, groups map[int64]bool
	for i := range ws {
		if !IsActive(ws[i], now) {
			continue
		}
		for _, id := range ws[i].MonitorIDs {
			if mons == nil {
				mons = map[int64]bool{}
			}
			mons[id] = true
		}
		for _, id := range ws[i].GroupIDs {
			if groups == nil {
				groups = map[int64]bool{}
			}
			groups[id] = true
		}
	}
	out := map[int64]bool{}
	if mons == nil && groups == nil {
		return out
	}
	for _, id := range monitorIDs {
		if mons[id] {
			out[id] = true
			continue
		}
		if groups != nil && groupOf != nil {
			if g := groupOf(id); g != nil && groups[*g] {
				out[id] = true
			}
		}
	}
	return out
}

// Validate checks a window and returns field-keyed errors
// (empty when valid). It trims the title in place.
//   - title: 1–120 characters
//   - kind: once | weekly
//   - once: startsAt < endsAt, span ≤ 30 days
//   - weekly: weekdays 1..127, startMinute 0..1439, durationMin 1..1440
//   - tz: a valid IANA time zone (including UTC)
//   - at least one monitor or group target
func Validate(w *store.Maintenance) model.FieldErrors {
	fe := model.FieldErrors{}

	w.Title = strings.TrimSpace(w.Title)
	if n := utf8.RuneCountInString(w.Title); n < 1 || n > 120 {
		fe["title"] = "must be 1-120 characters"
	}
	if _, err := time.LoadLocation(w.TZ); err != nil || w.TZ == "" || w.TZ == "Local" {
		fe["tz"] = "must be a valid IANA time zone"
	}
	if len(w.MonitorIDs) == 0 && len(w.GroupIDs) == 0 {
		fe["targets"] = "pick at least one monitor or group"
	}

	switch w.Kind {
	case KindOnce:
		if w.StartsAt == nil {
			fe["startsAt"] = "is required"
		}
		if w.EndsAt == nil {
			fe["endsAt"] = "is required"
		}
		if w.StartsAt != nil && w.EndsAt != nil {
			span := time.Duration(*w.EndsAt-*w.StartsAt) * time.Millisecond
			switch {
			case span <= 0:
				fe["endsAt"] = "must be after the start"
			case span > maxOnceSpan:
				fe["endsAt"] = "window can last at most 30 days"
			}
		}
	case KindWeekly:
		if w.Weekdays < 1 || w.Weekdays > 127 {
			fe["weekdays"] = "pick at least one weekday"
		}
		if w.StartMinute < 0 || w.StartMinute > 1439 {
			fe["startMinute"] = "must be 0-1439"
		}
		if w.DurationMin < 1 || w.DurationMin > 1440 {
			fe["durationMin"] = "must be 1-1440 minutes"
		}
	default:
		fe["kind"] = "must be once or weekly"
	}
	return fe
}
