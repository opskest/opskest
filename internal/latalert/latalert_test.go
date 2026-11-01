package latalert

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"opskest/internal/engine"
	"opskest/internal/latency"
	"opskest/internal/model"
	"opskest/internal/store"
)

type fakeSrc struct{ samples []engine.LatencySample }

func (f *fakeSrc) LatencySamples(time.Time) []engine.LatencySample { return f.samples }

type fakeRoll struct {
	rows  []store.Rollup
	calls int
}

func (f *fakeRoll) RollupSeries(context.Context, string, int64, int64, int64) ([]store.Rollup, error) {
	f.calls++
	return f.rows, nil
}

type fakeSink struct{ evs []model.Event }

func (f *fakeSink) Publish(ev model.Event) { f.evs = append(f.evs, ev) }
func (f *fakeSink) take() []model.Event    { e := f.evs; f.evs = nil; return e }

func rep(v, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func setup(cfg model.LatencyAlert) (*Evaluator, *fakeSrc, *fakeRoll, *fakeSink, *time.Time) {
	now := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	src := &fakeSrc{samples: []engine.LatencySample{{ID: 1, Name: "api", Cfg: cfg, Status: model.StatusUp}}}
	roll := &fakeRoll{}
	sink := &fakeSink{}
	ev := New(src, roll, sink, func() time.Time { return now }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return ev, src, roll, sink, &now
}

func TestAbsoluteLimitFiresOnceAndClearsWithHysteresis(t *testing.T) {
	ev, src, _, sink, _ := setup(model.LatencyAlert{Percentile: 95, ThresholdMs: 800, WindowMin: 15})
	ctx := context.Background()

	src.samples[0].Values = rep(500, 20)
	ev.Tick(ctx)
	if len(sink.take()) != 0 {
		t.Fatal("fired below the limit")
	}

	src.samples[0].Values = rep(1200, 20)
	ev.Tick(ctx)
	evs := sink.take()
	if len(evs) != 1 || evs[0].Kind != model.EvLatencyHigh || evs[0].MonitorID != 1 {
		t.Fatalf("over the limit: %+v", evs)
	}
	ev.Tick(ctx)
	if len(sink.take()) != 0 {
		t.Fatal("re-fired while still slow")
	}

	// 780 ms: under the limit but not under 90 % of it — stays firing.
	src.samples[0].Values = rep(780, 20)
	ev.Tick(ctx)
	if len(sink.take()) != 0 || !ev.Firing(1) {
		t.Fatal("cleared inside the hysteresis band")
	}
	src.samples[0].Values = rep(700, 20)
	ev.Tick(ctx)
	evs = sink.take()
	if len(evs) != 1 || evs[0].Kind != model.EvLatencyCleared {
		t.Fatalf("below 90%% of the limit: %+v", evs)
	}
}

func TestTooFewSamplesNotEvaluated(t *testing.T) {
	ev, src, _, sink, _ := setup(model.LatencyAlert{Percentile: 95, ThresholdMs: 100, WindowMin: 15})
	src.samples[0].Values = rep(5000, 4)
	ev.Tick(context.Background())
	if len(sink.take()) != 0 {
		t.Fatal("fired on 4 samples")
	}
}

func TestRelativeLimitUsesBaseline(t *testing.T) {
	ev, src, roll, sink, _ := setup(model.LatencyAlert{Percentile: 95, Factor: 3, WindowMin: 15})
	h := latency.Hist{}
	for i := 0; i < 200; i++ {
		h.Add(200)
	}
	roll.rows = []store.Rollup{{LatCount: 200, LatSum: 40000, LatMin: 200, LatMax: 200, LatHist: h.Encode()}}

	src.samples[0].Values = rep(550, 20) // < 3 × 200
	ev.Tick(context.Background())
	if len(sink.take()) != 0 {
		t.Fatal("fired under 3× usual")
	}
	src.samples[0].Values = rep(700, 20) // > 600
	ev.Tick(context.Background())
	evs := sink.take()
	if len(evs) != 1 || evs[0].Kind != model.EvLatencyHigh {
		t.Fatalf("over 3× usual: %+v", evs)
	}
	if roll.calls != 1 {
		t.Errorf("baseline loaded %d times within its TTL, want 1", roll.calls)
	}
	// Reason names the value, the limit and the usual value.
	if r := evs[0].Reason; r != "p95 700 ms over 15 min — limit 600 ms (usual 200 ms)" {
		t.Errorf("reason = %q", r)
	}
}

func TestRelativeOnlyWithoutBaselineDoesNothing(t *testing.T) {
	ev, src, _, sink, _ := setup(model.LatencyAlert{Percentile: 95, Factor: 2, WindowMin: 15})
	src.samples[0].Values = rep(9000, 20)
	ev.Tick(context.Background())
	if len(sink.take()) != 0 {
		t.Fatal("fired with no baseline to compare against")
	}
}

func TestDownOrMaintenanceClearsSilently(t *testing.T) {
	for _, st := range []model.Status{model.StatusDown, model.StatusMaintenance, model.StatusPaused} {
		ev, src, _, sink, _ := setup(model.LatencyAlert{Percentile: 95, ThresholdMs: 100, WindowMin: 15})
		src.samples[0].Values = rep(500, 10)
		ev.Tick(context.Background())
		sink.take()
		src.samples[0].Status = st
		ev.Tick(context.Background())
		if len(sink.take()) != 0 || ev.Firing(1) {
			t.Errorf("%v: should drop the firing state without an event", st)
		}
	}
}

func TestRemovedConfigForgotten(t *testing.T) {
	ev, src, _, sink, _ := setup(model.LatencyAlert{Percentile: 95, ThresholdMs: 100, WindowMin: 15})
	src.samples[0].Values = rep(500, 10)
	ev.Tick(context.Background())
	sink.take()
	src.samples = nil
	ev.Tick(context.Background())
	if ev.Firing(1) || len(sink.take()) != 0 {
		t.Fatal("removed alert still firing or emitted")
	}
}
