package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Subscriber is one status page subscription. Email is set for kind
// "email"; SlackURL (decrypted from slack_enc, never sent to a browser)
// for kind "slack". Token is the secret in confirm/unsubscribe links.
type Subscriber struct {
	ID          int64  `json:"id"`
	PageID      int64  `json:"pageId"`
	Kind        string `json:"kind"`
	Email       string `json:"email,omitempty"`
	SlackURL    string `json:"-"`
	Token       string `json:"-"`
	ConfirmedAt *int64 `json:"confirmedAt"`
	CreatedAt   int64  `json:"createdAt"`
}

// PendingSubscriberTTL is how long an unconfirmed email subscription lives.
const PendingSubscriberTTL = 48 * time.Hour

// NewToken returns a 32-byte random hex token.
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return hex.EncodeToString(b)
}

// ResendCooldown is how soon the same pending address can be sent a new
// confirmation: re-submitting it inside the window changes nothing.
const ResendCooldown = 10 * time.Minute

// AddEmailSubscriber records a pending email subscription. send reports
// whether a confirmation should go out (with sub.Token): true for a new
// address and for a pending one past ResendCooldown (it gets a fresh
// token, so the latest email is the one that works); false for a
// confirmed address or a pending one inside the cooldown. Every path
// writes and commits, so response timing doesn't reveal which it was.
func (s *Store) AddEmailSubscriber(ctx context.Context, pageID int64, email string, now time.Time) (sub Subscriber, send bool, err error) {
	email = strings.ToLower(strings.TrimSpace(email))
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return sub, false, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	var id, created int64
	var confirmed sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT id, confirmed_at, created_at FROM page_subscribers WHERE page_id=? AND kind='email' AND email=?`, pageID, email).Scan(&id, &confirmed, &created)
	switch {
	case err == nil && (confirmed.Valid || now.Sub(time.UnixMilli(created)) < ResendCooldown):
		// Nothing to send; a same-cost write keeps timing uniform.
		if _, err := tx.ExecContext(ctx, `UPDATE page_subscribers SET id=id WHERE id=?`, id); err != nil {
			return sub, false, fmt.Errorf("store: touch subscriber: %w", err)
		}
		return Subscriber{ID: id, PageID: pageID, Kind: "email", Email: email}, false, tx.Commit()
	case err == nil:
		sub = Subscriber{ID: id, PageID: pageID, Kind: "email", Email: email, Token: NewToken(), CreatedAt: now.UnixMilli()}
		if _, err := tx.ExecContext(ctx, `UPDATE page_subscribers SET token=?, created_at=? WHERE id=?`, sub.Token, sub.CreatedAt, id); err != nil {
			return sub, false, fmt.Errorf("store: refresh subscriber: %w", err)
		}
	case errors.Is(err, sql.ErrNoRows):
		sub = Subscriber{PageID: pageID, Kind: "email", Email: email, Token: NewToken(), CreatedAt: now.UnixMilli()}
		res, err := tx.ExecContext(ctx, `INSERT INTO page_subscribers(page_id, kind, email, token, created_at) VALUES (?, 'email', ?, ?, ?)`,
			pageID, email, sub.Token, sub.CreatedAt)
		if err != nil {
			return sub, false, fmt.Errorf("store: add subscriber: %w", err)
		}
		sub.ID, _ = res.LastInsertId()
	default:
		return sub, false, fmt.Errorf("store: find subscriber: %w", err)
	}
	return sub, true, tx.Commit()
}

// AddSlackSubscriber records a confirmed Slack webhook subscription (the
// caller has already proved the webhook works by posting to it). The same
// webhook on the same page is stored once; a repeat is a no-op.
func (s *Store) AddSlackSubscriber(ctx context.Context, pageID int64, url string, now time.Time) error {
	enc, err := s.Box.Seal([]byte(url))
	if err != nil {
		return fmt.Errorf("store: seal webhook: %w", err)
	}
	sum := sha256.Sum256([]byte(url))
	ms := now.UnixMilli()
	_, err = s.W.ExecContext(ctx, `INSERT OR IGNORE INTO page_subscribers(page_id, kind, slack_enc, slack_hash, token, confirmed_at, created_at) VALUES (?, 'slack', ?, ?, ?, ?, ?)`,
		pageID, enc, hex.EncodeToString(sum[:]), NewToken(), ms, ms)
	if err != nil {
		return fmt.Errorf("store: add slack subscriber: %w", err)
	}
	return nil
}

// ConfirmSubscriber confirms the pending subscription with token (one
// still within PendingSubscriberTTL). ErrNotFound for anything else.
func (s *Store) ConfirmSubscriber(ctx context.Context, token string, now time.Time) error {
	res, err := s.W.ExecContext(ctx, `UPDATE page_subscribers SET confirmed_at=? WHERE token=? AND (confirmed_at IS NOT NULL OR created_at>=?)`,
		now.UnixMilli(), token, now.Add(-PendingSubscriberTTL).UnixMilli())
	if err != nil {
		return fmt.Errorf("store: confirm subscriber: %w", err)
	}
	return checkAffected(res)
}

// Unsubscribe deletes the subscription with token. ErrNotFound if none.
func (s *Store) Unsubscribe(ctx context.Context, token string) error {
	res, err := s.W.ExecContext(ctx, `DELETE FROM page_subscribers WHERE token=?`, token)
	if err != nil {
		return fmt.Errorf("store: unsubscribe: %w", err)
	}
	return checkAffected(res)
}

// ConfirmedSubscribers returns every confirmed subscriber of pageID, with
// Slack webhooks decrypted (for sending only). A row that fails to
// decrypt is skipped.
func (s *Store) ConfirmedSubscribers(ctx context.Context, pageID int64) ([]Subscriber, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT id, kind, email, slack_enc, token, confirmed_at, created_at FROM page_subscribers
		WHERE page_id=? AND confirmed_at IS NOT NULL ORDER BY id`, pageID)
	if err != nil {
		return nil, fmt.Errorf("store: subscribers: %w", err)
	}
	defer rows.Close()
	var out []Subscriber
	for rows.Next() {
		sub := Subscriber{PageID: pageID}
		var email sql.NullString
		var enc []byte
		var confirmed sql.NullInt64
		if err := rows.Scan(&sub.ID, &sub.Kind, &email, &enc, &sub.Token, &confirmed, &sub.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan subscriber: %w", err)
		}
		sub.Email = email.String
		sub.ConfirmedAt = scanNullInt64(confirmed)
		if sub.Kind == "slack" {
			plain, err := s.Box.Open(enc)
			if err != nil {
				continue
			}
			sub.SlackURL = string(plain)
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// SubscriberCounts returns pageID's confirmed and pending counts.
func (s *Store) SubscriberCounts(ctx context.Context, pageID int64) (confirmed, pending int, err error) {
	err = s.R.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN confirmed_at IS NOT NULL THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN confirmed_at IS NULL THEN 1 ELSE 0 END), 0)
		FROM page_subscribers WHERE page_id=?`, pageID).Scan(&confirmed, &pending)
	return confirmed, pending, err
}

// ListSubscribers pages through pageID's subscribers, newest first, for
// the admin view (no tokens or webhook URLs).
func (s *Store) ListSubscribers(ctx context.Context, pageID int64, limit, offset int) ([]Subscriber, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT id, kind, email, confirmed_at, created_at FROM page_subscribers
		WHERE page_id=? ORDER BY id DESC LIMIT ? OFFSET ?`, pageID, clampLimit(limit), max(0, offset))
	if err != nil {
		return nil, fmt.Errorf("store: list subscribers: %w", err)
	}
	defer rows.Close()
	out := []Subscriber{}
	for rows.Next() {
		sub := Subscriber{PageID: pageID}
		var email sql.NullString
		var confirmed sql.NullInt64
		if err := rows.Scan(&sub.ID, &sub.Kind, &email, &confirmed, &sub.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan subscriber: %w", err)
		}
		sub.Email = email.String
		sub.ConfirmedAt = scanNullInt64(confirmed)
		out = append(out, sub)
	}
	return out, rows.Err()
}

// DeleteSubscriber removes subscriber id from pageID. ErrNotFound if none.
func (s *Store) DeleteSubscriber(ctx context.Context, pageID, id int64) error {
	res, err := s.W.ExecContext(ctx, `DELETE FROM page_subscribers WHERE page_id=? AND id=?`, pageID, id)
	if err != nil {
		return fmt.Errorf("store: delete subscriber: %w", err)
	}
	return checkAffected(res)
}

// PrunePendingSubscribers deletes email subscriptions never confirmed
// within PendingSubscriberTTL.
func (s *Store) PrunePendingSubscribers(ctx context.Context, now time.Time) error {
	_, err := s.W.ExecContext(ctx, `DELETE FROM page_subscribers WHERE confirmed_at IS NULL AND created_at<?`,
		now.Add(-PendingSubscriberTTL).UnixMilli())
	if err != nil {
		return fmt.Errorf("store: prune pending subscribers: %w", err)
	}
	return nil
}
