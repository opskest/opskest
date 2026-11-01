package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"opskest/internal/hub"
	"opskest/internal/model"
	"opskest/internal/store"
)

// summary.json follows the shape of Atlassian Statuspage's /api/v2/summary.json
// (page, status, components, incidents, scheduled_maintenances) so that
// existing widgets and scripts can read an Opskest page. Components carry
// only what the public page already shows: the public display name, a status
// word and the group the page puts them in. No target, reason, runbook, tag,
// check setting or internal incident update can appear: every value is built
// here from the sanitized types, never from a monitor or incident row.

type summaryPage struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	TimeZone  string `json:"time_zone"`
	UpdatedAt string `json:"updated_at"`
}

type summaryStatus struct {
	Indicator   string `json:"indicator"` // none, minor, major, critical, maintenance
	Description string `json:"description"`
}

type summaryComponent struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	Description *string `json:"description"`
	GroupID     *string `json:"group_id"`
	UpdatedAt   string  `json:"updated_at"`
	Position    int     `json:"position"`
}

type summaryIncident struct {
	ID              string                  `json:"id"`
	Name            string                  `json:"name"`
	Status          string                  `json:"status"`
	Impact          string                  `json:"impact"`
	CreatedAt       string                  `json:"created_at"`
	UpdatedAt       string                  `json:"updated_at"`
	ResolvedAt      *string                 `json:"resolved_at"`
	Shortlink       string                  `json:"shortlink"`
	IncidentUpdates []summaryIncidentUpdate `json:"incident_updates"`
}

type summaryIncidentUpdate struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

type summaryMaintenance struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Status         string  `json:"status"` // in_progress, scheduled
	Description    string  `json:"description"`
	ScheduledFor   *string `json:"scheduled_for"`
	ScheduledUntil *string `json:"scheduled_until"`
}

var componentStatus = map[model.Status]string{
	model.StatusUp:          "operational",
	model.StatusDegraded:    "degraded_performance",
	model.StatusPending:     "degraded_performance",
	model.StatusDown:        "major_outage",
	model.StatusMaintenance: "under_maintenance",
}

func summaryComponentStatus(st model.Status) string {
	if v, ok := componentStatus[st]; ok {
		return v
	}
	// Paused and never-checked services are not confirmed healthy, and
	// reporting them as operational would say more than we know.
	return "unknown"
}

var overallIndicator = map[string]summaryStatus{
	"operational": {"none", "All Systems Operational"},
	"degraded":    {"minor", "Partially Degraded Service"},
	"partial":     {"major", "Partial System Outage"},
	"major":       {"critical", "Major System Outage"},
	"maintenance": {"maintenance", "Service Under Maintenance"},
}

var overallRank = map[string]int{"operational": 0, "maintenance": 1, "degraded": 2, "partial": 3, "major": 4}

// raiseForIncidents mirrors the status page: an open incident declares at
// least its own level, even when every check happens to pass.
func raiseForIncidents(overall string, active []store.Incident) string {
	level := map[string]string{"minor": "degraded", "major": "partial", "maintenance": "maintenance"}
	for _, inc := range active {
		l, ok := level[inc.Impact]
		if !ok {
			l = "degraded"
		}
		if overallRank[l] > overallRank[overall] {
			overall = l
		}
	}
	return overall
}

func rfc3339(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339) }

// handlePublicSummary serves GET /api/public/pages/{slug}/summary.json.
func (s *server) handlePublicSummary(w http.ResponseWriter, r *http.Request) {
	page, ok := s.resolvePublicPage(w, r)
	if !ok {
		return
	}
	isPublic := page.Visibility == "public"
	monitorIDs := s.pageMonitorIDs(page)
	now := s.d.Now()

	monitors := []hub.PublicMonitor{}
	for _, sum := range s.d.Engine.Summaries() {
		if name, on := monitorIDs[sum.ID]; on {
			monitors = append(monitors, s.sanitizeFor(page, sum, name, isPublic, false))
		}
	}

	// The page's own group per component, in page order.
	groupOf := map[int64]string{}
	descriptions := map[int64]string{}
	for _, item := range page.Items {
		if item.MonitorID != nil && item.Description != "" {
			descriptions[*item.MonitorID] = item.Description
		}
		if item.GroupID == nil {
			continue
		}
		for _, id := range s.d.Engine.MonitorIDsInGroup(*item.GroupID) {
			if _, on := monitorIDs[id]; on {
				if _, set := groupOf[id]; !set {
					groupOf[id] = strconv.FormatInt(*item.GroupID, 10)
				}
			}
		}
	}

	incidents, err := s.d.Store.IncidentsForPage(r.Context(), page.ID, now.Add(-14*24*time.Hour).UnixMilli())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	var open []store.Incident
	for _, inc := range s.visibleIncidents(page, incidents) {
		if inc.ResolvedAt == nil {
			open = append(open, inc)
		}
	}
	activeMaint, upcomingMaint, err := s.pageMaintenanceFor(r, page, monitorIDs, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "maintenance unavailable", nil)
		return
	}

	updated := page.UpdatedAt
	comps := make([]summaryComponent, 0, len(monitors))
	for i, m := range monitors {
		c := summaryComponent{ID: strconv.FormatInt(m.ID, 10), Name: m.Name, Status: summaryComponentStatus(m.Status), Position: i + 1}
		if d, ok := descriptions[m.ID]; ok {
			c.Description = &d
		}
		if g, ok := groupOf[m.ID]; ok {
			c.GroupID = &g
		}
		if m.Since > updated {
			updated = m.Since
		}
		c.UpdatedAt = rfc3339(max(m.Since, page.UpdatedAt))
		comps = append(comps, c)
	}

	base := s.pageBase(page)
	pub := toPublicIncidents(open)
	outIncidents := make([]summaryIncident, 0, len(pub))
	for _, inc := range pub {
		si := summaryIncident{
			ID: strconv.FormatInt(inc.ID, 10), Name: inc.Title, Status: inc.Status, Impact: inc.Impact,
			CreatedAt: rfc3339(inc.CreatedAt), UpdatedAt: rfc3339(inc.UpdatedAt),
			Shortlink:       base + "/status/" + page.Slug + "/incidents/" + strconv.FormatInt(inc.ID, 10),
			IncidentUpdates: []summaryIncidentUpdate{},
		}
		for _, u := range inc.Updates {
			si.IncidentUpdates = append(si.IncidentUpdates, summaryIncidentUpdate{ID: strconv.FormatInt(u.ID, 10), Status: u.Status, Body: u.Body, CreatedAt: rfc3339(u.CreatedAt)})
		}
		if inc.UpdatedAt > updated {
			updated = inc.UpdatedAt
		}
		outIncidents = append(outIncidents, si)
	}

	maint := []summaryMaintenance{}
	add := func(list []publicMaintenance, status string) {
		for _, m := range list {
			sm := summaryMaintenance{ID: strconv.FormatInt(m.ID, 10), Name: m.Title, Status: status, Description: m.Description}
			start, end := m.NextStart, m.CurrentEnd
			if start == nil {
				start = m.StartsAt
			}
			if end == nil {
				end = m.EndsAt
			}
			if start != nil {
				v := rfc3339(*start)
				sm.ScheduledFor = &v
			}
			if end != nil {
				v := rfc3339(*end)
				sm.ScheduledUntil = &v
			}
			maint = append(maint, sm)
		}
	}
	add(activeMaint, "in_progress")
	add(upcomingMaint, "scheduled")

	overall := raiseForIncidents(computeOverall(monitors), open)
	body, err := json.MarshalIndent(map[string]any{
		"page": summaryPage{ID: page.Slug, Name: page.Title, URL: base + "/status/" + page.Slug,
			TimeZone: s.d.Store.Loc.String(), UpdatedAt: rfc3339(updated)},
		"status":                 overallIndicator[overall],
		"components":             comps,
		"incidents":              outIncidents,
		"scheduled_maintenances": maint,
	}, "", "  ")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "summary failed", nil)
		return
	}
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("ETag", etag)
	h.Set("X-Content-Type-Options", "nosniff")
	if isPublic {
		h.Set("Cache-Control", "public, max-age=30")
		// A widget on another site may read it; no credentials are involved.
		h.Set("Access-Control-Allow-Origin", "*")
	} else {
		h.Set("Cache-Control", "private, no-store")
	}
	if strings.Contains(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
