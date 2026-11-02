package store

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opskest/internal/model"
)

// testWriter builds and starts a Writer against s, wired to close cleanly
// when the test ends.
func testWriter(t *testing.T, s *Store) *Writer {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := s.NewWriter(log)
	w.Start(context.Background())
	t.Cleanup(w.Close)
	return w
}

func createTestMonitor(t *testing.T, s *Store, name string) *model.Monitor {
	t.Helper()
	m := model.NewMonitor(model.TypeHTTP)
	m.Name = name
	m.Target = "https://example.com/" + name
	if err := s.CreateMonitor(context.Background(), m); err != nil {
		t.Fatalf("CreateMonitor(%s): %v", name, err)
	}
	return m
}

func TestWriterBatchesAndRollsUp(t *testing.T) {
	s := newTestStore(t)
	w := testWriter(t, s)

	base := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC).UnixMilli()
	hourStarts := []int64{HourBucket(base), HourBucket(base) + 3600_000}
	monitorIDs := []int64{101, 102, 103}

	// Each (monitor, hour) group has an identical, fully-known
	// composition of 200 beats: 40 Up (lat 100..139), 40 Down (no
	// latency), 40 Degraded (lat 200..239), 30 Pending (no latency),
	// 20 Unknown (no latency), 30 Maintenance (excluded from every
	// status column and from latency, even though it carries one).
	for _, mid := range monitorIDs {
		for _, hourStart := range hourStarts {
			for i := 0; i < 200; i++ {
				ts := hourStart + int64(i)*1000
				hb := Heartbeat{MonitorID: mid, TS: ts}
				switch {
				case i < 40:
					hb.Status = model.StatusUp
					hb.LatencyMs = 100 + i
				case i < 80:
					hb.Status = model.StatusDown
					hb.LatencyMs = -1
				case i < 120:
					hb.Status = model.StatusDegraded
					hb.LatencyMs = 200 + (i - 80)
				case i < 150:
					hb.Status = model.StatusPending
					hb.LatencyMs = -1
				case i < 170:
					hb.Status = model.StatusUnknown
					hb.LatencyMs = -1
				default:
					hb.Status = model.StatusDown
					hb.LatencyMs = 999
					hb.Maint = true
				}
				w.Heartbeat(hb)
			}
		}
	}

	w.Flush()

	var count int
	if err := s.R.QueryRow(`SELECT COUNT(*) FROM heartbeats`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1200 {
		t.Fatalf("heartbeats count = %d, want 1200", count)
	}

	for _, mid := range monitorIDs {
		for _, hourStart := range hourStarts {
			var up, down, degraded, pending, unknown, maint int
			var latSum, latCount int64
			var latMin, latMax int
			err := s.R.QueryRow(`
				SELECT n_up, n_degraded, n_pending, n_down, n_unknown, n_maint, lat_sum, lat_count, lat_min, lat_max
				FROM rollup_hourly WHERE monitor_id=? AND bucket=?`, mid, hourStart).
				Scan(&up, &degraded, &pending, &down, &unknown, &maint, &latSum, &latCount, &latMin, &latMax)
			if err != nil {
				t.Fatalf("rollup_hourly monitor=%d bucket=%d: %v", mid, hourStart, err)
			}
			if up != 40 || down != 40 || degraded != 40 || pending != 30 || unknown != 20 || maint != 30 {
				t.Fatalf("rollup_hourly monitor=%d bucket=%d: up=%d down=%d degraded=%d pending=%d unknown=%d maint=%d",
					mid, hourStart, up, down, degraded, pending, unknown, maint)
			}
			if latCount != 80 || latSum != 13560 || latMin != 100 || latMax != 239 {
				t.Fatalf("rollup_hourly monitor=%d bucket=%d: latCount=%d latSum=%d latMin=%d latMax=%d",
					mid, hourStart, latCount, latSum, latMin, latMax)
			}
		}

		dayBucket := s.DayBucket(hourStarts[0])
		if got := s.DayBucket(hourStarts[1]); got != dayBucket {
			t.Fatalf("test setup: the two hours land on different days (%d vs %d)", dayBucket, got)
		}
		// base is 2024-01-01T08:00:00Z = 13:30 in testLoc (+05:30), so local midnight for
		// that calendar day is 2023-12-31T18:30:00Z. This is computed
		// independently in plain UTC (not via testLoc()/FixedZone, which
		// DayBucket itself uses), as a fixed check on DayBucket's actual
		// output rather than a tautology that only re-derives the same
		// value DayBucket would produce.
		wantDayBucket := time.Date(2023, 12, 31, 18, 30, 0, 0, time.UTC).UnixMilli()
		if dayBucket != wantDayBucket {
			t.Fatalf("DayBucket = %d, want %d (hand-computed local midnight for 2024-01-01)", dayBucket, wantDayBucket)
		}

		var up, down, degraded, pending, unknown, maint int
		var latSum, latCount int64
		var latMin, latMax int
		err := s.R.QueryRow(`
			SELECT n_up, n_degraded, n_pending, n_down, n_unknown, n_maint, lat_sum, lat_count, lat_min, lat_max
			FROM rollup_daily WHERE monitor_id=? AND bucket=?`, mid, dayBucket).
			Scan(&up, &degraded, &pending, &down, &unknown, &maint, &latSum, &latCount, &latMin, &latMax)
		if err != nil {
			t.Fatalf("rollup_daily monitor=%d bucket=%d: %v", mid, dayBucket, err)
		}
		if up != 80 || down != 80 || degraded != 80 || pending != 60 || unknown != 40 || maint != 60 {
			t.Fatalf("rollup_daily monitor=%d: up=%d down=%d degraded=%d pending=%d unknown=%d maint=%d",
				mid, up, down, degraded, pending, unknown, maint)
		}
		if latCount != 160 || latSum != 27120 || latMin != 100 || latMax != 239 {
			t.Fatalf("rollup_daily monitor=%d: latCount=%d latSum=%d latMin=%d latMax=%d",
				mid, latCount, latSum, latMin, latMax)
		}
	}
}

func TestWriterNeverBlocks(t *testing.T) {
	s := newTestStore(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Deliberately not started: nothing ever drains w.q, so every send
	// past its capacity must be dropped rather than block.
	w := newWriter(s, log, 10)

	start := time.Now()
	for i := 0; i < 50; i++ {
		w.Heartbeat(Heartbeat{MonitorID: 1, TS: int64(i), Status: model.StatusUp})
	}
	elapsed := time.Since(start)

	if elapsed > 50*time.Millisecond {
		t.Fatalf("50 enqueues on an unstarted writer took %v, want <= 50ms", elapsed)
	}
	if got := w.Dropped(); got != 40 {
		t.Fatalf("Dropped() = %d, want 40 (50 sent - 10 capacity)", got)
	}
}

func TestDeleteMonitorPurgesQueuedHeartbeats(t *testing.T) {
	s := newTestStore(t)
	w := testWriter(t, s)
	ctx := context.Background()

	m := createTestMonitor(t, s, "victim")

	base := time.Now().UnixMilli()
	for i := 0; i < 100; i++ {
		w.Heartbeat(Heartbeat{MonitorID: m.ID, TS: base + int64(i), Status: model.StatusUp, LatencyMs: 10})
	}

	w.ForgetMonitor(m.ID)
	if err := s.DeleteMonitor(ctx, m.ID); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}

	w.Flush()

	var count int
	if err := s.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM heartbeats WHERE monitor_id=?`, m.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("heartbeats for deleted monitor = %d, want 0", count)
	}

	var hourlyCount int
	if err := s.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM rollup_hourly WHERE monitor_id=?`, m.ID).Scan(&hourlyCount); err != nil {
		t.Fatal(err)
	}
	if hourlyCount != 0 {
		t.Fatalf("rollup_hourly rows for deleted monitor = %d, want 0", hourlyCount)
	}

	var dailyCount int
	if err := s.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM rollup_daily WHERE monitor_id=?`, m.ID).Scan(&dailyCount); err != nil {
		t.Fatal(err)
	}
	if dailyCount != 0 {
		t.Fatalf("rollup_daily rows for deleted monitor = %d, want 0", dailyCount)
	}
}

// TestForgetMonitorTombstoneIsSequenceBased is Review Focus 2 / ruling
// R10's required regression test: monitors.id has no AUTOINCREMENT, so
// SQLite reuses a deleted id (here, deterministically, because the store
// is fresh and the victim is the table's only row: the next INSERT picks
// max(rowid)+1, which is 1 once the table is empty). An id-only
// tombstone would then wrongly drop the new monitor's heartbeats; the
// seq-based tombstone must not.
func TestForgetMonitorTombstoneIsSequenceBased(t *testing.T) {
	s := newTestStore(t)
	w := testWriter(t, s)
	ctx := context.Background()

	victim := createTestMonitor(t, s, "victim")
	id := victim.ID

	base := time.Now().UnixMilli()
	for i := 0; i < 20; i++ {
		w.Heartbeat(Heartbeat{MonitorID: id, TS: base + int64(i), Status: model.StatusUp, LatencyMs: 1})
	}

	w.ForgetMonitor(id)
	if err := s.DeleteMonitor(ctx, id); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}

	reused := createTestMonitor(t, s, "new-tenant")
	if reused.ID != id {
		t.Fatalf("test setup: expected SQLite to reuse id %d for the new monitor, got %d", id, reused.ID)
	}

	freshBase := base + 10_000
	for i := 0; i < 5; i++ {
		w.Heartbeat(Heartbeat{MonitorID: id, TS: freshBase + int64(i), Status: model.StatusUp, LatencyMs: 5})
	}
	w.Flush()

	var count int
	if err := s.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM heartbeats WHERE monitor_id=?`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("heartbeats for reused id = %d, want 5 (only the fresh beats)", count)
	}

	var oldCount int
	if err := s.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM heartbeats WHERE monitor_id=? AND ts<?`, id, freshBase).Scan(&oldCount); err != nil {
		t.Fatal(err)
	}
	if oldCount != 0 {
		t.Fatalf("old (pre-forget) heartbeats resurrected under the reused id: %d rows", oldCount)
	}
}

// TestDoForSkipsForgottenMonitor is ruling R18 test (a): an op enqueued
// via DoFor for a monitor that is then forgotten (and deleted) before the
// writer processes it must be skipped, not run. Unlike heartbeats,
// outages has no FK to monitors, so without this check the op would
// succeed silently and leave a permanent orphan active outage - exactly
// the failure mode ruling R18 describes.
func TestDoForSkipsForgottenMonitor(t *testing.T) {
	s := newTestStore(t)
	w := testWriter(t, s)
	ctx := context.Background()

	m := createTestMonitor(t, s, "outage-victim")

	now := time.Now().UnixMilli()
	outageID, err := s.MaxOutageID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	outageID++

	// Simulates the real race: the monitor goes DOWN and an
	// outage-open op is queued, then an admin deletes the monitor
	// before the writer's next tick processes it.
	w.DoFor(m.ID, OpOpenOutage(Outage{ID: outageID, MonitorID: m.ID, StartedAt: now, FirstReason: "timeout"}))

	w.ForgetMonitor(m.ID)
	if err := s.DeleteMonitor(ctx, m.ID); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}

	w.Flush()

	active, err := s.ActiveOutages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range active {
		if o.MonitorID == m.ID {
			t.Fatalf("DoFor did not skip the op for a forgotten monitor: found orphan outage %+v", o)
		}
	}
}

// TestFailingOpDoesNotDiscardBatch is ruling R18 test (b): one op in a
// batch returning an error must not discard that batch's heartbeats or
// its other ops - each op runs in its own SAVEPOINT.
func TestFailingOpDoesNotDiscardBatch(t *testing.T) {
	s := newTestStore(t)
	w := testWriter(t, s)
	ctx := context.Background()

	m := createTestMonitor(t, s, "batch-with-bad-op")

	w.Heartbeat(Heartbeat{MonitorID: m.ID, TS: time.Now().UnixMilli(), Status: model.StatusUp, LatencyMs: 5})

	w.Do(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO this_table_does_not_exist(x) VALUES (1)`)
		return err
	})

	var ranAfter bool
	w.Do(func(tx *sql.Tx) error {
		ranAfter = true
		return nil
	})

	w.Flush()

	var count int
	if err := s.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM heartbeats WHERE monitor_id=?`, m.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("heartbeats after a failing op = %d, want 1 (the batch must not be discarded)", count)
	}
	if !ranAfter {
		t.Fatalf("the op queued after the failing one never ran")
	}
}

// TestPanickingOpIsContained is ruling R18 test (c): an op that panics
// must not crash the writer goroutine (or the process), and must not
// discard the rest of the batch.
func TestPanickingOpIsContained(t *testing.T) {
	s := newTestStore(t)
	w := testWriter(t, s)
	ctx := context.Background()

	m := createTestMonitor(t, s, "batch-with-panicking-op")

	w.Heartbeat(Heartbeat{MonitorID: m.ID, TS: time.Now().UnixMilli(), Status: model.StatusUp, LatencyMs: 5})

	w.Do(func(tx *sql.Tx) error {
		panic("boom")
	})

	var ranAfter bool
	w.Do(func(tx *sql.Tx) error {
		ranAfter = true
		return nil
	})

	w.Flush() // must return normally, not hang or crash the test process.

	var count int
	if err := s.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM heartbeats WHERE monitor_id=?`, m.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("heartbeats after a panicking op = %d, want 1", count)
	}
	if !ranAfter {
		t.Fatalf("the op queued after the panicking one never ran")
	}
}

func TestOutagesOpenCloseSummary(t *testing.T) {
	s := newTestStore(t)
	w := testWriter(t, s)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	o1 := Outage{ID: 1, MonitorID: 1, StartedAt: now - 3600_000, FirstReason: "timeout", ErrClass: "timeout"}
	o2 := Outage{ID: 2, MonitorID: 1, StartedAt: now - 7200_000, FirstReason: "timeout", ErrClass: "timeout"}
	o3 := Outage{ID: 3, MonitorID: 2, StartedAt: now - 1800_000, FirstReason: "dns", ErrClass: "dns"}

	w.Do(OpOpenOutage(o1))
	w.Do(OpOpenOutage(o2))
	w.Do(OpOpenOutage(o3))

	const dur1 = 600_000  // 10 min
	const dur2 = 1200_000 // 20 min
	w.Do(OpCloseOutage(o1.ID, o1.StartedAt+dur1))
	w.Do(OpCloseOutage(o2.ID, o2.StartedAt+dur2))

	w.Flush()

	maxID, err := s.MaxOutageID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if maxID != 3 {
		t.Fatalf("MaxOutageID = %d, want 3", maxID)
	}

	active, err := s.ActiveOutages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != o3.ID {
		t.Fatalf("ActiveOutages = %+v, want just outage 3", active)
	}

	sum, err := s.OutageSummary(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Active != 1 {
		t.Fatalf("Active = %d, want 1", sum.Active)
	}
	if sum.Last30d != 3 {
		t.Fatalf("Last30d = %d, want 3", sum.Last30d)
	}
	wantMTTR := int64((dur1 + dur2) / 2 / 1000)
	if sum.MTTRSeconds != wantMTTR {
		t.Fatalf("MTTRSeconds = %d, want %d", sum.MTTRSeconds, wantMTTR)
	}
	if sum.MostAffectedMonitorID == nil || *sum.MostAffectedMonitorID != 1 || sum.MostAffectedCount != 2 {
		t.Fatalf("MostAffected = %v/%d, want monitor 1 with count 2", sum.MostAffectedMonitorID, sum.MostAffectedCount)
	}

	if err := s.AckOutage(ctx, 9999, "alice", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AckOutage(unknown id) = %v, want ErrNotFound", err)
	}

	if err := s.AckOutage(ctx, o3.ID, "alice", now); err != nil {
		t.Fatalf("AckOutage: %v", err)
	}
	if err := s.NoteOutage(ctx, o3.ID, "root caused", "bob", now); err != nil {
		t.Fatalf("NoteOutage: %v", err)
	}
	active, err = s.ActiveOutages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].AckedBy == nil || *active[0].AckedBy != "alice" || active[0].Note == nil || *active[0].Note != "root caused" {
		t.Fatalf("outage not acked/noted as expected: %+v", active)
	}

	mid1 := int64(1)
	byMonitor, err := s.ListOutages(ctx, OutageFilter{MonitorID: &mid1})
	if err != nil {
		t.Fatal(err)
	}
	if len(byMonitor) != 2 {
		t.Fatalf("ListOutages(monitor=1) = %d rows, want 2", len(byMonitor))
	}
}

func TestRawSeriesDownsamples(t *testing.T) {
	s := newTestStore(t)
	w := testWriter(t, s)
	ctx := context.Background()

	const monitorID = int64(1)
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	to := from + 24*3600_000
	const n = 10000
	step := (to - from) / n

	for i := 0; i < n; i++ {
		ts := from + int64(i)*step
		status := model.StatusUp
		if i%37 == 0 {
			status = model.StatusDown
		}
		w.Heartbeat(Heartbeat{MonitorID: monitorID, TS: ts, Status: status, LatencyMs: i % 500})
	}
	w.Flush()

	const maxPoints = 600
	points, err := s.RawSeries(ctx, monitorID, from, to, maxPoints)
	if err != nil {
		t.Fatalf("RawSeries: %v", err)
	}
	if len(points) == 0 || len(points) > maxPoints {
		t.Fatalf("len(points) = %d, want 1..%d", len(points), maxPoints)
	}

	bucketMs := (to-from+maxPoints-1)/maxPoints + 1
	for _, p := range points {
		var minL, maxL sql.NullInt64
		var avgL sql.NullFloat64
		err := s.R.QueryRowContext(ctx, `
			SELECT MIN(latency_ms), MAX(latency_ms), AVG(latency_ms)
			FROM heartbeats WHERE monitor_id=? AND ts>=? AND ts<?`,
			monitorID, p.TS, p.TS+bucketMs).Scan(&minL, &maxL, &avgL)
		if err != nil {
			t.Fatalf("cross-check bucket %d: %v", p.TS, err)
		}
		if !minL.Valid {
			t.Fatalf("cross-check bucket %d: empty bucket returned by RawSeries", p.TS)
		}
		if p.Min != int(minL.Int64) || p.Max != int(maxL.Int64) {
			t.Fatalf("bucket %d: min=%d max=%d, want min=%d max=%d", p.TS, p.Min, p.Max, minL.Int64, maxL.Int64)
		}
		if p.Avg != int(avgL.Float64) {
			t.Fatalf("bucket %d: avg=%d, want %d", p.TS, p.Avg, int(avgL.Float64))
		}
	}
}

// TestRawSeriesBucketsAreAlignedToFrom is Important review issue 3: an
// unaligned `from` combined with a beat exactly at `to` used to overflow
// to maxPoints+1 distinct buckets, because bucketing was relative to
// epoch 0 rather than to `from`. 11 beats spaced exactly one bucket-width
// apart, with maxPoints=10, is exactly the case that used to produce 11
// buckets (one per beat) before the fix folds the last one back into the
// 10th.
func TestRawSeriesBucketsAreAlignedToFrom(t *testing.T) {
	s := newTestStore(t)
	w := testWriter(t, s)
	ctx := context.Background()

	const monitorID = int64(1)
	const maxPoints = 10
	const bucketMs = 60_000

	// Deliberately unaligned to any clean bucket boundary.
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli() + 137
	to := from + maxPoints*bucketMs

	for k := int64(0); k <= maxPoints; k++ {
		w.Heartbeat(Heartbeat{MonitorID: monitorID, TS: from + k*bucketMs, Status: model.StatusUp, LatencyMs: int(k)})
	}
	w.Flush()

	points, err := s.RawSeries(ctx, monitorID, from, to, maxPoints)
	if err != nil {
		t.Fatalf("RawSeries: %v", err)
	}
	if len(points) > maxPoints {
		t.Fatalf("len(points) = %d, want <= %d (from=%d is unaligned, and a beat sits exactly at to=%d)", len(points), maxPoints, from, to)
	}
}

// TestRawSeriesExcludesMaintFromLatency is minor review issue 11: a
// maintenance beat must not contribute to Avg/Min/Max, and must count in
// Other rather than Up/Down.
func TestRawSeriesExcludesMaintFromLatency(t *testing.T) {
	s := newTestStore(t)
	w := testWriter(t, s)
	ctx := context.Background()

	const monitorID = int64(1)
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	to := from + 60_000

	w.Heartbeat(Heartbeat{MonitorID: monitorID, TS: from, Status: model.StatusUp, LatencyMs: 100})
	w.Heartbeat(Heartbeat{MonitorID: monitorID, TS: from + 10_000, Status: model.StatusDown, LatencyMs: 99999, Maint: true})
	w.Flush()

	points, err := s.RawSeries(ctx, monitorID, from, to, 1)
	if err != nil {
		t.Fatalf("RawSeries: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("len(points) = %d, want 1", len(points))
	}
	p := points[0]
	if p.Avg != 100 || p.Min != 100 || p.Max != 100 {
		t.Fatalf("maint beat's latency leaked into the aggregate: %+v", p)
	}
	if p.Up != 1 || p.Down != 0 || p.Other != 1 {
		t.Fatalf("maint beat not counted as Other: %+v", p)
	}
}

func TestPruneUsesCutoff(t *testing.T) {
	s := newTestStore(t)
	w := testWriter(t, s)
	ctx := context.Background()

	m := createTestMonitor(t, s, "prune-target")

	now := time.Now()
	nowMs := now.UnixMilli()
	ts8d := nowMs - 8*24*3600_000
	ts6d := nowMs - 6*24*3600_000

	w.Heartbeat(Heartbeat{MonitorID: m.ID, TS: ts8d, Status: model.StatusUp, LatencyMs: 1})
	w.Heartbeat(Heartbeat{MonitorID: m.ID, TS: ts6d, Status: model.StatusUp, LatencyMs: 1})

	hourly91d := HourBucket(nowMs - 91*24*3600_000)
	hourlyKept := HourBucket(nowMs - 10*24*3600_000)
	daily731d := s.DayBucket(nowMs - 731*24*3600_000)
	dailyKept := s.DayBucket(nowMs - 10*24*3600_000)

	seedRollup := func(table string, bucket int64) {
		w.Do(func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO `+table+`(monitor_id,bucket,n_up) VALUES (?,?,1)`, m.ID, bucket)
			return err
		})
	}
	seedRollup("rollup_hourly", hourly91d)
	seedRollup("rollup_hourly", hourlyKept)
	seedRollup("rollup_daily", daily731d)
	seedRollup("rollup_daily", dailyKept)

	w.Flush()

	if err := s.Prune(ctx, now, 7); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	var beatCount int
	if err := s.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM heartbeats`).Scan(&beatCount); err != nil {
		t.Fatal(err)
	}
	if beatCount != 1 {
		t.Fatalf("heartbeats remaining = %d, want 1", beatCount)
	}
	var remainingTS int64
	if err := s.R.QueryRowContext(ctx, `SELECT ts FROM heartbeats`).Scan(&remainingTS); err != nil {
		t.Fatal(err)
	}
	if remainingTS != ts6d {
		t.Fatalf("remaining heartbeat ts = %d, want %d (the 6d-old one)", remainingTS, ts6d)
	}

	// The two heartbeats above also produce their own hourly/daily
	// rollup rows (at ts8d's and ts6d's own buckets), so check the two
	// explicitly-seeded buckets by existence rather than asserting a
	// total row count.
	bucketExists := func(table string, bucket int64) bool {
		var n int
		if err := s.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE monitor_id=? AND bucket=?`, m.ID, bucket).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}

	if bucketExists("rollup_hourly", hourly91d) {
		t.Fatalf("rollup_hourly bucket at 91d old = present, want pruned")
	}
	if !bucketExists("rollup_hourly", hourlyKept) {
		t.Fatalf("rollup_hourly bucket at 10d old = absent, want kept")
	}
	if bucketExists("rollup_daily", daily731d) {
		t.Fatalf("rollup_daily bucket at 731d old = present, want pruned")
	}
	if !bucketExists("rollup_daily", dailyKept) {
		t.Fatalf("rollup_daily bucket at 10d old = absent, want kept")
	}
}

func TestBackupRotation(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()

	base := time.Date(2024, 1, 1, 2, 0, 0, 0, time.UTC)
	var paths []string
	for i := 0; i < 9; i++ {
		p, err := s.Backup(context.Background(), dir, base.Add(time.Duration(i)*time.Minute), 7)
		if err != nil {
			t.Fatalf("Backup #%d: %v", i, err)
		}
		paths = append(paths, p)
	}

	files, err := ListBackups(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 7 {
		names := make([]string, len(files))
		for i, f := range files {
			names[i] = f.Name
		}
		t.Fatalf("len(files) = %d, want 7 (%v)", len(files), names)
	}

	newest := filepath.Base(paths[len(paths)-1])
	found := false
	for _, f := range files {
		if f.Name == newest {
			found = true
		}
	}
	if !found {
		t.Fatalf("newest backup %q not present among kept files", newest)
	}

	// Check the real directory listing, not ListBackups's result: that
	// function already filters .part files out by construction, so
	// looping over its output can never observe a leftover one.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".part") {
			t.Fatalf("found leftover .part file: %s", e.Name())
		}
	}

	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, newest)+"?mode=ro")
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer db.Close()

	var name string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='monitors'`).Scan(&name); err != nil {
		t.Fatalf("backup missing monitors table: %v", err)
	}
}

// TestBackupIgnoresForeignFiles is Important review issue 4: a file in
// the backups directory that isn't one of Backup's own
// opskest-YYYYMMDD-HHMM.db files must never be listed as a backup, and -
// critically - must never be pruned, even if its name would sort as
// "newest" among the real backups (here, deliberately, by starting with
// "z").
func TestBackupIgnoresForeignFiles(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()

	foreign := filepath.Join(dir, "zzz-manual-copy.db")
	if err := os.WriteFile(foreign, []byte("not a real backup"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Backup(context.Background(), dir, time.Date(2024, 1, 1, 2, 0, 0, 0, time.UTC), 1); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	files, err := ListBackups(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		names := make([]string, len(files))
		for i, f := range files {
			names[i] = f.Name
		}
		t.Fatalf("ListBackups = %d, want 1 (the foreign file must not be listed): %v", len(files), names)
	}
	if files[0].Name == filepath.Base(foreign) {
		t.Fatalf("the foreign file was listed as a backup")
	}

	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("foreign file was removed by pruning: %v", err)
	}
}

// TestHeartbeatQueriesAndRuntime covers the read paths not exercised by
// the brief's named scenarios: LastBeats, BeatsPage, BeatsOfDay,
// RollupsSince, RollupSeries, AllRuntime and OpUpsertRuntime.
func TestHeartbeatQueriesAndRuntime(t *testing.T) {
	s := newTestStore(t)
	w := testWriter(t, s)
	ctx := context.Background()

	m := createTestMonitor(t, s, "queries")
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()

	for i := 0; i < 10; i++ {
		status := model.StatusUp
		if i == 3 || i == 7 {
			status = model.StatusDown
		}
		w.Heartbeat(Heartbeat{MonitorID: m.ID, TS: base + int64(i)*60_000, Status: status, LatencyMs: 50 + i})
	}
	w.Flush()

	last, err := s.LastBeats(ctx, m.ID, 5)
	if err != nil {
		t.Fatalf("LastBeats: %v", err)
	}
	if len(last) != 5 {
		t.Fatalf("LastBeats len = %d, want 5", len(last))
	}
	for i := 1; i < len(last); i++ {
		if last[i].TS <= last[i-1].TS {
			t.Fatalf("LastBeats not oldest-to-newest: %+v", last)
		}
	}
	if last[len(last)-1].TS != base+9*60_000 {
		t.Fatalf("LastBeats last ts = %d, want the newest beat", last[len(last)-1].TS)
	}

	page, err := s.BeatsPage(ctx, m.ID, base+8*60_000, 3)
	if err != nil {
		t.Fatalf("BeatsPage: %v", err)
	}
	if len(page) != 3 {
		t.Fatalf("BeatsPage len = %d, want 3", len(page))
	}
	for i := 1; i < len(page); i++ {
		if page[i].TS >= page[i-1].TS {
			t.Fatalf("BeatsPage not newest-first: %+v", page)
		}
	}
	if page[0].TS != base+7*60_000 {
		t.Fatalf("BeatsPage first ts = %d, want the beat just before the cutoff", page[0].TS)
	}

	all, err := s.BeatsPage(ctx, m.ID, 0, 100)
	if err != nil {
		t.Fatalf("BeatsPage(beforeTS=0): %v", err)
	}
	if len(all) != 10 {
		t.Fatalf("BeatsPage(beforeTS=0) len = %d, want 10 (defaults to now)", len(all))
	}

	dayStart := s.DayBucket(base)
	dayEnd := dayStart + 24*3600_000
	ofDay, err := s.BeatsOfDay(ctx, m.ID, dayStart, dayEnd)
	if err != nil {
		t.Fatalf("BeatsOfDay: %v", err)
	}
	if len(ofDay) != 2 {
		t.Fatalf("BeatsOfDay len = %d, want 2 (the Down beats)", len(ofDay))
	}
	for _, hb := range ofDay {
		if hb.Status == model.StatusUp {
			t.Fatalf("BeatsOfDay returned an UP beat: %+v", hb)
		}
	}

	hourBucket := HourBucket(base)
	since, err := s.RollupsSince(ctx, "rollup_hourly", hourBucket)
	if err != nil {
		t.Fatalf("RollupsSince: %v", err)
	}
	if len(since[m.ID]) == 0 {
		t.Fatalf("RollupsSince returned nothing for monitor %d", m.ID)
	}

	series, err := s.RollupSeries(ctx, "rollup_hourly", m.ID, hourBucket, hourBucket)
	if err != nil {
		t.Fatalf("RollupSeries: %v", err)
	}
	if len(series) != 1 || series[0].Up != 8 || series[0].Down != 2 {
		t.Fatalf("RollupSeries = %+v, want one bucket with up=8 down=2", series)
	}

	empty, err := s.AllRuntime(ctx)
	if err != nil {
		t.Fatalf("AllRuntime: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("AllRuntime before any upsert = %+v, want empty", empty)
	}

	certNotAfter := base + 30*24*3600_000
	w.Do(OpUpsertRuntime(Runtime{MonitorID: m.ID, CertNotAfter: &certNotAfter, CertIssuer: "Let's Encrypt"}))
	w.Flush()

	rt, err := s.AllRuntime(ctx)
	if err != nil {
		t.Fatalf("AllRuntime: %v", err)
	}
	got, ok := rt[m.ID]
	if !ok || got.CertNotAfter == nil || *got.CertNotAfter != certNotAfter || got.CertIssuer != "Let's Encrypt" {
		t.Fatalf("AllRuntime after upsert = %+v", rt)
	}
}

func TestUptime(t *testing.T) {
	if pct, ok := (Rollup{Up: 98, Degraded: 1, Pending: 1}).Uptime(); !ok || pct != 100 {
		t.Fatalf("Uptime(98/1/1/0) = %v,%v want 100,true", pct, ok)
	}
	if pct, ok := (Rollup{Up: 99, Down: 1}).Uptime(); !ok || pct != 99 {
		t.Fatalf("Uptime(99/0/0/1) = %v,%v want 99,true", pct, ok)
	}
	if _, ok := (Rollup{Unknown: 50}).Uptime(); ok {
		t.Fatalf("Uptime(all unknown) ok = true, want false")
	}
}
