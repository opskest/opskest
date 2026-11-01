package api

import (
	"net/http"
	"testing"
	"time"

	"opskest/internal/latency"
	"opskest/internal/model"
	"opskest/internal/store"
)

type latencyResp struct {
	Range   string `json:"range"`
	Source  string `json:"source"`
	Summary struct {
		Count int64 `json:"count"`
		Avg   *int  `json:"avg"`
		Min   *int  `json:"min"`
		Max   *int  `json:"max"`
		P50   *int  `json:"p50"`
		P90   *int  `json:"p90"`
		P95   *int  `json:"p95"`
		P99   *int  `json:"p99"`
	} `json:"summary"`
	Points []struct {
		T     int64 `json:"t"`
		Count int64 `json:"count"`
		P50   *int  `json:"p50"`
		P99   *int  `json:"p99"`
	} `json:"points"`
}

// seedBeats writes one beat per latency, a second apart, ending a minute
// ago, and flushes them (heartbeats + rollups) through the real writer.
func seedBeats(env *testEnv, monitorID int64, lats []int) {
	end := time.Now().Add(-1 * time.Minute)
	for i, ms := range lats {
		status := model.StatusUp
		if ms < 0 {
			status = model.StatusDown
		}
		env.writer.Heartbeat(store.Heartbeat{
			MonitorID: monitorID, TS: end.Add(-time.Duration(len(lats)-i) * time.Second).UnixMilli(),
			Status: status, LatencyMs: ms,
		})
	}
	env.writer.Flush()
}

func TestMonitorLatencyRawExact(t *testing.T) {
	env := newTestEnv(t)
	mon := env.createMonitor(httpMonitorIn("lat", "https://example.com/lat"))
	// 1..100 ms, plus a failed check that must not count.
	lats := make([]int, 0, 101)
	for i := 1; i <= 100; i++ {
		lats = append(lats, i)
	}
	lats = append(lats, -1)
	seedBeats(env, mon.ID, lats)

	resp := env.do(http.MethodGet, "/api/monitors/"+itoa(mon.ID)+"/latency?range=1h", nil, "viewer")
	if resp.status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.status, resp.body)
	}
	var got latencyResp
	resp.json(t, &got)
	s := got.Summary
	if got.Source != "raw" || s.Count != 100 {
		t.Fatalf("source=%q count=%d, want raw/100", got.Source, s.Count)
	}
	// Nearest-rank on 1..100 is exact.
	for name, pair := range map[string][2]*int{"p50": {s.P50, ptr(50)}, "p90": {s.P90, ptr(90)}, "p95": {s.P95, ptr(95)}, "p99": {s.P99, ptr(99)}, "min": {s.Min, ptr(1)}, "max": {s.Max, ptr(100)}} {
		if pair[0] == nil || *pair[0] != *pair[1] {
			t.Errorf("%s = %v, want %d", name, pair[0], *pair[1])
		}
	}
	if len(got.Points) == 0 || len(got.Points) > 60 {
		t.Fatalf("1h points = %d, want 1..60", len(got.Points))
	}
}

func TestMonitorLatencyFromRollups(t *testing.T) {
	env := newTestEnv(t)
	mon := env.createMonitor(httpMonitorIn("lat7d", "https://example.com/lat7d"))
	lats := make([]int, 0, 200)
	for i := 0; i < 200; i++ {
		lats = append(lats, 200+i%5) // 200..204
	}
	seedBeats(env, mon.ID, lats)

	for _, rng := range []string{"7d", "30d", "90d"} {
		resp := env.do(http.MethodGet, "/api/monitors/"+itoa(mon.ID)+"/latency?range="+rng, nil, "viewer")
		if resp.status != http.StatusOK {
			t.Fatalf("%s: status = %d, body = %s", rng, resp.status, resp.body)
		}
		var got latencyResp
		resp.json(t, &got)
		if got.Summary.Count != 200 || got.Summary.P50 == nil {
			t.Fatalf("%s: summary = %+v", rng, got.Summary)
		}
		// Histogram estimate, clamped to the bucket's exact min/max.
		if p := *got.Summary.P50; p < 200 || p > 204 {
			t.Errorf("%s: p50 = %d, want 200..204", rng, p)
		}
		if len(got.Points) == 0 {
			t.Errorf("%s: no points", rng)
		}
	}
}

// TestMonitorLatencyAllFailed: a monitor whose every check failed has no
// latency at all — count 0 and null percentiles, never zeros.
func TestMonitorLatencyAllFailed(t *testing.T) {
	env := newTestEnv(t)
	mon := env.createMonitor(httpMonitorIn("dead", "https://example.com/dead"))
	seedBeats(env, mon.ID, []int{-1, -1, -1})

	for _, rng := range []string{"1h", "24h", "7d"} {
		resp := env.do(http.MethodGet, "/api/monitors/"+itoa(mon.ID)+"/latency?range="+rng, nil, "viewer")
		if resp.status != http.StatusOK {
			t.Fatalf("%s: status = %d", rng, resp.status)
		}
		var got latencyResp
		resp.json(t, &got)
		if got.Summary.Count != 0 || got.Summary.P50 != nil || got.Summary.Avg != nil {
			t.Errorf("%s: summary = %+v, want empty", rng, got.Summary)
		}
		if got.Points == nil {
			t.Errorf("%s: points must be [] not null", rng)
		}
	}
}

func TestMonitorLatencyErrors(t *testing.T) {
	env := newTestEnv(t)
	mon := env.createMonitor(httpMonitorIn("e", "https://example.com/e"))
	if r := env.do(http.MethodGet, "/api/monitors/"+itoa(mon.ID)+"/latency?range=1y", nil, "viewer"); r.status != http.StatusBadRequest {
		t.Errorf("bad range = %d, want 400", r.status)
	}
	if r := env.do(http.MethodGet, "/api/monitors/99999/latency?range=1h", nil, "viewer"); r.status != http.StatusNotFound {
		t.Errorf("unknown monitor = %d, want 404", r.status)
	}
	if r := env.do(http.MethodGet, "/api/monitors/"+itoa(mon.ID)+"/latency?range=1h", nil, ""); r.status != http.StatusUnauthorized && r.status != http.StatusNotFound {
		t.Errorf("anonymous = %d, want 401/404", r.status)
	}
}

func ptr(v int) *int { return &v }

// TestPartialHistogramNotReported: a rollup row from before the upgrade
// (no histogram) merged with a few new samples must not report
// percentiles from that unrepresentative subset — only the average.
func TestPartialHistogramNotReported(t *testing.T) {
	h := latency.Hist{}
	h.Add(744)
	partial := statsFromHist(h, 944, 3, 100, 744) // 3 samples, histogram knows 1
	if partial.P50 != nil || partial.P99 != nil || partial.Avg == nil || *partial.Avg != 315 {
		t.Fatalf("partial = %+v, want avg only", partial)
	}
	for i := 0; i < 2; i++ {
		h.Add(100)
	}
	full := statsFromHist(h, 944, 3, 100, 744)
	if full.P50 == nil {
		t.Fatalf("fully covered row lost its percentiles: %+v", full)
	}
}
