package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
)

// RegionResult is one country's latest worldwide check of a monitor.
type RegionResult struct {
	MonitorID int64  `json:"monitorId"`
	Country   string `json:"country"`
	City      string `json:"city"`
	OK        bool   `json:"ok"`
	Code      int    `json:"code"`
	LatencyMs int    `json:"latencyMs"`
	Detail    string `json:"detail"`
	CheckedAt int64  `json:"checkedAt"`
}

// OpReplaceRegionResults replaces a monitor's worldwide results, for use
// with Writer.DoFor.
func OpReplaceRegionResults(monitorID int64, rs []RegionResult) Op {
	return func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM region_results WHERE monitor_id=?`, monitorID); err != nil {
			return err
		}
		for _, r := range rs {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO region_results(monitor_id, country, city, ok, code, latency_ms, detail, checked_at) VALUES (?,?,?,?,?,?,?,?)`,
				monitorID, r.Country, r.City, boolToInt(r.OK), zeroOrNil(r.Code), negOrNil(r.LatencyMs), r.Detail, r.CheckedAt); err != nil {
				return err
			}
		}
		return nil
	}
}

// RegionResults returns every stored worldwide result, by monitor.
func (s *Store) RegionResults(ctx context.Context) (map[int64][]RegionResult, error) {
	rows, err := s.R.QueryContext(ctx, `SELECT monitor_id, country, city, ok, code, latency_ms, detail, checked_at FROM region_results ORDER BY monitor_id, country`)
	if err != nil {
		return nil, fmt.Errorf("store: region results: %w", err)
	}
	defer rows.Close()
	out := map[int64][]RegionResult{}
	for rows.Next() {
		var r RegionResult
		var ok int64
		var code, lat sql.NullInt64
		if err := rows.Scan(&r.MonitorID, &r.Country, &r.City, &ok, &code, &lat, &r.Detail, &r.CheckedAt); err != nil {
			return nil, err
		}
		r.OK, r.Code, r.LatencyMs = ok != 0, nullIntOr(code, 0), nullIntOr(lat, -1)
		out[r.MonitorID] = append(out[r.MonitorID], r)
	}
	return out, rows.Err()
}

const keyGlobalpingToken = "globalping_token"

// GlobalpingToken returns the saved Globalping API token ("" when none).
// It is stored encrypted, like channel secrets.
func (s *Store) GlobalpingToken(ctx context.Context) (string, error) {
	var v string
	err := s.R.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, keyGlobalpingToken).Scan(&v)
	if err == sql.ErrNoRows || v == "" {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return "", err
	}
	plain, err := s.Box.Open(raw)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// SetGlobalpingToken saves (or, with "", clears) the Globalping token.
func (s *Store) SetGlobalpingToken(ctx context.Context, token string) error {
	if token == "" {
		_, err := s.W.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, keyGlobalpingToken)
		return err
	}
	sealed, err := s.Box.Seal([]byte(token))
	if err != nil {
		return err
	}
	_, err = s.W.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		keyGlobalpingToken, base64.StdEncoding.EncodeToString(sealed))
	return err
}
