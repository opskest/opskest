package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"opskest/internal/check"
	"opskest/internal/logx"
	"opskest/internal/maint"
	"opskest/internal/model"
	"opskest/internal/store"
)

// Sink receives alert-worthy events (the alert dispatcher). Publish must
// not block; it is called with the monitor's publish lock held.
type Sink interface{ Publish(ev model.Event) }

// Scheduler is the part of scheduler.Scheduler the engine drives.
type Scheduler interface {
	Upsert(id int64, interval time.Duration)
	Reschedule(id int64, in time.Duration)
	Remove(id int64)
}

// Deps wires the engine. Sched may be set later with SetScheduler (the
// scheduler needs e.RunCheck first). Guard, Alerts and RDAP may be nil;
// Now defaults to time.Now, Log to slog.Default and Checker to check.Run.
type Deps struct {
	Store   *store.Store
	Writer  *store.Writer
	Sched   Scheduler
	Alerts  Sink
	Guard   Guard
	RDAP    *check.RDAP
	Now     func() time.Time
	Log     *slog.Logger
	Checker func(ctx context.Context, m *model.Monitor) model.Result
}

// AlertInfo is what the alert dispatcher needs to render a monitor.
type AlertInfo struct {
	Name, Group, Target, Runbook string
	ChannelIDs                   []int64
}

// Dirty is what changed since the last TakeDirty, for the SSE hub.
// System means connectivity changed.
type Dirty struct {
	Changed, Removed                     []int64
	Groups, Outages, Maintenance, System bool
}

const (
	guardReason      = "opskest lost internet connectivity"
	upsideDownReason = "reachable, but upside-down mode expects it to be down"
	panicReason      = "internal error: check panicked"

	beatsToLoad      = ringSize
	loadConcurrency  = 8
	rdapConcurrency  = 4
	rdapTimeout      = 30 * time.Second
	domainRecheck    = 24 * time.Hour
	maintenanceEvery = 15 * time.Second
	remindersEvery   = 30 * time.Second
	domainEvery      = time.Hour
	domainFirstRun   = time.Minute

	newMonitorFirstCheckWithin = 3 * time.Second
)

var (
	certThresholds   = []int{1, 3, 7, 14} // days, ascending
	domainThresholds = []int{7, 14, 30}
)

// entry is one monitor's in-memory state.
//
// Locking: Engine.mu guards the entries map, the sort order and the group
// and maintenance lists; entry.mu guards everything below it. m, host and
// the GroupID inside m are replaced (never mutated) under BOTH locks, so a
// holder of either may read them. Lock order is Engine.mu → entry.pub →
// entry.mu → (affMu | dirtyMu | connMu); Engine.mu is never taken while an
// entry lock is held.
//
// pub serialises one monitor's state changes together with their
// publishing (Writer + Sink): a writer of state takes pub, then mu, updates
// the state, releases mu and only then calls the Writer and the Sink. So the
// writer sees each monitor's ops in state order (an outage open never lands
// after its close), while mu — the lock readers such as Summaries need — is
// never held across a Writer or Sink call and never waits on a publisher.
type entry struct {
	id  int64
	pub sync.Mutex
	mu  sync.Mutex

	m       *model.Monitor
	host    string
	removed bool
	paused  bool
	maint   bool

	under     State // Step state; Status == UnderStatus
	disp      model.Status
	dispSince time.Time

	// first failure of the current failing episode, for outages.first_reason
	firstReason string
	firstClass  model.ErrClass

	ring     ring
	win      windows
	lastAt   int64 // last check, unix ms (0 = never)
	lastLat  int
	lastCode int    // HTTP status of the latest check, for alerts
	lastBody string // response snippet of the latest check, for alerts only (never stored)
	reason   string
	errClass model.ErrClass

	outage       *store.Outage // open outage (a Down alert went out); replaced, never mutated
	lastReminder time.Time
	degAlerted   bool

	// A 3rd-party feed (Settings.Vendor) is alert-only: it never opens an
	// outage, so vendorSince (non-zero while its Down alert is outstanding)
	// stands in for en.outage to pair Down, Reminder and Recovered.
	vendorSince time.Time

	rt         store.Runtime // pointer fields are replaced, never mutated
	pushBase   time.Time     // last push, or when the monitor was loaded/created/resumed
	domainBusy bool

	ver       uint64
	cache     MonitorSummary
	cacheVer  uint64
	cacheHour int64
	cacheOK   bool
}

func (en *entry) touch() { en.ver++ }

// pubs is what one state change hands to the writer and the sink.
type pubs struct {
	hb      store.Heartbeat
	hasHB   bool
	ops     []store.Op
	evs     []model.Event
	outages bool
}

// Engine holds every monitor's state in memory. See Deps and the method
// docs; the hot path (Apply) never reads the database.
type Engine struct {
	st      *store.Store
	w       *store.Writer
	sink    Sink
	guard   Guard
	rdap    *check.RDAP
	now     func() time.Time
	log     *slog.Logger
	loc     *time.Location
	checker func(ctx context.Context, m *model.Monitor) model.Result

	schedMu sync.RWMutex
	sched   Scheduler

	mu        sync.RWMutex
	entries   map[int64]*entry
	order     []*entry
	sortDirty bool
	byToken   map[string]int64
	groups    []store.Group
	groupByID map[int64]store.Group
	windows   []store.Maintenance

	outageSeq atomic.Int64

	connMu      sync.Mutex
	online      bool
	onlineSince time.Time

	dirtyMu     sync.Mutex
	dirty       map[int64]struct{}
	removedIDs  []int64
	dGroups     bool
	dOutages    bool
	dMaint      bool
	dSystem     bool
	activeWinID []int64

	affMu      sync.Mutex
	affected   map[int64]struct{}
	parentDown map[int64]struct{} // displayed DOWN or maintenance: holds its children's alerts

	// maintMu serialises maintenance passes, so a pass working from a
	// stale window list can never interleave with (and undo) a newer one.
	maintMu sync.Mutex

	domainRunning atomic.Bool
}

type nopSched struct{}

func (nopSched) Upsert(int64, time.Duration)     {}
func (nopSched) Reschedule(int64, time.Duration) {}
func (nopSched) Remove(int64)                    {}

// New builds an engine; call Load before scheduling any check.
func New(d Deps) *Engine {
	e := &Engine{
		st: d.Store, w: d.Writer, sink: d.Alerts, guard: d.Guard, rdap: d.RDAP,
		now: d.Now, log: d.Log, checker: d.Checker, sched: d.Sched,
		entries:    map[int64]*entry{},
		byToken:    map[string]int64{},
		groupByID:  map[int64]store.Group{},
		dirty:      map[int64]struct{}{},
		affected:   map[int64]struct{}{},
		parentDown: map[int64]struct{}{},
		online:     true,
	}
	if e.now == nil {
		e.now = time.Now
	}
	if e.log == nil {
		e.log = slog.Default()
	}
	if e.checker == nil {
		e.checker = func(ctx context.Context, m *model.Monitor) model.Result { return check.Run(ctx, m, time.Now) }
	}
	if e.sched == nil {
		e.sched = nopSched{}
	}
	e.loc = time.UTC
	if d.Store != nil && d.Store.Loc != nil {
		e.loc = d.Store.Loc
	}
	e.onlineSince = e.now()
	return e
}

// SetScheduler installs the scheduler (called once in app wiring, since
// the scheduler is built from e.RunCheck).
func (e *Engine) SetScheduler(s Scheduler) {
	if s == nil {
		s = nopSched{}
	}
	e.schedMu.Lock()
	e.sched = s
	e.schedMu.Unlock()
}

func (e *Engine) scheduler() Scheduler {
	e.schedMu.RLock()
	defer e.schedMu.RUnlock()
	return e.sched
}

func (e *Engine) dayBucket(ms int64) int64 {
	t := time.UnixMilli(ms).In(e.loc)
	start, _ := store.CalendarDayStart(t.Year(), t.Month(), t.Day(), e.loc)
	return start.UnixMilli()
}

func (e *Engine) lookup(id int64) *entry {
	e.mu.RLock()
	en := e.entries[id]
	e.mu.RUnlock()
	return en
}

func (e *Engine) snapshot() []*entry {
	e.mu.RLock()
	out := slices.Clone(e.order)
	e.mu.RUnlock()
	return out
}

func interval(m *model.Monitor) time.Duration { return time.Duration(m.IntervalS) * time.Second }

func pushWindow(m *model.Monitor) time.Duration {
	return time.Duration(m.IntervalS+m.Settings.GraceS) * time.Second
}

func beatChar(s model.Status, inMaint bool) byte {
	if inMaint {
		return 'm'
	}
	return s.BeatChar()
}

func lat32(ms int) int32 {
	if ms < 0 {
		return -1
	}
	if ms > 1<<30 {
		return 1 << 30
	}
	return int32(ms)
}

// ---- boot -------------------------------------------------------------------

// Load builds the in-memory state at boot: monitors, each
// one's last 100 beats (one primary-key range read per monitor, 8 at a
// time), open outages, runtime rows, rollups (hourly 25 h, daily 91 d),
// maintenance windows and groups. It then schedules every non-paused
// monitor. Boot state is never recorded as a status event.
func (e *Engine) Load(ctx context.Context) error {
	now := e.now()
	nowMs := now.UnixMilli()

	mons, err := e.st.ListMonitors(ctx)
	if err != nil {
		return err
	}
	groups, err := e.st.ListGroups(ctx)
	if err != nil {
		return err
	}
	ws, err := e.st.ListMaintenance(ctx)
	if err != nil {
		return err
	}
	outs, err := e.st.ActiveOutages(ctx)
	if err != nil {
		return err
	}
	rts, err := e.st.AllRuntime(ctx)
	if err != nil {
		return err
	}
	hourly, err := e.st.RollupsSince(ctx, "rollup_hourly", store.HourBucket(nowMs)-(hourlySlots)*hourMs)
	if err != nil {
		return err
	}
	dayFrom := time.UnixMilli(e.dayBucket(nowMs)).In(e.loc).AddDate(0, 0, -(dailySlots - 1)).UnixMilli()
	daily, err := e.st.RollupsSince(ctx, "rollup_daily", dayFrom)
	if err != nil {
		return err
	}
	maxOutage, err := e.st.MaxOutageID(ctx)
	if err != nil {
		return err
	}
	beats, err := e.loadBeats(ctx, mons)
	if err != nil {
		return err
	}

	openByMon := make(map[int64]store.Outage, len(outs))
	for _, o := range outs { // newest first: keep the newest per monitor
		if _, dup := openByMon[o.MonitorID]; !dup {
			openByMon[o.MonitorID] = o
		}
	}

	entries := make(map[int64]*entry, len(mons))
	order := make([]*entry, 0, len(mons))
	byToken := map[string]int64{}
	for i, m := range mons {
		en := &entry{id: m.ID, m: m, host: m.Host(), paused: m.Paused, lastLat: -1, pushBase: now}
		en.rt = store.Runtime{MonitorID: m.ID}
		if rt, ok := rts[m.ID]; ok {
			en.rt = rt
			if rt.LastPushAt != nil {
				en.pushBase = time.UnixMilli(*rt.LastPushAt)
			}
		}
		if o, ok := openByMon[m.ID]; ok && !m.Settings.Vendor {
			oc := o
			en.outage = &oc
		}
		restoreBeats(en, beats[i], now)
		if m.Settings.Vendor && en.under.Status == model.StatusDown {
			en.vendorSince = en.under.Since // its Down went out before the restart: no second alert
		}
		if !m.Settings.Vendor { // a 3rd-party feed has no uptime
			for _, r := range hourly[m.ID] {
				addRollup(en.win.hourly[:], hourMs, r)
			}
			for _, r := range daily[m.ID] {
				addRollup(en.win.daily[:], dayMs, r)
			}
		}
		entries[m.ID] = en
		order = append(order, en)
		if m.Type == model.TypePush && m.PushToken != "" {
			byToken[m.PushToken] = m.ID
		}
	}

	groupOf := func(id int64) *int64 {
		if en := entries[id]; en != nil {
			return en.m.GroupID
		}
		return nil
	}
	ids := make([]int64, 0, len(mons))
	for _, m := range mons {
		if !m.Settings.Vendor { // 3rd-party feeds are never put in maintenance
			ids = append(ids, m.ID)
		}
	}
	set := maint.ActiveSet(ws, ids, groupOf, now)

	// A paused or in-maintenance monitor has no beats to say how long it has
	// been so: take the time of the status event that put it there.
	displaySince := map[int64]time.Time{}
	for _, en := range order {
		want := model.StatusPaused
		switch {
		case en.paused:
		case set[en.id]:
			want = model.StatusMaintenance
		default:
			continue
		}
		displaySince[en.id] = e.statusSince(ctx, en.id, want, now)
	}

	e.mu.Lock()
	e.entries, e.order, e.byToken, e.windows = entries, order, byToken, ws
	e.setGroupsLocked(groups)
	e.sortDirty = true
	e.mu.Unlock()
	e.outageSeq.Store(maxOutage)

	e.affMu.Lock()
	clear(e.affected)
	clear(e.parentDown)
	e.affMu.Unlock()
	for _, en := range order {
		en.mu.Lock()
		en.maint = set[en.id]
		en.disp = displayed(en)
		switch {
		case en.disp == model.StatusPaused || en.disp == model.StatusMaintenance:
			en.dispSince = displaySince[en.id]
		default:
			en.dispSince = en.under.Since
		}
		// A degraded monitor that alerts on degradation has already told
		// someone (before the restart): its all-clear must still go out.
		en.degAlerted = en.under.Status == model.StatusDegraded && en.m.NotifyDegraded && !en.maint && !en.paused
		e.setAffected(en.id, en.disp, en.m.Settings.Vendor)
		en.touch()
		en.mu.Unlock()
	}

	e.dirtyMu.Lock()
	e.activeWinID = activeWindowIDs(ws, now)
	e.dGroups, e.dOutages, e.dMaint = true, true, true
	e.dirtyMu.Unlock()

	sched := e.scheduler()
	for _, en := range order {
		if !en.paused {
			sched.Upsert(en.id, interval(en.m))
		}
	}
	return nil
}

// statusSince is when id's displayed status last changed to want, from the
// status-event feed; now when nothing was recorded.
func (e *Engine) statusSince(ctx context.Context, id int64, want model.Status, now time.Time) time.Time {
	evs, err := e.st.ListStatusEvents(ctx, &id, 0, 20)
	if err == nil {
		for _, ev := range evs { // newest first
			if ev.To == want {
				return time.UnixMilli(ev.TS)
			}
		}
	}
	return now
}

// loadBeats reads each monitor's last 100 beats, loadConcurrency at a
// time on the read pool.
func (e *Engine) loadBeats(ctx context.Context, mons []*model.Monitor) ([][]store.Heartbeat, error) {
	out := make([][]store.Heartbeat, len(mons))
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
		next     atomic.Int64
	)
	workers := min(loadConcurrency, len(mons))
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= len(mons) {
					return
				}
				hbs, err := e.st.LastBeats(ctx, mons[i].ID, beatsToLoad)
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					return
				}
				out[i] = hbs
			}
		}()
	}
	wg.Wait()
	return out, firstErr
}

// restoreBeats rebuilds the ring and the Step state from the stored beats
// (oldest first). Guard beats (UNKNOWN) never changed the state, so the
// state is taken from the newest real result; for a restored DOWN with an
// open outage, Since is the outage start.
func restoreBeats(en *entry, hbs []store.Heartbeat, now time.Time) {
	en.under = State{Status: model.StatusUnknown, Since: now, UnderStatus: model.StatusUnknown}
	for _, hb := range hbs {
		en.ring.push(beat{ts: hb.TS, lat: lat32(hb.LatencyMs), c: beatChar(hb.Status, hb.Maint)})
	}
	if len(hbs) == 0 {
		return
	}
	newest := hbs[len(hbs)-1]
	en.lastAt, en.lastLat = newest.TS, newest.LatencyMs

	idx := -1
	for i := len(hbs) - 1; i >= 0; i-- {
		switch hbs[i].Status {
		case model.StatusUp, model.StatusDegraded, model.StatusPending, model.StatusDown:
			idx = i
		}
		if idx >= 0 {
			break
		}
	}
	if idx < 0 {
		return
	}
	cur := hbs[idx]
	st := cur.Status
	failing := st == model.StatusPending || st == model.StatusDown
	since, fails := cur.TS, 0
	firstReason, firstClass := cur.Msg, cur.ErrClass
	sameRun := true
	for j := idx; j >= 0; j-- {
		s := hbs[j].Status
		if s == model.StatusUnknown {
			continue
		}
		if !failing {
			if s != st {
				break
			}
			since = hbs[j].TS
			continue
		}
		if s != model.StatusPending && s != model.StatusDown {
			break
		}
		firstReason, firstClass = hbs[j].Msg, hbs[j].ErrClass
		if s == st && sameRun {
			since = hbs[j].TS
			fails++
		} else {
			sameRun = false
		}
	}
	if st == model.StatusDown && en.outage != nil {
		since = en.outage.StartedAt
	}
	en.under = State{Status: st, Since: time.UnixMilli(since), Fails: fails, UnderStatus: st}
	en.reason, en.errClass = cur.Msg, model.ErrClass(cur.ErrClass)
	if failing {
		en.firstReason, en.firstClass = firstReason, model.ErrClass(firstClass)
	}
}

func displayed(en *entry) model.Status {
	switch {
	case en.paused:
		return model.StatusPaused
	case en.maint:
		return model.StatusMaintenance
	}
	return en.under.Status
}

// refreshDisplay recomputes the displayed status and, when it changed,
// queues a status event (the "Important events" feed). Callers hold en.mu.
func (e *Engine) refreshDisplay(en *entry, now time.Time, reason string, class model.ErrClass, p *pubs) {
	d := displayed(en)
	if d == en.disp {
		return
	}
	from := en.disp
	en.disp, en.dispSince = d, now
	e.setAffected(en.id, d, en.m.Settings.Vendor)
	p.ops = append(p.ops, store.OpInsertStatusEvent(store.StatusEvent{
		MonitorID: en.id, TS: now.UnixMilli(), From: from, To: d, Reason: reason, ErrClass: string(class),
	}))
}

// publish hands p to the writer and the sink. Callers hold en.pub (and
// not en.mu).
func (e *Engine) publish(id int64, p *pubs) {
	if e.w != nil {
		if p.hasHB {
			e.w.Heartbeat(p.hb)
		}
		for _, op := range p.ops {
			e.w.DoFor(id, op)
		}
	}
	if e.sink != nil {
		for _, ev := range p.evs {
			e.sink.Publish(ev)
		}
	}
	e.markDirty(id, p.outages)
}

// ---- the check path ------------------------------------------------------------

// RunCheck is the scheduler's RunFunc: it runs one check for id (or, for a
// push monitor, its deadline check) and applies the result. A panicking
// checker is recovered, logged and recorded as a failure (ruling R9).
func (e *Engine) RunCheck(ctx context.Context, id int64) (next time.Duration) {
	defer func() {
		if rec := recover(); rec != nil {
			logx.Err(e.log, "engine: run check panicked", fmt.Errorf("%v", rec), "monitor", id)
			next = 0
		}
	}()
	en := e.lookup(id)
	if en == nil {
		return 0
	}
	en.mu.Lock()
	m, paused, removed, base := en.m, en.paused, en.removed, en.pushBase
	en.mu.Unlock()
	if paused || removed {
		return 0
	}

	if m.Type == model.TypePush {
		now := e.now()
		window := pushWindow(m)
		if deadline := base.Add(window); now.Before(deadline) {
			return deadline.Sub(now)
		}
		return e.Apply(id, model.Result{
			At: now, OK: false, LatencyMs: -1, ErrClass: model.ErrTimeout,
			Reason: fmt.Sprintf("no push received for %d s", int(window/time.Second)),
		})
	}

	// Group and manual monitors have no network check: they are read off the
	// engine's own state.
	switch m.Type {
	case model.TypeGroup:
		r := e.groupResult(m)
		r.At = e.now()
		return e.Apply(id, r)
	case model.TypeManual:
		en.mu.Lock()
		st, reason := en.under.Status, en.reason
		en.mu.Unlock()
		r := manualResult(reason, st)
		r.At = e.now()
		return e.Apply(id, r)
	}

	r, panicked := e.safeCheck(ctx, m)
	if !panicked && ctx.Err() != nil {
		return 0 // shutting down: a cancelled check says nothing about the target
	}
	return e.Apply(id, r)
}

func (e *Engine) safeCheck(ctx context.Context, m *model.Monitor) (r model.Result, panicked bool) {
	defer func() {
		if rec := recover(); rec != nil {
			logx.Err(e.log, "engine: check panicked", fmt.Errorf("%v", rec), "monitor", m.ID)
			r = model.Result{At: e.now(), OK: false, LatencyMs: -1, ErrClass: model.ErrOther, Reason: panicReason}
			panicked = true
		}
	}()
	return e.checker(ctx, m), false
}

// Apply folds one result into id's state: internet-loss guard, upside-down,
// Step, maintenance display, heartbeat, ring and uptime windows, outages,
// certificate expiry, status events and alerts. It returns the delay until
// the next check: the retry interval while PENDING, the interval otherwise,
// and 0 when the monitor is unknown, removed or paused.
func (e *Engine) Apply(id int64, r model.Result) time.Duration {
	en := e.lookup(id)
	if en == nil {
		return 0
	}
	if r.At.IsZero() {
		r.At = e.now()
	}
	en.mu.Lock()
	// Push, group and manual results do not come from this server's network, so
	// the internet-loss guard has nothing to say about them.
	skip, isPush := en.removed || en.paused, en.m.Type == model.TypePush || en.m.Type == model.TypeGroup || en.m.Type == model.TypeManual
	en.mu.Unlock()
	if skip {
		return 0
	}

	// Ask the guard outside any lock; it may dial for up to 2 s.
	offline := false
	if !r.OK && !isPush {
		offline = !e.checkGuard()
	} else if !e.isOnline() {
		e.checkGuard()
	}

	en.pub.Lock()
	defer en.pub.Unlock()
	p, delay, ok := e.applyLocked(en, r, offline)
	if !ok {
		return 0
	}
	e.publish(id, &p)
	return delay
}

// applyLocked does Apply's state work under en.mu. Callers hold en.pub and
// publish the result after it returns.
func (e *Engine) applyLocked(en *entry, r model.Result, offline bool) (p pubs, delay time.Duration, ok bool) {
	en.mu.Lock()
	defer en.mu.Unlock()
	if en.removed || en.paused {
		return p, 0, false
	}
	m := en.m
	now := r.At
	nowMs := now.UnixMilli()

	if offline {
		p.hb = heartbeat(m.ID, nowMs, model.StatusUnknown, r, guardReason, en.maint)
		p.hb.NoRollup = m.Settings.Vendor
		p.hasHB = true
		en.ring.push(beat{ts: nowMs, lat: lat32(r.LatencyMs), c: beatChar(model.StatusUnknown, en.maint)})
		en.lastAt = nowMs
		en.touch()
		return p, interval(m), true
	}

	if m.UpsideDown {
		r = invert(r)
	}
	en.lastCode, en.lastBody = r.Code, r.Body

	prev := en.under.Status
	retries := m.Retries
	if m.Type == model.TypeManual {
		retries = 0 // a status set by hand takes effect at once
	}
	tr := Step(en.under, r, retries, now)
	en.under = tr.Next
	under := tr.Next.Status

	if r.OK {
		en.firstReason, en.firstClass = "", ""
	} else if (prev != model.StatusPending && prev != model.StatusDown) || en.firstReason == "" {
		en.firstReason, en.firstClass = r.Reason, r.ErrClass
	}

	msg := r.Reason
	if under == model.StatusUp {
		msg = ""
	}
	p.hb = heartbeat(m.ID, nowMs, under, r, msg, en.maint)
	p.hb.NoRollup = m.Settings.Vendor
	p.hasHB = true
	en.ring.push(beat{ts: nowMs, lat: lat32(r.LatencyMs), c: beatChar(under, en.maint)})
	if !en.maint && !m.Settings.Vendor { // a 3rd-party feed never counts toward uptime
		en.win.add(store.HourBucket(nowMs), e.dayBucket(nowMs), under)
	}
	en.lastAt, en.lastLat, en.reason, en.errClass = nowMs, r.LatencyMs, msg, r.ErrClass

	// Recovered goes out whenever an alerted outage closes — even inside a
	// maintenance window, so PagerDuty/Opsgenie incidents always resolve.
	if r.OK && en.outage != nil {
		e.closeOutage(en, now, r.LatencyMs, &p)
	}
	if r.OK && !en.vendorSince.IsZero() {
		e.vendorRecovered(en, now, r.LatencyMs, &p)
	}
	for _, k := range tr.Events {
		switch k {
		case model.EvDegraded:
			if m.NotifyDegraded && !en.maint {
				p.evs = append(p.evs, e.event(en, model.EvDegraded, now, msg, r))
				en.degAlerted = true
			}
		case model.EvDegradedCleared:
			if en.degAlerted {
				p.evs = append(p.evs, e.event(en, model.EvDegradedCleared, now, "", r))
				en.degAlerted = false
			}
		}
	}
	if under == model.StatusUp && en.degAlerted { // e.g. DEGRADED → PENDING → UP
		p.evs = append(p.evs, e.event(en, model.EvDegradedCleared, now, "", r))
		en.degAlerted = false
	}
	// Down goes out (and the outage opens) when the monitor is DOWN outside
	// maintenance and nobody has been told yet: the normal flip, or the
	// first check after a restart that found it DOWN without an outage.
	if under == model.StatusDown && !en.maint && en.outage == nil {
		if m.Settings.Vendor {
			if en.vendorSince.IsZero() {
				e.vendorDown(en, now, r.Reason, r.LatencyMs, &p)
			}
		} else {
			e.openOutage(en, now, r.Reason, r.ErrClass, r.LatencyMs, &p)
		}
	}

	if r.Cert != nil {
		e.certCheck(en, r.Cert, now, &p)
	}

	e.refreshDisplay(en, now, msg, r.ErrClass, &p)
	en.touch()

	delay = interval(m)
	if under == model.StatusPending {
		delay = time.Duration(m.RetryIntervalS) * time.Second
	}
	return p, delay, true
}

func heartbeat(id, ts int64, st model.Status, r model.Result, msg string, inMaint bool) store.Heartbeat {
	hb := store.Heartbeat{
		MonitorID: id, TS: ts, Status: st, LatencyMs: r.LatencyMs, Code: r.Code,
		DNSMs: -1, TCPMs: -1, TLSMs: -1, TTFBMs: -1, WaitMs: -1, AppMs: -1,
		ErrClass: string(r.ErrClass), Msg: msg, Maint: inMaint,
	}
	if st == model.StatusUnknown {
		hb.ErrClass = ""
	}
	if t := r.Timing; t != nil {
		hb.DNSMs, hb.TCPMs, hb.TLSMs, hb.TTFBMs = t.DNSMs, t.TCPMs, t.TLSMs, t.TTFBMs
		hb.WaitMs, hb.AppMs, hb.Edge, hb.Conn = t.WaitMs, t.AppMs, t.Edge, t.Conn
	}
	return hb
}

// invert applies upside-down mode: reachable is a failure, unreachable a
// success.
func invert(r model.Result) model.Result {
	if r.OK {
		r.OK, r.Degraded = false, false
		r.Reason, r.ErrClass = upsideDownReason, model.ErrAssertion
		return r
	}
	r.OK, r.Degraded, r.Reason, r.ErrClass = true, false, "", model.ErrNone
	return r
}

func (e *Engine) event(en *entry, kind model.EventKind, now time.Time, reason string, r model.Result) model.Event {
	return model.Event{
		Kind: kind, MonitorID: en.id, At: now, Status: en.under.Status,
		Reason: reason, ErrClass: r.ErrClass, Since: en.under.Since, LatencyMs: r.LatencyMs,
		Code: r.Code, Response: r.Body,
	}
}

func (e *Engine) openOutage(en *entry, now time.Time, reason string, class model.ErrClass, latency int, p *pubs) {
	first, firstClass := en.firstReason, en.firstClass
	if first == "" {
		first, firstClass = reason, class
	}
	o := store.Outage{
		ID: e.outageSeq.Add(1), MonitorID: en.id, StartedAt: now.UnixMilli(),
		FirstReason: first, ErrClass: string(firstClass),
		// A parent that is down explains this outage: record it, hold its
		// alerts (releaseHeldOutages sends Down if the parent recovers first).
		SuppressedBy: e.downParent(en.m.Settings.DependsOn),
	}
	en.outage = &o
	en.lastReminder = time.Time{}
	en.degAlerted = false
	p.ops = append(p.ops, store.OpOpenOutage(o))
	p.outages = true
	if o.SuppressedBy != nil {
		return
	}
	p.evs = append(p.evs, model.Event{
		Kind: model.EvDown, MonitorID: en.id, At: now, Status: model.StatusDown,
		Reason: reason, ErrClass: class, Since: now, LatencyMs: latency, OutageID: o.ID,
		Code: en.lastCode, Response: en.lastBody,
	})
}

// vendorDown alerts that a 3rd-party feed is down without recording an
// outage: no row, no incident, no region confirmation (OutageID stays 0).
func (e *Engine) vendorDown(en *entry, now time.Time, reason string, latency int, p *pubs) {
	en.vendorSince = now
	en.lastReminder = time.Time{}
	en.degAlerted = false
	p.evs = append(p.evs, model.Event{
		Kind: model.EvDown, MonitorID: en.id, At: now, Status: model.StatusDown,
		Reason: reason, ErrClass: en.errClass, Since: now, LatencyMs: latency,
		Code: en.lastCode, Response: en.lastBody,
	})
}

// vendorRecovered is the all-clear for vendorDown.
func (e *Engine) vendorRecovered(en *entry, now time.Time, latency int, p *pubs) {
	started := en.vendorSince
	en.vendorSince = time.Time{}
	en.lastReminder = time.Time{}
	p.evs = append(p.evs, model.Event{
		Kind: model.EvRecovered, MonitorID: en.id, At: now, Status: en.under.Status,
		Since: started, Duration: now.Sub(started), LatencyMs: latency,
		Code: en.lastCode, Response: en.lastBody,
	})
}

func (e *Engine) closeOutage(en *entry, now time.Time, latency int, p *pubs) {
	o := en.outage
	started := time.UnixMilli(o.StartedAt)
	p.ops = append(p.ops, store.OpCloseOutage(o.ID, now.UnixMilli()))
	p.outages = true
	if o.SuppressedBy != nil { // nobody was told it went down
		en.outage = nil
		return
	}
	p.evs = append(p.evs, model.Event{
		Kind: model.EvRecovered, MonitorID: en.id, At: now, Status: en.under.Status,
		Since: started, Duration: now.Sub(started), LatencyMs: latency, OutageID: o.ID,
		Code: en.lastCode, Response: en.lastBody,
	})
	en.outage = nil
	en.lastReminder = time.Time{}
}

// tierFor returns the smallest threshold that days has reached (ascending
// thresholds), e.g. 6 days → 7 for the certificate ladder 1/3/7/14.
func tierFor(thresholds []int, days int) (int, bool) {
	for _, t := range thresholds {
		if days <= t {
			return t, true
		}
	}
	return 0, false
}

// certThresholdsFor returns the days-before-expiry ladder for m, ascending:
// its own Settings.CertThresholds, or the default 1/3/7/14.
func certThresholdsFor(m *model.Monitor) []int {
	t := m.Settings.CertThresholds
	if len(t) == 0 {
		return certThresholds
	}
	t = slices.Clone(t)
	slices.Sort(t)
	return t
}

// certCheck records the certificate and fires CertExpiring once per
// threshold (14/7/3/1 d) per certificate; a new NotAfter resets the ladder.
// The last alerted threshold is persisted (cert_alerted_days), so a
// restart never re-alerts. Callers hold en.mu.
func (e *Engine) certCheck(en *entry, c *model.CertInfo, now time.Time, p *pubs) {
	rt := en.rt
	changed := false
	na := c.NotAfter.UnixMilli()
	if rt.CertNotAfter == nil || *rt.CertNotAfter != na {
		rt.CertNotAfter, rt.CertAlertedDays = &na, nil
		changed = true
	}
	if rt.CertIssuer != c.Issuer || rt.CertSubject != c.Subject {
		rt.CertIssuer, rt.CertSubject = c.Issuer, c.Subject
		changed = true
	}
	days := daysUntil(c.NotAfter, now)
	if tier, ok := tierFor(certThresholdsFor(en.m), days); ok && (rt.CertAlertedDays == nil || tier < *rt.CertAlertedDays) {
		rt.CertAlertedDays = &tier
		changed = true
		p.evs = append(p.evs, model.Event{
			Kind: model.EvCertExpiring, MonitorID: en.id, At: now, Status: displayed(en),
			Reason: expiryReason("certificate", days), Days: days,
		})
	}
	if changed {
		en.rt = rt
		p.ops = append(p.ops, store.OpUpsertRuntime(rt))
	}
}

func expiryReason(what string, days int) string {
	switch {
	case days < 0:
		return fmt.Sprintf("%s expired %d days ago", what, -days)
	case days == 1:
		return what + " expires in 1 day"
	}
	return fmt.Sprintf("%s expires in %d days", what, days)
}

// Push records a push for token: status "down" is a failure carrying msg,
// anything else a success with latency ms (when > 0). It then re-arms the
// deadline at interval + grace. It returns false for an unknown token.
// Pushes to a paused monitor are accepted and ignored.
func (e *Engine) Push(token, status, msg string, ms int) bool {
	e.mu.RLock()
	id, ok := e.byToken[token]
	en := e.entries[id]
	e.mu.RUnlock()
	if !ok || en == nil {
		return false
	}
	now := e.now()
	r := model.Result{At: now, OK: true, LatencyMs: -1}
	if ms > 0 {
		r.LatencyMs = ms
	}
	if strings.EqualFold(status, "down") {
		r.OK, r.ErrClass = false, model.ErrOther
		r.Reason = model.TrimReason(msg)
		if r.Reason == "" {
			r.Reason = "push reported down"
		}
	}

	en.pub.Lock()
	en.mu.Lock()
	if en.removed || en.paused || en.m.Type != model.TypePush {
		en.mu.Unlock()
		en.pub.Unlock()
		return true
	}
	nowMs := now.UnixMilli()
	en.pushBase = now
	en.rt.LastPushAt = &nowMs
	rt := en.rt
	window := pushWindow(en.m)
	en.mu.Unlock()
	if e.w != nil {
		e.w.DoFor(id, store.OpUpsertRuntime(rt))
	}
	en.pub.Unlock()

	e.Apply(id, r)
	e.scheduler().Reschedule(id, window)
	return true
}

// ---- guard ----------------------------------------------------------------------

func (e *Engine) isOnline() bool {
	e.connMu.Lock()
	defer e.connMu.Unlock()
	return e.online
}

// checkGuard asks the guard (cached 30 s) and records connectivity
// changes for the UI banner.
func (e *Engine) checkGuard() bool {
	if e.guard == nil {
		return true
	}
	v := e.guard.Online(context.Background())
	e.connMu.Lock()
	changed := v != e.online
	if changed {
		e.online, e.onlineSince = v, e.now()
	}
	e.connMu.Unlock()
	if changed {
		e.dirtyMu.Lock()
		e.dSystem = true
		e.dirtyMu.Unlock()
		if !v {
			e.log.Warn("engine: internet connectivity lost; failures are recorded as unknown")
		} else {
			e.log.Info("engine: internet connectivity restored")
		}
	}
	return v
}

// Connectivity reports whether opskest can reach the internet, and
// since when.
func (e *Engine) Connectivity() (online bool, since time.Time) {
	e.connMu.Lock()
	defer e.connMu.Unlock()
	return e.online, e.onlineSince
}

// ---- config changes ---------------------------------------------------------------

// UpsertMonitor installs a created or updated monitor (after the store
// write): a new one starts UNKNOWN and is scheduled with jitter; an
// existing one swaps config, keeps its state, and is re-run right away
// when anything the check depends on changed.
func (e *Engine) UpsertMonitor(m *model.Monitor) {
	c := m.Clone()
	now := e.now()
	sched := e.scheduler()

	var becameVendor bool
	e.mu.Lock()
	en, exists := e.entries[c.ID]
	if !exists {
		en = &entry{id: c.ID, m: c, host: c.Host(), paused: c.Paused, lastLat: -1, pushBase: now}
		en.rt = store.Runtime{MonitorID: c.ID}
		en.under = State{Status: model.StatusUnknown, Since: now, UnderStatus: model.StatusUnknown}
		if !c.Settings.Vendor {
			en.maint = maint.ActiveSet(e.windows, []int64{c.ID}, func(int64) *int64 { return c.GroupID }, now)[c.ID]
		}
		en.disp, en.dispSince = displayed(en), now
		e.setAffected(c.ID, en.disp, c.Settings.Vendor)
		e.entries[c.ID] = en
		e.order = append(e.order, en)
		e.sortDirty = true
		if c.Type == model.TypePush && c.PushToken != "" {
			e.byToken[c.PushToken] = c.ID
		}
		e.mu.Unlock()
		e.dirtyMu.Lock()
		// SQLite may hand a just-deleted id to the new monitor: it must not
		// also be reported as removed in the same delta.
		e.removedIDs = slices.DeleteFunc(e.removedIDs, func(x int64) bool { return x == c.ID })
		e.dirty[c.ID] = struct{}{}
		e.dirtyMu.Unlock()
		if !c.Paused {
			sched.Upsert(c.ID, interval(c))
			if c.Type != model.TypePush {
				// Boot spreads first runs across the whole interval, but a monitor
				// someone has just created must not sit UNKNOWN for up to an
				// interval (a 5-minute monitor: up to 5 minutes): check it soon.
				sched.Reschedule(c.ID, firstCheckDelay(interval(c)))
			}
		}
		return
	}

	en.mu.Lock()
	old := en.m
	en.m, en.host = c, c.Host()
	wasPaused := en.paused
	becameVendor = c.Settings.Vendor && !old.Settings.Vendor
	en.touch()
	en.mu.Unlock()
	if old.PushToken != c.PushToken || old.Type != c.Type {
		if e.byToken[old.PushToken] == c.ID {
			delete(e.byToken, old.PushToken)
		}
		if c.Type == model.TypePush && c.PushToken != "" {
			e.byToken[c.PushToken] = c.ID
		}
	}
	if old.Name != c.Name || !sameGroup(old.GroupID, c.GroupID) {
		e.sortDirty = true
	}
	e.mu.Unlock()
	if becameVendor {
		e.makeVendor(en) // takes en.pub: never while holding Engine.mu
	}
	e.markDirty(c.ID, false)

	if !sameGroup(old.GroupID, c.GroupID) {
		e.tickMaintenance()
	}
	if c.Paused != wasPaused {
		e.SetPaused(c.ID, c.Paused)
		return
	}
	if c.Paused {
		return
	}
	sched.Upsert(c.ID, interval(c))
	if checkChanged(old, c) && c.Type != model.TypePush {
		sched.Reschedule(c.ID, 0)
	}
}

// makeVendor turns an existing monitor into an alert-only 3rd-party feed:
// its open outage is deleted (no Recovered goes out for it), it leaves
// maintenance and the common-cause sets, and its uptime windows are cleared.
func (e *Engine) makeVendor(en *entry) {
	en.pub.Lock()
	defer en.pub.Unlock()
	en.mu.Lock()
	var p pubs
	if o := en.outage; o != nil {
		p.ops = append(p.ops, store.OpDeleteOutage(o.ID))
		p.outages = true
		en.outage = nil
		if o.SuppressedBy == nil && en.under.Status == model.StatusDown {
			en.vendorSince = time.UnixMilli(o.StartedAt) // its Down already went out
		}
	}
	en.win = windows{}
	en.maint = false
	e.refreshDisplay(en, e.now(), "", "", &p)
	e.setAffected(en.id, en.disp, true)
	en.touch()
	en.mu.Unlock()
	e.publish(en.id, &p)
}

// firstCheckDelay is how long after creation a new monitor is first checked:
// a random moment within a few seconds, so importing hundreds of monitors does
// not fire hundreds of checks in the same instant.
func firstCheckDelay(iv time.Duration) time.Duration {
	return time.Duration(rand.Int64N(int64(min(iv, newMonitorFirstCheckWithin))))
}

func sameGroup(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func checkChanged(a, b *model.Monitor) bool {
	return a.Type != b.Type || a.Target != b.Target || a.TimeoutMs != b.TimeoutMs ||
		a.UpsideDown != b.UpsideDown || a.DegradedMs != b.DegradedMs || a.SSLWarnDays != b.SSLWarnDays ||
		!reflect.DeepEqual(a.Settings, b.Settings) || !reflect.DeepEqual(a.Secrets, b.Secrets)
}

// RemoveMonitor drops a monitor before store.DeleteMonitor: it leaves the
// scheduler, its queued writes are tombstoned (Writer.ForgetMonitor), and
// its state is discarded. A check already in flight is dropped by Apply.
func (e *Engine) RemoveMonitor(id int64) {
	e.mu.Lock()
	en := e.entries[id]
	if en != nil {
		delete(e.entries, id)
		if i := slices.Index(e.order, en); i >= 0 {
			e.order = slices.Delete(e.order, i, i+1)
		}
		for tok, tid := range e.byToken {
			if tid == id {
				delete(e.byToken, tok)
			}
		}
	}
	e.mu.Unlock()

	hadOutage := false
	if en != nil {
		en.mu.Lock()
		en.removed = true
		hadOutage = en.outage != nil
		en.mu.Unlock()
		// Wait out an in-flight state change so everything it enqueued
		// sits before the tombstone; any later one sees removed.
		en.pub.Lock()
		en.pub.Unlock()
	}
	e.scheduler().Remove(id)
	if e.w != nil {
		e.w.ForgetMonitor(id)
	}

	e.affMu.Lock()
	delete(e.affected, id)
	delete(e.parentDown, id)
	e.affMu.Unlock()
	e.dirtyMu.Lock()
	delete(e.dirty, id)
	if en != nil {
		e.removedIDs = append(e.removedIDs, id)
	}
	if hadOutage {
		e.dOutages = true
	}
	e.dirtyMu.Unlock()
}

// SetPaused pauses (unschedules, displays PAUSED) or resumes (UNKNOWN,
// rescheduled with jitter) a monitor, recording the status event. The
// caller persists the flag with store.SetPaused.
func (e *Engine) SetPaused(id int64, paused bool) {
	en := e.lookup(id)
	if en == nil {
		return
	}
	now := e.now()
	var p pubs
	en.pub.Lock()
	defer en.pub.Unlock()
	en.mu.Lock()
	if en.removed || en.paused == paused {
		en.mu.Unlock()
		return
	}
	en.paused = paused
	reason := "paused"
	if !paused {
		reason = "resumed"
		en.under = State{Status: model.StatusUnknown, Since: now, UnderStatus: model.StatusUnknown}
		en.firstReason, en.firstClass = "", ""
		en.pushBase = now
	}
	e.refreshDisplay(en, now, reason, "", &p)
	en.touch()
	iv := interval(en.m)
	en.mu.Unlock()
	e.publish(id, &p)

	if paused {
		e.scheduler().Remove(id)
	} else {
		e.scheduler().Upsert(id, iv)
	}
}

// Ack acknowledges an outage: it stops that outage's reminders and shows
// "acked by" everywhere. The store is written first (after a writer flush
// if the outage row is still queued), then memory.
func (e *Engine) Ack(outageID int64, by string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := e.now().UnixMilli()

	var target *entry
	for _, en := range e.snapshot() {
		en.mu.Lock()
		if en.outage != nil && en.outage.ID == outageID {
			target = en
		}
		en.mu.Unlock()
		if target != nil {
			break
		}
	}

	err := e.st.AckOutage(ctx, outageID, by, now)
	if errors.Is(err, store.ErrNotFound) && target != nil && e.w != nil {
		e.w.Flush() // the OpOpenOutage may still be queued
		err = e.st.AckOutage(ctx, outageID, by, now)
	}
	if err != nil {
		return err
	}
	if target == nil {
		return nil
	}
	target.mu.Lock()
	if target.outage != nil && target.outage.ID == outageID {
		o := *target.outage
		who, at := by, now
		o.AckedBy, o.AckedAt = &who, &at
		target.outage = &o
		target.touch()
	}
	target.mu.Unlock()
	e.markDirty(target.id, true)
	return nil
}

// ---- groups & maintenance ------------------------------------------------------------

func (e *Engine) setGroupsLocked(gs []store.Group) {
	e.groups = gs
	e.groupByID = make(map[int64]store.Group, len(gs))
	for _, g := range gs {
		e.groupByID[g.ID] = g
	}
}

// ReloadGroups re-reads the groups (after create/update/delete/reorder).
// Monitors of a deleted group become ungrouped, as the schema's ON DELETE
// SET NULL already made them in the database.
func (e *Engine) ReloadGroups(ctx context.Context) error {
	gs, err := e.st.ListGroups(ctx)
	if err != nil {
		return err
	}
	var orphaned []int64
	e.mu.Lock()
	e.setGroupsLocked(gs)
	for _, en := range e.order {
		if g := en.m.GroupID; g != nil {
			if _, ok := e.groupByID[*g]; !ok {
				en.mu.Lock()
				c := en.m.Clone()
				c.GroupID = nil
				en.m = c
				en.touch()
				en.mu.Unlock()
				orphaned = append(orphaned, en.id)
			}
		}
	}
	e.sortDirty = true
	e.mu.Unlock()

	e.dirtyMu.Lock()
	e.dGroups = true
	for _, id := range orphaned {
		e.dirty[id] = struct{}{}
	}
	e.dirtyMu.Unlock()
	if len(orphaned) > 0 {
		e.tickMaintenance()
	}
	return nil
}

// ReloadMaintenance re-reads the windows (after any maintenance edit) and
// re-evaluates which monitors are in maintenance, emitting Down for any
// that leave maintenance while DOWN.
func (e *Engine) ReloadMaintenance(ctx context.Context) error {
	ws, err := e.st.ListMaintenance(ctx)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.windows = ws
	e.mu.Unlock()
	e.dirtyMu.Lock()
	e.dMaint = true
	e.dirtyMu.Unlock()
	e.tickMaintenance()
	return nil
}

func activeWindowIDs(ws []store.Maintenance, now time.Time) []int64 {
	var ids []int64
	for i := range ws {
		if maint.IsActive(ws[i], now) {
			ids = append(ids, ws[i].ID)
		}
	}
	return ids
}

// tickMaintenance recomputes the maintenance set (every 15 s and on every
// edit). Entering maintenance displays MAINTENANCE; leaving it shows the
// underlying status and, if that is DOWN with nobody told yet, opens the
// outage and fires Down at that moment.
func (e *Engine) tickMaintenance() {
	e.maintMu.Lock()
	defer e.maintMu.Unlock()
	now := e.now()
	e.mu.RLock()
	ws := e.windows
	entries := slices.Clone(e.order)
	ids := make([]int64, 0, len(entries))
	for _, en := range entries {
		if !en.m.Settings.Vendor { // 3rd-party feeds are never put in maintenance
			ids = append(ids, en.id)
		}
	}
	set := maint.ActiveSet(ws, ids, func(id int64) *int64 {
		if en := e.entries[id]; en != nil {
			return en.m.GroupID
		}
		return nil
	}, now)
	e.mu.RUnlock()

	active := activeWindowIDs(ws, now)
	e.dirtyMu.Lock()
	if !slices.Equal(active, e.activeWinID) {
		e.activeWinID = active
		e.dMaint = true
	}
	e.dirtyMu.Unlock()

	for _, en := range entries {
		want := set[en.id]
		en.pub.Lock()
		en.mu.Lock()
		if en.removed || en.maint == want {
			en.mu.Unlock()
			en.pub.Unlock()
			continue
		}
		var p pubs
		en.maint = want
		reason := "maintenance started"
		if !want {
			reason = "maintenance ended"
			if !en.paused && en.under.Status == model.StatusDown && en.outage == nil {
				e.openOutage(en, now, en.reason, en.errClass, en.lastLat, &p)
			}
		}
		e.refreshDisplay(en, now, reason, "", &p)
		en.touch()
		en.mu.Unlock()
		e.publish(en.id, &p)
		en.pub.Unlock()
	}
}

// ActiveMaintenance returns the windows active now.
func (e *Engine) ActiveMaintenance() []store.Maintenance {
	now := e.now()
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := []store.Maintenance{}
	for _, w := range e.windows {
		if maint.IsActive(w, now) {
			out = append(out, w)
		}
	}
	return out
}

// ---- background ---------------------------------------------------------------------

// RunBackground starts the engine's own tickers and returns immediately:
// maintenance every 15 s (which also re-probes connectivity while
// offline), reminders every 30 s, and domain expiry hourly (first pass
// after a minute; each monitor at most daily). Uptime windows need no
// rollover pass (see uptime.go). Everything stops with ctx.
func (e *Engine) RunBackground(ctx context.Context) {
	go func() {
		mt := time.NewTicker(maintenanceEvery)
		rt := time.NewTicker(remindersEvery)
		dt := time.NewTimer(domainFirstRun)
		defer mt.Stop()
		defer rt.Stop()
		defer dt.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-mt.C:
				e.tickMaintenance()
				e.releaseHeldOutages()
				if !e.isOnline() {
					e.checkGuard()
				}
			case <-rt.C:
				e.tickReminders()
			case <-dt.C:
				e.tickDomain(ctx)
				dt.Reset(domainEvery)
			}
		}
	}()
}

// tickReminders fires Reminder every reminder_min while a monitor is DOWN
// with an unacknowledged outage, outside maintenance.
func (e *Engine) tickReminders() {
	now := e.now()
	for _, en := range e.snapshot() {
		en.pub.Lock()
		en.mu.Lock()
		o := en.outage
		var outageID int64
		var started time.Time
		switch {
		case en.removed || en.paused || en.maint || en.under.Status != model.StatusDown || en.m.ReminderMin <= 0:
		case o != nil && o.AckedBy == nil && o.SuppressedBy == nil:
			outageID, started = o.ID, time.UnixMilli(o.StartedAt)
		case o == nil && !en.vendorSince.IsZero(): // 3rd-party feed: alert-only, no outage row
			started = en.vendorSince
		}
		if started.IsZero() {
			en.mu.Unlock()
			en.pub.Unlock()
			continue
		}
		last := en.lastReminder
		if last.IsZero() {
			last = started
		}
		if now.Sub(last) < time.Duration(en.m.ReminderMin)*time.Minute {
			en.mu.Unlock()
			en.pub.Unlock()
			continue
		}
		en.lastReminder = now
		ev := model.Event{
			Kind: model.EvReminder, MonitorID: en.id, At: now, Status: model.StatusDown,
			Reason: en.reason, ErrClass: en.errClass, Since: started, Duration: now.Sub(started),
			LatencyMs: en.lastLat, OutageID: outageID, Code: en.lastCode, Response: en.lastBody,
		}
		en.mu.Unlock()
		if e.sink != nil {
			e.sink.Publish(ev)
		}
		en.pub.Unlock()
	}
}

// tickDomain starts one staggered pass over the http monitors whose domain
// expiry is due (never checked, or checked ≥ 24 h ago): lookups are spread
// evenly across the hour with at most 4 RDAP requests in flight. A pass
// still running when the next tick comes is not doubled up.
func (e *Engine) tickDomain(ctx context.Context) {
	if e.rdap == nil || !e.domainRunning.CompareAndSwap(false, true) {
		return
	}
	now := e.now()
	var due []int64
	for _, en := range e.snapshot() {
		en.mu.Lock()
		m := en.m
		if !en.removed && !en.paused && !en.domainBusy && m.Type == model.TypeHTTP && m.Settings.CheckDomainExpiry &&
			(en.rt.DomainCheckedAt == nil || now.Sub(time.UnixMilli(*en.rt.DomainCheckedAt)) >= domainRecheck) {
			en.domainBusy = true
			due = append(due, en.id)
		}
		en.mu.Unlock()
	}
	if len(due) == 0 {
		e.domainRunning.Store(false)
		return
	}
	go func() {
		defer e.domainRunning.Store(false)
		gap := domainEvery / time.Duration(len(due))
		sem := make(chan struct{}, rdapConcurrency)
		var wg sync.WaitGroup
		defer wg.Wait()
		for i, id := range due {
			if i > 0 {
				t := time.NewTimer(gap)
				select {
				case <-ctx.Done():
					t.Stop()
					e.releaseDomain(due[i:])
					return
				case <-t.C:
				}
			}
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				e.releaseDomain(due[i:])
				return
			}
			wg.Add(1)
			go func(id int64) {
				defer wg.Done()
				defer func() { <-sem }()
				e.checkDomain(ctx, id)
			}(id)
		}
	}()
}

func (e *Engine) releaseDomain(ids []int64) {
	for _, id := range ids {
		if en := e.lookup(id); en != nil {
			en.mu.Lock()
			en.domainBusy = false
			en.mu.Unlock()
		}
	}
}

func (e *Engine) checkDomain(ctx context.Context, id int64) {
	en := e.lookup(id)
	if en == nil {
		return
	}
	en.mu.Lock()
	host := en.host
	en.mu.Unlock()
	if host == "" || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		e.applyDomainResult(id, time.Time{}, check.ErrNoRDAP) // no registrable domain
		return
	}

	cctx, cancel := context.WithTimeout(ctx, rdapTimeout)
	exp, err := e.rdap.Expiry(cctx, host)
	cancel()
	if err != nil && !errors.Is(err, check.ErrNoRDAP) {
		// Transient: leave domain_checked_at alone so the next hourly
		// pass retries.
		logx.Err(e.log, "engine: domain expiry lookup failed", err, "monitor", id)
		e.releaseDomain([]int64{id})
		return
	}
	e.applyDomainResult(id, exp, err)
}

// applyDomainResult records an RDAP answer and fires DomainExpiring once
// per threshold (30/14/7 d) per expiry date. err is nil or check.ErrNoRDAP
// (the TLD has no RDAP server: expiry unknown, never alerts).
func (e *Engine) applyDomainResult(id int64, exp time.Time, err error) {
	en := e.lookup(id)
	if en == nil {
		return
	}
	now := e.now()
	nowMs := now.UnixMilli()
	var p pubs
	en.pub.Lock()
	defer en.pub.Unlock()
	en.mu.Lock()
	en.domainBusy = false
	if en.removed {
		en.mu.Unlock()
		return
	}
	rt := en.rt
	rt.DomainCheckedAt = &nowMs
	if errors.Is(err, check.ErrNoRDAP) {
		rt.DomainExpiresAt, rt.DomainAlertedDays = nil, nil
	} else {
		expMs := exp.UnixMilli()
		if rt.DomainExpiresAt == nil || *rt.DomainExpiresAt != expMs {
			rt.DomainExpiresAt, rt.DomainAlertedDays = &expMs, nil
		}
		days := daysUntil(exp, now)
		if tier, ok := tierFor(domainThresholds, days); ok && (rt.DomainAlertedDays == nil || tier < *rt.DomainAlertedDays) {
			rt.DomainAlertedDays = &tier
			p.evs = append(p.evs, model.Event{
				Kind: model.EvDomainExpiring, MonitorID: id, At: now, Status: en.disp,
				Reason: expiryReason("domain", days), Days: days,
			})
		}
	}
	en.rt = rt
	p.ops = append(p.ops, store.OpUpsertRuntime(rt))
	en.touch()
	en.mu.Unlock()
	e.publish(id, &p)
}

// ---- reads (hub, API, alerts) --------------------------------------------------------

func (e *Engine) markDirty(id int64, outages bool) {
	e.dirtyMu.Lock()
	e.dirty[id] = struct{}{}
	if outages {
		e.dOutages = true
	}
	e.dirtyMu.Unlock()
}

// TakeDirty returns what changed since the last call and resets it. It
// costs O(changed).
func (e *Engine) TakeDirty() Dirty {
	e.dirtyMu.Lock()
	defer e.dirtyMu.Unlock()
	d := Dirty{Removed: e.removedIDs, Groups: e.dGroups, Outages: e.dOutages, Maintenance: e.dMaint, System: e.dSystem}
	if len(e.dirty) > 0 {
		d.Changed = make([]int64, 0, len(e.dirty))
		for id := range e.dirty {
			d.Changed = append(d.Changed, id)
		}
		clear(e.dirty)
	}
	e.removedIDs = nil
	e.dGroups, e.dOutages, e.dMaint, e.dSystem = false, false, false, false
	return d
}

// ensureSorted re-sorts the display order (group sort, group name, then
// monitor name; ungrouped last) when monitors or groups changed.
func (e *Engine) ensureSorted() {
	e.mu.RLock()
	dirty := e.sortDirty
	e.mu.RUnlock()
	if !dirty {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.sortDirty {
		return
	}
	type key struct {
		grouped  bool
		sort     int
		gname    string
		gid      int64
		name     string
		id       int64
		entryPtr *entry
	}
	keys := make([]key, len(e.order))
	for i, en := range e.order {
		k := key{name: strings.ToLower(en.m.Name), id: en.id, entryPtr: en}
		if g := en.m.GroupID; g != nil {
			if grp, ok := e.groupByID[*g]; ok {
				k.grouped, k.sort, k.gname, k.gid = true, grp.Sort, strings.ToLower(grp.Name), grp.ID
			}
		}
		keys[i] = k
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.grouped != b.grouped {
			return a.grouped
		}
		if a.sort != b.sort {
			return a.sort < b.sort
		}
		if a.gname != b.gname {
			return a.gname < b.gname
		}
		if a.gid != b.gid {
			return a.gid < b.gid
		}
		if a.name != b.name {
			return a.name < b.name
		}
		return a.id < b.id
	})
	for i := range keys {
		e.order[i] = keys[i].entryPtr
	}
	e.sortDirty = false
}

// Summaries returns every monitor's summary, in display order. Unchanged
// monitors are served from a per-monitor cache, so the cost is one struct
// copy each.
func (e *Engine) Summaries() []MonitorSummary {
	now := e.now()
	sp := newUptimeSpans(now, e.loc, e.dayBucket)
	e.ensureSorted()
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]MonitorSummary, 0, len(e.order))
	for _, en := range e.order {
		en.mu.Lock()
		out = append(out, en.summaryLocked(now, &sp))
		en.mu.Unlock()
	}
	return out
}

// Summary returns one monitor's summary.
func (e *Engine) Summary(id int64) (MonitorSummary, bool) {
	en := e.lookup(id)
	if en == nil {
		return MonitorSummary{}, false
	}
	now := e.now()
	sp := newUptimeSpans(now, e.loc, e.dayBucket)
	en.mu.Lock()
	defer en.mu.Unlock()
	if en.removed {
		return MonitorSummary{}, false
	}
	return en.summaryLocked(now, &sp), true
}

// Groups returns the groups in display order.
func (e *Engine) Groups() []store.Group {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return append([]store.Group{}, e.groups...)
}

// Monitor returns a clone of the monitor's current config.
func (e *Engine) Monitor(id int64) (*model.Monitor, bool) {
	en := e.lookup(id)
	if en == nil {
		return nil, false
	}
	en.mu.Lock()
	defer en.mu.Unlock()
	if en.removed {
		return nil, false
	}
	c := en.m.Clone()
	c.Paused = en.paused
	return c, true
}

// MonitorIDsInGroup returns the ids of gid's monitors, in display order.
func (e *Engine) MonitorIDsInGroup(gid int64) []int64 {
	e.ensureSorted()
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := []int64{}
	for _, en := range e.order {
		// 3rd-party feeds are alert-only: never part of a group's reports or pages.
		if g := en.m.GroupID; g != nil && *g == gid && !en.m.Settings.Vendor {
			out = append(out, en.id)
		}
	}
	return out
}

// VendorIDs returns which of ids are 3rd-party feeds, in the order given
// (ids the engine does not know are ignored). Handlers use it to reject a
// feed as a status-page component, maintenance target or incident monitor.
func (e *Engine) VendorIDs(ids []int64) []int64 {
	var out []int64
	for _, id := range ids {
		if e.IsVendor(id) {
			out = append(out, id)
		}
	}
	return out
}

// IsVendor reports whether id is a 3rd-party feed (Settings.Vendor): an
// alert-only monitor that is never an outage, uptime, dependency or
// status-page component.
func (e *Engine) IsVendor(id int64) bool {
	e.mu.RLock()
	en := e.entries[id]
	e.mu.RUnlock()
	if en == nil {
		return false
	}
	en.mu.Lock()
	defer en.mu.Unlock()
	return en.m.Settings.Vendor
}

// ActiveOutages returns every open outage, newest first, from memory.
func (e *Engine) ActiveOutages() []store.Outage {
	out := []store.Outage{}
	for _, en := range e.snapshot() {
		en.mu.Lock()
		if en.outage != nil && !en.removed {
			out = append(out, *en.outage)
		}
		en.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt != out[j].StartedAt {
			return out[i].StartedAt > out[j].StartedAt
		}
		return out[i].ID > out[j].ID
	})
	return out
}

// AlertInfo resolves what the alert dispatcher shows for a monitor.
func (e *Engine) AlertInfo(id int64) (AlertInfo, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	en := e.entries[id]
	if en == nil {
		return AlertInfo{}, false
	}
	m := en.m
	info := AlertInfo{Name: m.Name, Target: m.Target, Runbook: m.Runbook, ChannelIDs: slices.Clone(m.ChannelIDs)}
	if g := m.GroupID; g != nil {
		info.Group = e.groupByID[*g].Name
	}
	return info, true
}

// RunNow schedules an immediate check of monitor id (a no-op for unknown
// or paused monitors, and for push monitors, which are never actively
// checked). If a check is already in flight, the monitor runs again as
// soon as it finishes. Reports whether a check was requested.
func (e *Engine) RunNow(id int64) bool {
	m, ok := e.Monitor(id)
	if !ok || m.Paused || m.Type == model.TypePush || m.Type == model.TypeManual {
		return false
	}
	e.scheduler().Reschedule(id, 0)
	return true
}

// Monitors returns a copy of every monitor (with its paused flag), in
// load order.
func (e *Engine) Monitors() []*model.Monitor {
	e.mu.RLock()
	order := append([]*entry(nil), e.order...)
	e.mu.RUnlock()
	out := make([]*model.Monitor, 0, len(order))
	for _, en := range order {
		en.mu.Lock()
		if !en.removed {
			c := en.m.Clone()
			c.Paused = en.paused
			out = append(out, c)
		}
		en.mu.Unlock()
	}
	return out
}
