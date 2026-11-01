package api

import (
	"fmt"
	"hash/fnv"
	"html"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"opskest/internal/engine"
	"opskest/internal/hub"
	"opskest/internal/model"
	"opskest/internal/store"
)

// Embeddable status badges for PUBLIC status pages.
//
//	GET /api/public/pages/{slug}/badge.svg                     overall status
//	GET /api/public/pages/{slug}/services/{id}/badge.svg       one service's status
//	GET /api/public/pages/{slug}/services/{id}/uptime.svg      ?window=24h|7d|30d|90d
//
// {id} is the monitor id the public page body already exposes
// (monitors[].id), so nothing new is revealed; it only resolves for a
// service that is on the page. Badges read the engine's in-memory summaries
// (status and the 24h/7d/30d/90d uptime windows), the same numbers the page
// shows, so there is no new database path and nothing touches the writer.
//
// What a badge may contain is a closed set: a fixed status vocabulary, a
// percentage, the window name, and the service's public display name. Never
// a reason, target, runbook or tag. Private pages and unknown pages or
// services all answer the same 404 image, never a 403, and private pages
// never answer a badge even to a signed-in user (a shared cache or camo
// proxy in front of the app must not be handed private state).

const (
	badgeCacheControl = "public, max-age=60"
	badgeMaxLabelLen  = 24 // runes of service name shown in a label
)

// badge palette: white text on each fill is >= 4.5:1.
const (
	badgeLabelBG = "#374151"
	badgeGreen   = "#2e7d32"
	badgeAmber   = "#a65f00"
	badgeOrange  = "#c2410c"
	badgeRed     = "#c62828"
	badgeBlue    = "#1d4ed8"
	badgeGray    = "#5b6470"
)

var badgeWindows = map[string]bool{"24h": true, "7d": true, "30d": true, "90d": true}

// writeBadge writes an SVG with the shared caching headers and a cheap ETag
// (a hash of the body), answering 304 to a matching If-None-Match.
func writeBadge(w http.ResponseWriter, r *http.Request, status int, svg string) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(svg))
	etag := `"` + strconv.FormatUint(h.Sum64(), 16) + `"`

	hdr := w.Header()
	hdr.Set("Content-Type", "image/svg+xml; charset=utf-8")
	hdr.Set("Cache-Control", badgeCacheControl)
	hdr.Set("X-Content-Type-Options", "nosniff")
	// Badges exist to be embedded on other sites.
	hdr.Set("Cross-Origin-Resource-Policy", "cross-origin")
	// If the SVG is ever opened directly, it may not run or load anything.
	hdr.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	hdr.Set("ETag", etag)

	if status == http.StatusOK && strings.Contains(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(svg))
	}
}

// badgeNotFound is the one body for every miss: unknown page, private page,
// unknown service, service not on the page. Byte-identical on purpose.
func badgeNotFound(w http.ResponseWriter, r *http.Request) {
	writeBadge(w, r, http.StatusNotFound, renderBadge("status", "not found", badgeGray))
}

// resolveBadgePage returns the page only when it is public and the caller is
// within the public rate limit. ok=false means the response is written.
func (s *server) resolveBadgePage(w http.ResponseWriter, r *http.Request) (*store.Page, bool) {
	p, err := s.d.Store.GetPageBySlug(r.Context(), r.PathValue("slug"))
	if err != nil || p.Visibility != "public" {
		badgeNotFound(w, r)
		return nil, false
	}
	if !s.publicLimit.allow(clientIP(r), s.d.Now()) {
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusTooManyRequests)
		return nil, false
	}
	return p, true
}

// handlePublicBadge serves the page's overall status badge.
func (s *server) handlePublicBadge(w http.ResponseWriter, r *http.Request) {
	pg, ok := s.resolveBadgePage(w, r)
	if !ok {
		return
	}
	monitorIDs := s.pageMonitorIDs(pg)
	monitors := []hub.PublicMonitor{}
	for _, sum := range s.d.Engine.Summaries() {
		if name, on := monitorIDs[sum.ID]; on {
			monitors = append(monitors, s.sanitizeFor(pg, sum, name, true, false))
		}
	}
	text, color := overallBadgeText(computeOverall(monitors))
	writeBadge(w, r, http.StatusOK, renderBadge("status", text, color))
}

// handlePublicServiceBadge serves one service's current status badge.
func (s *server) handlePublicServiceBadge(w http.ResponseWriter, r *http.Request) {
	sum, name, ok := s.badgeService(w, r)
	if !ok {
		return
	}
	text, color := serviceBadgeText(sum.Status)
	writeBadge(w, r, http.StatusOK, renderBadge(badgeLabelText(name), text, color))
}

// handlePublicUptimeBadge serves one service's uptime over a window.
func (s *server) handlePublicUptimeBadge(w http.ResponseWriter, r *http.Request) {
	sum, name, ok := s.badgeService(w, r)
	if !ok {
		return
	}
	window := r.URL.Query().Get("window")
	if window == "" {
		window = "30d"
	}
	// Exact match against a closed set; nothing from the query is reflected.
	// Checked after the page/service lookup so a miss is still the same 404.
	if !badgeWindows[window] {
		writeErr(w, http.StatusBadRequest, "invalid window: use 24h, 7d, 30d or 90d", nil)
		return
	}
	var v *float64
	switch window {
	case "24h":
		v = sum.Uptime24h
	case "7d":
		v = sum.Uptime7d
	case "30d":
		v = sum.Uptime30d
	case "90d":
		v = sum.Uptime90d
	}
	text, color := uptimeBadgeText(v)
	label := badgeLabelText(name) + " uptime " + window
	writeBadge(w, r, http.StatusOK, renderBadge(label, text, color))
}

// badgeService resolves {slug} and {id} to a service that is on a public
// page, returning its live summary and public display name.
func (s *server) badgeService(w http.ResponseWriter, r *http.Request) (engine.MonitorSummary, string, bool) {
	pg, ok := s.resolveBadgePage(w, r)
	if !ok {
		return engine.MonitorSummary{}, "", false
	}
	id, err := pathID(r, "id")
	if err != nil {
		badgeNotFound(w, r)
		return engine.MonitorSummary{}, "", false
	}
	override, on := s.pageMonitorIDs(pg)[id]
	if !on {
		badgeNotFound(w, r)
		return engine.MonitorSummary{}, "", false
	}
	sum, found := s.d.Engine.Summary(id)
	if !found {
		badgeNotFound(w, r)
		return engine.MonitorSummary{}, "", false
	}
	name := sum.Name
	if override != "" {
		name = override
	}
	if !showsMaintenance(pg) && sum.Status == model.StatusMaintenance {
		sum.Status = model.StatusUnknown // the owner hides maintenance on this page
	}
	return sum, name, true
}

// overallBadgeText maps computeOverall's result to badge text and colour.
func overallBadgeText(overall string) (string, string) {
	switch overall {
	case "operational":
		return "operational", badgeGreen
	case "degraded":
		return "degraded", badgeAmber
	case "partial":
		return "partial outage", badgeOrange
	case "major":
		return "major outage", badgeRed
	case "maintenance":
		return "maintenance", badgeBlue
	}
	return "unknown", badgeGray
}

// serviceBadgeText maps one service's status the same way the page's
// overall rollup treats it (pending counts as degraded).
func serviceBadgeText(st model.Status) (string, string) {
	switch st {
	case model.StatusUp:
		return "operational", badgeGreen
	case model.StatusDegraded, model.StatusPending:
		return "degraded", badgeAmber
	case model.StatusDown:
		return "outage", badgeRed
	case model.StatusMaintenance:
		return "maintenance", badgeBlue
	case model.StatusPaused:
		return "paused", badgeGray
	}
	return "unknown", badgeGray
}

// uptimeBadgeText formats a percentage. It truncates rather than rounds, so
// 99.996% never reads as a perfect 100%.
func uptimeBadgeText(v *float64) (string, string) {
	if v == nil || math.IsNaN(*v) {
		return "n/a", badgeGray
	}
	p := math.Min(100, math.Max(0, *v))
	text := strconv.FormatFloat(math.Floor(p*100)/100, 'f', 2, 64)
	text = strings.TrimRight(strings.TrimRight(text, "0"), ".") + "%"
	switch {
	case p >= 99.9:
		return text, badgeGreen
	case p >= 99:
		return text, badgeAmber
	case p >= 95:
		return text, badgeOrange
	}
	return text, badgeRed
}

// badgeLabelText makes an admin-entered service name safe and short for a
// label: control characters and newlines dropped, whitespace collapsed,
// bounded to badgeMaxLabelLen runes. (XML escaping happens at render time.)
func badgeLabelText(name string) string {
	var b strings.Builder
	space := false
	for _, r := range name {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if unicode.IsControl(r) || r == utf8.RuneError || !unicode.IsPrint(r) {
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	out := b.String()
	if utf8.RuneCountInString(out) > badgeMaxLabelLen {
		out = string([]rune(out)[:badgeMaxLabelLen-1]) + "…"
	}
	if out == "" {
		return "service"
	}
	return out
}

// badgeTextWidth estimates rendered width in px of s at 11px Verdana. It
// only has to be close: textLength on the element makes the browser fit the
// text to this width regardless of the font actually used.
func badgeTextWidth(s string) float64 {
	var w float64
	for _, r := range s {
		switch {
		case strings.ContainsRune("il.,:;'|!", r):
			w += 3.6
		case r == ' ' || r == 'I' || r == 'f' || r == 'j' || r == 't' || r == 'r' || r == '(' || r == ')' || r == '-':
			w += 4.4
		case strings.ContainsRune("mwMW%@", r):
			w += 10.2
		case r >= 'A' && r <= 'Z':
			w += 7.6
		case r >= '0' && r <= '9':
			w += 6.8
		case r >= 0x2E80:
			w += 11
		default:
			w += 6.4
		}
	}
	return math.Ceil(w)
}

// renderBadge builds a flat two-part SVG badge. label and value are escaped
// here, in the one place that writes them into markup. The <title> and
// aria-label give screen readers the full "label: value" text.
func renderBadge(label, value, color string) string {
	const pad = 7.0
	lw := badgeTextWidth(label)
	vw := math.Ceil(badgeTextWidth(value) * 1.08) // bold runs wider
	leftW := lw + 2*pad
	rightW := vw + 2*pad
	total := leftW + rightW

	desc := html.EscapeString(label + ": " + value)
	l := html.EscapeString(label)
	v := html.EscapeString(value)
	font := `font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11" text-rendering="geometricPrecision"`

	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%[1]g" height="20" viewBox="0 0 %[1]g 20" role="img" aria-label="%[2]s">`+
		`<title>%[2]s</title>`+
		`<clipPath id="r"><rect width="%[1]g" height="20" rx="3" fill="#fff"/></clipPath>`+
		`<g clip-path="url(#r)"><rect width="%[3]g" height="20" fill="%[4]s"/><rect x="%[3]g" width="%[5]g" height="20" fill="%[6]s"/></g>`+
		`<g fill="#fff" text-anchor="middle" %[7]s>`+
		`<text x="%[8]g" y="14" textLength="%[9]g" lengthAdjust="spacingAndGlyphs">%[10]s</text>`+
		`<text x="%[11]g" y="14" textLength="%[12]g" lengthAdjust="spacingAndGlyphs" font-weight="bold">%[13]s</text>`+
		`</g></svg>`,
		total, desc, leftW, badgeLabelBG, rightW, color, font,
		leftW/2, lw, l,
		leftW+rightW/2, vw, v)
}
