package check

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"time"

	"opskest/internal/model"
)

// maxBodyBytes is the response body read/discard cap: 1 MiB.
const maxBodyBytes = 1 << 20

func init() {
	Register(model.TypeHTTP, httpChecker{})
}

// httpChecker implements Checker for model.TypeHTTP.
type httpChecker struct{}

// redirectLimitError is returned by CheckRedirect once more redirects have
// been followed than the monitor allows. Its message is used verbatim as
// the Result reason.
type redirectLimitError struct{ n int }

func (e *redirectLimitError) Error() string {
	return fmt.Sprintf("stopped after %d redirects", e.n)
}

// traceTimes captures the httptrace timestamps needed to compute
// per-phase timings.
type traceTimes struct {
	dnsStart, dnsDone          time.Time
	connectStart, connectDone  time.Time
	tlsStart, tlsDone          time.Time
	gotFirstByte, wroteRequest time.Time
	remoteIP                   string
}

func (httpChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	if m.Settings.Vendor {
		return checkVendor(ctx, m)
	}
	return checkHTTP(ctx, m)
}

// VendorUnreadableReason is the reason of a vendor feed that could not be fetched or understood.
const VendorUnreadableReason = "Could not read this feed"

// checkVendor is a 3rd-party status feed (Settings.Vendor): it is down or
// degraded only when the vendor itself reports an incident. Failing to
// fetch or understand the feed, a slow response or an odd status code is
// not an incident on the vendor's side, so it never alerts.
func checkVendor(ctx context.Context, m *model.Monitor) model.Result {
	start := time.Now()
	client := &http.Client{Timeout: time.Duration(m.TimeoutMs) * time.Millisecond}
	res := model.Result{OK: true, LatencyMs: -1}
	handled, isDown, isDegraded, reason := CheckStatusPage(ctx, client, m.Target)
	res.LatencyMs = msSince(start)
	switch {
	case !handled:
		// Still no alert (a feed we cannot read says nothing about the vendor), but say so:
		// the providers page shows "Feed unreachable" instead of a green "no incidents".
		res.Reason = VendorUnreadableReason
	case isDown:
		res.OK, res.ErrClass, res.Reason = false, model.ErrOther, model.TrimReason(reason)
	case isDegraded:
		res.Degraded, res.Reason = true, model.TrimReason(reason)
	}
	return res
}

func checkHTTP(ctx context.Context, m *model.Monitor) model.Result {
	start := time.Now()
	s := m.Settings
	var oauthKeyUsed string
	var oauthCached bool

	method := s.Method
	if method == "" {
		method = http.MethodGet
	}

	transport, terr := newHTTPTransport(m)
	if terr != nil {
		return setupFailure(terr)
	}
	defer transport.CloseIdleConnections()

	var bodyReader io.Reader
	if s.BodyType != "" && s.BodyType != "none" && s.Body != "" {
		bodyReader = strings.NewReader(s.Body)
	}

	req, err := http.NewRequestWithContext(ctx, method, m.Target, bodyReader)
	if err != nil {
		return classifyRequestErr(m, err)
	}

	req.Header.Set("User-Agent", "opskest/"+Version)
	applyHeaders(req.Header, m)
	switch s.BodyType {
	case "json":
		req.Header.Set("Content-Type", "application/json")
	case "form":
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	switch s.AuthType {
	case "basic":
		req.SetBasicAuth(s.BasicUser, m.Secrets.BasicPass)
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+m.Secrets.BearerToken)
	case "oauth2":
		tok, key, cached, oerr := oauthToken(ctx, m, transport, time.Now())
		if oerr != nil {
			return model.Result{LatencyMs: -1, ErrClass: model.ErrOther, Reason: model.TrimReason(oerr.Error())}
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		oauthKeyUsed, oauthCached = key, cached
	}

	var tt traceTimes
	trace := &httptrace.ClientTrace{
		DNSStart:          func(httptrace.DNSStartInfo) { tt.dnsStart = time.Now() },
		DNSDone:           func(httptrace.DNSDoneInfo) { tt.dnsDone = time.Now() },
		ConnectStart:      func(string, string) { tt.connectStart = time.Now() },
		ConnectDone:       func(string, string, error) { tt.connectDone = time.Now() },
		TLSHandshakeStart: func() { tt.tlsStart = time.Now() },
		TLSHandshakeDone:  func(tls.ConnectionState, error) { tt.tlsDone = time.Now() },
		WroteRequest:      func(httptrace.WroteRequestInfo) { tt.wroteRequest = time.Now() },
		GotConn: func(info httptrace.GotConnInfo) {
			if info.Conn != nil {
				if a, ok := info.Conn.RemoteAddr().(*net.TCPAddr); ok {
					tt.remoteIP = a.IP.String()
				}
			}
		},
		GotFirstResponseByte: func() { tt.gotFirstByte = time.Now() },
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))

	maxRedirects := 10
	if s.MaxRedirects != nil {
		maxRedirects = *s.MaxRedirects
	}

	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if maxRedirects == 0 {
				return http.ErrUseLastResponse
			}
			if len(via) >= maxRedirects {
				return &redirectLimitError{n: maxRedirects}
			}
			return nil
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return classifyRequestErr(m, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized && oauthCached {
		// A token the server no longer accepts (revoked early): fetch a new one next time.
		oauthForget(oauthKeyUsed)
	}

	needBody := s.Keyword != "" || s.JSONPath != ""
	for _, a := range s.Assertions {
		if a.Source == "body" || a.Source == "json" {
			needBody = true
		}
	}
	var bodyBytes []byte
	if needBody {
		var readErr error
		bodyBytes, readErr = io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
		if readErr != nil {
			return bodyReadErrResult(readErr)
		}
	} else {
		// Keep the start for the alert snippet; discard the rest.
		bodyBytes, _ = io.ReadAll(io.LimitReader(resp.Body, snippetReadBytes))
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
	}

	latency := time.Since(start)

	res := model.Result{
		LatencyMs: int(latency.Milliseconds()),
		Code:      resp.StatusCode,
		Timing: &model.Timing{
			DNSMs:  msOrNeg(tt.dnsStart, tt.dnsDone),
			TCPMs:  msOrNeg(tt.connectStart, tt.connectDone),
			TLSMs:  msOrNeg(tt.tlsStart, tt.tlsDone),
			TTFBMs: msOrNeg(start, tt.gotFirstByte),
			WaitMs: msOrNeg(tt.wroteRequest, tt.gotFirstByte),
			AppMs:  AppTimeMs(resp.Header),
			Edge:   EdgeOf(resp.Header),
			Conn:   connInfo(tt.remoteIP, resp),
		},
	}
	if resp.TLS != nil {
		res.Cert = certInfoFromConnState(resp.TLS)
	}
	res.Body = bodySnippet(bodyBytes, resp.Header.Get("Content-Type"))

	cs, csErr := model.ParseCodes(s.AcceptedCodes)
	if csErr != nil {
		cs, _ = model.ParseCodes("200-299")
	}
	if !cs.Match(resp.StatusCode) {
		res.OK = false
		res.ErrClass = model.ErrHTTPStatus
		res.Reason = model.TrimReason(fmt.Sprintf("HTTP %d (expected %s)", resp.StatusCode, cs.String()))
		return res
	}

	if s.Keyword != "" {
		mode := s.KeywordMode
		if mode == "" {
			mode = "contains"
		}
		if ok, reason := EvalKeyword(bodyBytes, s.Keyword, mode, s.CaseSensitive); !ok {
			res.OK = false
			res.ErrClass = model.ErrAssertion
			res.Reason = model.TrimReason(reason)
			return res
		}
	}

	if s.JSONPath != "" {
		if ok, reason := EvalJSON(bodyBytes, s.JSONPath, s.JSONOp, s.JSONExpected); !ok {
			res.OK = false
			res.ErrClass = model.ErrAssertion
			res.Reason = model.TrimReason(reason)
			return res
		}
	}

	if len(s.Assertions) > 0 {
		if ok, reason := EvalAssertions(s.Assertions, resp.StatusCode, res.LatencyMs, resp.Header, bodyBytes); !ok {
			res.OK = false
			res.ErrClass = model.ErrAssertion
			res.Reason = reason
			return res
		}
	}

	res.OK = true
	return res
}

// classifyRequestErr builds a failure Result for an error that prevented
// completing the request. LatencyMs is left at -1 since no valid response
// was observed.
func classifyRequestErr(m *model.Monitor, err error) model.Result {
	var rle *redirectLimitError
	if errors.As(err, &rle) {
		return model.Result{
			OK:        false,
			LatencyMs: -1,
			ErrClass:  model.ErrOther,
			Reason:    model.TrimReason(rle.Error()),
		}
	}

	class, reason := ClassifyErr(err)
	if class == model.ErrTimeout {
		reason = fmt.Sprintf("timeout after %d ms", m.TimeoutMs)
	}
	return model.Result{
		OK:        false,
		LatencyMs: -1,
		ErrClass:  class,
		Reason:    model.TrimReason(reason),
	}
}

// bodyReadErrResult builds a failure Result for a connection that dropped
// (or otherwise failed) while its body was being read for an assertion.
// This must not be reported as an assertion failure: the body we'd be
// evaluating is partial or missing, so the honest failure is the
// transport error, classified the same way a request-level failure would
// be.
func bodyReadErrResult(err error) model.Result {
	class, _ := ClassifyErr(err)
	return model.Result{
		OK:        false,
		LatencyMs: -1,
		ErrClass:  class,
		Reason:    model.TrimReason(fmt.Sprintf("connection lost while reading response: %s", err.Error())),
	}
}

// msOrNeg returns the millisecond duration between two httptrace
// timestamps, or -1 when either was never recorded (e.g. DNS is skipped
// for an IP-literal target).
func msOrNeg(a, b time.Time) int {
	if a.IsZero() || b.IsZero() {
		return -1
	}
	return int(b.Sub(a).Milliseconds())
}

// applyHeaders copies the monitor's configured headers into h, taking a
// secret header's value from m.Secrets (it is never stored in Settings).
func applyHeaders(h http.Header, m *model.Monitor) {
	for _, hd := range m.Settings.Headers {
		v := hd.Value
		if hd.Secret {
			if sv, ok := m.Secrets.Headers[hd.Name]; ok {
				v = sv
			}
		}
		h.Set(hd.Name, v)
	}
}
