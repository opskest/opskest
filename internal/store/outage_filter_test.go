package store

import (
	"context"
	"testing"
	"time"
)

// TestListOutagesSearchNoteDuration covers the Outages tab's search (monitor
// name, reason or note, wildcards literal), the needs-a-note filter, the
// minimum duration (ongoing outages count up to now) and the NeedsNote count.
func TestListOutagesSearchNoteDuration(t *testing.T) {
	s := newTestStore(t)
	w := testWriter(t, s)
	ctx := context.Background()
	api := createTestMonitor(t, s, "Checkout API")
	web := createTestMonitor(t, s, "Website")

	now := time.Now().UnixMilli()
	min := int64(60_000)
	w.Do(OpOpenOutage(Outage{ID: 1, MonitorID: api.ID, StartedAt: now - 120*min, FirstReason: "HTTP 503", ErrClass: "http_status"}))
	w.Do(OpOpenOutage(Outage{ID: 2, MonitorID: web.ID, StartedAt: now - 90*min, FirstReason: "timeout 100%", ErrClass: "timeout"}))
	w.Do(OpOpenOutage(Outage{ID: 3, MonitorID: web.ID, StartedAt: now - 30*min, FirstReason: "dns", ErrClass: "dns"}))
	w.Do(OpCloseOutage(1, now-110*min)) // 10 min
	w.Do(OpCloseOutage(2, now-89*min))  // 1 min
	w.Flush()                           // 3 stays open: 30 min so far
	if err := s.NoteOutage(ctx, 1, "Database failover", "ops", now); err != nil {
		t.Fatal(err)
	}

	ids := func(f OutageFilter) []int64 {
		t.Helper()
		f.Now = now
		got, err := s.ListOutages(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		out := []int64{}
		for _, o := range got {
			out = append(out, o.ID)
		}
		return out
	}
	eq := func(name string, got []int64, want ...int64) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s = %v, want %v", name, got, want)
			}
		}
	}
	eq("by monitor name", ids(OutageFilter{Query: "checkout"}), 1)
	eq("by note", ids(OutageFilter{Query: "FAILOVER"}), 1)
	eq("by reason", ids(OutageFilter{Query: "dns"}), 3)
	eq("% is literal", ids(OutageFilter{Query: "100%"}), 2)
	eq("_ is literal", ids(OutageFilter{Query: "a_i"}))
	eq("needs a note", ids(OutageFilter{Note: "missing"}), 3, 2)
	eq("has a note", ids(OutageFilter{Note: "present"}), 1)
	eq("at least 5 min", ids(OutageFilter{MinDurationMs: 5 * min}), 3, 1)

	o, err := s.ListOutages(ctx, OutageFilter{Note: "present"})
	if err != nil || len(o) != 1 || o[0].NoteAt == nil || *o[0].NoteAt != now || o[0].NoteBy == nil || *o[0].NoteBy != "ops" {
		t.Fatalf("noted outage = %+v (%v), want noteBy ops and noteAt now", o, err)
	}
	// Clearing the note puts the outage back in "needs a note".
	if err := s.NoteOutage(ctx, 1, "  ", "ops", now); err != nil {
		t.Fatal(err)
	}
	eq("cleared note", ids(OutageFilter{Note: "missing"}), 3, 2, 1)

	sum, err := s.OutageSummary(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if sum.NeedsNote != 2 { // 1 and 2 ended without a note; 3 is still open
		t.Fatalf("NeedsNote = %d, want 2", sum.NeedsNote)
	}
}
