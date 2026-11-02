package alert

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"opskest/internal/model"
	"opskest/internal/store"
)

// parseClock reads "HH:MM" as minutes since midnight.
func parseClock(s string) (int, bool) {
	h, m, ok := strings.Cut(s, ":")
	if !ok || len(h) != 2 || len(m) != 2 {
		return 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

// QuietProblems validates a channel's quiet hours, keyed like the API's
// field errors ("quiet.start", ...).
func QuietProblems(q *store.QuietHours) model.FieldErrors {
	fe := model.FieldErrors{}
	if q == nil || !q.Enabled {
		return fe
	}
	start, ok := parseClock(q.Start)
	if !ok {
		fe["quiet.start"] = "use HH:MM, 24-hour"
	}
	end, ok2 := parseClock(q.End)
	if !ok2 {
		fe["quiet.end"] = "use HH:MM, 24-hour"
	}
	if ok && ok2 && start == end {
		fe["quiet.end"] = "must differ from the start"
	}
	if len(q.Days) > 7 {
		fe["quiet.days"] = "at most 7 days"
	}
	seen := map[int]bool{}
	for _, d := range q.Days {
		if d < 0 || d > 6 || seen[d] {
			fe["quiet.days"] = "days are 0 (Sunday) to 6 (Saturday), each once"
			break
		}
		seen[d] = true
	}
	return fe
}

// QuietActive reports whether now falls inside q. now should already be in
// the display time zone: the window is read off its wall clock.
func QuietActive(q *store.QuietHours, now time.Time) bool {
	if q == nil || !q.Enabled {
		return false
	}
	start, ok1 := parseClock(q.Start)
	end, ok2 := parseClock(q.End)
	if !ok1 || !ok2 || start == end {
		return false
	}
	onDay := func(d int) bool {
		return len(q.Days) == 0 || slices.Contains(q.Days, ((d%7)+7)%7)
	}
	mins := now.Hour()*60 + now.Minute()
	day := int(now.Weekday())
	if start < end {
		return onDay(day) && mins >= start && mins < end
	}
	// The window runs past midnight: its evening part belongs to today, its
	// morning part to the day before.
	switch {
	case mins >= start:
		return onDay(day)
	case mins < end:
		return onDay(day - 1)
	}
	return false
}

// critical reports whether k is an event a quiet window may let through when
// the channel asks for it: a monitor going down, its reminders, and the
// recovery that ends the outage.
func critical(k model.EventKind) bool {
	switch k {
	case model.EvDown, model.EvReminder, model.EvRecovered:
		return true
	}
	return false
}

// quietHolds reports whether a message of kind k is held back by ch's quiet
// hours at time now (in the display time zone).
func quietHolds(ch store.Channel, k model.EventKind, now time.Time) bool {
	q := ch.Config.Quiet
	if q == nil || !QuietActive(q, now) {
		return false
	}
	return !(q.BypassCritical && critical(k))
}
