package engine

import (
	"testing"

	"opskest/internal/model"
)

// A monitor someone has just created must be checked within seconds, not at a
// random moment of its first interval (a 5-minute monitor would sit UNKNOWN
// for up to 5 minutes). Boot still spreads first runs over the interval.
func TestNewMonitorIsCheckedSoon(t *testing.T) {
	h := newHarness(t)
	h.load()

	create := func(name string, mut func(*model.Monitor)) *model.Monitor {
		m := model.NewMonitor(model.TypeHTTP)
		m.Name, m.Target = name, "https://"+name+".example.com"
		m.IntervalS = 300
		if mut != nil {
			mut(m)
		}
		if err := h.s.CreateMonitor(h.ctx, m); err != nil {
			t.Fatal(err)
		}
		h.e.UpsertMonitor(m)
		return m
	}

	for i := 0; i < 30; i++ { // the delay is random: look at many
		m := create("a", nil)
		c, ok := h.sched.lastReschedule()
		if !ok {
			t.Fatal("a new monitor must get an early first check")
		}
		if c.id != m.ID || c.d < 0 || c.d >= newMonitorFirstCheckWithin {
			t.Fatalf("first check scheduled in %v for monitor %d (want < %v)", c.d, c.id, newMonitorFirstCheckWithin)
		}
	}

	// not for monitors that are paused, nor for push monitors (no active check)
	h.sched.mu.Lock()
	n := len(h.sched.reschedules)
	h.sched.mu.Unlock()
	create("paused", func(m *model.Monitor) { m.Paused = true })
	push := model.NewMonitor(model.TypePush)
	push.Name, push.IntervalS = "job", 300
	if err := h.s.CreateMonitor(h.ctx, push); err != nil {
		t.Fatal(err)
	}
	h.e.UpsertMonitor(push)
	h.sched.mu.Lock()
	defer h.sched.mu.Unlock()
	if len(h.sched.reschedules) != n {
		t.Fatalf("paused/push monitors must not be rescheduled for an immediate check: %v", h.sched.reschedules[n:])
	}
}
