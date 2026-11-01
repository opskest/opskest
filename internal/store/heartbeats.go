package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"opskest/internal/model"
)

const heartbeatColumns = `monitor_id, ts, status, latency_ms, code, dns_ms, tcp_ms, tls_ms, ttfb_ms, err_class, msg, maint, wait_ms, app_ms, edge, conn`

// scanHeartbeat decodes one heartbeatColumns-ordered row, mapping SQL
// NULL back to the Heartbeat sentinel values (<0 for latencies, 0 for
// Code, "" for the text fields) documented on the Heartbeat type.
func scanHeartbeat(sc scanner) (Heartbeat, error) {
	var hb Heartbeat
	var status int64
	var latency, code, dns, tcpMs, tls, ttfb sql.NullInt64
	var errClass, msg, edge, conn sql.NullString
	var maint int64
	var wait, app sql.NullInt64

	err := sc.Scan(
		&hb.MonitorID, &hb.TS, &status, &latency, &code,
		&dns, &tcpMs, &tls, &ttfb, &errClass, &msg, &maint, &wait, &app, &edge, &conn,
	)
	if err != nil {
		return hb, err
	}

	hb.Status = model.Status(status)
	hb.LatencyMs = nullIntOr(latency, -1)
	hb.Code = nullIntOr(code, 0)
	hb.DNSMs = nullIntOr(dns, -1)
	hb.TCPMs = nullIntOr(tcpMs, -1)
	hb.TLSMs = nullIntOr(tls, -1)
	hb.TTFBMs = nullIntOr(ttfb, -1)
	hb.ErrClass = errClass.String
	hb.Msg = msg.String
	hb.Maint = maint != 0
	hb.WaitMs = nullIntOr(wait, -1)
	hb.AppMs = nullIntOr(app, -1)
	hb.Edge = edge.String
	hb.Conn = conn.String
	return hb, nil
}

func nullIntOr(n sql.NullInt64, def int) int {
	if !n.Valid {
		return def
	}
	return int(n.Int64)
}

// defaultListLimit and maxListLimit match the global convention (list
// endpoints default to 50, max 200).
const (
	defaultListLimit = 50
	maxListLimit     = 200
)

// clampLimit maps a caller-supplied limit to the 1..maxListLimit range,
// defaulting a non-positive value to defaultListLimit.
func clampLimit(n int) int {
	if n <= 0 {
		return defaultListLimit
	}
	if n > maxListLimit {
		return maxListLimit
	}
	return n
}

// LastBeats returns monitorID's last n heartbeats (clamped to
// 1..maxListLimit, default 50), oldest first (newest last), via one
// primary-key range read.
func (s *Store) LastBeats(ctx context.Context, monitorID int64, n int) ([]Heartbeat, error) {
	n = clampLimit(n)
	rows, err := s.R.QueryContext(ctx, `
		SELECT `+heartbeatColumns+` FROM (
			SELECT `+heartbeatColumns+` FROM heartbeats WHERE monitor_id=? ORDER BY ts DESC LIMIT ?
		) ORDER BY ts ASC`, monitorID, n)
	if err != nil {
		return nil, fmt.Errorf("store: last beats: %w", err)
	}
	defer rows.Close()

	var out []Heartbeat
	for rows.Next() {
		hb, err := scanHeartbeat(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan heartbeat: %w", err)
		}
		out = append(out, hb)
	}
	return out, rows.Err()
}

// BeatsPage returns up to limit (clamped to 1..maxListLimit, default 50)
// heartbeats for monitorID older than beforeTS, newest first. beforeTS=0
// means "as of now".
func (s *Store) BeatsPage(ctx context.Context, monitorID int64, beforeTS int64, limit int) ([]Heartbeat, error) {
	if beforeTS == 0 {
		beforeTS = time.Now().UnixMilli() + 1
	}
	limit = clampLimit(limit)

	rows, err := s.R.QueryContext(ctx, `
		SELECT `+heartbeatColumns+` FROM heartbeats
		WHERE monitor_id=? AND ts<?
		ORDER BY ts DESC LIMIT ?`, monitorID, beforeTS, limit)
	if err != nil {
		return nil, fmt.Errorf("store: beats page: %w", err)
	}
	defer rows.Close()

	var out []Heartbeat
	for rows.Next() {
		hb, err := scanHeartbeat(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan heartbeat: %w", err)
		}
		out = append(out, hb)
	}
	return out, rows.Err()
}

// SeriesPoint is one bucketed point of a raw-heartbeat chart.
type SeriesPoint struct {
	TS              int64
	Avg, Min, Max   int
	Up, Down, Other int
}

// RawSeries buckets monitorID's heartbeats in [from, to] into at most
// maxPoints buckets, computed and aggregated server-side, so the caller
// never has to pull raw rows to downsample client-side. Buckets are
// anchored to `from` (bucket = from + ((ts-from)/b)*b), not to epoch 0:
// bucketing relative to epoch 0 means an unaligned `from` shifts every
// bucket boundary away from a clean multiple of the bucket size, which
// can hand back maxPoints+1 distinct buckets instead of maxPoints (most
// visibly when a beat lands exactly on `to`). b itself is the ceiling of
// span/maxPoints plus one extra millisecond, so that even a beat exactly
// at `to` (offset == maxPoints*ceil(span/maxPoints)) still floors into
// bucket maxPoints-1 rather than spilling into a maxPoints-th bucket.
// Maintenance beats are excluded from the latency aggregates (Avg/Min/
// Max) and from the Up/Down counts - they fall into Other instead - since
// a beat under maintenance carries no meaningful latency or status.
func (s *Store) RawSeries(ctx context.Context, monitorID int64, from, to int64, maxPoints int) ([]SeriesPoint, error) {
	if maxPoints < 1 {
		maxPoints = 1
	}
	span := to - from
	if span < 1 {
		span = 1
	}
	bucketMs := (span+int64(maxPoints)-1)/int64(maxPoints) + 1

	rows, err := s.R.QueryContext(ctx, `
		SELECT ? + ((ts - ?) / ?) * ? AS bucket,
			AVG(CASE WHEN maint=0 THEN latency_ms END),
			MIN(CASE WHEN maint=0 THEN latency_ms END),
			MAX(CASE WHEN maint=0 THEN latency_ms END),
			SUM(CASE WHEN maint=0 AND status=? THEN 1 ELSE 0 END),
			SUM(CASE WHEN maint=0 AND status=? THEN 1 ELSE 0 END),
			COUNT(*)
		FROM heartbeats
		WHERE monitor_id=? AND ts>=? AND ts<=?
		GROUP BY bucket
		ORDER BY bucket`,
		from, from, bucketMs, bucketMs, int(model.StatusUp), int(model.StatusDown), monitorID, from, to)
	if err != nil {
		return nil, fmt.Errorf("store: raw series: %w", err)
	}
	defer rows.Close()

	var out []SeriesPoint
	for rows.Next() {
		var sp SeriesPoint
		var avg sql.NullFloat64
		var min, max sql.NullInt64
		var total int
		if err := rows.Scan(&sp.TS, &avg, &min, &max, &sp.Up, &sp.Down, &total); err != nil {
			return nil, fmt.Errorf("store: scan series point: %w", err)
		}
		if avg.Valid {
			sp.Avg = int(avg.Float64)
		}
		if min.Valid {
			sp.Min = int(min.Int64)
		}
		if max.Valid {
			sp.Max = int(max.Int64)
		}
		sp.Other = total - sp.Up - sp.Down
		out = append(out, sp)
	}
	return out, rows.Err()
}

// BeatsOfDay returns up to 500 non-UP heartbeats for monitorID in
// [dayStart, dayEnd).
func (s *Store) BeatsOfDay(ctx context.Context, monitorID int64, dayStart, dayEnd int64) ([]Heartbeat, error) {
	rows, err := s.R.QueryContext(ctx, `
		SELECT `+heartbeatColumns+` FROM heartbeats
		WHERE monitor_id=? AND ts>=? AND ts<? AND status<>?
		ORDER BY ts LIMIT 500`, monitorID, dayStart, dayEnd, int(model.StatusUp))
	if err != nil {
		return nil, fmt.Errorf("store: beats of day: %w", err)
	}
	defer rows.Close()

	var out []Heartbeat
	for rows.Next() {
		hb, err := scanHeartbeat(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan heartbeat: %w", err)
		}
		out = append(out, hb)
	}
	return out, rows.Err()
}

// TsLatency is one measured latency sample.
type TsLatency struct {
	TS int64
	Ms int
}

// MaxRawLatencies caps LatenciesBetween: a day at the 10 s minimum
// interval is 8,640 samples, so this is never hit by a 24h window.
const MaxRawLatencies = 20_000

// LatenciesBetween returns monitorID's measured latencies in [from, to],
// oldest first: maintenance beats and failed checks (NULL latency) are
// excluded, matching what the rollups count.
func (s *Store) LatenciesBetween(ctx context.Context, monitorID, from, to int64) ([]TsLatency, error) {
	rows, err := s.R.QueryContext(ctx, `
		SELECT ts, latency_ms FROM heartbeats
		WHERE monitor_id=? AND ts>=? AND ts<=? AND maint=0 AND latency_ms IS NOT NULL
		ORDER BY ts LIMIT ?`, monitorID, from, to, MaxRawLatencies)
	if err != nil {
		return nil, fmt.Errorf("store: latencies: %w", err)
	}
	defer rows.Close()
	var out []TsLatency
	for rows.Next() {
		var v TsLatency
		if err := rows.Scan(&v.TS, &v.Ms); err != nil {
			return nil, fmt.Errorf("store: scan latency: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
