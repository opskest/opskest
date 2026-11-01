package engine

import (
	"fmt"
	"strings"

	"opskest/internal/model"
)

// Group monitors (model.TypeGroup) have no check of their own: every
// interval the engine reads the displayed status of their children and folds
// it into one result. A child that is paused, in maintenance or not yet
// checked (unknown) is left out, so a deliberate pause never takes a group
// down. A pending child (failing, still inside its retries) counts as up: the
// group follows the confirmed state of its children, not their doubts.
//
// Reading a child takes only that child's own lock, one at a time and never
// while holding the group's, so groups that contain groups cannot deadlock.

// maxGroupNames is how many failing child names a group's reason lists.
const maxGroupNames = 3

// groupResult evaluates m (a group monitor) against its children's current
// state. At is left for the caller.
func (e *Engine) groupResult(m *model.Monitor) model.Result {
	var up, down, degraded, skipped int
	var downNames, degradedNames []string
	for _, id := range m.Settings.Children {
		en := e.lookup(id)
		if en == nil {
			skipped++
			continue
		}
		en.mu.Lock()
		st, name, vendor := en.disp, en.m.Name, en.m.Settings.Vendor
		en.mu.Unlock()
		if vendor { // a 3rd-party feed is not part of your own service
			skipped++
			continue
		}
		switch st {
		case model.StatusDown:
			down++
			downNames = append(downNames, name)
		case model.StatusDegraded:
			up++
			degraded++
			degradedNames = append(degradedNames, name)
		case model.StatusUp, model.StatusPending:
			up++
		default: // paused, maintenance, unknown
			skipped++
		}
	}

	res := model.Result{OK: true, LatencyMs: -1}
	total := up + down
	if total == 0 {
		return res // nothing to judge: no news is not bad news
	}
	ok := true
	switch m.Settings.GroupMode {
	case "any":
		ok = up > 0
	case "min":
		ok = up >= m.Settings.GroupMin
	default: // all
		ok = down == 0
	}
	if !ok {
		res.OK, res.ErrClass = false, model.ErrOther
		switch m.Settings.GroupMode {
		case "any":
			res.Reason = fmt.Sprintf("all %d monitors are down: %s", down, nameList(downNames))
		case "min":
			res.Reason = fmt.Sprintf("%d of %d monitors up, at least %d needed; down: %s", up, total, m.Settings.GroupMin, nameList(downNames))
		default:
			res.Reason = fmt.Sprintf("%d of %d monitors down: %s", down, total, nameList(downNames))
		}
		res.Reason = model.TrimReason(res.Reason)
		return res
	}
	if degraded > 0 || down > 0 {
		res.Degraded = true
		switch {
		case down > 0 && degraded > 0:
			res.Reason = fmt.Sprintf("%d down, %d degraded (still within the group's limit)", down, degraded)
		case down > 0:
			res.Reason = fmt.Sprintf("%d of %d monitors down (still within the group's limit): %s", down, total, nameList(downNames))
		default:
			res.Reason = fmt.Sprintf("%d monitors degraded: %s", degraded, nameList(degradedNames))
		}
		res.Reason = model.TrimReason(res.Reason)
	}
	return res
}

func nameList(names []string) string {
	if len(names) <= maxGroupNames {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:maxGroupNames], ", "), len(names)-maxGroupNames)
}

// EvaluateGroup reports what a group monitor's result would be right now, for
// the monitor form's "test" button.
func (e *Engine) EvaluateGroup(m *model.Monitor) model.Result {
	r := e.groupResult(m)
	r.At = e.now()
	return r
}

// ValidateGroup checks a group's children against every monitor the engine
// knows: each must exist, and the group must not contain itself through
// another group. It returns a user-facing message, or "" when valid.
func (e *Engine) ValidateGroup(id int64, children []int64) string {
	if len(children) == 0 {
		return ""
	}
	e.mu.RLock()
	graph := make(map[int64][]int64, len(e.entries))
	for mid, en := range e.entries {
		if en.m.Type == model.TypeGroup {
			graph[mid] = en.m.Settings.Children // m is replaced, never mutated; Engine.mu suffices to read it
		} else {
			graph[mid] = nil
		}
	}
	e.mu.RUnlock()

	for _, c := range children {
		if _, ok := graph[c]; !ok {
			return fmt.Sprintf("monitor %d doesn't exist", c)
		}
		if e.IsVendor(c) {
			return fmt.Sprintf("monitor %d is a 3rd-party feed, which cannot be part of a group monitor", c)
		}
	}
	if id == 0 {
		return ""
	}
	seen := map[int64]bool{}
	stack := append([]int64(nil), children...)
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == id {
			return "these children would make a group contain itself"
		}
		if seen[cur] {
			continue
		}
		seen[cur] = true
		stack = append(stack, graph[cur]...)
	}
	return ""
}

// ── manual monitors ───────────────────────────────────────────────

// manualResult re-asserts a manual monitor's current status, so its beats
// and uptime keep accruing between changes. A manual monitor starts up.
func manualResult(reason string, st model.Status) model.Result {
	switch st {
	case model.StatusDown, model.StatusPending:
		if reason == "" {
			reason = "set down manually"
		}
		return model.Result{OK: false, LatencyMs: -1, ErrClass: model.ErrOther, Reason: model.TrimReason(reason)}
	case model.StatusDegraded:
		if reason == "" {
			reason = "set degraded manually"
		}
		return model.Result{OK: true, Degraded: true, LatencyMs: -1, Reason: model.TrimReason(reason)}
	}
	return model.Result{OK: true, LatencyMs: -1}
}

// SetManual sets a manual monitor's status to up, degraded or down, with an
// optional reason (shown on the monitor and in the alert). It reports false
// for an unknown id, a monitor that is not manual, or a paused one.
func (e *Engine) SetManual(id int64, status, reason string) bool {
	en := e.lookup(id)
	if en == nil {
		return false
	}
	en.mu.Lock()
	ok := !en.removed && !en.paused && en.m.Type == model.TypeManual
	en.mu.Unlock()
	if !ok {
		return false
	}
	var st model.Status
	switch status {
	case "up":
		st = model.StatusUp
	case "degraded":
		st = model.StatusDegraded
	case "down":
		st = model.StatusDown
	default:
		return false
	}
	r := manualResult(strings.TrimSpace(reason), st)
	r.At = e.now()
	e.Apply(id, r)
	return true
}
