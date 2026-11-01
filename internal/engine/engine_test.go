package engine

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opskest/internal/check"
	"opskest/internal/model"
	"opskest/internal/secret"
	"opskest/internal/store"
)

// ---- fakes ----------------------------------------------------------------

type schedCall struct {
	id int64
	d  time.Duration
}

type fakeSched struct {
	mu          sync.Mutex
	upserts     []schedCall
	reschedules []schedCall
	removes     []int64
}

func (f *fakeSched) Upsert(id int64, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserts = append(f.upserts, schedCall{id, d})
}

func (f *fakeSched) Reschedule(id int64, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reschedules = append(f.reschedules, schedCall{id, d})
}

func (f *fakeSched) Remove(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removes = append(f.removes, id)
}

func (f *fakeSched) upserted(id int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.upserts {
		if c.id == id {
			return true
		}
	}
	return false
}

func (f *fakeSched) lastReschedule() (schedCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reschedules) == 0 {
		return schedCall{}, false
	}
	return f.reschedules[len(f.reschedules)-1], true
}

type fakeSink struct {
	mu  sync.Mutex
	evs []model.Event
}

func (f *fakeSink) Publish(ev model.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.evs = append(f.evs, ev)
}

// take returns and clears the collected events.
func (f *fakeSink) take() []model.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.evs
	f.evs = nil
	return out
}

func kinds(evs []model.Event) []model.EventKind {
	var out []model.EventKind
	for _, e := range evs {
		out = append(out, e.Kind)
	}
	return out
}

type fakeGuard struct{ offline atomic.Bool }

func (g *fakeGuard) Online(context.Context) bool { return !g.offline.Load() }

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// stateOf exposes a monitor's state for tests: Status is the displayed
// status, UnderStatus the one ignoring maintenance and pause.
func (e *Engine) stateOf(id int64) (State, bool) {
	en := e.lookup(id)
	if en == nil {
		return State{}, false
	}
	en.mu.Lock()
	defer en.mu.Unlock()
	return State{Status: en.disp, Since: en.dispSince, Fails: en.under.Fails, UnderStatus: en.under.Status}, true
}

// ---- harness ----------------------------------------------------------------

type harness struct {
	t     *testing.T
	ctx   context.Context
	s     *store.Store
	w     *store.Writer
	e     *Engine
	sched *fakeSched
	sink  *fakeSink
	guard *fakeGuard
	clk   *fakeClock

	mu      sync.Mutex
	scripts map[int64][]model.Result
}

var ist = time.FixedZone("IST", 19800)

func newHarness(t *testing.T) *harness {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = 7
	}
	box, err := secret.New(key)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(t.TempDir(), box, ist)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := s.NewWriter(log)
	w.Start(context.Background())
	t.Cleanup(func() {
		w.Close()
		s.Close()
	})

	h := &harness{
		t: t, ctx: context.Background(), s: s, w: w,
		sched: &fakeSched{}, sink: &fakeSink{}, guard: &fakeGuard{},
		clk:     &fakeClock{t: time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)},
		scripts: map[int64][]model.Result{},
	}
	h.e = New(Deps{
		Store: s, Writer: w, Sched: h.sched, Alerts: h.sink, Guard: h.guard,
		Now: h.clk.Now, Log: log, Checker: h.check,
	})
	return h
}

// check is the scripted Checker: it pops the next queued result for m.
func (h *harness) check(_ context.Context, m *model.Monitor) model.Result {
	h.mu.Lock()
	defer h.mu.Unlock()
	q := h.scripts[m.ID]
	if len(q) == 0 {
		return model.Result{At: h.clk.Now(), OK: true, LatencyMs: 1}
	}
	r := q[0]
	h.scripts[m.ID] = q[1:]
	r.At = h.clk.Now()
	return r
}

func (h *harness) script(id int64, rs ...model.Result) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.scripts[id] = append(h.scripts[id], rs...)
}

func (h *harness) monitor(name string, mut func(*model.Monitor)) *model.Monitor {
	h.t.Helper()
	m := model.NewMonitor(model.TypeHTTP)
	m.Name = name
	m.Target = "https://" + name + ".example.com/health"
	if mut != nil {
		mut(m)
	}
	m.ApplyDefaults()
	if err := h.s.CreateMonitor(h.ctx, m); err != nil {
		h.t.Fatalf("CreateMonitor: %v", err)
	}
	return m
}

func (h *harness) load() {
	h.t.Helper()
	if err := h.e.Load(h.ctx); err != nil {
		h.t.Fatalf("Load: %v", err)
	}
}

// apply stamps r with the fake clock and applies it.
func (h *harness) apply(id int64, r model.Result) time.Duration {
	r.At = h.clk.Now()
	return h.e.Apply(id, r)
}

func (h *harness) summary(id int64) MonitorSummary {
	h.t.Helper()
	s, ok := h.e.Summary(id)
	if !ok {
		h.t.Fatalf("no summary for %d", id)
	}
	return s
}

func (h *harness) count(q string, args ...any) int {
	h.t.Helper()
	var n int
	if err := h.s.R.QueryRowContext(h.ctx, q, args...).Scan(&n); err != nil {
		h.t.Fatalf("%s: %v", q, err)
	}
	return n
}

func ok(lat int) model.Result { return model.Result{OK: true, LatencyMs: lat} }

func fail(reason string) model.Result {
	return model.Result{OK: false, LatencyMs: -1, Reason: reason, ErrClass: model.ErrHTTPStatus}
}

// ---- tests ------------------------------------------------------------------

func TestDownThenRecoverEmitsEventsAndOutage(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", func(m *model.Monitor) { m.Retries = 1 })
	h.load()

	h.apply(m.ID, fail("HTTP 503"))
	h.clk.Advance(20 * time.Second)
	flipAt := h.clk.Now()
	h.apply(m.ID, fail("HTTP 502"))
	h.clk.Advance(45 * time.Second)
	okAt := h.clk.Now()
	h.apply(m.ID, ok(30))

	evs := h.sink.take()
	if got := kinds(evs); !reflect.DeepEqual(got, []model.EventKind{model.EvDown, model.EvRecovered}) {
		t.Fatalf("events = %v, want [down recovered]", got)
	}
	if evs[1].Duration != okAt.Sub(flipAt) {
		t.Errorf("Recovered.Duration = %v, want %v", evs[1].Duration, okAt.Sub(flipAt))
	}
	if evs[0].OutageID == 0 || evs[0].OutageID != evs[1].OutageID {
		t.Errorf("outage ids: down=%d recovered=%d", evs[0].OutageID, evs[1].OutageID)
	}

	h.w.Flush()
	if n := h.count(`SELECT count(*) FROM heartbeats WHERE monitor_id=?`, m.ID); n != 3 {
		t.Errorf("heartbeats = %d, want 3", n)
	}
	var reason string
	var endedAt *int64
	if err := h.s.R.QueryRow(`SELECT first_reason, ended_at FROM outages WHERE monitor_id=?`, m.ID).Scan(&reason, &endedAt); err != nil {
		t.Fatal(err)
	}
	if reason != "HTTP 503" {
		t.Errorf("first_reason = %q, want the first failure's reason", reason)
	}
	if endedAt == nil || *endedAt != okAt.UnixMilli() {
		t.Errorf("ended_at = %v, want %d", endedAt, okAt.UnixMilli())
	}
	if n := h.count(`SELECT count(*) FROM outages`); n != 1 {
		t.Errorf("outages = %d, want 1", n)
	}
	if s := h.summary(m.ID); s.Status != model.StatusUp || s.OutageID != nil {
		t.Errorf("summary after recovery: status=%v outage=%v", s.Status, s.OutageID)
	}
}

func TestRetryIntervalWhilePending(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", func(m *model.Monitor) { m.IntervalS, m.RetryIntervalS, m.Retries = 60, 20, 2 })
	h.load()

	steps := []struct {
		r    model.Result
		want time.Duration
	}{
		{ok(10), 60 * time.Second},
		{fail("x"), 20 * time.Second}, // PENDING 1
		{fail("x"), 20 * time.Second}, // PENDING 2
		{fail("x"), 60 * time.Second}, // DOWN
		{fail("x"), 60 * time.Second}, // still DOWN
		{ok(10), 60 * time.Second},
	}
	for i, s := range steps {
		if got := h.apply(m.ID, s.r); got != s.want {
			t.Errorf("step %d: delay = %v, want %v", i, got, s.want)
		}
		h.clk.Advance(time.Second)
	}
}

func TestUpsideDown(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", func(m *model.Monitor) { m.UpsideDown = true; m.Retries = 0 })
	h.load()

	h.apply(m.ID, ok(10))
	s := h.summary(m.ID)
	if s.Status != model.StatusDown {
		t.Fatalf("status = %v, want down", s.Status)
	}
	if s.Reason != "reachable, but upside-down mode expects it to be down" {
		t.Fatalf("reason = %q", s.Reason)
	}
	if got := kinds(h.sink.take()); !reflect.DeepEqual(got, []model.EventKind{model.EvDown}) {
		t.Fatalf("events = %v", got)
	}

	h.clk.Advance(time.Minute)
	h.apply(m.ID, fail("connection refused"))
	if s := h.summary(m.ID); s.Status != model.StatusUp || s.Reason != "" {
		t.Fatalf("failing check on upside-down monitor: status=%v reason=%q, want up", s.Status, s.Reason)
	}
}

func TestGuardSuppresses(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", func(m *model.Monitor) { m.Retries = 0 })
	h.load()
	h.apply(m.ID, ok(10))
	h.sink.take()
	h.e.TakeDirty()

	h.guard.offline.Store(true)
	h.clk.Advance(time.Minute)
	offAt := h.clk.Now()
	h.apply(m.ID, fail("dial tcp: i/o timeout"))

	st, _ := h.e.stateOf(m.ID)
	if st.Status != model.StatusUp || st.UnderStatus != model.StatusUp || st.Fails != 0 {
		t.Fatalf("state changed under guard: %+v", st)
	}
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("events under guard: %v", kinds(evs))
	}
	if online, since := h.e.Connectivity(); online || !since.Equal(offAt) {
		t.Fatalf("Connectivity = %v since %v, want offline since %v", online, since, offAt)
	}
	if d := h.e.TakeDirty(); !d.System {
		t.Fatalf("connectivity change must set Dirty.System: %+v", d)
	}
	if s := h.summary(m.ID); !strings.HasSuffix(s.Beats, "uk") {
		t.Fatalf("beats = %q, want ...uk", s.Beats)
	}

	h.w.Flush()
	var status int
	var msg string
	if err := h.s.R.QueryRow(`SELECT status, msg FROM heartbeats WHERE monitor_id=? AND ts=?`, m.ID, offAt.UnixMilli()).Scan(&status, &msg); err != nil {
		t.Fatal(err)
	}
	if model.Status(status) != model.StatusUnknown || msg != "opskest lost internet connectivity" {
		t.Fatalf("guard beat = (%v, %q)", model.Status(status), msg)
	}

	h.guard.offline.Store(false)
	h.clk.Advance(time.Minute)
	h.apply(m.ID, fail("HTTP 500"))
	if got := kinds(h.sink.take()); !reflect.DeepEqual(got, []model.EventKind{model.EvDown}) {
		t.Fatalf("after guard back online: events = %v, want [down]", got)
	}
	if online, _ := h.e.Connectivity(); !online {
		t.Fatal("Connectivity should be restored")
	}
}

func TestMaintenanceSuppressesAndFiresOnExit(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", func(m *model.Monitor) { m.Retries = 1 })
	h.load()
	h.apply(m.ID, ok(10))

	now := h.clk.Now()
	start, end := now.Add(-time.Hour).UnixMilli(), now.Add(time.Hour).UnixMilli()
	w := &store.Maintenance{Title: "deploy", Kind: "once", StartsAt: &start, EndsAt: &end, TZ: "UTC", Active: true, MonitorIDs: []int64{m.ID}}
	if err := h.s.CreateMaintenance(h.ctx, w); err != nil {
		t.Fatal(err)
	}
	if err := h.e.ReloadMaintenance(h.ctx); err != nil {
		t.Fatal(err)
	}
	if got := h.e.ActiveMaintenance(); len(got) != 1 || got[0].ID != w.ID {
		t.Fatalf("ActiveMaintenance = %+v", got)
	}

	for i := 0; i < 3; i++ {
		h.clk.Advance(20 * time.Second)
		h.apply(m.ID, fail("HTTP 503"))
	}
	s := h.summary(m.ID)
	if s.Status != model.StatusMaintenance || !s.Maint {
		t.Fatalf("displayed = %v maint=%v, want maintenance", s.Status, s.Maint)
	}
	if !strings.HasSuffix(s.Beats, "mmm") {
		t.Fatalf("beats = %q, want ...mmm", s.Beats)
	}
	st, _ := h.e.stateOf(m.ID)
	if st.UnderStatus != model.StatusDown {
		t.Fatalf("under status = %v, want down", st.UnderStatus)
	}
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("events during maintenance: %v", kinds(evs))
	}
	h.w.Flush()
	if n := h.count(`SELECT count(*) FROM heartbeats WHERE monitor_id=? AND maint=1`, m.ID); n != 3 {
		t.Fatalf("maint beats = %d, want 3", n)
	}
	if n := h.count(`SELECT count(*) FROM outages`); n != 0 {
		t.Fatalf("no outage may open during maintenance, got %d", n)
	}

	if err := h.s.DeleteMaintenance(h.ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	h.clk.Advance(time.Second)
	if err := h.e.ReloadMaintenance(h.ctx); err != nil {
		t.Fatal(err)
	}
	evs := h.sink.take()
	if got := kinds(evs); !reflect.DeepEqual(got, []model.EventKind{model.EvDown}) {
		t.Fatalf("on exit: events = %v, want [down]", got)
	}
	if s := h.summary(m.ID); s.Status != model.StatusDown || s.OutageID == nil {
		t.Fatalf("after exit: status=%v outage=%v", s.Status, s.OutageID)
	}
	// Reloading again (or the 15 s tick) must not fire a second Down.
	if err := h.e.ReloadMaintenance(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.clk.Advance(20 * time.Second)
	h.apply(m.ID, fail("HTTP 503"))
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("second down fired: %v", kinds(evs))
	}
}

func TestReminder(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", func(m *model.Monitor) { m.Retries = 0; m.ReminderMin = 1 })
	h.load()

	h.apply(m.ID, fail("HTTP 503"))
	down := h.sink.take()
	if len(down) != 1 || down[0].Kind != model.EvDown {
		t.Fatalf("want down, got %v", kinds(down))
	}

	h.clk.Advance(30 * time.Second)
	h.e.tickReminders()
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("reminder too early: %v", kinds(evs))
	}
	h.clk.Advance(31 * time.Second)
	h.e.tickReminders()
	evs := h.sink.take()
	if got := kinds(evs); !reflect.DeepEqual(got, []model.EventKind{model.EvReminder}) {
		t.Fatalf("after 61s: %v, want [reminder]", got)
	}
	if evs[0].OutageID != down[0].OutageID || evs[0].Duration != 61*time.Second {
		t.Fatalf("reminder event = %+v", evs[0])
	}
	h.e.tickReminders()
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("reminder repeated immediately: %v", kinds(evs))
	}

	if err := h.e.Ack(down[0].OutageID, "alice"); err != nil {
		t.Fatal(err)
	}
	if s := h.summary(m.ID); s.Ack == nil || s.Ack.By != "alice" || s.Ack.At != h.clk.Now().UnixMilli() {
		t.Fatalf("summary ack = %+v", s.Ack)
	}
	h.clk.Advance(5 * time.Minute)
	h.e.tickReminders()
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("acked outage got reminders: %v", kinds(evs))
	}
	outs := h.e.ActiveOutages()
	if len(outs) != 1 || outs[0].AckedBy == nil || *outs[0].AckedBy != "alice" {
		t.Fatalf("ActiveOutages = %+v", outs)
	}
	stored, err := h.s.ActiveOutages(h.ctx)
	if err != nil || len(stored) != 1 || stored[0].AckedBy == nil {
		t.Fatalf("store ack not persisted: %+v %v", stored, err)
	}
}

func TestCertExpiryAlertsOncePerThreshold(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", nil)
	h.load()

	withCert := func(notAfter time.Time) model.Result {
		r := ok(10)
		r.Cert = &model.CertInfo{NotAfter: notAfter, Issuer: "R3", Subject: "api.example.com"}
		return r
	}
	firstNA := h.clk.Now().Add(6*24*time.Hour + 12*time.Hour)
	h.apply(m.ID, withCert(firstNA))
	evs := h.sink.take()
	if len(evs) != 1 || evs[0].Kind != model.EvCertExpiring || evs[0].Days != 6 {
		t.Fatalf("first cert result: %+v", evs)
	}
	if s := h.summary(m.ID); s.CertDays == nil || *s.CertDays != 6 {
		t.Fatalf("certDays = %v, want 6", s.CertDays)
	}

	h.clk.Advance(time.Minute)
	h.apply(m.ID, withCert(firstNA))
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("repeat result alerted again: %+v", evs)
	}

	// A renewed (different) certificate resets the alerted threshold.
	h.clk.Advance(time.Minute)
	secondNA := h.clk.Now().Add(6*24*time.Hour + 2*time.Hour)
	h.apply(m.ID, withCert(secondNA))
	evs = h.sink.take()
	if len(evs) != 1 || evs[0].Kind != model.EvCertExpiring || evs[0].Days != 6 {
		t.Fatalf("new cert: %+v", evs)
	}

	h.w.Flush()
	rt, err := h.s.AllRuntime(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := rt[m.ID]
	if r.CertNotAfter == nil || *r.CertNotAfter != secondNA.UnixMilli() || r.CertIssuer != "R3" {
		t.Fatalf("runtime cert = %+v", r)
	}
	if r.CertAlertedDays == nil || *r.CertAlertedDays != 7 {
		t.Fatalf("cert_alerted_days = %v, want threshold 7", r.CertAlertedDays)
	}

	// Crossing the next threshold (3) alerts once more.
	h.clk.Advance(4 * 24 * time.Hour)
	h.apply(m.ID, withCert(secondNA))
	evs = h.sink.take()
	if len(evs) != 1 || evs[0].Days != 2 {
		t.Fatalf("threshold 3: %+v", evs)
	}
}

func TestCertAlertStateSurvivesRestart(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", nil)
	h.load()
	na := h.clk.Now().Add(6*24*time.Hour + 12*time.Hour)
	r := ok(10)
	r.Cert = &model.CertInfo{NotAfter: na}
	h.apply(m.ID, r)
	h.sink.take()
	h.w.Flush()

	// A fresh engine over the same store must not re-alert threshold 7.
	e2 := New(Deps{Store: h.s, Writer: h.w, Sched: &fakeSched{}, Alerts: h.sink, Guard: h.guard, Now: h.clk.Now, Checker: h.check})
	if err := e2.Load(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.clk.Advance(time.Minute)
	r.At = h.clk.Now()
	e2.Apply(m.ID, r)
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("re-alerted after restart: %+v", evs)
	}
}

func TestPushDeadline(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("cron", func(m *model.Monitor) {
		m.Type = model.TypePush
		m.Target = ""
		m.IntervalS = 60
		m.Settings.GraceS = 30
	})
	h.load()
	if m.PushToken == "" {
		t.Fatal("push token not generated")
	}

	// Before the deadline, RunCheck only re-arms.
	if d := h.e.RunCheck(h.ctx, m.ID); d <= 0 || d > 90*time.Second {
		t.Fatalf("RunCheck before deadline = %v", d)
	}
	if s := h.summary(m.ID); s.LastCheckAt != 0 {
		t.Fatalf("RunCheck before deadline recorded a beat: %+v", s)
	}

	if !h.e.Push(m.PushToken, "up", "", 12) {
		t.Fatal("Push with a valid token returned false")
	}
	if s := h.summary(m.ID); s.Status != model.StatusUp || s.LatencyMs != 12 {
		t.Fatalf("after push: status=%v lat=%d", s.Status, s.LatencyMs)
	}
	if c, ok := h.sched.lastReschedule(); !ok || c.id != m.ID || c.d != 90*time.Second {
		t.Fatalf("Reschedule = %+v, want (%d, 90s)", c, m.ID)
	}

	h.clk.Advance(91 * time.Second)
	h.e.RunCheck(h.ctx, m.ID)
	if s := h.summary(m.ID); s.Status != model.StatusPending || s.Reason != "no push received for 90 s" {
		t.Fatalf("after deadline: status=%v reason=%q", s.Status, s.Reason)
	}

	h.clk.Advance(time.Second)
	if !h.e.Push(m.PushToken, "down", "disk full", 0) {
		t.Fatal("down push rejected")
	}
	if s := h.summary(m.ID); s.Reason != "disk full" || s.Status != model.StatusPending {
		t.Fatalf("down push: status=%v reason=%q", s.Status, s.Reason)
	}

	if h.e.Push("no-such-token", "up", "", 0) {
		t.Fatal("unknown token accepted")
	}

	h.w.Flush()
	rt, _ := h.s.AllRuntime(h.ctx)
	if lp := rt[m.ID].LastPushAt; lp == nil || *lp != h.clk.Now().UnixMilli() {
		t.Fatalf("last_push_at = %v", lp)
	}
}

func TestApplyAfterRemoveIsDropped(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", nil)
	h.load()
	h.e.TakeDirty()

	h.e.RemoveMonitor(m.ID)
	if got := h.apply(m.ID, fail("HTTP 500")); got != 0 {
		t.Fatalf("Apply after remove returned %v", got)
	}
	h.w.Flush()
	if n := h.count(`SELECT count(*) FROM heartbeats`); n != 0 {
		t.Fatalf("heartbeats after remove = %d", n)
	}
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("events after remove: %v", kinds(evs))
	}
	if _, ok := h.e.Summary(m.ID); ok {
		t.Fatal("summary still present")
	}
	if d := h.e.TakeDirty(); !reflect.DeepEqual(d.Removed, []int64{m.ID}) {
		t.Fatalf("Dirty.Removed = %v", d.Removed)
	}
	h.sched.mu.Lock()
	removed := reflect.DeepEqual(h.sched.removes, []int64{m.ID})
	h.sched.mu.Unlock()
	if !removed {
		t.Fatalf("scheduler Remove not called: %v", h.sched.removes)
	}
	if got := h.e.RunCheck(h.ctx, m.ID); got != 0 {
		t.Fatalf("RunCheck after remove = %v", got)
	}
}

func TestUptimeIgnoresGap(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", func(m *model.Monitor) { m.Retries = 0 })
	h.load()

	if s := h.summary(m.ID); s.Uptime24h != nil {
		t.Fatalf("uptime with no beats = %v, want null", *s.Uptime24h)
	}
	for i := 0; i < 10; i++ {
		h.apply(m.ID, ok(10))
		h.clk.Advance(time.Minute)
	}
	h.clk.Advance(5 * time.Hour)
	for i := 0; i < 10; i++ {
		h.apply(m.ID, ok(10))
		h.clk.Advance(time.Minute)
	}
	if s := h.summary(m.ID); s.Uptime24h == nil || *s.Uptime24h != 100 {
		t.Fatalf("uptime24h = %v, want 100", s.Uptime24h)
	}

	// UNKNOWN (guard) beats count on neither side.
	h.guard.offline.Store(true)
	for i := 0; i < 5; i++ {
		h.apply(m.ID, fail("timeout"))
		h.clk.Advance(time.Minute)
	}
	h.guard.offline.Store(false)
	if s := h.summary(m.ID); *s.Uptime24h != 100 || *s.Uptime7d != 100 || *s.Uptime30d != 100 || *s.Uptime90d != 100 {
		t.Fatalf("uptimes after unknown beats = %v/%v/%v/%v", *s.Uptime24h, *s.Uptime7d, *s.Uptime30d, *s.Uptime90d)
	}

	h.apply(m.ID, fail("HTTP 500"))
	want := 20.0 / 21.0 * 100
	if s := h.summary(m.ID); math.Abs(*s.Uptime24h-want) > 1e-9 {
		t.Fatalf("uptime24h = %v, want %v", *s.Uptime24h, want)
	}

	// 25 h later the hourly window has slid past every beat; the daily
	// windows still hold them.
	h.clk.Advance(25 * time.Hour)
	s := h.summary(m.ID)
	if s.Uptime24h != nil {
		t.Fatalf("uptime24h after 25h idle = %v, want null", *s.Uptime24h)
	}
	if s.Uptime7d == nil || math.Abs(*s.Uptime7d-want) > 1e-9 {
		t.Fatalf("uptime7d = %v, want %v", s.Uptime7d, want)
	}
}

func TestUptimeLoadedFromRollups(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", nil)
	now := h.clk.Now()
	for i := 0; i < 9; i++ {
		h.w.Heartbeat(store.Heartbeat{MonitorID: m.ID, TS: now.Add(-time.Duration(i+1) * time.Hour).UnixMilli(), Status: model.StatusUp, LatencyMs: 5})
	}
	h.w.Heartbeat(store.Heartbeat{MonitorID: m.ID, TS: now.Add(-30 * time.Minute).UnixMilli(), Status: model.StatusDown, LatencyMs: -1})
	h.w.Heartbeat(store.Heartbeat{MonitorID: m.ID, TS: now.Add(-3 * 24 * time.Hour).UnixMilli(), Status: model.StatusDown, LatencyMs: -1})
	h.w.Flush()
	h.load()

	s := h.summary(m.ID)
	if s.Uptime24h == nil || math.Abs(*s.Uptime24h-90) > 1e-9 {
		t.Fatalf("uptime24h = %v, want 90", s.Uptime24h)
	}
	want7 := 9.0 / 11.0 * 100
	if s.Uptime7d == nil || math.Abs(*s.Uptime7d-want7) > 1e-9 {
		t.Fatalf("uptime7d = %v, want %v", s.Uptime7d, want7)
	}
}

func TestLoadRestoresState(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", nil)
	paused := h.monitor("paused", func(m *model.Monitor) { m.Paused = true })

	base := h.clk.Now().Add(-150 * time.Minute)
	for i := 0; i < 150; i++ {
		st := model.StatusUp
		if i == 149 {
			st = model.StatusDown
		}
		h.w.Heartbeat(store.Heartbeat{MonitorID: m.ID, TS: base.Add(time.Duration(i) * time.Minute).UnixMilli(), Status: st, LatencyMs: 20, Msg: "HTTP 503"})
	}
	downAt := base.Add(149 * time.Minute).UnixMilli()
	h.w.DoFor(m.ID, store.OpOpenOutage(store.Outage{ID: 7, MonitorID: m.ID, StartedAt: downAt, FirstReason: "HTTP 503"}))
	h.w.Flush()

	h.load()
	s := h.summary(m.ID)
	if s.Status != model.StatusDown {
		t.Fatalf("status = %v, want down", s.Status)
	}
	if len(s.Beats) != 100 || s.Beats[99] != 'd' || len(s.Lat) != 100 {
		t.Fatalf("beats = %q (len %d), lat len %d", s.Beats, len(s.Beats), len(s.Lat))
	}
	if s.OutageID == nil || *s.OutageID != 7 || s.Since != downAt {
		t.Fatalf("outage=%v since=%d, want 7 since %d", s.OutageID, s.Since, downAt)
	}
	if s.LastCheckAt != downAt {
		t.Fatalf("lastCheckAt = %d, want %d", s.LastCheckAt, downAt)
	}
	if !h.sched.upserted(m.ID) {
		t.Fatal("monitor not scheduled")
	}
	if h.sched.upserted(paused.ID) {
		t.Fatal("paused monitor was scheduled")
	}
	if ps := h.summary(paused.ID); ps.Status != model.StatusPaused || !ps.Paused {
		t.Fatalf("paused summary = %v", ps.Status)
	}
	if n := h.count(`SELECT count(*) FROM status_events`); n != 0 {
		t.Fatalf("boot recorded %d status events", n)
	}

	// Recovery closes the restored outage and emits Recovered; a new down
	// gets the next outage id.
	h.apply(m.ID, ok(10))
	h.clk.Advance(time.Minute)
	for i := 0; i < 3; i++ {
		h.apply(m.ID, fail("HTTP 500"))
		h.clk.Advance(20 * time.Second)
	}
	evs := h.sink.take()
	if got := kinds(evs); !reflect.DeepEqual(got, []model.EventKind{model.EvRecovered, model.EvDown}) {
		t.Fatalf("events = %v", got)
	}
	if evs[0].OutageID != 7 || evs[1].OutageID != 8 {
		t.Fatalf("outage ids = %d, %d; want 7, 8", evs[0].OutageID, evs[1].OutageID)
	}
}

func TestClusters(t *testing.T) {
	h := newHarness(t)
	var ids []int64
	for _, target := range []string{"https://api.x.co/a", "https://api.x.co/b", "https://api.x.co:8443/c", "https://b.co/"} {
		m := h.monitor("m"+target[len(target)-2:], func(m *model.Monitor) { m.Target = target; m.Retries = 0 })
		ids = append(ids, m.ID)
	}
	up := h.monitor("healthy", func(m *model.Monitor) { m.Target = "https://api.x.co/ok" })
	h.load()
	h.apply(up.ID, ok(5))
	for _, id := range ids {
		h.apply(id, model.Result{OK: false, LatencyMs: -1, Reason: "timeout after 10 s", ErrClass: model.ErrTimeout})
	}

	cs := h.e.Clusters()
	var hosts, classes []Cluster
	for _, c := range cs {
		switch c.Kind {
		case "host":
			hosts = append(hosts, c)
		case "errClass":
			classes = append(classes, c)
		}
	}
	if len(hosts) != 1 || hosts[0].Key != "api.x.co" || len(hosts[0].MonitorIDs) != 3 || hosts[0].ErrClass != model.ErrTimeout {
		t.Fatalf("host clusters = %+v", hosts)
	}
	if len(classes) != 1 || classes[0].Key != "timeout" || len(classes[0].MonitorIDs) != 4 {
		t.Fatalf("errClass clusters = %+v", classes)
	}
	if hosts[0].Since != h.clk.Now().UnixMilli() {
		t.Fatalf("cluster since = %d", hosts[0].Since)
	}

	// Recovering two of them dissolves the host cluster.
	h.clk.Advance(time.Minute)
	h.apply(ids[0], ok(5))
	h.apply(ids[1], ok(5))
	for _, c := range h.e.Clusters() {
		if c.Kind == "host" {
			t.Fatalf("host cluster should be gone: %+v", c)
		}
	}
}

func TestSummariesBeatsEncoding(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", nil)
	base := h.clk.Now().Add(-10 * time.Minute)
	seq := []struct {
		st    model.Status
		maint bool
		lat   int
	}{
		{model.StatusUp, false, 10}, {model.StatusUp, false, 11}, {model.StatusDown, false, -1},
		{model.StatusPending, false, -1}, {model.StatusDegraded, false, 900}, {model.StatusDown, true, -1},
		{model.StatusUnknown, false, -1},
	}
	for i, b := range seq {
		h.w.Heartbeat(store.Heartbeat{MonitorID: m.ID, TS: base.Add(time.Duration(i) * time.Minute).UnixMilli(), Status: b.st, LatencyMs: b.lat, Maint: b.maint})
	}
	h.w.Flush()
	h.load()

	s := h.summary(m.ID)
	if s.Beats != "uudpgmk" {
		t.Fatalf("beats = %q, want uudpgmk", s.Beats)
	}
	if !reflect.DeepEqual(s.Lat, []int{10, 11, -1, -1, 900, -1, -1}) {
		t.Fatalf("lat = %v", s.Lat)
	}

	all := h.e.Summaries()
	if len(all) != 1 || all[0].ID != m.ID || all[0].Beats != s.Beats {
		t.Fatalf("Summaries = %+v", all)
	}
	if all[0].TagIDs == nil {
		t.Fatal("tagIds must be [] not null")
	}
}

func TestSummariesSortedByGroupThenName(t *testing.T) {
	h := newHarness(t)
	g1 := &store.Group{Name: "Payments", Sort: 1}
	g0 := &store.Group{Name: "Auth", Sort: 0}
	for _, g := range []*store.Group{g1, g0} {
		if err := h.s.CreateGroup(h.ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	h.monitor("zeta", func(m *model.Monitor) { m.GroupID = &g0.ID })
	h.monitor("beta", func(m *model.Monitor) { m.GroupID = &g1.ID })
	h.monitor("alpha", func(m *model.Monitor) { m.GroupID = &g1.ID })
	h.monitor("loose", nil)
	h.load()

	var names []string
	for _, s := range h.e.Summaries() {
		names = append(names, s.Name)
	}
	if want := []string{"zeta", "alpha", "beta", "loose"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("order = %v, want %v", names, want)
	}
	if got := h.e.MonitorIDsInGroup(g1.ID); len(got) != 2 {
		t.Fatalf("MonitorIDsInGroup = %v", got)
	}
	if gs := h.e.Groups(); len(gs) != 2 || gs[0].Name != "Auth" {
		t.Fatalf("Groups = %+v", gs)
	}
}

func TestStatusEventsRecorded(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", func(m *model.Monitor) { m.Retries = 1 })
	h.w.Heartbeat(store.Heartbeat{MonitorID: m.ID, TS: h.clk.Now().Add(-time.Minute).UnixMilli(), Status: model.StatusUp, LatencyMs: 5})
	h.w.Flush()
	h.load()

	h.apply(m.ID, fail("HTTP 503"))
	h.clk.Advance(20 * time.Second)
	h.apply(m.ID, fail("HTTP 502"))
	h.clk.Advance(20 * time.Second)
	h.apply(m.ID, fail("HTTP 502")) // DOWN → DOWN: no change
	h.clk.Advance(20 * time.Second)
	h.apply(m.ID, ok(10))
	h.w.Flush()

	evs, err := h.s.ListStatusEvents(h.ctx, &m.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	type ft struct{ from, to model.Status }
	var got []ft
	for i := len(evs) - 1; i >= 0; i-- {
		got = append(got, ft{evs[i].From, evs[i].To})
	}
	want := []ft{{model.StatusUp, model.StatusPending}, {model.StatusPending, model.StatusDown}, {model.StatusDown, model.StatusUp}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("status events = %+v, want %+v", got, want)
	}
	if evs[2].Reason != "HTTP 503" || evs[1].Reason != "HTTP 502" || evs[1].ErrClass != string(model.ErrHTTPStatus) {
		t.Fatalf("reasons = %q / %q (%q)", evs[2].Reason, evs[1].Reason, evs[1].ErrClass)
	}
}

func TestPauseResumeAndMaintenanceStatusEvents(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", nil)
	h.load()
	h.apply(m.ID, ok(5)) // UNKNOWN → UP

	h.clk.Advance(time.Minute)
	h.e.SetPaused(m.ID, true)
	if s := h.summary(m.ID); s.Status != model.StatusPaused || !s.Paused {
		t.Fatalf("after pause: %v", s.Status)
	}
	h.sched.mu.Lock()
	nRemoves := len(h.sched.removes)
	h.sched.mu.Unlock()
	if nRemoves != 1 {
		t.Fatalf("pause must unschedule, removes=%d", nRemoves)
	}
	if got := h.apply(m.ID, fail("x")); got != 0 {
		t.Fatalf("Apply while paused = %v, want dropped", got)
	}

	h.clk.Advance(time.Minute)
	h.e.SetPaused(m.ID, false)
	if s := h.summary(m.ID); s.Status != model.StatusUnknown || s.Paused {
		t.Fatalf("after resume: %v", s.Status)
	}

	now := h.clk.Now()
	start, end := now.Add(-time.Minute).UnixMilli(), now.Add(time.Hour).UnixMilli()
	w := &store.Maintenance{Title: "w", Kind: "once", StartsAt: &start, EndsAt: &end, TZ: "UTC", Active: true, MonitorIDs: []int64{m.ID}}
	if err := h.s.CreateMaintenance(h.ctx, w); err != nil {
		t.Fatal(err)
	}
	if err := h.e.ReloadMaintenance(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.clk.Advance(2 * time.Hour)
	h.e.tickMaintenance()
	h.w.Flush()

	evs, err := h.s.ListStatusEvents(h.ctx, &m.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for i := len(evs) - 1; i >= 0; i-- {
		got = append(got, evs[i].From.String()+">"+evs[i].To.String())
	}
	want := []string{"unknown>up", "up>paused", "paused>unknown", "unknown>maintenance", "maintenance>unknown"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("status events = %v, want %v", got, want)
	}
}

func TestUpsertMonitorAddsAndUpdates(t *testing.T) {
	h := newHarness(t)
	h.load()

	m := model.NewMonitor(model.TypeHTTP)
	m.Name, m.Target = "new", "https://new.example.com"
	if err := h.s.CreateMonitor(h.ctx, m); err != nil {
		t.Fatal(err)
	}
	h.e.UpsertMonitor(m)
	if s := h.summary(m.ID); s.Status != model.StatusUnknown || s.Name != "new" {
		t.Fatalf("new monitor summary = %+v", s)
	}
	if !h.sched.upserted(m.ID) {
		t.Fatal("new monitor not scheduled")
	}
	d := h.e.TakeDirty()
	if len(d.Changed) != 1 || d.Changed[0] != m.ID {
		t.Fatalf("dirty after add = %+v", d)
	}

	m2 := m.Clone()
	m2.Name = "renamed"
	m2.IntervalS = 30
	m2.TimeoutMs = 5000
	h.e.UpsertMonitor(m2)
	if s := h.summary(m.ID); s.Name != "renamed" || s.IntervalS != 30 {
		t.Fatalf("updated summary = %+v", s)
	}
	got, ok := h.e.Monitor(m.ID)
	if !ok || got.Name != "renamed" {
		t.Fatalf("Monitor() = %+v", got)
	}
	got.Name = "mutated"
	if again, _ := h.e.Monitor(m.ID); again.Name != "renamed" {
		t.Fatal("Monitor() must return a clone")
	}
	info, ok := h.e.AlertInfo(m.ID)
	if !ok || info.Name != "renamed" || info.Target != "https://new.example.com" {
		t.Fatalf("AlertInfo = %+v", info)
	}
}

func TestRunCheckRecoversPanic(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", func(m *model.Monitor) { m.Retries = 0 })
	h.load()
	h.e.checker = func(context.Context, *model.Monitor) model.Result { panic("boom") }

	if d := h.e.RunCheck(h.ctx, m.ID); d != 60*time.Second {
		t.Fatalf("RunCheck after panic = %v", d)
	}
	if s := h.summary(m.ID); s.Status != model.StatusDown || s.Reason != "internal error: check panicked" {
		t.Fatalf("summary = %v %q", s.Status, s.Reason)
	}
}

func TestRunCheckUsesChecker(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", func(m *model.Monitor) { m.Retries = 0 })
	h.load()
	h.script(m.ID, fail("HTTP 500"))
	h.e.RunCheck(h.ctx, m.ID)
	if s := h.summary(m.ID); s.Status != model.StatusDown || s.Reason != "HTTP 500" {
		t.Fatalf("summary = %v %q", s.Status, s.Reason)
	}
}

func TestDomainExpiryThresholds(t *testing.T) {
	h := newHarness(t)
	m := h.monitor("api", func(m *model.Monitor) { m.Settings.CheckDomainExpiry = true })
	h.load()

	exp := h.clk.Now().Add(20*24*time.Hour + time.Hour)
	h.e.applyDomainResult(m.ID, exp, nil)
	evs := h.sink.take()
	if len(evs) != 1 || evs[0].Kind != model.EvDomainExpiring || evs[0].Days != 20 {
		t.Fatalf("domain 20 d: %+v", evs)
	}
	if s := h.summary(m.ID); s.DomainDays == nil || *s.DomainDays != 20 {
		t.Fatalf("domainDays = %v", s.DomainDays)
	}
	h.e.applyDomainResult(m.ID, exp, nil)
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("repeat alerted: %+v", evs)
	}
	h.clk.Advance(8 * 24 * time.Hour) // 12 d left → threshold 14
	h.e.applyDomainResult(m.ID, exp, nil)
	if evs := h.sink.take(); len(evs) != 1 || evs[0].Days != 12 {
		t.Fatalf("threshold 14: %+v", evs)
	}
	// Renewal resets; no RDAP clears the date and never alerts.
	h.e.applyDomainResult(m.ID, exp.Add(365*24*time.Hour), nil)
	if evs := h.sink.take(); len(evs) != 0 {
		t.Fatalf("renewed domain alerted: %+v", evs)
	}
	h.e.applyDomainResult(m.ID, time.Time{}, check.ErrNoRDAP)
	if s := h.summary(m.ID); s.DomainDays != nil {
		t.Fatalf("no-rdap domainDays = %v, want null", *s.DomainDays)
	}
	h.w.Flush()
	rt, _ := h.s.AllRuntime(h.ctx)
	if r := rt[m.ID]; r.DomainCheckedAt == nil || r.DomainExpiresAt != nil {
		t.Fatalf("runtime domain = %+v", r)
	}
}

func TestNetGuard(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := dead.Addr().String()
	dead.Close()

	clk := &fakeClock{t: time.Unix(1000, 0)}
	ctx := context.Background()

	g := NewNetGuard([]string{deadAddr, ln.Addr().String()}, 500*time.Millisecond, 30*time.Second, clk.Now)
	if !g.Online(ctx) {
		t.Fatal("one reachable target must mean online")
	}

	off := NewNetGuard([]string{deadAddr, deadAddr}, 500*time.Millisecond, 30*time.Second, clk.Now)
	if off.Online(ctx) {
		t.Fatal("all targets failing must mean offline")
	}
	// Cached for 30 s: even a cancelled caller context gets the cached answer.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if off.Online(cctx) {
		t.Fatal("cached offline answer expected")
	}

	// Concurrent callers share one probe (race-detector coverage).
	many := NewNetGuard([]string{ln.Addr().String()}, 500*time.Millisecond, 30*time.Second, clk.Now)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !many.Online(ctx) {
				t.Error("concurrent Online = false")
			}
		}()
	}
	wg.Wait()
}

// TestConcurrentApplyAndReads is race-detector coverage for the locking
// rules: checks, pushes, maintenance ticks and hub reads at once.
func TestConcurrentApplyAndReads(t *testing.T) {
	h := newHarness(t)
	var ids []int64
	for i := 0; i < 8; i++ {
		m := h.monitor("m"+string(rune('a'+i)), func(m *model.Monitor) { m.Retries = 1; m.ReminderMin = 1 })
		ids = append(ids, m.ID)
	}
	h.load()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for _, id := range ids {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				r := ok(5)
				if i%3 == 0 {
					r = fail("HTTP 500")
				}
				h.apply(id, r)
			}
		}(id)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = h.e.Summaries()
			_ = h.e.Clusters()
			_ = h.e.ActiveOutages()
			_ = h.e.TakeDirty()
			h.e.tickMaintenance()
			h.e.tickReminders()
			h.clk.Advance(time.Second)
		}
	}()
	time.Sleep(50 * time.Millisecond)
	h.e.RemoveMonitor(ids[0])
	h.e.SetPaused(ids[1], true)
	h.e.SetPaused(ids[1], false)
	close(stop)
	wg.Wait()
}

// benchEngine builds an engine with n monitors, each with a full ring, and
// no store or writer (pure in-memory cost).
func benchEngine(b *testing.B, n int) (*Engine, *fakeClock) {
	b.Helper()
	clk := &fakeClock{t: time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)}
	e := New(Deps{Now: clk.Now, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	for i := 1; i <= n; i++ {
		m := model.NewMonitor(model.TypeHTTP)
		m.ID, m.Name, m.Target = int64(i), "monitor-"+strings.Repeat("x", i%7), "https://svc.example.com/health"
		e.UpsertMonitor(m)
	}
	for k := 0; k < ringSize; k++ {
		at := clk.Now().Add(time.Duration(k) * time.Second)
		for i := 1; i <= n; i++ {
			e.Apply(int64(i), model.Result{At: at, OK: true, LatencyMs: 20 + k})
		}
	}
	return e, clk
}

func BenchmarkSummariesJSON2000(b *testing.B) {
	e, _ := benchEngine(b, 2000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := json.Marshal(e.Summaries()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSummariesColdCache2000(b *testing.B) {
	e, clk := benchEngine(b, 2000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clk.Advance(time.Hour) // invalidates every cached summary
		_ = e.Summaries()
	}
}

func BenchmarkApply(b *testing.B) {
	e, clk := benchEngine(b, 2000)
	at := clk.Now().Add(time.Hour)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.Apply(int64(i%2000)+1, model.Result{At: at.Add(time.Duration(i) * time.Millisecond), OK: true, LatencyMs: 20})
	}
}
