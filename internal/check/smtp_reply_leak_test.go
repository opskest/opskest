package check

import (
	"bufio"
	"net"
	"strings"
	"testing"

	"opskest/internal/model"
)

// Service monitors never put a server reply into a failure reason (see .github/CONTRIBUTING.md):
// the reasons end up in alerts, the status events and the logs. net/textproto
// errors carry the server's own words, so the SMTP checker must not echo them.
func TestSMTPFailureReasonNeverEchoesTheServer(t *testing.T) {
	const secret = "SECRET-SERVER-WORDING"
	check := func(name string, handler func(net.Conn), tweak func(*model.Monitor)) {
		t.Helper()
		addr := serveTCP(t, handler)
		r := runService(t, model.TypeSMTP, addr, tweak)
		if r.OK {
			t.Fatalf("%s: expected a failure, got %+v", name, r)
		}
		if strings.Contains(r.Reason, secret) {
			t.Fatalf("%s: the failure reason repeats the server's reply: %q", name, r.Reason)
		}
		if r.ErrClass != model.ErrAssertion {
			t.Fatalf("%s: errClass %q, want assertion (reason %q)", name, r.ErrClass, r.Reason)
		}
	}

	check("malformed greeting", func(c net.Conn) { c.Write([]byte(secret + "\r\n")) }, nil)
	check("greeting refused", func(c net.Conn) { c.Write([]byte("554 " + secret + "\r\n")) }, nil)
	check("EHLO rejected", func(c net.Conn) {
		r := bufio.NewReader(c)
		c.Write([]byte("220 hi\r\n"))
		r.ReadString('\n')
		c.Write([]byte("502 " + secret + "\r\n"))
	}, nil)
	check("STARTTLS refused", func(c net.Conn) {
		r := bufio.NewReader(c)
		c.Write([]byte("220 hi\r\n"))
		r.ReadString('\n') // EHLO
		c.Write([]byte("250-hi\r\n250 STARTTLS\r\n"))
		r.ReadString('\n') // STARTTLS
		c.Write([]byte("454 " + secret + "\r\n"))
	}, func(m *model.Monitor) { m.Settings.Security = "starttls" })
}
