package alert

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"

	"opskest/internal/store"
)

// emailSender speaks SMTP directly (net/smtp): tls_mode "tls" dials
// straight into TLS, "starttls" upgrades after EHLO, and "none" stays
// plaintext. Auth is PLAIN whenever a username is configured.
type emailSender struct{}

func (emailSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	return SendMail(ctx, cfg, cfg.To, buildMIMEMessage(cfg, m))
}

// SendMail runs one SMTP transaction through cfg's server (dial, TLS,
// auth, MAIL FROM cfg.From) delivering the already-rendered RFC 5322
// message msg to rcpts. Errors are sanitized (no credentials). Used by
// alert emails and by status page subscriber mail (internal/notify).
func SendMail(ctx context.Context, cfg store.ChannelConfig, rcpts []string, msg []byte) error {
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))

	conn, err := dialSMTP(ctx, cfg, addr)
	if err != nil {
		return sanitizeSMTPErr(err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	// net/smtp's Client speaks directly to conn with blocking I/O and
	// knows nothing about ctx, so a cancellation with no deadline (e.g.
	// the dispatcher shutting down mid-send) would otherwise leave this
	// call blocked. Closing conn on ctx.Done unblocks any in-flight read
	// or write immediately.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return sanitizeSMTPErr(err)
	}
	defer client.Close()

	if cfg.TLSMode == "starttls" {
		if err := client.StartTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.Host}); err != nil {
			return sanitizeSMTPErr(err)
		}
	}

	if cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return sanitizeSMTPErr(err)
		}
	}

	if err := client.Mail(cfg.From); err != nil {
		return sanitizeSMTPErr(err)
	}
	for _, to := range rcpts {
		if err := client.Rcpt(to); err != nil {
			return sanitizeSMTPErr(err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return sanitizeSMTPErr(err)
	}
	if _, err := w.Write(msg); err != nil {
		return sanitizeSMTPErr(err)
	}
	if err := w.Close(); err != nil {
		return sanitizeSMTPErr(err)
	}
	return sanitizeSMTPErr(client.Quit())
}

// dialSMTP opens the connection, honoring ctx's deadline for the dial
// itself. tls_mode "tls" (implicit TLS, typically port 465) dials
// straight into a TLS handshake; anything else dials plaintext.
func dialSMTP(ctx context.Context, cfg store.ChannelConfig, addr string) (net.Conn, error) {
	dialer := &net.Dialer{}
	if dl, ok := ctx.Deadline(); ok {
		dialer.Deadline = dl
	}
	if cfg.TLSMode == "tls" {
		return tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.Host})
	}
	return dialer.DialContext(ctx, "tcp", addr)
}

// buildMIMEMessage renders m as a multipart/alternative RFC 5322 message
// with a plain-text and an HTML part, both carrying the same content.
func buildMIMEMessage(cfg store.ChannelConfig, m Message) []byte {
	subject := Title(m)
	text, html := buildEmailBody(m)
	const boundary = "opskest-boundary"

	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", cfg.From)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(cfg.To, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n\r\n", boundary, text)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/html; charset=utf-8\r\n\r\n%s\r\n\r\n", boundary, html)
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

func buildEmailBody(m Message) (text, html string) {
	title := Title(m)
	body := bodyLines(m)
	link := linkFor(m)

	text = fmt.Sprintf("%s\n\n%s\n\n%s", title, body, link)
	html = fmt.Sprintf(
		"<p><strong>%s</strong></p><pre>%s</pre><p><a href=%q>%s</a></p>",
		htmlEscape(title), htmlEscape(body), link, htmlEscape(link),
	)
	return text, html
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// sanitizeSMTPErr reduces an SMTP client error to a message safe to log
// . A
// server-rejected command surfaces as *textproto.Error, whose Error()
// already reads exactly like the reply the server sent (e.g. "535 5.7.8
// authentication failed", "550 mailbox unavailable") — server-controlled
// text that never contains the configured password (net/smtp never sends
// it back). A dial/TLS failure's Error() is similarly self-contained
// ("connection refused", "x509: certificate has expired..."). Either way
// the text is redacted, so a value that happens to look like a URL or a
// long token (an API key, say) never reaches alert_log.
func sanitizeSMTPErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return errors.New("smtp request timed out")
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return errors.New("smtp request timed out")
	}
	return errors.New(redact(err.Error()))
}
