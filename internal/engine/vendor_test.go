package engine

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"opskest/internal/model"
)

// A 3rd-party feed (Settings.Vendor) is alert-only: its Down, Reminder and
// Recovered alerts go out, but it never opens an outage and never counts
// toward uptime, clusters or groups.
func TestVendorDownAlertsWithoutOutage(t *testing.T) {
	h := newHarness(t)
	v := h.monitor("provider", func(m *model.Monitor) {
		m.Retries = 0
		m.ReminderMin = 1
		m.Settings.Vendor = true
	})
	h.load()

	h.apply(v.ID, ok(10))
	h.apply(v.ID, fail("provider reports a major outage"))
	evs := h.sink.take()
	if len(evs) != 1 || evs[0].Kind != model.EvDown || evs[0].MonitorID != v.ID || evs[0].OutageID != 0 {
		t.Fatalf("down events = %+v, want exactly one Down with no outage id", evs)
	}
	// A second failing check does not alert again.
	h.apply(v.ID, fail("provider reports a major outage"))
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("repeat down alerted: %v", kinds(evs))
	}

	s := h.summary(v.ID)
	if s.Status != model.StatusDown || s.Reason == "" || !s.Vendor {
		t.Fatalf("summary = status %v reason %q vendor %v; the feed must still show its own state", s.Status, s.Reason, s.Vendor)
	}
	if s.OutageID != nil || s.Ack != nil {
		t.Fatalf("summary links an outage: %+v", s)
	}
	if s.Uptime24h != nil || s.Uptime7d != nil || s.Uptime30d != nil || s.Uptime90d != nil {
		t.Fatalf("a vendor has uptime: %v %v %v %v", s.Uptime24h, s.Uptime7d, s.Uptime30d, s.Uptime90d)
	}
	if outs := h.e.ActiveOutages(); len(outs) != 0 {
		t.Fatalf("ActiveOutages = %+v", outs)
	}

	// Reminders still fire (and are not tied to an outage id).
	h.clk.Advance(61 * time.Second)
	h.e.tickReminders()
	evs = h.sink.take()
	if got := kinds(evs); !reflect.DeepEqual(got, []model.EventKind{model.EvReminder}) || evs[0].OutageID != 0 {
		t.Fatalf("reminders = %+v, want one reminder", evs)
	}

	h.apply(v.ID, ok(10))
	evs = h.sink.take()
	if got := kinds(evs); !reflect.DeepEqual(got, []model.EventKind{model.EvRecovered}) || evs[0].Duration <= 0 {
		t.Fatalf("recovery = %+v, want one recovered with a duration", evs)
	}
	h.apply(v.ID, ok(10))
	h.e.tickReminders()
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("events after recovery: %v", kinds(evs))
	}

	h.w.Flush()
	if n := h.count(`SELECT count(*) FROM outages`); n != 0 {
		t.Fatalf("outages rows = %d, want 0", n)
	}
	if n := h.count(`SELECT count(*) FROM heartbeats WHERE monitor_id=?`, v.ID); n == 0 {
		t.Fatalf("a vendor's own beats are still recorded")
	}
	if n := h.count(`SELECT count(*) FROM rollup_hourly WHERE monitor_id=?`, v.ID) +
		h.count(`SELECT count(*) FROM rollup_daily WHERE monitor_id=?`, v.ID); n != 0 {
		t.Fatalf("a vendor has %d rollup rows, want 0", n)
	}
}

// A restart while the feed is down does not re-send its Down.
func TestVendorDownNotRepeatedAfterRestart(t *testing.T) {
	h := newHarness(t)
	v := h.monitor("provider", func(m *model.Monitor) { m.Retries = 0; m.Settings.Vendor = true })
	h.load()
	h.apply(v.ID, fail("down"))
	if len(h.sink.take()) != 1 {
		t.Fatal("want one down")
	}
	h.w.Flush()
	h.load() // same store, fresh in-memory state
	h.apply(v.ID, fail("down"))
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("down re-sent after restart: %v", kinds(evs))
	}
	h.apply(v.ID, ok(5))
	if got := kinds(h.sink.take()); !reflect.DeepEqual(got, []model.EventKind{model.EvRecovered}) {
		t.Fatalf("recovery after restart = %v", got)
	}
}

// Vendors never form a common-cause cluster, never hold a dependent's
// alerts, never sway a group and never go into maintenance.
func TestVendorExcludedFromClustersGroupsDepsAndMaintenance(t *testing.T) {
	h := newHarness(t)
	a := h.monitor("provider-a", func(m *model.Monitor) { m.Target = "https://x.co/a"; m.Retries = 0; m.Settings.Vendor = true })
	b := h.monitor("provider-b", func(m *model.Monitor) { m.Target = "https://x.co/b"; m.Retries = 0; m.Settings.Vendor = true })
	own := h.monitor("own", func(m *model.Monitor) { m.Target = "https://x.co/c"; m.Retries = 0 })
	h.load()

	for _, id := range []int64{a.ID, b.ID, own.ID} {
		h.apply(id, model.Result{OK: false, LatencyMs: -1, Reason: "timeout", ErrClass: model.ErrTimeout})
	}
	h.sink.take()
	if cs := h.e.Clusters(); len(cs) != 0 {
		t.Fatalf("clusters = %+v; vendors must not count (only one own monitor is down)", cs)
	}
	if p := h.e.downParent([]int64{a.ID, b.ID}); p != nil {
		t.Fatalf("a down vendor is a dependency parent: %d", *p)
	}

	// Groups ignore a vendor child.
	g := model.NewMonitor(model.TypeGroup)
	g.Name = "grp"
	g.Settings.Children = []int64{a.ID}
	if r := h.e.groupResult(g); !r.OK || r.Degraded {
		t.Fatalf("group result with only a down vendor child = %+v, want clean OK", r)
	}

	// Validation: a vendor can be neither a dependency parent nor a group child.
	if msg := h.e.ValidateDependencies(own.ID, []int64{a.ID}); !strings.Contains(msg, "3rd-party") {
		t.Fatalf("ValidateDependencies = %q", msg)
	}
	if msg := h.e.ValidateGroup(0, []int64{a.ID}); !strings.Contains(msg, "3rd-party") {
		t.Fatalf("ValidateGroup = %q", msg)
	}
	if got := h.e.VendorIDs([]int64{own.ID, a.ID, 999, b.ID}); !reflect.DeepEqual(got, []int64{a.ID, b.ID}) {
		t.Fatalf("VendorIDs = %v", got)
	}
}

// Turning an existing monitor into a feed drops its open outage.
func TestMonitorBecomingVendorDropsOutage(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", func(m *model.Monitor) { m.Retries = 0 })
	h.load()
	h.apply(m.ID, fail("down"))
	h.sink.take()
	h.w.Flush()
	if n := h.count(`SELECT count(*) FROM outages WHERE monitor_id=?`, m.ID); n != 1 {
		t.Fatalf("setup: outages = %d", n)
	}

	c := m.Clone()
	c.Settings.Vendor = true
	h.e.UpsertMonitor(c)
	h.w.Flush()
	if n := h.count(`SELECT count(*) FROM outages WHERE monitor_id=?`, m.ID); n != 0 {
		t.Fatalf("outage kept after becoming a vendor: %d", n)
	}
	if outs := h.e.ActiveOutages(); len(outs) != 0 {
		t.Fatalf("ActiveOutages = %+v", outs)
	}
	if s := h.summary(m.ID); s.OutageID != nil || !s.Vendor {
		t.Fatalf("summary = %+v", s)
	}
	// Its recovery is announced once, without an outage.
	h.apply(m.ID, ok(5))
	if got := kinds(h.sink.take()); !reflect.DeepEqual(got, []model.EventKind{model.EvRecovered}) {
		t.Fatalf("recovery = %v", got)
	}
}
