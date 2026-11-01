package regions

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"opskest/internal/check"
	"opskest/internal/model"
	"opskest/internal/secret"
	"opskest/internal/store"
)

type capSink struct {
	mu  sync.Mutex
	evs []model.Event
}

func (s *capSink) Publish(ev model.Event) {
	s.mu.Lock()
	s.evs = append(s.evs, ev)
	s.mu.Unlock()
}

func (s *capSink) kinds() []model.EventKind {
	s.mu.Lock()
	defer s.mu.Unlock()
	var k []model.EventKind
	for _, e := range s.evs {
		k = append(k, e.Kind)
	}
	return k
}

func TestPublicHost(t *testing.T) {
	for host, want := range map[string]bool{
		"example.com": true, "api.example.com": true, "8.8.8.8": true,
		"10.0.0.5": false, "192.168.40.163": false, "127.0.0.1": false, "100.64.1.1": false,
		"localhost": false, "db": false, "svc.internal": false, "printer.local": false, "": false, "::1": false,
	} {
		if got := PublicHost(host); got != want {
			t.Errorf("PublicHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func httpMon(target string) *model.Monitor {
	m := model.NewMonitor(model.TypeHTTP)
	m.ID, m.Name, m.Target = 1, "api", target
	m.Settings.AcceptedCodes = "200"
	m.Settings.Keyword, m.Settings.KeywordMode, m.Settings.CaseSensitive = `"status":"ok"`, "contains", true
	return m
}

func TestPlanFor(t *testing.T) {
	cfg := Config{Countries: []string{"US", "DE"}, Token: "tok"}
	p := PlanFor(httpMon("https://user:pw@api.example.com/health?token=secret"), cfg)
	if p.Reason != "" || p.Req.Target.String() != "https://api.example.com/health" || len(p.Countries) != 2 || p.Req.Token != "tok" {
		t.Fatalf("plan = %+v", p)
	}
	legacyLocal := httpMon("https://api.example.com")
	legacyLocal.Settings.RegionMode = "india" // legacy persisted value
	custom := httpMon("https://api.example.com")
	custom.Settings.RegionMode, custom.Settings.RegionCountries = "custom", []string{"JP"}
	auth := httpMon("https://api.example.com")
	auth.Settings.AuthType = "bearer"
	private := httpMon("http://10.1.2.3/x")
	tcp := model.NewMonitor(model.TypeTCP)
	tcp.Target = "example.com:443"
	if PlanFor(legacyLocal, cfg).Reason == "" || PlanFor(auth, cfg).Reason == "" || PlanFor(private, cfg).Reason == "" || PlanFor(tcp, cfg).Reason == "" {
		t.Fatal("ineligible monitors got a plan")
	}
	if p := PlanFor(custom, cfg); p.Reason != "" || strings.Join(p.Countries, ",") != "JP" {
		t.Fatalf("custom plan = %+v", p)
	}
}

func TestEvaluateUsesMonitorRules(t *testing.T) {
	m := httpMon("https://api.example.com")
	if ok, _ := evaluate(m, check.GPProbe{OK: true, Code: 200, Body: []byte(`{"status":"ok"}`)}); !ok {
		t.Error("healthy probe failed")
	}
	if ok, why := evaluate(m, check.GPProbe{OK: true, Code: 200, Body: []byte(`{"status":"down"}`)}); ok || why == "" {
		t.Error("keyword mismatch passed")
	}
	if ok, why := evaluate(m, check.GPProbe{OK: true, Code: 503}); ok || why != "HTTP 503" {
		t.Errorf("503 = %v %q", ok, why)
	}
	if ok, why := evaluate(m, check.GPProbe{Detail: "timeout"}); ok || why != "timeout" {
		t.Errorf("network failure = %v %q", ok, why)
	}
}

func newStore(t *testing.T) (*store.Store, *store.Writer) {
	t.Helper()
	box, _ := secret.New(bytes.Repeat([]byte{1}, 32))
	st, err := store.Open(t.TempDir(), box, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	w := st.NewWriter(slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.Start(context.Background())
	t.Cleanup(w.Close)
	return st, w
}

// A sweep where one country keeps failing raises one regional alert,
// stores the results, and a later clean sweep clears it.
func TestSweepRegionalIssue(t *testing.T) {
	st, w := newStore(t)
	sink := &capSink{}
	cfg := Config{Sweep: true, Alerts: true, Countries: []string{"US", "CN", "DE"}, TestsPerHour: 500}
	c := New(sink, st, w, func() Config { return cfg }, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now)
	m := httpMon("https://api.example.com/health")
	c.SetHooks(Hooks{
		Monitors: func() []*model.Monitor { return []*model.Monitor{m} },
		Monitor:  func(int64) (*model.Monitor, bool) { return m, true },
		Status:   func(int64) model.Status { return model.StatusUp },
	})
	cnDown := true
	var calls int
	c.run = func(_ context.Context, r check.GPRequest) ([]check.GPProbe, error) {
		calls++
		var out []check.GPProbe
		for _, cc := range r.Countries {
			p := check.GPProbe{Country: cc, City: "X", OK: true, Code: 200, Body: []byte(`{"status":"ok"}`), LatencyMs: 50}
			if cc == "CN" && cnDown {
				p = check.GPProbe{Country: cc, City: "Beijing", Detail: "timeout"}
			}
			out = append(out, p)
		}
		return out, nil
	}

	if wait := c.sweepNext(context.Background()); wait <= 0 {
		t.Fatalf("pace = %v", wait)
	}
	if calls != 2 { // sweep + re-check of the failing country
		t.Fatalf("calls = %d, want 2", calls)
	}
	if k := sink.kinds(); len(k) != 1 || k[0] != model.EvRegional || !strings.Contains(sink.evs[0].Reason, "China (Beijing) — timeout") {
		t.Fatalf("events = %+v", sink.evs)
	}
	iss := c.Issues()
	if len(iss[1]) != 1 || iss[1][0].Country != "CN" {
		t.Fatalf("issues = %+v", iss)
	}
	// A second failing sweep doesn't re-alert.
	c.sweepNext(context.Background())
	if len(sink.kinds()) != 1 {
		t.Fatalf("re-alerted: %v", sink.kinds())
	}
	cnDown = false
	c.sweepNext(context.Background())
	if k := sink.kinds(); len(k) != 2 || k[1] != model.EvRegionalCleared {
		t.Fatalf("events = %v", k)
	}
	w.Flush()
	all, _ := st.RegionResults(context.Background())
	if len(all[1]) != 3 {
		t.Fatalf("stored = %+v", all)
	}
}

// A blip that passes on the re-check never alerts.
func TestSweepBlipDoesNotAlert(t *testing.T) {
	st, w := newStore(t)
	sink := &capSink{}
	cfg := Config{Sweep: true, Alerts: true, Countries: []string{"US", "JP"}, TestsPerHour: 500}
	c := New(sink, st, w, func() Config { return cfg }, nil, time.Now)
	m := httpMon("https://api.example.com/health")
	c.SetHooks(Hooks{Monitors: func() []*model.Monitor { return []*model.Monitor{m} }, Status: func(int64) model.Status { return model.StatusUp }})
	first := true
	c.run = func(_ context.Context, r check.GPRequest) ([]check.GPProbe, error) {
		var out []check.GPProbe
		for _, cc := range r.Countries {
			p := check.GPProbe{Country: cc, OK: true, Code: 200, Body: []byte(`{"status":"ok"}`)}
			if cc == "JP" && first {
				p = check.GPProbe{Country: cc, OK: true, Code: 502}
			}
			out = append(out, p)
		}
		first = false
		return out, nil
	}
	c.sweepNext(context.Background())
	if len(sink.kinds()) != 0 || len(c.Issues()) != 0 {
		t.Fatalf("blip alerted: %v %v", sink.kinds(), c.Issues())
	}
}

func TestConfirmOutage(t *testing.T) {
	st, w := newStore(t)
	w.Do(store.OpOpenOutage(store.Outage{ID: 7, MonitorID: 1, StartedAt: 1000, FirstReason: "HTTP 503"}))
	w.Flush()
	sink := &capSink{}
	cfg := Config{Enabled: true, Countries: []string{"US", "DE"}, TestsPerHour: 100}
	c := New(sink, st, w, func() Config { return cfg }, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now)
	m := httpMon("https://api.example.com/")
	c.SetHooks(Hooks{Monitor: func(int64) (*model.Monitor, bool) { return m, true }})
	c.run = func(_ context.Context, r check.GPRequest) ([]check.GPProbe, error) {
		return []check.GPProbe{
			{Country: "US", OK: true, Code: 200, Body: []byte(`{"status":"ok"}`)},
			{Country: "DE", OK: true, Code: 503},
		}, nil
	}
	c.confirm(context.Background(), job{outageID: 7, monitorID: 1})
	w.Flush()
	mid := int64(1)
	os, _ := st.ListOutages(context.Background(), store.OutageFilter{MonitorID: &mid, Limit: 5})
	if len(os) != 1 || os[0].Regions == nil || os[0].Regions.Total != 2 || os[0].Regions.Reachable != 1 {
		t.Fatalf("outage = %+v", os)
	}
}

func TestBudgetAndPace(t *testing.T) {
	now := time.Unix(0, 0)
	c := New(&capSink{}, nil, nil, func() Config { return Config{} }, nil, func() time.Time { return now })
	if !c.take(16, 16, 40) {
		t.Fatal("first take refused")
	}
	if c.take(16, 16, 40) { // 16+16 > 40-16
		t.Fatal("take over budget allowed")
	}
	if !c.take(16, 0, 40) { // confirmations may use the reserve
		t.Fatal("priority take refused")
	}
	if c.Usage() != 32 {
		t.Fatalf("usage = %d", c.Usage())
	}
	now = now.Add(time.Hour + time.Second)
	if c.Usage() != 0 {
		t.Fatal("usage should reset after an hour")
	}
	if p := pace(16, 209); p < 4*time.Minute || p > 5*time.Minute {
		t.Fatalf("pace = %v", p)
	}
}

// A 301 is followed to its Location, per country, like the monitor does.
func TestSweepFollowsRedirects(t *testing.T) {
	st, w := newStore(t)
	sink := &capSink{}
	cfg := Config{Sweep: true, Alerts: true, Countries: []string{"US", "DE"}, TestsPerHour: 500}
	c := New(sink, st, w, func() Config { return cfg }, nil, time.Now)
	m := httpMon("https://example.org/")
	m.Settings.Keyword = ""
	c.SetHooks(Hooks{Monitors: func() []*model.Monitor { return []*model.Monitor{m} }, Status: func(int64) model.Status { return model.StatusUp }})
	var hosts []string
	c.run = func(_ context.Context, r check.GPRequest) ([]check.GPProbe, error) {
		hosts = append(hosts, r.Target.Host)
		var out []check.GPProbe
		for _, cc := range r.Countries {
			if r.Target.Host == "example.org" {
				out = append(out, check.GPProbe{Country: cc, City: "X", OK: true, Code: 301, Location: "https://www.example.org/"})
			} else {
				out = append(out, check.GPProbe{Country: cc, OK: true, Code: 200})
			}
		}
		return out, nil
	}
	c.sweepNext(context.Background())
	if len(sink.kinds()) != 0 || len(c.Issues()) != 0 {
		t.Fatalf("redirect treated as failure: %v %v", sink.kinds(), c.Issues())
	}
	if strings.Join(hosts, ",") != "example.org,www.example.org" {
		t.Fatalf("hosts = %v", hosts)
	}

	// With redirects disabled on the monitor, a 301 is judged as-is.
	zero := 0
	m.Settings.MaxRedirects = &zero
	c.lastSwept = map[int64]time.Time{}
	c.sweepNext(context.Background())
	if len(c.Issues()[1]) != 2 {
		t.Fatalf("issues = %v", c.Issues())
	}
}

// The UI calls .filter on countries and results, so an ineligible monitor
// (local-only, TCP, internal host...) must serialise them as [] not null.
func TestMonitorStatusNeverNullArrays(t *testing.T) {
	st, w := newStore(t)
	cfg := Config{Countries: []string{"US", "DE"}}
	c := New(&capSink{}, st, w, func() Config { return cfg }, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now)
	legacyLocal := httpMon("https://api.example.com")
	legacyLocal.Settings.RegionMode = "india" // legacy persisted value
	tcp := model.NewMonitor(model.TypeTCP)
	tcp.Target = "example.com:443"
	for name, m := range map[string]*model.Monitor{"legacy-local": legacyLocal, "tcp": tcp, "private": httpMon("http://10.1.2.3/x")} {
		b, err := json.Marshal(c.MonitorStatus(m))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte("null")) {
			t.Errorf("%s: status JSON contains null: %s", name, b)
		}
	}
}
