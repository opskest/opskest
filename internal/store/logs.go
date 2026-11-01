package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"opskest/internal/logx"
)

// AlertLogEntry is one delivery attempt to an alert channel (§8.4).
type AlertLogEntry struct {
	ID        int64
	TS        int64
	ChannelID *int64
	MonitorID *int64
	Event     string
	OK        bool
	Attempt   int
	Error     string
}

// OpAlertLog inserts e, for use with Writer.Do: alert delivery runs off
// the writer's hot path, but every attempt still funnels through the
// single writer connection like any other write.
func OpAlertLog(e AlertLogEntry) Op {
	return func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO alert_log(ts, channel_id, monitor_id, event, ok, attempt, error)
			VALUES (?,?,?,?,?,?,?)`,
			e.TS, int64OrNil(e.ChannelID), int64OrNil(e.MonitorID), e.Event, boolToInt(e.OK), e.Attempt, e.Error,
		)
		return err
	}
}

// ListAlertLog returns alert_log entries newest-first, at most 200
// (default 50 when limit<=0). before (unix ms) is an exclusive cursor.
func (s *Store) ListAlertLog(ctx context.Context, limit int, before int64) ([]AlertLogEntry, error) {
	return s.ListAlertLogFiltered(ctx, limit, before, false)
}

// GetAlertLog returns one entry. ErrNotFound if id doesn't exist.
func (s *Store) GetAlertLog(ctx context.Context, id int64) (AlertLogEntry, error) {
	var e AlertLogEntry
	var channelID, monitorID sql.NullInt64
	var ok int64
	err := s.R.QueryRowContext(ctx, `SELECT id, ts, channel_id, monitor_id, event, ok, attempt, error FROM alert_log WHERE id=?`, id).
		Scan(&e.ID, &e.TS, &channelID, &monitorID, &e.Event, &ok, &e.Attempt, &e.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	e.ChannelID, e.MonitorID, e.OK = scanNullInt64(channelID), scanNullInt64(monitorID), intToBool(ok)
	return e, err
}

// ListAlertLogFiltered is ListAlertLog, optionally only failed attempts.
func (s *Store) ListAlertLogFiltered(ctx context.Context, limit int, before int64, failedOnly bool) ([]AlertLogEntry, error) {
	limit = clampLimit(limit)

	query := `SELECT id, ts, channel_id, monitor_id, event, ok, attempt, error FROM alert_log`
	var where []string
	var args []any
	if before > 0 {
		where = append(where, `ts<?`)
		args = append(args, before)
	}
	if failedOnly {
		where = append(where, `ok=0`)
	}
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, " AND ")
	}
	query += ` ORDER BY ts DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.R.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list alert log: %w", err)
	}
	defer rows.Close()

	var out []AlertLogEntry
	for rows.Next() {
		var e AlertLogEntry
		var channelID, monitorID sql.NullInt64
		var ok int64
		if err := rows.Scan(&e.ID, &e.TS, &channelID, &monitorID, &e.Event, &ok, &e.Attempt, &e.Error); err != nil {
			return nil, fmt.Errorf("store: scan alert log: %w", err)
		}
		e.ChannelID = scanNullInt64(channelID)
		e.MonitorID = scanNullInt64(monitorID)
		e.OK = intToBool(ok)
		out = append(out, e)
	}
	return out, rows.Err()
}

// AuditEntry is one recorded admin action (§15).
type AuditEntry struct {
	ID        int64
	TS        int64
	UserID    *int64
	UserEmail string // joined from users; empty when the user was deleted
	Action    string
	Entity    string
	EntityID  *int64
	Detail    string
}

// Audit records an admin action. It writes directly via the writer
// connection (not queued through Writer.Do), since it is a rare,
// user-triggered action, not the hot path. It is best-effort: on
// failure it logs via logx.Err and returns nil, so an audit-trail
// hiccup never fails the caller's real write.
func (s *Store) Audit(ctx context.Context, userID *int64, action, entity string, entityID *int64, detail string) error {
	now := time.Now().UnixMilli()
	_, err := s.W.ExecContext(ctx, `
		INSERT INTO audit_log(ts, user_id, action, entity, entity_id, detail)
		VALUES (?,?,?,?,?,?)`,
		now, int64OrNil(userID), action, entity, int64OrNil(entityID), detail,
	)
	if err != nil {
		logx.Err(s.log(), "store: write audit log", err)
	}
	return nil
}

// ListAudit returns audit_log entries newest-first, at most 200 (default
// 50 when limit<=0), with UserEmail joined from users. before (unix ms)
// is an exclusive cursor.
func (s *Store) ListAudit(ctx context.Context, limit int, before int64) ([]AuditEntry, error) {
	limit = clampLimit(limit)

	query := `
		SELECT a.id, a.ts, a.user_id, COALESCE(u.email, ''), a.action, a.entity, a.entity_id, a.detail
		FROM audit_log a
		LEFT JOIN users u ON u.id = a.user_id`
	var args []any
	if before > 0 {
		query += ` WHERE a.ts<?`
		args = append(args, before)
	}
	query += ` ORDER BY a.ts DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.R.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list audit log: %w", err)
	}
	defer rows.Close()

	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var userID, entityID sql.NullInt64
		if err := rows.Scan(&e.ID, &e.TS, &userID, &e.UserEmail, &e.Action, &e.Entity, &entityID, &e.Detail); err != nil {
			return nil, fmt.Errorf("store: scan audit log: %w", err)
		}
		e.UserID = scanNullInt64(userID)
		e.EntityID = scanNullInt64(entityID)
		out = append(out, e)
	}
	return out, rows.Err()
}
