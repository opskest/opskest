package api

import (
	"encoding/xml"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The Atom feed is what Slack's /feed app, Teams, Discord bots and feed
// readers consume, so it has to be strictly valid Atom (RFC 4287): required
// feed and entry elements, a self link, entries newest first, text escaped,
// the right content type and cache headers.
func TestPublicFeedIsValidAtom(t *testing.T) {
	f := newPublicFixture(t, "atomok", false, "public")
	env := f.env
	// A second, newer incident whose text tries to break out of the XML.
	pageID := pageIDBySlug(t, env, "atomok")
	r := env.do(http.MethodPost, "/api/incidents", map[string]any{
		"title": `Newer & "worse" <b>`, "impact": "minor", "body": "]]> <!-- x --> & more",
		"pageIds": []int64{pageID}, "monitorIds": []int64{f.monID},
	}, "admin")
	if r.status != http.StatusCreated {
		t.Fatalf("create incident: %d %s", r.status, r.body)
	}

	feed := env.do(http.MethodGet, "/api/public/pages/atomok/feed.atom", nil, "")
	if feed.status != http.StatusOK {
		t.Fatalf("feed: %d", feed.status)
	}
	if ct := feed.header.Get("Content-Type"); !strings.HasPrefix(ct, "application/atom+xml") || !strings.Contains(ct, "utf-8") {
		t.Errorf("content type %q", ct)
	}
	if cc := feed.header.Get("Cache-Control"); !strings.Contains(cc, "public") || !strings.Contains(cc, "max-age") {
		t.Errorf("a public page's feed should be cacheable, got %q", cc)
	}
	etag := feed.header.Get("ETag")
	if etag == "" {
		t.Fatal("feed has no ETag")
	}
	if again := env.doWithHeader(http.MethodGet, "/api/public/pages/atomok/feed.atom", "If-None-Match", etag); again.status != http.StatusNotModified {
		t.Errorf("If-None-Match: %d, want 304", again.status)
	}

	var parsed struct {
		XMLName xml.Name `xml:"http://www.w3.org/2005/Atom feed"`
		Title   string   `xml:"title"`
		ID      string   `xml:"id"`
		Updated string   `xml:"updated"`
		Author  struct {
			Name string `xml:"name"`
		} `xml:"author"`
		Links []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
			Type string `xml:"type,attr"`
		} `xml:"link"`
		Entries []struct {
			Title   string `xml:"title"`
			ID      string `xml:"id"`
			Updated string `xml:"updated"`
			Link    struct {
				Href string `xml:"href,attr"`
			} `xml:"link"`
			Content struct {
				Type string `xml:"type,attr"`
				Body string `xml:",chardata"`
			} `xml:"content"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(feed.body, &parsed); err != nil {
		t.Fatalf("the feed is not well-formed XML: %v\n%s", err, feed.body)
	}
	if parsed.Title == "" || parsed.ID == "" || parsed.Author.Name == "" {
		t.Errorf("feed needs title, id and author: %+v", parsed)
	}
	if _, err := time.Parse(time.RFC3339, parsed.Updated); err != nil {
		t.Errorf("feed updated %q is not RFC 3339", parsed.Updated)
	}
	var self, alt bool
	for _, l := range parsed.Links {
		self = self || (l.Rel == "self" && l.Type == "application/atom+xml" && strings.HasSuffix(l.Href, "/api/public/pages/atomok/feed.atom"))
		alt = alt || (l.Rel == "alternate" && strings.HasSuffix(l.Href, "/status/atomok"))
	}
	if !self || !alt {
		t.Errorf("feed links: %+v (want self and alternate)", parsed.Links)
	}
	if len(parsed.Entries) != 2 {
		t.Fatalf("entries = %d, want 2 (the linked incidents only)", len(parsed.Entries))
	}
	if !strings.Contains(parsed.Entries[0].Title, `Newer & "worse" <b>`) {
		t.Errorf("newest entry must come first, got %q", parsed.Entries[0].Title)
	}
	prev := ""
	for _, e := range parsed.Entries {
		if e.ID == "" || e.Link.Href == "" || e.Content.Type != "text" {
			t.Errorf("entry needs id, link and text content: %+v", e)
		}
		if _, err := time.Parse(time.RFC3339, e.Updated); err != nil {
			t.Errorf("entry updated %q", e.Updated)
		}
		if prev != "" && e.Updated > prev {
			t.Errorf("entries are not newest first: %s after %s", e.Updated, prev)
		}
		prev = e.Updated
		// Nothing internal may be in an entry.
		for _, secret := range []string{"secret-host", "wedged", "HTTP 503"} {
			if strings.Contains(e.Title+e.Content.Body, secret) {
				t.Errorf("entry leaks %q", secret)
			}
		}
	}
	if strings.Contains(string(feed.body), "<b>") || strings.Contains(string(feed.body), "<script>") {
		t.Errorf("markup in incident text must be escaped: %s", feed.body)
	}
}

// The Subscribe menu offers email only when the page can really send it, and
// Slack only when subscriptions are on; the flags are plain booleans.
func TestPublicPageSubscribeFlags(t *testing.T) {
	env := newTestEnv(t)
	subscribePage(t, env, "flags-on", "public", true)
	subscribePage(t, env, "flags-off", "public", false)
	mon := env.createMonitor(httpMonitorIn("no mail", "https://example.com/nm"))
	if r := env.do(http.MethodPost, "/api/pages", map[string]any{
		"slug": "flags-nomail", "title": "No mail", "visibility": "public", "layout": "list", "subscriptionsEnabled": true,
		"items": []map[string]any{{"monitorId": mon.ID, "sort": 0}},
	}, "admin"); r.status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	for slug, want := range map[string][2]bool{"flags-on": {true, true}, "flags-off": {false, false}, "flags-nomail": {false, true}} {
		var body struct {
			Page struct {
				SubscribeEmail bool `json:"subscribeEmail"`
				SubscribeSlack bool `json:"subscribeSlack"`
			} `json:"page"`
		}
		env.do(http.MethodGet, "/api/public/pages/"+slug, nil, "").json(t, &body)
		if got := [2]bool{body.Page.SubscribeEmail, body.Page.SubscribeSlack}; got != want {
			t.Errorf("%s: subscribe flags %v, want %v", slug, got, want)
		}
	}
	// A page that offers no email answers an email request with an error, not a silent 202.
	if r := env.subscribe("flags-nomail", map[string]any{"email": "a@example.com"}); r.status != http.StatusBadRequest {
		t.Errorf("email on a page without an email channel: %d, want 400", r.status)
	}
	// Webhook URLs are never returned by any public endpoint.
	hook := "https://hooks.slack.com/services/T000/B000/SECRETTOKEN"
	_ = env.subscribe("flags-on", map[string]any{"slackWebhookUrl": hook})
	for _, path := range []string{"/api/public/pages/flags-on", "/api/public/pages/flags-on/summary.json", "/api/public/pages/flags-on/feed.atom", "/api/public/pages/flags-on/feed.rss", "/api/public/pages/flags-on/incidents"} {
		if r := env.do(http.MethodGet, path, nil, ""); strings.Contains(string(r.body), "SECRETTOKEN") {
			t.Errorf("%s returns a subscriber's webhook URL", path)
		}
	}
}

// "Open everything on load" is a public, harmless switch; off by default.
func TestPageDefaultExpandedSetting(t *testing.T) {
	env := newTestEnv(t)
	mon := env.createMonitor(httpMonitorIn("fold", "https://example.com/fold"))
	mk := func(slug string, settings map[string]any) {
		t.Helper()
		body := map[string]any{"slug": slug, "title": "Fold", "visibility": "public", "layout": "list", "items": []map[string]any{{"monitorId": mon.ID, "sort": 0}}}
		if settings != nil {
			body["settings"] = settings
		}
		if r := env.do(http.MethodPost, "/api/pages", body, "admin"); r.status != http.StatusCreated {
			t.Fatalf("create %s: %d %s", slug, r.status, r.body)
		}
	}
	mk("fold-default", nil)
	mk("fold-open", map[string]any{"defaultExpanded": true})
	for slug, want := range map[string]bool{"fold-default": false, "fold-open": true} {
		var body struct {
			Page struct {
				Settings struct {
					DefaultExpanded bool `json:"defaultExpanded"`
				} `json:"settings"`
			} `json:"page"`
		}
		env.do(http.MethodGet, "/api/public/pages/"+slug, nil, "").json(t, &body)
		if body.Page.Settings.DefaultExpanded != want {
			t.Errorf("%s: defaultExpanded = %v, want %v", slug, body.Page.Settings.DefaultExpanded, want)
		}
	}
}

func pageIDBySlug(t *testing.T, env *testEnv, slug string) int64 {
	t.Helper()
	var list struct {
		Pages []struct {
			ID   int64  `json:"id"`
			Slug string `json:"slug"`
		} `json:"pages"`
	}
	env.do(http.MethodGet, "/api/pages", nil, "admin").json(t, &list)
	for _, p := range list.Pages {
		if p.Slug == slug {
			return p.ID
		}
	}
	t.Fatalf("no page %q", slug)
	return 0
}

// The RSS 2.0 feed carries the same data as the Atom feed and has to parse
// strictly: channel title, link and description, items newest first, text
// escaped, RSS content type, ETag/304.
func TestPublicFeedIsValidRSS(t *testing.T) {
	f := newPublicFixture(t, "rssok", false, "public")
	env := f.env
	pageID := pageIDBySlug(t, env, "rssok")
	if r := env.do(http.MethodPost, "/api/incidents", map[string]any{
		"title": `Newer & "worse" <b>`, "impact": "minor", "body": "]]> <!-- x --> & more",
		"pageIds": []int64{pageID}, "monitorIds": []int64{f.monID},
	}, "admin"); r.status != http.StatusCreated {
		t.Fatalf("create incident: %d %s", r.status, r.body)
	}
	feed := env.do(http.MethodGet, "/api/public/pages/rssok/feed.rss", nil, "")
	if feed.status != http.StatusOK {
		t.Fatalf("feed: %d", feed.status)
	}
	if ct := feed.header.Get("Content-Type"); !strings.HasPrefix(ct, "application/rss+xml") || !strings.Contains(ct, "utf-8") {
		t.Errorf("content type %q", ct)
	}
	if cc := feed.header.Get("Cache-Control"); !strings.Contains(cc, "public") || !strings.Contains(cc, "max-age") {
		t.Errorf("cache control %q", cc)
	}
	etag := feed.header.Get("ETag")
	if etag == "" {
		t.Fatal("feed has no ETag")
	}
	if again := env.doWithHeader(http.MethodGet, "/api/public/pages/rssok/feed.rss", "If-None-Match", etag); again.status != http.StatusNotModified {
		t.Errorf("If-None-Match: %d, want 304", again.status)
	}
	var parsed struct {
		XMLName xml.Name `xml:"rss"`
		Version string   `xml:"version,attr"`
		Channel struct {
			Title    string `xml:"title"`
			AtomLink struct {
				Href string `xml:"href,attr"`
				Rel  string `xml:"rel,attr"`
			} `xml:"http://www.w3.org/2005/Atom link"`
			Link          string `xml:"link"`
			Description   string `xml:"description"`
			LastBuildDate string `xml:"lastBuildDate"`
			Items         []struct {
				Title   string `xml:"title"`
				Link    string `xml:"link"`
				GUID    string `xml:"guid"`
				PubDate string `xml:"pubDate"`
				Desc    string `xml:"description"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(feed.body, &parsed); err != nil {
		t.Fatalf("the feed is not well-formed XML: %v\n%s", err, feed.body)
	}
	if parsed.Version != "2.0" || parsed.Channel.Title == "" || parsed.Channel.Description == "" || !strings.HasSuffix(parsed.Channel.Link, "/status/rssok") {
		t.Errorf("channel needs version 2.0, title, link and description: %+v", parsed)
	}
	if parsed.Channel.AtomLink.Rel != "self" || !strings.HasSuffix(parsed.Channel.AtomLink.Href, "/api/public/pages/rssok/feed.rss") {
		t.Errorf("channel needs a self link: %+v", parsed.Channel.AtomLink)
	}
	if _, err := time.Parse(time.RFC1123Z, parsed.Channel.LastBuildDate); err != nil {
		t.Errorf("lastBuildDate %q is not RFC 822", parsed.Channel.LastBuildDate)
	}
	if len(parsed.Channel.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(parsed.Channel.Items))
	}
	if !strings.Contains(parsed.Channel.Items[0].Title, `Newer & "worse" <b>`) {
		t.Errorf("newest item must come first, got %q", parsed.Channel.Items[0].Title)
	}
	var prev time.Time
	for i, it := range parsed.Channel.Items {
		at, err := time.Parse(time.RFC1123Z, it.PubDate)
		if err != nil || it.Title == "" || it.Link == "" || it.GUID == "" {
			t.Errorf("item needs title, link, guid and an RFC 822 pubDate: %+v", it)
		}
		if i > 0 && at.After(prev) {
			t.Errorf("items are not newest first")
		}
		prev = at
		for _, secret := range []string{"secret-host", "wedged", "HTTP 503"} {
			if strings.Contains(it.Title+it.Desc, secret) {
				t.Errorf("item leaks %q", secret)
			}
		}
	}
	if strings.Contains(string(feed.body), "<b>") || strings.Contains(string(feed.body), "<script>") {
		t.Errorf("markup in incident text must be escaped: %s", feed.body)
	}
	if r := env.do(http.MethodGet, "/api/public/pages/no-such-page/feed.rss", nil, ""); r.status != http.StatusNotFound {
		t.Errorf("unknown page: %d, want 404", r.status)
	}
}
