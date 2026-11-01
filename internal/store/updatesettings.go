package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// UpdateSettings are the self-update options (Settings -> Updates). They
// live in the key/value settings table; no migration is needed.
type UpdateSettings struct {
	Enabled bool
	// FeedURL is the https location of the release manifest. Empty means
	// no source is configured.
	FeedURL string
	// Mode is "off", "notify" or "auto".
	Mode string
	// AutoHour (0-23, display time zone) is when auto mode may install.
	AutoHour int
	// PublicKey is the base64 ed25519 release key. It is public by nature
	// but is still never written to logs.
	PublicKey string
}

// UpdateLast is the light persisted memory of the update checker: the last
// check (JSON), when an install last failed, and a version auto mode must
// leave alone because the admin rolled back from it.
type UpdateLast struct {
	CheckJSON       string
	InstallFailedAt int64 // unix ms, 0 = never
	SkipVersion     string
	// PrevVersion is the version of the kept .prev binary ("" = unknown).
	PrevVersion string
}

const (
	keyUpdEnabled    = "update_enabled"
	keyUpdFeedURL    = "update_feed_url"
	keyUpdMode       = "update_mode"
	keyUpdAutoHour   = "update_auto_hour"
	keyUpdPublicKey  = "update_public_key"
	keyUpdLastCheck  = "update_last_check"
	keyUpdFailedAt   = "update_install_failed_at"
	keyUpdSkipVer    = "update_skip_version"
	keyUpdPrevVer    = "update_prev_version"
	maxUpdateJSONLen = 64 << 10
)

// DefaultUpdateSettings are used for any key not stored yet: checking on,
// notify only, no feed configured, 03:00.
func DefaultUpdateSettings() UpdateSettings {
	return UpdateSettings{Enabled: true, Mode: "notify", AutoHour: 3}
}

func (u UpdateSettings) validate() error {
	switch u.Mode {
	case "off", "notify", "auto":
	default:
		return fmt.Errorf("%w: update mode must be off, notify or auto", ErrInvalid)
	}
	if u.AutoHour < 0 || u.AutoHour > 23 {
		return fmt.Errorf("%w: update hour must be between 0 and 23", ErrInvalid)
	}
	if len(u.FeedURL) > 2048 || len(u.PublicKey) > 256 {
		return fmt.Errorf("%w: update feed URL or key is too long", ErrInvalid)
	}
	return nil
}

func (s *Store) updateKV(ctx context.Context, keys ...string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range keys {
		var v string
		err := s.R.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, k).Scan(&v)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, fmt.Errorf("store: read setting %s: %w", k, err)
		}
		out[k] = v
	}
	return out, nil
}

// GetUpdateSettings reads the update options, defaulting missing keys.
func (s *Store) GetUpdateSettings(ctx context.Context) (UpdateSettings, error) {
	u := DefaultUpdateSettings()
	kv, err := s.updateKV(ctx, keyUpdEnabled, keyUpdFeedURL, keyUpdMode, keyUpdAutoHour, keyUpdPublicKey)
	if err != nil {
		return u, err
	}
	if v, ok := kv[keyUpdEnabled]; ok {
		u.Enabled = v == "1"
	}
	if v, ok := kv[keyUpdFeedURL]; ok {
		u.FeedURL = v
	}
	if v, ok := kv[keyUpdMode]; ok && (v == "off" || v == "notify" || v == "auto") {
		u.Mode = v
	}
	if v, ok := kv[keyUpdAutoHour]; ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 23 {
			u.AutoHour = n
		}
	}
	if v, ok := kv[keyUpdPublicKey]; ok {
		u.PublicKey = v
	}
	return u, nil
}

// PutUpdateSettings validates and upserts the update options in one
// transaction through the store's single writer connection.
func (s *Store) PutUpdateSettings(ctx context.Context, u UpdateSettings) error {
	if err := u.validate(); err != nil {
		return err
	}
	enabled := "0"
	if u.Enabled {
		enabled = "1"
	}
	return s.putUpdateKV(ctx, map[string]string{
		keyUpdEnabled:   enabled,
		keyUpdFeedURL:   u.FeedURL,
		keyUpdMode:      u.Mode,
		keyUpdAutoHour:  strconv.Itoa(u.AutoHour),
		keyUpdPublicKey: u.PublicKey,
	})
}

// GetUpdateLast reads the persisted checker memory.
func (s *Store) GetUpdateLast(ctx context.Context) (UpdateLast, error) {
	var l UpdateLast
	kv, err := s.updateKV(ctx, keyUpdLastCheck, keyUpdFailedAt, keyUpdSkipVer, keyUpdPrevVer)
	if err != nil {
		return l, err
	}
	l.CheckJSON = kv[keyUpdLastCheck]
	l.SkipVersion = kv[keyUpdSkipVer]
	l.PrevVersion = kv[keyUpdPrevVer]
	if n, err := strconv.ParseInt(kv[keyUpdFailedAt], 10, 64); err == nil {
		l.InstallFailedAt = n
	}
	return l, nil
}

// PutUpdateLast stores the checker memory.
func (s *Store) PutUpdateLast(ctx context.Context, l UpdateLast) error {
	if len(l.CheckJSON) > maxUpdateJSONLen {
		l.CheckJSON = ""
	}
	return s.putUpdateKV(ctx, map[string]string{
		keyUpdLastCheck: l.CheckJSON,
		keyUpdFailedAt:  strconv.FormatInt(l.InstallFailedAt, 10),
		keyUpdSkipVer:   l.SkipVersion,
		keyUpdPrevVer:   l.PrevVersion,
	})
}

func (s *Store) putUpdateKV(ctx context.Context, kv map[string]string) error {
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings(key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v,
		); err != nil {
			return fmt.Errorf("store: put setting %s: %w", k, err)
		}
	}
	return tx.Commit()
}
