package api

import (
	"net/http"
	"time"

	"opskest/internal/store"
)

// ── Beats ──────────────────────────────────────────────────────────

func (s *server) handleBeats(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	limit := clampLimit(queryInt(r, "limit", 50))
	before := queryInt64(r, "before", 0)

	beats, err := s.d.Store.BeatsPage(r.Context(), id, before, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	if beats == nil {
		beats = []store.Heartbeat{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"beats": beats})
}

// ── Series ─────────────────────────────────────────────────────────

func (s *server) handleSeries(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	rangeParam := r.URL.Query().Get("range")
	now := s.d.Now()

	switch rangeParam {
	case "1h":
		from := now.Add(-1 * time.Hour).UnixMilli()
		to := now.UnixMilli()
		pts, err := s.d.Store.RawSeries(r.Context(), id, from, to, 600)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"series": pts})

	case "24h", "":
		from := now.Add(-24 * time.Hour).UnixMilli()
		to := now.UnixMilli()
		pts, err := s.d.Store.RawSeries(r.Context(), id, from, to, 600)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"series": pts})

	case "7d":
		from := now.Add(-7 * 24 * time.Hour).UnixMilli()
		to := now.UnixMilli()
		rollups, err := s.d.Store.RollupSeries(r.Context(), "rollup_hourly", id, from, to)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"series": rollups})

	case "30d":
		from := now.Add(-30 * 24 * time.Hour).UnixMilli()
		to := now.UnixMilli()
		rollups, err := s.d.Store.RollupSeries(r.Context(), "rollup_hourly", id, from, to)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"series": rollups})

	case "90d":
		from := now.Add(-90 * 24 * time.Hour).UnixMilli()
		to := now.UnixMilli()
		rollups, err := s.d.Store.RollupSeries(r.Context(), "rollup_daily", id, from, to)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"series": rollups})

	default:
		writeErr(w, http.StatusBadRequest, "invalid range: use 1h, 24h, 7d, 30d or 90d", nil)
	}
}

// ── Daily ──────────────────────────────────────────────────────────

func (s *server) handleDaily(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	days := queryInt(r, "days", 90)
	if days < 1 {
		days = 1
	}
	if days > 365 {
		days = 365
	}

	now := s.d.Now()
	from := now.Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	to := now.UnixMilli()

	rollups, err := s.d.Store.RollupSeries(r.Context(), "rollup_daily", id, from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}

	type dayEntry struct {
		Day     int64    `json:"day"`
		Uptime  *float64 `json:"uptime"`
		Down    int      `json:"down"`
		Outages int      `json:"outages"`
	}
	// Outages that started on each day (bounded: at most 200 in the range).
	outagesPerDay := map[int64]int{}
	if outs, err := s.d.Store.ListOutages(r.Context(), store.OutageFilter{MonitorID: &id, From: from, To: to, Limit: 200}); err == nil {
		for _, o := range outs {
			outagesPerDay[s.d.Store.DayBucket(o.StartedAt)]++
		}
	}
	out := make([]dayEntry, 0, len(rollups))
	for _, ru := range rollups {
		var uptime *float64
		if v, ok := ru.Uptime(); ok {
			uptime = &v
		}
		out = append(out, dayEntry{
			Day:     ru.Bucket,
			Uptime:  uptime,
			Down:    ru.Down,
			Outages: outagesPerDay[ru.Bucket],
		})
	}

	writeJSON(w, http.StatusOK, out)
}

// ── Monitor outages ────────────────────────────────────────────────

func (s *server) handleMonitorOutages(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	limit := clampLimit(queryInt(r, "limit", 50))
	before := queryInt64(r, "before", 0)

	outages, err := s.d.Store.ListOutages(r.Context(), store.OutageFilter{
		MonitorID: &id,
		Before:    before,
		BeforeID:  queryInt64(r, "beforeId", 0),
		Limit:     limit,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	if outages == nil {
		outages = []store.Outage{}
	}
	nextBefore, nextBeforeID := outageCursor(outages, limit)
	writeJSON(w, http.StatusOK, map[string]any{"outages": outages, "nextBefore": nextBefore, "nextBeforeId": nextBeforeID})
}
