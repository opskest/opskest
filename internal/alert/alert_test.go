package alert

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opskest/internal/model"
	"opskest/internal/secret"
	"opskest/internal/store"
)

var update = flag.Bool("update", false, "update golden files")

// ---- shared fixtures -------------------------------------------------

// fixedDownItem/fixedDownMessage/fixedRecoveredMessage are the fixed
// fixture every golden test renders, so goldens stay directly comparable
// across channel types: monitor id 42 (dedup_key/alias "opskest-42"),
// name "API Gateway", reason "HTTP 503", app URL
// https://uptime.example.com.
func fixedDownItem() Item {
	since := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	return Item{
		MonitorID: 42,
		Name:      "API Gateway",
		Group:     "Payments",
		Target:    "https://api.example.com",
		Runbook:   "https://runbook.example.com/api-gateway",
		Status:    "down",
		Reason:    "HTTP 503",
		Since:     since,
		LatencyMs: 840,
		URL:       "https://uptime.example.com/monitors/42",
	}
}

func fixedDownMessage() Message {
	it := fixedDownItem()
	m := Message{Kind: model.EvDown, Items: []Item{it}, At: it.Since, AppURL: "https://uptime.example.com"}
	m.Title = Title(m)
	return m
}

func fixedRecoveredMessage() Message {
	it := fixedDownItem()
	it.Status = "up"
	it.Reason = ""
	it.Duration = 12 * time.Minute
	it.LatencyMs = 210
	m := Message{Kind: model.EvRecovered, Items: []Item{it}, At: it.Since.Add(12 * time.Minute), AppURL: "https://uptime.example.com"}
	m.Title = Title(m)
	return m
}

// ---- golden test harness ----------------------------------------------

// capturedRequest is what every HTTP-based golden test compares: the
// method, path (with query string), the non-boilerplate headers, and the
// parsed JSON body.
type capturedRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    any               `json:"body"`
}

// skipCaptureHeaders are headers every HTTP client sets that carry no
// information about what the sender chose to send.
var skipCaptureHeaders = map[string]bool{
	"User-Agent":      true,
	"Content-Length":  true,
	"Accept-Encoding": true,
	"Connection":      true,
	"Host":            true,
}

func captureServer(t *testing.T) (*httptest.Server, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		captured.Method = r.Method
		captured.Path = r.URL.Path
		if r.URL.RawQuery != "" {
			captured.Path += "?" + r.URL.RawQuery
		}
		captured.Headers = map[string]string{}
		for k, v := range r.Header {
			if skipCaptureHeaders[k] || len(v) == 0 {
				continue
			}
			captured.Headers[k] = v[0]
		}
		if len(body) > 0 {
			var v any
			if err := json.Unmarshal(body, &v); err == nil {
				captured.Body = v
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	return srv, captured
}

// checkGolden compares captured (marshaled indented) against
// testdata/name, parsing both sides back to a generic value first so the
// comparison is insensitive to key ordering. With -update it rewrites the
// golden file instead of comparing.
func checkGolden(t *testing.T, name string, captured *capturedRequest) {
	t.Helper()
	path := filepath.Join("testdata", name)

	got, err := json.MarshalIndent(captured, "", "  ")
	if err != nil {
		t.Fatalf("marshal captured request: %v", err)
	}
	got = append(got, '\n')

	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run with -update to create it): %v", path, err)
	}

	var wantV, gotV any
	if err := json.Unmarshal(want, &wantV); err != nil {
		t.Fatalf("parse golden %s: %v", path, err)
	}
	if err := json.Unmarshal(got, &gotV); err != nil {
		t.Fatalf("parse captured request: %v", err)
	}
	if !reflect.DeepEqual(wantV, gotV) {
		t.Errorf("%s mismatch\n got:\n%s\nwant:\n%s", name, got, want)
	}
}

// bodyContains reports whether captured's JSON body, re-marshaled, contains
// substr anywhere — a cheap way to assert presence of a value (the
// runbook, the group breadcrumb, ...) without caring exactly which field
// or block it landed in.
func bodyContains(t *testing.T, captured *capturedRequest, substr string) bool {
	t.Helper()
	raw, err := json.Marshal(captured.Body)
	if err != nil {
		t.Fatalf("marshal captured body: %v", err)
	}
	return strings.Contains(string(raw), substr)
}

func TestSlackDownGolden(t *testing.T) {
	srv, captured := captureServer(t)
	defer srv.Close()

	cfg := store.ChannelConfig{URL: srv.URL}
	if err := (slackSender{}).Send(context.Background(), cfg, fixedDownMessage()); err != nil {
		t.Fatalf("send: %v", err)
	}
	checkGolden(t, "slack_down.golden.json", captured)

	// Every message carries the runbook.
	if !bodyContains(t, captured, "runbook.example.com") {
		t.Errorf("expected the slack payload to render the runbook, got body %+v", captured.Body)
	}
	// #2: a standalone (non-grouped) message shows its group.
	if !bodyContains(t, captured, "Group:* Payments") {
		t.Errorf("expected the slack payload to show the group, got body %+v", captured.Body)
	}
}

func TestTeamsDownGolden(t *testing.T) {
	srv, captured := captureServer(t)
	defer srv.Close()

	cfg := store.ChannelConfig{URL: srv.URL}
	if err := (teamsSender{}).Send(context.Background(), cfg, fixedDownMessage()); err != nil {
		t.Fatalf("send: %v", err)
	}
	checkGolden(t, "teams_down.golden.json", captured)

	if !bodyContains(t, captured, "runbook.example.com") {
		t.Errorf("expected the teams card to render the runbook, got body %+v", captured.Body)
	}
	if !bodyContains(t, captured, "Group: Payments") {
		t.Errorf("expected the teams card to show the group, got body %+v", captured.Body)
	}
}

func TestGChatDownGolden(t *testing.T) {
	srv, captured := captureServer(t)
	defer srv.Close()

	cfg := store.ChannelConfig{URL: srv.URL}
	if err := (gchatSender{}).Send(context.Background(), cfg, fixedDownMessage()); err != nil {
		t.Fatalf("send: %v", err)
	}
	checkGolden(t, "gchat_down.golden.json", captured)

	if !bodyContains(t, captured, "runbook.example.com") {
		t.Errorf("expected the gchat card to render the runbook, got body %+v", captured.Body)
	}
	if !bodyContains(t, captured, "Group: Payments") {
		t.Errorf("expected the gchat card to show the group, got body %+v", captured.Body)
	}
}

func TestWebhookDownGolden(t *testing.T) {
	srv, captured := captureServer(t)
	defer srv.Close()

	cfg := store.ChannelConfig{URL: srv.URL, HMACSecret: "s3cr3t"}
	if err := (webhookSender{}).Send(context.Background(), cfg, fixedDownMessage()); err != nil {
		t.Fatalf("send: %v", err)
	}
	checkGolden(t, "webhook_down.golden.json", captured)

	// The signature must be over the exact body bytes sent, with the
	// configured secret.
	sig, ok := captured.Headers["X-Opskest-Signature"]
	if !ok {
		for k, v := range captured.Headers {
			if strings.EqualFold(k, "X-Opskest-Signature") {
				sig, ok = v, true
			}
		}
	}
	if !ok || !strings.HasPrefix(sig, "sha256=") {
		t.Errorf("expected X-Opskest-Signature header starting with sha256=, got %q (headers=%v)", sig, captured.Headers)
	}

	// #1: the webhook JSON carries the runbook as its own field.
	if m, ok := captured.Body.(map[string]any); ok {
		if mon, ok := m["monitor"].(map[string]any); ok {
			if mon["runbook"] != "https://runbook.example.com/api-gateway" {
				t.Errorf("expected monitor.runbook, got %+v", mon)
			}
		} else {
			t.Errorf("expected a monitor object in the webhook body, got %+v", m)
		}
	} else {
		t.Fatalf("expected the webhook body to be a JSON object, got %T", captured.Body)
	}
}

func TestPagerDutyGolden(t *testing.T) {
	srv, captured := captureServer(t)
	defer srv.Close()

	orig := pagerdutyEventsURL
	pagerdutyEventsURL = srv.URL
	t.Cleanup(func() { pagerdutyEventsURL = orig })

	cfg := store.ChannelConfig{RoutingKey: "rk-1"}

	if err := (pagerdutySender{}).Send(context.Background(), cfg, fixedDownMessage()); err != nil {
		t.Fatalf("send down: %v", err)
	}
	checkGolden(t, "pagerduty_down.golden.json", captured)
	// #1: custom_details plus a summary suffix carry the runbook.
	if !bodyContains(t, captured, "runbook.example.com") {
		t.Errorf("expected the pagerduty down payload to render the runbook, got body %+v", captured.Body)
	}

	*captured = capturedRequest{}
	if err := (pagerdutySender{}).Send(context.Background(), cfg, fixedRecoveredMessage()); err != nil {
		t.Fatalf("send recovered: %v", err)
	}
	checkGolden(t, "pagerduty_recovered.golden.json", captured)
	if !bodyContains(t, captured, "runbook.example.com") {
		t.Errorf("expected the pagerduty recovered payload to render the runbook, got body %+v", captured.Body)
	}
}

func TestOpsgenieGolden(t *testing.T) {
	srv, captured := captureServer(t)
	defer srv.Close()

	orig := opsgenieBaseURLUS
	opsgenieBaseURLUS = srv.URL
	t.Cleanup(func() { opsgenieBaseURLUS = orig })

	cfg := store.ChannelConfig{APIKey: "key-1", Region: "us"}

	if err := (opsgenieSender{}).Send(context.Background(), cfg, fixedDownMessage()); err != nil {
		t.Fatalf("send down: %v", err)
	}
	checkGolden(t, "opsgenie_down.golden.json", captured)
	// #1 and #3: the description carries both the runbook and the full
	// target.
	if !bodyContains(t, captured, "runbook.example.com") {
		t.Errorf("expected the opsgenie description to render the runbook, got body %+v", captured.Body)
	}
	if !bodyContains(t, captured, "https://api.example.com") {
		t.Errorf("expected the opsgenie description to render the target, got body %+v", captured.Body)
	}

	*captured = capturedRequest{}
	if err := (opsgenieSender{}).Send(context.Background(), cfg, fixedRecoveredMessage()); err != nil {
		t.Fatalf("send recovered: %v", err)
	}
	checkGolden(t, "opsgenie_recovered.golden.json", captured)
}

// ---- email --------------------------------------------------------

// fakeSMTPServer speaks just enough SMTP (EHLO/MAIL/RCPT/DATA/QUIT, no
// TLS) to let net/smtp complete a send, and reports the DATA payload it
// received on the returned channel.
func fakeSMTPServer(t *testing.T) (addr string, data chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	data = make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		writeLine := func(s string) { fmt.Fprintf(conn, "%s\r\n", s) }
		writeLine("220 localhost opskest test SMTP")

		var body strings.Builder
		inData := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")

			if inData {
				if line == "." {
					inData = false
					writeLine("250 OK")
					data <- body.String()
					continue
				}
				body.WriteString(line + "\n")
				continue
			}

			switch upper := strings.ToUpper(line); {
			case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
				writeLine("250-localhost")
				writeLine("250 OK")
			case strings.HasPrefix(upper, "MAIL FROM"):
				writeLine("250 OK")
			case strings.HasPrefix(upper, "RCPT TO"):
				writeLine("250 OK")
			case upper == "DATA":
				writeLine("354 Start mail input")
				inData = true
			case upper == "QUIT":
				writeLine("221 Bye")
				return
			default:
				writeLine("250 OK")
			}
		}
	}()
	return ln.Addr().String(), data
}

func TestEmailSMTP(t *testing.T) {
	addr, dataCh := fakeSMTPServer(t)
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	cfg := store.ChannelConfig{
		Host:    host,
		Port:    port,
		From:    "alerts@opskest.example",
		To:      []string{"oncall@opskest.example"},
		TLSMode: "none",
	}
	msg := fixedDownMessage()

	if err := (emailSender{}).Send(context.Background(), cfg, msg); err != nil {
		t.Fatalf("send: %v", err)
	}

	select {
	case raw := <-dataCh:
		if !strings.Contains(raw, "Subject: "+Title(msg)) {
			t.Errorf("DATA missing expected Subject header:\n%s", raw)
		}
		if !strings.Contains(raw, "multipart/alternative") {
			t.Errorf("DATA missing multipart/alternative content type:\n%s", raw)
		}
		if !strings.Contains(raw, "text/plain") || !strings.Contains(raw, "text/html") {
			t.Errorf("DATA missing text and html parts:\n%s", raw)
		}
		if !strings.Contains(raw, "API Gateway") || !strings.Contains(raw, "HTTP 503") {
			t.Errorf("DATA missing monitor name/reason:\n%s", raw)
		}
		// #1: every message carries the runbook, in both the text and
		// HTML parts.
		if strings.Count(raw, "runbook.example.com") < 2 {
			t.Errorf("DATA missing the runbook in both text and html parts:\n%s", raw)
		}
		// #2: a standalone message shows its group.
		if !strings.Contains(raw, "Group: Payments") {
			t.Errorf("DATA missing the \"Group: Payments\" line:\n%s", raw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the fake SMTP server to receive DATA")
	}
}

// TestEmailRespectsContextCancellation covers the SMTP minor: net/smtp's
// Client does blocking I/O on the raw conn and knows nothing about ctx, so
// without actively closing the conn on ctx.Done, canceling ctx (with no
// deadline set on the conn) would leave Send blocked forever waiting for a
// reply the server never sends.
func TestEmailRespectsContextCancellation(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprintf(conn, "220 localhost opskest test SMTP\r\n")
		// Swallow every command without ever replying, so the client's
		// blocking read for a response never returns on its own — only
		// Send closing the conn (via ctx.Done) ends this loop.
		buf := make([]byte, 4096)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}()

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	cfg := store.ChannelConfig{
		Host:    host,
		Port:    port,
		From:    "alerts@opskest.example",
		To:      []string{"oncall@opskest.example"},
		TLSMode: "none",
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (emailSender{}).Send(ctx, cfg, fixedDownMessage())
	}()

	time.Sleep(100 * time.Millisecond) // let it block waiting for EHLO's reply
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error once the context was canceled mid-send")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Send did not return within 2s of context cancellation — it isn't closing the conn on ctx.Done")
	}
}

// ---- dispatcher test helpers ---------------------------------------

func newTestStore(t *testing.T) (*store.Store, *store.Writer) {
	t.Helper()

	key := bytes.Repeat([]byte{0x01}, 32)
	box, err := secret.New(key)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	st, err := store.Open(t.TempDir(), box, time.UTC)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})

	w := st.NewWriter(slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.Start(context.Background())
	t.Cleanup(w.Close)

	return st, w
}

// fakeLookup is a test double for Lookup: a plain map guarded by a mutex,
// set up by each test with exactly the monitors it needs.
type fakeLookup struct {
	mu   sync.Mutex
	info map[int64]Info
}

func newFakeLookup() *fakeLookup { return &fakeLookup{info: map[int64]Info{}} }

func (f *fakeLookup) set(id int64, info Info) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.info[id] = info
}

func (f *fakeLookup) AlertInfo(id int64) (Info, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	info, ok := f.info[id]
	return info, ok
}

// captureSender is a Sender test double that records every Message it
// was asked to send and always succeeds, so dispatcher-level tests can
// assert on Message.Grouped/Items directly instead of parsing rendered
// wire formats.
type captureSender struct {
	mu   sync.Mutex
	msgs []Message
}

func (c *captureSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	c.mu.Lock()
	c.msgs = append(c.msgs, m)
	c.mu.Unlock()
	return nil
}

func (c *captureSender) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.msgs)
}

func (c *captureSender) all() []Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Message, len(c.msgs))
	copy(out, c.msgs)
	return out
}

// swapSender replaces the registered Sender for channelType for the
// duration of the test.
func swapSender(t *testing.T, channelType string, s Sender) {
	t.Helper()
	orig := senders[channelType]
	senders[channelType] = s
	t.Cleanup(func() { senders[channelType] = orig })
}

func createTestChannel(t *testing.T, st *store.Store, name, typ string, cfg store.ChannelConfig) store.Channel {
	t.Helper()
	ch := &store.Channel{Name: name, Type: typ, Config: cfg}
	if err := st.CreateChannel(context.Background(), ch); err != nil {
		t.Fatalf("create channel %s: %v", name, err)
	}
	return *ch
}

// waitFor polls cond every 5ms until it returns true or timeout elapses,
// failing the test otherwise.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %v", timeout)
	}
}

// ---- dispatcher tests -------------------------------------------------

func TestDispatcherRetriesAndLogs(t *testing.T) {
	st, w := newTestStore(t)

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}
		rw.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch := createTestChannel(t, st, "c1", "webhook", store.ChannelConfig{URL: srv.URL})

	lk := newFakeLookup()
	lk.set(1, Info{Name: "Mon 1", ChannelIDs: []int64{ch.ID}})

	d := NewDispatcher(st, w, lk, Options{
		AppURL: "https://uptime.example.com",
		Retry:  []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond},
		Hold:   5 * time.Millisecond,
	})
	d.Start(context.Background())
	defer d.Stop()

	d.Publish(model.Event{Kind: model.EvDown, MonitorID: 1, At: time.Now(), Reason: "boom"})

	var entries []store.AlertLogEntry
	waitFor(t, 3*time.Second, func() bool {
		w.Flush()
		var err error
		entries, err = st.ListAlertLog(context.Background(), 50, 0)
		if err != nil {
			t.Fatalf("list alert log: %v", err)
		}
		return len(entries) >= 3
	})
	if len(entries) != 3 {
		t.Fatalf("got %d alert_log entries, want 3", len(entries))
	}

	byAttempt := map[int]bool{}
	for _, e := range entries {
		byAttempt[e.Attempt] = e.OK
	}
	if want := map[int]bool{1: false, 2: false, 3: true}; !reflect.DeepEqual(byAttempt, want) {
		t.Errorf("ok-by-attempt = %+v, want %+v", byAttempt, want)
	}
}

func TestDispatcherTimeout(t *testing.T) {
	st, w := newTestStore(t)

	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		rw.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch := createTestChannel(t, st, "c1", "webhook", store.ChannelConfig{URL: srv.URL})

	lk := newFakeLookup()
	lk.set(1, Info{Name: "Mon 1", ChannelIDs: []int64{ch.ID}})

	d := NewDispatcher(st, w, lk, Options{
		AppURL:  "https://uptime.example.com",
		Timeout: 50 * time.Millisecond,
		Retry:   []time.Duration{},
		Hold:    5 * time.Millisecond,
	})
	d.Start(context.Background())
	defer d.Stop()

	start := time.Now()
	d.Publish(model.Event{Kind: model.EvDown, MonitorID: 1, At: time.Now(), Reason: "boom"})
	if elapsed := time.Since(start); elapsed > 5*time.Millisecond {
		t.Errorf("Publish took %v, want < 5ms (it must never block)", elapsed)
	}

	var entries []store.AlertLogEntry
	waitFor(t, 3*time.Second, func() bool {
		w.Flush()
		var err error
		entries, err = st.ListAlertLog(context.Background(), 50, 0)
		if err != nil {
			t.Fatalf("list alert log: %v", err)
		}
		return len(entries) >= 1
	})
	if len(entries) != 1 {
		t.Fatalf("got %d alert_log entries, want 1", len(entries))
	}
	if entries[0].OK {
		t.Errorf("expected the timed-out attempt to be logged as failed")
	}
	if !strings.Contains(entries[0].Error, "timed out") {
		t.Errorf("expected a timeout error, got %q", entries[0].Error)
	}
}

// TestChannelFailureErrorTextIsInformative is finding #5: alert_log.error
// must stay informative enough for the UI to show why a channel failed —
// the HTTP status and (redacted) response body, exactly as the finding's
// own example puts it.
func TestChannelFailureErrorTextIsInformative(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("channel_not_found"))
	}))
	defer srv.Close()

	err := (slackSender{}).Send(context.Background(), store.ChannelConfig{URL: srv.URL}, fixedDownMessage())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got, want := err.Error(), "http 404: channel_not_found"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}

// TestChannelURLAndTokenNeverLeakIntoAlertLog is finding #5's other half:
// a webhook URL carrying a secret token must never end up in alert_log,
// even when the underlying transport error would otherwise echo it back.
func TestChannelURLAndTokenNeverLeakIntoAlertLog(t *testing.T) {
	st, w := newTestStore(t)

	// A listener that's immediately closed: every request to its address
	// fails at the network level (connection refused) before any HTTP
	// request is even framed.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	secretToken := "sup3rSecretWebhookToken1234567890abcXYZ"
	badURL := "https://" + addr + "/hook/" + secretToken

	ch := createTestChannel(t, st, "wh", "webhook", store.ChannelConfig{URL: badURL})

	lk := newFakeLookup()
	lk.set(1, Info{Name: "Mon 1", ChannelIDs: []int64{ch.ID}})

	d := NewDispatcher(st, w, lk, Options{
		AppURL: "https://uptime.example.com",
		Retry:  []time.Duration{},
		Hold:   5 * time.Millisecond,
	})
	d.Start(context.Background())
	defer d.Stop()

	d.Publish(model.Event{Kind: model.EvDown, MonitorID: 1, At: time.Now(), Reason: "boom"})

	var entries []store.AlertLogEntry
	waitFor(t, 3*time.Second, func() bool {
		w.Flush()
		entries, err = st.ListAlertLog(context.Background(), 50, 0)
		return len(entries) >= 1
	})
	if len(entries) != 1 {
		t.Fatalf("got %d alert_log entries, want 1", len(entries))
	}
	if entries[0].Error == "" {
		t.Fatal("expected a non-empty, informative error")
	}
	if strings.Contains(entries[0].Error, secretToken) {
		t.Errorf("alert_log.error leaked the webhook token: %q", entries[0].Error)
	}
	if strings.Contains(entries[0].Error, "https://") {
		t.Errorf("alert_log.error leaked the webhook URL: %q", entries[0].Error)
	}
}

// TestDispatcherUsesItsOwnConfiguredHTTPClient is finding #6: the HTTP
// client a Sender uses must come from the Dispatcher it was configured
// with, not a package-level variable shared (and mutated) across every
// Dispatcher.
func TestDispatcherUsesItsOwnConfiguredHTTPClient(t *testing.T) {
	st, w := newTestStore(t)

	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Test-Client")
		rw.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch := createTestChannel(t, st, "c1", "webhook", store.ChannelConfig{URL: srv.URL})

	lk := newFakeLookup()
	lk.set(1, Info{Name: "Mon 1", ChannelIDs: []int64{ch.ID}})

	client := &http.Client{Transport: headerInjectingTransport{header: "X-Test-Client", value: "custom"}}

	d := NewDispatcher(st, w, lk, Options{
		AppURL: "https://uptime.example.com",
		HTTP:   client,
		Retry:  []time.Duration{},
		Hold:   5 * time.Millisecond,
	})
	d.Start(context.Background())
	defer d.Stop()

	d.Publish(model.Event{Kind: model.EvDown, MonitorID: 1, At: time.Now(), Reason: "boom"})

	waitFor(t, 3*time.Second, func() bool {
		w.Flush()
		entries, _ := st.ListAlertLog(context.Background(), 50, 0)
		return len(entries) >= 1
	})

	if gotHeader != "custom" {
		t.Errorf("expected the dispatcher's configured HTTP client to be used, got X-Test-Client=%q", gotHeader)
	}
}

// headerInjectingTransport is a minimal http.RoundTripper test double that
// tags every request, proving which *http.Client actually sent it.
type headerInjectingTransport struct {
	header, value string
}

func (h headerInjectingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set(h.header, h.value)
	return http.DefaultTransport.RoundTrip(req)
}

// TestSendTestLeavesMonitorIDNull covers the SendTest minor: a test send
// isn't about any real monitor, so alert_log.monitor_id must be NULL, not
// the zero value of the placeholder Item SendTest builds.
func TestSendTestLeavesMonitorIDNull(t *testing.T) {
	st, w := newTestStore(t)

	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch := createTestChannel(t, st, "c1", "webhook", store.ChannelConfig{URL: srv.URL})

	d := NewDispatcher(st, w, newFakeLookup(), Options{AppURL: "https://uptime.example.com"})
	d.Start(context.Background())
	defer d.Stop()

	if err := d.SendTest(context.Background(), ch); err != nil {
		t.Fatalf("SendTest: %v", err)
	}

	w.Flush()
	entries, err := st.ListAlertLog(context.Background(), 50, 0)
	if err != nil {
		t.Fatalf("list alert log: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d alert_log entries, want 1", len(entries))
	}
	if entries[0].MonitorID != nil {
		t.Errorf("expected a NULL monitor_id for a test send, got %d", *entries[0].MonitorID)
	}
}

// TestReloadChannelsAfterStopIsNoop covers the minor: nothing should start
// new worker goroutines (which would never be waited on again) once the
// dispatcher has been stopped.
func TestReloadChannelsAfterStopIsNoop(t *testing.T) {
	st, w := newTestStore(t)
	createTestChannel(t, st, "c1", "webhook", store.ChannelConfig{URL: "https://example.com/hook"})

	d := NewDispatcher(st, w, newFakeLookup(), Options{AppURL: "https://uptime.example.com"})
	d.Start(context.Background())
	d.Stop()

	if err := d.ReloadChannels(context.Background()); err != nil {
		t.Fatalf("ReloadChannels after Stop: %v", err)
	}

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("ReloadChannels after Stop appears to have started new goroutines")
	}
}

func TestStormGroupsChatButNotPagerDuty(t *testing.T) {
	st, w := newTestStore(t)

	slackCh := createTestChannel(t, st, "slack", "slack", store.ChannelConfig{URL: "https://hooks.slack.com/x"})
	pdCh := createTestChannel(t, st, "pd", "pagerduty", store.ChannelConfig{RoutingKey: "rk"})

	lk := newFakeLookup()
	for i := int64(1); i <= 4; i++ {
		lk.set(i, Info{Name: fmt.Sprintf("Mon %d", i), ChannelIDs: []int64{slackCh.ID, pdCh.ID}})
	}

	slackCap := &captureSender{}
	pdCap := &captureSender{}
	swapSender(t, "slack", slackCap)
	swapSender(t, "pagerduty", pdCap)

	d := NewDispatcher(st, w, lk, Options{
		AppURL:   "https://uptime.example.com",
		Hold:     50 * time.Millisecond,
		Settings: func() (int, time.Duration) { return 3, 30 * time.Second },
	})
	d.Start(context.Background())
	defer d.Stop()

	for i := int64(1); i <= 4; i++ {
		d.Publish(model.Event{Kind: model.EvDown, MonitorID: i, At: time.Now(), Reason: "boom"})
	}

	waitFor(t, 3*time.Second, func() bool {
		return slackCap.count() >= 1 && pdCap.count() >= 4
	})

	if got := slackCap.count(); got != 1 {
		t.Fatalf("slack got %d messages, want exactly 1", got)
	}
	msg := slackCap.all()[0]
	if !msg.Grouped {
		t.Errorf("expected the slack message to be Grouped")
	}
	if len(msg.Items) != 4 {
		t.Errorf("expected 4 items in the grouped slack message, got %d", len(msg.Items))
	}

	if got := pdCap.count(); got != 4 {
		t.Fatalf("pagerduty got %d messages, want exactly 4", got)
	}
	for _, m := range pdCap.all() {
		if m.Grouped {
			t.Errorf("pagerduty must never receive a grouped message")
		}
		if len(m.Items) != 1 {
			t.Errorf("pagerduty message should carry exactly 1 item, got %d", len(m.Items))
		}
	}
}

func TestBelowThresholdSendsIndividually(t *testing.T) {
	st, w := newTestStore(t)

	slackCh := createTestChannel(t, st, "slack", "slack", store.ChannelConfig{URL: "https://hooks.slack.com/x"})

	lk := newFakeLookup()
	lk.set(1, Info{Name: "Mon 1", ChannelIDs: []int64{slackCh.ID}})
	lk.set(2, Info{Name: "Mon 2", ChannelIDs: []int64{slackCh.ID}})

	slackCap := &captureSender{}
	swapSender(t, "slack", slackCap)

	d := NewDispatcher(st, w, lk, Options{
		AppURL:   "https://uptime.example.com",
		Hold:     20 * time.Millisecond,
		Settings: func() (int, time.Duration) { return 3, 30 * time.Second },
	})
	d.Start(context.Background())
	defer d.Stop()

	d.Publish(model.Event{Kind: model.EvDown, MonitorID: 1, At: time.Now(), Reason: "boom"})
	d.Publish(model.Event{Kind: model.EvDown, MonitorID: 2, At: time.Now(), Reason: "boom"})

	waitFor(t, 3*time.Second, func() bool { return slackCap.count() >= 2 })

	if got := slackCap.count(); got != 2 {
		t.Fatalf("slack got %d messages, want exactly 2", got)
	}
	for _, m := range slackCap.all() {
		if m.Grouped {
			t.Errorf("expected individual (non-grouped) messages below threshold")
		}
		if len(m.Items) != 1 {
			t.Errorf("expected exactly 1 item per message, got %d", len(m.Items))
		}
	}
}

// TestStormThresholdCountsDistinctMonitors is finding #4's core
// regression: 3 raw Down events from only 2 distinct monitors (monitor 1
// flaps twice) must not reach a storm threshold of 3 monitors. With the
// old event-counting stormState, this published 3 events and grouped them
// wrongly.
func TestStormThresholdCountsDistinctMonitors(t *testing.T) {
	st, w := newTestStore(t)

	slackCh := createTestChannel(t, st, "slack", "slack", store.ChannelConfig{URL: "https://hooks.slack.com/x"})

	lk := newFakeLookup()
	lk.set(1, Info{Name: "Mon 1", ChannelIDs: []int64{slackCh.ID}})
	lk.set(2, Info{Name: "Mon 2", ChannelIDs: []int64{slackCh.ID}})

	slackCap := &captureSender{}
	swapSender(t, "slack", slackCap)

	d := NewDispatcher(st, w, lk, Options{
		AppURL:   "https://uptime.example.com",
		Hold:     50 * time.Millisecond,
		Settings: func() (int, time.Duration) { return 3, 30 * time.Second },
	})
	d.Start(context.Background())
	defer d.Stop()

	d.Publish(model.Event{Kind: model.EvDown, MonitorID: 1, At: time.Now(), Reason: "first flap"})
	d.Publish(model.Event{Kind: model.EvDown, MonitorID: 1, At: time.Now(), Reason: "second flap"})
	d.Publish(model.Event{Kind: model.EvDown, MonitorID: 2, At: time.Now(), Reason: "boom"})

	waitFor(t, 3*time.Second, func() bool { return slackCap.count() >= 2 })
	time.Sleep(100 * time.Millisecond) // give any wrongful grouping time to also land

	if got := slackCap.count(); got != 2 {
		t.Fatalf("slack got %d messages, want 2 individual messages (2 distinct monitors is below the 3-monitor storm threshold)", got)
	}
	for _, m := range slackCap.all() {
		if m.Grouped {
			t.Errorf("3 raw events from only 2 distinct monitors must not trigger a storm group")
		}
	}
}

// TestStormGroupedMessageNeverListsAMonitorTwice is finding #4: a monitor
// that flaps more than once within the hold window must appear once, not
// once per event, in a grouped storm message.
func TestStormGroupedMessageNeverListsAMonitorTwice(t *testing.T) {
	st, w := newTestStore(t)

	slackCh := createTestChannel(t, st, "slack", "slack", store.ChannelConfig{URL: "https://hooks.slack.com/x"})

	lk := newFakeLookup()
	for i := int64(1); i <= 3; i++ {
		lk.set(i, Info{Name: fmt.Sprintf("Mon %d", i), ChannelIDs: []int64{slackCh.ID}})
	}

	slackCap := &captureSender{}
	swapSender(t, "slack", slackCap)

	d := NewDispatcher(st, w, lk, Options{
		AppURL:   "https://uptime.example.com",
		Hold:     50 * time.Millisecond,
		Settings: func() (int, time.Duration) { return 3, 30 * time.Second },
	})
	d.Start(context.Background())
	defer d.Stop()

	// Monitor 1 flaps twice within the hold window; 2 and 3 fire once.
	d.Publish(model.Event{Kind: model.EvDown, MonitorID: 1, At: time.Now(), Reason: "first"})
	d.Publish(model.Event{Kind: model.EvDown, MonitorID: 1, At: time.Now(), Reason: "second"})
	d.Publish(model.Event{Kind: model.EvDown, MonitorID: 2, At: time.Now(), Reason: "boom"})
	d.Publish(model.Event{Kind: model.EvDown, MonitorID: 3, At: time.Now(), Reason: "boom"})

	waitFor(t, 3*time.Second, func() bool { return slackCap.count() >= 1 })

	if got := slackCap.count(); got != 1 {
		t.Fatalf("slack got %d messages, want exactly 1 grouped message", got)
	}
	msg := slackCap.all()[0]
	if !msg.Grouped {
		t.Fatalf("expected a grouped message")
	}
	if len(msg.Items) != 3 {
		t.Errorf("expected 3 distinct monitors in the grouped message (monitor 1's flap must not be listed twice), got %d", len(msg.Items))
	}
	seen := map[int64]bool{}
	for _, it := range msg.Items {
		if seen[it.MonitorID] {
			t.Errorf("monitor %d listed twice in the grouped message", it.MonitorID)
		}
		seen[it.MonitorID] = true
	}
}

// TestStormSingleSurvivorSendsAsIndividualMessage is finding #4: even
// while the system is globally storming, a channel whose own dedup'd set
// ends up with just one monitor must get a normal single message (so
// Title uses "🔴 Down — Mon 1", never "🔴 1 monitors down").
func TestStormSingleSurvivorSendsAsIndividualMessage(t *testing.T) {
	st, w := newTestStore(t)

	slackCh := createTestChannel(t, st, "slack", "slack", store.ChannelConfig{URL: "https://hooks.slack.com/x"})
	teamsCh := createTestChannel(t, st, "teams", "teams", store.ChannelConfig{URL: "https://example.webhook.office.com/y"})

	lk := newFakeLookup()
	lk.set(1, Info{Name: "Mon 1", ChannelIDs: []int64{slackCh.ID}})
	lk.set(2, Info{Name: "Mon 2", ChannelIDs: []int64{teamsCh.ID}})
	lk.set(3, Info{Name: "Mon 3", ChannelIDs: []int64{teamsCh.ID}})

	slackCap := &captureSender{}
	teamsCap := &captureSender{}
	swapSender(t, "slack", slackCap)
	swapSender(t, "teams", teamsCap)

	d := NewDispatcher(st, w, lk, Options{
		AppURL:   "https://uptime.example.com",
		Hold:     50 * time.Millisecond,
		Settings: func() (int, time.Duration) { return 3, 30 * time.Second },
	})
	d.Start(context.Background())
	defer d.Stop()

	for i := int64(1); i <= 3; i++ {
		d.Publish(model.Event{Kind: model.EvDown, MonitorID: i, At: time.Now(), Reason: "boom"})
	}

	waitFor(t, 3*time.Second, func() bool { return slackCap.count() >= 1 && teamsCap.count() >= 1 })

	if got := slackCap.count(); got != 1 {
		t.Fatalf("slack got %d messages, want exactly 1", got)
	}
	msg := slackCap.all()[0]
	if msg.Grouped {
		t.Errorf("expected a single (non-grouped) message: only 1 monitor reports to this channel")
	}
	if len(msg.Items) != 1 {
		t.Errorf("expected exactly 1 item, got %d", len(msg.Items))
	}
	if got := Title(msg); got != "🔴 Down — Mon 1" {
		t.Errorf("Title() = %q, want the singular DOWN title (\"1 monitors down\" must never appear)", got)
	}

	if got := teamsCap.count(); got != 1 {
		t.Fatalf("teams got %d messages, want exactly 1 grouped message", got)
	}
	if tm := teamsCap.all()[0]; !tm.Grouped || len(tm.Items) != 2 {
		t.Errorf("expected teams to get 1 grouped message with 2 items, got Grouped=%v len=%d", tm.Grouped, len(tm.Items))
	}
}

// ---- message content ---------------------------------------------------

func TestTitleFormats(t *testing.T) {
	base := Item{Name: "API Gateway", Days: 6, Duration: 12 * time.Minute}

	tests := []struct {
		name    string
		kind    model.EventKind
		grouped bool
		n       int
		want    string
	}{
		{"down single", model.EvDown, false, 1, "🔴 Down — API Gateway"},
		{"down grouped", model.EvDown, true, 7, "🔴 7 monitors down"},
		{"recovered single", model.EvRecovered, false, 1, "🟢 Up — API Gateway (was down 12m)"},
		{"recovered grouped", model.EvRecovered, true, 4, "🟢 4 monitors recovered"},
		{"degraded single", model.EvDegraded, false, 1, "🟡 Degraded — API Gateway"},
		{"degraded grouped", model.EvDegraded, true, 3, "🟡 3 monitors degraded"},
		{"degraded cleared single", model.EvDegradedCleared, false, 1, "🟢 Back to normal — API Gateway"},
		{"degraded cleared grouped", model.EvDegradedCleared, true, 2, "🟢 2 monitors back to normal"},
		{"reminder single", model.EvReminder, false, 1, "🔴 Still down — API Gateway (for 12m)"},
		{"reminder grouped", model.EvReminder, true, 5, "🔴 5 monitors still down"},
		{"cert expiring single", model.EvCertExpiring, false, 1, "⚠️ Certificate expires in 6 days: API Gateway"},
		{"cert expiring grouped", model.EvCertExpiring, true, 2, "⚠️ 2 certificates expiring soon"},
		{"domain expiring single", model.EvDomainExpiring, false, 1, "⚠️ Domain expires in 6 days: API Gateway"},
		{"domain expiring grouped", model.EvDomainExpiring, true, 2, "⚠️ 2 domains expiring soon"},
		{"test", model.EvTest, false, 1, "🔔 Test alert: API Gateway"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			items := make([]Item, tc.n)
			for i := range items {
				items[i] = base
			}
			m := Message{Kind: tc.kind, Items: items, Grouped: tc.grouped}
			if got := Title(m); got != tc.want {
				t.Errorf("Title() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateConfig(t *testing.T) {
	t.Run("slack without url fails", func(t *testing.T) {
		fe := ValidateConfig("slack", store.ChannelConfig{})
		if fe["url"] == "" {
			t.Errorf("expected a url error, got %v", fe)
		}
	})

	t.Run("http url fails", func(t *testing.T) {
		fe := ValidateConfig("slack", store.ChannelConfig{URL: "http://example.com"})
		if fe["url"] == "" {
			t.Errorf("expected a url error for http://, got %v", fe)
		}
	})

	t.Run("https url passes", func(t *testing.T) {
		fe := ValidateConfig("slack", store.ChannelConfig{URL: "https://hooks.slack.com/services/x"})
		if len(fe) != 0 {
			t.Errorf("expected no errors, got %v", fe)
		}
	})

	t.Run("email without host from to fails", func(t *testing.T) {
		fe := ValidateConfig("email", store.ChannelConfig{})
		if fe["host"] == "" || fe["from"] == "" || fe["to"] == "" {
			t.Errorf("expected host/from/to errors, got %v", fe)
		}
	})

	t.Run("email host is not required to be a url", func(t *testing.T) {
		fe := ValidateConfig("email", store.ChannelConfig{
			Host: "smtp.example.com", From: "a@example.com", To: []string{"b@example.com"},
		})
		if len(fe) != 0 {
			t.Errorf("expected no errors, got %v", fe)
		}
	})

	t.Run("opsgenie invalid region fails", func(t *testing.T) {
		fe := ValidateConfig("opsgenie", store.ChannelConfig{APIKey: "k", Region: "xx"})
		if fe["region"] == "" {
			t.Errorf("expected a region error, got %v", fe)
		}
	})

	t.Run("opsgenie valid region passes", func(t *testing.T) {
		fe := ValidateConfig("opsgenie", store.ChannelConfig{APIKey: "k", Region: "eu"})
		if len(fe) != 0 {
			t.Errorf("expected no errors, got %v", fe)
		}
	})

	t.Run("pagerduty without routing key fails", func(t *testing.T) {
		fe := ValidateConfig("pagerduty", store.ChannelConfig{})
		if fe["routingKey"] == "" {
			t.Errorf("expected a routingKey error, got %v", fe)
		}
	})
}
