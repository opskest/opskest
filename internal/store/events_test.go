package store

import (
	"context"
	"testing"

	"opskest/internal/model"
)

func TestStatusEventsListAndPrune(t *testing.T) {
	s := newTestStore(t)
	w := testWriter(t, s)
	ctx := context.Background()

	// 260 events for monitor 1 at ts 1000..260000, and 5 for monitor 2.
	for i := 1; i <= 260; i++ {
		w.DoFor(1, OpInsertStatusEvent(StatusEvent{
			MonitorID: 1, TS: int64(i) * 1000,
			From: model.StatusUp, To: model.StatusDown,
			Reason: "HTTP 503", ErrClass: "http_status",
		}))
	}
	for i := 1; i <= 5; i++ {
		w.DoFor(2, OpInsertStatusEvent(StatusEvent{MonitorID: 2, TS: int64(i)*1000 + 500, From: model.StatusDown, To: model.StatusUp}))
	}
	w.Flush()

	// Default limit 50, newest first, across monitors.
	all, err := s.ListStatusEvents(ctx, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 50 {
		t.Fatalf("default limit: got %d, want 50", len(all))
	}
	if all[0].TS != 260000 || all[0].MonitorID != 1 {
		t.Fatalf("newest first: got %+v", all[0])
	}
	for i := 1; i < len(all); i++ {
		if all[i].TS > all[i-1].TS {
			t.Fatalf("not newest first at %d: %d > %d", i, all[i].TS, all[i-1].TS)
		}
	}
	if all[0].From != model.StatusUp || all[0].To != model.StatusDown || all[0].Reason != "HTTP 503" || all[0].ErrClass != "http_status" || all[0].ID == 0 {
		t.Fatalf("fields not round-tripped: %+v", all[0])
	}

	// Limit clamps to 200.
	big, err := s.ListStatusEvents(ctx, nil, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(big) != 200 {
		t.Fatalf("limit clamp: got %d, want 200", len(big))
	}

	// Monitor filter + exclusive cursor.
	mid := int64(2)
	m2, err := s.ListStatusEvents(ctx, &mid, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(m2) != 5 {
		t.Fatalf("monitor filter: got %d, want 5", len(m2))
	}
	for _, ev := range m2 {
		if ev.MonitorID != 2 {
			t.Fatalf("monitor filter leaked %+v", ev)
		}
	}
	// Monitor 2's events sit at ts 1500, 2500, 3500, 4500, 5500.
	page, err := s.ListStatusEvents(ctx, &mid, 4000, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 3 || page[0].TS != 3500 || page[2].TS != 1500 {
		t.Fatalf("cursor before=4000: got %+v", page)
	}
	excl, err := s.ListStatusEvents(ctx, &mid, 2500, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(excl) != 1 || excl[0].TS != 1500 {
		t.Fatalf("cursor must be exclusive: got %+v", excl)
	}

	// Prune removes strictly older rows.
	if err := s.PruneStatusEvents(ctx, 100000); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.R.QueryRowContext(ctx, `SELECT count(*) FROM status_events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 161 { // monitor 1: ts 100000..260000
		t.Fatalf("after prune: %d rows, want 161", n)
	}
}
