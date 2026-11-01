package store

import (
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"opskest/internal/logx"
)

// RestorePendingFileName is the staged-candidate file BootSwapRestore
// looks for, in DataDir, at the start of every boot, before Open.
const RestorePendingFileName = "restore-pending.db"

// ValidateBackupFile opens path read-only (never through the writer
// connection, and never with the app's own DB open at the same time) and
// confirms it is safe to become the live database: SQLite considers it
// intact, it has the tables a opskest database must have, and every
// migration recorded in it is one this binary also knows about. That last
// check is what "migration version <= current" means in practice: a
// candidate stamped with a migration name this build has never heard of
// can only have come from a newer binary, and loading it would silently
// downgrade the schema out from under future migrations, so it is
// refused rather than guessed at.
func ValidateBackupFile(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("store: candidate file: %w", err)
	}

	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=query_only(1)&mode=ro"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("store: open candidate: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return fmt.Errorf("store: candidate integrity check: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("store: candidate failed integrity check: %s", integrity)
	}

	for _, tbl := range []string{"schema_migrations", "monitors"} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&name); err != nil {
			return fmt.Errorf("store: candidate is missing table %q", tbl)
		}
	}

	known, err := migrationNames()
	if err != nil {
		return fmt.Errorf("store: read known migrations: %w", err)
	}
	knownSet := make(map[string]bool, len(known))
	for _, n := range known {
		knownSet[n] = true
	}

	rows, err := db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("store: read candidate migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return fmt.Errorf("store: scan candidate migration: %w", err)
		}
		if !knownSet[v] {
			return fmt.Errorf("store: candidate has migration %q this build does not recognize (likely from a newer version); refusing to restore", v)
		}
	}
	return rows.Err()
}

// StageRestore copies src (already validated by the caller via
// ValidateBackupFile) into dataDir/restore-pending.db, replacing any
// previously staged candidate. It writes to a .part file first and
// renames into place, so a concurrent boot never sees a half-written
// candidate.
func StageRestore(dataDir, src string) (string, error) {
	dst := filepath.Join(dataDir, RestorePendingFileName)
	if err := copyFileAtomic(src, dst); err != nil {
		return "", fmt.Errorf("store: stage restore: %w", err)
	}
	return dst, nil
}

// copyFileAtomic copies src to dst via a .part file plus rename, so a
// reader of dst's directory never observes a partially written file.
func copyFileAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer in.Close()

	part := dst + ".part"
	_ = os.Remove(part)
	out, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(part)
		return fmt.Errorf("copy: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(part)
		return fmt.Errorf("close dest: %w", err)
	}
	if err := os.Rename(part, dst); err != nil {
		os.Remove(part)
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}

// BootSwapRestore runs at the very start of boot, before Open: if
// dataDir/restore-pending.db exists, it re-validates it (defense in
// depth - the candidate was already validated once at staging time, but
// this runs after an arbitrary amount of time and an arbitrary shutdown
// in between), safety-copies the current opskest.db into backupDir as
// pre-restore-YYYYMMDD-HHMM.db, drops the WAL/SHM sidecars so the
// swapped-in file starts clean, and renames the candidate into place.
//
// Every failure path here logs and returns nil rather than aborting
// boot: a corrupt staged candidate, a backup dir that can't be written,
// or a rename that fails all leave the current opskest.db exactly where
// it was, because each step either hasn't touched it yet or (for the
// rename) is atomic - there is no state in which this function leaves
// neither the old nor the new database in place.
func BootSwapRestore(dataDir, backupDir string, now time.Time, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	pending := filepath.Join(dataDir, RestorePendingFileName)
	if _, err := os.Stat(pending); err != nil {
		return nil // no staged restore (the overwhelmingly common case) or an unreadable dataDir, which checkWritable already ruled out before this runs
	}

	if err := ValidateBackupFile(pending); err != nil {
		logx.Err(log, "boot restore: staged database failed validation; keeping current database", err)
		_ = os.Remove(pending)
		return nil
	}

	dbPath := filepath.Join(dataDir, dbFileName)
	if _, statErr := os.Stat(dbPath); statErr == nil {
		if err := os.MkdirAll(backupDir, 0o755); err != nil {
			logx.Err(log, "boot restore: cannot create backup dir for safety copy; aborting restore, keeping current database", err)
			return nil
		}
		safetyName := "pre-restore-" + now.Format("20060102-1504") + ".db"
		if err := copyFileAtomic(dbPath, filepath.Join(backupDir, safetyName)); err != nil {
			logx.Err(log, "boot restore: safety copy failed; aborting restore, keeping current database", err)
			return nil
		}
		log.Info("boot restore: safety copy of current database created", "name", safetyName)
	}

	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")

	if err := os.Rename(pending, dbPath); err != nil {
		logx.Err(log, "boot restore: swap failed; keeping current database", err)
		return nil
	}
	log.Info("boot restore: swapped in restored database")
	return nil
}
