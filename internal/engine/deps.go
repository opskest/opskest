package engine

import (
	"fmt"
	"time"

	"opskest/internal/model"
	"opskest/internal/store"
)

// Monitor dependencies (Settings.DependsOn): an outage that opens while a
// parent is down is recorded with SuppressedBy and sends no alerts. This
// pass, run with the maintenance tick, releases such outages once every
// parent is back: the child is still down on its own, so its Down goes
// out now. A held outage that closes first never alerts at all.
func (e *Engine) releaseHeldOutages() {
	now := e.now()
	for _, en := range e.snapshot() {
		en.pub.Lock()
		en.mu.Lock()
		o := en.outage
		// Paused or in maintenance: keep it held, so the Down goes out once
		// the child is back and still down (releasing now would lose it —
		// nothing re-opens an outage that's already open).
		if en.removed || en.paused || en.maint || o == nil || o.SuppressedBy == nil || e.downParent(en.m.Settings.DependsOn) != nil {
			en.mu.Unlock()
			en.pub.Unlock()
			continue
		}
		released := *o
		released.SuppressedBy = nil
		en.outage = &released
		en.lastReminder = now // reminders count from this Down, not from the (held) start
		en.touch()
		var p pubs
		p.ops = append(p.ops, store.OpSetOutageSuppressed(o.ID, nil))
		p.outages = true
		if en.under.Status == model.StatusDown {
			p.evs = append(p.evs, model.Event{
				Kind: model.EvDown, MonitorID: en.id, At: now, Status: model.StatusDown,
				Reason:   fmt.Sprintf("still down after its parent recovered: %s", en.reason),
				ErrClass: en.errClass, Since: time.UnixMilli(o.StartedAt), LatencyMs: en.lastLat,
				OutageID: o.ID, Code: en.lastCode, Response: en.lastBody,
			})
		}
		en.mu.Unlock()
		e.publish(en.id, &p)
		en.pub.Unlock()
	}
}

// ValidateDependencies checks parents against every monitor the engine
// knows: each must exist, and adding id → parents must not close a cycle
// (id 0 = a monitor not created yet, which nothing can depend on). It
// returns a user-facing message, or "" when valid.
func (e *Engine) ValidateDependencies(id int64, parents []int64) string {
	if len(parents) == 0 {
		return ""
	}
	e.mu.RLock()
	graph := make(map[int64][]int64, len(e.entries))
	for mid, en := range e.entries {
		graph[mid] = en.m.Settings.DependsOn // m is replaced, never mutated; Engine.mu suffices to read it
	}
	e.mu.RUnlock()

	for _, p := range parents {
		if _, ok := graph[p]; !ok {
			return fmt.Sprintf("monitor %d doesn't exist", p)
		}
		if e.IsVendor(p) {
			return fmt.Sprintf("monitor %d is a 3rd-party feed, which cannot be a dependency", p)
		}
	}
	if id == 0 {
		return ""
	}
	graph[id] = parents
	// Walk up from each parent: reaching id again means a cycle.
	seen := map[int64]bool{}
	stack := append([]int64(nil), parents...)
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == id {
			return "these parents would make a dependency loop"
		}
		if seen[cur] {
			continue
		}
		seen[cur] = true
		stack = append(stack, graph[cur]...)
	}
	return ""
}
