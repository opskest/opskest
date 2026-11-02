package store

import (
	"context"
	"fmt"
)

// Group is a wallboard card and a dashboard section.
type Group struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Sort        int    `json:"sort"`
	Description string `json:"description"`
}

// ListGroups returns every group, in display order.
func (s *Store) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT id, name, sort, description FROM groups ORDER BY sort, name`)
	if err != nil {
		return nil, fmt.Errorf("store: list groups: %w", err)
	}
	defer rows.Close()

	var out []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Sort, &g.Description); err != nil {
			return nil, fmt.Errorf("store: scan group: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// CreateGroup inserts g and sets g.ID. A name that differs from an
// existing group only by case returns ErrConflict (the column is
// COLLATE NOCASE UNIQUE).
func (s *Store) CreateGroup(ctx context.Context, g *Group) error {
	res, err := s.W.ExecContext(ctx, `INSERT INTO groups(name, sort, description) VALUES (?, ?, ?)`, g.Name, g.Sort, g.Description)
	if err != nil {
		return wrapConflict(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: last insert id: %w", err)
	}
	g.ID = id
	return nil
}

// UpdateGroup replaces g's row. It returns ErrNotFound if g.ID doesn't
// exist, or ErrConflict if the new name collides with another group.
func (s *Store) UpdateGroup(ctx context.Context, g *Group) error {
	res, err := s.W.ExecContext(ctx, `UPDATE groups SET name=?, sort=?, description=? WHERE id=?`, g.Name, g.Sort, g.Description, g.ID)
	if err != nil {
		return wrapConflict(err)
	}
	return checkAffected(res)
}

// DeleteGroup removes a group. Its monitors' group_id is set NULL by the
// schema's ON DELETE SET NULL.
func (s *Store) DeleteGroup(ctx context.Context, id int64) error {
	res, err := s.W.ExecContext(ctx, `DELETE FROM groups WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("store: delete group: %w", err)
	}
	return checkAffected(res)
}

// ReorderGroups sets each id's sort to its index in ids.
func (s *Store) ReorderGroups(ctx context.Context, ids []int64) error {
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE groups SET sort=? WHERE id=?`, i, id); err != nil {
			return fmt.Errorf("store: reorder group %d: %w", id, err)
		}
	}
	return tx.Commit()
}
