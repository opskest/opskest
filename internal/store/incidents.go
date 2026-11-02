package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// Incident is a human-written report of a problem: a title, an impact
// level and a timeline of updates. It can be linked to the monitors it
// affects and to the status pages that show it.
type Incident struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	Status     string `json:"status"` // investigating, identified, monitoring, resolved
	Impact     string `json:"impact"` // minor, major, maintenance
	CreatedBy  *int64 `json:"createdBy"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
	ResolvedAt *int64 `json:"resolvedAt"`

	Severity      string `json:"severity"` // critical, major, minor
	CommanderID   *int64 `json:"commanderId"`
	CommanderName string `json:"commanderName"`
	Postmortem    string `json:"postmortem"`

	MonitorIDs []int64          `json:"monitorIds"`
	PageIDs    []int64          `json:"pageIds"`
	Updates    []IncidentUpdate `json:"updates"`
}

// IncidentUpdate is one timeline entry on an incident.
type IncidentUpdate struct {
	ID            int64  `json:"id"`
	IncidentID    int64  `json:"incidentId"`
	Status        string `json:"status"`
	Body          string `json:"body"`
	CreatedBy     *int64 `json:"createdBy"`
	CreatedByName string `json:"createdByName"` // joined from users: name, falling back to email
	CreatedAt     int64  `json:"createdAt"`
	Internal      bool   `json:"internal"` // team-only note: never shown on status pages
	EditedAt      *int64 `json:"editedAt"` // set once the entry has been changed after posting
}

// ValidSeverity reports whether s is an incident severity.
func ValidSeverity(s string) bool { return s == "critical" || s == "major" || s == "minor" }

const incidentColumns = `id, title, status, impact, created_by, created_at, updated_at, resolved_at, severity, commander_id, (SELECT COALESCE(NULLIF(u.name, ''), u.email) FROM users u WHERE u.id = commander_id), postmortem`

// incidentColumnsAliased is incidentColumns qualified with the "i." alias
// used by IncidentsForPage's join query.
const incidentColumnsAliased = `i.id, i.title, i.status, i.impact, i.created_by, i.created_at, i.updated_at, i.resolved_at, i.severity, i.commander_id, (SELECT COALESCE(NULLIF(u.name, ''), u.email) FROM users u WHERE u.id = i.commander_id), i.postmortem`

func scanIncident(sc scanner) (Incident, error) {
	var inc Incident
	var createdBy, resolvedAt, commander sql.NullInt64
	var commanderName sql.NullString
	if err := sc.Scan(&inc.ID, &inc.Title, &inc.Status, &inc.Impact, &createdBy, &inc.CreatedAt, &inc.UpdatedAt, &resolvedAt,
		&inc.Severity, &commander, &commanderName, &inc.Postmortem); err != nil {
		return inc, err
	}
	inc.CommanderID = scanNullInt64(commander)
	inc.CommanderName = commanderName.String
	inc.CreatedBy = scanNullInt64(createdBy)
	inc.ResolvedAt = scanNullInt64(resolvedAt)
	return inc, nil
}

// inClause returns a "?,?,...,?" placeholder list of n entries, and args
// holding ids as []any ready to splice into a variadic call, for
// building `WHERE col IN (...)` queries with a dynamic id count.
func inClause(ids []int64) (placeholders string, args []any) {
	args = make([]any, len(ids))
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = "?"
		args[i] = id
	}
	return strings.Join(parts, ","), args
}

// CreateIncident inserts in and its monitor/page links, and writes the
// first incident_updates row (status in.Status, body firstBody,
// authored by in.CreatedBy). It sets in.ID, in.CreatedAt and
// in.UpdatedAt. An invalid Status or Impact returns ErrInvalid.
func (s *Store) CreateIncident(ctx context.Context, in *Incident, firstBody string) error {
	now := time.Now().UnixMilli()
	in.CreatedAt = now
	in.UpdatedAt = now
	in.ResolvedAt = nil

	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO incidents(title, status, impact, created_by, created_at, updated_at, resolved_at, severity, commander_id)
		VALUES (?,?,?,?,?,?,NULL,?,?)`,
		in.Title, in.Status, in.Impact, int64OrNil(in.CreatedBy), now, now, in.Severity, int64OrNil(in.CommanderID),
	)
	if err != nil {
		return wrapConflict(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: last insert id: %w", err)
	}
	in.ID = id

	if err := replaceIncidentMonitors(ctx, tx, id, in.MonitorIDs); err != nil {
		return err
	}
	if err := replaceIncidentPages(ctx, tx, id, in.PageIDs); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO incident_updates(incident_id, status, body, created_by, created_at)
		VALUES (?,?,?,?,?)`,
		id, in.Status, firstBody, int64OrNil(in.CreatedBy), now,
	); err != nil {
		return wrapConflict(err)
	}

	return tx.Commit()
}

// UpdateIncident replaces in's title, impact and monitor/page links.
// Status is owned by AddIncidentUpdate, not this method. ErrNotFound if
// in.ID doesn't exist.
func (s *Store) UpdateIncident(ctx context.Context, in *Incident) error {
	now := time.Now().UnixMilli()

	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `UPDATE incidents SET title=?, impact=?, severity=?, commander_id=?, updated_at=? WHERE id=?`,
		in.Title, in.Impact, in.Severity, int64OrNil(in.CommanderID), now, in.ID)
	if err != nil {
		return wrapConflict(err)
	}
	if err := checkAffected(res); err != nil {
		return err
	}
	in.UpdatedAt = now

	if err := replaceIncidentMonitors(ctx, tx, in.ID, in.MonitorIDs); err != nil {
		return err
	}
	if err := replaceIncidentPages(ctx, tx, in.ID, in.PageIDs); err != nil {
		return err
	}

	return tx.Commit()
}

func replaceIncidentMonitors(ctx context.Context, tx *sql.Tx, incidentID int64, monitorIDs []int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM incident_monitors WHERE incident_id=?`, incidentID); err != nil {
		return fmt.Errorf("store: clear incident monitors: %w", err)
	}
	for _, mid := range monitorIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO incident_monitors(incident_id, monitor_id) VALUES (?,?)`, incidentID, mid); err != nil {
			return fmt.Errorf("store: insert incident monitor: %w", err)
		}
	}
	return nil
}

func replaceIncidentPages(ctx context.Context, tx *sql.Tx, incidentID int64, pageIDs []int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM incident_pages WHERE incident_id=?`, incidentID); err != nil {
		return fmt.Errorf("store: clear incident pages: %w", err)
	}
	for _, pid := range pageIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO incident_pages(incident_id, page_id) VALUES (?,?)`, incidentID, pid); err != nil {
			return fmt.Errorf("store: insert incident page: %w", err)
		}
	}
	return nil
}

// AddIncidentUpdate appends u to its incident's timeline and updates the
// incident's status (and resolved_at, when u.Status is "resolved") to
// match. It sets u.ID and u.CreatedAt. ErrNotFound if u.IncidentID
// doesn't exist; ErrInvalid for an unrecognized status.
func (s *Store) AddIncidentUpdate(ctx context.Context, u *IncidentUpdate) error {
	now := time.Now().UnixMilli()

	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	var exists int64
	var current string
	err = tx.QueryRowContext(ctx, `SELECT id, status FROM incidents WHERE id=?`, u.IncidentID).Scan(&exists, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("store: check incident: %w", err)
	}

	if u.Internal {
		u.Status = current // a team note never moves the incident's status
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO incident_updates(incident_id, status, body, created_by, created_at, internal)
		VALUES (?,?,?,?,?,?)`,
		u.IncidentID, u.Status, u.Body, int64OrNil(u.CreatedBy), now, boolToInt(u.Internal),
	)
	if err != nil {
		return wrapConflict(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: last insert id: %w", err)
	}
	u.ID = id
	u.CreatedAt = now

	switch {
	case u.Internal:
		_, err = tx.ExecContext(ctx, `UPDATE incidents SET updated_at=? WHERE id=?`, now, u.IncidentID)
	case u.Status == "resolved":
		_, err = tx.ExecContext(ctx, `UPDATE incidents SET status=?, updated_at=?, resolved_at=? WHERE id=?`,
			u.Status, now, now, u.IncidentID)
	default:
		_, err = tx.ExecContext(ctx, `UPDATE incidents SET status=?, updated_at=? WHERE id=?`,
			u.Status, now, u.IncidentID)
	}
	if err != nil {
		return wrapConflict(err)
	}

	return tx.Commit()
}

// EditIncidentUpdate changes the body of one timeline entry of incident
// incidentID and, when internal is non-nil, whether it is a team-only note.
// It stamps edited_at and the incident's updated_at but never touches the
// incident's status, whatever the entry's own status. ErrNotFound if the
// entry isn't on that incident; ErrInvalid when a team note would be empty.
func (s *Store) EditIncidentUpdate(ctx context.Context, incidentID, updateID int64, body string, internal *bool) (*IncidentUpdate, error) {
	now := time.Now().UnixMilli()
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	var cur int64
	if err := tx.QueryRowContext(ctx, `SELECT internal FROM incident_updates WHERE id=? AND incident_id=?`, updateID, incidentID).Scan(&cur); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get incident update: %w", err)
	}
	isInternal := cur != 0
	if internal != nil {
		isInternal = *internal
	}
	if isInternal && strings.TrimSpace(body) == "" {
		return nil, ErrInvalid
	}
	if _, err := tx.ExecContext(ctx, `UPDATE incident_updates SET body=?, internal=?, edited_at=? WHERE id=? AND incident_id=?`,
		body, boolToInt(isInternal), now, updateID, incidentID); err != nil {
		return nil, fmt.Errorf("store: edit incident update: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE incidents SET updated_at=? WHERE id=?`, now, incidentID); err != nil {
		return nil, fmt.Errorf("store: touch incident: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	inc, err := s.GetIncident(ctx, incidentID)
	if err != nil {
		return nil, err
	}
	for i := range inc.Updates {
		if inc.Updates[i].ID == updateID {
			return &inc.Updates[i], nil
		}
	}
	return nil, ErrNotFound
}

// GetIncident returns ErrNotFound when id doesn't exist.
func (s *Store) GetIncident(ctx context.Context, id int64) (*Incident, error) {
	inc, err := scanIncident(s.R.QueryRowContext(ctx, `SELECT `+incidentColumns+` FROM incidents WHERE id=?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get incident: %w", err)
	}

	incidents := []Incident{inc}
	if err := s.fillIncidentExtras(ctx, incidents); err != nil {
		return nil, err
	}
	return &incidents[0], nil
}

// ListIncidents returns incidents newest-first, at most 200 (default 50
// when limit<=0), with monitor/page links and updates filled in.
// activeOnly excludes resolved incidents. before (unix ms) is an
// exclusive cursor on created_at.
func (s *Store) ListIncidents(ctx context.Context, activeOnly bool, limit int, before int64) ([]Incident, error) {
	limit = clampLimit(limit)

	query := `SELECT ` + incidentColumns + ` FROM incidents`
	var where []string
	var args []any
	if activeOnly {
		where = append(where, `status<>'resolved'`)
	}
	if before > 0 {
		where = append(where, `created_at<?`)
		args = append(args, before)
	}
	for i, w := range where {
		if i == 0 {
			query += " WHERE "
		} else {
			query += " AND "
		}
		query += w
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.R.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list incidents: %w", err)
	}
	var out []Incident
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: scan incident: %w", err)
		}
		out = append(out, inc)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	if err := s.fillIncidentExtras(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// IncidentsForPage returns the incidents linked to pageID that a status
// page should show: every active (non-resolved) incident, plus resolved
// incidents whose resolved_at is at or after since. Newest first, with
// monitor/page links and updates filled in.
func (s *Store) IncidentsForPage(ctx context.Context, pageID int64, since int64) ([]Incident, error) {
	rows, err := s.R.QueryContext(ctx, `
		SELECT `+incidentColumnsAliased+`
		FROM incidents i
		JOIN incident_pages ip ON ip.incident_id = i.id
		WHERE ip.page_id=? AND (i.status<>'resolved' OR i.resolved_at>=?)
		ORDER BY i.created_at DESC`, pageID, since)
	if err != nil {
		return nil, fmt.Errorf("store: incidents for page: %w", err)
	}
	var out []Incident
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: scan incident: %w", err)
		}
		out = append(out, inc)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	if err := s.fillIncidentExtras(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteIncident removes an incident. Its updates and monitor/page links
// cascade via their FK ON DELETE CASCADE.
func (s *Store) DeleteIncident(ctx context.Context, id int64) error {
	res, err := s.W.ExecContext(ctx, `DELETE FROM incidents WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("store: delete incident: %w", err)
	}
	return checkAffected(res)
}

// fillIncidentExtras batch-loads MonitorIDs, PageIDs and Updates (each
// with its author's name, falling back to email) for every incident in
// incidents, in 3 queries total rather than one per incident.
func (s *Store) fillIncidentExtras(ctx context.Context, incidents []Incident) error {
	if len(incidents) == 0 {
		return nil
	}

	ids := make([]int64, len(incidents))
	byID := make(map[int64]*Incident, len(incidents))
	for i := range incidents {
		ids[i] = incidents[i].ID
		byID[incidents[i].ID] = &incidents[i]
	}
	placeholders, args := inClause(ids)

	monRows, err := s.R.QueryContext(ctx, `SELECT incident_id, monitor_id FROM incident_monitors WHERE incident_id IN (`+placeholders+`) ORDER BY incident_id, monitor_id`, args...)
	if err != nil {
		return fmt.Errorf("store: incident monitors: %w", err)
	}
	for monRows.Next() {
		var incID, monID int64
		if err := monRows.Scan(&incID, &monID); err != nil {
			monRows.Close()
			return fmt.Errorf("store: scan incident monitor: %w", err)
		}
		if inc, ok := byID[incID]; ok {
			inc.MonitorIDs = append(inc.MonitorIDs, monID)
		}
	}
	if err := monRows.Err(); err != nil {
		monRows.Close()
		return err
	}
	monRows.Close()

	pageRows, err := s.R.QueryContext(ctx, `SELECT incident_id, page_id FROM incident_pages WHERE incident_id IN (`+placeholders+`) ORDER BY incident_id, page_id`, args...)
	if err != nil {
		return fmt.Errorf("store: incident pages: %w", err)
	}
	for pageRows.Next() {
		var incID, pageID int64
		if err := pageRows.Scan(&incID, &pageID); err != nil {
			pageRows.Close()
			return fmt.Errorf("store: scan incident page: %w", err)
		}
		if inc, ok := byID[incID]; ok {
			inc.PageIDs = append(inc.PageIDs, pageID)
		}
	}
	if err := pageRows.Err(); err != nil {
		pageRows.Close()
		return err
	}
	pageRows.Close()

	updRows, err := s.R.QueryContext(ctx, `
		SELECT u.id, u.incident_id, u.status, u.body, u.created_by,
			COALESCE(NULLIF(usr.name, ''), usr.email, ''), u.created_at, u.internal, u.edited_at
		FROM incident_updates u
		LEFT JOIN users usr ON usr.id = u.created_by
		WHERE u.incident_id IN (`+placeholders+`)
		ORDER BY u.incident_id, u.created_at ASC, u.id ASC`, args...)
	if err != nil {
		return fmt.Errorf("store: incident updates: %w", err)
	}
	for updRows.Next() {
		var u IncidentUpdate
		var createdBy, internal, edited sql.NullInt64
		if err := updRows.Scan(&u.ID, &u.IncidentID, &u.Status, &u.Body, &createdBy, &u.CreatedByName, &u.CreatedAt, &internal, &edited); err != nil {
			updRows.Close()
			return fmt.Errorf("store: scan incident update: %w", err)
		}
		u.CreatedBy = scanNullInt64(createdBy)
		u.Internal = internal.Int64 != 0
		u.EditedAt = scanNullInt64(edited)
		if inc, ok := byID[u.IncidentID]; ok {
			inc.Updates = append(inc.Updates, u)
		}
	}
	if err := updRows.Err(); err != nil {
		updRows.Close()
		return err
	}
	updRows.Close()

	return nil
}

// SetPostmortem stores an incident's postmortem text. ErrNotFound if id
// doesn't exist.
func (s *Store) SetPostmortem(ctx context.Context, id int64, text string) error {
	res, err := s.W.ExecContext(ctx, `UPDATE incidents SET postmortem=?, updated_at=? WHERE id=?`, text, time.Now().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("store: set postmortem: %w", err)
	}
	return checkAffected(res)
}

// PageIncidentsBefore returns up to limit incidents linked to pageID that
// sort after the cursor (beforeTS, beforeID) in (created_at DESC, id DESC)
// order — beforeTS 0 = from the newest — with updates filled in: the
// status page's paginated history. The id tiebreak keeps two incidents
// created in the same millisecond from straddling a page and vanishing.
func (s *Store) PageIncidentsBefore(ctx context.Context, pageID, beforeTS, beforeID int64, limit int) ([]Incident, error) {
	if beforeTS <= 0 {
		beforeTS = time.Now().UnixMilli() + 1
		beforeID = 0
	}
	if beforeID <= 0 {
		beforeID = math.MaxInt64
	}
	limit = clampLimit(limit)
	rows, err := s.R.QueryContext(ctx, `
		SELECT `+incidentColumnsAliased+`
		FROM incidents i
		JOIN incident_pages ip ON ip.incident_id = i.id
		WHERE ip.page_id=? AND (i.created_at<? OR (i.created_at=? AND i.id<?))
		ORDER BY i.created_at DESC, i.id DESC LIMIT ?`, pageID, beforeTS, beforeTS, beforeID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: page incidents: %w", err)
	}
	var out []Incident
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: scan incident: %w", err)
		}
		out = append(out, inc)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if err := s.fillIncidentExtras(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// PageIncident returns incident id only if it is linked to pageID;
// ErrNotFound otherwise (so a page can't be used to read any incident).
func (s *Store) PageIncident(ctx context.Context, pageID, id int64) (*Incident, error) {
	inc, err := scanIncident(s.R.QueryRowContext(ctx, `
		SELECT `+incidentColumnsAliased+`
		FROM incidents i
		JOIN incident_pages ip ON ip.incident_id = i.id
		WHERE ip.page_id=? AND i.id=?`, pageID, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: page incident: %w", err)
	}
	list := []Incident{inc}
	if err := s.fillIncidentExtras(ctx, list); err != nil {
		return nil, err
	}
	return &list[0], nil
}
