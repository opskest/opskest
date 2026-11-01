package store

import (
	"context"
	"testing"
	"time"

	"opskest/internal/latency"
	"opskest/internal/model"
)

// TestRollupHistAccumulatesAcrossCommits proves the latency histogram on a
// rollup row is merged (by opskest_hist_merge, in the upsert) across
// separate writer commits into the same bucket, and skips failed checks.
func TestRollupHistAccumulatesAcrossCommits(t *testing.T) {
	s := newTestStore(t)
	w := testWriter(t, s)
	ctx := context.Background()
	m := createTestMonitor(t, s, "hist")

	base := HourBucket(time.Now().UnixMilli())
	for i := 0; i < 50; i++ {
		w.Heartbeat(Heartbeat{MonitorID: m.ID, TS: base + int64(i), Status: model.StatusUp, LatencyMs: 100})
	}
	w.Flush()
	for i := 50; i < 100; i++ {
		w.Heartbeat(Heartbeat{MonitorID: m.ID, TS: base + int64(i), Status: model.StatusUp, LatencyMs: 900})
	}
	w.Heartbeat(Heartbeat{MonitorID: m.ID, TS: base + 100, Status: model.StatusDown, LatencyMs: -1})
	w.Flush()

	for _, table := range []string{"rollup_hourly", "rollup_daily"} {
		rows, err := s.RollupSeries(ctx, table, m.ID, 0, base+1_000_000_000)
		if err != nil || len(rows) != 1 {
			t.Fatalf("%s: rows=%d err=%v", table, len(rows), err)
		}
		h := rows[0].Histogram()
		if h.Count() != 100 {
			t.Fatalf("%s: hist count = %d, want 100", table, h.Count())
		}
		p50, _ := h.Quantile(0.5, rows[0].LatMin, rows[0].LatMax)
		p99, _ := h.Quantile(0.99, rows[0].LatMin, rows[0].LatMax)
		if p50 < 95 || p50 > 105 || p99 < 855 || p99 > 900 {
			t.Fatalf("%s: p50=%d p99=%d, want ~100 / ~900", table, p50, p99)
		}
	}
}

// TestRollupHistMergesOntoLegacyNull proves a row written before the
// lat_hist column existed (NULL) picks up a histogram of just the new
// samples, rather than erroring or staying NULL.
func TestRollupHistMergesOntoLegacyNull(t *testing.T) {
	s := newTestStore(t)
	w := testWriter(t, s)
	ctx := context.Background()
	m := createTestMonitor(t, s, "legacy")

	base := HourBucket(time.Now().UnixMilli())
	if _, err := s.W.ExecContext(ctx, `INSERT INTO rollup_hourly(monitor_id,bucket,n_up,lat_sum,lat_count,lat_min,lat_max) VALUES(?,?,10,500,10,50,50)`, m.ID, base); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		w.Heartbeat(Heartbeat{MonitorID: m.ID, TS: base + int64(i), Status: model.StatusUp, LatencyMs: 200})
	}
	w.Flush()

	rows, err := s.RollupSeries(ctx, "rollup_hourly", m.ID, base, base)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	if rows[0].LatCount != 13 {
		t.Fatalf("lat_count = %d, want 13", rows[0].LatCount)
	}
	if got := rows[0].Histogram(); got.Count() != 3 || got[latency.Bin(200)] != 3 {
		t.Fatalf("hist = %v, want 3 samples in bin(200)", got)
	}
}

// TestRollupHistsMergesWindow proves RollupHists returns one merged row
// per monitor across every bucket in the window.
func TestRollupHistsMergesWindow(t *testing.T) {
	s := newTestStore(t)
	w := testWriter(t, s)
	ctx := context.Background()
	a := createTestMonitor(t, s, "a")
	b := createTestMonitor(t, s, "b")

	h0 := HourBucket(time.Now().Add(-3 * time.Hour).UnixMilli())
	for hour := int64(0); hour < 3; hour++ {
		ts := h0 + hour*3_600_000
		w.Heartbeat(Heartbeat{MonitorID: a.ID, TS: ts, Status: model.StatusUp, LatencyMs: 10})
		w.Heartbeat(Heartbeat{MonitorID: b.ID, TS: ts, Status: model.StatusUp, LatencyMs: 1000})
	}
	w.Flush()

	got, err := s.RollupHists(ctx, "rollup_hourly", h0, h0+2*3_600_000)
	if err != nil {
		t.Fatal(err)
	}
	if got[a.ID].Histogram().Count() != 3 || got[a.ID].LatCount != 3 || got[a.ID].LatMax != 10 {
		t.Fatalf("a = %+v", got[a.ID])
	}
	if got[b.ID].Histogram().Count() != 3 || got[b.ID].LatMin != 1000 {
		t.Fatalf("b = %+v", got[b.ID])
	}
}

// TestPageIncidentsCursorTies: incidents created in the same millisecond
// never vanish across a page boundary — the cursor tiebreaks on id.
func TestPageIncidentsCursorTies(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := &Page{Slug: "ties", Title: "Ties", Visibility: "public", Layout: "list", HistoryDays: 90}
	if err := s.CreatePage(ctx, p); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		inc := &Incident{Title: "t", Status: "investigating", Impact: "minor", PageIDs: []int64{p.ID}}
		if err := s.CreateIncident(ctx, inc, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.W.ExecContext(ctx, `UPDATE incidents SET created_at=1000`); err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	var ts, id int64
	for page := 0; page < 5; page++ {
		got, err := s.PageIncidentsBefore(ctx, p.ID, ts, id, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, inc := range got {
			seen[inc.ID] = true
		}
		if len(got) < 2 {
			break
		}
		ts, id = got[len(got)-1].CreatedAt, got[len(got)-1].ID
	}
	if len(seen) != 3 {
		t.Fatalf("paged through %d incidents, want 3", len(seen))
	}
}
