package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Page is a public or private status page (§11): a list of groups and/or
// monitors, each optionally under a display name that overrides the
// underlying group/monitor name.
type Page struct {
	ID          int64  `json:"id"`
	Slug        string `json:"slug"` // [a-z0-9-]{2,48}, unique
	Title       string `json:"title"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"` // public, private
	Layout      string `json:"layout"`     // list, board
	ShowLatency bool   `json:"showLatency"`

	// Alarm: the board/list plays a sound when a monitor on the page goes
	// down (AlarmDown) or degraded (AlarmDegraded). Empty = default sound.
	AlarmMuted    bool   `json:"alarmMuted"`
	AlarmDown     string `json:"alarmDown"`
	AlarmDegraded string `json:"alarmDegraded"`

	// Branding for a customer-facing page. URLs are https-only and the
	// colour is #rrggbb (validated by the API, not here).
	LogoURL          string `json:"logoUrl"`
	BrandColor       string `json:"brandColor"`
	WebsiteURL       string `json:"websiteUrl"`
	SupportURL       string `json:"supportUrl"`
	FooterText       string `json:"footerText"`
	ShowResponseTime bool   `json:"showResponseTime"` // public p50/p95 chart per component
	HistoryDays      int    `json:"historyDays"`      // 30, 60 or 90 days of uptime bars

	// Subscribers: visitors may subscribe by email (sent through the email
	// channel SubscriberChannelID) or a Slack webhook.
	SubscriptionsEnabled bool   `json:"subscriptionsEnabled"`
	SubscriberChannelID  *int64 `json:"subscriberChannelId"`

	// Settings: theme, markdown copy, announcement and show/hide switches.
	// nil on input means "not sent": create uses the defaults and update
	// keeps what is stored. It is always set on output.
	Settings *PageSettings `json:"settings"`

	Items []PageItem `json:"items"`

	CreatedAt int64 `json:"createdAt"`
	UpdatedAt int64 `json:"updatedAt"`
}

// PageItem is one row on a status page: either a group or a monitor
// (never both), in display order (Sort).
type PageItem struct {
	Sort        int    `json:"sort"`
	GroupID     *int64 `json:"groupId"`
	MonitorID   *int64 `json:"monitorId"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"` // one line, shown on the public page
}

const pageColumns = `id, slug, title, description, visibility, layout, show_latency, alarm_muted, alarm_down, alarm_degraded, logo_url, brand_color, website_url, support_url, footer_text, show_response_time, history_days, subscriptions_enabled, subscriber_channel_id, settings, created_at, updated_at`

func scanPage(sc scanner) (Page, error) {
	var p Page
	var showLatency, muted, showRT, subs int64
	var subChan sql.NullInt64
	var settings string
	err := sc.Scan(&p.ID, &p.Slug, &p.Title, &p.Description, &p.Visibility, &p.Layout, &showLatency, &muted, &p.AlarmDown, &p.AlarmDegraded,
		&p.LogoURL, &p.BrandColor, &p.WebsiteURL, &p.SupportURL, &p.FooterText, &showRT, &p.HistoryDays, &subs, &subChan, &settings, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return p, err
	}
	ps := decodePageSettings(settings)
	p.Settings = &ps
	p.SubscriptionsEnabled = intToBool(subs)
	p.SubscriberChannelID = scanNullInt64(subChan)
	p.ShowLatency = intToBool(showLatency)
	p.ShowResponseTime = intToBool(showRT)
	p.AlarmMuted = intToBool(muted)
	return p, nil
}

// ListPages returns every status page, with Items filled in, using 2
// queries total.
func (s *Store) ListPages(ctx context.Context) ([]Page, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT `+pageColumns+` FROM status_pages ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: list pages: %w", err)
	}
	var out []Page
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: scan page: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	byID := make(map[int64]*Page, len(out))
	for i := range out {
		byID[out[i].ID] = &out[i]
	}

	itemRows, err := s.R.QueryContext(ctx, `SELECT page_id, sort, group_id, monitor_id, display_name, description FROM status_page_items ORDER BY page_id, sort`)
	if err != nil {
		return nil, fmt.Errorf("store: list page items: %w", err)
	}
	defer itemRows.Close()
	for itemRows.Next() {
		var pageID int64
		var it PageItem
		var groupID, monitorID sql.NullInt64
		if err := itemRows.Scan(&pageID, &it.Sort, &groupID, &monitorID, &it.DisplayName, &it.Description); err != nil {
			return nil, fmt.Errorf("store: scan page item: %w", err)
		}
		it.GroupID = scanNullInt64(groupID)
		it.MonitorID = scanNullInt64(monitorID)
		if p, ok := byID[pageID]; ok {
			p.Items = append(p.Items, it)
		}
	}
	if err := itemRows.Err(); err != nil {
		return nil, err
	}

	return out, nil
}

func (s *Store) loadPageItems(ctx context.Context, pageID int64) ([]PageItem, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT sort, group_id, monitor_id, display_name, description FROM status_page_items WHERE page_id=? ORDER BY sort`, pageID)
	if err != nil {
		return nil, fmt.Errorf("store: list page items: %w", err)
	}
	defer rows.Close()

	var out []PageItem
	for rows.Next() {
		var it PageItem
		var groupID, monitorID sql.NullInt64
		if err := rows.Scan(&it.Sort, &groupID, &monitorID, &it.DisplayName, &it.Description); err != nil {
			return nil, fmt.Errorf("store: scan page item: %w", err)
		}
		it.GroupID = scanNullInt64(groupID)
		it.MonitorID = scanNullInt64(monitorID)
		out = append(out, it)
	}
	return out, rows.Err()
}

// GetPage returns ErrNotFound when id doesn't exist.
func (s *Store) GetPage(ctx context.Context, id int64) (*Page, error) {
	p, err := scanPage(s.R.QueryRowContext(ctx, `SELECT `+pageColumns+` FROM status_pages WHERE id=?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get page: %w", err)
	}
	items, err := s.loadPageItems(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	p.Items = items
	return &p, nil
}

// GetPageBySlug returns ErrNotFound when slug doesn't exist.
func (s *Store) GetPageBySlug(ctx context.Context, slug string) (*Page, error) {
	p, err := scanPage(s.R.QueryRowContext(ctx, `SELECT `+pageColumns+` FROM status_pages WHERE slug=?`, slug))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get page by slug: %w", err)
	}
	items, err := s.loadPageItems(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	p.Items = items
	return &p, nil
}

// CreatePage inserts p and its items, setting p.ID, p.CreatedAt and
// p.UpdatedAt. A slug already in use returns ErrConflict; an invalid
// Visibility or Layout returns ErrInvalid.
func (s *Store) CreatePage(ctx context.Context, p *Page) error {
	normAlarm(p)
	if p.Settings == nil {
		d := DefaultPageSettings()
		p.Settings = &d
	}
	now := time.Now().UnixMilli()
	p.CreatedAt = now
	p.UpdatedAt = now

	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO status_pages(slug, title, description, visibility, layout, show_latency, alarm_muted, alarm_down, alarm_degraded,
			logo_url, brand_color, website_url, support_url, footer_text, show_response_time, history_days,
			subscriptions_enabled, subscriber_channel_id, settings, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.Slug, p.Title, p.Description, p.Visibility, p.Layout, boolToInt(p.ShowLatency), boolToInt(p.AlarmMuted), p.AlarmDown, p.AlarmDegraded,
		p.LogoURL, p.BrandColor, p.WebsiteURL, p.SupportURL, p.FooterText, boolToInt(p.ShowResponseTime), p.HistoryDays,
		boolToInt(p.SubscriptionsEnabled), int64OrNil(p.SubscriberChannelID), encodePageSettings(*p.Settings), now, now,
	)
	if err != nil {
		return wrapConflict(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: last insert id: %w", err)
	}
	p.ID = id

	if err := insertPageItems(ctx, tx, id, p.Items); err != nil {
		return err
	}

	return tx.Commit()
}

// UpdatePage replaces p's row and its items entirely. ErrNotFound if
// p.ID doesn't exist; ErrConflict for a slug collision; ErrInvalid for
// a bad Visibility or for a Layout that differs from the stored one (a
// page's layout is fixed when it is created; an empty Layout keeps it).
func (s *Store) UpdatePage(ctx context.Context, p *Page) error {
	normAlarm(p)
	if p.Settings == nil { // not sent: keep what is stored
		cur, err := s.GetPage(ctx, p.ID)
		if err != nil {
			return err
		}
		p.Settings = cur.Settings
	}
	now := time.Now().UnixMilli()

	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	var storedLayout string
	if err := tx.QueryRowContext(ctx, `SELECT layout FROM status_pages WHERE id=?`, p.ID).Scan(&storedLayout); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("store: read page layout: %w", err)
	}
	if p.Layout == "" {
		p.Layout = storedLayout
	}
	if p.Layout != storedLayout {
		return fmt.Errorf("%w: a page's layout cannot be changed after it is created", ErrInvalid)
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE status_pages SET slug=?, title=?, description=?, visibility=?, layout=?, show_latency=?, alarm_muted=?, alarm_down=?, alarm_degraded=?,
			logo_url=?, brand_color=?, website_url=?, support_url=?, footer_text=?, show_response_time=?, history_days=?,
			subscriptions_enabled=?, subscriber_channel_id=?, settings=?, updated_at=?
		WHERE id=?`,
		p.Slug, p.Title, p.Description, p.Visibility, p.Layout, boolToInt(p.ShowLatency), boolToInt(p.AlarmMuted), p.AlarmDown, p.AlarmDegraded,
		p.LogoURL, p.BrandColor, p.WebsiteURL, p.SupportURL, p.FooterText, boolToInt(p.ShowResponseTime), p.HistoryDays,
		boolToInt(p.SubscriptionsEnabled), int64OrNil(p.SubscriberChannelID), encodePageSettings(*p.Settings), now, p.ID,
	)
	if err != nil {
		return wrapConflict(err)
	}
	if err := checkAffected(res); err != nil {
		return err
	}
	p.UpdatedAt = now

	if _, err := tx.ExecContext(ctx, `DELETE FROM status_page_items WHERE page_id=?`, p.ID); err != nil {
		return fmt.Errorf("store: clear page items: %w", err)
	}
	if err := insertPageItems(ctx, tx, p.ID, p.Items); err != nil {
		return err
	}

	return tx.Commit()
}

func insertPageItems(ctx context.Context, tx *sql.Tx, pageID int64, items []PageItem) error {
	for _, it := range items {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO status_page_items(page_id, sort, group_id, monitor_id, display_name, description)
			VALUES (?,?,?,?,?,?)`,
			pageID, it.Sort, int64OrNil(it.GroupID), int64OrNil(it.MonitorID), it.DisplayName, it.Description,
		); err != nil {
			return fmt.Errorf("store: insert page item: %w", err)
		}
	}
	return nil
}

// DeletePage removes a status page. Its items cascade via ON DELETE
// CASCADE; incidents linked to it lose that link the same way.
func (s *Store) DeletePage(ctx context.Context, id int64) error {
	res, err := s.W.ExecContext(ctx, `DELETE FROM status_pages WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("store: delete page: %w", err)
	}
	return checkAffected(res)
}

// Alarm sounds a page may choose; anything else falls back to the default.
var (
	alarmDownSounds     = map[string]bool{"police-siren": true, "industrial-klaxon": true, "retro-panic": true}
	alarmDegradedSounds = map[string]bool{"modern-chime": true, "tech-radar": true}
)

func normAlarm(p *Page) {
	if !alarmDownSounds[p.AlarmDown] {
		p.AlarmDown = ""
	}
	if !alarmDegradedSounds[p.AlarmDegraded] {
		p.AlarmDegraded = ""
	}
}
