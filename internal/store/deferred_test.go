package store

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"opskest/internal/model"
)

func TestPushTokenAlphabetAndLength(t *testing.T) {
	counts := map[byte]int{}
	for i := 0; i < 2000; i++ {
		tok, err := generatePushToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) != pushTokenLen {
			t.Fatalf("len = %d", len(tok))
		}
		for j := 0; j < len(tok); j++ {
			if !strings.ContainsRune(pushTokenAlphabet, rune(tok[j])) {
				t.Fatalf("char %q not in alphabet", tok[j])
			}
			counts[tok[j]]++
		}
	}
	// 64,000 draws over 62 symbols: every symbol should appear, and none
	// should be wildly over-represented (the old modulo bias favoured 8).
	if len(counts) != len(pushTokenAlphabet) {
		t.Fatalf("only %d of %d symbols seen", len(counts), len(pushTokenAlphabet))
	}
	expected := 64000.0 / float64(len(pushTokenAlphabet))
	for c, n := range counts {
		if float64(n) > expected*1.25 || float64(n) < expected*0.75 {
			t.Errorf("symbol %q count %d far from expected %.0f", c, n, expected)
		}
	}
}

func TestListOutagesTieBreakAndFilters(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	w := s.NewWriter(slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.Start(ctx)
	defer w.Close()

	// Two groups; three monitors.
	g1 := &Group{Name: "G1"}
	g2 := &Group{Name: "G2"}
	if err := s.CreateGroup(ctx, g1); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateGroup(ctx, g2); err != nil {
		t.Fatal(err)
	}
	mk := func(name string, gid int64) int64 {
		m := model.NewMonitor(model.TypeHTTP)
		m.Name, m.Target, m.GroupID = name, "https://"+strings.ToLower(name)+".example.com", &gid
		if err := s.CreateMonitor(ctx, m); err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	a, b, c := mk("A", g1.ID), mk("B", g1.ID), mk("C", g2.ID)

	// Five outages share one start millisecond (a storm), plus two more.
	same := int64(1_800_000_000_000)
	id := int64(0)
	open := func(mid, at int64) {
		id++
		w.Do(OpOpenOutage(Outage{ID: id, MonitorID: mid, StartedAt: at, FirstReason: "x"}))
	}
	for i := 0; i < 5; i++ {
		open([]int64{a, b, c}[i%3], same)
	}
	open(a, same-1000)
	open(c, same+1000)
	w.Flush()

	// Page through with limit 2 using the (started_at, id) cursor: all 7 rows, no dupes.
	seen := map[int64]bool{}
	var before, beforeID int64
	for page := 0; page < 10; page++ {
		rows, err := s.ListOutages(ctx, OutageFilter{Before: before, BeforeID: beforeID, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range rows {
			if seen[o.ID] {
				t.Fatalf("outage %d returned twice", o.ID)
			}
			seen[o.ID] = true
		}
		if len(rows) < 2 {
			break
		}
		before, beforeID = rows[len(rows)-1].StartedAt, rows[len(rows)-1].ID
	}
	if len(seen) != 7 {
		t.Fatalf("paged %d outages, want 7", len(seen))
	}

	// Filters.
	rows, _ := s.ListOutages(ctx, OutageFilter{GroupID: &g2.ID, Limit: 50})
	for _, o := range rows {
		if o.MonitorID != c {
			t.Errorf("group filter leaked monitor %d", o.MonitorID)
		}
	}
	rows, _ = s.ListOutages(ctx, OutageFilter{From: same, To: same, Limit: 50})
	if len(rows) != 5 {
		t.Errorf("from/to = %d rows, want 5", len(rows))
	}
	rows, _ = s.ListOutages(ctx, OutageFilter{MonitorID: &a, Limit: 50})
	if len(rows) != 3 {
		t.Errorf("monitor filter = %d rows, want 3", len(rows))
	}
}

func TestPruneLogsAndSessions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	old := now.Add(-400 * 24 * time.Hour).UnixMilli()
	recent := now.UnixMilli()
	for _, ts := range []int64{old, recent} {
		if _, err := s.W.Exec(`INSERT INTO alert_log (ts, event, ok, attempt) VALUES (?, 'down', 1, 1)`, ts); err != nil {
			t.Fatal(err)
		}
		if _, err := s.W.Exec(`INSERT INTO audit_log (ts, action, entity) VALUES (?, 'x', 'y')`, ts); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Prune(ctx, now, 7); err != nil {
		t.Fatal(err)
	}
	var n int
	s.R.QueryRow(`SELECT count(*) FROM alert_log`).Scan(&n)
	if n != 1 {
		t.Errorf("alert_log rows = %d, want 1", n)
	}
	s.R.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&n)
	if n != 1 {
		t.Errorf("audit_log rows = %d, want 1", n)
	}
}

func TestRollupIgnoresPausedCountsMaintenance(t *testing.T) {
	m := map[rollupKey]*rollupDelta{}
	addRollupDelta(m, Heartbeat{MonitorID: 1, Status: model.StatusPaused, LatencyMs: 10}, 0)
	addRollupDelta(m, Heartbeat{MonitorID: 1, Status: model.StatusMaintenance, LatencyMs: 10}, 0)
	d := m[rollupKey{monitorID: 1, bucket: 0}]
	if d.up+d.down+d.unknown+d.degraded+d.pending != 0 {
		t.Errorf("paused/maintenance counted as a status: %+v", d)
	}
	if d.maint != 1 {
		t.Errorf("maint = %d, want 1", d.maint)
	}
}
