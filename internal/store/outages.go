package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

// Outage is one DOWN episode for a monitor. EndedAt is nil while the
// outage is still active.
type Outage struct {
	ID        int64 `json:"id"`
	MonitorID int64 `json:"monitorId"`

	StartedAt int64  `json:"startedAt"`
	EndedAt   *int64 `json:"endedAt"`

	FirstReason string `json:"firstReason"`
	ErrClass    string `json:"errClass"`

	AckedBy *string `json:"ackedBy"`
	AckedAt *int64  `json:"ackedAt"`

	Note   *string `json:"note"`
	NoteBy *string `json:"noteBy"`
	NoteAt *int64  `json:"noteAt"`
	// NoteCount is how many notes the outage's thread holds (Note, NoteBy
	// and NoteAt summarise the newest one).
	NoteCount int `json:"noteCount"`

	// Regions is the multi-region confirmation, nil until one has run.
	Regions *RegionCheck `json:"regions"`

	// SuppressedBy is the parent monitor that was down when this outage
	// opened: its alerts are held until that parent recovers. nil for an
	// ordinary (alerted) outage.
	SuppressedBy *int64 `json:"suppressedBy"`
}

// OpSetOutageSuppressed sets (or, with nil, clears) outage id's
// suppressed_by, for use with Writer.Do.
func OpSetOutageSuppressed(id int64, by *int64) Op {
	return func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE outages SET suppressed_by=? WHERE id=?`, int64OrNil(by), id)
		return err
	}
}

// RegionCheck is what other regions saw when the outage opened.
type RegionCheck struct {
	CheckedAt int64         `json:"checkedAt"`
	Reachable int           `json:"reachable"` // probes that got a healthy answer
	Total     int           `json:"total"`
	Error     string        `json:"error,omitempty"` // the confirmation itself failed
	Probes    []RegionProbe `json:"probes"`
}

// RegionProbe is one remote probe's result.
type RegionProbe struct {
	Country   string `json:"country"`
	City      string `json:"city"`
	OK        bool   `json:"ok"`
	Code      int    `json:"code,omitempty"`
	LatencyMs int    `json:"latencyMs"`
	Detail    string `json:"detail,omitempty"`
}

// OpSetOutageRegions stores rc on outage id, for use with Writer.Do.
func OpSetOutageRegions(id int64, rc RegionCheck) Op {
	b, _ := json.Marshal(rc)
	return func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE outages SET regions=? WHERE id=?`, string(b), id)
		return err
	}
}

func strPtrOrNil(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func scanOutage(sc scanner) (Outage, error) {
	var o Outage
	var endedAt, ackedAt, suppressedBy, noteAt sql.NullInt64
	var ackedBy, note, noteBy, regions sql.NullString

	err := sc.Scan(
		&o.ID, &o.MonitorID, &o.StartedAt, &endedAt, &o.FirstReason, &o.ErrClass,
		&ackedBy, &ackedAt, &note, &noteBy, &regions, &suppressedBy, &noteAt, &o.NoteCount,
	)
	if err != nil {
		return o, err
	}

	o.EndedAt = scanNullInt64(endedAt)
	o.AckedAt = scanNullInt64(ackedAt)
	o.SuppressedBy = scanNullInt64(suppressedBy)
	o.NoteAt = scanNullInt64(noteAt)
	if ackedBy.Valid {
		o.AckedBy = &ackedBy.String
	}
	if note.Valid {
		o.Note = &note.String
	}
	if noteBy.Valid {
		o.NoteBy = &noteBy.String
	}
	if regions.Valid && regions.String != "" {
		var rc RegionCheck
		if json.Unmarshal([]byte(regions.String), &rc) == nil {
			o.Regions = &rc
		}
	}
	return o, nil
}

const outageColumns = `id, monitor_id, started_at, ended_at, first_reason, err_class, acked_by, acked_at, note, note_by, regions, suppressed_by, note_at,
	(SELECT COUNT(*) FROM outage_notes n WHERE n.outage_id = outages.id)`

// MaxOutageID returns the highest outage id in use, or 0 if there are
// none yet, so callers can mint the next id for OpOpenOutage themselves
// (the writer assigns ids explicitly rather than via AUTOINCREMENT,
// since the row is inserted by an Op running on the writer's connection,
// not through a direct INSERT that could use LastInsertId).
func (s *Store) MaxOutageID(ctx context.Context) (int64, error) {
	var id sql.NullInt64
	if err := s.R.QueryRowContext(ctx, `SELECT MAX(id) FROM outages`).Scan(&id); err != nil {
		return 0, fmt.Errorf("store: max outage id: %w", err)
	}
	if !id.Valid {
		return 0, nil
	}
	return id.Int64, nil
}

// OpOpenOutage inserts o with its explicit id, for use with Writer.Do.
func OpOpenOutage(o Outage) Op {
	return func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO outages(id, monitor_id, started_at, ended_at, first_reason, err_class, acked_by, acked_at, note, note_by, suppressed_by)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			o.ID, o.MonitorID, o.StartedAt, int64OrNil(o.EndedAt), o.FirstReason, o.ErrClass,
			strPtrOrNil(o.AckedBy), int64OrNil(o.AckedAt), strPtrOrNil(o.Note), strPtrOrNil(o.NoteBy), int64OrNil(o.SuppressedBy),
		)
		return err
	}
}

// OpDeleteOutage removes outage id and its notes, for use with Writer.DoFor
// (a monitor that became a 3rd-party feed never has outages).
func OpDeleteOutage(id int64) Op {
	return func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM outage_notes WHERE outage_id=?`, id); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM outages WHERE id=?`, id)
		return err
	}
}

// OpCloseOutage closes outage id at endedAt, for use with Writer.Do.
func OpCloseOutage(id, endedAt int64) Op {
	return func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE outages SET ended_at=? WHERE id=?`, endedAt, id)
		return err
	}
}

// AckOutage records who acknowledged outage id and when. It writes
// directly via the writer connection (not queued), since it is a rare,
// user-triggered action that should apply immediately. ErrNotFound if id
// doesn't exist.
func (s *Store) AckOutage(ctx context.Context, id int64, by string, at int64) error {
	res, err := s.W.ExecContext(ctx, `UPDATE outages SET acked_by=?, acked_at=? WHERE id=?`, by, at, id)
	if err != nil {
		return fmt.Errorf("store: ack outage: %w", err)
	}
	return checkAffected(res)
}

// ActiveOutages returns every outage with no end time, newest first.
func (s *Store) ActiveOutages(ctx context.Context) ([]Outage, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT `+outageColumns+` FROM outages WHERE ended_at IS NULL ORDER BY started_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: active outages: %w", err)
	}
	defer rows.Close()

	var out []Outage
	for rows.Next() {
		o, err := scanOutage(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan outage: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// OutageFilter narrows ListOutages. A nil pointer field means "no
// filter"; zero From/To/Before means "no bound".
type OutageFilter struct {
	Active    *bool
	MonitorID *int64
	GroupID   *int64

	From, To, Before int64
	// BeforeID breaks ties for rows sharing Before's started_at: with it,
	// the page continues at (started_at, id) < (Before, BeforeID), so a
	// storm of outages starting in the same millisecond isn't skipped.
	BeforeID int64

	// Query matches the monitor's name, the first reason or the note
	// (case-insensitive substring).
	Query string
	// Note is "missing" (no note yet) or "present"; "" means either.
	Note string
	// MinDurationMs keeps outages at least this long; an ongoing outage
	// counts up to Now.
	MinDurationMs int64
	Now           int64

	Limit int
}

const outageListDefaultLimit = 50
const outageListMaxLimit = 200

// ListOutages returns outages matching f, newest first, limited to at
// most 200 rows.
func (s *Store) ListOutages(ctx context.Context, f OutageFilter) ([]Outage, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = outageListDefaultLimit
	}
	if limit > outageListMaxLimit {
		limit = outageListMaxLimit
	}

	query := `SELECT o.id, o.monitor_id, o.started_at, o.ended_at, o.first_reason, o.err_class, o.acked_by, o.acked_at, o.note, o.note_by, o.regions, o.suppressed_by, o.note_at,
		(SELECT COUNT(*) FROM outage_notes n WHERE n.outage_id = o.id)
		FROM outages o`
	var joins []string
	var where []string
	var args []any

	if f.GroupID != nil || f.Query != "" {
		// LEFT: an outage of a deleted monitor still matches its reason/note.
		joins = append(joins, `LEFT JOIN monitors m ON m.id = o.monitor_id`)
	}
	if f.GroupID != nil {
		where = append(where, `m.group_id=?`)
		args = append(args, *f.GroupID)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + likeEscape(strings.ToLower(q)) + "%"
		where = append(where, `(lower(COALESCE(m.name,'')) LIKE ? ESCAPE '\' OR lower(o.first_reason) LIKE ? ESCAPE '\' OR EXISTS (SELECT 1 FROM outage_notes n WHERE n.outage_id = o.id AND lower(n.body) LIKE ? ESCAPE '\'))`)
		args = append(args, like, like, like)
	}
	switch f.Note {
	case "missing":
		where = append(where, `(o.note IS NULL OR trim(o.note)='')`)
	case "present":
		where = append(where, `(o.note IS NOT NULL AND trim(o.note)<>'')`)
	}
	if f.MinDurationMs > 0 {
		where = append(where, `(COALESCE(o.ended_at, ?) - o.started_at) >= ?`)
		args = append(args, f.Now, f.MinDurationMs)
	}
	if f.Active != nil {
		if *f.Active {
			where = append(where, `o.ended_at IS NULL`)
		} else {
			where = append(where, `o.ended_at IS NOT NULL`)
		}
	}
	if f.MonitorID != nil {
		where = append(where, `o.monitor_id=?`)
		args = append(args, *f.MonitorID)
	}
	if f.From != 0 {
		where = append(where, `o.started_at>=?`)
		args = append(args, f.From)
	}
	if f.To != 0 {
		where = append(where, `o.started_at<=?`)
		args = append(args, f.To)
	}
	if f.Before != 0 && f.BeforeID != 0 {
		where = append(where, `(o.started_at<? OR (o.started_at=? AND o.id<?))`)
		args = append(args, f.Before, f.Before, f.BeforeID)
	} else if f.Before != 0 {
		where = append(where, `o.started_at<?`)
		args = append(args, f.Before)
	}

	for _, j := range joins {
		query += " " + j
	}
	for i, w := range where {
		if i == 0 {
			query += " WHERE "
		} else {
			query += " AND "
		}
		query += w
	}
	query += ` ORDER BY o.started_at DESC, o.id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.R.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list outages: %w", err)
	}
	defer rows.Close()

	var out []Outage
	for rows.Next() {
		o, err := scanOutage(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan outage: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// OutageSummary is the Outages tab's headline numbers, all over the
// trailing 30 days from now.
type OutageSummary struct {
	Active  int
	Last30d int
	// NeedsNote counts outages over the last 30 days that have ended
	// without anyone writing down why.
	NeedsNote int

	MTTRSeconds int64

	MostAffectedMonitorID *int64
	MostAffectedCount     int
}

const outageSummaryWindowMs = int64(30) * 24 * 3600 * 1000

// OutageSummary computes the Outages tab summary as of now.
func (s *Store) OutageSummary(ctx context.Context, now int64) (OutageSummary, error) {
	var sum OutageSummary
	cutoff := now - outageSummaryWindowMs

	if err := s.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM outages WHERE ended_at IS NULL`).Scan(&sum.Active); err != nil {
		return sum, fmt.Errorf("store: count active outages: %w", err)
	}

	if err := s.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM outages WHERE started_at>=?`, cutoff).Scan(&sum.Last30d); err != nil {
		return sum, fmt.Errorf("store: count last 30d outages: %w", err)
	}

	if err := s.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM outages
		WHERE started_at>=? AND ended_at IS NOT NULL AND (note IS NULL OR trim(note)='')`, cutoff).Scan(&sum.NeedsNote); err != nil {
		return sum, fmt.Errorf("store: count outages needing a note: %w", err)
	}

	var mttr sql.NullFloat64
	if err := s.R.QueryRowContext(ctx, `
		SELECT AVG(ended_at - started_at) FROM outages
		WHERE started_at>=? AND ended_at IS NOT NULL`, cutoff).Scan(&mttr); err != nil {
		return sum, fmt.Errorf("store: mttr: %w", err)
	}
	if mttr.Valid {
		sum.MTTRSeconds = int64(math.Round(mttr.Float64 / 1000))
	}

	var monitorID sql.NullInt64
	var count int
	err := s.R.QueryRowContext(ctx, `
		SELECT monitor_id, COUNT(*) c FROM outages
		WHERE started_at>=?
		GROUP BY monitor_id ORDER BY c DESC, monitor_id ASC LIMIT 1`, cutoff).Scan(&monitorID, &count)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// no outages in the window: leave MostAffected* zero.
	case err != nil:
		return sum, fmt.Errorf("store: most affected monitor: %w", err)
	default:
		id := monitorID.Int64
		sum.MostAffectedMonitorID = &id
		sum.MostAffectedCount = count
	}

	return sum, nil
}

// RecentRegionChecks returns the latest outages that carry a multi-region
// confirmation, newest first (at most limit).
func (s *Store) RecentRegionChecks(ctx context.Context, limit int) ([]Outage, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT `+outageColumns+` FROM outages WHERE regions IS NOT NULL ORDER BY started_at DESC, id DESC LIMIT ?`, clampLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("store: recent region checks: %w", err)
	}
	defer rows.Close()
	var out []Outage
	for rows.Next() {
		o, err := scanOutage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// OutagesOverlapping returns every outage (any monitor) that was open at
// some point in [from, to): started before to and not ended before from.
// For reports; one indexed range read.
func (s *Store) OutagesOverlapping(ctx context.Context, from, to int64) ([]Outage, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT `+outageColumns+` FROM outages
		WHERE started_at < ? AND (ended_at IS NULL OR ended_at > ?)
		ORDER BY monitor_id, started_at`, to, from)
	if err != nil {
		return nil, fmt.Errorf("store: outages overlapping: %w", err)
	}
	defer rows.Close()
	var out []Outage
	for rows.Next() {
		o, err := scanOutage(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan outage: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// MonitorOutagesOverlapping returns up to limit of one monitor's outages
// that were open at some point in [from, to), oldest first. The status
// page's day view reads it: outages are kept as long as the monitor is, so
// the day view still has its downtime after raw heartbeats are pruned.
func (s *Store) MonitorOutagesOverlapping(ctx context.Context, monitorID, from, to int64, limit int) ([]Outage, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT `+outageColumns+` FROM outages
		WHERE monitor_id = ? AND started_at < ? AND (ended_at IS NULL OR ended_at > ?)
		ORDER BY started_at LIMIT ?`, monitorID, to, from, limit)
	if err != nil {
		return nil, fmt.Errorf("store: monitor outages overlapping: %w", err)
	}
	defer rows.Close()
	var out []Outage
	for rows.Next() {
		o, err := scanOutage(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan outage: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// likeEscape escapes LIKE's wildcards (and the escape character itself) so
// a search for "50%" or "a_b" matches literally. Pair with ESCAPE '\'.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
