package maint

import (
	"strings"
	"testing"
	"time"

	"opskest/internal/store"
)

var ist = mustLoc("Asia/Kolkata")

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		return time.FixedZone("IST", 19800)
	}
	return l
}

func ms(t time.Time) *int64 { v := t.UnixMilli(); return &v }

func utc(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
}

func once(start, end time.Time) store.Maintenance {
	return store.Maintenance{
		ID: 1, Title: "deploy", Kind: "once", TZ: "UTC", Active: true,
		StartsAt: ms(start), EndsAt: ms(end), MonitorIDs: []int64{1},
	}
}

func weekly(days, startMinute, dur int, tz string) store.Maintenance {
	return store.Maintenance{
		ID: 2, Title: "weekly", Kind: "weekly", TZ: tz, Active: true,
		Weekdays: days, StartMinute: startMinute, DurationMin: dur, MonitorIDs: []int64{1},
	}
}

// Fixed calendar used below: 2026-09-27 is a Sunday, 09-28 Monday,
// 09-29 Tuesday, 09-30 Wednesday, 10-01 Thursday.
func TestCalendarAnchors(t *testing.T) {
	if wd := utc(2026, 9, 28, 0, 0).Weekday(); wd != time.Monday {
		t.Fatalf("2026-09-28 is %v, want Monday", wd)
	}
}

func TestIsActiveOnce(t *testing.T) {
	w := once(utc(2026, 9, 28, 10, 0), utc(2026, 9, 28, 11, 0))
	cases := []struct {
		name string
		w    store.Maintenance
		now  time.Time
		want bool
	}{
		{"before", w, utc(2026, 9, 28, 9, 59), false},
		{"at start", w, utc(2026, 9, 28, 10, 0), true},
		{"inside", w, utc(2026, 9, 28, 10, 30), true},
		{"end exclusive", w, utc(2026, 9, 28, 11, 0), false},
		{"inactive flag", func() store.Maintenance { x := w; x.Active = false; return x }(), utc(2026, 9, 28, 10, 30), false},
		{"missing bounds", func() store.Maintenance { x := w; x.EndsAt = nil; return x }(), utc(2026, 9, 28, 10, 30), false},
	}
	for _, c := range cases {
		if got := IsActive(c.w, c.now); got != c.want {
			t.Errorf("%s: IsActive = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIsActiveWeeklyAcrossMidnight(t *testing.T) {
	// Monday (bit 0) at 23:00 IST for 120 minutes → until Tue 01:00 IST.
	w := weekly(1<<0, 23*60, 120, "Asia/Kolkata")
	cases := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"mon 22:59 ist", time.Date(2026, 9, 28, 22, 59, 0, 0, ist), false},
		{"mon 23:00 ist", time.Date(2026, 9, 28, 23, 0, 0, 0, ist), true},
		{"tue 00:30 ist", time.Date(2026, 9, 29, 0, 30, 0, 0, ist), true},
		{"tue 01:00 ist (end exclusive)", time.Date(2026, 9, 29, 1, 0, 0, 0, ist), false},
		{"tue 01:01 ist", time.Date(2026, 9, 29, 1, 1, 0, 0, ist), false},
		{"tue 23:30 ist (tuesday not selected)", time.Date(2026, 9, 29, 23, 30, 0, 0, ist), false},
		{"tue 00:30 ist expressed in utc", time.Date(2026, 9, 29, 0, 30, 0, 0, ist).UTC(), true},
	}
	for _, c := range cases {
		if got := IsActive(w, c.now); got != c.want {
			t.Errorf("%s: IsActive = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIsActiveWeeklyTimezone(t *testing.T) {
	// Wednesday (bit 2) 02:00 for 60 minutes.
	inUTC := weekly(1<<2, 120, 60, "UTC")
	inIST := weekly(1<<2, 120, 60, "Asia/Kolkata")

	wed0230UTC := utc(2026, 9, 30, 2, 30)                  // = Wed 08:00 IST
	wed0230IST := time.Date(2026, 9, 30, 2, 30, 0, 0, ist) // = Tue 21:00 UTC

	if !IsActive(inUTC, wed0230UTC) {
		t.Error("tz=UTC window should be active at Wed 02:30 UTC")
	}
	if IsActive(inUTC, wed0230IST) {
		t.Error("tz=UTC window should not be active at Wed 02:30 IST (Tue 21:00 UTC)")
	}
	if !IsActive(inIST, wed0230IST) {
		t.Error("tz=Asia/Kolkata window should be active at Wed 02:30 IST")
	}
	if IsActive(inIST, wed0230UTC) {
		t.Error("tz=Asia/Kolkata window should not be active at Wed 02:30 UTC (08:00 IST)")
	}
}

func TestNextStart(t *testing.T) {
	// Monday + Thursday at 03:00 UTC.
	w := weekly(1<<0|1<<3, 180, 30, "UTC")
	sun := utc(2026, 9, 27, 12, 0)
	got, ok := NextStart(w, sun)
	if !ok || !got.Equal(utc(2026, 9, 28, 3, 0)) {
		t.Fatalf("NextStart from Sunday = %v,%v; want Mon 03:00 UTC", got, ok)
	}
	// Strictly after now: exactly at Monday's start → Thursday.
	got, ok = NextStart(w, utc(2026, 9, 28, 3, 0))
	if !ok || !got.Equal(utc(2026, 10, 1, 3, 0)) {
		t.Fatalf("NextStart at Mon start = %v,%v; want Thu 03:00 UTC", got, ok)
	}

	// Once: future → StartsAt; past → false.
	o := once(utc(2026, 9, 28, 10, 0), utc(2026, 9, 28, 11, 0))
	got, ok = NextStart(o, sun)
	if !ok || !got.Equal(utc(2026, 9, 28, 10, 0)) {
		t.Fatalf("NextStart once future = %v,%v", got, ok)
	}
	if _, ok := NextStart(o, utc(2026, 9, 28, 10, 30)); ok {
		t.Fatal("NextStart for a once-window that already started should be false")
	}
	if _, ok := NextStart(o, utc(2026, 9, 29, 0, 0)); ok {
		t.Fatal("NextStart for a past once-window should be false")
	}
	inactive := w
	inactive.Active = false
	if _, ok := NextStart(inactive, sun); ok {
		t.Fatal("NextStart for an inactive window should be false")
	}
}

func TestCurrentEnd(t *testing.T) {
	w := weekly(1<<0, 23*60, 120, "Asia/Kolkata")
	end, ok := CurrentEnd(w, time.Date(2026, 9, 29, 0, 30, 0, 0, ist))
	if !ok || !end.Equal(time.Date(2026, 9, 29, 1, 0, 0, 0, ist)) {
		t.Fatalf("CurrentEnd weekly = %v,%v; want Tue 01:00 IST", end, ok)
	}
	if _, ok := CurrentEnd(w, time.Date(2026, 9, 29, 1, 1, 0, 0, ist)); ok {
		t.Fatal("CurrentEnd outside the window should be false")
	}
	o := once(utc(2026, 9, 28, 10, 0), utc(2026, 9, 28, 11, 0))
	end, ok = CurrentEnd(o, utc(2026, 9, 28, 10, 30))
	if !ok || !end.Equal(utc(2026, 9, 28, 11, 0)) {
		t.Fatalf("CurrentEnd once = %v,%v", end, ok)
	}
}

func TestActiveSet(t *testing.T) {
	g7 := int64(7)
	g8 := int64(8)
	groups := map[int64]*int64{1: &g7, 2: &g7, 3: &g8, 4: nil}
	groupOf := func(id int64) *int64 { return groups[id] }
	now := utc(2026, 9, 28, 10, 30)

	groupWin := once(utc(2026, 9, 28, 10, 0), utc(2026, 9, 28, 11, 0))
	groupWin.MonitorIDs = nil
	groupWin.GroupIDs = []int64{7}

	monWin := once(utc(2026, 9, 28, 10, 0), utc(2026, 9, 28, 11, 0))
	monWin.MonitorIDs = []int64{4}

	pastWin := once(utc(2026, 9, 27, 10, 0), utc(2026, 9, 27, 11, 0))
	pastWin.MonitorIDs = []int64{3}

	set := ActiveSet([]store.Maintenance{groupWin, monWin, pastWin}, []int64{1, 2, 3, 4}, groupOf, now)
	want := map[int64]bool{1: true, 2: true, 4: true}
	if len(set) != len(want) {
		t.Fatalf("ActiveSet = %v, want %v", set, want)
	}
	for id := range want {
		if !set[id] {
			t.Errorf("monitor %d should be in maintenance; set=%v", id, set)
		}
	}
	if set[3] {
		t.Error("monitor 3 (other group, past window) must not be in maintenance")
	}

	// Monitor-only target includes only that monitor.
	only := ActiveSet([]store.Maintenance{monWin}, []int64{1, 2, 3, 4}, groupOf, now)
	if len(only) != 1 || !only[4] {
		t.Fatalf("monitor-only ActiveSet = %v, want {4}", only)
	}

	// Nothing active → empty.
	if got := ActiveSet(nil, []int64{1, 2}, groupOf, now); len(got) != 0 {
		t.Fatalf("empty windows ActiveSet = %v", got)
	}
}

func validOnce() store.Maintenance {
	return once(utc(2026, 9, 28, 10, 0), utc(2026, 9, 28, 11, 0))
}

func validWeekly() store.Maintenance {
	return weekly(1<<0, 60, 30, "Asia/Kolkata")
}

func TestValidate(t *testing.T) {
	if fe := Validate(ptr(validOnce())); len(fe) != 0 {
		t.Fatalf("valid once window: %v", fe)
	}
	if fe := Validate(ptr(validWeekly())); len(fe) != 0 {
		t.Fatalf("valid weekly window: %v", fe)
	}
	groupOnly := validWeekly()
	groupOnly.MonitorIDs = nil
	groupOnly.GroupIDs = []int64{3}
	if fe := Validate(&groupOnly); len(fe) != 0 {
		t.Fatalf("group-only target should be valid: %v", fe)
	}

	cases := []struct {
		name  string
		field string
		mut   func(*store.Maintenance)
		base  func() store.Maintenance
	}{
		{"empty title", "title", func(m *store.Maintenance) { m.Title = "  " }, validOnce},
		{"long title", "title", func(m *store.Maintenance) { m.Title = strings.Repeat("x", 121) }, validOnce},
		{"bad kind", "kind", func(m *store.Maintenance) { m.Kind = "daily" }, validOnce},
		{"bad tz", "tz", func(m *store.Maintenance) { m.TZ = "Not/AZone" }, validOnce},
		{"no targets", "targets", func(m *store.Maintenance) { m.MonitorIDs = nil; m.GroupIDs = nil }, validOnce},
		{"once missing start", "startsAt", func(m *store.Maintenance) { m.StartsAt = nil }, validOnce},
		{"once missing end", "endsAt", func(m *store.Maintenance) { m.EndsAt = nil }, validOnce},
		{"once start not before end", "endsAt", func(m *store.Maintenance) { m.EndsAt = m.StartsAt }, validOnce},
		{"once span over 30 days", "endsAt", func(m *store.Maintenance) {
			m.EndsAt = ms(time.UnixMilli(*m.StartsAt).Add(30*24*time.Hour + time.Minute))
		}, validOnce},
		{"weekly no days", "weekdays", func(m *store.Maintenance) { m.Weekdays = 0 }, validWeekly},
		{"weekly days overflow", "weekdays", func(m *store.Maintenance) { m.Weekdays = 128 }, validWeekly},
		{"weekly start minute negative", "startMinute", func(m *store.Maintenance) { m.StartMinute = -1 }, validWeekly},
		{"weekly start minute too big", "startMinute", func(m *store.Maintenance) { m.StartMinute = 1440 }, validWeekly},
		{"weekly zero duration", "durationMin", func(m *store.Maintenance) { m.DurationMin = 0 }, validWeekly},
		{"weekly duration too long", "durationMin", func(m *store.Maintenance) { m.DurationMin = 1441 }, validWeekly},
	}
	for _, c := range cases {
		m := c.base()
		c.mut(&m)
		fe := Validate(&m)
		if _, ok := fe[c.field]; !ok {
			t.Errorf("%s: expected error on %q, got %v", c.name, c.field, fe)
		}
	}

	// Boundary values are accepted.
	edge := validWeekly()
	edge.Weekdays, edge.StartMinute, edge.DurationMin = 127, 1439, 1440
	if fe := Validate(&edge); len(fe) != 0 {
		t.Fatalf("boundary weekly values should be valid: %v", fe)
	}
	span := validOnce()
	span.EndsAt = ms(time.UnixMilli(*span.StartsAt).Add(30 * 24 * time.Hour))
	if fe := Validate(&span); len(fe) != 0 {
		t.Fatalf("exactly 30 days should be valid: %v", fe)
	}
}

func ptr(m store.Maintenance) *store.Maintenance { return &m }
