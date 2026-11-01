package store

import (
	"context"
	"fmt"
	"time"
)

const (
	rollupHourlyRetentionDays = 90
	rollupDailyRetentionDays  = 730
	alertLogRetentionDays     = 30
	auditLogRetentionDays     = 365
)

// heartbeatDeleteChunk bounds each single DELETE against heartbeats to at
// most this many rows. heartbeats is WITHOUT ROWID (its PK is
// (monitor_id, ts)), so there is no rowid to drive a
// "WHERE rowid IN (SELECT rowid ... LIMIT n)" chunk; instead each chunk
// selects its own victim ts values via a LIMIT subquery on the PK index.
// Without chunking, lowering rawDays for a monitor with a large backlog
// could turn into a single DELETE scanning/removing millions of rows,
// holding the single writer connection - and so blocking the hot-path
// Writer - for seconds at a time.
const heartbeatDeleteChunk = 5000

func daysAgoMs(now time.Time, days int) int64 {
	return now.UnixMilli() - int64(days)*24*3600*1000
}

// Prune deletes everything past its retention window:
// heartbeats older than rawDays, rollup_hourly older than 90 days and
// rollup_daily older than 730 days (each per monitor, as bounded
// primary-key range deletes, never a full-table scan or an unbounded
// single statement), alert_log older than 30 days, audit_log older than
// 365 days, and expired sessions. It finishes with
// PRAGMA incremental_vacuum(2000), since the database is
// auto_vacuum=INCREMENTAL.
func (s *Store) Prune(ctx context.Context, now time.Time, rawDays int) error {
	rawCutoff := daysAgoMs(now, rawDays)
	hourlyCutoff := daysAgoMs(now, rollupHourlyRetentionDays)
	dailyCutoff := daysAgoMs(now, rollupDailyRetentionDays)

	ids, err := s.monitorIDsForPrune(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.pruneHeartbeatsChunked(ctx, id, rawCutoff); err != nil {
			return fmt.Errorf("store: prune heartbeats for monitor %d: %w", id, err)
		}
		if _, err := s.W.ExecContext(ctx, `DELETE FROM rollup_hourly WHERE monitor_id=? AND bucket<?`, id, hourlyCutoff); err != nil {
			return fmt.Errorf("store: prune rollup_hourly for monitor %d: %w", id, err)
		}
		if _, err := s.W.ExecContext(ctx, `DELETE FROM rollup_daily WHERE monitor_id=? AND bucket<?`, id, dailyCutoff); err != nil {
			return fmt.Errorf("store: prune rollup_daily for monitor %d: %w", id, err)
		}
	}

	if _, err := s.W.ExecContext(ctx, `DELETE FROM alert_log WHERE ts<?`, daysAgoMs(now, alertLogRetentionDays)); err != nil {
		return fmt.Errorf("store: prune alert_log: %w", err)
	}
	if _, err := s.W.ExecContext(ctx, `DELETE FROM audit_log WHERE ts<?`, daysAgoMs(now, auditLogRetentionDays)); err != nil {
		return fmt.Errorf("store: prune audit_log: %w", err)
	}
	if _, err := s.W.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at<?`, now.UnixMilli()); err != nil {
		return fmt.Errorf("store: prune sessions: %w", err)
	}

	if _, err := s.W.ExecContext(ctx, `PRAGMA incremental_vacuum(2000)`); err != nil {
		return fmt.Errorf("store: incremental vacuum: %w", err)
	}
	return nil
}

// pruneHeartbeatsChunked deletes id's heartbeats older than cutoff in
// batches of at most heartbeatDeleteChunk rows, so a single Prune pass
// never holds the writer connection for the whole backlog at once - each
// chunk is its own auto-committed statement, letting the Writer's own
// transactions interleave between chunks.
func (s *Store) pruneHeartbeatsChunked(ctx context.Context, monitorID, cutoff int64) error {
	for {
		res, err := s.W.ExecContext(ctx, `
			DELETE FROM heartbeats WHERE monitor_id=? AND ts IN (
				SELECT ts FROM heartbeats WHERE monitor_id=? AND ts<? ORDER BY ts LIMIT ?
			)`, monitorID, monitorID, cutoff, heartbeatDeleteChunk)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n < heartbeatDeleteChunk {
			return nil
		}
	}
}

func (s *Store) monitorIDsForPrune(ctx context.Context) ([]int64, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT id FROM monitors`)
	if err != nil {
		return nil, fmt.Errorf("store: list monitor ids: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: scan monitor id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
