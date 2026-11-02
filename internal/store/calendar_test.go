package store

import (
	"testing"
	"time"
)

func TestCalendarDayStartAcrossMidnightGap(t *testing.T) {
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Fatal(err)
	}
	start, ok := CalendarDayStart(2026, time.September, 6, loc)
	want := time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC)
	if !ok || !start.Equal(want) {
		t.Fatalf("start = %v, want %v", start, want)
	}
	st := &Store{Loc: loc}
	if got := st.DayBucket(want.Add(12 * time.Hour).UnixMilli()); got != want.UnixMilli() {
		t.Fatalf("bucket = %v", time.UnixMilli(got))
	}
	end := NextCalendarDayStart(start, loc)
	if end.Sub(start) != 23*time.Hour || end.In(loc).Hour() != 0 {
		t.Fatalf("next day = %v, duration = %v", end, end.Sub(start))
	}
}

func TestCalendarDayStartOrdinaryDSTAndSkippedDate(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	for _, tc := range []struct {
		month time.Month
		day   int
		hours time.Duration
	}{{3, 8, 23}, {11, 1, 25}} {
		start, ok := CalendarDayStart(2026, tc.month, tc.day, loc)
		if !ok || NextCalendarDayStart(start, loc).Sub(start) != tc.hours*time.Hour {
			t.Fatalf("DST date: %+v, %v", tc, start)
		}
	}
	apia, _ := time.LoadLocation("Pacific/Apia")
	if _, ok := CalendarDayStart(2011, 12, 30, apia); ok {
		t.Fatal("skipped date must have no start")
	}
}
