package check

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/miekg/dns"

	"opskest/internal/model"
)

// ---------------------------------------------------------------------
// TCP
// ---------------------------------------------------------------------

func newTCPMonitor(target string) *model.Monitor {
	m := model.NewMonitor(model.TypeTCP)
	m.Name = "test"
	m.Target = target
	return m
}

func TestTCPOpen(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	m := newTCPMonitor(ln.Addr().String())

	res := Run(context.Background(), m, time.Now)
	if !res.OK {
		t.Fatalf("expected OK, got Reason=%q ErrClass=%q", res.Reason, res.ErrClass)
	}
	if res.LatencyMs < 0 {
		t.Errorf("LatencyMs = %d, want >= 0", res.LatencyMs)
	}
}

func TestTCPRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close() // now nothing is listening on this port

	m := newTCPMonitor(addr)

	res := Run(context.Background(), m, time.Now)
	if res.OK {
		t.Fatalf("expected not OK against a closed port")
	}
	if res.ErrClass != model.ErrRefused {
		t.Errorf("ErrClass = %q, want %q (reason=%q)", res.ErrClass, model.ErrRefused, res.Reason)
	}
}

func TestTCPWithTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := newTCPMonitor(srv.Listener.Addr().String())
	m.Settings.TLS = true
	m.Settings.IgnoreTLS = true

	res := Run(context.Background(), m, time.Now)
	if !res.OK {
		t.Fatalf("expected OK, got Reason=%q ErrClass=%q", res.Reason, res.ErrClass)
	}
	if res.Cert == nil || res.Cert.NotAfter.IsZero() {
		t.Fatalf("expected a non-zero Cert.NotAfter, got %+v", res.Cert)
	}
}

// ---------------------------------------------------------------------
// DNS
// ---------------------------------------------------------------------

// startDNSServer starts a miekg/dns UDP server on 127.0.0.1:0 with the
// given handler and returns its address ("127.0.0.1:<port>"). It is shut
// down automatically when the test ends.
func startDNSServer(t *testing.T, handler dns.HandlerFunc) string {
	t.Helper()

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}

	server := &dns.Server{PacketConn: pc, Handler: handler}
	go server.ActivateAndServe()
	t.Cleanup(func() {
		server.Shutdown()
	})

	return pc.LocalAddr().String()
}

func newDNSMonitor(target, resolver, record, expected, match string) *model.Monitor {
	m := model.NewMonitor(model.TypeDNS)
	m.Name = "test"
	m.Target = target
	m.Settings.Resolver = resolver
	m.Settings.Record = record
	m.Settings.Expected = expected
	m.Settings.Match = match
	return m
}

func TestDNSA(t *testing.T) {
	addr := startDNSServer(t, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		if len(r.Question) == 1 && r.Question[0].Qtype == dns.TypeA && r.Question[0].Name == "x.test." {
			rr, err := dns.NewRR("x.test. 60 IN A 10.1.2.3")
			if err != nil {
				t.Fatalf("NewRR: %v", err)
			}
			m.Answer = append(m.Answer, rr)
		} else {
			m.Rcode = dns.RcodeNameError
		}
		_ = w.WriteMsg(m)
	})

	t.Run("match", func(t *testing.T) {
		m := newDNSMonitor("x.test", addr, "A", "10.1.2.3", "equals")
		res := Run(context.Background(), m, time.Now)
		if !res.OK {
			t.Fatalf("expected OK, got Reason=%q ErrClass=%q", res.Reason, res.ErrClass)
		}
	})

	t.Run("mismatch", func(t *testing.T) {
		m := newDNSMonitor("x.test", addr, "A", "10.9.9.9", "equals")
		res := Run(context.Background(), m, time.Now)
		if res.OK {
			t.Fatalf("expected not OK")
		}
		if res.ErrClass != model.ErrAssertion {
			t.Errorf("ErrClass = %q, want %q", res.ErrClass, model.ErrAssertion)
		}
		want := "A x.test = [10.1.2.3], expected 10.9.9.9"
		if res.Reason != want {
			t.Errorf("Reason = %q, want %q", res.Reason, want)
		}
	})
}

func TestDNSTXTContains(t *testing.T) {
	addr := startDNSServer(t, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		if len(r.Question) == 1 && r.Question[0].Qtype == dns.TypeTXT {
			rr, err := dns.NewRR(`x.test. 60 IN TXT "v=spf1 include:_spf.google.com ~all"`)
			if err != nil {
				t.Fatalf("NewRR: %v", err)
			}
			m.Answer = append(m.Answer, rr)
		} else {
			m.Rcode = dns.RcodeNameError
		}
		_ = w.WriteMsg(m)
	})

	m := newDNSMonitor("x.test", addr, "TXT", "_spf.google.com", "contains")
	res := Run(context.Background(), m, time.Now)
	if !res.OK {
		t.Fatalf("expected OK, got Reason=%q ErrClass=%q", res.Reason, res.ErrClass)
	}
}

func TestDNSNXDomain(t *testing.T) {
	addr := startDNSServer(t, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Rcode = dns.RcodeNameError
		_ = w.WriteMsg(m)
	})

	m := newDNSMonitor("nope.test", addr, "A", "", "")
	res := Run(context.Background(), m, time.Now)
	if res.OK {
		t.Fatalf("expected not OK")
	}
	if res.ErrClass != model.ErrDNS {
		t.Errorf("ErrClass = %q, want %q (reason=%q)", res.ErrClass, model.ErrDNS, res.Reason)
	}
	if res.Code != 3 || !strings.Contains(res.Reason, "NXDOMAIN") {
		t.Errorf("Code = %d reason = %q, want rcode 3 / NXDOMAIN", res.Code, res.Reason)
	}
}

// ---------------------------------------------------------------------
// WebSocket
// ---------------------------------------------------------------------

func newWebSocketMonitor(target string) *model.Monitor {
	m := model.NewMonitor(model.TypeWebSocket)
	m.Name = "test"
	m.Target = target
	return m
}

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

func TestWebSocketEcho(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()

		_, data, err := c.Read(r.Context())
		if err != nil {
			return
		}
		if err := c.Write(r.Context(), websocket.MessageText, data); err != nil {
			return
		}
		c.Close(websocket.StatusNormalClosure, "")
	}))
	defer srv.Close()

	t.Run("expect matches", func(t *testing.T) {
		m := newWebSocketMonitor(wsURL(srv.URL))
		m.Settings.Send = "ping"
		m.Settings.Expect = "ping"

		res := Run(context.Background(), m, time.Now)
		if !res.OK {
			t.Fatalf("expected OK, got Reason=%q ErrClass=%q", res.Reason, res.ErrClass)
		}
	})

	t.Run("expect mismatches", func(t *testing.T) {
		m := newWebSocketMonitor(wsURL(srv.URL))
		m.Settings.Send = "ping"
		m.Settings.Expect = "nope"

		res := Run(context.Background(), m, time.Now)
		if res.OK {
			t.Fatalf("expected not OK")
		}
		if res.ErrClass != model.ErrAssertion {
			t.Errorf("ErrClass = %q, want %q (reason=%q)", res.ErrClass, model.ErrAssertion, res.Reason)
		}
	})
}

func TestWebSocketNotUpgrade(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := newWebSocketMonitor(wsURL(srv.URL))

	res := Run(context.Background(), m, time.Now)
	if res.OK {
		t.Fatalf("expected not OK against a plain HTTP server")
	}
}

// ---------------------------------------------------------------------
// Ping
// ---------------------------------------------------------------------

func TestPingLocalhost(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not support the unprivileged ICMP socket used by the Linux deployment")
	}
	m := model.NewMonitor(model.TypePing)
	m.Name = "test"
	m.Target = "127.0.0.1"
	m.Settings.Count = 2

	res := Run(context.Background(), m, time.Now)
	if !res.OK {
		msg := strings.ToLower(res.Reason)
		if strings.Contains(msg, "permission") || strings.Contains(msg, "operation not permitted") {
			t.Skip("unprivileged ICMP not permitted in this sandbox")
		}
		t.Fatalf("expected OK, got Reason=%q ErrClass=%q", res.Reason, res.ErrClass)
	}
	if res.LossPct != 0 {
		t.Errorf("LossPct = %v, want 0", res.LossPct)
	}
}

// startMultiADNSServer answers "alb.test." with the given A records.
func startMultiADNSServer(t *testing.T, ips ...string) string {
	t.Helper()
	return startDNSServer(t, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		if len(r.Question) == 1 && r.Question[0].Qtype == dns.TypeA && r.Question[0].Name == "alb.test." {
			for _, ip := range ips {
				rr, err := dns.NewRR("alb.test. 60 IN A " + ip)
				if err != nil {
					t.Errorf("NewRR: %v", err)
					return
				}
				m.Answer = append(m.Answer, rr)
			}
		} else if len(r.Question) == 1 && r.Question[0].Qtype == dns.TypeAAAA && r.Question[0].Name == "alb.test." {
			// NOERROR, no answers
		} else {
			m.Rcode = dns.RcodeNameError
		}
		_ = w.WriteMsg(m)
	})
}

func TestDNSMultiValue(t *testing.T) {
	addr := startMultiADNSServer(t, "10.0.0.2", "10.0.0.1", "10.0.0.3")

	cases := []struct {
		name     string
		expected []string
		match    string
		ok       bool
		reason   string
	}{
		{"exact same set any order", []string{"10.0.0.3", "10.0.0.1", "10.0.0.2"}, "exact", true, ""},
		{"exact rotated", []string{"10.0.0.1", "10.0.0.2", "10.0.0.9"}, "exact", false, "A alb.test changed: missing 10.0.0.9; new 10.0.0.3"},
		{"exact subset", []string{"10.0.0.1", "10.0.0.2"}, "exact", false, "A alb.test changed: new 10.0.0.3"},
		{"all subset", []string{"10.0.0.1", "10.0.0.2"}, "all", true, ""},
		{"all missing", []string{"10.0.0.1", "10.0.0.9"}, "all", false, "A alb.test changed: missing 10.0.0.9"},
		{"equals any one", []string{"10.0.0.9", "10.0.0.3"}, "equals", true, ""},
		{"equals none", []string{"10.0.0.8", "10.0.0.9"}, "equals", false, ""}, // resolver may reorder answers,
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newDNSMonitor("alb.test", addr, "A", "", c.match)
			m.Settings.ExpectedValues = c.expected
			res := Run(context.Background(), m, time.Now)
			if res.OK != c.ok {
				t.Fatalf("OK = %v, want %v (reason=%q)", res.OK, c.ok, res.Reason)
			}
			if !c.ok && c.reason != "" && res.Reason != c.reason {
				t.Errorf("Reason = %q, want %q", res.Reason, c.reason)
			}
		})
	}
}

func TestResolveDNSSorted(t *testing.T) {
	addr := startMultiADNSServer(t, "10.0.0.2", "10.0.0.1", "10.0.0.2")
	got, err := ResolveDNS(context.Background(), "alb.test", "a", addr)
	if err != nil {
		t.Fatalf("ResolveDNS: %v", err)
	}
	if want := []string{"10.0.0.1", "10.0.0.2"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
