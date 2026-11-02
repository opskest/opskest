package scheduler

import (
	"context"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitTimeout bounds every wait on the scheduler's goroutines. It is a safety
// net that turns a hang into a failure, not a timing assumption: a passing run
// never gets near it.
const waitTimeout = 2 * time.Second

var t0 = time.Date(2026, time.September, 24, 10, 0, 0, 0, time.UTC)

type rec struct {
	id int64
	at time.Time
}

// harness drives a Scheduler on a FakeClock with a RunFunc that records
// (id, clk.Now()) on a channel and can block until released.
type harness struct {
	t   *testing.T
	clk *FakeClock
	s   *Scheduler

	runs      chan rec
	block     bool
	release   chan struct{}
	relOnce   sync.Once
	nextD     atomic.Int64 // what the RunFunc returns
	returned  atomic.Int64 // runs that have returned
	panicNext atomic.Bool  // the next run to return panics instead
	cancelled chan int64   // ids of blocked runs that saw their ctx cancelled

	mu        sync.Mutex
	active    map[int64]int
	peak      map[int64]int
	activeAll int
	peakAll   int
}

func newHarness(t *testing.T, concurrency int, block bool) *harness {
	t.Helper()
	h := &harness{
		t:         t,
		clk:       NewFakeClock(t0),
		runs:      make(chan rec, 10000),
		block:     block,
		release:   make(chan struct{}),
		cancelled: make(chan int64, 100),
		active:    map[int64]int{},
		peak:      map[int64]int{},
	}
	h.s = New(h.clk, concurrency, h.run, rand.New(rand.NewPCG(1, 2)))
	t.Cleanup(func() {
		h.unblock()
		h.s.Stop()
	})
	return h
}

func (h *harness) run(ctx context.Context, id int64) time.Duration {
	h.mu.Lock()
	h.active[id]++
	h.activeAll++
	h.peak[id] = max(h.peak[id], h.active[id])
	h.peakAll = max(h.peakAll, h.activeAll)
	h.mu.Unlock()

	h.runs <- rec{id: id, at: h.clk.Now()}
	if h.block {
		select {
		case <-h.release:
		case <-ctx.Done():
			h.cancelled <- id
		}
	}

	h.mu.Lock()
	h.active[id]--
	h.activeAll--
	h.mu.Unlock()
	h.returned.Add(1)
	if h.panicNext.CompareAndSwap(true, false) {
		panic("check blew up")
	}
	return time.Duration(h.nextD.Load())
}

// unblock releases every blocked run, now and in future.
func (h *harness) unblock() { h.relOnce.Do(func() { close(h.release) }) }

// start starts the loop and waits until it has taken in the monitors that were
// registered before Start and armed its first timer.
func (h *harness) start() {
	h.t.Helper()
	h.startWith(context.Background())
}

func (h *harness) startWith(ctx context.Context) {
	h.t.Helper()
	h.s.Start(ctx)
	armed := make(chan struct{})
	go func() {
		h.clk.BlockUntilWaiters(1)
		close(armed)
	}()
	select {
	case <-armed:
	case <-time.After(waitTimeout):
		h.t.Fatal("scheduler loop never armed a timer")
	}
}

// settle waits until the loop has applied every queued command, handled every
// finished run and parked with exactly inFlight runs still executing.
func (h *harness) settle(inFlight int) {
	h.t.Helper()
	h.inspect(inFlight, nil)
}

// inspect is settle, then fn runs inside the loop goroutine (so it may read the
// loop-owned heap and maps).
func (h *harness) inspect(inFlight int, fn func()) {
	h.t.Helper()
	select {
	case <-h.s.testSync(inFlight, fn):
	case <-time.After(waitTimeout):
		h.t.Fatalf("scheduler did not settle with %d run(s) in flight; stats %+v", inFlight, h.s.Stats())
	}
}

type view struct {
	ok            bool
	next          time.Time
	interval      time.Duration
	running       bool
	heapLen, byID int
}

func (h *harness) view(inFlight int, id int64) view {
	h.t.Helper()
	var v view
	h.inspect(inFlight, func() {
		v.heapLen, v.byID = h.s.h.Len(), len(h.s.byID)
		if e, ok := h.s.byID[id]; ok {
			v.ok, v.next, v.interval, v.running = true, e.next, e.interval, h.s.running[id]
		}
	})
	return v
}

func (h *harness) advanceTo(at time.Time) { h.clk.Advance(at.Sub(h.clk.Now())) }

// step advances the clock n times by d and settles after each step.
func (h *harness) step(n int, d time.Duration, inFlight int) {
	h.t.Helper()
	for range n {
		h.clk.Advance(d)
		h.settle(inFlight)
	}
}

// waitRun waits for the next recorded run (used for runs that block, whose
// record may land after the loop has settled).
func (h *harness) waitRun() rec {
	h.t.Helper()
	select {
	case r := <-h.runs:
		return r
	case <-time.After(waitTimeout):
		h.t.Fatal("expected a run, none started")
		return rec{}
	}
}

// drain returns the runs recorded so far. After settle(0) every finished run
// has recorded, because a run records before it returns.
func (h *harness) drain() []rec {
	var out []rec
	for {
		select {
		case r := <-h.runs:
			out = append(out, r)
		default:
			return out
		}
	}
}

func (h *harness) peakOf(id int64) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.peak[id]
}

func assertTimes(t *testing.T, got []rec, want []time.Time) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d runs %v, want %d at %v", len(got), got, len(want), want)
	}
	for i := range want {
		if !got[i].at.Equal(want[i]) {
			t.Errorf("run %d at %v, want %v", i, got[i].at, want[i])
		}
	}
}

func TestJitteredFirstRun(t *testing.T) {
	h := newHarness(t, 256, false)
	const n = 100
	for id := int64(1); id <= n; id++ {
		h.s.Upsert(id, time.Minute)
	}
	h.start()
	h.settle(0)
	h.step(60, time.Second, 0)

	count := map[int64]int{}
	secs := map[int64]bool{}
	for _, r := range h.drain() {
		count[r.id]++
		secs[r.at.Unix()] = true
	}
	for id := int64(1); id <= n; id++ {
		if count[id] != 1 {
			t.Errorf("monitor %d ran %d times in its first interval, want 1", id, count[id])
		}
	}
	// 100 uniform draws over 60 one-second bins fill ~48.8 on average; every
	// seed tried reaches 40 (Ruling R8). Without jitter this would be 1.
	if len(secs) < 40 {
		t.Errorf("first runs fell in %d distinct seconds, want at least 40", len(secs))
	}
}

func TestRunsAtInterval(t *testing.T) {
	h := newHarness(t, 4, false)
	h.s.Upsert(1, 10*time.Second)
	h.start()
	first := h.view(0, 1).next
	h.advanceTo(first)
	h.settle(0)
	assertTimes(t, h.drain(), []time.Time{first})

	h.step(35, time.Second, 0)
	assertTimes(t, h.drain(), []time.Time{
		first.Add(10 * time.Second), first.Add(20 * time.Second), first.Add(30 * time.Second),
	})
}

func TestNextFromRunFunc(t *testing.T) {
	h := newHarness(t, 4, false)
	h.nextD.Store(int64(3 * time.Second))
	h.s.Upsert(1, 10*time.Second)
	h.start()
	first := h.view(0, 1).next
	h.advanceTo(first)
	h.settle(0)
	assertTimes(t, h.drain(), []time.Time{first})

	h.step(9, time.Second, 0)
	assertTimes(t, h.drain(), []time.Time{
		first.Add(3 * time.Second), first.Add(6 * time.Second), first.Add(9 * time.Second),
	})
}

func TestSkipIfOverrun(t *testing.T) {
	h := newHarness(t, 4, true)
	h.s.Upsert(1, 10*time.Second)
	h.start()
	first := h.view(0, 1).next
	h.advanceTo(first)
	h.waitRun()
	h.settle(1)

	h.step(21, time.Second, 1) // past 2 intervals with the first run still blocked
	st := h.s.Stats()
	if st.InFlight != 1 {
		t.Errorf("InFlight = %d, want 1", st.InFlight)
	}
	if st.Skipped < 1 {
		t.Errorf("Skipped = %d, want >= 1", st.Skipped)
	}
	if got := h.drain(); len(got) != 0 {
		t.Fatalf("a second run started while the first was in flight: %v", got)
	}

	// Once released, the overrun check is followed by one fresh run: its planned
	// slot is in the past, so it lands in [now, now+interval), never a pile-up.
	h.unblock()
	now := h.clk.Now()
	if next := h.view(0, 1).next; next.Before(now) || !next.Before(now.Add(10*time.Second)) {
		t.Fatalf("next run at now%+v, want in [now, now+10s)", next.Sub(now))
	}
	h.step(10, time.Second, 0)
	if got := h.drain(); len(got) != 1 {
		t.Fatalf("got %d runs in the interval after release, want 1", len(got))
	}
	if p := h.peakOf(1); p != 1 {
		t.Errorf("peak concurrent runs for monitor 1 = %d, want 1", p)
	}
}

func TestConcurrencyCap(t *testing.T) {
	h := newHarness(t, 4, true)
	for id := int64(1); id <= 20; id++ {
		h.s.Upsert(id, 10*time.Second)
	}
	h.start()
	h.clk.Advance(10 * time.Second) // all 20 are due at once
	h.settle(4)
	if st := h.s.Stats(); st.InFlight != 4 {
		t.Fatalf("InFlight = %d, want 4", st.InFlight)
	}
	for range 4 {
		h.waitRun()
	}

	// A full pool must not block the loop: commands are still applied and the
	// 50 ms dispatch retry comes and goes without starting a fifth run.
	h.s.Upsert(21, time.Hour)
	h.settle(4)
	h.clk.Advance(50 * time.Millisecond)
	h.settle(4)
	if st := h.s.Stats(); st.InFlight != 4 || st.Monitors != 21 {
		t.Fatalf("stats %+v, want InFlight 4 and Monitors 21", st)
	}
	if got := h.drain(); len(got) != 0 {
		t.Fatalf("runs started beyond the cap: %v", got)
	}

	// Freed slots go to the waiting monitors straight away.
	h.unblock()
	h.settle(0)
	ran := map[int64]bool{}
	for _, r := range h.drain() {
		ran[r.id] = true
	}
	if len(ran) != 16 {
		t.Errorf("after release %d more monitors ran, want the 16 that were waiting", len(ran))
	}
	h.mu.Lock()
	peak := h.peakAll
	h.mu.Unlock()
	if peak > 4 {
		t.Errorf("peak concurrent runs = %d, want <= 4", peak)
	}
}

func TestNoReplayAfterStall(t *testing.T) {
	h := newHarness(t, 4, false)
	const interval = 10 * time.Second
	h.s.Upsert(1, interval)
	h.start()
	first := h.view(0, 1).next
	h.advanceTo(first)
	h.settle(0)
	assertTimes(t, h.drain(), []time.Time{first})

	h.clk.Advance(10 * time.Minute) // one jump: 60 intervals missed
	h.settle(0)
	if got := h.drain(); len(got) != 1 {
		t.Fatalf("got %d runs after a 10 min stall, want exactly 1", len(got))
	}
	now := h.clk.Now()
	next := h.view(0, 1).next
	if next.Before(now) || !next.Before(now.Add(interval)) {
		t.Fatalf("next run at now%+v, want in [now, now+%v)", next.Sub(now), interval)
	}
	if st := h.s.Stats(); st.Skipped != 0 {
		t.Errorf("Skipped = %d after a stall with no overrun, want 0", st.Skipped)
	}

	// The following interval holds one run, not a burst of catch-ups.
	h.step(10, time.Second, 0)
	if got := h.drain(); len(got) != 1 {
		t.Fatalf("got %d runs in the interval after the stall, want 1", len(got))
	}
}

// Ruling R14: after a stall the fleet re-spreads over the interval instead of
// all landing in the same second.
func TestStallRespreadsFleet(t *testing.T) {
	h := newHarness(t, 256, false)
	const n, interval = 100, time.Minute
	for id := int64(1); id <= n; id++ {
		h.s.Upsert(id, interval)
	}
	h.start()
	h.step(60, time.Second, 0)
	if got := len(h.drain()); got != n {
		t.Fatalf("got %d first runs, want %d", got, n)
	}

	h.clk.Advance(10 * time.Minute) // every monitor misses ~10 runs
	h.settle(0)
	if got := len(h.drain()); got != n {
		t.Fatalf("got %d runs after the stall, want one each (%d)", got, n)
	}
	now := h.clk.Now()
	var lo, hi time.Time
	h.inspect(0, func() {
		lo, hi = h.s.h[0].next, h.s.h[0].next
		for _, e := range h.s.byID {
			lo, hi = minTime(lo, e.next), maxTime(hi, e.next)
		}
	})
	if lo.Before(now) || !hi.Before(now.Add(interval)) {
		t.Fatalf("next runs span now%+v..now%+v, want within [now, now+%v)", lo.Sub(now), hi.Sub(now), interval)
	}
	if span := hi.Sub(lo); span < interval/2 {
		t.Fatalf("next runs bunched within %v after the stall, want spread over >= %v", span, interval/2)
	}
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// Review Focus 1 / Ruling R15: the engine's RunFunc answers with the interval
// the check started under, so an Upsert that shortens it mid-run must still win.
func TestUpsertWhileRunning(t *testing.T) {
	h := newHarness(t, 4, true)
	h.nextD.Store(int64(30 * time.Second)) // the old interval, as the engine would return
	h.s.Upsert(1, 30*time.Second)
	h.start()
	first := h.view(0, 1).next
	h.advanceTo(first)
	h.waitRun()
	h.settle(1)

	h.s.Upsert(1, 10*time.Second)
	if v := h.view(1, 1); v.heapLen != 1 || v.byID != 1 || v.interval != 10*time.Second || !v.running {
		t.Fatalf("while running after Upsert: %+v, want one running entry at 10s", v)
	}

	h.unblock()
	v := h.view(0, 1)
	if v.heapLen != 1 || v.byID != 1 {
		t.Fatalf("heap has %d entries (map %d), want exactly 1", v.heapLen, v.byID)
	}
	if want := first.Add(10 * time.Second); !v.next.Equal(want) {
		t.Fatalf("next run at first+%v, want first+10s (the new interval)", v.next.Sub(first))
	}
	h.advanceTo(first.Add(10 * time.Second))
	h.settle(0)
	assertTimes(t, h.drain(), []time.Time{first.Add(10 * time.Second)})
}

// Ruling R15: shortening an idle monitor's interval pulls its next run in.
func TestUpsertShortensIdleInterval(t *testing.T) {
	h := newHarness(t, 4, false)
	h.s.Upsert(1, 24*time.Hour)
	h.start()
	if first := h.view(0, 1).next; !first.After(t0.Add(time.Minute)) {
		t.Fatalf("first run jittered to t0+%v, the test needs it after t0+60s", first.Sub(t0))
	}

	h.s.Upsert(1, time.Minute)
	v := h.view(0, 1)
	if v.interval != time.Minute || v.next.Before(t0) || v.next.After(t0.Add(time.Minute)) {
		t.Fatalf("after 24h -> 60s: %+v, want interval 60s and next in [now, now+60s]", v)
	}
	h.advanceTo(v.next)
	h.settle(0)
	assertTimes(t, h.drain(), []time.Time{v.next})
}

func TestRemoveWhileRunning(t *testing.T) {
	h := newHarness(t, 4, true)
	h.s.Upsert(1, 10*time.Second)
	h.start()
	h.advanceTo(h.view(0, 1).next)
	h.waitRun()
	h.settle(1)

	h.s.Remove(1)
	if v := h.view(1, 1); v.ok || v.heapLen != 0 {
		t.Fatalf("after Remove: %+v, want no entry", v)
	}
	h.unblock()
	h.settle(0)
	h.step(6, 10*time.Second, 0)
	if got := h.drain(); len(got) != 0 {
		t.Fatalf("removed monitor ran again: %v", got)
	}
	if st := h.s.Stats(); st.Monitors != 0 || st.InFlight != 0 {
		t.Errorf("stats %+v, want Monitors 0 and InFlight 0", st)
	}
}

// Review Focus 1: a monitor deleted and re-created while its old check is still
// running must not get a second, concurrent run.
func TestRemoveThenReAddWhileRunning(t *testing.T) {
	h := newHarness(t, 4, true)
	h.s.Upsert(1, 10*time.Second)
	h.start()
	h.s.Reschedule(1, 0)
	h.waitRun()
	h.settle(1)

	h.s.Remove(1)
	h.s.Upsert(1, 10*time.Second) // a new entry with its own jittered first run
	v := h.view(1, 1)
	if v.heapLen != 1 || v.byID != 1 {
		t.Fatalf("heap has %d entries (map %d), want 1", v.heapLen, v.byID)
	}
	h.advanceTo(v.next) // the new entry comes due while the old run is in flight
	h.settle(1)
	if st := h.s.Stats(); st.InFlight != 1 || st.Skipped < 1 {
		t.Fatalf("stats %+v, want InFlight 1 and Skipped >= 1", st)
	}
	if got := h.drain(); len(got) != 0 {
		t.Fatalf("second run started while the old one was in flight: %v", got)
	}
	slot := h.view(1, 1).next
	if want := v.next.Add(10 * time.Second); !slot.Equal(want) {
		t.Fatalf("skipped slot moved next to first+%v, want first+10s", slot.Sub(v.next))
	}

	// The old run's completion belongs to the removed entry: it frees the id
	// but must not replan the new entry.
	h.unblock()
	if got := h.view(0, 1).next; !got.Equal(slot) {
		t.Fatalf("stale completion replanned the new entry to slot%+v", got.Sub(slot))
	}
	h.advanceTo(slot)
	h.settle(0)
	assertTimes(t, h.drain(), []time.Time{slot})
	if p := h.peakOf(1); p != 1 {
		t.Errorf("peak concurrent runs for monitor 1 = %d, want 1", p)
	}
}

func TestReschedule(t *testing.T) {
	h := newHarness(t, 4, false)
	h.s.Upsert(1, time.Minute)
	h.start()
	if first := h.view(0, 1).next; !first.After(t0) {
		t.Fatalf("first run jittered to %v, the test needs it after t0", first)
	}
	h.settle(0)
	if got := h.drain(); len(got) != 0 {
		t.Fatalf("ran before its first run: %v", got)
	}

	h.s.Reschedule(1, 0) // run now: no clock movement needed
	h.settle(0)
	assertTimes(t, h.drain(), []time.Time{t0})

	h.s.Reschedule(1, 5*time.Second)
	h.settle(0) // applied at t0, before the clock moves
	h.step(4, time.Second, 0)
	if got := h.drain(); len(got) != 0 {
		t.Fatalf("ran before the rescheduled time: %v", got)
	}
	h.step(1, time.Second, 0)
	assertTimes(t, h.drain(), []time.Time{t0.Add(5 * time.Second)})
}

// A push arriving during the monitor's deadline check reschedules it; the
// check's completion must not overwrite that deadline.
func TestRescheduleWhileRunning(t *testing.T) {
	h := newHarness(t, 4, true)
	h.s.Upsert(1, 10*time.Second)
	h.start()
	h.s.Reschedule(1, 0)
	h.waitRun()
	h.settle(1)

	h.s.Reschedule(1, 25*time.Second)
	h.settle(1)
	h.unblock()
	if got, want := h.view(0, 1).next, t0.Add(25*time.Second); !got.Equal(want) {
		t.Fatalf("next run at t0+%v, want t0+25s", got.Sub(t0))
	}
}

// "Run now" while a check is in flight is not an overrun skip: the monitor
// runs again as soon as the current run finishes.
func TestRunNowWhileRunningReruns(t *testing.T) {
	h := newHarness(t, 4, true)
	h.s.Upsert(1, 10*time.Second)
	h.start()
	h.s.Reschedule(1, 0)
	h.waitRun()
	h.settle(1)

	h.s.Reschedule(1, 0) // run-now during the run
	h.settle(1)
	skippedBefore := h.s.Stats().Skipped
	h.unblock()
	h.waitRun() // the rerun starts right away, with no clock movement
	if h.s.Stats().Skipped != skippedBefore {
		t.Fatalf("run-now counted as a skip")
	}
}

// TestCancelStartCtx shuts down with cancel-then-Stop: the cancel must stop the loop and
// reach the runs in flight, so Stop does not wait out a check timeout.
func TestCancelStartCtx(t *testing.T) {
	h := newHarness(t, 4, true)
	h.s.Upsert(1, 10*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.startWith(ctx)
	h.s.Reschedule(1, 0)
	h.waitRun()
	h.settle(1)

	cancel()
	select {
	case <-h.s.exited:
	case <-time.After(waitTimeout):
		t.Fatal("cancelling the Start ctx did not stop the loop")
	}
	select {
	case id := <-h.cancelled:
		if id != 1 {
			t.Fatalf("cancelled run for id %d, want 1", id)
		}
	case <-time.After(waitTimeout):
		t.Fatal("the in-flight run's ctx was not cancelled")
	}
	stopped := make(chan struct{})
	go func() {
		h.s.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(waitTimeout):
		t.Fatal("Stop did not return after the ctx was cancelled")
	}
	if st := h.s.Stats(); st.InFlight != 0 {
		t.Errorf("InFlight = %d after cancel and Stop, want 0", st.InFlight)
	}
}

func TestStopWaitsForInflight(t *testing.T) {
	h := newHarness(t, 4, true)
	h.s.Upsert(1, 10*time.Second)
	h.start()
	h.s.Reschedule(1, 0)
	h.waitRun()
	h.settle(1)

	stopped := make(chan bool, 1)
	go func() {
		h.s.Stop()
		stopped <- h.returned.Load() == 1
	}()
	select {
	case <-h.s.exited:
	case <-time.After(waitTimeout):
		t.Fatal("Stop did not stop the loop")
	}
	// The loop is gone; only the in-flight run can still hold Stop. This window
	// cannot fail a correct Stop, which is blocked for as long as the run is.
	select {
	case <-stopped:
		t.Fatal("Stop returned while a run was still in flight")
	case <-time.After(50 * time.Millisecond):
	}
	h.unblock()
	select {
	case ok := <-stopped:
		if !ok {
			t.Fatal("Stop returned before the in-flight run finished")
		}
	case <-time.After(waitTimeout):
		t.Fatal("Stop did not return after the run was released")
	}
}

// Ruling R9: a panicking check is recovered and counted; the monitor keeps its
// registered cadence and the scheduler keeps running.
func TestRunFuncPanicRecovered(t *testing.T) {
	h := newHarness(t, 4, false)
	h.nextD.Store(int64(3 * time.Second)) // what a normal run would answer
	h.panicNext.Store(true)
	h.s.Upsert(1, 10*time.Second)
	h.start()
	first := h.view(0, 1).next
	h.advanceTo(first)
	h.settle(0)
	assertTimes(t, h.drain(), []time.Time{first})
	if st := h.s.Stats(); st.Panics != 1 || st.InFlight != 0 {
		t.Fatalf("stats %+v, want Panics 1 and InFlight 0", st)
	}
	if next := h.view(0, 1).next; !next.Equal(first.Add(10 * time.Second)) {
		t.Fatalf("next run at first+%v, want the registered interval (first+10s)", next.Sub(first))
	}

	h.step(10, time.Second, 0)
	assertTimes(t, h.drain(), []time.Time{first.Add(10 * time.Second)})
	if st := h.s.Stats(); st.Panics != 1 {
		t.Errorf("Panics = %d, want 1", st.Panics)
	}
}

func TestLagStats(t *testing.T) {
	h := newHarness(t, 4, false)
	h.s.Upsert(1, 10*time.Second)
	h.start()
	first := h.view(0, 1).next
	for i := range 5 {
		h.advanceTo(first.Add(time.Duration(i) * 10 * time.Second))
		h.settle(0)
	}
	if got := h.drain(); len(got) != 5 {
		t.Fatalf("got %d runs, want 5", len(got))
	}
	if st := h.s.Stats(); st.LagP50 != 0 || st.LagP99 != 0 {
		t.Fatalf("lag p50 %v p99 %v, want 0 for runs started on time", st.LagP50, st.LagP99)
	}

	// A start 2 s late shows up as lag: p99 is the late run, p50 is unchanged.
	h.advanceTo(first.Add(50*time.Second + 2*time.Second))
	h.settle(0)
	if st := h.s.Stats(); st.LagP99 != 2*time.Second || st.LagP50 != 0 {
		t.Fatalf("lag p50 %v p99 %v, want 0 and 2s", st.LagP50, st.LagP99)
	}
}
