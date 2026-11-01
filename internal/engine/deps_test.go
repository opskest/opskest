package engine

import (
	"reflect"
	"testing"
	"time"

	"opskest/internal/model"
)

// TestChildAlertsHeldWhileParentDown: the child's outage is recorded but
// sends nothing (no Down, no reminder, no Recovered) while the parent is
// down; the parent's own alerts are untouched.
func TestChildAlertsHeldWhileParentDown(t *testing.T) {
	h := newHarness(t)
	parent := h.monitor("gateway", func(m *model.Monitor) { m.Retries = 0; m.ReminderMin = 1 })
	child := h.monitor("api", func(m *model.Monitor) {
		m.Retries = 0
		m.ReminderMin = 1
		m.Settings.DependsOn = []int64{parent.ID}
	})
	h.load()

	h.apply(parent.ID, fail("connection refused"))
	h.apply(child.ID, fail("HTTP 502"))
	if got := kinds(h.sink.take()); !reflect.DeepEqual(got, []model.EventKind{model.EvDown}) {
		t.Fatalf("events = %v, want only the parent's down", got)
	}
	if s := h.summary(child.ID); s.Status != model.StatusDown || s.SuppressedBy == nil || *s.SuppressedBy != parent.ID {
		t.Fatalf("child summary: status=%v suppressedBy=%v", s.Status, s.SuppressedBy)
	}

	h.clk.Advance(2 * time.Minute)
	h.e.tickReminders()
	if evs := h.sink.take(); len(evs) != 1 || evs[0].MonitorID != parent.ID {
		t.Fatalf("reminders = %v, want only the parent's", kinds(evs))
	}

	// Both recover: only the parent's Recovered goes out.
	h.apply(parent.ID, ok(10))
	h.apply(child.ID, ok(10))
	evs := h.sink.take()
	if len(evs) != 1 || evs[0].Kind != model.EvRecovered || evs[0].MonitorID != parent.ID {
		t.Fatalf("recovery events = %+v, want only the parent's recovered", evs)
	}

	h.w.Flush()
	if n := h.count(`SELECT count(*) FROM outages WHERE monitor_id=? AND suppressed_by=? AND ended_at IS NOT NULL`, child.ID, parent.ID); n != 1 {
		t.Fatalf("child outage rows (held, closed) = %d, want 1", n)
	}
}

// TestHeldChildAlertsWhenParentRecoversFirst: if the parent comes back
// while the child is still down, the child is down on its own — its Down
// goes out then, and its later recovery alerts normally.
func TestHeldChildAlertsWhenParentRecoversFirst(t *testing.T) {
	h := newHarness(t)
	parent := h.monitor("gateway", func(m *model.Monitor) { m.Retries = 0 })
	child := h.monitor("api", func(m *model.Monitor) {
		m.Retries = 0
		m.Settings.DependsOn = []int64{parent.ID}
	})
	h.load()

	h.apply(parent.ID, fail("down"))
	h.apply(child.ID, fail("HTTP 502"))
	h.sink.take()

	h.e.releaseHeldOutages()
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("released while parent still down: %v", kinds(evs))
	}

	h.apply(parent.ID, ok(10))
	h.sink.take() // parent's recovered
	h.e.releaseHeldOutages()
	evs := h.sink.take()
	if len(evs) != 1 || evs[0].Kind != model.EvDown || evs[0].MonitorID != child.ID {
		t.Fatalf("after parent recovered: %+v, want the child's down", evs)
	}
	if s := h.summary(child.ID); s.SuppressedBy != nil {
		t.Fatalf("child still marked held: %v", *s.SuppressedBy)
	}
	h.e.releaseHeldOutages()
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("down re-sent: %v", kinds(evs))
	}

	h.apply(child.ID, ok(10))
	if got := kinds(h.sink.take()); !reflect.DeepEqual(got, []model.EventKind{model.EvRecovered}) {
		t.Fatalf("child recovery = %v, want [recovered]", got)
	}
	h.w.Flush()
	if n := h.count(`SELECT count(*) FROM outages WHERE monitor_id=? AND suppressed_by IS NULL`, child.ID); n != 1 {
		t.Fatalf("released outage not persisted: %d", n)
	}
}

// TestParentInMaintenanceHoldsChild: a parent under maintenance holds its
// children's alerts too (planned work upstream isn't the child's fault).
func TestParentInMaintenanceHoldsChild(t *testing.T) {
	h := newHarness(t)
	parent := h.monitor("db", nil)
	child := h.monitor("api", func(m *model.Monitor) {
		m.Retries = 0
		m.Settings.DependsOn = []int64{parent.ID}
	})
	h.load()
	h.e.setAffected(parent.ID, model.StatusMaintenance, false)
	h.apply(child.ID, fail("HTTP 502"))
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("child alerted while parent in maintenance: %v", kinds(evs))
	}
}

func TestValidateDependencies(t *testing.T) {
	h := newHarness(t)
	a := h.monitor("a", nil)
	b := h.monitor("b", func(m *model.Monitor) { m.Settings.DependsOn = []int64{a.ID} })
	c := h.monitor("c", func(m *model.Monitor) { m.Settings.DependsOn = []int64{b.ID} })
	h.load()

	if msg := h.e.ValidateDependencies(a.ID, []int64{c.ID}); msg == "" {
		t.Errorf("a→c closes a loop (c→b→a), want an error")
	}
	if msg := h.e.ValidateDependencies(a.ID, []int64{99999}); msg == "" {
		t.Errorf("unknown parent accepted")
	}
	if msg := h.e.ValidateDependencies(c.ID, []int64{a.ID, b.ID}); msg != "" {
		t.Errorf("c→a,b is a DAG, got %q", msg)
	}
	if msg := h.e.ValidateDependencies(0, []int64{c.ID}); msg != "" {
		t.Errorf("new monitor can't make a loop, got %q", msg)
	}
}

func TestLatencySamplesWindowAndSuccessOnly(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", func(m *model.Monitor) {
		m.Retries = 3
		m.Settings.LatencyAlert = &model.LatencyAlert{Percentile: 95, ThresholdMs: 500, WindowMin: 5}
	})
	plain := h.monitor("web", nil)
	h.load()

	h.apply(m.ID, ok(900)) // will fall out of the 5-min window
	h.clk.Advance(6 * time.Minute)
	h.apply(m.ID, ok(100))
	h.apply(m.ID, fail("timeout")) // pending: not a success
	h.apply(m.ID, ok(200))
	h.apply(plain.ID, ok(50))

	got := h.e.LatencySamples(h.clk.Now())
	if len(got) != 1 || got[0].ID != m.ID {
		t.Fatalf("samples for %d monitors, want only the one with an alert: %+v", len(got), got)
	}
	if !reflect.DeepEqual(got[0].Values, []int{100, 200}) {
		t.Fatalf("values = %v, want [100 200]", got[0].Values)
	}
}

// TestHeldOutageNotReleasedWhilePaused: a parent recovering while the
// child is paused must not burn the child's held Down — it goes out once
// the child is back and still down, and no reminder follows immediately.
func TestHeldOutageNotReleasedWhilePaused(t *testing.T) {
	h := newHarness(t)
	parent := h.monitor("gateway", func(m *model.Monitor) { m.Retries = 0 })
	child := h.monitor("api", func(m *model.Monitor) {
		m.Retries = 0
		m.ReminderMin = 1
		m.Settings.DependsOn = []int64{parent.ID}
	})
	h.load()

	h.apply(parent.ID, fail("down"))
	h.clk.Advance(10 * time.Minute) // held for longer than ReminderMin
	h.apply(child.ID, fail("HTTP 502"))
	h.sink.take()

	h.e.SetPaused(child.ID, true)
	h.apply(parent.ID, ok(10))
	h.sink.take()
	h.e.releaseHeldOutages()
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("released while paused: %v", kinds(evs))
	}

	h.e.SetPaused(child.ID, false)
	h.apply(child.ID, fail("HTTP 502"))
	h.e.releaseHeldOutages()
	if got := kinds(h.sink.take()); !reflect.DeepEqual(got, []model.EventKind{model.EvDown}) {
		t.Fatalf("after unpause: %v, want [down]", got)
	}
	h.clk.Advance(30 * time.Second)
	h.e.tickReminders()
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("reminder right after the released down: %v", kinds(evs))
	}
}
