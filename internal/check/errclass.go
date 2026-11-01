package check

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strings"

	"opskest/internal/model"
)

// ClassifyErr classifies why a check failed and returns a short,
// human-readable reason to pair with it. It never includes response
// bodies or header values.
func ClassifyErr(err error) (model.ErrClass, string) {
	if err == nil {
		return model.ErrNone, ""
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return model.ErrTimeout, "timeout"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return model.ErrTimeout, "timeout"
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return model.ErrDNS, "dns: " + dnsErr.Err
	}

	if isConnectionRefused(err) {
		return model.ErrRefused, "connection refused"
	}

	var certVerifyErr *tls.CertificateVerificationError
	var unknownAuthErr x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var certInvalidErr x509.CertificateInvalidError
	// The errors.As checks above cover the concrete x509/tls error types
	// known at the time of writing, but the Go standard library has
	// changed how it wraps TLS handshake failures across versions (plain
	// x509 errors historically, *tls.CertificateVerificationError since
	// Go 1.20, and possibly something else in a future toolchain). The
	// "x509:"/"tls:" substring fallback below keeps classification
	// correct even against a wrapper type these checks don't yet know
	// about, at the small cost of also matching an unrelated error whose
	// message happens to contain one of those substrings.
	switch {
	case errors.As(err, &certVerifyErr),
		errors.As(err, &unknownAuthErr),
		errors.As(err, &hostnameErr),
		errors.As(err, &certInvalidErr),
		strings.Contains(err.Error(), "x509:"),
		strings.Contains(err.Error(), "tls:"):
		return model.ErrTLS, "tls: " + err.Error()
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return model.ErrNetwork, opErr.Error()
	}

	return model.ErrOther, err.Error()
}
