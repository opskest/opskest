package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Roles. Admin is the sole role SetUserRole and DeleteUser must never leave
// at zero; the permission model itself lives in internal/auth/roles.go.
const (
	RoleAdmin    = "admin"
	RoleUser     = "user"
	RoleViewer   = "viewer"
	RoleDisabled = "disabled"
)

// User is a signed-in person.
type User struct {
	ID          int64
	Email       string
	Name        string
	AvatarURL   string
	Role        string
	CreatedAt   time.Time
	LastLoginAt *time.Time
}

func scanUser(sc scanner) (*User, error) {
	var u User
	var createdAt int64
	var lastLogin sql.NullInt64
	if err := sc.Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.Role, &createdAt, &lastLogin); err != nil {
		return nil, err
	}
	u.CreatedAt = time.UnixMilli(createdAt)
	if lastLogin.Valid {
		t := time.UnixMilli(lastLogin.Int64)
		u.LastLoginAt = &t
	}
	return &u, nil
}

const userColumns = `id, email, name, avatar_url, role, created_at, last_login_at`

// UpdateDisplayName changes only the authenticated account's presentation name.
// Identity, credentials, permissions and sessions remain unchanged.
func (s *Store) UpdateDisplayName(ctx context.Context, id int64, name string) error {
	result, err := s.W.ExecContext(ctx, `UPDATE users SET name=? WHERE id=?`, name, id)
	if err != nil {
		return fmt.Errorf("store: update display name: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update display name rows: %w", err)
	}
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

// GetUser returns ErrNotFound when id doesn't exist.
func (s *Store) GetUser(ctx context.Context, id int64) (*User, error) {
	u, err := scanUser(s.R.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id=?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get user: %w", err)
	}
	return u, nil
}

// ListUsers returns every user, ordered by id.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: list users: %w", err)
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan user: %w", err)
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// SetUserRole changes id's role. It returns ErrConflict rather than
// leaving zero admins: an admin being changed to anything else is
// refused while it is the only admin.
func (s *Store) SetUserRole(ctx context.Context, id int64, role string) error {
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	var current string
	if err := tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id=?`, id).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("store: get role: %w", err)
	}

	if current == RoleAdmin && role != RoleAdmin {
		var admins int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role='admin'`).Scan(&admins); err != nil {
			return fmt.Errorf("store: count admins: %w", err)
		}
		if admins <= 1 {
			return ErrConflict
		}
	}

	if _, err := tx.ExecContext(ctx, `UPDATE users SET role=? WHERE id=?`, role, id); err != nil {
		return fmt.Errorf("store: set role: %w", err)
	}
	return tx.Commit()
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// CreateUser adds an account with the given role. It returns ErrConflict if the
// email already exists. The caller validates the email and sets the password.
func (s *Store) CreateUser(ctx context.Context, email, name, role string, now time.Time) (*User, error) {
	email = normalizeEmail(email)
	res, err := s.W.ExecContext(ctx,
		`INSERT INTO users (email, name, role, created_at) VALUES (?, ?, ?, ?)`,
		email, strings.TrimSpace(name), role, now.UnixMilli())
	if err != nil {
		return nil, wrapConflict(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("store: user id: %w", err)
	}
	return s.GetUser(ctx, id)
}

// DeleteUser removes an account and (by cascade) its sessions. Like
// SetUserRole it refuses (ErrConflict) to remove the last admin.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	var role string
	if err := tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id=?`, id).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("store: get role: %w", err)
	}
	if role == RoleAdmin {
		var admins int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role='admin'`).Scan(&admins); err != nil {
			return fmt.Errorf("store: count admins: %w", err)
		}
		if admins <= 1 {
			return ErrConflict
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id); err != nil {
		return fmt.Errorf("store: delete user: %w", err)
	}
	return tx.Commit()
}
