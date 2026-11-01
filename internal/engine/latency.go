package engine

import (
	"time"

	"opskest/internal/model"
)

// LatencySample is one monitor's input to the response-time alert
// evaluator: its alert config, displayed status and the latencies of its
// successful checks inside the config's window.
type LatencySample struct {
	ID     int64
	Name   string
	Cfg    model.LatencyAlert
	Status model.Status // displayed: paused/maintenance win over the check status
	Values []int        // ms, oldest first; successful (up/degraded) beats only
}

// LatencySamples returns a sample for every monitor with a response-time
// alert configured, read from the in-memory ring (no database access).
func (e *Engine) LatencySamples(now time.Time) []LatencySample {
	var out []LatencySample
	for _, en := range e.snapshot() {
		en.mu.Lock()
		if en.removed || en.m.Settings.LatencyAlert == nil {
			en.mu.Unlock()
			continue
		}
		cfg := *en.m.Settings.LatencyAlert
		since := now.Add(-time.Duration(cfg.WindowMin) * time.Minute).UnixMilli()
		s := LatencySample{ID: en.id, Name: en.m.Name, Cfg: cfg, Status: displayed(en)}
		en.ring.last(ringSize, func(b beat) {
			if b.ts >= since && b.lat >= 0 && (b.c == 'u' || b.c == 'g') {
				s.Values = append(s.Values, int(b.lat))
			}
		})
		en.mu.Unlock()
		out = append(out, s)
	}
	return out
}
