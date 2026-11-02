package check

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Globalping runs one measurement on the public Globalping network
// (https://globalping.io) and waits for it to finish. It backs the
// worldwide-reachability tool and multi-region outage confirmation.
// Unauthenticated use is rate limited by Globalping per source IP, so
// callers keep their own budget.

// GlobalpingURL is the API base; tests point it at an httptest server.
var GlobalpingURL = "https://api.globalping.io/v1/measurements"

// GPProbe is one probe's result, normalized across measurement types.
type GPProbe struct {
	Country   string
	City      string
	OK        bool
	Code      int // HTTP status (http only)
	LatencyMs int
	DNSMs     int
	TLSMs     int
	IP        string
	Detail    string // failure detail, trimmed
	Body      []byte // HTTP: start of the response body (for keyword checks)
	Location  string // HTTP: Location header of a redirect response
}

// GPLimit is Globalping's rate-limit state from the latest response.
type GPLimit struct {
	Limit, Remaining int
	ResetAt          time.Time
}

var gpLimit atomic.Pointer[GPLimit]

// GlobalpingLimit returns the latest known rate-limit state (ok=false
// until a measurement has been made).
func GlobalpingLimit() (GPLimit, bool) {
	if l := gpLimit.Load(); l != nil {
		return *l, true
	}
	return GPLimit{}, false
}

func noteLimit(h http.Header) {
	lim, err1 := strconv.Atoi(h.Get("X-RateLimit-Limit"))
	rem, err2 := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	reset, err3 := strconv.Atoi(h.Get("X-RateLimit-Reset"))
	if err1 != nil || err2 != nil || err3 != nil {
		return
	}
	gpLimit.Store(&GPLimit{Limit: lim, Remaining: rem, ResetAt: time.Now().Add(time.Duration(reset) * time.Second)})
}

// GPRequest describes the measurement. Kind is "http" or "ping".
type GPRequest struct {
	Kind      string
	Target    *url.URL // http: full URL; ping: Host() is used
	Countries []string // one probe per country
	Token     string   // optional Globalping API token (higher limits)
}

var gpClient = &http.Client{Timeout: 8 * time.Second}

// Globalping creates the measurement and polls until it finishes or ctx
// ends. Partial results are returned when ctx ends mid-measurement.
func Globalping(ctx context.Context, req GPRequest) ([]GPProbe, error) {
	if req.Target == nil || req.Target.Hostname() == "" {
		return nil, errors.New("globalping: no target")
	}
	locs := make([]map[string]any, 0, len(req.Countries))
	for _, c := range req.Countries {
		locs = append(locs, map[string]any{"country": c, "limit": 1})
	}
	body := map[string]any{"type": req.Kind, "target": req.Target.Hostname(), "locations": locs}
	switch req.Kind {
	case "http":
		u := req.Target
		proto := "HTTPS"
		if u.Scheme == "http" {
			proto = "HTTP"
		}
		path := u.EscapedPath()
		if path == "" {
			path = "/"
		}
		request := map[string]any{"method": "GET", "path": path}
		if u.RawQuery != "" { // the API rejects an empty query
			request["query"] = u.RawQuery
		}
		opts := map[string]any{"protocol": proto, "request": request}
		if p := u.Port(); p != "" {
			if n, err := strconv.Atoi(p); err == nil {
				opts["port"] = n
			}
		}
		body["measurementOptions"] = opts
	case "ping":
		body["measurementOptions"] = map[string]any{"packets": 3}
	case "dns":
		body["measurementOptions"] = map[string]any{"query": map[string]any{"type": "A"}}
	default:
		return nil, fmt.Errorf("globalping: unsupported kind %q", req.Kind)
	}

	b, _ := json.Marshal(body)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, GlobalpingURL, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("User-Agent", "opskest/"+Version)
	if req.Token != "" {
		hreq.Header.Set("Authorization", "Bearer "+req.Token)
	}
	resp, err := gpClient.Do(hreq)
	if err != nil {
		return nil, fmt.Errorf("globalping: %w", err)
	}
	defer resp.Body.Close()
	noteLimit(resp.Header)
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, errors.New("globalping: rate limited")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, fmt.Errorf("globalping: http %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&created); err != nil || created.ID == "" {
		return nil, errors.New("globalping: no measurement id")
	}

	var last []GPProbe
	for {
		select {
		case <-ctx.Done():
			if len(last) > 0 {
				return last, nil
			}
			return nil, fmt.Errorf("globalping: %w", ctx.Err())
		case <-time.After(700 * time.Millisecond):
		}
		probes, done, err := gpPoll(ctx, created.ID)
		if err != nil {
			continue // transient; retry until ctx ends
		}
		last = probes
		if done {
			return probes, nil
		}
	}
}

func gpPoll(ctx context.Context, id string) ([]GPProbe, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GlobalpingURL+"/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", "opskest/"+Version)
	resp, err := gpClient.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("http %d", resp.StatusCode)
	}
	var m struct {
		Status  string `json:"status"`
		Results []struct {
			Probe struct {
				Country string `json:"country"`
				City    string `json:"city"`
			} `json:"probe"`
			Result struct {
				Status          string         `json:"status"`
				RawOutput       string         `json:"rawOutput"`
				RawBody         string         `json:"rawBody"`
				Headers         map[string]any `json:"headers"`
				ResolvedAddress string         `json:"resolvedAddress"`
				StatusCode      int            `json:"statusCode"`
				Timings         struct {
					Total float64 `json:"total"`
					DNS   float64 `json:"dns"`
					TLS   float64 `json:"tls"`
				} `json:"timings"`
				Answers []json.RawMessage `json:"answers"`
				Stats   struct {
					Avg  float64 `json:"avg"`
					Loss float64 `json:"loss"`
					Rcv  int     `json:"rcv"`
				} `json:"stats"`
			} `json:"result"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return nil, false, err
	}
	out := make([]GPProbe, 0, len(m.Results))
	for _, r := range m.Results {
		p := GPProbe{
			Country: strings.ToUpper(r.Probe.Country), City: r.Probe.City,
			Code: r.Result.StatusCode, IP: r.Result.ResolvedAddress, Body: []byte(r.Result.RawBody), Location: headerValue(r.Result.Headers, "location"),
			LatencyMs: int(r.Result.Timings.Total), DNSMs: int(r.Result.Timings.DNS), TLSMs: int(r.Result.Timings.TLS),
		}
		switch {
		case r.Result.Status == "in-progress":
			continue
		case r.Result.Status != "finished":
			p.Detail = gpDetail(r.Result.RawOutput, "probe failed")
		case r.Result.StatusCode > 0:
			p.OK = r.Result.StatusCode < 500
			if !p.OK {
				p.Detail = fmt.Sprintf("HTTP %d", r.Result.StatusCode)
			}
		case r.Result.Stats.Rcv > 0:
			p.OK, p.LatencyMs = true, int(r.Result.Stats.Avg)
		case len(r.Result.Answers) > 0:
			p.OK = true
		default:
			p.Detail = gpDetail(r.Result.RawOutput, "no response")
		}
		out = append(out, p)
	}
	return out, m.Status != "in-progress", nil
}

// gpDetail keeps the last meaningful line of a probe's raw output (curl-
// or ping-style), trimmed — enough to say "connection refused" or
// "timeout" without storing a transcript.
func gpDetail(raw, fallback string) string {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			if len(l) > 160 {
				l = l[:160]
			}
			return l
		}
	}
	return fallback
}

// headerValue reads a header from Globalping's parsed headers (a string,
// or a list of strings for repeated headers), case-insensitively.
func headerValue(h map[string]any, name string) string {
	for k, v := range h {
		if !strings.EqualFold(k, name) {
			continue
		}
		switch x := v.(type) {
		case string:
			return x
		case []any:
			if len(x) > 0 {
				if s, ok := x[0].(string); ok {
					return s
				}
			}
		}
	}
	return ""
}
