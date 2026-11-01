// Package hub fans engine state out over Server-Sent Events.
package hub

import (
	"strings"

	"opskest/internal/engine"
	"opskest/internal/model"
)

// PublicMonitor is a monitor as seen by SSE clients and GET /api/monitors.
// When displayed on a public status page, target, reason, runbook and tagIds
// are stripped; latency is omitted unless the page enables it.
type PublicMonitor struct {
	ID        int64        `json:"id"`
	Name      string       `json:"name"`
	GroupID   *int64       `json:"groupId"`
	Status    model.Status `json:"status"`
	Since     int64        `json:"since"`
	Uptime24h *float64     `json:"uptime24h"`
	Uptime30d *float64     `json:"uptime30d"`
	Beats     string       `json:"beats"`
	LatencyMs *int         `json:"latencyMs,omitempty"`
	Reason    string       `json:"reason,omitempty"`
}

// Sanitize converts a MonitorSummary to a PublicMonitor. For public pages
// it strips reason, and shows latency only if showLatency is true.
// displayName overrides the monitor name when non-empty.
func Sanitize(s engine.MonitorSummary, displayName string, public, showLatency bool) PublicMonitor {
	name := s.Name
	if displayName != "" {
		name = displayName
	}
	pm := PublicMonitor{
		ID:        s.ID,
		Name:      name,
		GroupID:   s.GroupID,
		Status:    s.Status,
		Since:     s.Since,
		Uptime24h: s.Uptime24h,
		Uptime30d: s.Uptime30d,
		Beats:     lastBeats(s.Beats, publicBeats),
	}
	if !public {
		pm.Reason = s.Reason
		lat := s.LatencyMs
		pm.LatencyMs = &lat
	} else if showLatency {
		lat := s.LatencyMs
		pm.LatencyMs = &lat
	}
	return pm
}

// HideMaintenance removes every trace of maintenance from a public monitor,
// for a page whose owner turned "show scheduled maintenance" off: the
// maintenance status and its beats read as unknown (no data), which is
// never described as confirmed healthy service and never names the cause.
func HideMaintenance(pm PublicMonitor) PublicMonitor {
	if pm.Status == model.StatusMaintenance {
		pm.Status = model.StatusUnknown
	}
	if strings.IndexByte(pm.Beats, 'm') >= 0 {
		pm.Beats = strings.ReplaceAll(pm.Beats, "m", "k")
	}
	return pm
}

// publicBeats is how many recent beats a status page gets. The dashboard
// shows up to 100 on a monitor's page; a status page's board cards draw
// far fewer, so it keeps the smaller payload.
const publicBeats = 50

// lastBeats returns the newest n beat characters (one byte each).
func lastBeats(b string, n int) string {
	if len(b) > n {
		return b[len(b)-n:]
	}
	return b
}
