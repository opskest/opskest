package api

import (
	"net/http"
	"strings"
	"time"

	"opskest/internal/auth"
	"opskest/internal/check"
	"opskest/internal/model"
	"opskest/internal/regions"
	"opskest/internal/store"
)

func (s *server) regionConfig(r *http.Request) (regions.Config, store.Settings, bool) {
	st, err := s.d.Store.GetSettings(r.Context())
	if err != nil {
		return regions.Config{}, st, false
	}
	token, _ := s.d.Store.GlobalpingToken(r.Context())
	return regions.Config{
		Enabled: st.RegionConfirm == 1, Sweep: st.RegionSweep == 1, Alerts: st.RegionAlerts == 1,
		Countries: st.RegionCountries, TestsPerHour: st.RegionTestsPerHour, Token: token,
	}, st, true
}

// GET /api/regions — Settings → Multi-region overview.
func (s *server) handleRegions(w http.ResponseWriter, r *http.Request) {
	cfg, _, ok := s.regionConfig(r)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	out := map[string]any{
		"enabled":      cfg.Enabled,
		"sweep":        cfg.Sweep,
		"alerts":       cfg.Alerts,
		"countries":    cfg.Countries,
		"testsPerHour": cfg.TestsPerHour,
		"tokenSet":     cfg.Token != "",
		"usedHour":     0,
		"eligible":     0,
		"cycleS":       0,
		"recent":       []any{},
	}
	if l, ok := check.GlobalpingLimit(); ok {
		out["limit"] = map[string]any{"limit": l.Limit, "remaining": l.Remaining, "resetInS": int64(max(0, time.Until(l.ResetAt).Seconds()))}
	}
	if c := s.d.Regions; c != nil {
		out["usedHour"] = c.Usage()
		out["eligible"] = c.Eligible(cfg)
		out["cycleS"] = int64(c.Cycle(cfg).Seconds())
	}
	outs, _ := s.d.Store.RecentRegionChecks(r.Context(), 20)
	type recent struct {
		OutageID    int64              `json:"outageId"`
		MonitorID   int64              `json:"monitorId"`
		MonitorName string             `json:"monitorName"`
		StartedAt   int64              `json:"startedAt"`
		EndedAt     *int64             `json:"endedAt"`
		Regions     *store.RegionCheck `json:"regions"`
	}
	list := make([]recent, 0, len(outs))
	for _, o := range outs {
		name := ""
		if m, ok := s.d.Engine.Monitor(o.MonitorID); ok {
			name = m.Name
		}
		list = append(list, recent{OutageID: o.ID, MonitorID: o.MonitorID, MonitorName: name, StartedAt: o.StartedAt, EndedAt: o.EndedAt, Regions: o.Regions})
	}
	out["recent"] = list
	writeJSON(w, http.StatusOK, out)
}

// PUT /api/regions/token {token} — save or clear ("") the Globalping API
// token. It is stored encrypted and never returned.
func (s *server) handleRegionToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	in.Token = strings.TrimSpace(in.Token)
	if len(in.Token) > 200 {
		writeErr(w, http.StatusBadRequest, "token too long", nil)
		return
	}
	if err := s.d.Store.SetGlobalpingToken(r.Context(), in.Token); err != nil {
		writeErr(w, http.StatusInternalServerError, "save failed", nil)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "update", "globalping_token", nil, map[bool]string{true: "set", false: "cleared"}[in.Token != ""])
	writeJSON(w, http.StatusOK, map[string]bool{"tokenSet": in.Token != ""})
}

// POST /api/regions/check {url} — check any public URL from the
// configured countries now.
func (s *server) handleRegionCheck(w http.ResponseWriter, r *http.Request) {
	if s.d.Regions == nil {
		writeErr(w, http.StatusServiceUnavailable, "multi-region checks are not available", nil)
		return
	}
	var in struct {
		MonitorID int64  `json:"monitorId"`
		URL       string `json:"url"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	typ, target := model.TypeHTTP, strings.TrimSpace(in.URL)
	if in.MonitorID > 0 {
		m, ok := s.d.Engine.Monitor(in.MonitorID)
		if !ok {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		typ, target = m.Type, m.Target
	} else if target != "" && !strings.Contains(target, "://") {
		target = "https://" + target
	}
	if target == "" {
		writeErr(w, http.StatusBadRequest, "pick a monitor or enter a URL", map[string]string{"url": "required"})
		return
	}
	rc, ok := s.d.Regions.CheckNow(r.Context(), typ, target)
	if !ok {
		writeErr(w, http.StatusBadRequest, "only HTTP, ping and DNS targets on public hostnames can be checked from other countries", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"target": target, "type": typ, "result": rc})
}

// GET /api/monitors/{id}/regions — a monitor's worldwide results.
func (s *server) handleMonitorRegions(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	m, ok := s.d.Engine.Monitor(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	if s.d.Regions == nil {
		writeJSON(w, http.StatusOK, regions.Status{Reason: "multi-region checks are not available", Results: []store.RegionResult{}, Countries: []string{}})
		return
	}
	writeJSON(w, http.StatusOK, s.d.Regions.MonitorStatus(m))
}

// POST /api/monitors/{id}/regions/check — check this monitor worldwide now.
func (s *server) handleMonitorRegionCheck(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil || s.d.Regions == nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	if _, ok := s.d.Engine.Monitor(id); !ok {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	_, reason, err := s.d.Regions.CheckMonitor(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	if reason != "" {
		writeErr(w, http.StatusBadRequest, reason, nil)
		return
	}
	m, _ := s.d.Engine.Monitor(id)
	writeJSON(w, http.StatusOK, s.d.Regions.MonitorStatus(m))
}

// GET /api/regions/issues — monitors currently failing from some
// countries (for the Overview and the dashboard).
func (s *server) handleRegionIssues(w http.ResponseWriter, r *http.Request) {
	type issue struct {
		MonitorID int64                `json:"monitorId"`
		Name      string               `json:"name"`
		Summary   string               `json:"summary"`
		Failing   []store.RegionResult `json:"failing"`
	}
	out := []issue{}
	if s.d.Regions != nil {
		for id, failing := range s.d.Regions.Issues() {
			m, ok := s.d.Engine.Monitor(id)
			if !ok || m.Paused {
				continue
			}
			st := s.d.Regions.MonitorStatus(m)
			out = append(out, issue{MonitorID: id, Name: m.Name, Summary: regions.FailingSummary(st.Results), Failing: failing})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"issues": out})
}
