package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type Credentials struct {
	UserID                       int64
	Username, PasswordHash       string
	TOTPSecret, TOTPPending      []byte
	PendingExpires, LastTOTPStep int64
	RecoveryHashes               []string
}

func (s *Store) HasLocalAccounts(ctx context.Context) (bool, error) {
	var n int
	err := s.R.QueryRowContext(ctx, `SELECT count(*) FROM local_credentials`).Scan(&n)
	return n > 0, err
}

// CreateLocalUser serializes the first-admin check with creation on the single
// writer connection. Two concurrent bootstrap requests cannot both succeed.
func (s *Store) CreateLocalUser(ctx context.Context, username, name, passwordHash, role string, first bool, now time.Time) (*User, error) {
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if first {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM local_credentials`).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, ErrConflict
		}
		role = RoleAdmin
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO users(email,name,role,created_at) VALUES(?,?,?,?)`, username+"@local.invalid", name, role, now.UnixMilli())
	if err != nil {
		return nil, wrapConflict(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO local_credentials(user_id,username,password_hash) VALUES(?,?,?)`, id, username, passwordHash); err != nil {
		return nil, wrapConflict(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetUser(ctx, id)
}

func scanCredentials(row scanner) (*Credentials, error) {
	var c Credentials
	var codes string
	err := row.Scan(&c.UserID, &c.Username, &c.PasswordHash, &c.TOTPSecret, &c.TOTPPending, &c.PendingExpires, &c.LastTOTPStep, &codes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(codes), &c.RecoveryHashes); err != nil {
		return nil, err
	}
	return &c, nil
}

const credentialColumns = `user_id,username,password_hash,totp_secret,totp_pending,pending_expires,last_totp_step,recovery_hashes`

func (s *Store) CredentialsByUsername(ctx context.Context, username string) (*Credentials, error) {
	return scanCredentials(s.R.QueryRowContext(ctx, `SELECT `+credentialColumns+` FROM local_credentials WHERE username=?`, username))
}
func (s *Store) Credentials(ctx context.Context, id int64) (*Credentials, error) {
	return scanCredentials(s.R.QueryRowContext(ctx, `SELECT `+credentialColumns+` FROM local_credentials WHERE user_id=?`, id))
}
func (s *Store) SetTOTPPending(ctx context.Context, id int64, secret []byte, expires int64) error {
	res, err := s.W.ExecContext(ctx, `UPDATE local_credentials SET totp_pending=?,pending_expires=? WHERE user_id=? AND totp_secret IS NULL`, secret, expires, id)
	if err != nil {
		return err
	}
	return checkAffected(res)
}
func (s *Store) EnableTOTP(ctx context.Context, id int64, pending []byte, step int64, codes []string, now int64) error {
	encoded, _ := json.Marshal(codes)
	res, err := s.W.ExecContext(ctx, `UPDATE local_credentials SET totp_secret=totp_pending,totp_pending=NULL,pending_expires=0,last_totp_step=?,recovery_hashes=? WHERE user_id=? AND totp_pending=? AND pending_expires>? AND totp_secret IS NULL`, step, string(encoded), id, pending, now)
	if err != nil {
		return err
	}
	return checkAffected(res)
}

// ConsumeTOTP is a compare-and-update: replay is rejected even across concurrent requests.
func (s *Store) ConsumeTOTP(ctx context.Context, id, step int64, secret []byte) (bool, error) {
	res, err := s.W.ExecContext(ctx, `UPDATE local_credentials SET last_totp_step=? WHERE user_id=? AND last_totp_step<? AND totp_secret=?`, step, id, step, secret)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
func (s *Store) ConsumeRecovery(ctx context.Context, id int64, hash string) (bool, error) {
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	c, err := scanCredentials(tx.QueryRowContext(ctx, `SELECT `+credentialColumns+` FROM local_credentials WHERE user_id=?`, id))
	if err != nil {
		return false, err
	}
	found := -1
	for i, h := range c.RecoveryHashes {
		if h == hash {
			found = i
			break
		}
	}
	if found < 0 || len(c.TOTPSecret) == 0 {
		return false, nil
	}
	codes := append(c.RecoveryHashes[:found], c.RecoveryHashes[found+1:]...)
	b, _ := json.Marshal(codes)
	if _, err = tx.ExecContext(ctx, `UPDATE local_credentials SET recovery_hashes=? WHERE user_id=?`, string(b), id); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
func (s *Store) ReplaceRecovery(ctx context.Context, id int64, codes []string) error {
	b, _ := json.Marshal(codes)
	_, err := s.W.ExecContext(ctx, `UPDATE local_credentials SET recovery_hashes=? WHERE user_id=? AND totp_secret IS NOT NULL`, string(b), id)
	return err
}
func (s *Store) DisableTOTP(ctx context.Context, id int64) error {
	_, err := s.W.ExecContext(ctx, `UPDATE local_credentials SET totp_secret=NULL,totp_pending=NULL,pending_expires=0,last_totp_step=-1,recovery_hashes='[]' WHERE user_id=?`, id)
	return err
}
func (s *Store) ChangePassword(ctx context.Context, id int64, oldHash, newHash string) error {
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE local_credentials SET password_hash=? WHERE user_id=? AND password_hash=?`, newHash, id, oldHash)
	if err != nil {
		return err
	}
	if err = checkAffected(res); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) RecordLocalLogin(ctx context.Context, id int64, now time.Time) error {
	_, err := s.W.ExecContext(ctx, `UPDATE users SET last_login_at=? WHERE id=?`, now.UnixMilli(), id)
	return err
}

// TwoFactorUsers returns the ids of the users who have an authenticator app
// set up.
func (s *Store) TwoFactorUsers(ctx context.Context) (map[int64]bool, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT user_id FROM local_credentials WHERE totp_secret IS NOT NULL AND length(totp_secret) > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// HasTwoFactor reports whether the user has an authenticator app set up.
func (s *Store) HasTwoFactor(ctx context.Context, id int64) (bool, error) {
	var n int
	err := s.R.QueryRowContext(ctx, `SELECT count(*) FROM local_credentials WHERE user_id=? AND totp_secret IS NOT NULL AND length(totp_secret) > 0`, id).Scan(&n)
	return n > 0, err
}
