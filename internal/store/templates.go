package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Template is a saved starting point for new monitors: the monitor form's
// settings as JSON, without a name, target, group or secrets. The JSON is
// owned by the web form; the server only keeps it clean (see the api package).
type Template struct {
	ID          int64
	Name        string
	Description string
	Type        string
	Config      json.RawMessage
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const templateColumns = `id, name, description, type, config, created_at, updated_at`

func scanTemplate(sc scanner) (*Template, error) {
	var t Template
	var cfg string
	var created, updated int64
	if err := sc.Scan(&t.ID, &t.Name, &t.Description, &t.Type, &cfg, &created, &updated); err != nil {
		return nil, err
	}
	t.Config = json.RawMessage(cfg)
	t.CreatedAt, t.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
	return &t, nil
}

// ListTemplates returns every template, by name.
func (s *Store) ListTemplates(ctx context.Context) ([]Template, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT `+templateColumns+` FROM monitor_templates ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("store: list templates: %w", err)
	}
	defer rows.Close()
	var out []Template
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan template: %w", err)
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// GetTemplate returns ErrNotFound when id doesn't exist.
func (s *Store) GetTemplate(ctx context.Context, id int64) (*Template, error) {
	t, err := scanTemplate(s.R.QueryRowContext(ctx, `SELECT `+templateColumns+` FROM monitor_templates WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get template: %w", err)
	}
	return t, nil
}

// CreateTemplate inserts t and sets its ID and timestamps. A name already in
// use (case-insensitively) is ErrConflict.
func (s *Store) CreateTemplate(ctx context.Context, t *Template, createdBy *int64) error {
	now := time.Now()
	res, err := s.W.ExecContext(ctx, `
		INSERT INTO monitor_templates(name, description, type, config, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.Name, t.Description, t.Type, string(t.Config), createdBy, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return wrapConflict(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: last insert id: %w", err)
	}
	t.ID, t.CreatedAt, t.UpdatedAt = id, now, now
	return nil
}

// UpdateTemplate replaces name, description, type and config.
func (s *Store) UpdateTemplate(ctx context.Context, t *Template) error {
	now := time.Now()
	res, err := s.W.ExecContext(ctx, `
		UPDATE monitor_templates SET name=?, description=?, type=?, config=?, updated_at=? WHERE id=?`,
		t.Name, t.Description, t.Type, string(t.Config), now.UnixMilli(), t.ID)
	if err != nil {
		return wrapConflict(err)
	}
	t.UpdatedAt = now
	return checkAffected(res)
}

// DeleteTemplate removes a template; monitors made from it are unaffected.
func (s *Store) DeleteTemplate(ctx context.Context, id int64) error {
	res, err := s.W.ExecContext(ctx, `DELETE FROM monitor_templates WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("store: delete template: %w", err)
	}
	return checkAffected(res)
}
