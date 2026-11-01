package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ChannelConfig holds every alert-channel field across all channel
// types. Only the fields relevant to Channel.Type are meaningful; the
// rest stay zero. The whole struct is encrypted at rest (config_enc)
// because some fields embed secrets (webhook URLs with tokens, API
// keys, SMTP passwords).
type ChannelConfig struct {
	URL        string            `json:"url,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	HMACSecret string            `json:"hmacSecret,omitempty"`
	RoutingKey string            `json:"routingKey,omitempty"`
	APIKey     string            `json:"apiKey,omitempty"`
	Region     string            `json:"region,omitempty"`
	Host       string            `json:"host,omitempty"`
	Port       int               `json:"port,omitempty"`
	Username   string            `json:"username,omitempty"`
	Password   string            `json:"password,omitempty"`
	From       string            `json:"from,omitempty"`
	To         []string          `json:"to,omitempty"`
	TLSMode    string            `json:"tlsMode,omitempty"`
	// Target is the destination inside a service: a Telegram chat id, a
	// Pushover user key or a Matrix room id.
	Target string `json:"target,omitempty"`
	// Topic (Zulip): the topic within the stream.
	Topic string `json:"topic,omitempty"`
	// Template, when set, replaces the body of a single-monitor alert with
	// this text; see alert.RenderTemplate for the placeholders.
	Template string `json:"template,omitempty"`
	// Quiet, when set, holds back this channel's alerts during certain hours.
	Quiet *QuietHours `json:"quiet,omitempty"`
}

// QuietHours is a recurring window in the display time zone in which a
// channel stays silent. Days are 0 (Sunday) to 6 (Saturday) and name the day
// the window starts on; none means every day. A window whose End is not
// after its Start runs past midnight into the next day. BypassCritical lets
// down alerts (and their reminders and recoveries) through anyway.
type QuietHours struct {
	Enabled        bool   `json:"enabled"`
	Days           []int  `json:"days,omitempty"`
	Start          string `json:"start"` // "HH:MM"
	End            string `json:"end"`   // "HH:MM"
	BypassCritical bool   `json:"bypassCritical,omitempty"`
}

// Channel is one configured alert destination.
type Channel struct {
	ID        int64
	Name      string
	Type      string
	Config    ChannelConfig
	IsDefault bool
	CreatedAt time.Time
}

const channelColumns = `id, name, type, config_enc, is_default, created_at`

func (s *Store) scanChannel(sc scanner) (*Channel, error) {
	var c Channel
	var enc []byte
	var isDefault int64
	var createdAt int64
	if err := sc.Scan(&c.ID, &c.Name, &c.Type, &enc, &isDefault, &createdAt); err != nil {
		return nil, err
	}
	c.IsDefault = intToBool(isDefault)
	c.CreatedAt = time.UnixMilli(createdAt)
	if err := s.Box.OpenJSON(enc, &c.Config); err != nil {
		return nil, fmt.Errorf("store: decrypt channel config: %w", err)
	}
	return &c, nil
}

// ListChannels returns every channel with its config decrypted.
func (s *Store) ListChannels(ctx context.Context) ([]Channel, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT `+channelColumns+` FROM channels ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: list channels: %w", err)
	}
	defer rows.Close()

	var out []Channel
	for rows.Next() {
		c, err := s.scanChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan channel: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// GetChannel returns ErrNotFound when id doesn't exist.
func (s *Store) GetChannel(ctx context.Context, id int64) (*Channel, error) {
	c, err := s.scanChannel(s.R.QueryRowContext(ctx, `SELECT `+channelColumns+` FROM channels WHERE id=?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get channel: %w", err)
	}
	return c, nil
}

// CreateChannel seals c.Config, inserts the row and sets c.ID and
// c.CreatedAt.
func (s *Store) CreateChannel(ctx context.Context, c *Channel) error {
	enc, err := s.Box.SealJSON(c.Config)
	if err != nil {
		return fmt.Errorf("store: seal channel config: %w", err)
	}
	c.CreatedAt = time.Now()

	res, err := s.W.ExecContext(ctx, `
		INSERT INTO channels(name, type, config_enc, is_default, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		c.Name, c.Type, enc, boolToInt(c.IsDefault), c.CreatedAt.UnixMilli(),
	)
	if err != nil {
		return wrapConflict(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: last insert id: %w", err)
	}
	c.ID = id
	return nil
}

// UpdateChannel reseals c.Config and replaces the row. It returns
// ErrNotFound if c.ID doesn't exist.
func (s *Store) UpdateChannel(ctx context.Context, c *Channel) error {
	enc, err := s.Box.SealJSON(c.Config)
	if err != nil {
		return fmt.Errorf("store: seal channel config: %w", err)
	}
	res, err := s.W.ExecContext(ctx, `
		UPDATE channels SET name=?, type=?, config_enc=?, is_default=? WHERE id=?`,
		c.Name, c.Type, enc, boolToInt(c.IsDefault), c.ID,
	)
	if err != nil {
		return wrapConflict(err)
	}
	return checkAffected(res)
}

// AssignChannelToAll attaches channel id to every monitor that does not have
// it yet, and returns how many monitors were changed.
func (s *Store) AssignChannelToAll(ctx context.Context, id int64) (int64, error) {
	res, err := s.W.ExecContext(ctx, `
		INSERT OR IGNORE INTO monitor_channels(monitor_id, channel_id)
		SELECT id, ? FROM monitors`, id)
	if err != nil {
		return 0, fmt.Errorf("store: assign channel: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DeleteChannel removes a channel. Its monitor_channels rows are removed
// by the schema's ON DELETE CASCADE.
func (s *Store) DeleteChannel(ctx context.Context, id int64) error {
	res, err := s.W.ExecContext(ctx, `DELETE FROM channels WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("store: delete channel: %w", err)
	}
	return checkAffected(res)
}
