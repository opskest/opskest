package model

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ErrClass classifies why a check failed.
type ErrClass string

const (
	ErrNone       ErrClass = ""
	ErrTimeout    ErrClass = "timeout"
	ErrDNS        ErrClass = "dns"
	ErrRefused    ErrClass = "refused"
	ErrTLS        ErrClass = "tls"
	ErrHTTPStatus ErrClass = "http_status"
	ErrAssertion  ErrClass = "assertion"
	ErrNetwork    ErrClass = "network"
	ErrOther      ErrClass = "other"
)

// Timing holds per-phase timings in milliseconds. -1 means not measured.
type Timing struct {
	DNSMs, TCPMs, TLSMs, TTFBMs int
	// WaitMs is request sent → first response byte: time spent by the
	// edge (load balancer / proxy) and the app. AppMs is the app's own
	// processing time when the response reports it (-1 when it doesn't).
	WaitMs, AppMs int
	// Edge says who answered: CDN and POP, load balancer, web server.
	Edge string
	// Conn is "ip · HTTP/2 · TLS 1.3[ · resumed]" for the connection used.
	Conn string
}

// CertInfo describes a TLS certificate observed during a check.
type CertInfo struct {
	NotAfter time.Time
	Issuer   string
	Subject  string
}

// Result is what a single check produces, before the engine folds it into
// the monitor's state machine.
type Result struct {
	At        time.Time
	OK        bool
	Degraded  bool
	LatencyMs int // -1 = none
	Code      int // 0 = none
	Timing    *Timing
	Reason    string // ≤200 runes; "" when OK && !Degraded
	ErrClass  ErrClass
	Cert      *CertInfo
	LossPct   float64 // ping only
	// Body is a short snippet of a text response (≤500 bytes), for alert
	// messages only: it is never logged or stored.
	Body string
}

const maxReasonRunes = 200

// TrimReason collapses runs of whitespace to a single space, trims the
// ends, and caps the result at 200 runes, appending "…" when truncated.
func TrimReason(s string) string {
	var b strings.Builder
	inSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			inSpace = true
			continue
		}
		if inSpace && b.Len() > 0 {
			b.WriteByte(' ')
		}
		inSpace = false
		b.WriteRune(r)
	}
	out := b.String()
	if utf8.RuneCountInString(out) <= maxReasonRunes {
		return out
	}
	runes := []rune(out)
	return string(runes[:maxReasonRunes-1]) + "…"
}
