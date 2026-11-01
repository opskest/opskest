package engine

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"opskest/internal/model"
)

func (h *harness) group(name, mode string, min int, children ...*model.Monitor) *model.Monitor {
	h.t.Helper()
	return h.monitor(name, func(m *model.Monitor) {
		m.Type = model.TypeGroup
		m.Target = ""
		m.Retries = 0
		m.Settings.GroupMode = mode
		m.Settings.GroupMin = min
		for _, c := range children {
			m.Settings.Children = append(m.Settings.Children, c.ID)
		}
	})
}

func noRetries(m *model.Monitor) { m.Retries = 0 }

func (h *harness) runGroup(g *model.Monitor) State {
	h.t.Helper()
	h.clk.Advance(time.Second)
	h.e.RunCheck(h.ctx, g.ID)
	st, _ := h.e.stateOf(g.ID)
	return st
}

func TestGroupAllMode(t *testing.T) {
	h := newHarness(t)
	a := h.monitor("a", noRetries)
	b := h.monitor("b", noRetries)
	g := h.group("grp", "all", 0, a, b)
	h.load()

	h.apply(a.ID, ok(1))
	h.apply(b.ID, ok(1))
	if st := h.runGroup(g); st.Status != model.StatusUp {
		t.Fatalf("both up: group is %v, want up", st.Status)
	}
	h.sink.take()

	h.clk.Advance(time.Second)
	h.apply(a.ID, fail("HTTP 500"))
	h.sink.take()
	if st := h.runGroup(g); st.Status != model.StatusDown {
		t.Fatalf("one child down: group is %v, want down", st.Status)
	}
	evs := h.sink.take()
	if len(evs) != 1 || evs[0].Kind != model.EvDown || evs[0].MonitorID != g.ID {
		t.Fatalf("events = %+v, want one Down for the group", evs)
	}
	if !strings.Contains(evs[0].Reason, "a") || !strings.Contains(evs[0].Reason, "1 of 2") {
		t.Errorf("reason = %q, want it to name the failing child and the count", evs[0].Reason)
	}

	h.clk.Advance(time.Second)
	h.apply(a.ID, ok(1))
	h.sink.take()
	if st := h.runGroup(g); st.Status != model.StatusUp {
		t.Fatalf("child recovered: group is %v, want up", st.Status)
	}
	if got := kinds(h.sink.take()); !reflect.DeepEqual(got, []model.EventKind{model.EvRecovered}) {
		t.Fatalf("events = %v, want [recovered]", got)
	}
}

func TestGroupAnyAndMinModes(t *testing.T) {
	h := newHarness(t)
	a := h.monitor("a", noRetries)
	b := h.monitor("b", noRetries)
	c := h.monitor("c", noRetries)
	anyG := h.group("any", "any", 0, a, b)
	minG := h.group("min", "min", 2, a, b, c)
	h.load()
	for _, m := range []*model.Monitor{a, b, c} {
		h.apply(m.ID, ok(1))
	}

	h.apply(a.ID, fail("x"))
	// any: one of two down is only a degradation; min 2 of 3: two still up.
	if st := h.runGroup(anyG); st.Status != model.StatusDegraded {
		t.Fatalf("any, one down: %v, want degraded", st.Status)
	}
	if st := h.runGroup(minG); st.Status != model.StatusDegraded {
		t.Fatalf("min 2 of 3, one down: %v, want degraded", st.Status)
	}

	h.apply(b.ID, fail("x"))
	if st := h.runGroup(anyG); st.Status != model.StatusDown {
		t.Fatalf("any, all down: %v, want down", st.Status)
	}
	if st := h.runGroup(minG); st.Status != model.StatusDown {
		t.Fatalf("min 2 of 3, two down: %v, want down", st.Status)
	}
	s := h.summary(minG.ID)
	if !strings.Contains(s.Reason, "1 of 3 monitors up, at least 2 needed") {
		t.Errorf("reason = %q", s.Reason)
	}
}

func TestGroupIgnoresPausedAndUncheckedChildren(t *testing.T) {
	h := newHarness(t)
	a := h.monitor("a", noRetries)
	b := h.monitor("b", noRetries)
	g := h.group("grp", "all", 0, a, b)
	h.load()

	// Nothing has been checked yet: unknown children say nothing either way.
	if st := h.runGroup(g); st.Status != model.StatusUp {
		t.Fatalf("all unknown: %v, want up (nothing to judge)", st.Status)
	}
	h.apply(a.ID, fail("x"))
	h.e.SetPaused(a.ID, true)
	if st := h.runGroup(g); st.Status != model.StatusUp {
		t.Fatalf("down child is paused, other unchecked: %v, want up", st.Status)
	}
}

func TestGroupDoesNotNeedTheInternetGuard(t *testing.T) {
	h := newHarness(t)
	a := h.monitor("a", noRetries)
	g := h.group("grp", "all", 0, a)
	h.load()
	h.apply(a.ID, ok(1))
	// The child failed while the internet was reachable; now the guard says offline.
	h.apply(a.ID, fail("x"))
	h.guard.offline.Store(true)
	if st := h.runGroup(g); st.Status != model.StatusDown {
		t.Fatalf("group = %v while the guard is offline, want down", st.Status)
	}
}

func TestValidateGroup(t *testing.T) {
	h := newHarness(t)
	a := h.monitor("a", nil)
	g1 := h.group("g1", "all", 0, a)
	g2 := h.group("g2", "all", 0, g1)
	h.load()

	if msg := h.e.ValidateGroup(0, []int64{a.ID, g1.ID}); msg != "" {
		t.Errorf("valid children rejected: %q", msg)
	}
	if msg := h.e.ValidateGroup(0, []int64{9999}); !strings.Contains(msg, "doesn't exist") {
		t.Errorf("missing child: %q", msg)
	}
	// g1 -> g2 would close g2 -> g1 -> g2.
	if msg := h.e.ValidateGroup(g1.ID, []int64{g2.ID}); !strings.Contains(msg, "contain itself") {
		t.Errorf("cycle accepted: %q", msg)
	}
	if msg := h.e.ValidateGroup(g2.ID, []int64{a.ID}); msg != "" {
		t.Errorf("valid edit rejected: %q", msg)
	}
}

func TestManualMonitor(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("nightly", func(m *model.Monitor) {
		m.Type = model.TypeManual
		m.Target = ""
		m.Retries = 5 // ignored: a manual status takes effect at once
	})
	web := h.monitor("web", nil)
	h.load()

	h.e.RunCheck(h.ctx, m.ID)
	if st, _ := h.e.stateOf(m.ID); st.Status != model.StatusUp {
		t.Fatalf("a new manual monitor is %v, want up", st.Status)
	}

	h.clk.Advance(time.Second)
	if !h.e.SetManual(m.ID, "down", "batch job failed") {
		t.Fatal("SetManual down was refused")
	}
	if st, _ := h.e.stateOf(m.ID); st.Status != model.StatusDown {
		t.Fatalf("after set down: %v, want down (retries do not apply)", st.Status)
	}
	evs := h.sink.take()
	var down *model.Event
	for i := range evs {
		if evs[i].MonitorID == m.ID && evs[i].Kind == model.EvDown {
			down = &evs[i]
		}
	}
	if down == nil || down.Reason != "batch job failed" {
		t.Fatalf("down event = %+v", evs)
	}

	// The scheduled run re-asserts the status without a second alert.
	h.clk.Advance(time.Minute)
	h.e.RunCheck(h.ctx, m.ID)
	if st, _ := h.e.stateOf(m.ID); st.Status != model.StatusDown {
		t.Fatalf("a scheduled run changed a manual status: %v", st.Status)
	}
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("re-asserting alerted again: %+v", evs)
	}

	h.clk.Advance(time.Second)
	h.e.SetManual(m.ID, "degraded", "")
	if st, _ := h.e.stateOf(m.ID); st.Status != model.StatusDegraded {
		t.Fatalf("after set degraded: %v", st.Status)
	}
	h.e.SetManual(m.ID, "up", "")
	if st, _ := h.e.stateOf(m.ID); st.Status != model.StatusUp {
		t.Fatalf("after set up: %v", st.Status)
	}

	// Offline guard does not turn a manual status into "unknown".
	h.guard.offline.Store(true)
	h.e.SetManual(m.ID, "down", "")
	if st, _ := h.e.stateOf(m.ID); st.Status != model.StatusDown {
		t.Fatalf("manual status while the guard is offline: %v", st.Status)
	}

	if h.e.SetManual(web.ID, "down", "") {
		t.Error("SetManual accepted a non-manual monitor")
	}
	if h.e.SetManual(m.ID, "sideways", "") {
		t.Error("SetManual accepted an invalid status")
	}
	if h.e.SetManual(424242, "up", "") {
		t.Error("SetManual accepted an unknown id")
	}
	if h.e.RunNow(m.ID) {
		t.Error("RunNow should not apply to a manual monitor")
	}
}

func TestPerMonitorCertThresholds(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", func(m *model.Monitor) { m.Settings.CertThresholds = []int{30, 1, 14, 7} }) // unsorted on purpose
	h.load()

	cert := func(notAfter time.Time) model.Result {
		r := ok(10)
		r.Cert = &model.CertInfo{NotAfter: notAfter, Issuer: "CA", Subject: "api"}
		return r
	}
	na := h.clk.Now().Add(29*24*time.Hour + 6*time.Hour)

	h.apply(m.ID, cert(na))
	evs := h.sink.take()
	if len(evs) != 1 || evs[0].Kind != model.EvCertExpiring || evs[0].Days != 29 {
		t.Fatalf("at 29 days: %+v, want one alert (the default ladder would stay silent)", evs)
	}
	h.clk.Advance(time.Minute)
	h.apply(m.ID, cert(na))
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("same threshold alerted twice: %+v", evs)
	}
	for _, step := range []struct{ advance, days int }{{15, 14}, {7, 7}, {6, 1}} {
		h.clk.Advance(time.Duration(step.advance) * 24 * time.Hour)
		h.apply(m.ID, cert(na))
		evs := h.sink.take()
		if len(evs) != 1 || evs[0].Days != step.days {
			t.Fatalf("expected one alert at %d days, got %+v", step.days, evs)
		}
	}
}

func TestDefaultCertThresholdsUnchanged(t *testing.T) {
	m := model.NewMonitor(model.TypeHTTP)
	if got := certThresholdsFor(m); !reflect.DeepEqual(got, []int{1, 3, 7, 14}) {
		t.Fatalf("default ladder = %v", got)
	}
}

func TestGroupAndManualAreNotClusteredAsACommonCause(t *testing.T) {
	h := newHarness(t)
	a := h.monitor("a", noRetries)
	b := h.monitor("b", noRetries)
	g := h.group("grp", "any", 0, a, b)
	man := h.monitor("hand", func(m *model.Monitor) { m.Type = model.TypeManual; m.Target = "" })
	h.load()

	h.apply(a.ID, ok(1))
	h.apply(b.ID, ok(1))
	h.apply(a.ID, model.Result{OK: false, LatencyMs: -1, ErrClass: model.ErrOther, Reason: "x"})
	h.apply(b.ID, ok(1))
	h.runGroup(g)
	h.e.SetManual(man.ID, "down", "by hand")
	for _, c := range h.e.Clusters() {
		for _, id := range c.MonitorIDs {
			if id == g.ID || id == man.ID {
				t.Fatalf("cluster %+v includes a group or manual monitor", c)
			}
		}
	}
}
