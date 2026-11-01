package hub

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"log/slog"

	"opskest/internal/engine"
	"opskest/internal/model"
	"opskest/internal/store"
)

// fakeSource is a controllable Source for hub tests.
type fakeSource struct {
	mu        sync.RWMutex
	summaries []engine.MonitorSummary
	groups    []store.Group
	outages   []store.Outage
	maint     []store.Maintenance
	clusters  []engine.Cluster
	online    bool
	since     time.Time
	dirty     engine.Dirty
}

func (f *fakeSource) Summaries() []engine.MonitorSummary {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return append([]engine.MonitorSummary(nil), f.summaries...)
}
func (f *fakeSource) Summary(id int64) (engine.MonitorSummary, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, s := range f.summaries {
		if s.ID == id {
			return s, true
		}
	}
	return engine.MonitorSummary{}, false
}
func (f *fakeSource) TakeDirty() engine.Dirty {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.dirty
	f.dirty = engine.Dirty{}
	return d
}
func (f *fakeSource) setDirty(d engine.Dirty) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dirty = d
}
func (f *fakeSource) Groups() []store.Group {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.groups
}
func (f *fakeSource) ActiveOutages() []store.Outage {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.outages
}
func (f *fakeSource) ActiveMaintenance() []store.Maintenance {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.maint
}
func (f *fakeSource) Clusters() []engine.Cluster {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.clusters
}
func (f *fakeSource) Connectivity() (bool, time.Time) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.online, f.since
}

func testHub(src *fakeSource) (*Hub, func() time.Time) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nowFn := func() time.Time { return now }
	h := New(src, "test-v1", nowFn, slog.Default())
	return h, nowFn
}

func TestSnapshotThenDelta(t *testing.T) {
	src := &fakeSource{
		summaries: []engine.MonitorSummary{
			{ID: 1, Name: "A", Status: model.StatusUp, TagIDs: []int64{}},
			{ID: 2, Name: "B", Status: model.StatusDown, TagIDs: []int64{}},
		},
		groups: []store.Group{{ID: 1, Name: "g1"}},
		online: true,
		since:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	h, _ := testHub(src)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeAll(w, r)
	}))
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	// Read the snapshot event.
	ev := readSSEEvent(t, scanner)
	if ev["type"] != "snapshot" {
		t.Fatalf("expected snapshot, got %v", ev["type"])
	}
	monitors := ev["monitors"].([]any)
	if len(monitors) != 2 {
		t.Fatalf("expected 2 monitors in snapshot, got %d", len(monitors))
	}

	// Mark monitor 1 as dirty and wait for a delta.
	src.setDirty(engine.Dirty{Changed: []int64{1}})
	ev = readSSEEvent(t, scanner)
	if ev["type"] != "delta" {
		t.Fatalf("expected delta, got %v", ev["type"])
	}
	deltaMonitors := ev["monitors"].([]any)
	if len(deltaMonitors) != 1 {
		t.Fatalf("expected 1 monitor in delta, got %d", len(deltaMonitors))
	}
	m := deltaMonitors[0].(map[string]any)
	if int64(m["id"].(float64)) != 1 {
		t.Fatalf("expected delta for monitor 1, got %v", m["id"])
	}
}

func TestNoDeltaWhenIdle(t *testing.T) {
	src := &fakeSource{
		summaries: []engine.MonitorSummary{
			{ID: 1, Name: "A", Status: model.StatusUp, TagIDs: []int64{}},
		},
		online: true,
		since:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	h, _ := testHub(src)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeAll(w, r)
	}))
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	// Read snapshot.
	ev := readSSEEvent(t, scanner)
	if ev["type"] != "snapshot" {
		t.Fatalf("expected snapshot, got %v", ev["type"])
	}

	lines := make(chan string, 10)
	go func() {
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	// Wait 2.5s — no delta should come since nothing is dirty.
	time.Sleep(2500 * time.Millisecond)

	select {
	case line := <-lines:
		if strings.HasPrefix(line, "event: delta") {
			t.Fatalf("unexpected delta event when idle: %s", line)
		}
	default:
		// Correct: no delta was sent.
	}
}

func TestScopedSanitizes(t *testing.T) {
	src := &fakeSource{
		summaries: []engine.MonitorSummary{
			{ID: 1, Name: "A", Status: model.StatusDown, Target: "https://secret-host.internal",
				Reason: "HTTP 503 from secret-host", Runbook: "check logs", TagIDs: []int64{1}, LatencyMs: 42},
			{ID: 2, Name: "B", Status: model.StatusUp, TagIDs: []int64{}},
		},
		online: true,
		since:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	h, _ := testHub(src)

	// Public scope: no reason, no latency by default.
	sc := PageScope{
		MonitorIDs:  map[int64]string{1: ""},
		Public:      true,
		ShowLatency: false,
	}
	snap := h.scopedSnapshot(sc)
	body := string(snap)
	if strings.Contains(body, "secret-host") {
		t.Error("public snapshot leaks target/reason")
	}
	if strings.Contains(body, "latencyMs") {
		t.Error("public snapshot without showLatency should not include latencyMs")
	}
	if strings.Contains(body, "runbook") {
		t.Error("public snapshot should not include runbook")
	}
	if strings.Contains(body, "tagIds") {
		t.Error("public snapshot should not include tagIds")
	}

	// Public scope with showLatency.
	sc.ShowLatency = true
	snap = h.scopedSnapshot(sc)
	body = string(snap)
	if !strings.Contains(body, "latencyMs") {
		t.Error("public snapshot with showLatency should include latencyMs")
	}
	if strings.Contains(body, "secret-host") {
		t.Error("public snapshot with showLatency still leaks reason")
	}

	// Private scope: reason and latency included.
	sc.Public = false
	snap = h.scopedSnapshot(sc)
	body = string(snap)
	if !strings.Contains(body, "latencyMs") {
		t.Error("private scope should include latency")
	}
}

func TestSlowClientDropped(t *testing.T) {
	src := &fakeSource{
		summaries: []engine.MonitorSummary{
			{ID: 1, Name: "A", Status: model.StatusUp, TagIDs: []int64{}},
		},
		online: true,
		since:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	h, _ := testHub(src)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx)

	// Add a client that never reads.
	c := h.addClient()
	_ = c

	// Send more than clientBufSize messages.
	for i := 0; i < clientBufSize+5; i++ {
		src.setDirty(engine.Dirty{Changed: []int64{1}})
		h.fanOut(h.buildDelta(src.TakeDirty()))
	}

	// The slow client should have been dropped.
	if h.ClientCount() != 0 {
		t.Errorf("expected 0 clients after slow client dropped, got %d", h.ClientCount())
	}
}

// TestPingComment proves the hub sends an SSE comment ping every 15s of
// simulated time, to keep idle connections (and intermediate proxies) alive.
func TestPingComment(t *testing.T) {
	src := &fakeSource{
		summaries: []engine.MonitorSummary{
			{ID: 1, Name: "A", Status: model.StatusUp, TagIDs: []int64{}},
		},
		online: true,
		since:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	var now time.Time
	var mu sync.Mutex
	nowFn := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	mu.Lock()
	now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mu.Unlock()

	h := New(src, "test-v1", nowFn, slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeAll(w, r)
	}))
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	// Read the initial snapshot.
	_ = readSSEEvent(t, scanner)

	lines := make(chan string, 10)
	go func() {
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	// Advance the fake clock past the 15s ping interval and let the hub's
	// real-time ticker (1s) catch up to it.
	mu.Lock()
	now = now.Add(16 * time.Second)
	mu.Unlock()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case line := <-lines:
			if strings.HasPrefix(line, ": ping") {
				return
			}
		case <-deadline:
			t.Fatal("no ping comment received within 3s")
		}
	}
}

// readSSEEvent reads lines from the scanner until it finds a complete SSE
// event (two consecutive newlines). It returns the parsed JSON data.
func readSSEEvent(t *testing.T, scanner *bufio.Scanner) map[string]any {
	t.Helper()
	var dataLine string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			dataLine = strings.TrimPrefix(line, "data: ")
		}
		if line == "" && dataLine != "" {
			var m map[string]any
			if err := json.Unmarshal([]byte(dataLine), &m); err != nil {
				t.Fatalf("invalid JSON in SSE data: %v\ndata: %s", err, dataLine)
			}
			return m
		}
	}
	t.Fatal("scanner exhausted without finding a complete SSE event")
	return nil
}

// A public page's stream keeps pinging while the page is quiet (so a proxy's
// idle timeout does not cut it), and many viewers share one snapshot.
func TestScopedStreamPingsAndShares(t *testing.T) {
	src := &fakeSource{
		summaries: []engine.MonitorSummary{{ID: 1, Name: "A", Status: model.StatusUp, TagIDs: []int64{}}},
		online:    true,
		since:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	var now time.Time
	var mu sync.Mutex
	nowFn := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	mu.Lock()
	now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mu.Unlock()

	h := New(src, "test-v1", nowFn, slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx)

	builds := 0
	scope := func() PageScope {
		builds++
		return PageScope{MonitorIDs: map[int64]string{1: ""}, Public: true}
	}
	a, va := h.scopedFor("p:1", scope)
	b, vb := h.scopedFor("p:1", scope)
	if builds != 1 || va != vb || &a[0] != &b[0] {
		t.Fatalf("same generation rebuilt: builds=%d versions=%d,%d", builds, va, vb)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeScoped(w, r, "p:1", scope)
	}))
	defer ts.Close()
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	_ = readSSEEvent(t, scanner)

	lines := make(chan string, 10)
	go func() {
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	mu.Lock()
	now = now.Add(16 * time.Second)
	mu.Unlock()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case line := <-lines:
			if strings.HasPrefix(line, ": ping") {
				return
			}
		case <-deadline:
			t.Fatal("a quiet public stream sent no ping within 3s")
		}
	}
}
