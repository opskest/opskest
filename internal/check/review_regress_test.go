package check

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"opskest/internal/model"
)

// A server that never sends a newline must not make the line-based checkers
// buffer without limit: they stop at maxServiceRead and fail cleanly.
func TestServiceCheckersBoundHostileReads(t *testing.T) {
	flood := serveTCP(t, func(c net.Conn) {
		buf := make([]byte, 64<<10)
		for i := range buf {
			buf[i] = 'a'
		}
		for {
			if _, err := c.Write(buf); err != nil {
				return
			}
		}
	})
	for _, typ := range []model.MonitorType{model.TypeSSH, model.TypeMemcache, model.TypeRedis} {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		start := time.Now()
		r := runService(t, typ, flood, nil)
		runtime.ReadMemStats(&after)
		if r.OK {
			t.Errorf("%s: a flood is not a healthy server: %+v", typ, r)
		}
		if time.Since(start) > 2500*time.Millisecond {
			t.Errorf("%s: the read limit should end the check long before its timeout (%v)", typ, time.Since(start))
		}
		// 4 MiB cap, with slack for bufio's doubling; a runaway read is hundreds of MiB.
		if grown := int64(after.TotalAlloc - before.TotalAlloc); grown > 64<<20 {
			t.Errorf("%s: allocated %d MiB reading from a hostile server", typ, grown>>20)
		}
	}
}

// The gRPC service name length is a protobuf varint: names of 128+ bytes
// need two bytes, which a single byte(len) corrupted.
func TestGRPCLongServiceName(t *testing.T) {
	name := strings.Repeat("s", 200)
	var got []byte
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status")
		w.Write([]byte{0, 0, 0, 0, 2, 0x08, 1})
		w.Header().Set("Grpc-Status", "0")
	}))
	srv.Config.Protocols = new(http.Protocols)
	srv.Config.Protocols.SetUnencryptedHTTP2(true)
	srv.Start()
	defer srv.Close()

	r := runService(t, model.TypeGRPC, strings.TrimPrefix(srv.URL, "http://"), func(m *model.Monitor) { m.Settings.GRPCService = name })
	if !r.OK {
		t.Fatalf("%+v", r)
	}
	// 5-byte message frame header, then 0x0a, varint(200) = c8 01, the name.
	if len(got) != 5+1+2+200 || got[5] != 0x0a || got[6] != 0xc8 || got[7] != 0x01 {
		t.Fatalf("malformed request message: % x ...", got[:min(len(got), 12)])
	}
	if n := binary.BigEndian.Uint32(got[1:5]); n != 203 {
		t.Fatalf("frame length = %d, want 203", n)
	}
}

func TestQueryProblemBlocksFileOutput(t *testing.T) {
	for _, q := range []string{
		"SELECT * FROM t INTO OUTFILE '/tmp/x'",
		"select 1 into dumpfile '/tmp/x'",
		"SELECT 1 /*!50000 INTO OUTFILE '/tmp/x' */",
		"SELECT 1 INTO\n\tOUTFILE '/tmp/x'",
	} {
		if p := model.QueryProblem(q); p == "" {
			t.Errorf("%q must be rejected", q)
		}
	}
	if p := model.QueryProblem("SELECT count(*) FROM outfile_log"); p != "" {
		t.Errorf("a table name that merely contains the word is fine: %s", p)
	}
}

func TestSQLFailRedactsRowValues(t *testing.T) {
	m := model.NewMonitor(model.TypePostgresSQL)
	if got := redactQuoted(`invalid input syntax for type integer: "alice@example.com"`); strings.Contains(got, "alice") {
		t.Errorf("row value leaked: %s", got)
	}
	if got := redactQuoted(`Truncated incorrect DOUBLE value: 'hunter2'`); strings.Contains(got, "hunter2") {
		t.Errorf("row value leaked: %s", got)
	}
	_ = m
}

func TestSQLOpenRejectsUnsafeTargets(t *testing.T) {
	c := &sqlChecker{dialect: "postgres", last: map[int64]deadlockBaseline{}}
	for _, target := range []string{"db:5432 sslmode=disable", "db:abc", "/var/run/postgresql:5432", "@abstract:5432", ":5432", "db:0", "db:99999"} {
		m := sqlMonitor(model.TypePostgresSQL, target)
		if _, err := c.open(context.Background(), m); err == nil {
			t.Errorf("%q must be rejected", target)
		}
	}
	db, err := c.open(context.Background(), sqlMonitor(model.TypePostgresSQL, "db.example.test:5432"))
	if err != nil {
		t.Fatalf("a normal target must open: %v", err)
	}
	db.Close()
}

// Only the first row of a custom query is used. A query that streams millions
// of rows must end the check promptly: before the statement was cancelled
// after the first row, rows.Close drained the whole result set, and on MySQL a
// big cross join held the check open for minutes against a 4 s timeout.
func TestIntegrationBigResultSetEndsPromptly(t *testing.T) {
	for _, tc := range []struct {
		env, query string
		typ        model.MonitorType
	}{
		{"OPSKEST_IT_PG", `SELECT n FROM opskest_big`, model.TypePostgresSQL},
		{"OPSKEST_IT_MYSQL", `SELECT a.N FROM (SELECT 1 N UNION SELECT 2) a, (SELECT 1 UNION SELECT 2) b, (SELECT 1 UNION SELECT 2) c, information_schema.columns d, information_schema.columns e`, model.TypeMySQLSQL},
	} {
		s := itFromEnv(t, tc.env, tc.typ)
		if tc.typ == model.TypePostgresSQL {
			admin := s.open(t)
			if _, err := admin.Exec(`CREATE TABLE IF NOT EXISTS opskest_big AS SELECT g AS n FROM generate_series(1, 5000000) g`); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { admin.Exec(`DROP TABLE IF EXISTS opskest_big`) })
		}
		m := s.monitor(0, func(m *model.Monitor) { m.Settings.Query = tc.query; m.TimeoutMs = 20000 })
		start := time.Now()
		r := s.run(t, m)
		if !r.OK {
			t.Errorf("%s: %+v", tc.typ, r)
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("%s: the check took %v; the rest of the result set must be abandoned", tc.typ, d)
		}
	}
}

// Pointing a deadlock monitor at another server must start a new baseline;
// comparing the new server's counter with the old one's fakes new deadlocks.
func TestIntegrationDeadlockBaselineIsPerServer(t *testing.T) {
	s := itFromEnv(t, "OPSKEST_IT_PG", model.TypePostgresSQL)
	m := s.monitor(77, func(m *model.Monitor) { m.Settings.Probe, m.Settings.Threshold = "deadlocks", 0 })
	c := &sqlChecker{dialect: "postgres", last: map[int64]deadlockBaseline{}}
	c.last[77] = deadlockBaseline{server: "another-server:5432", total: -1000} // as if the old server had a far lower counter
	if r := c.Check(context.Background(), m); !r.OK {
		t.Fatalf("a changed server is a new baseline, not a burst of deadlocks: %+v", r)
	}
	if got := c.last[77]; got.server == "another-server:5432" {
		t.Fatal("the baseline must move to the new server")
	}
}
