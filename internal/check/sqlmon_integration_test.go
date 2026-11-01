package check

// Integration tests for the database monitors against real servers. They are
// skipped unless these are set (for example to throwaway Docker containers):
//
//	OPSKEST_IT_PG=host:port,user,password,database
//	OPSKEST_IT_MYSQL=host:port,user,password,database
//
// The account must be able to create tables and see other sessions
// (a superuser or root is fine for a disposable server).

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"opskest/internal/model"
)

type itServer struct {
	typ                    model.MonitorType
	driver, dsn            string
	addr, user, pass, name string
}

func itFromEnv(t *testing.T, env string, typ model.MonitorType) itServer {
	t.Helper()
	v := os.Getenv(env)
	if v == "" {
		t.Skipf("%s not set", env)
	}
	parts := strings.Split(v, ",")
	if len(parts) != 4 {
		t.Fatalf("%s must be host:port,user,password,database", env)
	}
	s := itServer{typ: typ, addr: parts[0], user: parts[1], pass: parts[2], name: parts[3]}
	if typ == model.TypePostgresSQL {
		s.driver = "pgx"
		s.dsn = "postgres://" + s.user + ":" + s.pass + "@" + s.addr + "/" + s.name + "?sslmode=disable"
	} else {
		s.driver = "mysql"
		s.dsn = s.user + ":" + s.pass + "@tcp(" + s.addr + ")/" + s.name
	}
	return s
}

func (s itServer) monitor(id int64, tweak func(*model.Monitor)) *model.Monitor {
	m := model.NewMonitor(s.typ)
	m.ID, m.Name, m.Target, m.TimeoutMs = id, "it", s.addr, 8000
	m.Settings.BasicUser, m.Settings.Database, m.Secrets.BasicPass = s.user, s.name, s.pass
	m.Settings.Query = "SELECT 42"
	if tweak != nil {
		tweak(m)
	}
	m.ApplyDefaults()
	return m
}

func (s itServer) run(t *testing.T, m *model.Monitor) model.Result {
	t.Helper()
	if fe := m.Validate(); len(fe) != 0 {
		t.Fatalf("invalid: %v", fe)
	}
	return Run(context.Background(), m, time.Now)
}

func (s itServer) open(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open(s.driver, s.dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestPostgresIntegration(t *testing.T) {
	runSQLIntegration(t, itFromEnv(t, "OPSKEST_IT_PG", model.TypePostgresSQL))
}

func TestMySQLIntegration(t *testing.T) {
	runSQLIntegration(t, itFromEnv(t, "OPSKEST_IT_MYSQL", model.TypeMySQLSQL))
}

func runSQLIntegration(t *testing.T, s itServer) {
	admin := s.open(t)
	ctx := context.Background()
	pg := s.typ == model.TypePostgresSQL
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := admin.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`DROP TABLE IF EXISTS opskest_it`)
	if pg {
		mustExec(`CREATE TABLE opskest_it (id int PRIMARY KEY, n int)`)
	} else {
		mustExec(`CREATE TABLE opskest_it (id int PRIMARY KEY, n int) ENGINE=InnoDB`)
	}
	mustExec(`INSERT INTO opskest_it VALUES (1, 0), (2, 0)`)
	t.Cleanup(func() { admin.Exec(`DROP TABLE IF EXISTS opskest_it`) })

	t.Run("custom query", func(t *testing.T) {
		if r := s.run(t, s.monitor(0, nil)); !r.OK || r.LatencyMs < 0 {
			t.Fatalf("plain: %+v", r)
		}
		gt := func(v string) func(*model.Monitor) {
			return func(m *model.Monitor) { m.Settings.QueryOp, m.Settings.QueryExpected = "gt", v }
		}
		if r := s.run(t, s.monitor(0, gt("40"))); !r.OK {
			t.Fatalf("42 > 40: %+v", r)
		}
		r := s.run(t, s.monitor(0, gt("50")))
		if r.OK || !strings.Contains(r.Reason, "returned 42") {
			t.Fatalf("42 > 50 must fail: %+v", r)
		}
		empty := s.monitor(0, func(m *model.Monitor) {
			m.Settings.Query = "SELECT n FROM opskest_it WHERE id = 99"
			m.Settings.QueryOp, m.Settings.QueryExpected = "eq", "0"
		})
		if r := s.run(t, empty); r.OK || !strings.Contains(r.Reason, "no rows") {
			t.Fatalf("no rows: %+v", r)
		}
	})

	t.Run("wrong password is a clean failure", func(t *testing.T) {
		m := s.monitor(0, func(m *model.Monitor) { m.Secrets.BasicPass = "definitely-wrong-pw" })
		r := s.run(t, m)
		if r.OK || strings.Contains(r.Reason, "definitely-wrong-pw") || strings.Contains(r.Reason, s.pass) {
			t.Fatalf("%+v", r)
		}
	})

	t.Run("read-only transaction", func(t *testing.T) {
		q := `WITH i AS (INSERT INTO opskest_it VALUES (3, 3) RETURNING 1) SELECT * FROM i`
		if !pg {
			t.Skip("MySQL has no data-modifying SELECT; writes are blocked by the statement check and START TRANSACTION READ ONLY")
		}
		r := s.run(t, s.monitor(0, func(m *model.Monitor) { m.Settings.Query = q }))
		if r.OK {
			t.Fatalf("a data-modifying CTE must be refused in a read-only transaction: %+v", r)
		}
		var n int
		if err := admin.QueryRow(`SELECT count(*) FROM opskest_it WHERE id = 3`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("row was written: n=%d err=%v", n, err)
		}
	})

	t.Run("slow queries", func(t *testing.T) {
		sleeper := s.open(t)
		go func() {
			if pg {
				sleeper.Exec(`SELECT pg_sleep(9)`)
			} else {
				sleeper.Query(`SELECT SLEEP(9)`)
			}
		}()
		time.Sleep(3 * time.Second)
		probe := func(m *model.Monitor) { m.Settings.Probe, m.Settings.ThresholdSecs = "long_running", 2 }
		r := s.run(t, s.monitor(0, probe))
		if r.OK || !strings.Contains(r.Reason, "running longer than 2 s") {
			t.Fatalf("slow query not seen: %+v", r)
		}
		allow := s.monitor(0, func(m *model.Monitor) { probe(m); m.Settings.Threshold = 5 })
		if r := s.run(t, allow); !r.OK {
			t.Fatalf("within the allowance: %+v", r)
		}
		time.Sleep(7 * time.Second)
		if r := s.run(t, s.monitor(0, probe)); !r.OK {
			t.Fatalf("after it finished: %+v", r)
		}
	})

	t.Run("lock waits", func(t *testing.T) {
		holder := s.open(t)
		holder.SetMaxOpenConns(1)
		tx, err := holder.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`UPDATE opskest_it SET n = n + 1 WHERE id = 1`); err != nil {
			t.Fatal(err)
		}
		waiter := s.open(t)
		done := make(chan struct{})
		go func() {
			defer close(done)
			waiter.Exec(`UPDATE opskest_it SET n = n + 10 WHERE id = 1`)
		}()
		time.Sleep(2 * time.Second)
		r := s.run(t, s.monitor(0, func(m *model.Monitor) { m.Settings.Probe = "blocked" }))
		if r.OK || !strings.Contains(r.Reason, "waiting on a lock") {
			t.Fatalf("lock wait not seen: %+v", r)
		}
		tx.Rollback()
		<-done
		var rel model.Result
		for i := 0; i < 10; i++ { // the server clears the wait a moment after the release
			if rel = s.run(t, s.monitor(0, func(m *model.Monitor) { m.Settings.Probe = "blocked" })); rel.OK {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if !rel.OK {
			t.Fatalf("after release: %+v", rel)
		}
	})

	t.Run("deadlocks", func(t *testing.T) {
		probe := func(m *model.Monitor) { m.Settings.Probe = "deadlocks" }
		if r := s.run(t, s.monitor(4242, probe)); !r.OK {
			t.Fatalf("baseline: %+v", r)
		}
		if r := s.run(t, s.monitor(4242, probe)); !r.OK {
			t.Fatalf("no new deadlocks: %+v", r)
		}
		makeDeadlock(t, s)
		var got model.Result
		for i := 0; i < 12; i++ { // the server publishes the counter a moment later
			got = s.run(t, s.monitor(4242, probe))
			if !got.OK {
				break
			}
			time.Sleep(time.Second)
		}
		if got.OK || !strings.Contains(got.Reason, "new deadlock") {
			t.Fatalf("deadlock not reported: %+v", got)
		}
		if r := s.run(t, s.monitor(4242, probe)); !r.OK {
			t.Fatalf("a deadlock is reported once: %+v", r)
		}
	})

	t.Run("connection use", func(t *testing.T) {
		var held []*sql.Conn
		pool := s.open(t)
		pool.SetMaxOpenConns(12)
		for i := 0; i < 6; i++ {
			c, err := pool.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			held = append(held, c)
		}
		defer func() {
			for _, c := range held {
				c.Close()
			}
		}()
		tight := func(m *model.Monitor) { m.Settings.Probe, m.Settings.Threshold = "connections", 1 }
		r := s.run(t, s.monitor(0, tight))
		if r.OK || !strings.Contains(r.Reason, "connections at") {
			t.Fatalf("connection use not seen: %+v", r)
		}
		loose := func(m *model.Monitor) { m.Settings.Probe, m.Settings.Threshold = "connections", 90 }
		if r := s.run(t, s.monitor(0, loose)); !r.OK {
			t.Fatalf("under the limit: %+v", r)
		}
	})
}

// makeDeadlock runs two transactions that lock rows 1 and 2 in opposite
// orders; the server aborts one of them.
func makeDeadlock(t *testing.T, s itServer) {
	t.Helper()
	ctx := context.Background()
	a, b := s.open(t), s.open(t)
	a.SetMaxOpenConns(1)
	b.SetMaxOpenConns(1)
	ta, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	tb, err := b.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ta.Exec(`UPDATE opskest_it SET n = n + 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := tb.Exec(`UPDATE opskest_it SET n = n + 1 WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	go func() { _, err := ta.Exec(`UPDATE opskest_it SET n = n + 1 WHERE id = 2`); errs <- err }()
	time.Sleep(time.Second)
	go func() { _, err := tb.Exec(`UPDATE opskest_it SET n = n + 1 WHERE id = 1`); errs <- err }()
	failed := 0
	for i := 0; i < 2; i++ {
		select {
		case err := <-errs:
			if err != nil {
				failed++
				ta.Rollback()
				tb.Rollback()
			}
		case <-time.After(15 * time.Second):
			t.Fatal("deadlock never resolved")
		}
	}
	ta.Rollback()
	tb.Rollback()
	if failed == 0 {
		t.Fatal("the server did not report a deadlock")
	}
}

// TLS: the server must present a certificate that is NOT trusted (self
// signed), so a strict connection fails with a TLS error while "ignore TLS
// errors" connects. Skipped unless the same env vars as above are set and
// the server has TLS switched on.
func TestPostgresTLSIntegration(t *testing.T) {
	runSQLTLS(t, itFromEnv(t, "OPSKEST_IT_PG_TLS", model.TypePostgresSQL))
}
func TestMySQLTLSIntegration(t *testing.T) {
	runSQLTLS(t, itFromEnv(t, "OPSKEST_IT_MYSQL", model.TypeMySQLSQL))
}

func runSQLTLS(t *testing.T, s itServer) {
	strict := s.run(t, s.monitor(0, func(m *model.Monitor) { m.Settings.TLS = true }))
	if strict.OK || strict.ErrClass != model.ErrTLS {
		t.Fatalf("a self-signed certificate must fail a strict TLS connection: %+v", strict)
	}
	for _, secret := range []string{s.pass} {
		if strings.Contains(strict.Reason, secret) {
			t.Fatalf("reason leaks the password: %s", strict.Reason)
		}
	}
	lax := s.run(t, s.monitor(0, func(m *model.Monitor) { m.Settings.TLS, m.Settings.IgnoreTLS = true, true }))
	if !lax.OK {
		t.Fatalf("ignoring TLS errors must connect: %+v", lax)
	}
	plain := s.run(t, s.monitor(0, nil))
	if !plain.OK {
		t.Fatalf("the plain connection still works: %+v", plain)
	}
}
