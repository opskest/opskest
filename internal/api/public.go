package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"opskest/internal/auth"
	"opskest/internal/engine"
	"opskest/internal/hub"
	"opskest/internal/model"
	"opskest/internal/store"
)

// resolvePublicPage is the common prelude of every /api/public/pages/{slug}
// endpoint: unknown slug and private-without-session both 404 (never 403),
// and a public page is rate-limited per IP. ok=false means the response
// has already been written.
func (s *server) resolvePublicPage(w http.ResponseWriter, r *http.Request) (*store.Page, bool) {
	page, err := s.d.Store.GetPageBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return nil, false
	}
	if page.Visibility == "private" {
		if _, ok := auth.UserFrom(r.Context()); !ok {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return nil, false
		}
	}
	if page.Visibility == "public" {
		if !s.publicLimit.allow(clientIP(r), s.d.Now()) {
			w.WriteHeader(http.StatusTooManyRequests)
			return nil, false
		}
	}
	return page, true
}

// pageMonitorIDs expands a page's items to monitor id -> display name
// override ("" = use the monitor's own name). Groups expand to their
// current members.
func (s *server) pageMonitorIDs(page *store.Page) map[int64]string {
	ids := map[int64]string{}
	for _, item := range page.Items {
		if item.GroupID != nil {
			for _, id := range s.d.Engine.MonitorIDsInGroup(*item.GroupID) {
				if _, set := ids[id]; !set {
					ids[id] = ""
				}
			}
		}
	}
	// A monitor listed on its own keeps its own display name even if a
	// group on the page also contains it.
	for _, item := range page.Items {
		if item.MonitorID != nil && !s.d.Engine.IsVendor(*item.MonitorID) {
			ids[*item.MonitorID] = item.DisplayName
		}
	}
	return ids
}

// handlePublicPage serves GET /api/public/pages/{slug}.
func (s *server) handlePublicPage(w http.ResponseWriter, r *http.Request) {
	page, ok := s.resolvePublicPage(w, r)
	if !ok {
		return
	}
	monitorIDs := s.pageMonitorIDs(page)

	// Live monitors, sanitized.
	isPublic := page.Visibility == "public"
	monitors := []hub.PublicMonitor{}
	for _, sum := range s.d.Engine.Summaries() {
		if displayName, on := monitorIDs[sum.ID]; on {
			monitors = append(monitors, s.sanitizeFor(page, sum, displayName, isPublic, page.ShowLatency))
		}
	}

	now := s.d.Now()
	hist, err := s.pageHistory(r.Context(), page, monitorIDs, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	uptime := hist.uptime
	if page.Settings != nil && !page.Settings.ShowUptime {
		uptime = map[int64]*float64{} // the owner hid the percentages
	}

	// Incidents: active, plus resolved in the past 14 days.
	since14d := now.Add(-14 * 24 * time.Hour).UnixMilli()
	incidents, err := s.d.Store.IncidentsForPage(r.Context(), page.ID, since14d)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "incident updates unavailable", nil)
		return
	}
	var activeIncidents, pastIncidents []store.Incident
	for _, inc := range s.visibleIncidents(page, incidents) {
		if inc.ResolvedAt == nil {
			activeIncidents = append(activeIncidents, inc)
		} else {
			pastIncidents = append(pastIncidents, inc)
		}
	}

	activeMaint, upcomingMaint, err := s.pageMaintenanceFor(r, page, monitorIDs, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "maintenance updates unavailable", nil)
		return
	}

	// Groups (in page order) with their on-page members and description;
	// monitor items' descriptions keyed by monitor id.
	groupNames := map[int64]string{}
	for _, g := range s.d.Engine.Groups() {
		groupNames[g.ID] = g.Name
	}
	pageGroups := []map[string]any{}
	monitorDescriptions := map[int64]string{}
	for _, item := range page.Items {
		if item.MonitorID != nil && item.Description != "" {
			monitorDescriptions[*item.MonitorID] = item.Description
		}
		if item.GroupID == nil {
			continue
		}
		gname, exists := groupNames[*item.GroupID]
		if !exists {
			continue
		}
		pageIDs := []int64{}
		for _, id := range s.d.Engine.MonitorIDsInGroup(*item.GroupID) {
			if _, on := monitorIDs[id]; on {
				pageIDs = append(pageIDs, id)
			}
		}
		if item.DisplayName != "" {
			gname = item.DisplayName
		}
		pageGroups = append(pageGroups, map[string]any{
			"id": *item.GroupID, "name": gname, "description": item.Description, "monitorIds": pageIDs,
		})
	}

	subEmail, subSlack := subscribeOptions(page)
	feedOn := page.Settings == nil || page.Settings.FeedEnabled
	out := map[string]any{
		"page": map[string]any{
			"title":       page.Title,
			"description": page.Description,
			"layout":      page.Layout,
			"visibility":  page.Visibility,
			"showLatency": page.ShowLatency,
			// Alarm config is presentation only (which sound, muted or not):
			// nothing about targets or reasons.
			"alarmMuted":    page.AlarmMuted,
			"alarmDown":     page.AlarmDown,
			"alarmDegraded": page.AlarmDegraded,
			// Branding: admin-entered, validated https URLs and plain text.
			"logoUrl":          page.LogoURL,
			"brandColor":       page.BrandColor,
			"websiteUrl":       page.WebsiteURL,
			"supportUrl":       page.SupportURL,
			"footerText":       page.FooterText,
			"showResponseTime": page.ShowResponseTime,
			"historyDays":      page.HistoryDays,
			"subscribeEmail":   subEmail,
			"subscribeSlack":   subSlack,
			"settings":         publicPageSettings(page),
			"feedAvailable":    feedOn,
		},
		"monitorExtras":       s.pageMonitorExtras(page, monitorIDs),
		"groups":              pageGroups,
		"monitorDescriptions": monitorDescriptions,
		"monitors":            monitors,
		"daily":               hist.daily,
		"uptime":              uptime,
		"incidents": map[string]any{
			"active": toPublicIncidents(activeIncidents),
			"past":   toPublicIncidents(pastIncidents),
		},
		"overall": computeOverall(monitors),
	}
	// A page that hides maintenance carries no maintenance key at all.
	if showsMaintenance(page) {
		out["maintenance"] = map[string]any{"active": activeMaint, "upcoming": upcomingMaint}
	}
	writeJSON(w, http.StatusOK, out)
}

// showsMaintenance reports whether the owner lets this page show scheduled
// and in-progress maintenance (the default, also for pages saved before the
// option existed).
func showsMaintenance(p *store.Page) bool {
	return p.Settings == nil || p.Settings.ShowMaintenance
}

// pageMaintenanceFor is pageMaintenance for a page: empty lists, and no
// query, when the page does not show maintenance.
func (s *server) pageMaintenanceFor(r *http.Request, page *store.Page, monitorIDs map[int64]string, now time.Time) (active, upcoming []publicMaintenance, err error) {
	if !showsMaintenance(page) {
		return []publicMaintenance{}, []publicMaintenance{}, nil
	}
	return s.pageMaintenance(r, monitorIDs, now)
}

// sanitizeFor sanitizes one monitor for a page, masking maintenance when the
// page does not show it.
func (s *server) sanitizeFor(page *store.Page, sum engine.MonitorSummary, name string, public, showLatency bool) hub.PublicMonitor {
	pm := hub.Sanitize(sum, name, public, showLatency)
	if !showsMaintenance(page) {
		pm = hub.HideMaintenance(pm)
	}
	return pm
}

// visibleIncidents drops incidents whose impact is "maintenance" on a page
// that does not show maintenance. The input slice is not modified.
func (s *server) visibleIncidents(page *store.Page, list []store.Incident) []store.Incident {
	if showsMaintenance(page) {
		return list
	}
	out := make([]store.Incident, 0, len(list))
	for _, inc := range list {
		if inc.Impact != "maintenance" {
			out = append(out, inc)
		}
	}
	return out
}

// pageMaintenance returns the page's active windows and those starting in
// the next 7 days, as public DTOs: only windows that target at least one
// monitor on the page.
func (s *server) pageMaintenance(r *http.Request, monitorIDs map[int64]string, now time.Time) (active, upcoming []publicMaintenance, err error) {
	active, upcoming = []publicMaintenance{}, []publicMaintenance{}
	allMaint, err := s.d.Store.ListMaintenance(r.Context())
	if err != nil {
		return nil, nil, err
	}
	horizon := now.Add(7 * 24 * time.Hour).UnixMilli()
	for _, m := range allMaint {
		if !m.Active || !s.maintenanceTouches(m, monitorIDs) {
			continue
		}
		state, nextStart, currentEnd := computeMaintenanceState(m, now)
		pm := toPublicMaintenance(m, nextStart, currentEnd)
		if state == "active" {
			active = append(active, pm)
		} else if state == "upcoming" {
			if nextStart == nil || *nextStart <= horizon {
				upcoming = append(upcoming, pm)
			}
		}
	}
	return active, upcoming, nil
}

func (s *server) maintenanceTouches(m store.Maintenance, monitorIDs map[int64]string) bool {
	for _, mid := range m.MonitorIDs {
		if _, ok := monitorIDs[mid]; ok {
			return true
		}
	}
	for _, gid := range m.GroupIDs {
		for _, id := range s.d.Engine.MonitorIDsInGroup(gid) {
			if _, ok := monitorIDs[id]; ok {
				return true
			}
		}
	}
	return false
}

// publicMaintenance is the only shape a maintenance window takes on a status
// page: what the card shows, never the window's targets (which can name
// monitors that aren't on this page) or who created it.
type publicMaintenance struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	StartsAt    *int64 `json:"startsAt"`
	EndsAt      *int64 `json:"endsAt"`
	NextStart   *int64 `json:"nextStart"`
	CurrentEnd  *int64 `json:"currentEnd"`
}

func toPublicMaintenance(m store.Maintenance, nextStart, currentEnd *int64) publicMaintenance {
	return publicMaintenance{
		ID: m.ID, Title: m.Title, Description: m.Description, Kind: m.Kind,
		StartsAt: m.StartsAt, EndsAt: m.EndsAt, NextStart: nextStart, CurrentEnd: currentEnd,
	}
}

func computeOverall(monitors []hub.PublicMonitor) string {
	if len(monitors) == 0 {
		return "operational"
	}
	var down, degraded, maint int
	for _, m := range monitors {
		switch m.Status {
		case model.StatusDown:
			down++
		case model.StatusDegraded, model.StatusPending:
			degraded++
		case model.StatusMaintenance:
			maint++
		}
	}
	if down > 0 && down*2 >= len(monitors) {
		return "major"
	}
	if down > 0 {
		return "partial"
	}
	if degraded > 0 {
		return "degraded"
	}
	if maint > 0 {
		return "maintenance"
	}
	return "operational"
}

// handlePublicStream serves GET /api/public/pages/{slug}/stream.
func (s *server) handlePublicStream(w http.ResponseWriter, r *http.Request) {
	page, ok := s.resolvePublicPage(w, r)
	if !ok {
		return
	}
	isPublic := page.Visibility == "public"
	s.d.Hub.ServeScoped(w, r, fmt.Sprintf("%d:%d", page.ID, page.UpdatedAt), func() hub.PageScope {
		return hub.PageScope{
			MonitorIDs:  s.pageMonitorIDs(page),
			Public:      isPublic,
			ShowLatency: page.ShowLatency,
			// Re-read on every tick, so toggling the option applies live.
			HideMaintenance: !showsMaintenance(page),
		}
	})
}

// handlePublicDay serves GET /api/public/pages/{slug}/day?monitor=&date=YYYY-MM-DD.
//
// The date is a DISPLAY_TZ day, the same day the daily bars are bucketed by,
// so a bar and its day view describe the same calendar day, including DST. Down periods
// come from outage rows, which outlive raw heartbeats, so a day older than
// the raw retention still shows the downtime its bar reports. Degraded
// periods need raw beats and are only there while those are kept.
func (s *server) handlePublicDay(w http.ResponseWriter, r *http.Request) {
	page, ok := s.resolvePublicPage(w, r)
	if !ok {
		return
	}

	monitorID := queryInt64(r, "monitor", 0)
	if monitorID == 0 {
		writeErr(w, http.StatusBadRequest, "monitor required", nil)
		return
	}
	if _, on := s.pageMonitorIDs(page)[monitorID]; !on {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}

	date, err := time.Parse("2006-01-02", r.URL.Query().Get("date"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid date format (YYYY-MM-DD)", nil)
		return
	}
	t, exists := store.CalendarDayStart(date.Year(), date.Month(), date.Day(), s.d.Store.Loc)
	if !exists {
		writeErr(w, http.StatusBadRequest, "date does not exist in the server timezone", nil)
		return
	}
	now := s.d.Now()
	dayStart := t.UnixMilli()
	dayEnd := store.NextCalendarDayStart(t, s.d.Store.Loc).UnixMilli()
	clipEnd := min(dayEnd, now.UnixMilli())

	beats, err := s.d.Store.BeatsOfDay(r.Context(), monitorID, dayStart, dayEnd)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	outages, err := s.d.Store.MonitorOutagesOverlapping(r.Context(), monitorID, dayStart, dayEnd, 200)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}

	isPublic := page.Visibility == "public"
	// Same rule as hub.Sanitize: a public page shows latency only when the
	// page opts in, and never a reason.
	showLatency := !isPublic || page.ShowLatency

	type beatOut struct {
		TS        int64  `json:"ts"`
		Status    string `json:"status"`
		LatencyMs *int   `json:"latencyMs,omitempty"`
		Reason    string `json:"reason,omitempty"`
	}
	out := make([]beatOut, 0, len(beats))
	for _, b := range beats {
		bo := beatOut{TS: b.TS, Status: model.Status(b.Status).String()}
		if showLatency {
			lat := b.LatencyMs
			bo.LatencyMs = &lat
		}
		if !isPublic {
			bo.Reason = b.Msg
		}
		out = append(out, bo)
	}

	type periodOut struct {
		Status  string `json:"status"`
		Start   int64  `json:"start"`
		End     int64  `json:"end"`
		Ongoing bool   `json:"ongoing,omitempty"`
		Reason  string `json:"reason,omitempty"`
		// Note is the on-call note on the outage: private pages only.
		Note string `json:"note,omitempty"`
	}
	periods := []periodOut{}
	for _, o := range outages {
		p := periodOut{Status: "down", Start: max(o.StartedAt, dayStart), End: clipEnd}
		if o.EndedAt != nil {
			p.End = min(*o.EndedAt, dayEnd)
		} else {
			p.Ongoing = true
		}
		if p.End < p.Start {
			p.End = p.Start
		}
		if !isPublic {
			p.Reason = o.FirstReason
			if o.Note != nil {
				p.Note = *o.Note
			}
		}
		periods = append(periods, p)
	}

	// Degraded runs from raw beats: consecutive degraded checks, a run
	// broken by a gap longer than 2.5 intervals (an up check in between).
	interval := int64(60_000)
	if m, ok := s.d.Engine.Monitor(monitorID); ok && m.IntervalS > 0 {
		interval = int64(m.IntervalS) * 1000
	}
	var run *periodOut
	flush := func() {
		if run != nil {
			run.End = min(run.End+interval, clipEnd)
			periods = append(periods, *run)
			run = nil
		}
	}
	for _, b := range beats {
		if model.Status(b.Status) != model.StatusDegraded {
			continue
		}
		if run != nil && b.TS-run.End > interval*5/2 {
			flush()
		}
		if run == nil {
			run = &periodOut{Status: "degraded", Start: b.TS, End: b.TS}
			if !isPublic {
				run.Reason = b.Msg
			}
		}
		run.End = b.TS
	}
	flush()
	sort.Slice(periods, func(i, j int) bool { return periods[i].Start < periods[j].Start })

	writeJSON(w, http.StatusOK, map[string]any{
		"dayStart": dayStart,
		"dayEnd":   dayEnd,
		"periods":  periods,
		"beats":    out,
	})
}

// clientIP extracts the client IP from X-Real-IP, X-Forwarded-For or RemoteAddr.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	return host
}
