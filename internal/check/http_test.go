package check

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"opskest/internal/model"
)

func newHTTPMonitor(target string) *model.Monitor {
	m := model.NewMonitor(model.TypeHTTP)
	m.Name = "test"
	m.Target = target
	return m
}

func TestHTTPOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := newHTTPMonitor(srv.URL)

	res := Run(context.Background(), m, time.Now)
	if !res.OK {
		t.Fatalf("expected OK, got Reason=%q ErrClass=%q", res.Reason, res.ErrClass)
	}
	if res.Code != http.StatusOK {
		t.Errorf("Code = %d, want %d", res.Code, http.StatusOK)
	}
	if res.Timing == nil {
		t.Fatalf("Timing is nil")
	}
	if res.Timing.TTFBMs < 0 {
		t.Errorf("TTFBMs = %d, want >= 0", res.Timing.TTFBMs)
	}
	if res.Timing.DNSMs != -1 {
		t.Errorf("DNSMs = %d, want -1 for an IP-literal URL", res.Timing.DNSMs)
	}
}

func TestHTTPStatusMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	m := newHTTPMonitor(srv.URL)

	res := Run(context.Background(), m, time.Now)
	if res.OK {
		t.Fatalf("expected not OK")
	}
	if res.ErrClass != model.ErrHTTPStatus {
		t.Errorf("ErrClass = %q, want %q", res.ErrClass, model.ErrHTTPStatus)
	}
	want := "HTTP 503 (expected 200-299)"
	if res.Reason != want {
		t.Errorf("Reason = %q, want %q", res.Reason, want)
	}
}

func TestHTTPTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := newHTTPMonitor(srv.URL)
	m.IntervalS = 10
	m.TimeoutMs = 300

	res := Run(context.Background(), m, time.Now)
	if res.OK {
		t.Fatalf("expected not OK due to timeout")
	}
	if res.ErrClass != model.ErrTimeout {
		t.Errorf("ErrClass = %q, want %q", res.ErrClass, model.ErrTimeout)
	}
	want := "timeout after 300 ms"
	if !strings.HasPrefix(res.Reason, want) {
		t.Errorf("Reason = %q, want prefix %q", res.Reason, want)
	}
}

func TestHTTPRedirects(t *testing.T) {
	t.Run("stopped after limit", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/r0", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/r1", http.StatusFound)
		})
		mux.HandleFunc("/r1", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/r2", http.StatusFound)
		})
		mux.HandleFunc("/r2", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/r3", http.StatusFound)
		})
		mux.HandleFunc("/r3", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		m := newHTTPMonitor(srv.URL + "/r0")
		two := 2
		m.Settings.MaxRedirects = &two

		res := Run(context.Background(), m, time.Now)
		if res.OK {
			t.Fatalf("expected not OK")
		}
		want := "stopped after 2 redirects"
		if res.Reason != want {
			t.Errorf("Reason = %q, want %q", res.Reason, want)
		}
	})

	t.Run("zero means dont follow", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/somewhere-else", http.StatusMovedPermanently)
		}))
		defer srv.Close()

		m := newHTTPMonitor(srv.URL)
		zero := 0
		m.Settings.MaxRedirects = &zero
		m.Settings.AcceptedCodes = "301"

		res := Run(context.Background(), m, time.Now)
		if !res.OK {
			t.Fatalf("expected OK on the raw 301, got Reason=%q", res.Reason)
		}
		if res.Code != http.StatusMovedPermanently {
			t.Errorf("Code = %d, want %d", res.Code, http.StatusMovedPermanently)
		}
	})
}

func TestHTTPMethodHeadersBodyAuth(t *testing.T) {
	type captured struct {
		method      string
		env         string
		body        string
		contentType string
		authHeader  string
	}

	t.Run("post json body headers and basic auth", func(t *testing.T) {
		ch := make(chan captured, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			ch <- captured{
				method:      r.Method,
				env:         r.Header.Get("X-Env"),
				body:        string(b),
				contentType: r.Header.Get("Content-Type"),
				authHeader:  r.Header.Get("Authorization"),
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		m := newHTTPMonitor(srv.URL)
		m.Settings.Method = http.MethodPost
		m.Settings.BodyType = "json"
		m.Settings.Body = `{"foo":"bar"}`
		m.Settings.Headers = []model.Header{{Name: "X-Env", Value: "prod"}}
		m.Settings.AuthType = "basic"
		m.Settings.BasicUser = "u"
		m.Secrets.BasicPass = "p"

		res := Run(context.Background(), m, time.Now)
		if !res.OK {
			t.Fatalf("expected OK, got Reason=%q", res.Reason)
		}

		got := <-ch
		if got.method != http.MethodPost {
			t.Errorf("method = %q, want POST", got.method)
		}
		if got.env != "prod" {
			t.Errorf("X-Env = %q, want prod", got.env)
		}
		if got.body != `{"foo":"bar"}` {
			t.Errorf("body = %q", got.body)
		}
		if got.contentType != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got.contentType)
		}
		req := &http.Request{Header: http.Header{"Authorization": []string{got.authHeader}}}
		user, pass, ok := req.BasicAuth()
		if !ok || user != "u" || pass != "p" {
			t.Errorf("basic auth = %q/%q ok=%v, want u/p", user, pass, ok)
		}
	})

	t.Run("bearer token from secrets", func(t *testing.T) {
		ch := make(chan string, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ch <- r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		m := newHTTPMonitor(srv.URL)
		m.Settings.AuthType = "bearer"
		m.Secrets.BearerToken = "tok123"

		res := Run(context.Background(), m, time.Now)
		if !res.OK {
			t.Fatalf("expected OK, got Reason=%q", res.Reason)
		}
		want := "Bearer tok123"
		if got := <-ch; got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
	})

	t.Run("secret header value from secrets", func(t *testing.T) {
		ch := make(chan string, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ch <- r.Header.Get("X-Secret")
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		m := newHTTPMonitor(srv.URL)
		m.Settings.Headers = []model.Header{{Name: "X-Secret", Secret: true}}
		m.Secrets.Headers = map[string]string{"X-Secret": "shhh"}

		res := Run(context.Background(), m, time.Now)
		if !res.OK {
			t.Fatalf("expected OK, got Reason=%q", res.Reason)
		}
		want := "shhh"
		if got := <-ch; got != want {
			t.Errorf("X-Secret = %q, want %q", got, want)
		}
	})
}

func TestHTTPBodyCap(t *testing.T) {
	const totalSize = 5 * 1024 * 1024
	const needleOffset = 2 * 1024 * 1024
	body := bytes.Repeat([]byte("x"), totalSize)
	copy(body[needleOffset:], []byte("NEEDLE"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	m := newHTTPMonitor(srv.URL)
	m.Settings.Keyword = "NEEDLE"
	m.Settings.KeywordMode = "contains"

	res := Run(context.Background(), m, time.Now)
	if res.OK {
		t.Fatalf("expected not OK: keyword beyond the 1 MiB cap must not be found")
	}
	if res.ErrClass != model.ErrAssertion {
		t.Errorf("ErrClass = %q, want %q", res.ErrClass, model.ErrAssertion)
	}
	if !strings.Contains(res.Reason, "NEEDLE") {
		t.Errorf("Reason = %q, want it to mention NEEDLE", res.Reason)
	}
}

// TestHTTPBodyReadError covers a connection that passes the status check
// but drops mid-body while an assertion is reading it. It must be reported
// as a transport failure, not as a false assertion mismatch evaluated
// against partial data.
func TestHTTPBodyReadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, bufrw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		// Declare a body far longer than what's actually sent, then close
		// the connection: the client will hit an unexpected-EOF style
		// error partway through reading the body.
		bufrw.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 1000\r\n\r\n")
		bufrw.WriteString("partial")
		bufrw.Flush()
	}))
	defer srv.Close()

	m := newHTTPMonitor(srv.URL)
	m.Settings.Keyword = "whatever"
	m.Settings.KeywordMode = "contains"

	res := Run(context.Background(), m, time.Now)
	if res.OK {
		t.Fatalf("expected not OK for a connection that drops mid-body")
	}
	if res.ErrClass == model.ErrAssertion {
		t.Errorf("ErrClass = %q, must not be misreported as an assertion failure", res.ErrClass)
	}
	if !strings.Contains(res.Reason, "connection lost while reading response") {
		t.Errorf("Reason = %q, want it to mention the dropped connection", res.Reason)
	}
}

func TestHTTPTLSCertCaptured(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := newHTTPMonitor(srv.URL)
	m.Settings.IgnoreTLS = true

	res := Run(context.Background(), m, time.Now)
	if !res.OK {
		t.Fatalf("expected OK, got Reason=%q", res.Reason)
	}
	if res.Cert == nil || res.Cert.NotAfter.IsZero() {
		t.Fatalf("expected a non-zero Cert.NotAfter, got %+v", res.Cert)
	}
}

func TestHTTPTLSInvalid(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := newHTTPMonitor(srv.URL)
	m.Settings.IgnoreTLS = false

	res := Run(context.Background(), m, time.Now)
	if res.OK {
		t.Fatalf("expected not OK against a self-signed cert with IgnoreTLS=false")
	}
	if res.ErrClass != model.ErrTLS {
		t.Errorf("ErrClass = %q, want %q (reason=%q)", res.ErrClass, model.ErrTLS, res.Reason)
	}
}

func TestRunDegradedBySlowness(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := newHTTPMonitor(srv.URL)
	m.DegradedMs = 50

	res := Run(context.Background(), m, time.Now)
	if !res.OK {
		t.Fatalf("expected OK, got Reason=%q", res.Reason)
	}
	if !res.Degraded {
		t.Fatalf("expected Degraded")
	}
	if !strings.HasPrefix(res.Reason, "slow:") {
		t.Errorf("Reason = %q, want prefix %q", res.Reason, "slow:")
	}
}

// fakeCertChecker is injected as a Checker under a private monitor type to
// exercise Run's certificate-expiry degradation path without a real TLS
// handshake.
type fakeCertChecker struct {
	cert      *model.CertInfo
	latencyMs int
}

func (f fakeCertChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	return model.Result{OK: true, Cert: f.cert, LatencyMs: f.latencyMs}
}

func TestRunDegradedByCert(t *testing.T) {
	const fakeType model.MonitorType = "test-only-cert-fake"
	fixedNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	Register(fakeType, fakeCertChecker{cert: &model.CertInfo{NotAfter: fixedNow.Add(5 * 24 * time.Hour)}})

	m := model.NewMonitor(model.TypeHTTP)
	m.Type = fakeType
	m.SSLWarnDays = 14

	res := Run(context.Background(), m, func() time.Time { return fixedNow })
	if !res.OK {
		t.Fatalf("expected OK, got Reason=%q", res.Reason)
	}
	if !res.Degraded {
		t.Fatalf("expected Degraded")
	}
	want := "certificate expires in 5 days"
	if res.Reason != want {
		t.Errorf("Reason = %q, want %q", res.Reason, want)
	}
}

func TestRunDegradedByExpiredCert(t *testing.T) {
	const fakeType model.MonitorType = "test-only-expired-cert-fake"
	fixedNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	Register(fakeType, fakeCertChecker{cert: &model.CertInfo{NotAfter: fixedNow.Add(-5 * 24 * time.Hour)}})

	m := model.NewMonitor(model.TypeHTTP)
	m.Type = fakeType
	m.SSLWarnDays = 14

	res := Run(context.Background(), m, func() time.Time { return fixedNow })
	if !res.OK {
		t.Fatalf("expected OK, got Reason=%q", res.Reason)
	}
	if !res.Degraded {
		t.Fatalf("expected Degraded")
	}
	want := "certificate expired 5 days ago"
	if res.Reason != want {
		t.Errorf("Reason = %q, want %q", res.Reason, want)
	}
}

func TestRunDegradedComposesReasons(t *testing.T) {
	const fakeType model.MonitorType = "test-only-slow-and-cert-fake"
	fixedNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	Register(fakeType, fakeCertChecker{
		cert:      &model.CertInfo{NotAfter: fixedNow.Add(6 * 24 * time.Hour)},
		latencyMs: 2400,
	})

	m := model.NewMonitor(model.TypeHTTP)
	m.Type = fakeType
	m.DegradedMs = 2000
	m.SSLWarnDays = 14

	res := Run(context.Background(), m, func() time.Time { return fixedNow })
	if !res.OK {
		t.Fatalf("expected OK, got Reason=%q", res.Reason)
	}
	if !res.Degraded {
		t.Fatalf("expected Degraded")
	}
	want := "slow: 2400 ms > 2000 ms; certificate expires in 6 days"
	if res.Reason != want {
		t.Errorf("Reason = %q, want %q", res.Reason, want)
	}
}

func TestFreshConnectionEachCheck(t *testing.T) {
	var mu sync.Mutex
	newConns := 0

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			mu.Lock()
			newConns++
			mu.Unlock()
		}
	}
	srv.Start()
	defer srv.Close()

	m := newHTTPMonitor(srv.URL)

	for i := 0; i < 3; i++ {
		res := Run(context.Background(), m, time.Now)
		if !res.OK {
			t.Fatalf("check %d: expected OK, got Reason=%q", i, res.Reason)
		}
	}

	mu.Lock()
	got := newConns
	mu.Unlock()
	if got != 3 {
		t.Errorf("new connections = %d, want 3", got)
	}
}
