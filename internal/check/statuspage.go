package check

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// hasWord reports whether word occurs in s (assumed already lowercased) as
// a whole word rather than as a substring of a longer one. Plain
// strings.Contains misreads free-text incident titles: "down" matches
// inside "slowdown", "major" matches inside "majority", and critically
// "resolved" matches inside "unresolved" (which would make an active,
// still-unresolved incident register as if it had been resolved). Only
// ASCII letters/digits/underscore count as word characters, which is
// sufficient since callers pre-lowercase ASCII status text.
func hasWord(s, word string) bool {
	n := len(word)
	if n == 0 {
		return false
	}
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] != word {
			continue
		}
		if i > 0 && isWordByte(s[i-1]) {
			continue
		}
		if end := i + n; end < len(s) && isWordByte(s[end]) {
			continue
		}
		return true
	}
	return false
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// CheckStatusPage checks whether target is a known 3rd party status page or RSS feed and,
// queries their incident API/feed to verify if their actual service is degraded or down.
// Returns (handled, isDown, isDegraded, reason).
func CheckStatusPage(ctx context.Context, client *http.Client, target string) (bool, bool, bool, string) {
	u, err := url.Parse(target)
	if err != nil {
		return false, false, false, ""
	}
	host := strings.ToLower(u.Host)
	path := strings.ToLower(u.Path)

	// 1. RSS / Atom Feed (e.g. /feed, /history.rss, .xml, /rss)
	if strings.Contains(path, "rss") || strings.Contains(path, "feed") || strings.HasSuffix(path, ".xml") || strings.Contains(u.RawQuery, "rss") {
		if handled, down, degraded, reason := checkRSSOrAtomFeed(ctx, client, target); handled {
			return true, down, degraded, reason
		}
	}

	// 2. Meta Status (metastatus.com)
	if strings.Contains(host, "metastatus.com") {
		return checkMetaStatus(ctx, client, u)
	}

	// 3. Firebase & Google Cloud / Workspace Status
	if strings.Contains(host, "status.firebase.google.com") ||
		strings.Contains(host, "status.cloud.google.com") ||
		(strings.Contains(host, "google.com") && strings.Contains(path, "appsstatus")) ||
		(strings.Contains(host, "google.com") && strings.Contains(host, "status")) {
		return checkGoogleStatus(ctx, client, u)
	}

	// 4. Slack Status (status.slack.com)
	if strings.Contains(host, "status.slack.com") {
		return checkSlackStatus(ctx, client, u)
	}

	// 5. Atlassian Statuspage.io feeds (GitHub, Cloudflare, OpenAI, Stripe, and generic status.*)
	if strings.Contains(host, "statuspage.io") || strings.Contains(host, "githubstatus.com") ||
		strings.Contains(host, "cloudflarestatus.com") || strings.HasPrefix(host, "status.") ||
		strings.Contains(host, "openai.com") || strings.Contains(host, "stripe.com") {
		if handled, down, degraded, reason := checkStatuspageIO(ctx, client, u); handled {
			return true, down, degraded, reason
		}
	}

	// 6. Generic HTML status page fallback (e.g. status.*)
	if strings.HasPrefix(host, "status.") || strings.Contains(path, "status") {
		if handled, down, degraded, reason := checkGenericHTMLStatusPage(ctx, client, target); handled {
			return true, down, degraded, reason
		}
	}

	return false, false, false, ""
}

func checkStatuspageIO(ctx context.Context, client *http.Client, u *url.URL) (bool, bool, bool, string) {
	apiURL := fmt.Sprintf("%s://%s/api/v2/summary.json", u.Scheme, u.Host)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return false, false, false, ""
	}
	req.Header.Set("User-Agent", "opskest/"+Version)
	resp, err := client.Do(req)
	if err != nil {
		return false, false, false, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, false, false, ""
	}

	var data struct {
		Status struct {
			Indicator   string `json:"indicator"`
			Description string `json:"description"`
		} `json:"status"`
		Incidents []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Impact string `json:"impact"`
		} `json:"incidents"`
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false, false, false, ""
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return false, false, false, ""
	}

	indicator := strings.ToLower(data.Status.Indicator)
	if indicator == "critical" || indicator == "major" {
		reason := "Statuspage Major Outage: " + data.Status.Description
		if len(data.Incidents) > 0 {
			reason = fmt.Sprintf("Outage: %s (%s)", data.Incidents[0].Name, data.Incidents[0].Status)
		}
		return true, true, false, reason
	}
	if indicator == "minor" {
		reason := "Statuspage Degraded: " + data.Status.Description
		if len(data.Incidents) > 0 {
			reason = fmt.Sprintf("Degraded: %s (%s)", data.Incidents[0].Name, data.Incidents[0].Status)
		}
		return true, false, true, reason
	}

	// Check unresolved incidents
	for _, inc := range data.Incidents {
		st := strings.ToLower(inc.Status)
		if st != "resolved" && st != "completed" && st != "postmortem" {
			imp := strings.ToLower(inc.Impact)
			if imp == "critical" || imp == "major" {
				return true, true, false, fmt.Sprintf("Outage: %s (%s)", inc.Name, inc.Status)
			}
			return true, false, true, fmt.Sprintf("Degraded: %s (%s)", inc.Name, inc.Status)
		}
	}

	return true, false, false, ""
}

func checkMetaStatus(ctx context.Context, client *http.Client, u *url.URL) (bool, bool, bool, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://metastatus.com/data/orgs.json", nil)
	if err != nil {
		return false, false, false, ""
	}
	req.Header.Set("User-Agent", "opskest/"+Version)
	resp, err := client.Do(req)
	if err != nil {
		return false, false, false, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, false, false, ""
	}

	type metaService struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	type metaOrg struct {
		ID       string        `json:"id"`
		Name     string        `json:"name"`
		Services []metaService `json:"services"`
	}

	var orgs []metaOrg
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err := json.Unmarshal(body, &orgs); err != nil {
		return false, false, false, ""
	}

	slug := strings.Trim(strings.ToLower(u.Path), "/")
	if slug == "" {
		for _, org := range orgs {
			for _, svc := range org.Services {
				st := strings.ToLower(svc.Status)
				if st != "" && st != "no known issues" && !strings.Contains(st, "operational") {
					if hasWord(st, "outage") || hasWord(st, "down") {
						return true, true, false, fmt.Sprintf("Meta Outage: %s (%s) - %s", org.Name, svc.Name, svc.Status)
					}
					return true, false, true, fmt.Sprintf("Meta Disruption: %s (%s) - %s", org.Name, svc.Name, svc.Status)
				}
			}
		}
		return true, false, false, ""
	}

	for _, org := range orgs {
		orgID := strings.ToLower(org.ID)
		if slug != "" && (orgID == slug || strings.Contains(orgID, slug) || strings.Contains(slug, orgID)) {
			for _, svc := range org.Services {
				st := strings.ToLower(svc.Status)
				if st != "" && st != "no known issues" && !strings.Contains(st, "operational") {
					if hasWord(st, "outage") || hasWord(st, "down") {
						return true, true, false, fmt.Sprintf("Meta Outage: %s - %s", svc.Name, svc.Status)
					}
					return true, false, true, fmt.Sprintf("Meta Disruption: %s - %s", svc.Name, svc.Status)
				}
			}
			return true, false, false, ""
		}
	}

	return true, false, false, ""
}

func checkGoogleStatus(ctx context.Context, client *http.Client, u *url.URL) (bool, bool, bool, string) {
	apiURL := "https://status.firebase.google.com/incidents.json"
	host := strings.ToLower(u.Host)
	path := strings.ToLower(u.Path)
	if strings.Contains(host, "status.cloud.google.com") {
		apiURL = "https://status.cloud.google.com/incidents.json"
	} else if strings.Contains(path, "appsstatus") || strings.Contains(host, "appsstatus") {
		apiURL = "https://www.google.com/appsstatus/dashboard/incidents.json"
	} else if strings.Contains(host, "status.firebase.google.com") {
		apiURL = "https://status.firebase.google.com/incidents.json"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return false, false, false, ""
	}
	req.Header.Set("User-Agent", "opskest/"+Version)
	resp, err := client.Do(req)
	if err != nil {
		return false, false, false, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, false, false, ""
	}

	type fbIncident struct {
		ID               string `json:"id"`
		Begin            string `json:"begin"`
		End              string `json:"end"`
		Desc             string `json:"external_desc"`
		StatusImpact     string `json:"status_impact"`
		Severity         string `json:"severity"`
		ServiceName      string `json:"service_name"`
		MostRecentUpdate struct {
			Status string `json:"status"`
			Text   string `json:"text"`
		} `json:"most_recent_update"`
	}
	var incs []fbIncident
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err := json.Unmarshal(body, &incs); err != nil {
		return false, false, false, ""
	}

	for _, inc := range incs {
		// An incident is active only if End is empty AND most_recent_update is NOT resolved/available
		st := strings.ToUpper(inc.MostRecentUpdate.Status)
		isResolved := inc.End != "" || st == "AVAILABLE" || st == "RESOLVED" || st == "CLOSED" || st == "COMPLETED" || st == "POSTMORTEM"
		if !isResolved {
			svcName := inc.ServiceName
			if svcName == "" {
				svcName = "Service"
			}
			desc := inc.Desc
			if desc == "" {
				desc = inc.MostRecentUpdate.Text
			}
			impact := strings.ToUpper(inc.StatusImpact)
			sev := strings.ToUpper(inc.Severity)
			if impact == "SERVICE_OUTAGE" || sev == "HIGH" || sev == "CRITICAL" {
				return true, true, false, fmt.Sprintf("Google/Firebase Outage: %s - %s", svcName, desc)
			}
			return true, false, true, fmt.Sprintf("Google/Firebase Disruption: %s - %s", svcName, desc)
		}
	}
	return true, false, false, ""
}

func checkSlackStatus(ctx context.Context, client *http.Client, u *url.URL) (bool, bool, bool, string) {
	apiURL := "https://status.slack.com/api/v2.0.0/current"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return false, false, false, ""
	}
	req.Header.Set("User-Agent", "opskest/"+Version)
	resp, err := client.Do(req)
	if err != nil {
		return false, false, false, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, false, false, ""
	}

	var data struct {
		Status          string `json:"status"` // "ok", "active"
		ActiveIncidents []struct {
			Title  string `json:"title"`
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"active_incidents"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err := json.Unmarshal(body, &data); err != nil {
		return false, false, false, ""
	}

	if data.Status != "ok" && len(data.ActiveIncidents) > 0 {
		inc := data.ActiveIncidents[0]
		return true, true, false, fmt.Sprintf("Slack Outage: %s (%s)", inc.Title, inc.Status)
	}
	return true, false, false, ""
}

func checkRSSOrAtomFeed(ctx context.Context, client *http.Client, target string) (bool, bool, bool, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false, false, false, ""
	}
	req.Header.Set("User-Agent", "opskest/"+Version)
	resp, err := client.Do(req)
	if err != nil {
		return false, false, false, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, false, false, ""
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false, false, false, ""
	}

	// Try RSS 2.0
	type rssItem struct {
		Title       string `xml:"title"`
		Description string `xml:"description"`
		PubDate     string `xml:"pubDate"`
	}
	type rssDoc struct {
		XMLName xml.Name `xml:"rss"`
		Channel struct {
			Title string    `xml:"title"`
			Items []rssItem `xml:"item"`
		} `xml:"channel"`
	}

	var rss rssDoc
	if err := xml.Unmarshal(body, &rss); err == nil && len(rss.Channel.Items) > 0 {
		latest := rss.Channel.Items[0]
		down, degraded := classifyFeedEntry(latest.Title + " " + latest.Description)
		switch {
		case down:
			return true, true, false, fmt.Sprintf("RSS Outage: %s", latest.Title)
		case degraded:
			return true, false, true, fmt.Sprintf("RSS Disruption: %s", latest.Title)
		}
		return true, false, false, ""
	}

	// Try Atom
	type atomEntry struct {
		Title   string `xml:"title"`
		Summary string `xml:"summary"`
		Updated string `xml:"updated"`
	}
	type atomDoc struct {
		XMLName xml.Name    `xml:"feed"`
		Title   string      `xml:"title"`
		Entries []atomEntry `xml:"entry"`
	}

	var atom atomDoc
	if err := xml.Unmarshal(body, &atom); err == nil && len(atom.Entries) > 0 {
		latest := atom.Entries[0]
		down, degraded := classifyFeedEntry(latest.Title + " " + latest.Summary)
		switch {
		case down:
			return true, true, false, fmt.Sprintf("Atom Outage: %s", latest.Title)
		case degraded:
			return true, false, true, fmt.Sprintf("Atom Disruption: %s", latest.Title)
		}
		return true, false, false, ""
	}

	return false, false, false, ""
}

// classifyFeedEntry reads the free text of the latest feed entry and decides
// whether it describes an active outage (down) or a lesser disruption
// (degraded). Matching is whole-word so "slowdown" is not "down",
// "majority" is not "major" and "unresolved" is not "resolved".
func classifyFeedEntry(text string) (down, degraded bool) {
	t := strings.ToLower(text)
	if hasWord(t, "resolved") || hasWord(t, "completed") {
		return false, false
	}
	if hasWord(t, "outage") || hasWord(t, "down") || hasWord(t, "major") {
		return true, false
	}
	if hasWord(t, "degraded") || hasWord(t, "incident") || hasWord(t, "investigating") || hasWord(t, "disruption") {
		return false, true
	}
	return false, false
}

// checkGenericHTMLStatusPage inspects generic HTML status pages for active incident banners or operational states.
func checkGenericHTMLStatusPage(ctx context.Context, client *http.Client, target string) (bool, bool, bool, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false, false, false, ""
	}
	req.Header.Set("User-Agent", "opskest/"+Version)
	resp, err := client.Do(req)
	if err != nil {
		return false, false, false, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, false, false, ""
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false, false, false, ""
	}
	text := strings.ToLower(string(body))

	// If page explicitly states all systems operational, it's 100% healthy
	if strings.Contains(text, "all systems operational") ||
		strings.Contains(text, "everything is operational") ||
		strings.Contains(text, "no incidents reported") ||
		strings.Contains(text, "all services operational") ||
		strings.Contains(text, "all services are running normally") {
		return true, false, false, ""
	}

	// Detect major outage markers
	if strings.Contains(text, "major outage") ||
		strings.Contains(text, "critical outage") ||
		strings.Contains(text, "service outage") {
		return true, true, false, "Status Page: Major Outage Detected"
	}

	// Detect degraded/disruption markers
	if strings.Contains(text, "partial outage") ||
		strings.Contains(text, "degraded performance") ||
		strings.Contains(text, "active incident") ||
		strings.Contains(text, "service disruption") {
		return true, false, true, "Status Page: Service Disruption Reported"
	}

	// Default to operational if status page fetched successfully with HTTP 200
	return true, false, false, ""
}
