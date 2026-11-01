package engine

import (
	"testing"
	"time"

	"opskest/internal/model"
	"opskest/internal/store"
)

// restarted builds a second engine over the same store, as after a process
// restart (the first engine is simply forgotten).
func (h *harness) restarted() *Engine {
	h.t.Helper()
	h.w.Flush()
	e2 := New(Deps{Store: h.s, Writer: h.w, Sched: &fakeSched{}, Alerts: h.sink, Guard: h.guard, Now: h.clk.Now, Checker: h.check})
	if err := e2.Load(h.ctx); err != nil {
		h.t.Fatal(err)
	}
	return e2
}

// A paused monitor has no beats, so "paused since" must come from the status
// event that paused it, not be reset to the restart time.
func TestRestartKeepsPausedSince(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", nil)
	h.load()
	h.apply(m.ID, ok(5))
	h.clk.Advance(time.Minute)
	pausedAt := h.clk.Now()
	h.e.SetPaused(m.ID, true)
	if err := h.s.SetPaused(h.ctx, m.ID, true); err != nil {
		t.Fatal(err)
	}

	h.clk.Advance(3 * time.Hour)
	e2 := h.restarted()
	s, ok := e2.Summary(m.ID)
	if !ok || s.Status != model.StatusPaused {
		t.Fatalf("after restart: %+v", s)
	}
	if s.Since != pausedAt.UnixMilli() {
		t.Fatalf("paused since %v after a restart 3h later, want %v", time.UnixMilli(s.Since).UTC(), pausedAt.UTC())
	}
}

// The same for a monitor sitting in a maintenance window.
func TestRestartKeepsMaintenanceSince(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", nil)
	h.load()
	h.apply(m.ID, ok(5))
	h.clk.Advance(time.Minute)
	now := h.clk.Now()
	start, end := now.Add(-time.Minute).UnixMilli(), now.Add(6*time.Hour).UnixMilli()
	w := &store.Maintenance{Title: "w", Kind: "once", StartsAt: &start, EndsAt: &end, TZ: "UTC", Active: true, MonitorIDs: []int64{m.ID}}
	if err := h.s.CreateMaintenance(h.ctx, w); err != nil {
		t.Fatal(err)
	}
	if err := h.e.ReloadMaintenance(h.ctx); err != nil {
		t.Fatal(err)
	}
	enteredAt := h.clk.Now()

	h.clk.Advance(time.Hour)
	e2 := h.restarted()
	s, _ := e2.Summary(m.ID)
	if s.Status != model.StatusMaintenance {
		t.Fatalf("after restart: %v", s.Status)
	}
	if s.Since != enteredAt.UnixMilli() {
		t.Fatalf("maintenance since %v after a restart an hour later, want %v", time.UnixMilli(s.Since).UTC(), enteredAt.UTC())
	}
}

// If the "degraded" alert went out before a restart, the "back to normal"
// alert must follow when the monitor recovers afterwards.
func TestRestartKeepsDegradedAlertState(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", func(m *model.Monitor) { m.NotifyDegraded = true })
	h.load()
	slow := model.Result{OK: true, Degraded: true, LatencyMs: 900, Reason: "slow: 900 ms > 300 ms"}
	h.apply(m.ID, ok(5))
	h.sink.take()
	h.clk.Advance(30 * time.Second)
	h.apply(m.ID, slow)
	if got := kinds(h.sink.take()); len(got) != 1 || got[0] != model.EvDegraded {
		t.Fatalf("before restart: %v", got)
	}

	h.clk.Advance(time.Minute)
	e2 := h.restarted()
	h.clk.Advance(time.Minute)
	r := slow
	r.At = h.clk.Now()
	e2.Apply(m.ID, r) // still slow after the restart: nothing new to say
	if got := kinds(h.sink.take()); len(got) != 0 {
		t.Fatalf("a restart repeated the degraded alert: %v", got)
	}
	h.clk.Advance(time.Minute)
	r = ok(5)
	r.At = h.clk.Now()
	e2.Apply(m.ID, r)
	if got := kinds(h.sink.take()); len(got) != 1 || got[0] != model.EvDegradedCleared {
		t.Fatalf("after the restart the all-clear must still be sent, got %v", got)
	}
}
