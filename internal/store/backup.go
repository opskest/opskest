package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BackupFile describes one file in the backups directory.
type BackupFile struct {
	Name    string
	Size    int64
	ModTime time.Time
}

// backupNamePattern is the only shape of filename ListBackups (and, by
// extension, pruneBackups) will ever recognize as a backup: exactly
// "opskest-YYYYMMDD-HHMM.db", the format Backup itself produces. Anything
// else in the directory - an operator's pre-upgrade.db, a manual copy, an
// in-progress .part file - is left alone: never listed, and, critically,
// never a candidate for deletion. Without this, ListBackups treated every
// non-.part file as a backup, so a foreign file could itself be deleted
// by rotation, or, if its name sorted after the real backups', could be
// mistaken for the newest and cause an actual just-created backup to be
// pruned instead.
var backupNamePattern = regexp.MustCompile(`^opskest-\d{8}-\d{4}\.db$`)

// preRestoreNamePattern matches the safety copy BootSwapRestore makes of
// the current database just before swapping in a restored one. It is a
// separate prefix, never a candidate for pruneBackups' rotation (which
// only ever counts and removes opskest-*.db files): a pre-restore copy is
// an operator safety net, not part of the nightly-backup rotation, so it
// must survive that rotation regardless of BackupKeep.
var preRestoreNamePattern = regexp.MustCompile(`^pre-restore-\d{8}-\d{4}\.db$`)

// IsBackupFileName reports whether name is a nightly/manual backup
// produced by Backup (never a pre-restore safety copy).
func IsBackupFileName(name string) bool {
	return backupNamePattern.MatchString(name)
}

// IsAnyBackupFileName reports whether name is either a nightly/manual
// backup or a pre-restore safety copy - the two kinds of file the
// Backups UI lists and may restore or delete.
func IsAnyBackupFileName(name string) bool {
	return backupNamePattern.MatchString(name) || preRestoreNamePattern.MatchString(name)
}

// Backup takes a consistent snapshot of the database into dir via
// VACUUM INTO, through the writer connection: it writes to a .part file
// first and renames it into place only once the snapshot is complete, so
// a reader of dir never sees a half-written backup. Backups beyond keep
// are then pruned oldest-first; the newest is never pruned, even if keep
// is 0.
func (s *Store) Backup(ctx context.Context, dir string, now time.Time, keep int) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("store: create backup dir: %w", err)
	}

	name := "opskest-" + now.Format("20060102-1504") + ".db"
	finalPath := filepath.Join(dir, name)
	partPath := finalPath + ".part"

	// Clear out a stale .part left by a previous failed attempt at the
	// same minute before starting a fresh one.
	_ = os.Remove(partPath)

	if _, err := s.W.ExecContext(ctx, `VACUUM INTO ?`, partPath); err != nil {
		os.Remove(partPath)
		return "", fmt.Errorf("store: vacuum into: %w", err)
	}

	if err := os.Rename(partPath, finalPath); err != nil {
		os.Remove(partPath)
		return "", fmt.Errorf("store: rename backup into place: %w", err)
	}

	if err := pruneBackups(dir, keep); err != nil {
		return finalPath, err
	}
	return finalPath, nil
}

// pruneBackups removes every backup beyond the keep newest, never the
// single newest one even if keep <= 0.
func pruneBackups(dir string, keep int) error {
	if keep < 1 {
		keep = 1
	}

	files, err := ListBackups(dir)
	if err != nil {
		return err
	}
	if len(files) <= keep {
		return nil
	}

	for _, f := range files[keep:] {
		if err := os.Remove(filepath.Join(dir, f.Name)); err != nil {
			return fmt.Errorf("store: prune backup %s: %w", f.Name, err)
		}
	}
	return nil
}

// ListBackups lists dir's backup files, newest first, recognizing only
// names matching backupNamePattern - never an in-progress .part file, and
// never any other file that happens to live in the backups directory.
// Sorting is by filename (the fixed opskest-YYYYMMDD-HHMM.db format sorts
// chronologically as a string) rather than by filesystem ModTime, since a
// backup rotation can create several files within the same wall-clock
// second.
func ListBackups(dir string) ([]BackupFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: list backups: %w", err)
	}

	var out []BackupFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !backupNamePattern.MatchString(name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, fmt.Errorf("store: stat backup %s: %w", name, err)
		}
		out = append(out, BackupFile{Name: name, Size: info.Size(), ModTime: info.ModTime()})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out, nil
}

// ListAllBackupFiles lists dir's backup files for display: both
// nightly/manual backups and pre-restore safety copies, newest first.
// Unlike ListBackups (whose sort relies on every name sharing the
// "opskest-" prefix so a plain string sort is already chronological), the
// two prefixes here sort a pre-restore file before a same-minute uptime
// file lexically even when it's newer, so this sorts by the fixed
// "YYYYMMDD-HHMM" suffix both name shapes share instead.
func ListAllBackupFiles(dir string) ([]BackupFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: list backups: %w", err)
	}

	var out []BackupFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !IsAnyBackupFileName(name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, fmt.Errorf("store: stat backup %s: %w", name, err)
		}
		out = append(out, BackupFile{Name: name, Size: info.Size(), ModTime: info.ModTime()})
	}

	sort.Slice(out, func(i, j int) bool { return backupTimestampSuffix(out[i].Name) > backupTimestampSuffix(out[j].Name) })
	return out, nil
}

// backupTimestampSuffix returns the "YYYYMMDD-HHMM" portion shared by both
// backup name shapes (opskest-*.db, pre-restore-*.db), for sorting mixed
// prefixes chronologically regardless of prefix length.
func backupTimestampSuffix(name string) string {
	base := strings.TrimSuffix(name, ".db")
	const tsLen = len("20060102-1504")
	if len(base) < tsLen {
		return base
	}
	return base[len(base)-tsLen:]
}

// DeleteBackupFile removes name from dir. It returns ErrNotFound if name
// doesn't look like a backup file (either shape) or doesn't exist -
// never a raw filesystem error that might leak a path outside dir.
func DeleteBackupFile(dir, name string) error {
	path, err := resolveBackupPath(dir, name, IsAnyBackupFileName)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("store: delete backup: %w", err)
	}
	return nil
}

// BackupFilePath resolves name (either a nightly/manual backup or a
// pre-restore safety copy) to its full path in dir, or ErrNotFound.
func BackupFilePath(dir, name string) (string, error) {
	return resolveBackupPath(dir, name, IsAnyBackupFileName)
}

// UptimeBackupFilePath resolves name to its full path in dir, accepting
// only a nightly/manual backup (never a pre-restore copy) - used by the
// download endpoint, which the spec pins to exactly this shape.
func UptimeBackupFilePath(dir, name string) (string, error) {
	return resolveBackupPath(dir, name, IsBackupFileName)
}

// resolveBackupPath validates name against allowed and confirms the file
// exists, without ever stat'ing a path outside dir (name is joined, never
// a caller-supplied path).
func resolveBackupPath(dir, name string, allowed func(string) bool) (string, error) {
	if !allowed(name) {
		return "", ErrNotFound
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("store: stat backup: %w", err)
	}
	return path, nil
}

// BackupRun records the outcome of the most recent backup attempt
// (scheduled or manual), for the Backups UI's "last run" status line.
type BackupRun struct {
	At    time.Time
	OK    bool
	Error string
}

const (
	keyLastBackupAt    = "last_backup_at"
	keyLastBackupOK    = "last_backup_ok"
	keyLastBackupError = "last_backup_error"
)

// RecordBackupRun upserts the last-backup-run status. It is called after
// every backup attempt, scheduled or manual, so the UI always reflects
// the most recent one regardless of which triggered it.
func (s *Store) RecordBackupRun(ctx context.Context, at time.Time, ok bool, errMsg string) error {
	okStr := "0"
	if ok {
		okStr = "1"
	}
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	for k, v := range map[string]string{
		keyLastBackupAt:    fmt.Sprintf("%d", at.UnixMilli()),
		keyLastBackupOK:    okStr,
		keyLastBackupError: errMsg,
	} {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings(key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v,
		); err != nil {
			return fmt.Errorf("store: record backup run %s: %w", k, err)
		}
	}
	return tx.Commit()
}

// GetLastBackupRun returns the most recently recorded backup run, or nil
// if none has been recorded yet (a fresh install before its first
// scheduled or manual backup).
func (s *Store) GetLastBackupRun(ctx context.Context) (*BackupRun, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT key, value FROM settings WHERE key IN (?, ?, ?)`,
		keyLastBackupAt, keyLastBackupOK, keyLastBackupError)
	if err != nil {
		return nil, fmt.Errorf("store: get last backup run: %w", err)
	}
	defer rows.Close()

	raw := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("store: scan backup run: %w", err)
		}
		raw[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	atRaw, ok := raw[keyLastBackupAt]
	if !ok {
		return nil, nil
	}
	atMs, err := strconv.ParseInt(atRaw, 10, 64)
	if err != nil {
		return nil, nil
	}
	return &BackupRun{
		At:    time.UnixMilli(atMs),
		OK:    raw[keyLastBackupOK] == "1",
		Error: raw[keyLastBackupError],
	}, nil
}
