package store

import "encoding/json"

// PageSettings are a status page's presentation and content options. They
// are stored as one JSON document (status_pages.settings). Every field has a
// default, applied before decoding, so a page saved before an option existed
// reads as if it had been saved with that default.
//
// Nothing here is operational data: it is all text and switches the page
// owner typed or chose for the public audience. Whatever reaches a visitor
// is still limited by the public DTOs in internal/api and hub.Sanitize.
type PageSettings struct {
	// Theme: a closed set of safe choices instead of custom CSS.
	HeaderStyle string `json:"headerStyle"` // plain, tinted, solid
	Font        string `json:"font"`        // system, humanist, neutral, serif, mono
	Corners     string `json:"corners"`     // rounded, square
	ColorMode   string `json:"colorMode"`   // "" (app default), system, light, dark: first visit only

	// Content. Markdown is rendered by the browser without raw HTML.
	DescriptionMD string     `json:"descriptionMd"`
	FooterMD      string     `json:"footerMd"`
	Banner        PageBanner `json:"banner"`

	// Show/hide switches. Defaults keep what pages showed before they existed.
	ShowUptime    bool `json:"showUptime"`
	ShowCert      bool `json:"showCert"`      // days until the TLS certificate expires
	ShowLastCheck bool `json:"showLastCheck"` // when each service was last checked

	// RefreshSeconds is how often the page re-reads incidents, maintenance
	// and history while the tab is visible: 30, 60, 120 or 300.
	RefreshSeconds int `json:"refreshSeconds"`

	// DefaultExpanded opens every group, timeline and section when the page
	// loads. Off by default: the page starts collapsed and visitors open what
	// they want (a deep link or a group's own problem state does not open it).
	DefaultExpanded bool `json:"defaultExpanded"`

	// FeedEnabled turns the Atom feed (and its link on the page) on or off.
	FeedEnabled bool `json:"feedEnabled"`

	// ShowMaintenance controls whether the public page shows scheduled and
	// in-progress maintenance. On by default, also for pages saved before the
	// option existed (the key is simply missing from their stored document).
	// Off: the public API carries no maintenance data for this page at all.
	ShowMaintenance bool `json:"showMaintenance"`

	// CanonicalURL is the origin (https://status.example.com) the page is
	// reachable at when it sits behind a reverse proxy under its own name.
	// Used for the canonical link, feed and summary URLs and badge snippets.
	CanonicalURL string `json:"canonicalUrl"`
}

// PageBanner is the pinned announcement shown above the services.
type PageBanner struct {
	Text     string `json:"text"`     // inline markdown; "" = no banner
	Severity string `json:"severity"` // info, success, warning, critical
}

// DefaultPageSettings is what a page without a saved document looks like.
func DefaultPageSettings() PageSettings {
	return PageSettings{
		HeaderStyle: "plain", Font: "system", Corners: "rounded",
		Banner:          PageBanner{Severity: "info"},
		ShowUptime:      true,
		RefreshSeconds:  60,
		FeedEnabled:     true,
		ShowMaintenance: true,
	}
}

// decodePageSettings reads a stored document over the defaults. A corrupt
// document yields the defaults rather than an unreadable page.
func decodePageSettings(raw string) PageSettings {
	s := DefaultPageSettings()
	if raw == "" || raw == "{}" {
		return s
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return DefaultPageSettings()
	}
	return s
}

// UnmarshalJSON decodes over the defaults, so a client that sends only some
// options (or an older one that sends none) never zeroes the rest.
func (s *PageSettings) UnmarshalJSON(b []byte) error {
	type plain PageSettings
	v := plain(DefaultPageSettings())
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*s = PageSettings(v)
	return nil
}

func encodePageSettings(s PageSettings) string {
	b, err := json.Marshal(s)
	if err != nil {
		return "{}"
	}
	return string(b)
}
