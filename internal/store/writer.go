package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"opskest/internal/latency"
	"opskest/internal/logx"
	"opskest/internal/model"
)

// defaultQueueCap is the writer's queue channel capacity (§5.5, global
// constraints): large enough that a burst of checks never blocks a
// caller, small enough to bound memory if the writer falls behind.
const defaultQueueCap = 20000

// batchSize is the number of queued items that force an early commit,
// ahead of the 1s ticker.
const batchSize = 500

// shutdownDrainDeadline bounds how long Close waits for the queue to
// drain. The timer is selected directly inside the drain loop (never
// detached into a separate goroutine) so it is guaranteed to fire even if
// the loop is otherwise busy committing.
const shutdownDrainDeadline = 10 * time.Second

// Heartbeat is one check result queued for the writer. Fields documented
// "<0 -> NULL" or `"" -> NULL` are sentinel values the writer maps to a
// SQL NULL in the heartbeats row; there is no separate "not measured" type
// because the hot path must stay allocation-light.
type Heartbeat struct {
	MonitorID int64        `json:"monitorId"`
	TS        int64        `json:"ts"` // unix ms
	Status    model.Status `json:"status"`
	LatencyMs int          `json:"latencyMs"` // <0 -> NULL
	Code      int          `json:"code"`      // 0 -> NULL

	DNSMs  int    `json:"dnsMs"`
	TCPMs  int    `json:"tcpMs"`
	TLSMs  int    `json:"tlsMs"`
	TTFBMs int    `json:"ttfbMs"`
	WaitMs int    `json:"waitMs"` // request sent → first byte (<0 -> NULL)
	AppMs  int    `json:"appMs"`  // app-reported processing time (<0 -> NULL)
	Edge   string `json:"edge"`
	Conn   string `json:"conn"` // "ip · HTTP/2 · TLS 1.3"

	ErrClass string `json:"errClass"` // "" -> NULL
	Msg      string `json:"msg"`      // "" -> NULL

	Maint bool `json:"maint"`

	// NoRollup keeps the beat out of the hourly/daily rollups (uptime and
	// latency history): set for 3rd-party feeds, which are never counted.
	NoRollup bool `json:"-"`
}

// Op is a write operation the writer runs inside its per-second
// transaction, after heartbeat inserts and rollup upserts, in the order
// it was enqueued. Each Op runs inside its own SAVEPOINT (see commitTx):
// an Op that returns an error, or panics, is logged and rolled back to
// that savepoint without discarding the rest of the batch.
type Op func(tx *sql.Tx) error

type queueKind uint8

const (
	queueHeartbeat queueKind = iota
	queueOp
	queueFlush
)

// queueItem is what actually travels through the writer's channel. seq is
// a monotonically increasing sequence number assigned at enqueue time,
// used by ForgetMonitor's tombstones (ruling R10): monitors.id has no
// AUTOINCREMENT, so a deleted id can be reused, and an id-only tombstone
// would then drop a brand-new monitor's heartbeats. Tagging every item
// with a seq lets the writer tell "queued before the forget" apart from
// "queued after it" even though the id is identical. opScoped/opMonitor
// carry the same tombstone check for an Op enqueued via DoFor (ruling
// R18); an Op enqueued via the plain, unscoped Do is never tombstoned.
type queueItem struct {
	kind queueKind
	seq  int64

	hb Heartbeat

	op        Op
	opMonitor int64
	opScoped  bool

	done chan struct{} // queueFlush only
}

// Writer is the single goroutine that owns every write to the database.
// Heartbeats, rollup upserts, outage writes and runtime updates all pass
// through it, batched into one transaction per second (or every 500
// queued items, whichever comes first). See spec §5.5.
type Writer struct {
	s   *Store
	log *slog.Logger
	q   chan queueItem

	seq atomic.Int64

	mu   sync.Mutex
	tomb map[int64]int64 // monitor id -> seq at ForgetMonitor time

	dropped atomic.Uint64
	last    atomic.Pointer[batchStat]

	cancel context.CancelFunc
	done   chan struct{}
}

// NewWriter builds a Writer for s. Call Start to begin committing.
func (s *Store) NewWriter(log *slog.Logger) *Writer {
	return newWriter(s, log, defaultQueueCap)
}

// newWriter is the shared constructor; queueCap is overridable only from
// tests (see history_test.go) so TestWriterNeverBlocks can force the
// queue full without enqueueing 20,000 items.
func newWriter(s *Store, log *slog.Logger, queueCap int) *Writer {
	return &Writer{
		s:    s,
		log:  log,
		q:    make(chan queueItem, queueCap),
		tomb: make(map[int64]int64),
		done: make(chan struct{}),
	}
}

// Start launches the writer's loop. It returns immediately; the loop runs
// until ctx is done, at which point it drains (see Close).
func (w *Writer) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	go w.loop(ctx)
}

// Heartbeat enqueues a check result. It never blocks: when the queue is
// full, the heartbeat is dropped and counted (Dropped).
func (w *Writer) Heartbeat(hb Heartbeat) {
	w.enqueue(queueItem{kind: queueHeartbeat, seq: w.nextSeq(), hb: hb})
}

// Do enqueues a global write op - not tied to any one monitor, and never
// skipped by a tombstone - to run inside the writer's next transaction.
// It never blocks: when the queue is full, the op is dropped and counted.
func (w *Writer) Do(op Op) {
	w.enqueue(queueItem{kind: queueOp, seq: w.nextSeq(), op: op})
}

// DoFor enqueues an op scoped to monitorID, subject to the same seq-based
// tombstone check as Heartbeat (ruling R10/R18): if ForgetMonitor(id) was
// recorded at or after this op was enqueued, the op is skipped rather
// than run against a monitor that is being (or has been) deleted.
// Without this, a queued OpUpsertRuntime for a just-deleted monitor would
// violate monitor_runtime's foreign key (rolling back the op's
// savepoint, but not the reason to prefer skipping it outright), and a
// queued OpOpenOutage would leave a permanent orphan active outage,
// since outages has no FK to monitors to catch it. It never blocks: when
// the queue is full, the op is dropped and counted.
func (w *Writer) DoFor(monitorID int64, op Op) {
	w.enqueue(queueItem{kind: queueOp, seq: w.nextSeq(), op: op, opMonitor: monitorID, opScoped: true})
}

func (w *Writer) nextSeq() int64 {
	return w.seq.Add(1)
}

func (w *Writer) enqueue(item queueItem) {
	select {
	case w.q <- item:
	default:
		w.dropped.Add(1)
	}
}

// Flush blocks until everything enqueued before this call has been
// committed (or the writer has tried and logged a failure). Unlike
// Heartbeat/Do it deliberately blocks on a full queue rather than
// dropping: it is a rare, explicit call from tests and shutdown, not the
// hot path the "never blocks" guarantee protects. It also selects on the
// writer's own shutdown signal (w.done) on both the send and the wait, so
// a Flush call racing a concurrent Close - or made after the writer has
// already stopped - returns instead of blocking forever: drain's final
// cleanup (releaseAbandoned) closes the done channel of any flush marker
// still sitting unprocessed in the queue when the writer gives up.
func (w *Writer) Flush() {
	done := make(chan struct{})
	select {
	case w.q <- queueItem{kind: queueFlush, seq: w.nextSeq(), done: done}:
	case <-w.done:
		return
	}
	select {
	case <-done:
	case <-w.done:
	}
}

// Close stops the writer: it cancels the loop's context, which makes the
// loop drain the queue (committing in batches) until either the queue is
// empty or shutdownDrainDeadline elapses, then returns. Calling Close on
// a writer whose Start was never called returns immediately - there is no
// loop to stop and w.done would otherwise never close.
func (w *Writer) Close() {
	if w.cancel == nil {
		return
	}
	w.cancel()
	<-w.done
}

// Dropped returns the number of heartbeats and ops dropped so far because
// the queue was full.
func (w *Writer) Dropped() uint64 {
	return w.dropped.Load()
}

// LastBatch reports the size and duration of the most recently committed
// batch (heartbeat rows actually written, excluding ones skipped for a
// tombstoned monitor), for tests and diagnostics.
func (w *Writer) LastBatch() (rows int, dur time.Duration) {
	if st := w.last.Load(); st != nil {
		return st.rows, st.dur
	}
	return 0, 0
}

// ForgetMonitor records a tombstone for id at the current sequence
// number, called by callers before DeleteMonitor. Any already-queued (or
// concurrently-racing-in) heartbeat/op with seq <= this value is skipped
// at commit time; anything enqueued afterwards (including for a brand
// new monitor that reused id, since ids are not AUTOINCREMENT) has a
// higher seq and is kept.
func (w *Writer) ForgetMonitor(id int64) {
	w.mu.Lock()
	w.tomb[id] = w.seq.Load()
	w.mu.Unlock()
}

func (w *Writer) loop(ctx context.Context) {
	defer close(w.done)

	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	var batch []queueItem
	for {
		select {
		case item := <-w.q:
			batch = append(batch, item)
			if item.kind == queueFlush || len(batch) >= batchSize {
				w.commit(batch)
				batch = nil
			}
		case <-tick.C:
			if len(batch) > 0 {
				w.commit(batch)
				batch = nil
			}
		case <-ctx.Done():
			w.drain(batch)
			return
		}
	}
}

// drain commits whatever was already batched, then keeps draining the
// queue - committing in batches - until it is empty or
// shutdownDrainDeadline elapses. The deadline timer is a channel selected
// right here in the loop (not a detached goroutine/context), so it is
// guaranteed to actually fire and end the drain.
func (w *Writer) drain(pending []queueItem) {
	// One deadline bounds the whole drain, including the first commit and
	// any Begin blocked on the writer connection: ctx cancels those, and
	// the timer channel ends the loop.
	ctx, cancel := context.WithTimeout(context.Background(), shutdownDrainDeadline)
	defer cancel()

	if len(pending) > 0 {
		w.commitCtx(ctx, pending)
	}

	var batch []queueItem
	for {
		select {
		case item := <-w.q:
			batch = append(batch, item)
			if item.kind == queueFlush || len(batch) >= batchSize {
				w.commitCtx(ctx, batch)
				batch = nil
			}
		case <-ctx.Done():
			w.releaseBatch(batch)
			w.releaseAbandoned()
			return
		default:
			if len(batch) > 0 {
				w.commitCtx(ctx, batch)
				batch = nil
			}
			if len(w.q) == 0 {
				w.releaseAbandoned()
				return
			}
		}
	}
}

// releaseBatch closes flush markers in a batch that will not be committed
// (the drain deadline passed first) and counts its other items as dropped.
func (w *Writer) releaseBatch(batch []queueItem) {
	for _, item := range batch {
		if item.kind == queueFlush && item.done != nil {
			close(item.done)
			continue
		}
		w.dropped.Add(1)
	}
}

// releaseAbandoned drains whatever is left in the queue without
// committing it - the drain deadline passed, or a last item slipped in
// right as the queue emptied - and releases (closes) the done channel of
// any flush marker found there. Without this, a concurrent Flush call
// whose marker never got processed before the writer stopped would block
// forever; Flush's own select on w.done is the other half of that
// guarantee.
func (w *Writer) releaseAbandoned() {
	for {
		select {
		case item := <-w.q:
			if item.kind == queueFlush && item.done != nil {
				close(item.done)
			}
		default:
			return
		}
	}
}

// commit runs one transaction for batch: heartbeat inserts, then rollup
// upserts, then the queued Ops - each Op in its own SAVEPOINT, so one
// failing or panicking Op cannot discard the rest of the batch. A
// heartbeat-insert or rollup-upsert failure is fatal to the whole
// transaction (logged, batch dropped, never retried); an Op's own
// failure or panic is contained to that Op alone (logged, rolled back to
// its savepoint) and the transaction continues. Never panics the
// process.
func (w *Writer) commit(batch []queueItem) {
	w.commitCtx(context.Background(), batch)
}

// commitCtx is commit bounded by ctx: during shutdown drain, ctx carries
// the drain deadline so a Begin blocked on a busy writer connection (or a
// slow transaction) is abandoned when the deadline passes.
func (w *Writer) commitCtx(ctx context.Context, batch []queueItem) {
	if len(batch) == 0 {
		return
	}

	start := time.Now()
	maxSeq := int64(0)
	for _, item := range batch {
		if item.seq > maxSeq {
			maxSeq = item.seq
		}
	}

	rows, err := w.commitTx(ctx, batch)
	dur := time.Since(start)
	// Record only committed batches, as one consistent (rows, dur) pair; a
	// flush-only (zero-row) batch doesn't overwrite the last real one.
	if err == nil && rows > 0 {
		w.last.Store(&batchStat{rows: rows, dur: dur})
	}
	if err != nil {
		logx.Err(w.log, "store: writer commit failed", err)
	}

	for _, item := range batch {
		if item.kind == queueFlush && item.done != nil {
			close(item.done)
		}
	}

	// Tombstones are pure memory hygiene: correctness never depends on
	// pruning, since seq only increases, so a future heartbeat for a
	// reused id always has seq > tomb[id] regardless of when (or
	// whether) the entry is removed. We only remove entries we know we
	// have now fully processed, successfully or not - a dropped batch's
	// items are gone either way and will never be retried.
	if maxSeq > 0 {
		w.mu.Lock()
		for id, seq := range w.tomb {
			if seq <= maxSeq {
				delete(w.tomb, id)
			}
		}
		w.mu.Unlock()
	}
}

type rollupKey struct {
	monitorID int64
	bucket    int64
}

// rollupDelta accumulates one commit's worth of changes for one
// (monitor, bucket), in a form ready to bind into the upsert statement:
// latMin/latMax are nil when no latency sample landed in this bucket
// during this commit, which must bind as SQL NULL so the ON CONFLICT
// COALESCE formula leaves the stored min/max alone.
type rollupDelta struct {
	up, degraded, pending, down, unknown, maint int64
	latSum, latCount                            int64
	latMin, latMax                              *int
	hist                                        latency.Hist
}

func (d *rollupDelta) addLatency(ms int) {
	if d.hist == nil {
		d.hist = latency.Hist{}
	}
	d.hist.Add(ms)
	d.latSum += int64(ms)
	d.latCount++
	if d.latMin == nil || ms < *d.latMin {
		v := ms
		d.latMin = &v
	}
	if d.latMax == nil || ms > *d.latMax {
		v := ms
		d.latMax = &v
	}
}

func (w *Writer) commitTx(ctx context.Context, batch []queueItem) (rows int, err error) {
	tx, err := w.s.W.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// The tombstone snapshot is taken here, after Begin has actually
	// acquired the (single) writer connection - not earlier in commit(),
	// before Begin. s.W allows exactly one connection, and database/sql
	// hands a freed connection to whichever waiter it likes, not
	// necessarily in call order: if the snapshot were taken before
	// Begin, a concurrent ForgetMonitor+DeleteMonitor could have its
	// DeleteMonitor transaction win the connection and purge a monitor's
	// rows *after* our stale snapshot was already taken but *before* our
	// Begin() actually ran, so we would then insert that monitor's
	// queued heartbeats straight back in - a permanent orphan (and, if
	// the id is later reused, silently misattributed to the new
	// monitor). Since ForgetMonitor always happens-before its caller's
	// DeleteMonitor call in the same goroutine, and DeleteMonitor cannot
	// even start its transaction until it also acquires this same
	// connection, reading the tombstone map only once we hold the
	// connection guarantees we see every tombstone whose DeleteMonitor
	// has already committed by this point.
	w.mu.Lock()
	tomb := make(map[int64]int64, len(w.tomb))
	for id, seq := range w.tomb {
		tomb[id] = seq
	}
	w.mu.Unlock()

	hbStmt, err := tx.Prepare(`INSERT OR IGNORE INTO heartbeats(
		monitor_id, ts, status, latency_ms, code, dns_ms, tcp_ms, tls_ms, ttfb_ms, err_class, msg, maint, wait_ms, app_ms, edge, conn
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer hbStmt.Close()

	hourly := map[rollupKey]*rollupDelta{}
	daily := map[rollupKey]*rollupDelta{}
	var ops []Op

	for _, item := range batch {
		switch item.kind {
		case queueHeartbeat:
			hb := item.hb
			if tombSeq, tombed := tomb[hb.MonitorID]; tombed && item.seq <= tombSeq {
				continue
			}

			res, execErr := hbStmt.Exec(
				hb.MonitorID, hb.TS, int(hb.Status),
				negOrNil(hb.LatencyMs), zeroOrNil(hb.Code),
				negOrNil(hb.DNSMs), negOrNil(hb.TCPMs), negOrNil(hb.TLSMs), negOrNil(hb.TTFBMs),
				nullString(hb.ErrClass), nullString(hb.Msg), boolToInt(hb.Maint),
				negOrNil(hb.WaitMs), negOrNil(hb.AppMs), nullString(hb.Edge), nullString(hb.Conn),
			)
			if execErr != nil {
				return rows, execErr
			}
			n, _ := res.RowsAffected()
			rows += int(n)
			if n == 0 {
				// Same-ms duplicate: INSERT OR IGNORE left the existing
				// row untouched, so this heartbeat must not also double
				// its rollup delta.
				continue
			}

			if !hb.NoRollup {
				addRollupDelta(hourly, hb, HourBucket(hb.TS))
				addRollupDelta(daily, hb, w.s.DayBucket(hb.TS))
			}

		case queueOp:
			if item.opScoped {
				if tombSeq, tombed := tomb[item.opMonitor]; tombed && item.seq <= tombSeq {
					continue
				}
			}
			ops = append(ops, item.op)
		}
	}

	if err := upsertRollups(tx, "rollup_hourly", hourly); err != nil {
		return rows, err
	}
	if err := upsertRollups(tx, "rollup_daily", daily); err != nil {
		return rows, err
	}

	for _, op := range ops {
		w.runOp(tx, op)
	}

	if err := tx.Commit(); err != nil {
		return rows, err
	}
	return rows, nil
}

// runOp runs op inside its own SAVEPOINT, so a failing or panicking op
// never discards the rest of the batch's heartbeats, rollups or other
// ops: on error - including a recovered panic - it is logged via
// logx.Err (the error text only, never the op's payload) and rolled back
// to the savepoint; the outer transaction and every other queued write
// continues normally.
func (w *Writer) runOp(tx *sql.Tx, op Op) {
	if op == nil {
		return
	}

	if _, err := tx.Exec(`SAVEPOINT op`); err != nil {
		logx.Err(w.log, "store: writer savepoint failed", err)
		return
	}

	if err := runOpRecovered(op, tx); err != nil {
		logx.Err(w.log, "store: writer op failed", err)
		if _, err := tx.Exec(`ROLLBACK TO op`); err != nil {
			logx.Err(w.log, "store: writer rollback to savepoint failed", err)
		}
	}

	if _, err := tx.Exec(`RELEASE op`); err != nil {
		logx.Err(w.log, "store: writer release savepoint failed", err)
	}
}

// runOpRecovered calls op, converting a panic into an error so one
// misbehaving op can never take down the writer goroutine or the
// process.
func runOpRecovered(op Op, tx *sql.Tx) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("store: op panicked: %v", r)
		}
	}()
	return op(tx)
}

// addRollupDelta folds one heartbeat into the in-memory delta for
// (hb.MonitorID, bucket). A maintenance beat counts only in n_maint and
// contributes no latency, regardless of its status column.
func addRollupDelta(m map[rollupKey]*rollupDelta, hb Heartbeat, bucket int64) {
	key := rollupKey{monitorID: hb.MonitorID, bucket: bucket}
	d := m[key]
	if d == nil {
		d = &rollupDelta{}
		m[key] = d
	}

	if hb.Maint || hb.Status == model.StatusMaintenance {
		d.maint++
		return
	}
	if hb.Status == model.StatusPaused {
		return // paused monitors aren't checked; never count toward uptime
	}

	switch hb.Status {
	case model.StatusUp:
		d.up++
	case model.StatusDegraded:
		d.degraded++
	case model.StatusPending:
		d.pending++
	case model.StatusDown:
		d.down++
	default:
		d.unknown++
	}

	if hb.LatencyMs >= 0 {
		d.addLatency(hb.LatencyMs)
	}
}

// upsertRollups applies every (monitor, bucket) delta in m to table
// ("rollup_hourly" or "rollup_daily" - both fixed, package-chosen
// strings, never user input) with the ON CONFLICT accumulation formula
// from the spec.
func upsertRollups(tx *sql.Tx, table string, m map[rollupKey]*rollupDelta) error {
	if len(m) == 0 {
		return nil
	}

	stmt, err := tx.Prepare(`INSERT INTO ` + table + `(
		monitor_id, bucket, n_up, n_degraded, n_pending, n_down, n_unknown, n_maint,
		lat_sum, lat_count, lat_min, lat_max, lat_hist
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(monitor_id, bucket) DO UPDATE SET
		n_up=n_up+excluded.n_up, n_degraded=n_degraded+excluded.n_degraded, n_pending=n_pending+excluded.n_pending,
		n_down=n_down+excluded.n_down, n_unknown=n_unknown+excluded.n_unknown, n_maint=n_maint+excluded.n_maint,
		lat_sum=lat_sum+excluded.lat_sum, lat_count=lat_count+excluded.lat_count,
		lat_min=MIN(COALESCE(lat_min,excluded.lat_min),COALESCE(excluded.lat_min,lat_min)),
		lat_max=MAX(COALESCE(lat_max,excluded.lat_max),COALESCE(excluded.lat_max,lat_max)),
		lat_hist=opskest_hist_merge(lat_hist,excluded.lat_hist)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for key, d := range m {
		var latMin, latMax any
		if d.latMin != nil {
			latMin = *d.latMin
		}
		if d.latMax != nil {
			latMax = *d.latMax
		}
		var hist any
		if b := d.hist.Encode(); b != nil {
			hist = b
		}
		if _, err := stmt.Exec(
			key.monitorID, key.bucket, d.up, d.degraded, d.pending, d.down, d.unknown, d.maint,
			d.latSum, d.latCount, latMin, latMax, hist,
		); err != nil {
			return err
		}
	}
	return nil
}

// negOrNil binds a negative sentinel as SQL NULL.
func negOrNil(v int) any {
	if v < 0 {
		return nil
	}
	return v
}

// zeroOrNil binds a zero sentinel as SQL NULL (used for Heartbeat.Code,
// where 0 means "no HTTP status was observed").
func zeroOrNil(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

// batchStat is one committed batch's size and duration, stored as a pair
// so LastBatch never returns the rows of one batch with another's time.
type batchStat struct {
	rows int
	dur  time.Duration
}
