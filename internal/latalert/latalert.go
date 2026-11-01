// Package latalert evaluates response-time alerts (model.LatencyAlert):
// every minute, for each monitor with one configured, it takes the chosen
// percentile of the successful checks in the window (from the engine's
// in-memory ring) and compares it with the limit — an absolute ms value,
// a multiple of the monitor's usual value (the same percentile over the
// last 7 days of hourly histograms), or the lower of the two.
//
// It fires EvLatencyHigh once when the value goes over the limit and
// EvLatencyCleared once it drops below 90 % of it (hysteresis, so a value
// hovering at the limit doesn't flap). Paused, maintenance and down
// monitors aren't evaluated and drop any firing state silently: a down
// monitor already has its own alert. State is in memory, so a monitor
// still slow after a restart alerts once more.
package latalert

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"time"

	"opskest/internal/engine"
	"opskest/internal/latency"
	"opskest/internal/logx"
	"opskest/internal/model"
	"opskest/internal/store"
)

const (
	Every          = time.Minute
	minSamples     = 5
	baselineSpan   = 7 * 24 * time.Hour
	baselineTTL    = time.Hour
	minBaselineN   = 100
	clearRatio     = 0.9
	maxRefreshTick = 200 // baseline queries per tick, so a restart doesn't burst 2,000
)

// Source is what the evaluator reads from the engine.
type Source interface {
	LatencySamples(now time.Time) []engine.LatencySample
}

// Rollups is what the evaluator reads from the store (the baseline).
type Rollups interface {
	RollupSeries(ctx context.Context, table string, monitorID, from, to int64) ([]store.Rollup, error)
}

// Sink receives the alert events (the alert dispatcher).
type Sink interface {
	Publish(ev model.Event)
}

type baseline struct {
	at    time.Time
	pct   int
	value int
	ok    bool
}

type state struct {
	since time.Time
	limit int
}

// Evaluator holds per-monitor firing state and baseline cache. Not safe
// for concurrent Tick calls; Run calls it from one goroutine.
type Evaluator struct {
	src   Source
	roll  Rollups
	sink  Sink
	now   func() time.Time
	log   *slog.Logger
	base  map[int64]baseline
	state map[int64]*state
}

func New(src Source, roll Rollups, sink Sink, now func() time.Time, log *slog.Logger) *Evaluator {
	return &Evaluator{src: src, roll: roll, sink: sink, now: now, log: log,
		base: map[int64]baseline{}, state: map[int64]*state{}}
}

// Run ticks every minute until ctx ends.
func (ev *Evaluator) Run(ctx context.Context) {
	t := time.NewTicker(Every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ev.Tick(ctx)
		}
	}
}

// Tick runs one evaluation pass.
func (ev *Evaluator) Tick(ctx context.Context) {
	now := ev.now()
	samples := ev.src.LatencySamples(now)
	seen := make(map[int64]bool, len(samples))
	refreshed := 0
	for _, s := range samples {
		seen[s.ID] = true
		if s.Status == model.StatusPaused || s.Status == model.StatusMaintenance || s.Status == model.StatusDown {
			delete(ev.state, s.ID)
			continue
		}
		if len(s.Values) < minSamples {
			continue
		}
		value := percentile(s.Values, s.Cfg.Percentile)

		limit, usual := 0, 0
		if s.Cfg.ThresholdMs > 0 {
			limit = s.Cfg.ThresholdMs
		}
		if s.Cfg.Factor > 0 {
			b := ev.base[s.ID]
			if (b.at.IsZero() || now.Sub(b.at) >= baselineTTL || b.pct != s.Cfg.Percentile) && refreshed < maxRefreshTick {
				refreshed++
				b = ev.loadBaseline(ctx, s.ID, s.Cfg.Percentile, now)
				ev.base[s.ID] = b
			}
			if b.ok {
				usual = b.value
				rel := int(math.Round(s.Cfg.Factor * float64(b.value)))
				if limit == 0 || rel < limit {
					limit = rel
				}
			}
		}
		if limit <= 0 {
			continue // relative-only and no baseline yet
		}

		st := ev.state[s.ID]
		switch {
		case st == nil && value > limit:
			ev.state[s.ID] = &state{since: now, limit: limit}
			ev.publish(model.EvLatencyHigh, s, now, value, limit, usual, now)
		case st != nil && float64(value) < clearRatio*float64(st.limit):
			delete(ev.state, s.ID)
			ev.publish(model.EvLatencyCleared, s, now, value, st.limit, usual, st.since)
		}
	}
	// Alert removed, or monitor deleted: forget it quietly.
	for id := range ev.state {
		if !seen[id] {
			delete(ev.state, id)
		}
	}
	for id := range ev.base {
		if !seen[id] {
			delete(ev.base, id)
		}
	}
}

// Firing reports whether monitor id currently has a response-time alert out.
func (ev *Evaluator) Firing(id int64) bool { return ev.state[id] != nil }

func (ev *Evaluator) publish(kind model.EventKind, s engine.LatencySample, now time.Time, value, limit, usual int, since time.Time) {
	reason := fmt.Sprintf("p%d %s over %d min — limit %s", s.Cfg.Percentile, fmtMs(value), s.Cfg.WindowMin, fmtMs(limit))
	if usual > 0 {
		reason += fmt.Sprintf(" (usual %s)", fmtMs(usual))
	}
	status := model.StatusDegraded
	if kind == model.EvLatencyCleared {
		status = model.StatusUp
		reason = fmt.Sprintf("p%d %s over %d min, back under %s after %s",
			s.Cfg.Percentile, fmtMs(value), s.Cfg.WindowMin, fmtMs(limit), now.Sub(since).Round(time.Minute))
	}
	// Duration and LatencyMs stay zero: channels render those as "down
	// for …" / "N ms", and the reason already says everything.
	ev.sink.Publish(model.Event{
		Kind: kind, MonitorID: s.ID, At: now, Status: status, Reason: reason, Since: since,
	})
}

func (ev *Evaluator) loadBaseline(ctx context.Context, id int64, pct int, now time.Time) baseline {
	hour := store.HourBucket(now.UnixMilli())
	rows, err := ev.roll.RollupSeries(ctx, "rollup_hourly", id, now.Add(-baselineSpan).UnixMilli(), hour-1)
	if err != nil {
		logx.Err(ev.log, "latency alert baseline", err)
		return baseline{at: now, pct: pct}
	}
	h := latency.Hist{}
	minV, maxV, n := 0, 0, int64(0)
	for _, r := range rows {
		if r.LatCount == 0 {
			continue
		}
		if n == 0 || r.LatMin < minV {
			minV = r.LatMin
		}
		if n == 0 || r.LatMax > maxV {
			maxV = r.LatMax
		}
		n += r.LatCount
		h.Merge(r.Histogram())
	}
	if h.Count() < minBaselineN {
		return baseline{at: now, pct: pct}
	}
	v, ok := h.Quantile(float64(pct)/100, minV, maxV)
	return baseline{at: now, pct: pct, value: v, ok: ok && v > 0}
}

// percentile is the nearest-rank p-th percentile of values.
func percentile(values []int, p int) int {
	v := append([]int(nil), values...)
	sort.Ints(v)
	idx := int(math.Ceil(float64(p)/100*float64(len(v)))) - 1
	if idx < 0 {
		idx = 0
	}
	return v[idx]
}

func fmtMs(ms int) string {
	if ms >= 1000 {
		return fmt.Sprintf("%.2f s", float64(ms)/1000)
	}
	return fmt.Sprintf("%d ms", ms)
}
