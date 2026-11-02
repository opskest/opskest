package check

import (
	"crypto/tls"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// AppTimeMs returns the processing time the responding app reports about
// itself, in ms, or -1. Knowing it splits "server wait" into the app's own
// work and everything in front of it (load balancer, proxy, queueing).
// Sources, most specific first: Server-Timing (a total/app/backend/origin
// entry, else the largest), Envoy's upstream time, X-Response-Time,
// X-Runtime (Rails, seconds), and a few common custom headers.
func AppTimeMs(h http.Header) int {
	if v := h.Values("Server-Timing"); len(v) > 0 {
		if ms, ok := serverTiming(strings.Join(v, ",")); ok {
			return ms
		}
	}
	if v := h.Get("X-Envoy-Upstream-Service-Time"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
			return n
		}
	}
	for _, k := range []string{"X-Response-Time", "X-Processing-Time", "X-Request-Duration", "X-Upstream-Response-Time", "X-App-Time"} {
		if ms, ok := parseDuration(h.Get(k), 1); ok {
			return ms
		}
	}
	if ms, ok := parseDuration(h.Get("X-Runtime"), 1000); ok { // seconds
		return ms
	}
	return -1
}

var stDur = regexp.MustCompile(`(?i)dur=([0-9.]+)`)

func serverTiming(v string) (int, bool) {
	best, found := -1.0, false
	for _, entry := range strings.Split(v, ",") {
		m := stDur.FindStringSubmatch(entry)
		if m == nil {
			continue
		}
		d, err := strconv.ParseFloat(m[1], 64)
		if err != nil || d < 0 {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(strings.SplitN(entry, ";", 2)[0]))
		switch name {
		case "total", "app", "backend", "origin", "upstream", "server":
			return int(d + 0.5), true
		}
		if d > best {
			best, found = d, true
		}
	}
	return int(best + 0.5), found
}

// parseDuration reads "123", "123ms", "0.123s" or "123.4 ms". A bare
// number is multiplied by unitMs (1 = already ms, 1000 = seconds).
func parseDuration(v string, unitMs float64) (int, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return 0, false
	}
	mult := unitMs
	switch {
	case strings.HasSuffix(v, "ms"):
		v, mult = strings.TrimSpace(strings.TrimSuffix(v, "ms")), 1
	case strings.HasSuffix(v, "µs") || strings.HasSuffix(v, "us"):
		v, mult = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(v, "µs"), "us")), 0.001
	case strings.HasSuffix(v, "s"):
		v, mult = strings.TrimSpace(strings.TrimSuffix(v, "s")), 1000
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 || f*mult > 3_600_000 {
		return 0, false
	}
	return int(f*mult + 0.5), true
}

var cfRayPOP = regexp.MustCompile(`-([A-Z]{3})$`)

// EdgeOf describes who answered, from response headers: the CDN and its
// POP, cache status, and the load balancer / web server, joined outermost
// first, e.g. "Cloudflare BOM · cache MISS · nginx". Empty when nothing
// identifies it.
func EdgeOf(h http.Header) string {
	var parts []string
	add := func(s string) {
		for _, p := range parts {
			if p == s {
				return
			}
		}
		parts = append(parts, s)
	}
	server := strings.ToLower(h.Get("Server"))

	switch {
	case h.Get("Cf-Ray") != "":
		if m := cfRayPOP.FindStringSubmatch(strings.TrimSpace(h.Get("Cf-Ray"))); m != nil {
			add("Cloudflare " + m[1])
		} else {
			add("Cloudflare")
		}
	case h.Get("X-Amz-Cf-Pop") != "" || strings.Contains(strings.ToLower(h.Get("Via")), "cloudfront"):
		if pop := h.Get("X-Amz-Cf-Pop"); pop != "" {
			add("CloudFront " + strings.ToUpper(pop[:min(3, len(pop))]))
		} else {
			add("CloudFront")
		}
	case strings.Contains(strings.ToLower(h.Get("X-Served-By")), "cache-"):
		add("Fastly")
	case h.Get("X-Vercel-Id") != "":
		add("Vercel")
	case h.Get("X-Nf-Request-Id") != "":
		add("Netlify")
	case strings.Contains(strings.ToLower(h.Get("Via")), "google") || server == "gws" || server == "gfe":
		add("Google Front End")
	case h.Get("X-Azure-Ref") != "":
		add("Azure Front Door")
	}

	cache := strings.ToUpper(firstNonEmpty(h.Get("Cf-Cache-Status"), h.Get("X-Cache"), h.Get("X-Cache-Status")))
	switch {
	case strings.Contains(cache, "HIT"):
		add("cache HIT")
	case strings.Contains(cache, "MISS"), strings.Contains(cache, "DYNAMIC"), strings.Contains(cache, "BYPASS"), strings.Contains(cache, "EXPIRED"):
		add("cache MISS")
	}

	switch {
	case strings.HasPrefix(server, "awselb"):
		add("AWS ALB")
	case server == "elb" || h.Get("X-Amzn-Trace-Id") != "" && server == "":
		add("AWS load balancer")
	case strings.HasPrefix(server, "cloudflare"):
		add("Cloudflare")
	case strings.Contains(server, "openresty"):
		add("OpenResty")
	case strings.HasPrefix(server, "nginx"):
		add("nginx")
	case strings.HasPrefix(server, "envoy") || h.Get("X-Envoy-Upstream-Service-Time") != "":
		add("Envoy")
	case strings.HasPrefix(server, "apache"):
		add("Apache")
	case strings.HasPrefix(server, "caddy"):
		add("Caddy")
	case strings.Contains(server, "iis"):
		add("IIS")
	case strings.HasPrefix(server, "amazons3"):
		add("Amazon S3")
	case server != "" && len(server) <= 30:
		add(h.Get("Server"))
	}
	out := strings.Join(parts, " · ")
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// connInfo summarises the connection a check used: server IP, HTTP
// version, TLS version and session resumption, e.g.
// "13.235.1.20 · HTTP/2 · TLS 1.3". TLS 1.2 costs two round trips to
// handshake, TLS 1.3 one — which is what makes a handshake "slow" or not.
func connInfo(ip string, resp *http.Response) string {
	var parts []string
	if ip != "" {
		parts = append(parts, ip)
	}
	switch resp.ProtoMajor {
	case 2:
		parts = append(parts, "HTTP/2")
	case 3:
		parts = append(parts, "HTTP/3")
	default:
		parts = append(parts, resp.Proto)
	}
	if st := resp.TLS; st != nil {
		switch st.Version {
		case tls.VersionTLS13:
			parts = append(parts, "TLS 1.3")
		case tls.VersionTLS12:
			parts = append(parts, "TLS 1.2")
		case tls.VersionTLS11:
			parts = append(parts, "TLS 1.1")
		case tls.VersionTLS10:
			parts = append(parts, "TLS 1.0")
		}
		if st.DidResume {
			parts = append(parts, "resumed")
		}
	}
	return strings.Join(parts, " · ")
}

// snippetReadBytes is how much of a response is kept for the alert
// snippet when the check itself doesn't need the body.
const snippetReadBytes = 2048

// bodySnippet returns the start of a textual response (HTML, JSON, XML,
// plain text), at most 500 bytes on a rune boundary with blank-line runs
// collapsed, for showing in an alert — or "" for binary content.
func bodySnippet(body []byte, contentType string) string {
	if len(body) == 0 {
		return ""
	}
	ct := strings.ToLower(contentType)
	textual := ct == "" || strings.HasPrefix(ct, "text/") || strings.Contains(ct, "json") ||
		strings.Contains(ct, "xml") || strings.Contains(ct, "javascript") || strings.Contains(ct, "html")
	if !textual {
		return ""
	}
	if len(body) > 600 {
		body = body[:600]
	}
	for !utf8.Valid(body) && len(body) > 0 { // drop a rune cut in half
		body = body[:len(body)-1]
	}
	if len(body) == 0 || strings.ContainsRune(string(body), 0) {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if strings.TrimSpace(l) == "" {
			if blank {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, l)
	}
	s := strings.TrimSpace(strings.Join(out, "\n"))
	if len(s) > 500 {
		cut := 500
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "…"
	}
	return s
}
