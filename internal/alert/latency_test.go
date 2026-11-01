package alert

import (
	"strings"
	"testing"

	"opskest/internal/model"
)

// Response-time alerts read as "slow", carry the measured detail, never
// say "down for", and never page.
func TestLatencyAlertRendering(t *testing.T) {
	it := Item{MonitorID: 7, Name: "Checkout API", Reason: "p95 1.24 s over 15 min — limit 800 ms (usual 265 ms)"}
	high := Message{Kind: model.EvLatencyHigh, Items: []Item{it}}
	if got := Title(high); got != "🐢 Slow — Checkout API" {
		t.Errorf("title = %q", got)
	}
	var details string
	for _, f := range DetailFields(high, it) {
		details += f.Label + "=" + f.Value + "\n"
	}
	if !strings.Contains(details, "p95 1.24 s over 15 min") || strings.Contains(details, "down for") {
		t.Errorf("details = %s", details)
	}
	if Title(Message{Kind: model.EvLatencyCleared, Items: []Item{it}}) != "🟢 Response time back to normal — Checkout API" {
		t.Errorf("cleared title wrong")
	}
	if slackColorFor(model.EvLatencyCleared) != slackColorUp || slackColorFor(model.EvLatencyHigh) != slackColorWarn {
		t.Errorf("slack colours wrong")
	}
	for _, k := range []model.EventKind{model.EvLatencyHigh, model.EvLatencyCleared, model.EvRegional} {
		if !informational(k) {
			t.Errorf("%s should not page", k)
		}
	}
	if informational(model.EvDown) {
		t.Errorf("down must page")
	}
}
