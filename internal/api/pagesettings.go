package api

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"opskest/internal/store"
)

// Limits for the markdown fields. They are generous for prose and small
// enough that a status page payload stays small.
const (
	maxDescriptionMD = 2000
	maxFooterMD      = 600
	maxBannerText    = 300
	maxMarkdownLinks = 20
)

var (
	headerStyles    = map[string]bool{"plain": true, "tinted": true, "solid": true}
	pageFonts       = map[string]bool{"system": true, "humanist": true, "neutral": true, "serif": true, "mono": true}
	cornerStyles    = map[string]bool{"rounded": true, "square": true}
	colorModes      = map[string]bool{"": true, "system": true, "light": true, "dark": true}
	bannerSeverity  = map[string]bool{"info": true, "success": true, "warning": true, "critical": true}
	refreshChoices  = map[int]bool{30: true, 60: true, 120: true, 300: true}
	mdDestination   = regexp.MustCompile(`(!?)\[[^\]\n]*\]\(\s*([^)\s]*)`)
	mdAutolinkOrTag = regexp.MustCompile(`(?i)<\s*(script|iframe|object|embed|style|svg|img|a|link|meta|form)\b`)
)

// markdownProblem checks the safe subset the status page renders: no
// control characters, bounded length and link count, links limited to
// http, https and mailto, images limited to https. The page renders
// markdown into elements, never as HTML, so this is a second line of
// defence and a clear message for the author rather than the only guard.
func markdownProblem(s string, max int, multiline bool) string {
	if utf8.RuneCountInString(s) > max {
		return "at most " + strconv.Itoa(max) + " characters"
	}
	for _, r := range s {
		if r == '\n' && multiline || r == '\t' {
			continue
		}
		if unicode.IsControl(r) {
			return "contains a control character"
		}
	}
	if !multiline && strings.ContainsRune(s, '\n') {
		return "must be a single line"
	}
	if mdAutolinkOrTag.MatchString(s) {
		return "HTML is not allowed; use markdown"
	}
	links := mdDestination.FindAllStringSubmatch(s, -1)
	if len(links) > maxMarkdownLinks {
		return "at most 20 links and images"
	}
	for _, m := range links {
		dest := strings.ToLower(m[2])
		switch {
		case m[1] == "!" && !strings.HasPrefix(dest, "https://"):
			return "images must use https://"
		case strings.HasPrefix(dest, "https://"), strings.HasPrefix(dest, "http://"), strings.HasPrefix(dest, "mailto:"):
		default:
			return "links must start with https://, http:// or mailto:"
		}
	}
	return ""
}

// canonicalOrigin validates and normalises a page's public address: an
// https origin, nothing else.
func canonicalOrigin(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true
	}
	if len(raw) > 200 {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", false
	}
	host := strings.ToLower(u.Host)
	if strings.ContainsAny(host, " \\") {
		return "", false
	}
	return "https://" + host, true
}

// validatePageSettings normalises s in place and returns per-field messages,
// empty when valid.
func validatePageSettings(s *store.PageSettings) map[string]string {
	errs := map[string]string{}
	if !headerStyles[s.HeaderStyle] {
		errs["headerStyle"] = "choose plain, tinted or solid"
	}
	if !pageFonts[s.Font] {
		errs["font"] = "choose a font from the list"
	}
	if !cornerStyles[s.Corners] {
		errs["corners"] = "choose rounded or square"
	}
	if !colorModes[s.ColorMode] {
		errs["colorMode"] = "choose the app default, device, light or dark"
	}
	s.DescriptionMD = strings.TrimSpace(strings.ReplaceAll(s.DescriptionMD, "\r\n", "\n"))
	if msg := markdownProblem(s.DescriptionMD, maxDescriptionMD, true); msg != "" {
		errs["descriptionMd"] = msg
	}
	s.FooterMD = strings.TrimSpace(strings.ReplaceAll(s.FooterMD, "\r\n", "\n"))
	if msg := markdownProblem(s.FooterMD, maxFooterMD, true); msg != "" {
		errs["footerMd"] = msg
	}
	s.Banner.Text = strings.TrimSpace(s.Banner.Text)
	if s.Banner.Severity == "" {
		s.Banner.Severity = "info"
	}
	if !bannerSeverity[s.Banner.Severity] {
		errs["bannerSeverity"] = "choose info, success, warning or critical"
	}
	if msg := markdownProblem(s.Banner.Text, maxBannerText, false); msg != "" {
		errs["bannerText"] = msg
	}
	if s.RefreshSeconds == 0 {
		s.RefreshSeconds = 60
	}
	if !refreshChoices[s.RefreshSeconds] {
		errs["refreshSeconds"] = "choose 30, 60, 120 or 300 seconds"
	}
	origin, ok := canonicalOrigin(s.CanonicalURL)
	if !ok {
		errs["canonicalUrl"] = "must be an https:// address with no path, for example https://status.example.com"
	}
	s.CanonicalURL = origin
	return errs
}

// pageBase is the address a page is reached at: its canonical origin when
// the owner set one, otherwise this installation's APP_URL.
func (s *server) pageBase(p *store.Page) string {
	if p.Settings != nil && p.Settings.CanonicalURL != "" {
		return strings.TrimRight(p.Settings.CanonicalURL, "/")
	}
	return strings.TrimRight(s.d.Cfg.AppURL, "/")
}
