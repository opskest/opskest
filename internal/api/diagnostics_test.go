package api

import (
	"math"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"opskest/internal/model"
	"opskest/internal/store"
)

type diagResp struct {
	Now     int64 `json:"now"`
	Monitor struct {
		Type           string   `json:"type"`
		IntervalS      int      `json:"intervalS"`
		TimeoutMs      int      `json:"timeoutMs"`
		SSLWarnDays    int      `json:"sslWarnDays"`
		Probe          string   `json:"probe"`
		HasQuery       bool     `json:"hasQuery"`
		HasKeyword     bool     `json:"hasKeyword"`
		Record         string   `json:"record"`
		ExpectedValues []string `json:"expectedValues"`
	} `json:"monitor"`
	Cert *struct {
		NotAfter int64  `json:"notAfter"`
		Issuer   string `json:"issuer"`
		Subject  string `json:"subject"`
	} `json:"cert"`
	Beats []struct {
		TS       int64  `json:"ts"`
		Status   string `json:"status"`
		ErrClass string `json:"errClass"`
		Msg      string `json:"msg"`
	} `json:"beats"`
}

func seedDiagBeats(env *testEnv, monitorID int64, n int) {
	base := time.Now().Add(-time.Duration(n+1) * time.Second)
	for i := 0; i < n; i++ {
		hb := store.Heartbeat{
			MonitorID: monitorID, TS: base.Add(time.Duration(i) * time.Second).UnixMilli(),
			Status: model.StatusUp, LatencyMs: 10 + i, DNSMs: -1, TCPMs: -1, TLSMs: -1, TTFBMs: -1, WaitMs: -1, AppMs: -1,
		}
		env.writer.Heartbeat(hb)
	}
	env.writer.Flush()
}

func TestDiagnosticsBoundedAndNewestFirst(t *testing.T) {
	env := newTestEnv(t)
	mon := env.createMonitor(httpMonitorIn("diag", "https://example.com/diag"))
	seedDiagBeats(env, mon.ID, 250)

	resp := env.do(http.MethodGet, "/api/monitors/"+itoa(mon.ID)+"/diagnostics", nil, "viewer")
	if resp.status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.status, resp.body)
	}
	var got diagResp
	resp.json(t, &got)
	if len(got.Beats) != 200 {
		t.Fatalf("beats = %d, want exactly 200 (the cap)", len(got.Beats))
	}
	for i := 1; i < len(got.Beats); i++ {
		if got.Beats[i].TS >= got.Beats[i-1].TS {
			t.Fatalf("beats not newest first at %d", i)
		}
	}
	if got.Monitor.Type != "http" || got.Monitor.IntervalS != 60 || got.Monitor.TimeoutMs != 5000 || got.Monitor.SSLWarnDays != 14 {
		t.Errorf("monitor config = %+v", got.Monitor)
	}
	if got.Now == 0 || got.Cert != nil {
		t.Errorf("now = %d cert = %+v, want a clock and no certificate", got.Now, got.Cert)
	}
}

func TestDiagnosticsEmptyAndUnknown(t *testing.T) {
	env := newTestEnv(t)
	mon := env.createMonitor(httpMonitorIn("fresh", "https://example.com/fresh"))

	resp := env.do(http.MethodGet, "/api/monitors/"+itoa(mon.ID)+"/diagnostics", nil, "viewer")
	if resp.status != http.StatusOK {
		t.Fatalf("status = %d", resp.status)
	}
	if !strings.Contains(string(resp.body), `"beats":[]`) {
		t.Errorf("a monitor with no checks must return beats as [], got %s", resp.body)
	}

	for _, path := range []string{"/api/monitors/99999/diagnostics", "/api/monitors/abc/diagnostics"} {
		if r := env.do(http.MethodGet, path, nil, "viewer"); r.status != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, r.status)
		}
	}
	if r := env.do(http.MethodGet, "/api/monitors/"+itoa(mon.ID)+"/diagnostics", nil, ""); r.status != http.StatusUnauthorized && r.status != http.StatusNotFound {
		t.Errorf("anonymous = %d, want 401/404", r.status)
	}
}

func TestDiagnosticsCertificate(t *testing.T) {
	env := newTestEnv(t)
	mon := env.createMonitor(httpMonitorIn("tlsmon", "https://example.com/tls"))
	notAfter := time.Now().Add(20 * 24 * time.Hour).UnixMilli()
	env.writer.Do(store.OpUpsertRuntime(store.Runtime{
		MonitorID: mon.ID, CertNotAfter: &notAfter, CertIssuer: "Example CA", CertSubject: "example.com",
	}))
	env.writer.Flush()

	var got diagResp
	env.do(http.MethodGet, "/api/monitors/"+itoa(mon.ID)+"/diagnostics", nil, "viewer").json(t, &got)
	if got.Cert == nil || got.Cert.NotAfter != notAfter || got.Cert.Issuer != "Example CA" || got.Cert.Subject != "example.com" {
		t.Fatalf("cert = %+v", got.Cert)
	}

	// A type that never observes a certificate does not show a stale row.
	ping := httpMonitorIn("pinger", "example.com")
	ping.Type = model.TypePing
	ping.Settings = model.Settings{Count: 3, MaxLossPct: 34}
	pm := env.createMonitor(ping)
	env.writer.Do(store.OpUpsertRuntime(store.Runtime{MonitorID: pm.ID, CertNotAfter: &notAfter, CertIssuer: "Stale CA"}))
	env.writer.Flush()
	var pg diagResp
	env.do(http.MethodGet, "/api/monitors/"+itoa(pm.ID)+"/diagnostics", nil, "viewer").json(t, &pg)
	if pg.Cert != nil {
		t.Errorf("ping monitor leaked a stale certificate: %+v", pg.Cert)
	}
}

// TestDiagnosticsNeverLeaksSecrets: credentials, headers, bodies, keywords and
// SQL text stay out of the response; the beats still carry their stored reason.
func TestDiagnosticsNeverLeaksSecrets(t *testing.T) {
	env := newTestEnv(t)
	secrets := []string{"s3cret-bearer-token", "s3cret-header-value", "sentinel-body-text", "sentinel-keyword-text", "s3cret-basic-pass"}

	in := httpMonitorIn("leaky", "https://example.com/leaky")
	in.Settings.AuthType = "bearer"
	in.Settings.Method = "POST"
	in.Settings.BodyType = "raw"
	in.Settings.Body = secrets[2]
	in.Settings.Keyword = secrets[3]
	bearer := secrets[0]
	in.Secrets.BearerToken = &bearer
	in.Headers = []HeaderIn{{Name: "X-Api-Key", Value: &secrets[1], Secret: true}}
	mon := env.createMonitor(in)

	sqlIn := httpMonitorIn("db", "db.example.com:5432")
	sqlIn.Type = model.TypePostgresSQL
	sqlIn.Settings = model.Settings{
		BasicUser: "monitor", Database: "appdb", Probe: "query",
		Query: "SELECT sentinel_query_text FROM t", QueryOp: "eq", QueryExpected: "sentinel-expected-value",
	}
	pass := secrets[4]
	sqlIn.Secrets.BasicPass = &pass
	sm := env.createMonitor(sqlIn)

	for _, m := range []MonitorOut{mon, sm} {
		resp := env.do(http.MethodGet, "/api/monitors/"+itoa(m.ID)+"/diagnostics", nil, "viewer")
		if resp.status != http.StatusOK {
			t.Fatalf("status = %d, body = %s", resp.status, resp.body)
		}
		body := string(resp.body)
		for _, s := range append(append([]string{}, secrets...), "sentinel_query_text", "sentinel-expected-value") {
			if strings.Contains(body, s) {
				t.Errorf("monitor %d: diagnostics response leaks %q: %s", m.ID, s, body)
			}
		}
	}

	var got diagResp
	env.do(http.MethodGet, "/api/monitors/"+itoa(sm.ID)+"/diagnostics", nil, "viewer").json(t, &got)
	if got.Monitor.Probe != "query" || !got.Monitor.HasQuery {
		t.Errorf("sql monitor = %+v, want probe query and hasQuery", got.Monitor)
	}
	env.do(http.MethodGet, "/api/monitors/"+itoa(mon.ID)+"/diagnostics", nil, "viewer").json(t, &got)
	if !got.Monitor.HasKeyword {
		t.Errorf("http monitor hasKeyword = false, want true (flag only)")
	}
}

func TestDiagnosticsDNSExpectedValues(t *testing.T) {
	env := newTestEnv(t)
	in := httpMonitorIn("dnsmon", "example.com")
	in.Type = model.TypeDNS
	in.Settings = model.Settings{Record: "A", ExpectedValues: []string{"203.0.113.7"}, Match: "equals"}
	mon := env.createMonitor(in)

	var got diagResp
	env.do(http.MethodGet, "/api/monitors/"+itoa(mon.ID)+"/diagnostics", nil, "viewer").json(t, &got)
	if got.Monitor.Record != "A" || len(got.Monitor.ExpectedValues) != 1 || got.Monitor.ExpectedValues[0] != "203.0.113.7" {
		t.Errorf("dns config = %+v", got.Monitor)
	}
}

// ── Response-time analysis (GET /api/monitors/{id}/latency) ─────────────────
//
// These tests check the percentile maths behind the Response time card
// against an independent calculation. 1h and 24h read raw beats and must be
// exact. 7d, 30d and 90d read rollup histograms whose bins grow by a factor of
// 1.1: a percentile is the geometric middle of a bin, so it can sit up to
// sqrt(1.1)-1 (about 4.9 %) from the true sample, plus half a millisecond for
// rounding to whole ms. histTolerance is that bound with a little headroom.
// Count, min, max and avg come from exact counters and are always exact.

func histTolerance(exact int) float64 { return 0.05*float64(exact) + 1 }

// latencyWin is latencyResp plus the window the server says it really covered.
type latencyWin struct {
	latencyResp
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

type latBeat struct {
	ts     int64
	ms     int
	status model.Status
	maint  bool
}

func seedLatBeats(env *testEnv, monitorID int64, beats []latBeat) {
	for _, b := range beats {
		env.writer.Heartbeat(store.Heartbeat{
			MonitorID: monitorID, TS: b.ts, Status: b.status, LatencyMs: b.ms, Maint: b.maint,
			DNSMs: -1, TCPMs: -1, TLSMs: -1, TTFBMs: -1, WaitMs: -1, AppMs: -1,
		})
	}
	env.writer.Flush()
}

// refPercentile is nearest-rank on a sorted slice using integer arithmetic only
// (the implementation uses math.Ceil on floats); p is a whole percent.
func refPercentile(sorted []int, p int) int {
	rank := (p*len(sorted) + 99) / 100
	if rank < 1 {
		rank = 1
	}
	return sorted[rank-1]
}

// latWorkload builds beats spread evenly over [start, end] with a long-tailed
// deterministic latency distribution, rare spikes, some failures with no
// latency, and one maintenance beat with an absurd latency that must never be
// counted. measured is the sorted list of latencies that must be counted.
func latWorkload(start, end int64, n int) (beats []latBeat, measured []int) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < n; i++ {
		ts := start + (end-start)*int64(i)/int64(n-1)
		ms := 5 + int(rng.ExpFloat64()*60)
		switch {
		case i%97 == 5:
			ms = 4000 + rng.Intn(9000)
		case i%41 == 3:
			beats = append(beats, latBeat{ts: ts, ms: -1, status: model.StatusDown})
			continue
		}
		beats = append(beats, latBeat{ts: ts, ms: ms, status: model.StatusUp})
		measured = append(measured, ms)
	}
	beats = append(beats, latBeat{ts: start + 12345, ms: 99999, status: model.StatusUp, maint: true})
	sort.Ints(measured)
	return beats, measured
}

func TestLatencyPercentilesMatchIndependentCalculation(t *testing.T) {
	cases := []struct {
		rng    string
		span   time.Duration
		margin time.Duration // keeps the first sample clear of the partial bucket at the window's old edge
		exact  bool
		source string
	}{
		{"1h", time.Hour, 2 * time.Minute, true, "raw"},
		{"24h", 24 * time.Hour, 5 * time.Minute, true, "raw"},
		{"7d", 7 * 24 * time.Hour, 3 * time.Hour, false, "hourly"},
		{"30d", 30 * 24 * time.Hour, 3 * time.Hour, false, "hourly"},
		{"90d", 90 * 24 * time.Hour, 3 * 24 * time.Hour, false, "daily"},
	}
	for _, c := range cases {
		t.Run(c.rng, func(t *testing.T) {
			env := newTestEnv(t)
			mon := env.createMonitor(httpMonitorIn("acc-"+c.rng, "https://example.com/acc"))
			now := time.Now()
			start := now.Add(-c.span + c.margin).UnixMilli()
			end := now.Add(-2 * time.Minute).UnixMilli()
			beats, measured := latWorkload(start, end, 1500)
			// One real sample outside the window: it must not be counted.
			beats = append(beats, latBeat{ts: now.Add(-c.span - 2*c.margin).UnixMilli(), ms: 77777, status: model.StatusUp})
			seedLatBeats(env, mon.ID, beats)

			resp := env.do(http.MethodGet, "/api/monitors/"+itoa(mon.ID)+"/latency?range="+c.rng, nil, "viewer")
			if resp.status != http.StatusOK {
				t.Fatalf("status = %d, body = %s", resp.status, resp.body)
			}
			var got latencyResp
			resp.json(t, &got)
			if got.Source != c.source {
				t.Errorf("source = %q, want %q", got.Source, c.source)
			}
			s := got.Summary
			if s.Count != int64(len(measured)) {
				t.Fatalf("count = %d, want %d (failed, maintenance and out-of-window beats must not count)", s.Count, len(measured))
			}
			var sum int64
			for _, v := range measured {
				sum += int64(v)
			}
			wantAvg := int(math.Round(float64(sum) / float64(len(measured))))
			if s.Min == nil || *s.Min != measured[0] || s.Max == nil || *s.Max != measured[len(measured)-1] {
				t.Errorf("min/max = %v/%v, want %d/%d (always exact)", s.Min, s.Max, measured[0], measured[len(measured)-1])
			}
			if s.Avg == nil || *s.Avg != wantAvg {
				t.Errorf("avg = %v, want %d (always exact)", s.Avg, wantAvg)
			}
			for _, q := range []struct {
				name string
				p    int
				got  *int
			}{{"p50", 50, s.P50}, {"p90", 90, s.P90}, {"p95", 95, s.P95}, {"p99", 99, s.P99}} {
				want := refPercentile(measured, q.p)
				if q.got == nil {
					t.Errorf("%s missing", q.name)
					continue
				}
				diff := math.Abs(float64(*q.got - want))
				if c.exact && diff != 0 {
					t.Errorf("%s = %d, want exactly %d", q.name, *q.got, want)
				}
				if !c.exact && diff > histTolerance(want) {
					t.Errorf("%s = %d, want %d within %.1f (histogram bins are 1.1x wide)", q.name, *q.got, want, histTolerance(want))
				}
			}
			var inPoints int64
			for _, p := range got.Points {
				inPoints += p.Count
			}
			if inPoints != s.Count {
				t.Errorf("points hold %d samples, summary says %d", inPoints, s.Count)
			}
		})
	}
}

// A window holding one sample reports that sample for every statistic, and a
// window with none reports no statistics at all, never zeros.
func TestLatencySparseWindows(t *testing.T) {
	env := newTestEnv(t)
	mon := env.createMonitor(httpMonitorIn("sparse", "https://example.com/sparse"))
	seedLatBeats(env, mon.ID, []latBeat{{ts: time.Now().Add(-30 * time.Minute).UnixMilli(), ms: 137, status: model.StatusUp}})

	for _, rng := range []string{"1h", "24h", "7d", "30d", "90d"} {
		var got latencyResp
		env.do(http.MethodGet, "/api/monitors/"+itoa(mon.ID)+"/latency?range="+rng, nil, "viewer").json(t, &got)
		s := got.Summary
		if s.Count != 1 {
			t.Fatalf("%s: count = %d, want 1", rng, s.Count)
		}
		for name, v := range map[string]*int{"avg": s.Avg, "min": s.Min, "max": s.Max, "p50": s.P50, "p90": s.P90, "p95": s.P95, "p99": s.P99} {
			if v == nil || *v != 137 {
				t.Errorf("%s: %s = %v, want 137 (a single sample is exact, clamped to min/max)", rng, name, v)
			}
		}
	}

	empty := env.createMonitor(httpMonitorIn("none", "https://example.com/none"))
	for _, rng := range []string{"1h", "24h", "7d", "30d", "90d"} {
		var got latencyResp
		env.do(http.MethodGet, "/api/monitors/"+itoa(empty.ID)+"/latency?range="+rng, nil, "viewer").json(t, &got)
		s := got.Summary
		if s.Count != 0 || s.Avg != nil || s.Min != nil || s.Max != nil || s.P50 != nil || s.P99 != nil || got.Points == nil || len(got.Points) != 0 {
			t.Errorf("%s: empty monitor summary = %+v points=%v, want count 0, null stats, [] points", rng, s, got.Points)
		}
	}
}

// Failed checks that still got a response (an HTTP 500, a failed assertion)
// have a measured duration, so they count. The Response time card therefore
// counts "checks with a response time", not "successful checks" (it used to
// say the latter).
func TestLatencyCountsFailedChecksThatMeasuredATime(t *testing.T) {
	env := newTestEnv(t)
	mon := env.createMonitor(httpMonitorIn("fivehundred", "https://example.com/500"))
	base := time.Now().Add(-10 * time.Minute)
	var beats []latBeat
	for i := 0; i < 5; i++ {
		beats = append(beats, latBeat{ts: base.Add(time.Duration(i) * time.Second).UnixMilli(), ms: 40 + i, status: model.StatusDown})
	}
	seedLatBeats(env, mon.ID, beats)
	for _, rng := range []string{"1h", "7d"} {
		var got latencyResp
		env.do(http.MethodGet, "/api/monitors/"+itoa(mon.ID)+"/latency?range="+rng, nil, "viewer").json(t, &got)
		if got.Summary.Count != 5 || got.Summary.P50 == nil || math.Abs(float64(*got.Summary.P50-42)) > histTolerance(42) {
			t.Errorf("%s: summary = %+v, want the 5 down checks counted with p50 near 42", rng, got.Summary)
		}
	}
}

// Window edges. Raw ranges cut at the exact instant. Rollup ranges include a
// bucket only when it STARTS inside the window, so the partial UTC hour (7d,
// 30d) or partial server-zone day (90d) at the old end is left out whole: the
// summary covers a window shortened by up to one bucket. Rollups cannot be
// split, so the response reports the window it really covers in from/to
// (unix ms) and the card shows it.
func TestLatencyWindowEdges(t *testing.T) {
	env := newTestEnv(t)
	mon := env.createMonitor(httpMonitorIn("edges", "https://example.com/edges"))
	now := time.Now()

	// Raw: 40 s inside, 40 s outside the 1h window.
	seedLatBeats(env, mon.ID, []latBeat{
		{ts: now.Add(-time.Hour + 40*time.Second).UnixMilli(), ms: 11, status: model.StatusUp},
		{ts: now.Add(-time.Hour - 40*time.Second).UnixMilli(), ms: 99, status: model.StatusUp},
	})
	var raw latencyWin
	env.do(http.MethodGet, "/api/monitors/"+itoa(mon.ID)+"/latency?range=1h", nil, "viewer").json(t, &raw)
	if raw.Summary.Count != 1 || raw.Summary.Max == nil || *raw.Summary.Max != 11 {
		t.Errorf("1h: summary = %+v, want only the sample inside the window", raw.Summary)
	}
	if d := raw.To - raw.From; d != time.Hour.Milliseconds() {
		t.Errorf("1h: window = %d ms, want exactly 1h", d)
	}

	// Hourly: the first full UTC hour boundary after the window start.
	mon = env.createMonitor(httpMonitorIn("edges7", "https://example.com/edges7"))
	from := now.Add(-7 * 24 * time.Hour)
	boundary := store.HourBucket(from.UnixMilli()) + time.Hour.Milliseconds()
	seedLatBeats(env, mon.ID, []latBeat{
		{ts: boundary - 90_000, ms: 555, status: model.StatusUp},   // inside the window, but in the hour that started before it
		{ts: boundary + 30*60_000, ms: 66, status: model.StatusUp}, // in the first hour that starts inside the window
	})
	var week latencyWin
	env.do(http.MethodGet, "/api/monitors/"+itoa(mon.ID)+"/latency?range=7d", nil, "viewer").json(t, &week)
	if week.Summary.Count != 1 || week.Summary.Max == nil || *week.Summary.Max != 66 {
		t.Errorf("7d: summary = %+v, want only the sample in the first whole hour", week.Summary)
	}
	if week.From != boundary {
		t.Errorf("7d: from = %s, want the first whole hour %s", time.UnixMilli(week.From).UTC(), time.UnixMilli(boundary).UTC())
	}
	if week.From < from.UnixMilli() || week.From-from.UnixMilli() > time.Hour.Milliseconds() {
		t.Errorf("7d: from is %v after the requested start, want 0..1h", time.Duration(week.From-from.UnixMilli())*time.Millisecond)
	}

	// Daily buckets are server-zone midnights (the test zone is UTC+05:30, so
	// they do not line up with UTC days).
	mon = env.createMonitor(httpMonitorIn("edges90", "https://example.com/edges90"))
	from90 := now.Add(-90 * 24 * time.Hour)
	dayStart := env.store.DayBucket(from90.UnixMilli())
	nextDay := env.store.DayBucket(dayStart + 36*time.Hour.Milliseconds())
	seedLatBeats(env, mon.ID, []latBeat{
		{ts: nextDay - 90_000, ms: 777, status: model.StatusUp},
		{ts: nextDay + 60*60_000, ms: 88, status: model.StatusUp},
	})
	var quarter latencyWin
	env.do(http.MethodGet, "/api/monitors/"+itoa(mon.ID)+"/latency?range=90d", nil, "viewer").json(t, &quarter)
	if quarter.From != nextDay {
		t.Errorf("90d: from = %s, want the first server-zone midnight %s", time.UnixMilli(quarter.From).UTC(), time.UnixMilli(nextDay).UTC())
	}
	if quarter.Summary.Count != 1 || quarter.Summary.Max == nil || *quarter.Summary.Max != 88 {
		t.Errorf("90d: summary = %+v, want only the 88 ms sample (the 777 ms one is in the partial first day)", quarter.Summary)
	}
}

// Raw chart points sit on a fixed grid (whole minutes for 1h, quarter hours
// for 24h) instead of one anchored to the moment of the request, which slid by
// the time between two refreshes and showed points at times like 12:07.
func TestLatencyRawPointsAreGridAligned(t *testing.T) {
	env := newTestEnv(t)
	mon := env.createMonitor(httpMonitorIn("grid", "https://example.com/grid"))
	now := time.Now()
	beats, _ := latWorkload(now.Add(-23*time.Hour).UnixMilli(), now.Add(-2*time.Minute).UnixMilli(), 600)
	seedLatBeats(env, mon.ID, beats)

	for _, c := range []struct {
		rng   string
		width int64
	}{{"1h", 60_000}, {"24h", 15 * 60_000}} {
		var got latencyResp
		env.do(http.MethodGet, "/api/monitors/"+itoa(mon.ID)+"/latency?range="+c.rng, nil, "viewer").json(t, &got)
		if len(got.Points) == 0 {
			t.Fatalf("%s: no points", c.rng)
		}
		var prev int64
		for _, p := range got.Points {
			if p.T%c.width != 0 {
				t.Errorf("%s: point at %s is not on a %d ms grid", c.rng, time.UnixMilli(p.T).UTC(), c.width)
			}
			if p.T <= prev {
				t.Errorf("%s: points out of order at %d", c.rng, p.T)
			}
			prev = p.T
		}
	}
}
