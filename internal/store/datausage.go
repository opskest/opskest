package store

import (
	"context"
	"fmt"
	"os"
	"time"
)

// TableUsage is one table's row count (and, for time-series tables, its
// oldest row) for Settings → Data.
type TableUsage struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Rows     int64  `json:"rows"`
	OldestTS *int64 `json:"oldestTs"`
}

// DataUsage is the database's on-disk size and per-table contents.
type DataUsage struct {
	FileBytes int64        `json:"fileBytes"`
	WALBytes  int64        `json:"walBytes"`
	FreeBytes int64        `json:"freeBytes"` // pages on the freelist (reclaimable by compacting)
	Tables    []TableUsage `json:"tables"`
}

// usageTables lists what Settings → Data reports, with the timestamp
// column used for "oldest".
var usageTables = []struct{ name, label, tsCol string }{
	{"heartbeats", "Check results (raw)", "ts"},
	{"rollup_hourly", "Hourly rollups", "bucket"},
	{"rollup_daily", "Daily rollups", "bucket"},
	{"status_events", "Status events", "ts"},
	{"outages", "Outages", "started_at"},
	{"alert_log", "Alert delivery log", "ts"},
	{"audit_log", "Activity log", "ts"},
	{"monitors", "Monitors", ""},
	{"incidents", "Incidents", "created_at"},
}

// Usage reports file sizes and row counts. Reads only.
func (s *Store) Usage(ctx context.Context) (DataUsage, error) {
	var u DataUsage
	if fi, err := os.Stat(s.path); err == nil {
		u.FileBytes = fi.Size()
	}
	if fi, err := os.Stat(s.path + "-wal"); err == nil {
		u.WALBytes = fi.Size()
	}
	var pageSize, free int64
	_ = s.R.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize)
	_ = s.R.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free)
	u.FreeBytes = pageSize * free

	for _, t := range usageTables {
		tu := TableUsage{Name: t.name, Label: t.label}
		if err := s.R.QueryRowContext(ctx, `SELECT count(*) FROM `+t.name).Scan(&tu.Rows); err != nil {
			return u, fmt.Errorf("store: count %s: %w", t.name, err)
		}
		if t.tsCol != "" && tu.Rows > 0 {
			var oldest int64
			if err := s.R.QueryRowContext(ctx, `SELECT min(`+t.tsCol+`) FROM `+t.name).Scan(&oldest); err == nil {
				tu.OldestTS = &oldest
			}
		}
		u.Tables = append(u.Tables, tu)
	}
	return u, nil
}

// ClearKinds are the history kinds an admin may clear. Configuration
// (monitors, channels, pages, users) is never cleared this way.
var ClearKinds = map[string]string{
	"heartbeats":    "heartbeats",
	"status_events": "status_events",
	"alert_log":     "alert_log",
	"audit_log":     "audit_log",
	"outages":       "outages",
}

// ClearHistory deletes rows of one history kind older than keepDays (0 =
// everything), optionally for one monitor only. Active (open) outages are
// never deleted. It returns how many rows were removed. Heartbeats are
// deleted per monitor in bounded chunks, like retention, so a large clear
// never holds the writer for long.
func (s *Store) ClearHistory(ctx context.Context, kind string, keepDays int, monitorID *int64, now time.Time) (int64, error) {
	table, ok := ClearKinds[kind]
	if !ok {
		return 0, ErrInvalid
	}
	cutoff := now.UnixMilli() + 1
	if keepDays > 0 {
		cutoff = daysAgoMs(now, keepDays)
	}

	if table == "heartbeats" {
		ids := []int64{}
		if monitorID != nil {
			ids = append(ids, *monitorID)
		} else {
			var err error
			if ids, err = s.monitorIDsForPrune(ctx); err != nil {
				return 0, err
			}
		}
		var before, after int64
		_ = s.R.QueryRowContext(ctx, `SELECT count(*) FROM heartbeats`).Scan(&before)
		for _, id := range ids {
			if err := s.pruneHeartbeatsChunked(ctx, id, cutoff); err != nil {
				return 0, err
			}
		}
		_ = s.R.QueryRowContext(ctx, `SELECT count(*) FROM heartbeats`).Scan(&after)
		return before - after, nil
	}

	tsCol := map[string]string{"status_events": "ts", "alert_log": "ts", "audit_log": "ts", "outages": "started_at"}[table]
	q := `DELETE FROM ` + table + ` WHERE ` + tsCol + ` < ?`
	args := []any{cutoff}
	if monitorID != nil && table != "audit_log" {
		q += ` AND monitor_id = ?`
		args = append(args, *monitorID)
	}
	if table == "outages" {
		q += ` AND ended_at IS NOT NULL`
	}
	res, err := s.W.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("store: clear %s: %w", table, err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Compact rebuilds the database file to return freed space to the disk
// (VACUUM). It holds the writer connection for the duration — seconds on
// a large database — so it's an explicit admin action, never automatic.
func (s *Store) Compact(ctx context.Context) error {
	if _, err := s.W.ExecContext(ctx, `VACUUM`); err != nil {
		return fmt.Errorf("store: vacuum: %w", err)
	}
	_, _ = s.W.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return nil
}
