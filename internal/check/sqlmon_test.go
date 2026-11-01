package check

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"opskest/internal/model"
)

func TestCompareQuery(t *testing.T) {
	cases := []struct {
		got, op, want string
		ok            bool
	}{
		{"12", "gt", "10", true}, {"12", "lt", "10", false}, {"10", "gte", "10", true}, {"10", "lte", "9", false},
		{"10.0", "eq", "10", true}, {"10", "ne", "10", false},
		{"ok", "eq", "ok", true}, {"ok", "ne", "ok", false}, {"ok", "gt", "1", false},
		{"all systems ok", "contains", "ok", true}, {"all", "contains", "ok", false},
		{" 7 ", "eq", "7", true},
	}
	for _, c := range cases {
		if got := compareQuery(c.got, c.op, c.want); got != c.ok {
			t.Errorf("compareQuery(%q %s %q) = %v, want %v", c.got, c.op, c.want, got, c.ok)
		}
	}
}

func TestSQLString(t *testing.T) {
	for in, want := range map[any]string{nil: "", int64(5): "5", 2.5: "2.5", "x": "x", true: "true"} {
		if got := sqlString(in); got != want {
			t.Errorf("%v -> %q, want %q", in, got, want)
		}
	}
	if got := sqlString([]byte("abc")); got != "abc" {
		t.Error(got)
	}
	if got := sqlString(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); got != "2026-01-02T03:04:05Z" {
		t.Error(got)
	}
}

func TestQuoteDSNEscapes(t *testing.T) {
	if got := quoteDSN(`pa'ss\word`); got != `'pa\'ss\\word'` {
		t.Fatal(got)
	}
}

func sqlMonitor(typ model.MonitorType, target string) *model.Monitor {
	m := model.NewMonitor(typ)
	m.Name, m.Target, m.TimeoutMs = "db", target, 3000
	m.Settings.BasicUser, m.Settings.Query = "monitor", "SELECT 1"
	m.Secrets.BasicPass = "s3cr3t-pa55"
	return m
}

func TestSQLConnectionRefusedHidesCredentials(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens here now

	for _, typ := range []model.MonitorType{model.TypePostgresSQL, model.TypeMySQLSQL} {
		m := sqlMonitor(typ, addr)
		if fe := m.Validate(); len(fe) != 0 {
			t.Fatalf("%s: %v", typ, fe)
		}
		r := Run(testCtx(t), m, time.Now)
		if r.OK {
			t.Fatalf("%s: expected failure", typ)
		}
		for _, secret := range []string{"s3cr3t-pa55", "SELECT 1"} {
			if strings.Contains(r.Reason, secret) {
				t.Errorf("%s: reason leaks %q: %s", typ, secret, r.Reason)
			}
		}
	}
}

// A server that answers the startup message with an authentication error
// must produce a database error reason, not a crash or a leaked password.
func TestPostgresAuthFailureReason(t *testing.T) {
	addr := serveTCP(t, func(c net.Conn) {
		// Each connection starts with an SSLRequest (answered N) or goes
		// straight to the StartupMessage, as pgx does when it falls back.
		for {
			hdr := make([]byte, 8)
			if _, err := io.ReadFull(c, hdr[:4]); err != nil {
				return
			}
			n := binary.BigEndian.Uint32(hdr[:4])
			rest := make([]byte, n-4)
			if _, err := io.ReadFull(c, rest); err != nil {
				return
			}
			if n == 8 && binary.BigEndian.Uint32(rest) == 80877103 {
				c.Write([]byte("N"))
				continue
			}
			break
		}

		var body []byte
		for _, f := range [][2]string{{"S", "FATAL"}, {"C", "28P01"}, {"M", "password authentication failed for user \"monitor\""}} {
			body = append(body, f[0][0])
			body = append(body, f[1]...)
			body = append(body, 0)
		}
		body = append(body, 0)
		msg := []byte{'E', 0, 0, 0, 0}
		binary.BigEndian.PutUint32(msg[1:], uint32(len(body)+4))
		c.Write(append(msg, body...))
	})
	m := sqlMonitor(model.TypePostgresSQL, addr)
	r := Run(testCtx(t), m, time.Now)
	if r.OK || !strings.Contains(r.Reason, "28P01") || strings.Contains(r.Reason, "s3cr3t-pa55") {
		t.Fatalf("reason: %+v", r)
	}
}

func TestSQLMonitorValidation(t *testing.T) {
	m := sqlMonitor(model.TypeMySQLSQL, "db:3306")
	m.Settings.BasicUser = ""
	if fe := m.Validate(); fe["settings.basicUser"] == "" {
		t.Error("username required")
	}
	m = sqlMonitor(model.TypeMySQLSQL, "db:3306")
	m.Settings.Query = "DROP TABLE x"
	if fe := m.Validate(); fe["settings.query"] == "" {
		t.Error("write statements must be rejected")
	}
	m = sqlMonitor(model.TypePostgresSQL, "db:5432")
	m.Settings.Probe = "long_running"
	m.ApplyDefaults()
	if m.Settings.ThresholdSecs != 30 {
		t.Errorf("default long-running limit: %d", m.Settings.ThresholdSecs)
	}
	if fe := m.Validate(); len(fe) != 0 {
		t.Errorf("probe monitors don't need a query: %v", fe)
	}
	m.Settings.Probe = "nonsense"
	if fe := m.Validate(); fe["settings.probe"] == "" {
		t.Error("unknown probe accepted")
	}
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}
