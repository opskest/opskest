package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"opskest/internal/store"
)

// handlePublicIncidents serves GET /api/public/pages/{slug}/incidents?before=&limit=:
// the page's incident history, newest first, public updates only.
func (s *server) handlePublicIncidents(w http.ResponseWriter, r *http.Request) {
	page, ok := s.resolvePublicPage(w, r)
	if !ok {
		return
	}
	limit := clampLimit(queryInt(r, "limit", 20))
	list, err := s.d.Store.PageIncidentsBefore(r.Context(), page.ID, queryInt64(r, "before", 0), queryInt64(r, "beforeId", 0), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	var next, nextID *int64
	if len(list) == limit {
		last := list[len(list)-1]
		next, nextID = &last.CreatedAt, &last.ID
	}
	list = s.visibleIncidents(page, list)
	writeJSON(w, http.StatusOK, map[string]any{"incidents": toPublicIncidents(list), "nextBefore": next, "nextBeforeId": nextID})
}

// handlePublicIncident serves GET /api/public/pages/{slug}/incidents/{id}.
// An incident that exists but isn't linked to this page is a 404 too.
func (s *server) handlePublicIncident(w http.ResponseWriter, r *http.Request) {
	page, ok := s.resolvePublicPage(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	inc, err := s.d.Store.PageIncident(r.Context(), page.ID, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	if len(s.visibleIncidents(page, []store.Incident{*inc})) == 0 {
		writeErr(w, http.StatusNotFound, "not found", nil) // a maintenance report on a page that hides maintenance
		return
	}
	writeJSON(w, http.StatusOK, toPublicIncidents([]store.Incident{*inc})[0])
}

// publicLatencyPoint is the public response-time shape: p50 and p95 only.
// The tail (p99/max) of an internal endpoint is operational detail, not
// something a customer page needs.
type publicLatencyPoint struct {
	T   int64 `json:"t"`
	P50 *int  `json:"p50"`
	P95 *int  `json:"p95"`
}

var publicLatencyRanges = map[string]bool{"24h": true, "7d": true, "30d": true}

// handlePublicLatency serves GET /api/public/pages/{slug}/latency?monitor=&range=.
// 404 unless the page opted into response-time charts and the monitor is on it.
func (s *server) handlePublicLatency(w http.ResponseWriter, r *http.Request) {
	page, ok := s.resolvePublicPage(w, r)
	if !ok {
		return
	}
	monitorID := queryInt64(r, "monitor", 0)
	if !page.ShowResponseTime {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	if _, on := s.pageMonitorIDs(page)[monitorID]; !on {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "24h"
	}
	if !publicLatencyRanges[rng] {
		writeErr(w, http.StatusBadRequest, "invalid range: use 24h, 7d or 30d", nil)
		return
	}
	summary, points, _, err := s.latencySeries(r.Context(), monitorID, rng, s.d.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	out := make([]publicLatencyPoint, 0, len(points))
	for _, p := range points {
		if p.P50 == nil { // no percentile data for this window (e.g. before the upgrade that added it)
			continue
		}
		out = append(out, publicLatencyPoint{T: p.T, P50: p.P50, P95: p.P95})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"range":   rng,
		"summary": map[string]any{"p50": summary.P50, "p95": summary.P95},
		"points":  out,
	})
}

// ── Atom feed ──────────────────────────────────────────────────────

type atomFeed struct {
	XMLName xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Title   string      `xml:"title"`
	ID      string      `xml:"id"`
	Updated string      `xml:"updated"`
	Author  atomPerson  `xml:"author"`
	Link    []atomLink  `xml:"link"`
	Entries []atomEntry `xml:"entry"`
}

// atomPerson is the required author element (RFC 4287 §4.1.1): the page.
type atomPerson struct {
	Name string `xml:"name"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr,omitempty"`
	Type string `xml:"type,attr,omitempty"`
}

type atomEntry struct {
	Title   string   `xml:"title"`
	ID      string   `xml:"id"`
	Updated string   `xml:"updated"`
	Link    atomLink `xml:"link"`
	Content atomText `xml:"content"`
}

type atomText struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}

func atomTime(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339) }

// handlePublicFeed serves GET /api/public/pages/{slug}/feed.atom: the last
// 50 incidents linked to the page, each entry carrying its public timeline
// as plain text (encoding/xml escapes every byte of it).
func (s *server) handlePublicFeed(w http.ResponseWriter, r *http.Request) {
	page, ok := s.resolvePublicPage(w, r)
	if !ok {
		return
	}
	if page.Settings != nil && !page.Settings.FeedEnabled {
		writeErr(w, http.StatusNotFound, "not found", nil) // the owner turned the feed off
		return
	}
	list, err := s.d.Store.PageIncidentsBefore(r.Context(), page.ID, 0, 0, 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	origin := s.pageBase(page)
	base := origin + "/status/" + page.Slug
	feed := atomFeed{
		Title:   page.Title + " status",
		ID:      base,
		Updated: atomTime(page.UpdatedAt),
		Author:  atomPerson{Name: page.Title},
		Link: []atomLink{
			{Href: base, Rel: "alternate", Type: "text/html"},
			{Href: origin + "/api/public/pages/" + page.Slug + "/feed.atom", Rel: "self", Type: "application/atom+xml"},
		},
	}
	for _, inc := range toPublicIncidents(s.visibleIncidents(page, list)) {
		if inc.UpdatedAt > page.UpdatedAt && atomTime(inc.UpdatedAt) > feed.Updated {
			feed.Updated = atomTime(inc.UpdatedAt)
		}
		var b strings.Builder
		for i := len(inc.Updates) - 1; i >= 0; i-- { // newest update first
			u := inc.Updates[i]
			if u.Status != "" {
				b.WriteString(strings.ToUpper(u.Status[:1]) + u.Status[1:])
			}
			b.WriteString(" — " + time.UnixMilli(u.CreatedAt).UTC().Format("2006-01-02 15:04 UTC") + "\n")
			b.WriteString(u.Body + "\n\n")
		}
		link := base + "/incidents/" + strconv.FormatInt(inc.ID, 10)
		feed.Entries = append(feed.Entries, atomEntry{
			Title:   "[" + inc.Status + "] " + inc.Title,
			ID:      link,
			Updated: atomTime(inc.UpdatedAt),
			Link:    atomLink{Href: link, Rel: "alternate", Type: "text/html"},
			Content: atomText{Type: "text", Body: strings.TrimSpace(b.String())},
		})
	}
	out, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "feed failed", nil)
		return
	}
	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	if page.Visibility == "private" {
		// Never let a shared cache in front of the app keep a private page's feed.
		w.Header().Set("Cache-Control", "private, no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=60")
	}
	body := append([]byte(xml.Header), out...)
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// ── RSS 2.0 feed ───────────────────────────────────────────────────

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title         string    `xml:"title"`
	Link          string    `xml:"link"`
	Description   string    `xml:"description"`
	Language      string    `xml:"language"`
	LastBuildDate string    `xml:"lastBuildDate"`
	AtomLink      rssSelf   `xml:"http://www.w3.org/2005/Atom link"`
	Items         []rssItem `xml:"item"`
}

type rssSelf struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type rssItem struct {
	Title       string  `xml:"title"`
	Link        string  `xml:"link"`
	GUID        rssGUID `xml:"guid"`
	PubDate     string  `xml:"pubDate"`
	Description string  `xml:"description"`
}

type rssGUID struct {
	IsPermaLink bool   `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

func rssTime(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC1123Z) }

// handlePublicRSS serves GET /api/public/pages/{slug}/feed.rss: the same
// incidents and public text as the Atom feed, as RSS 2.0 for readers and chat
// tools that only speak RSS. Same visibility, feed switch, caching and ETag.
func (s *server) handlePublicRSS(w http.ResponseWriter, r *http.Request) {
	page, ok := s.resolvePublicPage(w, r)
	if !ok {
		return
	}
	if page.Settings != nil && !page.Settings.FeedEnabled {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	list, err := s.d.Store.PageIncidentsBefore(r.Context(), page.ID, 0, 0, 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	origin := s.pageBase(page)
	base := origin + "/status/" + page.Slug
	desc := page.Description
	if strings.TrimSpace(desc) == "" {
		desc = "Incidents and updates for " + page.Title
	}
	updated := page.UpdatedAt
	feed := rssFeed{
		Version: "2.0",
		Channel: rssChannel{
			Title:       page.Title + " status",
			Link:        base,
			Description: desc,
			Language:    "en",
			AtomLink:    rssSelf{Href: origin + "/api/public/pages/" + page.Slug + "/feed.rss", Rel: "self", Type: "application/rss+xml"},
		},
	}
	for _, inc := range toPublicIncidents(s.visibleIncidents(page, list)) {
		if inc.UpdatedAt > updated {
			updated = inc.UpdatedAt
		}
		var b strings.Builder
		for i := len(inc.Updates) - 1; i >= 0; i-- { // newest update first
			u := inc.Updates[i]
			if u.Status != "" {
				b.WriteString(strings.ToUpper(u.Status[:1]) + u.Status[1:])
			}
			b.WriteString(" — " + time.UnixMilli(u.CreatedAt).UTC().Format("2006-01-02 15:04 UTC") + "\n")
			b.WriteString(u.Body + "\n\n")
		}
		link := base + "/incidents/" + strconv.FormatInt(inc.ID, 10)
		feed.Channel.Items = append(feed.Channel.Items, rssItem{
			Title:       "[" + inc.Status + "] " + inc.Title,
			Link:        link,
			GUID:        rssGUID{IsPermaLink: true, Value: link},
			PubDate:     rssTime(inc.UpdatedAt),
			Description: strings.TrimSpace(b.String()),
		})
	}
	feed.Channel.LastBuildDate = rssTime(updated)
	out, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "feed failed", nil)
		return
	}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	if page.Visibility == "private" {
		w.Header().Set("Cache-Control", "private, no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=60")
	}
	body := append([]byte(xml.Header), out...)
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
