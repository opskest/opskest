package store

import (
	"context"
	"fmt"
)

// defaultTagColor matches the schema's column default; it is applied in
// Go too, since an explicit column list in INSERT/UPDATE bypasses the
// DEFAULT clause for an empty string.
const defaultTagColor = "#6b7385"

// Tag is a label applied to monitors.
type Tag struct {
	ID    int64
	Name  string
	Color string
}

// ListTags returns every tag, alphabetically.
func (s *Store) ListTags(ctx context.Context) ([]Tag, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT id, name, color FROM tags ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("store: list tags: %w", err)
	}
	defer rows.Close()

	var out []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Color); err != nil {
			return nil, fmt.Errorf("store: scan tag: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CreateTag inserts t and sets t.ID. A name that differs from an
// existing tag only by case returns ErrConflict (the column is
// COLLATE NOCASE UNIQUE).
func (s *Store) CreateTag(ctx context.Context, t *Tag) error {
	if t.Color == "" {
		t.Color = defaultTagColor
	}
	res, err := s.W.ExecContext(ctx, `INSERT INTO tags(name, color) VALUES (?, ?)`, t.Name, t.Color)
	if err != nil {
		return wrapConflict(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: last insert id: %w", err)
	}
	t.ID = id
	return nil
}

// UpdateTag replaces t's row. It returns ErrNotFound if t.ID doesn't
// exist, or ErrConflict if the new name collides with another tag.
func (s *Store) UpdateTag(ctx context.Context, t *Tag) error {
	if t.Color == "" {
		t.Color = defaultTagColor
	}
	res, err := s.W.ExecContext(ctx, `UPDATE tags SET name=?, color=? WHERE id=?`, t.Name, t.Color, t.ID)
	if err != nil {
		return wrapConflict(err)
	}
	return checkAffected(res)
}

// DeleteTag removes a tag. Its monitor_tags rows are removed by the
// schema's ON DELETE CASCADE.
func (s *Store) DeleteTag(ctx context.Context, id int64) error {
	res, err := s.W.ExecContext(ctx, `DELETE FROM tags WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("store: delete tag: %w", err)
	}
	return checkAffected(res)
}
