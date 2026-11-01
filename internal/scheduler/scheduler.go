// Package scheduler runs monitor checks on time. One goroutine owns a min-heap
// of next-run times and hands due checks to a bounded pool of workers. First
// runs are jittered across the interval, a check that overruns its interval
// causes the next one to be skipped (checks never queue behind themselves), and
// runs missed during a stall are never replayed: a monitor whose next run is
// already past is re-spread over its interval instead.
package scheduler

import (
	"container/heap"
	"context"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// RunFunc executes one check for id and returns the delay until the next run
// (the engine decides: interval or retry interval). Returning <= 0 means "use
// the registered interval".
type RunFunc func(ctx context.Context, id int64) (next time.Duration)

// Stats is a point-in-time view for /metrics and the About page.
type Stats struct {
	Monitors, InFlight int
	// Skipped counts the times a run came due while the previous run of the
	// same monitor was still in flight.
	Skipped uint64
	// LagP50 and LagP99 are percentiles of actual start - planned start over
	// the last 10,000 runs.
	LagP50, LagP99 time.Duration
	// Panics counts RunFunc panics. Each is recovered and the monitor keeps its
	// registered interval.
	Panics uint64
}

const (
	saturatedRetry = 50 * time.Millisecond // re-check a full pool this often (a finishing run also wakes the loop)
	lagWindow      = 10000                 // lag samples kept for the percentiles
)

type cmdKind uint8

const (
	cmdUpsert cmdKind = iota
	cmdReschedule
	cmdRemove
	cmdSync
)

type command struct {
	kind cmdKind
	id   int64
	d    time.Duration

	// cmdSync only.
	inFlight int
	fn       func()
	reply    chan struct{}
}

// doneMsg reports a finished run to the loop. e identifies the entry the run
// was started for; the worker never dereferences it.
type doneMsg struct {
	e    *entry
	id   int64
	next time.Duration
}

// Scheduler dispatches RunFunc calls for registered monitors. Upsert, Reschedule
// and Remove never block and may be called before Start (the engine registers
// every monitor at boot, then starts the loop).
type Scheduler struct {
	clk  Clock
	conc int
	run  RunFunc

	// The command queue. It is a mutex-guarded slice plus a wake signal rather
	// than a bounded channel so that callers never block: not before Start
	// (thousands of monitors), not from HTTP handlers, not from a RunFunc.
	mu       sync.Mutex
	queue    []command
	started  bool
	stopped  bool
	wake     chan struct{} // capacity 1
	quit     chan struct{}
	stopOnce sync.Once
	exited   chan struct{} // closed when the loop returns
	done     chan doneMsg  // capacity conc, so a worker never blocks on it
	wg       sync.WaitGroup

	// Owned by the loop goroutine.
	rnd     *rand.Rand
	h       entryHeap
	byID    map[int64]*entry
	running map[int64]bool // per id, so a monitor removed and re-added mid-run still counts as running
	busy    int            // semaphore: runs started whose completion the loop has not handled yet
	spare   []command
	syncs   []command

	monitors atomic.Int64
	inFlight atomic.Int64
	skipped  atomic.Uint64
	panics   atomic.Uint64
	lags     lagRing
}

// New returns a Scheduler that runs at most concurrency checks at once. A nil
// rnd is seeded from the time; a non-nil one must not be used by the caller
// afterwards. A nil clk means RealClock, and concurrency < 1 means 1.
func New(clk Clock, concurrency int, run RunFunc, rnd *rand.Rand) *Scheduler {
	if run == nil {
		panic("scheduler: nil RunFunc")
	}
	if clk == nil {
		clk = RealClock()
	}
	concurrency = max(concurrency, 1)
	if rnd == nil {
		seed := uint64(time.Now().UnixNano())
		rnd = rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	}
	return &Scheduler{
		clk:     clk,
		conc:    concurrency,
		run:     run,
		wake:    make(chan struct{}, 1),
		quit:    make(chan struct{}),
		exited:  make(chan struct{}),
		done:    make(chan doneMsg, concurrency),
		rnd:     rnd,
		byID:    map[int64]*entry{},
		running: map[int64]bool{},
	}
}

// Upsert registers a monitor or changes its interval. A new monitor first runs
// at now + rand[0, interval). An existing one whose interval changed runs next
// at min(current next, now + interval); if its check is in flight, that bound
// also caps the next run the completion plans. An interval <= 0 is ignored.
func (s *Scheduler) Upsert(id int64, interval time.Duration) {
	s.enqueue(command{kind: cmdUpsert, id: id, d: interval})
}

// Reschedule sets a monitor's next run to now + in: push deadlines, and "run
// now" with in = 0. If the monitor's check is in flight, the new time replaces
// the one the completion would have planned (and, if it comes due before the
// check finishes, it is skipped like any overrun). Unknown ids are ignored.
func (s *Scheduler) Reschedule(id int64, in time.Duration) {
	s.enqueue(command{kind: cmdReschedule, id: id, d: in})
}

// Remove unschedules a monitor. A check already in flight runs to completion,
// and its completion is then discarded.
func (s *Scheduler) Remove(id int64) {
	s.enqueue(command{kind: cmdRemove, id: id})
}

// Start starts the loop. Runs receive ctx, and cancelling it also stops the
// loop. Start after Start or Stop does nothing.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.stopped {
		return
	}
	s.started = true
	go s.loop(ctx)
}

// Stop stops dispatching and waits for in-flight runs to return. It does not
// cancel them; cancel the ctx given to Start for that. Later commands are
// dropped. Stop is idempotent.
func (s *Scheduler) Stop() {
	s.stopOnce.Do(func() { close(s.quit) })
	s.mu.Lock()
	s.stopped = true
	s.queue = nil
	started := s.started
	s.mu.Unlock()
	if started {
		<-s.exited
	}
	s.wg.Wait()
}

// Stats returns the current counters and lag percentiles.
func (s *Scheduler) Stats() Stats {
	p50, p99 := s.lags.percentiles()
	return Stats{
		Monitors: int(s.monitors.Load()),
		InFlight: int(s.inFlight.Load()),
		Skipped:  s.skipped.Load(),
		LagP50:   p50,
		LagP99:   p99,
		Panics:   s.panics.Load(),
	}
}

func (s *Scheduler) enqueue(c command) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.queue = append(s.queue, c)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// testSync is a test hook. The returned channel is closed once the loop has
// applied every command queued before it, handled every finished run and
// parked with exactly inFlight runs outstanding and no timer due; fn, if set,
// runs inside the loop at that moment. Nothing moves until the caller acts.
func (s *Scheduler) testSync(inFlight int, fn func()) <-chan struct{} {
	reply := make(chan struct{})
	s.enqueue(command{kind: cmdSync, inFlight: inFlight, fn: fn, reply: reply})
	return reply
}

func (s *Scheduler) loop(ctx context.Context) {
	defer func() {
		s.mu.Lock()
		s.stopped = true
		s.queue = nil
		s.mu.Unlock()
		close(s.exited)
	}()
	var (
		timer   <-chan time.Time
		timerAt time.Time
	)
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.quit:
			return
		default:
		}

		now := s.clk.Now()
		s.applyCommands(now)
		s.drainDone(now)
		saturated := s.dispatch(ctx, now)

		// Arm the timer for the earliest due entry, or for the retry when the
		// pool is full. An armed timer is only replaced by an earlier one; one
		// that fires too early just costs a spare pass.
		var at time.Time
		arm := true
		switch {
		case saturated:
			at = now.Add(saturatedRetry)
		case len(s.h) > 0:
			at = s.h[0].next
		default:
			arm = false
		}
		// Measured from a fresh now: time may have moved during this pass.
		if arm && (timer == nil || at.Before(timerAt)) {
			timer, timerAt = s.clk.After(at.Sub(s.clk.Now())), at
		}

		s.monitors.Store(int64(len(s.byID)))
		if len(s.syncs) > 0 {
			s.answerSyncs(timer != nil, timerAt)
		}

		select {
		case <-ctx.Done():
			return
		case <-s.quit:
			return
		case <-s.wake:
		case d := <-s.done:
			s.complete(d, s.clk.Now())
		case <-timer:
			timer = nil
		}
	}
}

func (s *Scheduler) applyCommands(now time.Time) {
	s.mu.Lock()
	q := s.queue
	s.queue = s.spare[:0]
	s.mu.Unlock()
	for i := range q {
		c := &q[i]
		switch c.kind {
		case cmdUpsert:
			s.upsert(c.id, c.d, now)
		case cmdReschedule:
			s.reschedule(c.id, c.d, now)
		case cmdRemove:
			s.remove(c.id)
		case cmdSync:
			s.syncs = append(s.syncs, *c)
		}
		*c = command{}
	}
	s.spare = q[:0]
}

func (s *Scheduler) upsert(id int64, interval time.Duration, now time.Time) {
	if interval <= 0 {
		return
	}
	if e, ok := s.byID[id]; ok {
		if e.interval == interval {
			return
		}
		e.interval = interval
		at := now.Add(interval)
		if s.running[id] {
			// The run in flight will answer with a delay chosen under the old
			// interval (the engine's is always positive): bound it.
			e.capAt, e.capped = at, true
		}
		if at.Before(e.next) {
			e.next = at
			heap.Fix(&s.h, e.index)
		}
		return
	}
	e := &entry{id: id, interval: interval, next: now.Add(s.jitter(interval))}
	s.byID[id] = e
	heap.Push(&s.h, e)
}

func (s *Scheduler) reschedule(id int64, in time.Duration, now time.Time) {
	e, ok := s.byID[id]
	if !ok {
		return
	}
	if s.running[id] && in <= 0 {
		e.rerun = true // run again as soon as the current run finishes
		return
	}
	e.next = now.Add(max(in, 0))
	e.pinned = s.running[id]
	heap.Fix(&s.h, e.index)
}

func (s *Scheduler) remove(id int64) {
	e, ok := s.byID[id]
	if !ok {
		return
	}
	heap.Remove(&s.h, e.index)
	delete(s.byID, id)
}

// dispatch starts every due entry it can. It reports true when a due entry is
// left waiting because the pool is full; the loop then retries after
// saturatedRetry or as soon as a run finishes. It never blocks.
func (s *Scheduler) dispatch(ctx context.Context, now time.Time) (saturated bool) {
	for len(s.h) > 0 {
		e := s.h[0]
		if e.next.After(now) {
			return false
		}
		if s.running[e.id] {
			// Skip if overrun: plan the next slot on the cadence, after now.
			s.skipped.Add(1)
			e.next = e.next.Add((now.Sub(e.next)/e.interval + 1) * e.interval)
			heap.Fix(&s.h, 0)
			continue
		}
		if s.busy >= s.conc {
			return true
		}
		s.busy++
		s.running[e.id] = true
		s.inFlight.Add(1)
		e.planned, e.pinned, e.capped = e.next, false, false
		// Until the completion plans the real next run, next marks the slot at
		// which this run counts as overrunning.
		e.next = e.planned.Add(e.interval)
		if !e.next.After(now) {
			e.next = now.Add(e.interval)
		}
		heap.Fix(&s.h, 0)
		s.wg.Add(1)
		go s.work(ctx, e, e.id, e.planned)
	}
	return false
}

func (s *Scheduler) work(ctx context.Context, e *entry, id int64, planned time.Time) {
	defer s.wg.Done()
	s.lags.add(s.clk.Now().Sub(planned))
	next := s.runSafely(ctx, id)
	s.inFlight.Add(-1)
	// Never blocks: at most conc runs are outstanding and done holds conc.
	s.done <- doneMsg{e: e, id: id, next: next}
}

// runSafely calls the RunFunc, turning a panic into a counted, finished run
// that keeps the registered interval, so one bad check cannot take down the
// scheduler or leak its semaphore slot.
func (s *Scheduler) runSafely(ctx context.Context, id int64) (next time.Duration) {
	defer func() {
		if recover() != nil {
			s.panics.Add(1) // next is still 0: the registered interval applies
		}
	}()
	return s.run(ctx, id)
}

func (s *Scheduler) drainDone(now time.Time) {
	for {
		select {
		case d := <-s.done:
			s.complete(d, now)
		default:
			return
		}
	}
}

// complete plans the next run from the one that finished: lastPlanned + d,
// where d is the RunFunc's answer or the interval. A time already in the past
// (the run overran, the pool was saturated or the loop stalled) becomes
// now + rand[0, d): missed runs are never replayed, and a fleet that fell
// behind together re-spreads over the interval instead of bunching. An
// interval change made during the run caps the result.
func (s *Scheduler) complete(d doneMsg, now time.Time) {
	s.busy--
	delete(s.running, d.id)
	e, ok := s.byID[d.id]
	if !ok || e != d.e {
		return // removed (or removed and re-added) while running
	}
	if e.rerun {
		e.rerun, e.pinned = false, false
		e.next = now
		heap.Fix(&s.h, e.index)
		return
	}
	if e.pinned {
		e.pinned = false
		return
	}
	step := d.next
	if step <= 0 {
		step = e.interval
	}
	next := e.planned.Add(step)
	if next.Before(now) {
		next = now.Add(s.jitter(step))
	}
	if e.capped && e.capAt.Before(next) {
		next = e.capAt
		if next.Before(now) {
			next = now // already overdue under the new interval: run it now
		}
	}
	e.next = next
	heap.Fix(&s.h, e.index)
}

func (s *Scheduler) jitter(upTo time.Duration) time.Duration {
	return time.Duration(s.rnd.Int64N(int64(upTo)))
}

// answerSyncs releases the testSync requests whose condition holds. The loop
// is quiet when nothing is queued, no completion is waiting and no timer is
// due by the clock's current reading.
func (s *Scheduler) answerSyncs(timerArmed bool, timerAt time.Time) {
	timerDue := timerArmed && !timerAt.After(s.clk.Now())
	s.mu.Lock()
	queued := len(s.queue)
	s.mu.Unlock()
	quiet := queued == 0 && len(s.done) == 0 && !timerDue
	kept := s.syncs[:0]
	for _, c := range s.syncs {
		if !quiet || s.busy != c.inFlight {
			kept = append(kept, c)
			continue
		}
		if c.fn != nil {
			c.fn()
		}
		close(c.reply)
	}
	clear(s.syncs[len(kept):])
	s.syncs = kept
}

// lagRing keeps the most recent lagWindow start lags. Workers add to it and
// Stats reads it, so it has its own lock.
type lagRing struct {
	mu  sync.Mutex
	buf []time.Duration
	pos int
}

func (r *lagRing) add(d time.Duration) {
	d = max(d, 0)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) < lagWindow {
		r.buf = append(r.buf, d)
		return
	}
	r.buf[r.pos] = d
	r.pos = (r.pos + 1) % lagWindow
}

func (r *lagRing) percentiles() (p50, p99 time.Duration) {
	r.mu.Lock()
	sorted := slices.Clone(r.buf)
	r.mu.Unlock()
	if len(sorted) == 0 {
		return 0, 0
	}
	slices.Sort(sorted)
	return nearestRank(sorted, 50), nearestRank(sorted, 99)
}

// nearestRank returns the pct-th percentile of sorted: element ceil(pct/100·n)−1.
func nearestRank(sorted []time.Duration, pct int) time.Duration {
	i := (len(sorted)*pct+99)/100 - 1
	return sorted[max(i, 0)]
}
