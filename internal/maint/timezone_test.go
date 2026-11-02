package maint

import (
	"testing"
	"time"
)

func TestWeeklyUsesIANAZoneAndSkipsDSTGap(t *testing.T) {
	// Sundays at 02:30 in New York do not exist on spring-forward day.
	w := weekly(1<<6, 150, 60, "America/New_York")
	if fe := Validate(&w); len(fe) != 0 {
		t.Fatalf("valid IANA zone: %v", fe)
	}
	next, ok := NextStart(w, utc(2026, 3, 8, 0, 0))
	want := utc(2026, 3, 15, 6, 30)
	if !ok || !next.Equal(want) {
		t.Fatalf("next gap-free occurrence = %v, want %v", next, want)
	}
	if IsActive(w, utc(2026, 3, 8, 6, 45)) {
		t.Fatal("missing 02:30 must not run at 01:30")
	}

	// The local hour remains 02:30, while its UTC hour follows DST.
	before, _ := NextStart(w, utc(2026, 3, 1, 0, 0))
	if !before.Equal(utc(2026, 3, 1, 7, 30)) {
		t.Fatalf("winter start = %v", before)
	}
}

func TestWeeklyFallBackRunsOnceAtFirstInstant(t *testing.T) {
	w := weekly(1<<6, 90, 30, "America/New_York")
	next, ok := NextStart(w, utc(2026, 11, 1, 0, 0))
	if !ok || !next.Equal(utc(2026, 11, 1, 5, 30)) {
		t.Fatalf("fold start = %v", next)
	}
	if !IsActive(w, utc(2026, 11, 1, 5, 45)) {
		t.Fatal("first occurrence must be active")
	}
	if IsActive(w, utc(2026, 11, 1, 6, 45)) {
		t.Fatal("fold must not run maintenance twice")
	}
	end, ok := CurrentEnd(w, utc(2026, 11, 1, 5, 45))
	if !ok || end.Sub(next) != 30*time.Minute {
		t.Fatalf("duration changed across fold: %v", end)
	}
}
