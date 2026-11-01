package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"opskest/internal/model"
)

const monitorColumns = `id, name, type, target, group_id, interval_s, retry_interval_s, retries,
	timeout_ms, degraded_ms, ssl_warn_days, upside_down, notify_degraded, reminder_min, paused,
	description, runbook, settings_json, secrets_enc, push_token, created_by, created_at, updated_at`

const pushTokenAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
const pushTokenLen = 32

// generatePushToken returns a 32-character token drawn from
// [A-Za-z0-9] using crypto/rand.
func generatePushToken() (string, error) {
	// Rejection sampling keeps every alphabet character equally likely:
	// bytes >= the largest multiple of len(alphabet) below 256 are dropped
	// instead of wrapped (256 % 62 != 0 would otherwise bias 8 characters).
	n := len(pushTokenAlphabet)
	limit := 256 - 256%n
	tok := make([]byte, 0, pushTokenLen)
	buf := make([]byte, pushTokenLen*2)
	for len(tok) < pushTokenLen {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("store: read random: %w", err)
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			tok = append(tok, pushTokenAlphabet[int(b)%n])
			if len(tok) == pushTokenLen {
				break
			}
		}
	}
	return string(tok), nil
}

// isEmptySecrets reports whether sec holds nothing worth encrypting, so
// the caller can store NULL instead of a sealed empty struct.
func isEmptySecrets(sec model.Secrets) bool {
	return sec.IsEmpty()
}

// encodeSecrets seals sec for storage in secrets_enc, or returns nil
// (SQL NULL) when sec is empty.
func (s *Store) encodeSecrets(sec model.Secrets) ([]byte, error) {
	if isEmptySecrets(sec) {
		return nil, nil
	}
	enc, err := s.Box.SealJSON(sec)
	if err != nil {
		return nil, fmt.Errorf("store: seal secrets: %w", err)
	}
	return enc, nil
}

// decodeSecrets opens a secrets_enc value, returning a zero Secrets for
// NULL (empty enc).
func (s *Store) decodeSecrets(enc []byte) (model.Secrets, error) {
	var sec model.Secrets
	if len(enc) == 0 {
		return sec, nil
	}
	if err := s.Box.OpenJSON(enc, &sec); err != nil {
		return sec, fmt.Errorf("store: decrypt secrets: %w", err)
	}
	return sec, nil
}

// scanMonitorRow decodes one row in monitorColumns order into a
// model.Monitor. TagIDs and ChannelIDs are left nil; callers fill them
// in separately (see GetMonitor and ListMonitors).
func (s *Store) scanMonitorRow(sc scanner) (*model.Monitor, error) {
	var m model.Monitor
	var typ string
	var groupID, createdBy sql.NullInt64
	var upsideDown, notifyDegraded, paused int64
	var settingsJSON string
	var secretsEnc []byte
	var pushToken sql.NullString
	var createdAt, updatedAt int64

	err := sc.Scan(
		&m.ID, &m.Name, &typ, &m.Target, &groupID,
		&m.IntervalS, &m.RetryIntervalS, &m.Retries, &m.TimeoutMs, &m.DegradedMs, &m.SSLWarnDays,
		&upsideDown, &notifyDegraded, &m.ReminderMin, &paused,
		&m.Description, &m.Runbook, &settingsJSON, &secretsEnc, &pushToken,
		&createdBy, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}

	m.Type = model.MonitorType(typ)
	m.GroupID = scanNullInt64(groupID)
	m.CreatedBy = scanNullInt64(createdBy)
	m.UpsideDown = intToBool(upsideDown)
	m.NotifyDegraded = intToBool(notifyDegraded)
	m.Paused = intToBool(paused)
	if pushToken.Valid {
		m.PushToken = pushToken.String
	}
	m.CreatedAt = time.UnixMilli(createdAt)
	m.UpdatedAt = time.UnixMilli(updatedAt)

	if err := json.Unmarshal([]byte(settingsJSON), &m.Settings); err != nil {
		return nil, fmt.Errorf("store: unmarshal settings: %w", err)
	}

	sec, err := s.decodeSecrets(secretsEnc)
	if err != nil {
		return nil, err
	}
	m.Secrets = sec

	return &m, nil
}

// ListMonitors returns every monitor with its TagIDs and ChannelIDs
// filled in, using exactly 3 queries (monitors, monitor_tags,
// monitor_channels) joined in memory, never one query per monitor.
func (s *Store) ListMonitors(ctx context.Context) ([]*model.Monitor, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT `+monitorColumns+` FROM monitors ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: list monitors: %w", err)
	}
	var monitors []*model.Monitor
	byID := map[int64]*model.Monitor{}
	for rows.Next() {
		m, err := s.scanMonitorRow(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: scan monitor: %w", err)
		}
		monitors = append(monitors, m)
		byID[m.ID] = m
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	tagRows, err := s.R.QueryContext(ctx, `SELECT monitor_id, tag_id FROM monitor_tags ORDER BY monitor_id, tag_id`)
	if err != nil {
		return nil, fmt.Errorf("store: list monitor tags: %w", err)
	}
	for tagRows.Next() {
		var mid, tid int64
		if err := tagRows.Scan(&mid, &tid); err != nil {
			tagRows.Close()
			return nil, fmt.Errorf("store: scan monitor tag: %w", err)
		}
		if m, ok := byID[mid]; ok {
			m.TagIDs = append(m.TagIDs, tid)
		}
	}
	if err := tagRows.Err(); err != nil {
		tagRows.Close()
		return nil, err
	}
	tagRows.Close()

	chRows, err := s.R.QueryContext(ctx, `SELECT monitor_id, channel_id FROM monitor_channels ORDER BY monitor_id, channel_id`)
	if err != nil {
		return nil, fmt.Errorf("store: list monitor channels: %w", err)
	}
	for chRows.Next() {
		var mid, cid int64
		if err := chRows.Scan(&mid, &cid); err != nil {
			chRows.Close()
			return nil, fmt.Errorf("store: scan monitor channel: %w", err)
		}
		if m, ok := byID[mid]; ok {
			m.ChannelIDs = append(m.ChannelIDs, cid)
		}
	}
	if err := chRows.Err(); err != nil {
		chRows.Close()
		return nil, err
	}
	chRows.Close()

	return monitors, nil
}

// GetMonitor returns ErrNotFound when id doesn't exist.
func (s *Store) GetMonitor(ctx context.Context, id int64) (*model.Monitor, error) {
	m, err := s.scanMonitorRow(s.R.QueryRowContext(ctx, `SELECT `+monitorColumns+` FROM monitors WHERE id=?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get monitor: %w", err)
	}

	tagIDs, err := s.int64ColumnWhere(ctx, `SELECT tag_id FROM monitor_tags WHERE monitor_id=? ORDER BY tag_id`, id)
	if err != nil {
		return nil, fmt.Errorf("store: get monitor tags: %w", err)
	}
	m.TagIDs = tagIDs

	chanIDs, err := s.int64ColumnWhere(ctx, `SELECT channel_id FROM monitor_channels WHERE monitor_id=? ORDER BY channel_id`, id)
	if err != nil {
		return nil, fmt.Errorf("store: get monitor channels: %w", err)
	}
	m.ChannelIDs = chanIDs

	return m, nil
}

func (s *Store) int64ColumnWhere(ctx context.Context, query string, arg int64) ([]int64, error) {
	rows, err := s.R.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CreateMonitor inserts m, setting m.ID, m.CreatedAt and m.UpdatedAt. A
// push monitor with no PushToken gets one generated.
func (s *Store) CreateMonitor(ctx context.Context, m *model.Monitor) error {
	now := time.Now()
	m.CreatedAt = now
	m.UpdatedAt = now

	if m.Type == model.TypePush && m.PushToken == "" {
		token, err := generatePushToken()
		if err != nil {
			return err
		}
		m.PushToken = token
	}

	settingsJSON, err := json.Marshal(m.Settings)
	if err != nil {
		return fmt.Errorf("store: marshal settings: %w", err)
	}
	secretsEnc, err := s.encodeSecrets(m.Secrets)
	if err != nil {
		return err
	}

	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO monitors (
			name, type, target, group_id, interval_s, retry_interval_s, retries,
			timeout_ms, degraded_ms, ssl_warn_days, upside_down, notify_degraded,
			reminder_min, paused, description, runbook, settings_json, secrets_enc,
			push_token, created_by, created_at, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.Name, string(m.Type), m.Target, int64OrNil(m.GroupID), m.IntervalS, m.RetryIntervalS, m.Retries,
		m.TimeoutMs, m.DegradedMs, m.SSLWarnDays, boolToInt(m.UpsideDown), boolToInt(m.NotifyDegraded),
		m.ReminderMin, boolToInt(m.Paused), m.Description, m.Runbook, string(settingsJSON), secretsEnc,
		nullString(m.PushToken), int64OrNil(m.CreatedBy), m.CreatedAt.UnixMilli(), m.UpdatedAt.UnixMilli(),
	)
	if err != nil {
		return wrapConflict(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: last insert id: %w", err)
	}
	m.ID = id

	if err := replaceMonitorTags(ctx, tx, id, m.TagIDs); err != nil {
		return err
	}
	if err := replaceMonitorChannels(ctx, tx, id, m.ChannelIDs); err != nil {
		return err
	}

	return tx.Commit()
}

// UpdateMonitor replaces m's row and its tag/channel links. It never
// touches push_token, created_by or created_at: PushToken is generated
// once at creation and kept, whatever m.PushToken holds. On success,
// m.PushToken is refreshed from the stored value so callers see it.
func (s *Store) UpdateMonitor(ctx context.Context, m *model.Monitor) error {
	m.UpdatedAt = time.Now()

	settingsJSON, err := json.Marshal(m.Settings)
	if err != nil {
		return fmt.Errorf("store: marshal settings: %w", err)
	}
	secretsEnc, err := s.encodeSecrets(m.Secrets)
	if err != nil {
		return err
	}

	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE monitors SET
			name=?, type=?, target=?, group_id=?, interval_s=?, retry_interval_s=?, retries=?,
			timeout_ms=?, degraded_ms=?, ssl_warn_days=?, upside_down=?, notify_degraded=?,
			reminder_min=?, paused=?, description=?, runbook=?, settings_json=?, secrets_enc=?,
			updated_at=?
		WHERE id=?`,
		m.Name, string(m.Type), m.Target, int64OrNil(m.GroupID), m.IntervalS, m.RetryIntervalS, m.Retries,
		m.TimeoutMs, m.DegradedMs, m.SSLWarnDays, boolToInt(m.UpsideDown), boolToInt(m.NotifyDegraded),
		m.ReminderMin, boolToInt(m.Paused), m.Description, m.Runbook, string(settingsJSON), secretsEnc,
		m.UpdatedAt.UnixMilli(), m.ID,
	)
	if err != nil {
		return wrapConflict(err)
	}
	if err := checkAffected(res); err != nil {
		return err
	}

	if err := replaceMonitorTags(ctx, tx, m.ID, m.TagIDs); err != nil {
		return err
	}
	if err := replaceMonitorChannels(ctx, tx, m.ID, m.ChannelIDs); err != nil {
		return err
	}

	var pushToken sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT push_token FROM monitors WHERE id=?`, m.ID).Scan(&pushToken); err != nil {
		return fmt.Errorf("store: reload push token: %w", err)
	}
	m.PushToken = ""
	if pushToken.Valid {
		m.PushToken = pushToken.String
	}

	return tx.Commit()
}

func replaceMonitorTags(ctx context.Context, tx *sql.Tx, monitorID int64, tagIDs []int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM monitor_tags WHERE monitor_id=?`, monitorID); err != nil {
		return fmt.Errorf("store: clear monitor tags: %w", err)
	}
	for _, tid := range tagIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO monitor_tags(monitor_id, tag_id) VALUES (?, ?)`, monitorID, tid); err != nil {
			return fmt.Errorf("store: insert monitor tag: %w", err)
		}
	}
	return nil
}

func replaceMonitorChannels(ctx context.Context, tx *sql.Tx, monitorID int64, channelIDs []int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM monitor_channels WHERE monitor_id=?`, monitorID); err != nil {
		return fmt.Errorf("store: clear monitor channels: %w", err)
	}
	for _, cid := range channelIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO monitor_channels(monitor_id, channel_id) VALUES (?, ?)`, monitorID, cid); err != nil {
			return fmt.Errorf("store: insert monitor channel: %w", err)
		}
	}
	return nil
}

// DeleteMonitor deletes the monitor and every row that references it:
// heartbeats, rollups, outages and status_events have no FK (they are the
// hot write path) and so are purged explicitly; monitor_tags, monitor_channels and
// monitor_runtime cascade via their FK ON DELETE CASCADE. All of it runs
// in one transaction.
func (s *Store) DeleteMonitor(ctx context.Context, id int64) error {
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `DELETE FROM monitors WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("store: delete monitor: %w", err)
	}
	if err := checkAffected(res); err != nil {
		return err
	}

	for _, stmt := range []string{
		`DELETE FROM heartbeats WHERE monitor_id=?`,
		`DELETE FROM rollup_hourly WHERE monitor_id=?`,
		`DELETE FROM rollup_daily WHERE monitor_id=?`,
		`DELETE FROM outages WHERE monitor_id=?`,
		`DELETE FROM status_events WHERE monitor_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, id); err != nil {
			return fmt.Errorf("store: purge related rows: %w", err)
		}
	}

	return tx.Commit()
}

// SetPaused pauses or resumes a monitor.
func (s *Store) SetPaused(ctx context.Context, id int64, paused bool) error {
	res, err := s.W.ExecContext(ctx, `UPDATE monitors SET paused=? WHERE id=?`, boolToInt(paused), id)
	if err != nil {
		return fmt.Errorf("store: set paused: %w", err)
	}
	return checkAffected(res)
}

// MonitorByPushToken resolves a push token to its monitor id, or
// ErrNotFound for an unknown token.
func (s *Store) MonitorByPushToken(ctx context.Context, token string) (int64, error) {
	var id int64
	err := s.R.QueryRowContext(ctx, `SELECT id FROM monitors WHERE push_token=?`, token).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("store: monitor by push token: %w", err)
	}
	return id, nil
}
