package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Session is a signed-in browser session. The cookie itself never
// touches the database: only the SHA-256 hash of its token does.
type Session struct {
	UserID    int64
	CSRF      string
	ExpiresAt time.Time
}

// CreateSession stores a new session keyed by tokenHash (the SHA-256 of
// the random cookie token).
func (s *Store) CreateSession(ctx context.Context, tokenHash []byte, userID int64, csrf string, expires time.Time) error {
	_, err := s.W.ExecContext(ctx, `
		INSERT INTO sessions(id_hash, user_id, csrf_token, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?)`,
		tokenHash, userID, csrf, time.Now().UnixMilli(), expires.UnixMilli(),
	)
	if err != nil {
		return wrapConflict(err)
	}
	return nil
}

// GetSession looks up a session by its token hash. It returns ErrNotFound
// when the session is missing, has expired as of now, or belongs to a
// disabled user, so callers never need to check those separately.
func (s *Store) GetSession(ctx context.Context, tokenHash []byte, now time.Time) (*Session, *User, error) {
	row := s.R.QueryRowContext(ctx, `
		SELECT s.user_id, s.csrf_token, s.expires_at,
		       u.id, u.email, u.name, u.avatar_url, u.role, u.created_at, u.last_login_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.id_hash = ?`, tokenHash)

	var sess Session
	var expiresAt int64
	var u User
	var createdAt int64
	var lastLogin sql.NullInt64
	err := row.Scan(
		&sess.UserID, &sess.CSRF, &expiresAt,
		&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.Role, &createdAt, &lastLogin,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, fmt.Errorf("store: get session: %w", err)
	}
	sess.ExpiresAt = time.UnixMilli(expiresAt)
	u.CreatedAt = time.UnixMilli(createdAt)
	if lastLogin.Valid {
		t := time.UnixMilli(lastLogin.Int64)
		u.LastLoginAt = &t
	}

	if sess.ExpiresAt.Before(now) {
		return nil, nil, ErrNotFound
	}
	if u.Role == RoleDisabled {
		return nil, nil, ErrNotFound
	}
	return &sess, &u, nil
}

// TouchSession slides a session's expiry forward.
func (s *Store) TouchSession(ctx context.Context, tokenHash []byte, expires time.Time) error {
	res, err := s.W.ExecContext(ctx, `UPDATE sessions SET expires_at=? WHERE id_hash=?`, expires.UnixMilli(), tokenHash)
	if err != nil {
		return fmt.Errorf("store: touch session: %w", err)
	}
	return checkAffected(res)
}

// DeleteSession removes one session (sign-out). Deleting an
// already-gone session is not an error.
func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	if _, err := s.W.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash=?`, tokenHash); err != nil {
		return fmt.Errorf("store: delete session: %w", err)
	}
	return nil
}

// DeleteUserSessions removes every session for a user (e.g. on disable).
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	if _, err := s.W.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID); err != nil {
		return fmt.Errorf("store: delete user sessions: %w", err)
	}
	return nil
}

func (s *Store) DeleteOtherUserSessions(ctx context.Context, userID int64, keepHash []byte) error {
	_, err := s.W.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=? AND id_hash<>?`, userID, keepHash)
	return err
}

// PruneSessions deletes every session that expired before now and
// returns how many rows were removed.
func (s *Store) PruneSessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.W.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, now.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("store: prune sessions: %w", err)
	}
	return res.RowsAffected()
}
