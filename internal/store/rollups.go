package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"opskest/internal/latency"
)

// Rollup is one (monitor, bucket) row from rollup_hourly or rollup_daily.
// LatMin/LatMax are meaningless (and left 0) when LatCount is 0, mirroring
// the NULL the column holds until a latency sample lands in that bucket.
type Rollup struct {
	MonitorID int64
	Bucket    int64

	Up, Degraded, Pending, Down, Unknown, Maint int
	LatSum, LatCount                            int64
	LatMin, LatMax                              int
	// LatHist is the encoded latency histogram (nil before migration
	// 0011, or when no sample landed). Kept encoded so callers that only
	// need counters — the engine's boot seeding — never pay to decode it.
	LatHist []byte `json:"-"`
}

// Histogram decodes LatHist. A NULL or corrupt blob is an empty histogram.
func (r Rollup) Histogram() latency.Hist {
	return latency.Decode(r.LatHist)
}

// Uptime returns the uptime percentage (0-100) for the rollup: good
// (Up+Degraded+Pending) over good+bad (Down). Unknown and Maint samples
// are excluded from both sides. ok is false when there is no good-or-bad
// sample to compute a percentage from (ruling R6).
func (r Rollup) Uptime() (pct float64, ok bool) {
	good := r.Up + r.Degraded + r.Pending
	bad := r.Down
	total := good + bad
	if total == 0 {
		return 0, false
	}
	return float64(good) / float64(total) * 100, true
}

// HourBucket returns the UTC hour start (unix ms) containing tsMs.
func HourBucket(tsMs int64) int64 {
	t := time.UnixMilli(tsMs).UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, time.UTC).UnixMilli()
}

// DayBucket returns local midnight (unix ms), in s.Loc (DISPLAY_TZ),
// containing tsMs.
func (s *Store) DayBucket(tsMs int64) int64 {
	t := time.UnixMilli(tsMs).In(s.Loc)
	start, _ := CalendarDayStart(t.Year(), t.Month(), t.Day(), s.Loc)
	return start.UnixMilli()
}

// rollupTableName rejects anything but the two rollup tables: table is
// interpolated directly into SQL text (SQLite has no way to parameterize
// an identifier), so every caller must go through this check first.
func rollupTableName(table string) (string, error) {
	switch table {
	case "rollup_hourly", "rollup_daily":
		return table, nil
	default:
		return "", fmt.Errorf("store: invalid rollup table %q", table)
	}
}

const rollupColumns = `monitor_id, bucket, n_up, n_degraded, n_pending, n_down, n_unknown, n_maint, lat_sum, lat_count, lat_min, lat_max, lat_hist`

func scanRollup(sc scanner) (Rollup, error) {
	var r Rollup
	var latMin, latMax sql.NullInt64
	err := sc.Scan(
		&r.MonitorID, &r.Bucket, &r.Up, &r.Degraded, &r.Pending, &r.Down, &r.Unknown, &r.Maint,
		&r.LatSum, &r.LatCount, &latMin, &latMax, &r.LatHist,
	)
	if err != nil {
		return r, err
	}
	if latMin.Valid {
		r.LatMin = int(latMin.Int64)
	}
	if latMax.Valid {
		r.LatMax = int(latMax.Int64)
	}
	return r, nil
}

// RollupsSince returns every rollup at or after since, across all
// monitors, grouped by monitor id, in one query.
func (s *Store) RollupsSince(ctx context.Context, table string, since int64) (map[int64][]Rollup, error) {
	tbl, err := rollupTableName(table)
	if err != nil {
		return nil, err
	}

	rows, err := s.R.QueryContext(ctx, `SELECT `+rollupColumns+` FROM `+tbl+` WHERE bucket>=? ORDER BY monitor_id, bucket`, since)
	if err != nil {
		return nil, fmt.Errorf("store: rollups since: %w", err)
	}
	defer rows.Close()

	out := map[int64][]Rollup{}
	for rows.Next() {
		r, err := scanRollup(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan rollup: %w", err)
		}
		out[r.MonitorID] = append(out[r.MonitorID], r)
	}
	return out, rows.Err()
}

// RollupSeries returns monitorID's rollups in [from, to], ordered by
// bucket.
func (s *Store) RollupSeries(ctx context.Context, table string, monitorID, from, to int64) ([]Rollup, error) {
	tbl, err := rollupTableName(table)
	if err != nil {
		return nil, err
	}

	rows, err := s.R.QueryContext(ctx, `SELECT `+rollupColumns+` FROM `+tbl+`
		WHERE monitor_id=? AND bucket>=? AND bucket<=? ORDER BY bucket`, monitorID, from, to)
	if err != nil {
		return nil, fmt.Errorf("store: rollup series: %w", err)
	}
	defer rows.Close()

	var out []Rollup
	for rows.Next() {
		r, err := scanRollup(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan rollup: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RollupHists returns, per monitor, one Rollup merging every bucket of
// table in [from, to]: summed counters, overall min/max and the merged
// histogram. Bucket is from. One query, for fleet-wide percentile tables.
func (s *Store) RollupHists(ctx context.Context, table string, from, to int64) (map[int64]Rollup, error) {
	tbl, err := rollupTableName(table)
	if err != nil {
		return nil, err
	}
	rows, err := s.R.QueryContext(ctx, `SELECT `+rollupColumns+` FROM `+tbl+`
		WHERE bucket>=? AND bucket<=?`, from, to)
	if err != nil {
		return nil, fmt.Errorf("store: rollup hists: %w", err)
	}
	defer rows.Close()

	out := map[int64]Rollup{}
	hists := map[int64]latency.Hist{}
	for rows.Next() {
		r, err := scanRollup(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan rollup: %w", err)
		}
		acc, seen := out[r.MonitorID]
		if !seen {
			acc = Rollup{MonitorID: r.MonitorID, Bucket: from}
		}
		acc.Up += r.Up
		acc.Degraded += r.Degraded
		acc.Pending += r.Pending
		acc.Down += r.Down
		acc.Unknown += r.Unknown
		acc.Maint += r.Maint
		if r.LatCount > 0 {
			if acc.LatCount == 0 || r.LatMin < acc.LatMin {
				acc.LatMin = r.LatMin
			}
			if acc.LatCount == 0 || r.LatMax > acc.LatMax {
				acc.LatMax = r.LatMax
			}
		}
		acc.LatSum += r.LatSum
		acc.LatCount += r.LatCount
		out[r.MonitorID] = acc
		if len(r.LatHist) > 0 {
			h := hists[r.MonitorID]
			if h == nil {
				h = latency.Hist{}
				hists[r.MonitorID] = h
			}
			h.Merge(r.Histogram())
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for id, h := range hists {
		acc := out[id]
		acc.LatHist = h.Encode()
		out[id] = acc
	}
	return out, nil
}
