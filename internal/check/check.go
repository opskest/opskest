// Package check runs the actual endpoint checks for every monitor type:
// building the request, timing each phase, evaluating assertions, and
// producing a model.Result with a clear, bounded failure reason.
package check

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"opskest/internal/model"
)

// Version is the opskest version string sent in the User-Agent header.
// main sets this at build/startup time; it defaults to "dev".
var Version = "dev"

// Checker performs one check for a monitor type. Implementations do not
// stamp Result.At or apply degradation; Run does that.
type Checker interface {
	Check(ctx context.Context, m *model.Monitor) model.Result
}

var (
	registryMu sync.RWMutex
	registry   = map[model.MonitorType]Checker{}
)

// Register associates a Checker with a monitor type. Checkers call this
// from their own init().
func Register(t model.MonitorType, c Checker) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[t] = c
}

func lookup(t model.MonitorType) (Checker, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	c, ok := registry[t]
	return c, ok
}

// Run applies m.TimeoutMs via context, dispatches to the checker
// registered for m.Type, stamps At using now, then applies the shared
// degradation rules (slow latency, near-expiry certificate). Degradation
// is only applied when the check succeeded. Run does not invert OK for
// upside-down monitors; the engine does that.
//
// Push monitors are never actively checked: Run returns a fixed, failing
// Result for them without consulting the registry.
func Run(ctx context.Context, m *model.Monitor, now func() time.Time) model.Result {
	if m.Type == model.TypePush {
		return model.Result{
			At:     now(),
			OK:     false,
			Reason: model.TrimReason("push monitors are not actively checked"),
		}
	}

	c, ok := lookup(m.Type)
	if !ok {
		return model.Result{
			At:       now(),
			OK:       false,
			ErrClass: model.ErrOther,
			Reason:   model.TrimReason(fmt.Sprintf("no checker registered for monitor type %q", m.Type)),
		}
	}

	timeout := time.Duration(m.TimeoutMs) * time.Millisecond
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	res := c.Check(cctx, m)
	res.At = now()

	if res.OK {
		applyDegradation(&res, m)
	}

	return res
}

// applyDegradation marks res as Degraded, with a matching reason, when the
// latency exceeds m.DegradedMs (if set), when res.Cert expires within
// m.SSLWarnDays, or when res.Cert has already expired. When more than one
// condition holds, their reasons are composed in that order, joined by
// "; ", rather than one silently overwriting the other. Ping loss
// degradation is handled by the ping checker itself, since only it knows
// LossPct semantics.
func applyDegradation(res *model.Result, m *model.Monitor) {
	var reasons []string

	// Status page and 3rd party vendor monitors degrade ONLY on reported vendor incidents,
	// never because of HTTP latency to fetch their status website!
	if !m.Settings.Vendor && m.DegradedMs > 0 && res.LatencyMs > m.DegradedMs {
		res.Degraded = true
		reasons = append(reasons, fmt.Sprintf("slow: %d ms > %d ms", res.LatencyMs, m.DegradedMs))
	}

	if res.Cert != nil && m.SSLWarnDays > 0 {
		days := int(res.Cert.NotAfter.Sub(res.At) / (24 * time.Hour))
		switch {
		case days < 0:
			res.Degraded = true
			reasons = append(reasons, fmt.Sprintf("certificate expired %d days ago", -days))
		case days <= m.SSLWarnDays:
			res.Degraded = true
			reasons = append(reasons, fmt.Sprintf("certificate expires in %d days", days))
		}
	}

	if len(reasons) > 0 {
		res.Reason = model.TrimReason(strings.Join(reasons, "; "))
	}
}
