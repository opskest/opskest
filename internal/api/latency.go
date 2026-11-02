package api

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sort"
	"time"

	"opskest/internal/latency"
	"opskest/internal/store"
)

// LatencyStats summarises a set of latency samples. Every value is nil
// when Count is 0 (no measured latency — e.g. every check failed), so a
// chart shows a gap rather than a misleading 0 ms.
type LatencyStats struct {
	Count int64 `json:"count"`
	Avg   *int  `json:"avg"`
	Min   *int  `json:"min"`
	Max   *int  `json:"max"`
	P50   *int  `json:"p50"`
	P90   *int  `json:"p90"`
	P95   *int  `json:"p95"`
	P99   *int  `json:"p99"`
}

// LatencyPoint is one chart point: the stats of the samples in [T, T+width).
type LatencyPoint struct {
	T int64 `json:"t"`
	LatencyStats
}

var quantiles = [4]float64{0.50, 0.90, 0.95, 0.99}

var errBadRange = errors.New("invalid range")

// latencyRange describes how one ?range= value is served.
type latencyRange struct {
	span   time.Duration
	width  time.Duration // point width
	source string        // raw | hourly | daily
}

var latencyRanges = map[string]latencyRange{
	"1h":  {time.Hour, time.Minute, "raw"},
	"24h": {24 * time.Hour, 15 * time.Minute, "raw"},
	"7d":  {7 * 24 * time.Hour, time.Hour, "hourly"},
	"30d": {30 * 24 * time.Hour, 6 * time.Hour, "hourly"},
	"90d": {90 * 24 * time.Hour, 24 * time.Hour, "daily"},
}

// statsFromSorted computes exact nearest-rank stats over sorted samples.
func statsFromSorted(v []int) LatencyStats {
	st := LatencyStats{Count: int64(len(v))}
	if len(v) == 0 {
		return st
	}
	var sum int64
	for _, x := range v {
		sum += int64(x)
	}
	st.Avg = intp(int(math.Round(float64(sum) / float64(len(v)))))
	st.Min, st.Max = intp(v[0]), intp(v[len(v)-1])
	ps := [4]**int{&st.P50, &st.P90, &st.P95, &st.P99}
	for i, q := range quantiles {
		idx := int(math.Ceil(q*float64(len(v)))) - 1
		if idx < 0 {
			idx = 0
		}
		*ps[i] = intp(v[idx])
	}
	return st
}

// statsFromHist computes stats from a merged histogram plus the exact
// count/sum/min/max the rollup rows carry alongside it.
func statsFromHist(h latency.Hist, latSum, latCount int64, min, max int) LatencyStats {
	st := LatencyStats{Count: latCount}
	if latCount == 0 {
		return st
	}
	st.Avg = intp(int(math.Round(float64(latSum) / float64(latCount))))
	st.Min, st.Max = intp(min), intp(max)
	// Rollups written before histograms existed count samples the
	// histogram never saw: percentiles from a small recent subset of a
	// range would mislabel it, so report them only when the histogram
	// covers at least half the samples (the chart shows the average then).
	if 2*h.Count() < uint64(latCount) {
		return st
	}
	ps := [4]**int{&st.P50, &st.P90, &st.P95, &st.P99}
	for i, q := range quantiles {
		if v, ok := h.Quantile(q, min, max); ok {
			*ps[i] = intp(v)
		}
	}
	return st
}

func intp(v int) *int { return &v }

// rollupAcc merges rollup rows into one stats bucket.
type rollupAcc struct {
	sum, count int64
	min, max   int
	hist       latency.Hist
}

func (a *rollupAcc) add(r store.Rollup) {
	if r.LatCount == 0 {
		return
	}
	if a.count == 0 || r.LatMin < a.min {
		a.min = r.LatMin
	}
	if a.count == 0 || r.LatMax > a.max {
		a.max = r.LatMax
	}
	a.sum += r.LatSum
	a.count += r.LatCount
	if len(r.LatHist) > 0 {
		if a.hist == nil {
			a.hist = latency.Hist{}
		}
		a.hist.Merge(r.Histogram())
	}
}

func (a *rollupAcc) stats() LatencyStats {
	return statsFromHist(a.hist, a.sum, a.count, a.min, a.max)
}

// latencySeries computes one monitor's summary and chart points for rng.
// Shared by the private detail endpoint and the public status page.
func (s *server) latencySeries(ctx context.Context, monitorID int64, rng string, now time.Time) (LatencyStats, []LatencyPoint, string, error) {
	lr, ok := latencyRanges[rng]
	if !ok {
		return LatencyStats{}, nil, "", errBadRange
	}
	to := now.UnixMilli()
	from := now.Add(-lr.span).UnixMilli()
	width := lr.width.Milliseconds()
	points := []LatencyPoint{}

	if lr.source == "raw" {
		samples, err := s.d.Store.LatenciesBetween(ctx, monitorID, from, to)
		if err != nil {
			return LatencyStats{}, nil, "", err
		}
		all := make([]int, 0, len(samples))
		var cur []int
		curT := int64(-1)
		flush := func() {
			if len(cur) > 0 {
				sort.Ints(cur)
				points = append(points, LatencyPoint{T: curT, LatencyStats: statsFromSorted(cur)})
			}
		}
		for _, smp := range samples {
			// Epoch-aligned, like the hourly grid: points sit on whole minutes
			// (1h) or quarter hours (24h) and a sample keeps its point from one
			// refresh to the next. Anchoring to `from` instead slid every
			// boundary a little on each request, so points showed times like
			// 12:07 and their percentiles changed under the viewer.
			t := smp.TS / width * width
			if t != curT {
				flush()
				cur, curT = cur[:0:0], t
			}
			cur = append(cur, smp.Ms)
			all = append(all, smp.Ms)
		}
		flush()
		sort.Ints(all)
		return statsFromSorted(all), points, "raw", nil
	}

	table := "rollup_hourly"
	if lr.source == "daily" {
		table = "rollup_daily"
	}
	// A rollup row is included when its bucket starts inside the range, so
	// neither the summary nor the first point reaches before from.
	rows, err := s.d.Store.RollupSeries(ctx, table, monitorID, from, to)
	if err != nil {
		return LatencyStats{}, nil, "", err
	}
	var total rollupAcc
	var cur rollupAcc
	curT := int64(math.MinInt64)
	flush := func() {
		if cur.count > 0 {
			points = append(points, LatencyPoint{T: curT, LatencyStats: cur.stats()})
		}
	}
	for _, r := range rows {
		// Hourly rows are UTC hours: points sit on a UTC grid of width
		// (1h or 6h). Daily rows are local midnights, one point each.
		t := r.Bucket
		if lr.source == "hourly" {
			t = r.Bucket / width * width
		}
		if t != curT {
			flush()
			cur, curT = rollupAcc{}, t
		}
		cur.add(r)
		total.add(r)
	}
	flush()
	return total.stats(), points, lr.source, nil
}

// latencyWindow is the span a ?range= really covers, in unix ms. Raw ranges
// cover [now-span, now] exactly. Rollup ranges include a bucket only when it
// starts inside the window (latencySeries), so from is moved up to the first
// UTC hour (7d, 30d) or server-zone midnight (90d) at or after now-span: the
// partial bucket at the old end is left out whole, and callers should say
// where the numbers really start instead of implying the full span.
func (s *server) latencyWindow(lr latencyRange, now time.Time) (from, to int64) {
	from, to = now.Add(-lr.span).UnixMilli(), now.UnixMilli()
	switch lr.source {
	case "hourly":
		if b := store.HourBucket(from); b < from {
			from = b + time.Hour.Milliseconds()
		}
	case "daily":
		if b := s.d.Store.DayBucket(from); b < from {
			// A local day is 23-25 h long, so 36 h after a midnight is always inside the next day.
			from = s.d.Store.DayBucket(b + 36*time.Hour.Milliseconds())
		}
	}
	return from, to
}

// handleMonitorLatency serves GET /api/monitors/{id}/latency?range=.
func (s *server) handleMonitorLatency(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	if _, ok := s.d.Engine.Summary(id); !ok {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "24h"
	}
	now := s.d.Now()
	summary, points, source, err := s.latencySeries(r.Context(), id, rng, now)
	if errors.Is(err, errBadRange) {
		writeErr(w, http.StatusBadRequest, "invalid range: use 1h, 24h, 7d, 30d or 90d", nil)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	from, to := s.latencyWindow(latencyRanges[rng], now)
	writeJSON(w, http.StatusOK, map[string]any{"range": rng, "source": source, "from": from, "to": to, "summary": summary, "points": points})
}
