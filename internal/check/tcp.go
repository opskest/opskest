package check

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"regexp"
	"time"

	"opskest/internal/model"
)

func init() {
	Register(model.TypeTCP, tcpChecker{})
}

// tcpExpectRead caps how much of a reply is read while waiting for the
// expected text.
const tcpExpectRead = 8 << 10

// tcpChecker implements Checker for model.TypeTCP. m.Target is "host:port".
// When Settings.TLS is set, it performs a TLS handshake over the connection
// (verified unless Settings.IgnoreTLS) and records the leaf certificate.
// When Settings.Send or Settings.Expect is set it then speaks to the server:
// it writes Send (text, or "hex:" bytes) and waits for a reply containing
// Expect (text, or a regular expression with ExpectMode regex). The reply is
// never put in a failure reason.
type tcpChecker struct{}

func (tcpChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	start := time.Now()
	s := m.Settings
	exchange := s.Send != "" || s.Expect != ""

	if !s.TLS {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", m.Target)
		if err != nil {
			return classifyDialErr(m, err)
		}
		defer conn.Close()
		if exchange {
			return tcpExchange(ctx, m, conn, start, nil)
		}
		return model.Result{OK: true, LatencyMs: msSince(start)}
	}

	cfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: s.IgnoreTLS}
	if host, _, err := net.SplitHostPort(m.Target); err == nil {
		cfg.ServerName = host
	}
	dialer := &tls.Dialer{NetDialer: &net.Dialer{}, Config: cfg}
	conn, err := dialer.DialContext(ctx, "tcp", m.Target)
	if err != nil {
		return classifyDialErr(m, err)
	}
	defer conn.Close()

	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		// Unreachable in practice: tls.Dialer.DialContext always returns
		// a *tls.Conn on success.
		return model.Result{OK: true, LatencyMs: msSince(start)}
	}
	cs := tlsConn.ConnectionState()
	cert := certInfoFromConnState(&cs)
	if exchange {
		return tcpExchange(ctx, m, conn, start, cert)
	}

	return model.Result{
		OK:        true,
		LatencyMs: msSince(start),
		Cert:      cert,
	}
}

// tcpExchange writes the monitor's Send and waits until what the server
// returns satisfies Expect.
func tcpExchange(ctx context.Context, m *model.Monitor, conn net.Conn, start time.Time, cert *model.CertInfo) model.Result {
	s := m.Settings
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if s.Send != "" {
		payload, err := udpPayload(s.Send)
		if err != nil {
			return protoFail("the payload to send is not valid hex")
		}
		if _, err := conn.Write(payload); err != nil {
			return ioFail(m, err)
		}
	}
	if s.Expect == "" {
		return model.Result{OK: true, LatencyMs: msSince(start), Cert: cert}
	}

	var matches func([]byte) bool
	if s.ExpectMode == "regex" {
		re, err := regexp.Compile(s.Expect)
		if err != nil {
			return protoFail("the expected pattern is not a valid regular expression")
		}
		matches = re.Match
	} else {
		want, err := udpPayload(s.Expect)
		if err != nil {
			return protoFail("the expected text is not valid hex")
		}
		matches = func(b []byte) bool { return bytes.Contains(b, want) }
	}

	var acc []byte
	buf := make([]byte, 2048)
	for {
		n, err := conn.Read(buf)
		acc = append(acc, buf[:n]...)
		if matches(acc) {
			return model.Result{OK: true, LatencyMs: msSince(start), Cert: cert}
		}
		if err != nil {
			if len(acc) > 0 && !isTimeout(err) {
				return protoFail(fmt.Sprintf("the reply did not contain the expected text (%d bytes received)", len(acc)))
			}
			return ioFail(m, err)
		}
		if len(acc) >= tcpExpectRead {
			return protoFail(fmt.Sprintf("the expected text was not in the first %d bytes of the reply", tcpExpectRead))
		}
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// classifyDialErr builds a failure Result for a TCP/TLS dial error.
func classifyDialErr(m *model.Monitor, err error) model.Result {
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

// msSince returns the elapsed time since start in whole milliseconds.
func msSince(start time.Time) int {
	return int(time.Since(start).Milliseconds())
}
