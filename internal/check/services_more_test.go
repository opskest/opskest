package check

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opskest/internal/model"
)

func TestAMQPChecker(t *testing.T) {
	good := serveTCP(t, func(c net.Conn) {
		hdr := make([]byte, 8)
		if _, err := c.Read(hdr); err != nil || string(hdr[:4]) != "AMQP" {
			return
		}
		// frame type 1, channel 0, size 0x20, class 10, method 10
		c.Write([]byte{1, 0, 0, 0, 0, 0, 0x20, 0, 10, 0, 10, 0, 0})
	})
	if r := runService(t, model.TypeAMQP, good, nil); !r.OK {
		t.Fatalf("amqp: %+v", r)
	}
	bad := serveTCP(t, func(c net.Conn) { c.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n")) })
	if r := runService(t, model.TypeAMQP, bad, nil); r.OK {
		t.Fatalf("not amqp must fail: %+v", r)
	}
}

func TestMemcachedChecker(t *testing.T) {
	good := serveTCP(t, func(c net.Conn) {
		buf := make([]byte, 16)
		c.Read(buf)
		c.Write([]byte("VERSION 1.6.21\r\n"))
	})
	if r := runService(t, model.TypeMemcache, good, nil); !r.OK {
		t.Fatalf("memcached: %+v", r)
	}
	bad := serveTCP(t, func(c net.Conn) { c.Write([]byte("ERROR\r\n")) })
	if r := runService(t, model.TypeMemcache, bad, nil); r.OK {
		t.Fatalf("not memcached must fail: %+v", r)
	}
}

func TestBannerChecker(t *testing.T) {
	addr := serveTCP(t, func(c net.Conn) { c.Write([]byte("220 ProFTPD Server ready.\r\n")) })
	set := func(expect string) func(*model.Monitor) {
		return func(m *model.Monitor) { m.Settings.Expect = expect }
	}
	if r := runService(t, model.TypeBanner, addr, set("ProFTPD")); !r.OK {
		t.Fatalf("match: %+v", r)
	}
	r := runService(t, model.TypeBanner, addr, set("vsftpd"))
	if r.OK || strings.Contains(r.Reason, "ProFTPD") {
		t.Fatalf("mismatch must fail without echoing the banner: %+v", r)
	}
	m := model.NewMonitor(model.TypeBanner)
	m.Name, m.Target = "b", addr
	if fe := m.Validate(); fe["settings.expect"] == "" {
		t.Fatalf("expect is required: %v", fe)
	}
}

func TestGRPCChecker(t *testing.T) {
	handler := func(serving bool, code string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/grpc.health.v1.Health/Check" || r.Header.Get("Content-Type") != "application/grpc" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/grpc")
			w.Header().Set("Trailer", "Grpc-Status")
			status := byte(1)
			if !serving {
				status = 2
			}
			w.Write([]byte{0, 0, 0, 0, 2, 0x08, status})
			w.Header().Set("Grpc-Status", code)
		})
	}
	serve := func(h http.Handler) string {
		srv := httptest.NewUnstartedServer(h)
		srv.Config.Protocols = new(http.Protocols)
		srv.Config.Protocols.SetUnencryptedHTTP2(true)
		srv.Start()
		t.Cleanup(srv.Close)
		return strings.TrimPrefix(srv.URL, "http://")
	}

	if r := runService(t, model.TypeGRPC, serve(handler(true, "0")), nil); !r.OK {
		t.Fatalf("serving: %+v", r)
	}
	if r := runService(t, model.TypeGRPC, serve(handler(false, "0")), nil); r.OK || !strings.Contains(r.Reason, "not serving") {
		t.Fatalf("not serving: %+v", r)
	}
	if r := runService(t, model.TypeGRPC, serve(handler(true, "12")), nil); r.OK || !strings.Contains(r.Reason, "health service") {
		t.Fatalf("unimplemented: %+v", r)
	}
	plain := serve(http.NotFoundHandler())
	if r := runService(t, model.TypeGRPC, plain, nil); r.OK {
		t.Fatalf("a plain web server is not gRPC: %+v", r)
	}
}

func TestSQLQueryValidation(t *testing.T) {
	ok := []string{"SELECT 1", "select count(*) from orders;", "WITH a AS (SELECT 1) SELECT * FROM a", "SHOW STATUS", "explain select 1"}
	for _, q := range ok {
		if p := model.QueryProblem(q); p != "" {
			t.Errorf("%q: %s", q, p)
		}
	}
	bad := map[string]string{
		"":                           "required",
		"DELETE FROM orders":         "must start",
		"SELECT 1; DROP TABLE users": "one statement",
		"UPDATE t SET a=1":           "must start",
		"  insert into t values (1)": "must start",
	}
	for q, want := range bad {
		if p := model.QueryProblem(q); !strings.Contains(p, want) {
			t.Errorf("%q: got %q, want %q", q, p, want)
		}
	}

	m := model.NewMonitor(model.TypePostgresSQL)
	m.Name, m.Target = "db", "db.internal:5432"
	m.Settings.BasicUser, m.Settings.Query = "monitor", "SELECT count(*) FROM orders"
	m.Settings.QueryOp, m.Settings.QueryExpected = "gt", "x"
	if fe := m.Validate(); fe["settings.queryExpected"] == "" {
		t.Errorf("numeric comparison needs a number: %v", fe)
	}
	m.Settings.QueryExpected = "10"
	if fe := m.Validate(); len(fe) != 0 {
		t.Errorf("valid query monitor rejected: %v", fe)
	}
	m.Settings.Probe = "connections"
	m.Settings.Threshold = 0
	if fe := m.Validate(); fe["settings.threshold"] == "" {
		t.Errorf("connections threshold must be 1-100: %v", fe)
	}
}
