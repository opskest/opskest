package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Maintenance is a scheduled window during which its targets' Down,
// Degraded and Reminder alerts are suppressed. Kind "once" uses
// StartsAt/EndsAt; kind "weekly" uses Weekdays (a Mon=bit0 bitmask),
// StartMinute and DurationMin, evaluated in TZ.
type Maintenance struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Kind        string `json:"kind"` // once, weekly
	StartsAt    *int64 `json:"startsAt"`
	EndsAt      *int64 `json:"endsAt"`
	Weekdays    int    `json:"weekdays"`
	StartMinute int    `json:"startMinute"`
	DurationMin int    `json:"durationMin"`
	TZ          string `json:"tz"` // IANA time zone
	Active      bool   `json:"active"`

	MonitorIDs []int64 `json:"monitorIds"`
	GroupIDs   []int64 `json:"groupIds"`

	CreatedBy *int64 `json:"createdBy"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

const maintenanceColumns = `id, title, description, kind, starts_at, ends_at, weekdays, start_minute,
	duration_min, tz, active, created_by, created_at, updated_at`

func scanMaintenance(sc scanner) (Maintenance, error) {
	var m Maintenance
	var startsAt, endsAt, createdBy sql.NullInt64
	var active int64
	err := sc.Scan(
		&m.ID, &m.Title, &m.Description, &m.Kind, &startsAt, &endsAt, &m.Weekdays, &m.StartMinute,
		&m.DurationMin, &m.TZ, &active, &createdBy, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return m, err
	}
	m.StartsAt = scanNullInt64(startsAt)
	m.EndsAt = scanNullInt64(endsAt)
	m.CreatedBy = scanNullInt64(createdBy)
	m.Active = intToBool(active)
	return m, nil
}

// ListMaintenance returns every maintenance window with its MonitorIDs
// and GroupIDs filled in, using 2 queries total.
func (s *Store) ListMaintenance(ctx context.Context) ([]Maintenance, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT `+maintenanceColumns+` FROM maintenance ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: list maintenance: %w", err)
	}
	var out []Maintenance
	byID := map[int64]*Maintenance{}
	for rows.Next() {
		m, err := scanMaintenance(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: scan maintenance: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range out {
		byID[out[i].ID] = &out[i]
	}

	targetRows, err := s.R.QueryContext(ctx, `SELECT maintenance_id, monitor_id, group_id FROM maintenance_targets ORDER BY maintenance_id`)
	if err != nil {
		return nil, fmt.Errorf("store: list maintenance targets: %w", err)
	}
	defer targetRows.Close()
	for targetRows.Next() {
		var maintID int64
		var monitorID, groupID sql.NullInt64
		if err := targetRows.Scan(&maintID, &monitorID, &groupID); err != nil {
			return nil, fmt.Errorf("store: scan maintenance target: %w", err)
		}
		m, ok := byID[maintID]
		if !ok {
			continue
		}
		if monitorID.Valid {
			m.MonitorIDs = append(m.MonitorIDs, monitorID.Int64)
		}
		if groupID.Valid {
			m.GroupIDs = append(m.GroupIDs, groupID.Int64)
		}
	}
	if err := targetRows.Err(); err != nil {
		return nil, err
	}

	return out, nil
}

// CreateMaintenance inserts m and its monitor/group targets, setting
// m.ID, m.CreatedAt and m.UpdatedAt. An invalid Kind or TZ returns
// ErrInvalid.
func (s *Store) CreateMaintenance(ctx context.Context, m *Maintenance) error {
	now := time.Now().UnixMilli()
	m.CreatedAt = now
	m.UpdatedAt = now

	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO maintenance(
			title, description, kind, starts_at, ends_at, weekdays, start_minute,
			duration_min, tz, active, created_by, created_at, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.Title, m.Description, m.Kind, int64OrNil(m.StartsAt), int64OrNil(m.EndsAt), m.Weekdays, m.StartMinute,
		m.DurationMin, m.TZ, boolToInt(m.Active), int64OrNil(m.CreatedBy), now, now,
	)
	if err != nil {
		return wrapConflict(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: last insert id: %w", err)
	}
	m.ID = id

	if err := replaceMaintenanceTargets(ctx, tx, id, m.MonitorIDs, m.GroupIDs); err != nil {
		return err
	}

	return tx.Commit()
}

// UpdateMaintenance replaces m's row and its monitor/group targets.
// ErrNotFound if m.ID doesn't exist.
func (s *Store) UpdateMaintenance(ctx context.Context, m *Maintenance) error {
	now := time.Now().UnixMilli()

	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE maintenance SET
			title=?, description=?, kind=?, starts_at=?, ends_at=?, weekdays=?, start_minute=?,
			duration_min=?, tz=?, active=?, updated_at=?
		WHERE id=?`,
		m.Title, m.Description, m.Kind, int64OrNil(m.StartsAt), int64OrNil(m.EndsAt), m.Weekdays, m.StartMinute,
		m.DurationMin, m.TZ, boolToInt(m.Active), now, m.ID,
	)
	if err != nil {
		return wrapConflict(err)
	}
	if err := checkAffected(res); err != nil {
		return err
	}
	m.UpdatedAt = now

	if err := replaceMaintenanceTargets(ctx, tx, m.ID, m.MonitorIDs, m.GroupIDs); err != nil {
		return err
	}

	return tx.Commit()
}

func replaceMaintenanceTargets(ctx context.Context, tx *sql.Tx, maintenanceID int64, monitorIDs, groupIDs []int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM maintenance_targets WHERE maintenance_id=?`, maintenanceID); err != nil {
		return fmt.Errorf("store: clear maintenance targets: %w", err)
	}
	for _, mid := range monitorIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO maintenance_targets(maintenance_id, monitor_id, group_id) VALUES (?,?,NULL)`, maintenanceID, mid); err != nil {
			return fmt.Errorf("store: insert maintenance monitor target: %w", err)
		}
	}
	for _, gid := range groupIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO maintenance_targets(maintenance_id, monitor_id, group_id) VALUES (?,NULL,?)`, maintenanceID, gid); err != nil {
			return fmt.Errorf("store: insert maintenance group target: %w", err)
		}
	}
	return nil
}

// DeleteMaintenance removes a maintenance window. Its targets cascade
// via their FK ON DELETE CASCADE.
func (s *Store) DeleteMaintenance(ctx context.Context, id int64) error {
	res, err := s.W.ExecContext(ctx, `DELETE FROM maintenance WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("store: delete maintenance: %w", err)
	}
	return checkAffected(res)
}
