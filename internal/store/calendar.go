package store

import "time"

// CalendarDayStart returns the first existing wall-clock minute of a date.
// Some IANA zones advance at midnight, so time.Date(00:00) can normalize
// into the previous date. A wholly skipped calendar date has no start.
func CalendarDayStart(year int, month time.Month, day int, loc *time.Location) (time.Time, bool) {
	for minute := 0; minute < 24*60; minute++ {
		t := time.Date(year, month, day, 0, minute, 0, 0, loc)
		y, m, d := t.In(loc).Date()
		if y == year && m == month && d == day && t.Hour()*60+t.Minute() == minute {
			return t, true
		}
	}
	return time.Time{}, false
}

// NextCalendarDayStart advances dates, not the start's possibly nonzero hour.
func NextCalendarDayStart(start time.Time, loc *time.Location) time.Time {
	y, m, d := start.In(loc).Date()
	date := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	for offset := 1; offset <= 3; offset++ {
		next := date.AddDate(0, 0, offset)
		if t, ok := CalendarDayStart(next.Year(), next.Month(), next.Day(), loc); ok {
			return t
		}
	}
	return start.AddDate(0, 0, 1)
}
