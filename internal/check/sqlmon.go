package check

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	mysqldrv "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"

	"opskest/internal/model"
)

// Database monitors log in (Settings.BasicUser and Secrets.BasicPass) and run
// either the monitor's own read-only query or a built-in health probe:
//
//	query         run Settings.Query, optionally compare its first value
//	deadlocks     deadlocks since the previous check
//	long_running  queries running longer than ThresholdSecs
//	blocked       sessions waiting on a lock
//	connections   percent of the connection limit in use
//
// Everything runs in a read-only transaction. Failure reasons carry counts
// and durations, never query text, row contents or credentials.

func init() {
	// The MySQL driver logs connection hiccups to stderr by default.
	_ = mysqldrv.SetLogger(log.New(io.Discard, "", 0))
	Register(model.TypePostgresSQL, &sqlChecker{dialect: "postgres", last: map[int64]deadlockBaseline{}})
	Register(model.TypeMySQLSQL, &sqlChecker{dialect: "mysql", last: map[int64]deadlockBaseline{}})
}

// deadlockBaseline is the previous deadlock counter of one monitor, tied to
// the server it was read from: pointing the monitor at another server (or
// another login) must start a new baseline, not compare two unrelated counters.
type deadlockBaseline struct {
	server string
	total  float64
}

type sqlChecker struct {
	dialect string

	mu   sync.Mutex
	last map[int64]deadlockBaseline // monitor id -> previous deadlock counter
}

func (c *sqlChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	db, err := c.open(ctx, m)
	if err != nil {
		return sqlFail(m, err)
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return sqlFail(m, err)
	}
	defer tx.Rollback()

	s := m.Settings
	switch s.Probe {
	case "deadlocks":
		return c.deadlocks(ctx, tx, m)
	case "long_running":
		return c.longRunning(ctx, tx, m)
	case "blocked":
		return c.blocked(ctx, tx, m)
	case "connections":
		return c.connections(ctx, tx, m)
	default:
		return c.query(ctx, tx, m)
	}
}

// ── connecting ────────────────────────────────────────────────────

func quoteDSN(v string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v) + "'"
}

func (c *sqlChecker) open(ctx context.Context, m *model.Monitor) (*sql.DB, error) {
	host, port, err := net.SplitHostPort(m.Target)
	if err != nil {
		return nil, err
	}
	// The monitor form validates this, but the "test" endpoint and old rows
	// don't: the port goes into a key=value connection string unquoted, and a
	// host starting with / or @ would make the PostgreSQL driver dial a local
	// unix socket instead of the network.
	if n, perr := strconv.Atoi(port); perr != nil || n < 1 || n > 65535 || host == "" || host[0] == '/' || host[0] == '@' {
		return nil, errors.New("invalid connection settings")
	}
	s := m.Settings
	var db *sql.DB

	switch c.dialect {
	case "postgres":
		mode := "prefer"
		if s.TLS {
			mode = "verify-full"
			if s.IgnoreTLS {
				mode = "require"
			}
		}
		dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
			quoteDSN(host), port, quoteDSN(s.BasicUser), quoteDSN(m.Secrets.BasicPass), quoteDSN(s.Database), mode)
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			return nil, errors.New("invalid connection settings")
		}
		cfg.RuntimeParams["default_transaction_read_only"] = "on"
		cfg.RuntimeParams["application_name"] = "opskest"
		db = stdlib.OpenDB(*cfg)
	default:
		cfg := mysqldrv.NewConfig()
		cfg.User, cfg.Passwd, cfg.Net, cfg.Addr, cfg.DBName = s.BasicUser, m.Secrets.BasicPass, "tcp", m.Target, s.Database
		if dl, ok := ctx.Deadline(); ok {
			cfg.Timeout = time.Until(dl)
		}
		if s.TLS {
			cfg.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host, InsecureSkipVerify: s.IgnoreTLS}
		}
		conn, err := mysqldrv.NewConnector(cfg)
		if err != nil {
			return nil, errors.New("invalid connection settings")
		}
		db = sql.OpenDB(conn)
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(time.Minute)
	return db, nil
}

// sqlFail turns a driver error into a bounded, secret-free failure.
func sqlFail(m *model.Monitor, err error) model.Result {
	var pg *pgconn.PgError
	var my *mysqldrv.MySQLError
	switch {
	case errors.As(err, &pg):
		msg := pg.Message
		if strings.HasPrefix(pg.Code, "22") || strings.HasPrefix(pg.Code, "23") { // data exception, integrity violation
			msg = redactQuoted(msg)
		}
		return model.Result{LatencyMs: -1, ErrClass: model.ErrOther,
			Reason: model.TrimReason(fmt.Sprintf("database error %s: %s", pg.Code, shortErr(errors.New(msg))))}
	case errors.As(err, &my):
		msg := my.Message
		if mysqlDataErrors[my.Number] {
			msg = redactQuoted(msg)
		}
		return model.Result{LatencyMs: -1, ErrClass: model.ErrOther,
			Reason: model.TrimReason(fmt.Sprintf("database error %d: %s", my.Number, shortErr(errors.New(msg))))}
	}
	return classifyDialErr(m, err)
}

// mysqlDataErrors are the server errors whose message quotes a value it
// choked on ("Truncated incorrect DOUBLE value: 'alice@example.com'").
var mysqlDataErrors = map[uint16]bool{1048: true, 1062: true, 1263: true, 1264: true, 1265: true, 1292: true, 1366: true, 1406: true, 1690: true}

var quotedValue = regexp.MustCompile(`"[^"]*"|'[^']*'`)

// redactQuoted hides quoted text in a server message: for data errors that
// text is a row value, which a failure reason must never carry.
func redactQuoted(s string) string { return quotedValue.ReplaceAllString(s, "…") }

func assertFail(format string, args ...any) model.Result {
	return protoFail(fmt.Sprintf(format, args...))
}

// ── probes ────────────────────────────────────────────────────────

func okAfter(start time.Time) model.Result { return model.Result{OK: true, LatencyMs: msSince(start)} }

func (c *sqlChecker) deadlocks(ctx context.Context, tx *sql.Tx, m *model.Monitor) model.Result {
	start := time.Now()
	var total float64
	var err error
	if c.dialect == "postgres" {
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(sum(deadlocks), 0)::float8 FROM pg_stat_database`).Scan(&total)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT COUNT FROM information_schema.INNODB_METRICS WHERE NAME = 'lock_deadlocks'`).Scan(&total)
		if err != nil { // MariaDB keeps the counter in the status variables
			var name string
			err = tx.QueryRowContext(ctx, `SHOW GLOBAL STATUS LIKE 'Innodb_deadlocks'`).Scan(&name, &total)
		}
	}
	if err != nil {
		return sqlFail(m, err)
	}
	res := okAfter(start)

	// Deadlocks are a counter the server only ever raises: alert on how many
	// happened since the last check. The first check just records a baseline.
	server := m.Target + "\x00" + m.Settings.BasicUser + "\x00" + m.Settings.Database
	c.mu.Lock()
	base, seen := c.last[m.ID]
	if m.ID != 0 {
		c.last[m.ID] = deadlockBaseline{server: server, total: total}
	}
	c.mu.Unlock()
	prev := base.total
	if !seen || base.server != server || total < prev { // first look, another server, or the counter was reset by a restart
		return res
	}
	if delta := int(total - prev); delta > m.Settings.Threshold {
		return assertFail("%d new deadlock%s since the last check", delta, plural(delta))
	}
	return res
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (c *sqlChecker) longRunning(ctx context.Context, tx *sql.Tx, m *model.Monitor) model.Result {
	start := time.Now()
	secs := m.Settings.ThresholdSecs
	var count int
	var longest float64
	var err error
	if c.dialect == "postgres" {
		err = tx.QueryRowContext(ctx, `
			SELECT count(*), COALESCE(max(EXTRACT(EPOCH FROM (now() - query_start))), 0)::float8
			FROM pg_stat_activity
			WHERE state = 'active' AND backend_type = 'client backend' AND pid <> pg_backend_pid()
			  AND now() - query_start > make_interval(secs => $1::float8)`, float64(secs)).Scan(&count, &longest)
	} else {
		err = tx.QueryRowContext(ctx, `
			SELECT COUNT(*), COALESCE(MAX(TIME), 0) FROM information_schema.PROCESSLIST
			WHERE COMMAND IN ('Query', 'Execute') AND ID <> CONNECTION_ID() AND TIME > ?`, secs).Scan(&count, &longest)
	}
	if err != nil {
		return sqlFail(m, err)
	}
	if count > m.Settings.Threshold {
		return assertFail("%d quer%s running longer than %d s (longest %d s)", count, map[bool]string{true: "y", false: "ies"}[count == 1], secs, int(longest))
	}
	return okAfter(start)
}

func (c *sqlChecker) blocked(ctx context.Context, tx *sql.Tx, m *model.Monitor) model.Result {
	start := time.Now()
	q := `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock'`
	if c.dialect != "postgres" {
		q = `SELECT COUNT(*) FROM information_schema.INNODB_TRX WHERE trx_state = 'LOCK WAIT'`
	}
	var n int
	if err := tx.QueryRowContext(ctx, q).Scan(&n); err != nil {
		return sqlFail(m, err)
	}
	if n > m.Settings.Threshold {
		return assertFail("%d session%s waiting on a lock", n, plural(n))
	}
	return okAfter(start)
}

func (c *sqlChecker) connections(ctx context.Context, tx *sql.Tx, m *model.Monitor) model.Result {
	start := time.Now()
	var pct float64
	if c.dialect == "postgres" {
		err := tx.QueryRowContext(ctx, `
			SELECT 100.0 * count(*) / current_setting('max_connections')::float8
			FROM pg_stat_activity WHERE backend_type = 'client backend'`).Scan(&pct)
		if err != nil {
			return sqlFail(m, err)
		}
	} else {
		var name string
		var used, limit float64
		if err := tx.QueryRowContext(ctx, `SHOW GLOBAL STATUS LIKE 'Threads_connected'`).Scan(&name, &used); err != nil {
			return sqlFail(m, err)
		}
		if err := tx.QueryRowContext(ctx, `SELECT @@max_connections`).Scan(&limit); err != nil {
			return sqlFail(m, err)
		}
		if limit > 0 {
			pct = 100 * used / limit
		}
	}
	if pct > float64(m.Settings.Threshold) {
		return assertFail("connections at %.0f%% of the limit (alert above %d%%)", pct, m.Settings.Threshold)
	}
	return okAfter(start)
}

// ── custom query ──────────────────────────────────────────────────

func (c *sqlChecker) query(ctx context.Context, tx *sql.Tx, m *model.Monitor) model.Result {
	s := m.Settings
	if p := model.QueryProblem(s.Query); p != "" {
		return protoFail("the query is not allowed: " + p)
	}
	start := time.Now()
	// Only the first row matters. Cancelling the statement once it has been
	// read stops the server streaming the rest (and stops rows.Close from
	// draining it): a SELECT with millions of rows must not hold the check
	// open far past its timeout.
	qctx, stop := context.WithCancel(ctx)
	defer stop()
	rows, err := tx.QueryContext(qctx, strings.TrimSuffix(strings.TrimSpace(s.Query), ";"))
	if err != nil {
		return sqlFail(m, err)
	}
	defer rows.Close()

	var first any
	hasRow := rows.Next()
	if hasRow {
		cols, err := rows.Columns()
		if err != nil {
			return sqlFail(m, err)
		}
		dest := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return sqlFail(m, err)
		}
		first = dest[0]
	}
	if err := rows.Err(); err != nil {
		return sqlFail(m, err)
	}
	elapsed := msSince(start)
	res := model.Result{OK: true, LatencyMs: elapsed}
	stop()

	if s.QueryOp == "" {
		return res
	}
	if !hasRow {
		return assertFail("the query returned no rows")
	}
	got := sqlString(first)
	if !compareQuery(got, s.QueryOp, s.QueryExpected) {
		// Only a number is echoed; text results may be sensitive.
		if _, err := strconv.ParseFloat(got, 64); err == nil {
			return assertFail("the query returned %s, expected %s %s", got, sqlOpWord(s.QueryOp), s.QueryExpected)
		}
		return assertFail("the query result did not match (%s)", sqlOpWord(s.QueryOp))
	}
	return res
}

func sqlOpWord(op string) string {
	return map[string]string{"eq": "=", "ne": "≠", "gt": ">", "gte": "≥", "lt": "<", "lte": "≤", "contains": "contains"}[op]
}

// sqlString renders a scanned first column as text.
func sqlString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	case string:
		return x
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	default:
		return fmt.Sprint(x)
	}
}

// compareQuery applies op between got and want: numerically when both are
// numbers, otherwise as text (contains is always text).
func compareQuery(got, op, want string) bool {
	want = strings.TrimSpace(want)
	if op == "contains" {
		return strings.Contains(got, want)
	}
	g, gerr := strconv.ParseFloat(strings.TrimSpace(got), 64)
	w, werr := strconv.ParseFloat(want, 64)
	if gerr != nil || werr != nil {
		switch op {
		case "eq":
			return got == want
		case "ne":
			return got != want
		}
		return false
	}
	switch op {
	case "eq":
		return g == w
	case "ne":
		return g != w
	case "gt":
		return g > w
	case "gte":
		return g >= w
	case "lt":
		return g < w
	case "lte":
		return g <= w
	}
	return false
}
