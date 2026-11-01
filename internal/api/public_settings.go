package api

import (
	"opskest/internal/store"
)

// publicPageSettings is the part of a page's settings a visitor receives:
// presentation, owner-written copy and switches. Nothing in it comes from a
// monitor, so there is no target, reason, runbook or tag to leak.
func publicPageSettings(p *store.Page) map[string]any {
	st := store.DefaultPageSettings()
	if p.Settings != nil {
		st = *p.Settings
	}
	return map[string]any{
		"headerStyle":     st.HeaderStyle,
		"font":            st.Font,
		"corners":         st.Corners,
		"colorMode":       st.ColorMode,
		"descriptionMd":   st.DescriptionMD,
		"footerMd":        st.FooterMD,
		"banner":          map[string]string{"text": st.Banner.Text, "severity": st.Banner.Severity},
		"showUptime":      st.ShowUptime,
		"showCert":        st.ShowCert,
		"showLastCheck":   st.ShowLastCheck,
		"refreshSeconds":  st.RefreshSeconds,
		"defaultExpanded": st.DefaultExpanded,
		"feedEnabled":     st.FeedEnabled,
		"showMaintenance": st.ShowMaintenance,
		"canonicalUrl":    st.CanonicalURL,
	}
}

// monitorExtra is the optional per-service detail a page may opt into. Both
// values are plain numbers: a check time and whole days until a certificate
// expires, never the certificate's subject, issuer or the host it belongs to.
type monitorExtra struct {
	LastCheckAt *int64 `json:"lastCheckAt,omitempty"`
	CertDays    *int   `json:"certDays,omitempty"`
}

// pageMonitorExtras builds the opt-in details, keyed by monitor id. The map
// is empty (not nil) when the page opted into neither, so the key is always
// present and an old client sees nothing new.
func (s *server) pageMonitorExtras(p *store.Page, monitorIDs map[int64]string) map[int64]monitorExtra {
	out := map[int64]monitorExtra{}
	if p.Settings == nil || (!p.Settings.ShowCert && !p.Settings.ShowLastCheck) {
		return out
	}
	for _, sum := range s.d.Engine.Summaries() {
		if _, on := monitorIDs[sum.ID]; !on {
			continue
		}
		var ex monitorExtra
		if p.Settings.ShowLastCheck && sum.LastCheckAt > 0 {
			at := sum.LastCheckAt
			ex.LastCheckAt = &at
		}
		if p.Settings.ShowCert && sum.CertDays != nil {
			d := *sum.CertDays
			ex.CertDays = &d
		}
		if ex.LastCheckAt != nil || ex.CertDays != nil {
			out[sum.ID] = ex
		}
	}
	return out
}
