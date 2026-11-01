package store

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opskest/internal/model"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// makeRealBackup opens a fresh store, seeds it with one monitor so the
// backup isn't a totally empty schema, and returns the path to a real
// opskest-*.db backup file produced by Store.Backup.
func makeRealBackup(t *testing.T) string {
	t.Helper()
	st := newTestStore(t)
	m := model.NewMonitor(model.TypeHTTP)
	m.Name = "m"
	m.Target = "http://x"
	if err := st.CreateMonitor(context.Background(), m); err != nil {
		t.Fatalf("seed monitor: %v", err)
	}
	backupDir := t.TempDir()
	path, err := st.Backup(context.Background(), backupDir, time.Now(), 5)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	return path
}

func TestValidateBackupFileAcceptsRealBackup(t *testing.T) {
	path := makeRealBackup(t)
	if err := ValidateBackupFile(path); err != nil {
		t.Fatalf("ValidateBackupFile: %v", err)
	}
}

func TestValidateBackupFileRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "garbage.db")
	if err := os.WriteFile(path, []byte("not a sqlite database"), 0o644); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	if err := ValidateBackupFile(path); err == nil {
		t.Fatal("ValidateBackupFile: want error for non-sqlite file")
	}
}

func TestValidateBackupFileRejectsMissingTables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.db")
	// A valid empty SQLite file (via a fresh store with no monitors table
	// touched) still has monitors from migrations; instead build one with
	// no tables at all by opening and closing immediately.
	db, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Write the SQLite file header magic so it "looks like" a database to
	// a naive check, but has no tables - open() via modernc will still
	// treat an empty file as a valid empty database, and integrity_check
	// passes on it, so this exercises the "missing table" branch instead
	// of the corruption branch.
	db.Close()
	if err := ValidateBackupFile(path); err == nil {
		t.Fatal("ValidateBackupFile: want error for a database missing required tables")
	} else if !strings.Contains(err.Error(), "missing table") {
		t.Fatalf("ValidateBackupFile error = %v, want mention of missing table", err)
	}
}

func TestValidateBackupFileRejectsUnknownMigration(t *testing.T) {
	path := makeRealBackup(t)
	dsn := "file:" + path
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open backup for tampering: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES ('9999_from_the_future.sql', 0)`); err != nil {
		db.Close()
		t.Fatalf("tamper: %v", err)
	}
	db.Close()

	if err := ValidateBackupFile(path); err == nil {
		t.Fatal("ValidateBackupFile: want error for a migration this build doesn't know")
	}
}

func TestStageRestoreCopiesIntoPlace(t *testing.T) {
	path := makeRealBackup(t)
	dataDir := t.TempDir()

	staged, err := StageRestore(dataDir, path)
	if err != nil {
		t.Fatalf("StageRestore: %v", err)
	}
	if staged != filepath.Join(dataDir, RestorePendingFileName) {
		t.Fatalf("staged path = %s", staged)
	}
	if err := ValidateBackupFile(staged); err != nil {
		t.Fatalf("staged file should still validate: %v", err)
	}
	if _, err := os.Stat(staged + ".part"); !os.IsNotExist(err) {
		t.Fatalf("expected no leftover .part file, stat err = %v", err)
	}
}

func TestBootSwapRestoreSwapsInPending(t *testing.T) {
	backupPath := makeRealBackup(t)

	dataDir := t.TempDir()
	backupDir := t.TempDir()

	// Simulate an existing current database.
	currentPath := filepath.Join(dataDir, dbFileName)
	if err := os.WriteFile(currentPath, []byte("old database bytes"), 0o644); err != nil {
		t.Fatalf("write current db: %v", err)
	}
	if err := os.WriteFile(currentPath+"-wal", []byte("wal"), 0o644); err != nil {
		t.Fatalf("write wal: %v", err)
	}
	if err := os.WriteFile(currentPath+"-shm", []byte("shm"), 0o644); err != nil {
		t.Fatalf("write shm: %v", err)
	}

	if _, err := StageRestore(dataDir, backupPath); err != nil {
		t.Fatalf("StageRestore: %v", err)
	}

	now := time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)
	if err := BootSwapRestore(dataDir, backupDir, now, discardLogger()); err != nil {
		t.Fatalf("BootSwapRestore: %v", err)
	}

	// The pending file is gone (renamed into place).
	if _, err := os.Stat(filepath.Join(dataDir, RestorePendingFileName)); !os.IsNotExist(err) {
		t.Fatalf("restore-pending.db should be gone, stat err = %v", err)
	}
	// The swapped-in file is the backup, not the old bytes.
	if err := ValidateBackupFile(currentPath); err != nil {
		t.Fatalf("current db after swap should validate as a real backup: %v", err)
	}
	// WAL/SHM sidecars were removed.
	if _, err := os.Stat(currentPath + "-wal"); !os.IsNotExist(err) {
		t.Fatalf("wal sidecar should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(currentPath + "-shm"); !os.IsNotExist(err) {
		t.Fatalf("shm sidecar should be removed, stat err = %v", err)
	}
	// A pre-restore safety copy of the OLD database exists.
	safetyName := "pre-restore-20260102-0304.db"
	safetyBytes, err := os.ReadFile(filepath.Join(backupDir, safetyName))
	if err != nil {
		t.Fatalf("read safety copy: %v", err)
	}
	if string(safetyBytes) != "old database bytes" {
		t.Fatalf("safety copy content = %q, want the old database's bytes", safetyBytes)
	}
}

func TestBootSwapRestoreNoOpWhenNoPending(t *testing.T) {
	dataDir := t.TempDir()
	backupDir := t.TempDir()
	currentPath := filepath.Join(dataDir, dbFileName)
	if err := os.WriteFile(currentPath, []byte("untouched"), 0o644); err != nil {
		t.Fatalf("write current db: %v", err)
	}

	if err := BootSwapRestore(dataDir, backupDir, time.Now(), discardLogger()); err != nil {
		t.Fatalf("BootSwapRestore: %v", err)
	}

	b, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatalf("read current db: %v", err)
	}
	if string(b) != "untouched" {
		t.Fatalf("current db was modified with no pending restore staged")
	}
}

func TestBootSwapRestoreRejectsCorruptPending(t *testing.T) {
	dataDir := t.TempDir()
	backupDir := t.TempDir()

	currentPath := filepath.Join(dataDir, dbFileName)
	if err := os.WriteFile(currentPath, []byte("good current db"), 0o644); err != nil {
		t.Fatalf("write current db: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, RestorePendingFileName), []byte("garbage, not sqlite"), 0o644); err != nil {
		t.Fatalf("write pending: %v", err)
	}

	if err := BootSwapRestore(dataDir, backupDir, time.Now(), discardLogger()); err != nil {
		t.Fatalf("BootSwapRestore: %v", err)
	}

	// Current DB is untouched.
	b, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatalf("read current db: %v", err)
	}
	if string(b) != "good current db" {
		t.Fatalf("current db should be untouched when the pending candidate is corrupt")
	}
	// The corrupt pending file was cleaned up.
	if _, err := os.Stat(filepath.Join(dataDir, RestorePendingFileName)); !os.IsNotExist(err) {
		t.Fatalf("corrupt restore-pending.db should have been removed, stat err = %v", err)
	}
	// No safety copy was made, since the restore never proceeded.
	entries, _ := os.ReadDir(backupDir)
	if len(entries) != 0 {
		t.Fatalf("expected no safety copy when restore is aborted, found %d files", len(entries))
	}
}

func TestRecordAndGetLastBackupRun(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if run, err := st.GetLastBackupRun(ctx); err != nil {
		t.Fatalf("GetLastBackupRun: %v", err)
	} else if run != nil {
		t.Fatalf("expected nil last run before any recorded, got %+v", run)
	}

	at := time.Date(2026, 3, 4, 5, 6, 0, 0, time.UTC)
	if err := st.RecordBackupRun(ctx, at, true, ""); err != nil {
		t.Fatalf("RecordBackupRun: %v", err)
	}
	run, err := st.GetLastBackupRun(ctx)
	if err != nil {
		t.Fatalf("GetLastBackupRun: %v", err)
	}
	if run == nil || !run.OK || run.Error != "" || !run.At.Equal(at) {
		t.Fatalf("run = %+v, want ok at %v", run, at)
	}

	at2 := at.Add(time.Hour)
	if err := st.RecordBackupRun(ctx, at2, false, "disk full"); err != nil {
		t.Fatalf("RecordBackupRun: %v", err)
	}
	run, err = st.GetLastBackupRun(ctx)
	if err != nil {
		t.Fatalf("GetLastBackupRun: %v", err)
	}
	if run == nil || run.OK || run.Error != "disk full" || !run.At.Equal(at2) {
		t.Fatalf("run = %+v, want failed run at %v", run, at2)
	}
}
