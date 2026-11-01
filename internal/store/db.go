// Package store provides the SQLite persistence layer: a single writer
// connection plus a read pool, embedded schema migrations, and one
// repository file per config entity (users, sessions, groups, tags,
// monitors, channels, settings).
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"opskest/internal/secret"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ErrNotFound is returned when a lookup by id, token or key finds no row.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when a write would violate a unique constraint
// (a monitor's push token, a group name, a tag name, a status page's
// slug) or, for SetUserRole, would leave zero admins.
var ErrConflict = errors.New("conflict")

// ErrInvalid is returned when a write would violate a CHECK constraint
// (an incident's status/impact, a status page's visibility/layout, a
// maintenance window's kind/tz, ...), so the API can map it to a 400
// instead of a generic 500.
var ErrInvalid = errors.New("invalid")

// dbFileName is the SQLite database file inside dataDir.
const dbFileName = "opskest.db"

// Store is the SQLite persistence layer. W is the single writer
// connection (SetMaxOpenConns(1)) used for every write and for reads that
// must be transactionally consistent with a write in progress. R is a
// read-only pool (SetMaxOpenConns(8)) used for plain reads, so readers
// never queue behind the writer.
type Store struct {
	W   *sql.DB
	R   *sql.DB
	Box *secret.Box
	Loc *time.Location // DISPLAY_TZ, for daily rollup buckets

	// Log is used by best-effort writes (Audit) to report a failure
	// without failing the caller. Open never sets it, so it defaults to
	// slog.Default() at first use (see log()); set it directly on a
	// *Store when a caller wants best-effort failures routed elsewhere.
	Log *slog.Logger

	// path is the database file, for Usage's size report.
	path string
}

// log returns s.Log, falling back to slog.Default() when unset.
func (s *Store) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Open creates dataDir if needed, opens (creating if needed)
// dataDir/opskest.db, applies pragmas, and runs the embedded migrations.
func Open(dataDir string, box *secret.Box, loc *time.Location) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}
	dbPath := filepath.Join(dataDir, dbFileName)

	w, err := sql.Open("sqlite", writerDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("store: open writer: %w", err)
	}
	w.SetMaxOpenConns(1)

	if err := prepareNewDatabase(w); err != nil {
		w.Close()
		return nil, err
	}
	if err := migrate(w); err != nil {
		w.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}

	r, err := sql.Open("sqlite", readerDSN(dbPath))
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("store: open reader: %w", err)
	}
	r.SetMaxOpenConns(8)

	return &Store{W: w, R: r, Box: box, Loc: loc, path: dbPath}, nil
}

// Close closes both connections. It closes the read pool first so no new
// reads race with the writer connection shutting down.
func (s *Store) Close() error {
	rErr := s.R.Close()
	wErr := s.W.Close()
	if rErr != nil {
		return rErr
	}
	return wErr
}

// writerDSN builds the single-writer connection string: WAL journal mode,
// NORMAL synchronous (safe under WAL), a 5s busy timeout so the single
// writer never fails immediately under contention, and foreign keys on.
func writerDSN(path string) string {
	return "file:" + path +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)"
}

// readerDSN is the writer DSN plus mode=ro and query_only(1), so the read
// pool can never write even by accident.
func readerDSN(path string) string {
	return writerDSN(path) +
		"&_pragma=query_only(1)" +
		"&mode=ro"
}

// prepareNewDatabase sets auto_vacuum=INCREMENTAL before any table
// exists. This only runs once, on a brand-new database (user_version 0
// and no tables yet): auto_vacuum cannot be changed after tables are
// created without a full VACUUM, and re-running this on an
// already-initialized database on every boot would be wasted work.
func prepareNewDatabase(w *sql.DB) error {
	isNew, err := isNewDatabase(w)
	if err != nil {
		return fmt.Errorf("store: inspect database: %w", err)
	}
	if !isNew {
		return nil
	}
	if _, err := w.Exec(`PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
		return fmt.Errorf("store: set auto_vacuum: %w", err)
	}
	if _, err := w.Exec(`VACUUM`); err != nil {
		return fmt.Errorf("store: vacuum: %w", err)
	}
	return nil
}

func isNewDatabase(w *sql.DB) (bool, error) {
	var userVersion int
	if err := w.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		return false, err
	}
	if userVersion != 0 {
		return false, nil
	}
	var tableCount int
	if err := w.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table'`).Scan(&tableCount); err != nil {
		return false, err
	}
	return tableCount == 0, nil
}

// wrapConflict maps a SQLite unique-constraint violation to ErrConflict
// and a CHECK-constraint violation (an invalid enum-like value such as
// an incident status or a status page visibility) to ErrInvalid, leaving
// every other error (including nil) untouched.
func wrapConflict(err error) error {
	if err == nil {
		return nil
	}
	// Prefer SQLite's extended result codes; fall back to the message text
	// only if a driver ever stops exposing them.
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return ErrConflict
		case sqlite3.SQLITE_CONSTRAINT_CHECK:
			return ErrInvalid
		}
	}
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrConflict
	}
	if strings.Contains(err.Error(), "CHECK constraint failed") {
		return ErrInvalid
	}
	return err
}

// checkAffected returns ErrNotFound when res reports zero affected rows,
// which is how Update*/Delete* methods report "no such row" without a
// separate existence check.
func checkAffected(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// scanner is satisfied by both *sql.Row and *sql.Rows, so a single
// row-decoding helper can serve both a Get and a List query.
type scanner interface {
	Scan(dest ...any) error
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func intToBool(n int64) bool {
	return n != 0
}

// int64OrNil returns nil for a nil pointer, so it binds as SQL NULL, and
// the pointed-to value otherwise. Used for nullable *int64 model fields
// (Monitor.GroupID, Monitor.CreatedBy).
func int64OrNil(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func scanNullInt64(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// nullString returns nil for an empty string so it binds as SQL NULL.
// Used for push_token, whose column is UNIQUE and so cannot hold more
// than one empty string.
func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
