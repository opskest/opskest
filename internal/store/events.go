package store

import (
	"context"
	"database/sql"
	"fmt"

	"opskest/internal/model"
)

// StatusEvent is one change of a monitor's displayed status (the
// Kuma-style "Important events" feed): UP → PENDING, DOWN → UP, into and
// out of MAINTENANCE, PAUSED and back. The engine records one per change
// through Writer.DoFor; boot-time state is never recorded.
type StatusEvent struct {
	ID, MonitorID, TS int64
	From, To          model.Status
	Reason, ErrClass  string
}

// OpInsertStatusEvent inserts e (its ID is assigned by SQLite), for use
// with Writer.DoFor(e.MonitorID, ...).
func OpInsertStatusEvent(e StatusEvent) Op {
	return func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO status_events(monitor_id, ts, from_status, to_status, reason, err_class)
			VALUES (?,?,?,?,?,?)`,
			e.MonitorID, e.TS, int(e.From), int(e.To), e.Reason, e.ErrClass)
		return err
	}
}

// ListStatusEvents returns status events newest first, optionally for one
// monitor. before is an exclusive ts cursor (0 = from the newest); limit is
// clamped to 1..200 (default 50).
func (s *Store) ListStatusEvents(ctx context.Context, monitorID *int64, before int64, limit int) ([]StatusEvent, error) {
	limit = clampLimit(limit)
	query := `SELECT id, monitor_id, ts, from_status, to_status, reason, err_class FROM status_events`
	var where []string
	var args []any
	if monitorID != nil {
		where = append(where, `monitor_id=?`)
		args = append(args, *monitorID)
	} else {
		// The global feed leaves out 3rd-party feeds (alert-only); a feed's own
		// timeline is still served per monitor.
		where = append(where, `monitor_id NOT IN (SELECT id FROM monitors WHERE CASE WHEN json_valid(settings_json) THEN json_extract(settings_json,'$.vendor') END = 1)`)
	}
	if before > 0 {
		where = append(where, `ts<?`)
		args = append(args, before)
	}
	for i, w := range where {
		if i == 0 {
			query += " WHERE "
		} else {
			query += " AND "
		}
		query += w
	}
	query += ` ORDER BY ts DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.R.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list status events: %w", err)
	}
	defer rows.Close()

	out := make([]StatusEvent, 0, limit)
	for rows.Next() {
		var e StatusEvent
		var from, to int64
		if err := rows.Scan(&e.ID, &e.MonitorID, &e.TS, &from, &to, &e.Reason, &e.ErrClass); err != nil {
			return nil, fmt.Errorf("store: scan status event: %w", err)
		}
		e.From, e.To = model.Status(from), model.Status(to)
		out = append(out, e)
	}
	return out, rows.Err()
}

// PruneStatusEvents deletes status events with ts < olderThan (unix ms).
func (s *Store) PruneStatusEvents(ctx context.Context, olderThan int64) error {
	if _, err := s.W.ExecContext(ctx, `DELETE FROM status_events WHERE ts<?`, olderThan); err != nil {
		return fmt.Errorf("store: prune status events: %w", err)
	}
	return nil
}
