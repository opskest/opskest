package api

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"time"

	"opskest/internal/store"
)

// handleSystem serves GET /api/system.
func (s *server) handleSystem(w http.ResponseWriter, r *http.Request) {
	stats := s.d.SchedStats()
	online, since := s.d.Engine.Connectivity()

	backups, _ := store.ListBackups(s.d.Cfg.BackupDir)
	if backups == nil {
		backups = []store.BackupFile{}
	}
	type backupOut struct {
		Name    string `json:"name"`
		Size    int64  `json:"size"`
		ModTime int64  `json:"modTime"`
	}
	bOut := make([]backupOut, 0, len(backups))
	for _, b := range backups {
		bOut = append(bOut, backupOut{Name: b.Name, Size: b.Size, ModTime: b.ModTime.UnixMilli()})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"version":       s.d.Version,
		"startedAt":     s.d.Started.UnixMilli(),
		"monitors":      stats.Monitors,
		"lagP50Ms":      stats.LagP50.Milliseconds(),
		"lagP99Ms":      stats.LagP99.Milliseconds(),
		"inFlight":      stats.InFlight,
		"writerDropped": s.d.Writer.Dropped(),
		"connectivity": map[string]any{
			"online": online,
			"since":  since.UnixMilli(),
		},
		"backups": map[string]any{
			"dir":   s.d.Cfg.BackupDir,
			"files": bOut,
		},
	})
}

// handleMetrics serves GET /metrics in Prometheus text format.
func (s *server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	// If METRICS_TOKEN is set, require it as a Bearer token.
	if s.d.Cfg.MetricsToken != "" {
		authHeader := r.Header.Get("Authorization")
		expected := "Bearer " + s.d.Cfg.MetricsToken
		if subtle.ConstantTimeCompare([]byte(authHeader), []byte(expected)) != 1 {
			if authHeader == "" {
				w.WriteHeader(http.StatusUnauthorized)
			} else {
				w.WriteHeader(http.StatusUnauthorized)
			}
			return
		}
	} else {
		// No metrics token configured → 404.
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}

	sums := s.d.Engine.Summaries()
	stats := s.d.SchedStats()
	groups := s.d.Engine.Groups()
	groupNames := make(map[int64]string)
	for _, g := range groups {
		groupNames[g.ID] = g.Name
	}

	var b strings.Builder
	for _, m := range sums {
		if m.Vendor { // 3rd-party feeds are alert-only: not exported as metrics
			continue
		}
		group := ""
		if m.GroupID != nil {
			group = groupNames[*m.GroupID]
		}
		labels := fmt.Sprintf(`id="%d",name="%s",group="%s",type="%s"`, m.ID, escapeProm(m.Name), escapeProm(group), m.Type)

		var upVal float64
		switch {
		case m.Status == 1 || m.Status == 2: // up or degraded
			upVal = 1
		case m.Status == 4: // down
			upVal = 0
		default:
			upVal = -1
		}
		fmt.Fprintf(&b, "opskest_monitor_up{%s} %g\n", labels, upVal)
		fmt.Fprintf(&b, "opskest_monitor_latency_ms{%s} %d\n", labels, m.LatencyMs)
		if m.Uptime24h != nil {
			fmt.Fprintf(&b, "opskest_monitor_uptime_24h{%s} %g\n", labels, *m.Uptime24h)
		}
		if m.CertDays != nil {
			fmt.Fprintf(&b, "opskest_monitor_cert_days{%s} %d\n", labels, *m.CertDays)
		}
	}

	fmt.Fprintf(&b, "opskest_scheduler_lag_p99_seconds %g\n", stats.LagP99.Seconds())
	fmt.Fprintf(&b, "opskest_writer_dropped_total %d\n", s.d.Writer.Dropped())
	fmt.Fprintf(&b, "opskest_checks_inflight %d\n", stats.InFlight)

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

func escapeProm(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// handleListEvents serves GET /api/events.
func (s *server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	var monitorID *int64
	if mid := queryInt64(r, "monitorId", 0); mid > 0 {
		monitorID = &mid
	}
	before := queryInt64(r, "before", 0)
	limit := clampLimit(queryInt(r, "limit", 50))

	events, err := s.d.Store.ListStatusEvents(r.Context(), monitorID, before, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}

	type eventOut struct {
		ID        int64  `json:"id"`
		MonitorID int64  `json:"monitorId"`
		TS        int64  `json:"ts"`
		From      string `json:"from"`
		To        string `json:"to"`
		Reason    string `json:"reason"`
		ErrClass  string `json:"errClass"`
	}
	out := make([]eventOut, 0, len(events))
	for _, e := range events {
		out = append(out, eventOut{
			ID:        e.ID,
			MonitorID: e.MonitorID,
			TS:        e.TS,
			From:      e.From.String(),
			To:        e.To.String(),
			Reason:    e.Reason,
			ErrClass:  e.ErrClass,
		})
	}

	var nextBefore *int64
	if len(events) == limit {
		ts := events[len(events)-1].TS
		nextBefore = &ts
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"events":     out,
		"nextBefore": nextBefore,
	})
}

// ── Seed the started time ──────────────────────────────────────────

var _ = time.Now // reference
