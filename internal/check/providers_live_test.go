package check

// Live verification of the provider catalog (web/src/lib/providers.ts).
//
// This test talks to real third-party status pages, so it is skipped unless
// OPSKEST_LIVE_PROVIDERS=1. It is a maintenance tool, not part of CI. See
// Docs.md, "Third-party provider feeds" for how to produce the JSON and how to read the table.
//
//	OPSKEST_LIVE_PROVIDERS=1 OPSKEST_PROVIDERS_JSON=/path/providers.json \
//	  go test ./internal/check -run LiveProviders -count=1 -v
//
// Manners: at most 4 requests in flight, one run, https only (a redirect to
// plain http is refused), no retries, a 10 s timeout per request and an
// identifying User-Agent. Response bodies are never logged or stored; they
// are only sniffed in memory for the shape the checker needs.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"opskest/internal/model"
)

const (
	liveConcurrency = 4
	liveTimeoutMs   = 10000
	liveMaxRedirect = 5
)

type liveProvider struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Category string `json:"category"`
	Provider string `json:"provider"` // statuspage | google | rss, as detectVendorProvider in the web picks it
}

type liveResult struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Category string `json:"category"`
	Provider string `json:"provider"`
	Class    string `json:"class"`
	Detail   string `json:"detail"`
	FinalURL string `json:"finalUrl,omitempty"`
	Reading  string `json:"reading,omitempty"`  // which reader of statuspage.go handled it
	Incident string `json:"incident,omitempty"` // "none", "degraded" or "down": what the feed reported when read
}

// hop is one HTTP round trip made by the checker.
type hop struct {
	url      string
	status   int
	err      error
	location string
	sniffOK  bool // body looked like what the reader wants (statuspage summary only)
}

// recorder is the http.RoundTripper handed to the checker. It records every
// hop, forces https, and sets the User-Agent.
type recorder struct {
	base http.RoundTripper
	mu   sync.Mutex
	hops []*hop
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	h := &hop{url: req.URL.String()}
	r.mu.Lock()
	r.hops = append(r.hops, h)
	r.mu.Unlock()
	if req.URL.Scheme != "https" {
		h.err = errors.New("refused: not https")
		return nil, h.err
	}
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", "opskest/"+Version+" provider-catalog-check (one-off catalog verification)")
	resp, err := r.base.RoundTrip(req)
	if err != nil {
		h.err = err
		return nil, err
	}
	h.status = resp.StatusCode
	h.location = resp.Header.Get("Location")
	if strings.HasSuffix(strings.ToLower(req.URL.Path), "/api/v2/summary.json") && resp.StatusCode/100 == 2 {
		resp.Body = &sniffBody{rc: resp.Body, h: h}
	}
	return resp, nil
}

// sniffBody watches (never keeps or logs) the first MiB of a Statuspage
// summary for the "indicator" key that every real summary has.
type sniffBody struct {
	rc   io.ReadCloser
	h    *hop
	n    int
	tail string
	seen bool
}

func (s *sniffBody) Read(p []byte) (int, error) {
	n, err := s.rc.Read(p)
	if n > 0 && s.n < 1<<20 && !s.seen {
		chunk := s.tail + string(p[:n])
		if strings.Contains(chunk, `"indicator"`) {
			s.seen = true
			s.h.sniffOK = true
		}
		if len(chunk) > 16 {
			chunk = chunk[len(chunk)-16:]
		}
		s.tail = chunk
		s.n += n
	}
	return n, err
}

func (s *sniffBody) Close() error { return s.rc.Close() }

type chain struct {
	orig, final *hop
	hops        []*hop
}

// chains groups hops into request chains: a hop continues the previous chain
// when the previous one answered with a redirect.
func chains(hops []*hop) []*chain {
	var out []*chain
	for _, h := range hops {
		if n := len(out); n > 0 {
			last := out[n-1].final
			if last.err == nil && last.status/100 == 3 && last.location != "" {
				out[n-1].hops = append(out[n-1].hops, h)
				out[n-1].final = h
				continue
			}
		}
		out = append(out, &chain{orig: h, final: h, hops: []*hop{h}})
	}
	return out
}

func bareHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

func isFeedish(target string) bool {
	u, err := url.Parse(target)
	if err != nil {
		return false
	}
	path := strings.ToLower(u.Path)
	return strings.Contains(path, "rss") || strings.Contains(path, "feed") || strings.HasSuffix(path, ".xml") || strings.Contains(u.RawQuery, "rss")
}

// structuredReading names the reader that handled the target, or "" when the
// only thing that handled it was the generic HTML fallback (which just scans
// page text and cannot read incidents).
func structuredReading(target string, cs []*chain) string {
	if len(cs) == 0 {
		return ""
	}
	win := cs[len(cs)-1]
	u, err := url.Parse(win.orig.url)
	if err != nil {
		return ""
	}
	path := strings.ToLower(u.Path)
	switch {
	case strings.HasSuffix(path, "/api/v2/summary.json"):
		if win.final.sniffOK {
			return "statuspage summary API"
		}
		return ""
	case strings.HasSuffix(path, "/incidents.json"):
		return "google incidents.json"
	case strings.HasSuffix(path, "/data/orgs.json"):
		return "meta orgs.json"
	case strings.HasSuffix(path, "/api/v2.0.0/current"):
		return "slack current API"
	case len(cs) == 1 && isFeedish(target):
		return "RSS/Atom feed"
	}
	return ""
}

func errKind(err error) string {
	var dns *net.DNSError
	var ne net.Error
	switch {
	case errors.As(err, &dns):
		return "dns: " + trimLive(dns.Err)
	case errors.As(err, &ne) && ne.Timeout(), errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	msg := err.Error()
	// Drop the URL the net/http wrapper prefixes; keep the cause.
	if i := strings.LastIndex(msg, "\": "); i >= 0 {
		msg = msg[i+3:]
	}
	return trimLive(msg)
}

func trimLive(v any) string {
	s := fmt.Sprint(v)
	if len(s) > 100 {
		s = s[:100]
	}
	return s
}

func classifyLive(target string, handled, down, degraded bool, hops []*hop) (class, detail, finalURL, reading string) {
	if len(hops) == 0 {
		return "UNSUPPORTED_FORMAT", "checker made no request for this URL form", "", ""
	}
	cs := chains(hops)

	// Redirected to another host? Compare each chain's start and end.
	for _, c := range cs {
		if c.final.err == nil && c.final.status/100 == 3 {
			continue
		}
		if len(c.hops) > 1 {
			if loc := c.hops[len(c.hops)-1].url; bareHost(loc) != bareHost(c.orig.url) {
				return "REDIRECTED_ELSEWHERE", redirectDetail(handled, target, cs, fmt.Sprintf("HTTP %d after redirect from %s", c.final.status, bareHost(c.orig.url))), stripQuery(loc), redirectReading(handled, target, cs)
			}
		}
	}
	// A redirect whose follow-up the client refused (http downgrade etc.).
	for _, c := range cs {
		if c.final.err == nil && c.final.status/100 == 3 && c.final.location != "" {
			if lu, err := url.Parse(c.final.location); err == nil {
				if bareHost(c.final.location) != "" && bareHost(c.final.location) != bareHost(c.orig.url) {
					return "REDIRECTED_ELSEWHERE", fmt.Sprintf("HTTP %d redirect not followed", c.final.status), stripQuery(lu.String()), ""
				}
			}
		}
	}

	reading = structuredReading(target, cs)
	if handled {
		if reading != "" {
			return "OK", "", "", reading
		}
		win := cs[len(cs)-1]
		if strings.HasSuffix(strings.ToLower(win.orig.url), "/api/v2/summary.json") {
			return "UNSUPPORTED_FORMAT", "2xx JSON from /api/v2/summary.json but not a Statuspage summary", "", ""
		}
		return "UNSUPPORTED_FORMAT", "only the generic HTML page scan handled it (no incident feed/API found)", "", "generic HTML scan"
	}

	// Not handled: find out why.
	any2xx := false
	allErr := true
	for _, c := range cs {
		if c.final.err == nil {
			allErr = false
			if c.final.status/100 == 2 {
				any2xx = true
			}
		}
	}
	switch {
	case allErr:
		return "UNREACHABLE", errKind(cs[0].final.err), "", ""
	case !any2xx:
		first := cs[0].final
		if first.err != nil {
			first = cs[len(cs)-1].final
		}
		return "BAD_STATUS", fmt.Sprintf("HTTP %d", first.status), "", ""
	}
	return "UNSUPPORTED_FORMAT", "2xx but the checker could not read incidents from it", "", ""
}

// redirectReading is the structured reader that actually produced an answer
// for a redirected entry, or "" (also when only the generic HTML scan did).
func redirectReading(handled bool, target string, cs []*chain) string {
	if !handled {
		return ""
	}
	return structuredReading(target, cs)
}

func redirectDetail(handled bool, target string, cs []*chain, base string) string {
	switch {
	case !handled:
		return base + "; not readable"
	case structuredReading(target, cs) == "":
		return base + "; generic HTML scan only"
	}
	return base + "; readable"
}

func stripQuery(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		u.RawQuery, u.Fragment, u.User = "", "", nil
		return u.String()
	}
	return raw
}

// liveMonitor builds the monitor the app would create for a catalog entry:
// an http monitor flagged as a vendor feed.
func liveMonitor(p liveProvider) *model.Monitor {
	m := model.NewMonitor(model.TypeHTTP)
	m.Name = p.Name
	m.Target = p.URL
	m.TimeoutMs = liveTimeoutMs
	m.Retries = 0
	m.Settings.Vendor = true
	return m
}

func checkLiveProvider(base http.RoundTripper, p liveProvider) liveResult {
	r := liveResult{Name: p.Name, URL: p.URL, Category: p.Category, Provider: p.Provider}
	m := liveMonitor(p)

	pu, err := url.Parse(m.Target)
	if err != nil || pu.Scheme != "https" {
		r.Class, r.Detail = "UNSUPPORTED_FORMAT", "not an https URL"
		return r
	}

	// checkVendor (http.go) builds a client with just a timeout and calls
	// CheckStatusPage; we do the same with an instrumented transport so the
	// hops are observable, and we keep the (handled, down, degraded) answer
	// that checkVendor flattens into "OK".
	rec := &recorder{base: base}
	client := &http.Client{
		Timeout:   time.Duration(m.TimeoutMs) * time.Millisecond,
		Transport: rec,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= liveMaxRedirect {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Duration(m.TimeoutMs)*time.Millisecond)
	defer cancel()
	handled, down, degraded, _ := CheckStatusPage(ctx, client, m.Target)

	rec.mu.Lock()
	hops := append([]*hop(nil), rec.hops...)
	rec.mu.Unlock()

	r.Class, r.Detail, r.FinalURL, r.Reading = classifyLive(m.Target, handled, down, degraded, hops)
	if handled {
		switch {
		case down:
			r.Incident = "down"
		case degraded:
			r.Incident = "degraded"
		default:
			r.Incident = "none"
		}
	}
	return r
}

func TestLiveProviders(t *testing.T) {
	if os.Getenv("OPSKEST_LIVE_PROVIDERS") != "1" {
		t.Skip("set OPSKEST_LIVE_PROVIDERS=1 (and OPSKEST_PROVIDERS_JSON) to fetch real third-party status pages")
	}
	path := os.Getenv("OPSKEST_PROVIDERS_JSON")
	if path == "" {
		t.Fatal("OPSKEST_PROVIDERS_JSON must point at the extracted provider list")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var providers []liveProvider
	if err := json.Unmarshal(raw, &providers); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(providers) == 0 {
		t.Fatal("no providers in JSON")
	}

	base := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     true,
	}
	defer base.CloseIdleConnections()

	results := make([]liveResult, len(providers))
	sem := make(chan struct{}, liveConcurrency)
	var wg sync.WaitGroup
	for i, p := range providers {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, p liveProvider) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = checkLiveProvider(base, p)
		}(i, p)
	}
	wg.Wait()

	counts := map[string]int{}
	var b strings.Builder
	fmt.Fprintf(&b, "\n%-30s %-20s %-40s %s\n", "NAME", "CLASS", "DETAIL", "URL")
	for _, r := range results {
		counts[r.Class]++
		detail := r.Detail
		if r.Reading != "" {
			detail = r.Reading + ", incident: " + r.Incident
			if r.Detail != "" {
				detail += "; " + r.Detail
			}
		}
		shown := r.URL
		if r.FinalURL != "" {
			shown += " -> " + r.FinalURL
		}
		fmt.Fprintf(&b, "%-30s %-20s %-40s %s\n", trunc(r.Name, 28), r.Class, trunc(detail, 60), shown)
	}
	classes := make([]string, 0, len(counts))
	for c := range counts {
		classes = append(classes, c)
	}
	sort.Strings(classes)
	b.WriteString("\nSUMMARY:")
	for _, c := range classes {
		fmt.Fprintf(&b, " %s=%d", c, counts[c])
	}
	fmt.Fprintf(&b, " total=%d\n", len(results))
	t.Log(b.String())

	if out := os.Getenv("OPSKEST_PROVIDERS_OUT"); out != "" {
		data, _ := json.MarshalIndent(results, "", "  ")
		if err := os.WriteFile(out, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "~"
}

// Offline checks of the classifier, so the live tool itself stays honest.
func TestLiveProviderClassifier(t *testing.T) {
	ok := func(u string, st int) *hop { return &hop{url: u, status: st} }
	target := "https://status.example.com/history.rss"

	cases := []struct {
		name    string
		handled bool
		hops    []*hop
		want    string
	}{
		{"feed parsed", true, []*hop{ok(target, 200)}, "OK"},
		{"no request", false, nil, "UNSUPPORTED_FORMAT"},
		{"dns", false, []*hop{{url: target, err: &net.DNSError{Err: "no such host", Name: "x"}}}, "UNREACHABLE"},
		{"404 everywhere", false, []*hop{ok(target, 404), ok("https://status.example.com/api/v2/summary.json", 404)}, "BAD_STATUS"},
		{"200 but unreadable", false, []*hop{ok(target, 200), ok("https://status.example.com/api/v2/summary.json", 200)}, "UNSUPPORTED_FORMAT"},
		{"generic html only", true, []*hop{ok(target, 200), ok("https://status.example.com/api/v2/summary.json", 404), ok(target, 200)}, "UNSUPPORTED_FORMAT"},
		{"redirect elsewhere", true, []*hop{
			{url: target, status: 301, location: "https://other.example.org/history.rss"},
			ok("https://other.example.org/history.rss", 200),
		}, "REDIRECTED_ELSEWHERE"},
		{"www redirect is same host", true, []*hop{
			{url: target, status: 301, location: "https://www.status.example.com/history.rss"},
			ok("https://www.status.example.com/history.rss", 200),
		}, "OK"},
	}
	for _, c := range cases {
		got, _, _, _ := classifyLive(target, c.handled, false, false, c.hops)
		if got != c.want {
			t.Errorf("%s: class = %s, want %s", c.name, got, c.want)
		}
	}
}
