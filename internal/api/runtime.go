package api

import (
	"net/http"
	"time"
)

// Runtime metadata is deliberately public: status visitors need the same
// calendar-day timezone as the server's history buckets. No host names or IPs.
func (s *server) handleRuntime(w http.ResponseWriter, r *http.Request) {
	zone, region := "UTC", "This server"
	if s.d.Cfg != nil {
		if s.d.Cfg.DisplayTZ != nil {
			zone = s.d.Cfg.DisplayTZ.String()
		}
		if s.d.Cfg.ServerRegion != "" {
			region = s.d.Cfg.ServerRegion
		}
	}
	if _, err := time.LoadLocation(zone); err != nil || zone == "Local" {
		zone = "UTC"
	}
	writeJSON(w, http.StatusOK, map[string]string{"timeZone": zone, "serverRegion": region})
}
