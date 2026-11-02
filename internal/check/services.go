package check

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	"opskest/internal/model"
)

// Service checkers: each speaks just enough of a protocol to prove the
// server is up and answering correctly, using only the standard library.
// They never authenticate beyond what the monitor is configured with, and
// they never put credentials or server replies into a failure reason.

func init() {
	Register(model.TypeTLS, tlsChecker{})
	Register(model.TypeSMTP, smtpChecker{})
	Register(model.TypeSSH, sshChecker{})
	Register(model.TypeRedis, redisChecker{})
	Register(model.TypePostgres, postgresChecker{})
	Register(model.TypeMySQL, mysqlChecker{})
	Register(model.TypeMongoDB, mongoChecker{})
	Register(model.TypeMQTT, mqttChecker{})
	Register(model.TypeNTP, ntpChecker{})
	Register(model.TypeUDP, udpChecker{})
}

// ── helpers ───────────────────────────────────────────────────────

// protoFail is a failure Result for a server that answered, but not the
// way the protocol says it should.
func protoFail(reason string) model.Result {
	return model.Result{LatencyMs: -1, ErrClass: model.ErrAssertion, Reason: model.TrimReason(reason)}
}

// ioFail classifies a read or write error mid-conversation.
func ioFail(m *model.Monitor, err error) model.Result {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return protoFail("connection closed by the server")
	}
	return classifyDialErr(m, err)
}

// dialService connects to m.Target (host:port), wrapping the connection in
// TLS when useTLS is set. Every read and write on the returned connection
// is bounded by the monitor's timeout.
func dialService(ctx context.Context, m *model.Monitor, useTLS bool) (net.Conn, *model.CertInfo, error) {
	var d net.Dialer
	tcp, err := d.DialContext(ctx, "tcp", m.Target)
	if err != nil {
		return nil, nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = tcp.SetDeadline(dl)
	}
	raw := &capConn{Conn: tcp, left: maxServiceRead}
	if !useTLS {
		return raw, nil, nil
	}
	host, _, _ := net.SplitHostPort(m.Target)
	conn := tls.Client(raw, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host, InsecureSkipVerify: m.Settings.IgnoreTLS})
	if err := conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, nil, err
	}
	cs := conn.ConnectionState()
	return conn, certInfoFromConnState(&cs), nil
}

// maxServiceRead is how much a service checker will read from one server in
// total. A health check needs a greeting or one reply; bufio's ReadString and
// net/smtp's line reader otherwise buffer without limit until they see a
// newline, so a hostile or broken server that streams bytes could use gigabytes
// of memory before the check's timeout.
const maxServiceRead = 4 << 20

var errServerFlood = errors.New("the server sent far more data than a health check expects")

// capConn fails reads once maxServiceRead bytes have arrived.
type capConn struct {
	net.Conn
	left int64
}

func (c *capConn) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, errServerFlood
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.Conn.Read(p)
	c.left -= int64(n)
	return n, err
}

func okResult(start time.Time, cert *model.CertInfo) model.Result {
	return model.Result{OK: true, LatencyMs: msSince(start), Cert: cert}
}

// withDefaultPort appends port when target has none.
func withDefaultPort(target, port string) string {
	if _, _, err := net.SplitHostPort(target); err == nil {
		return target
	}
	return net.JoinHostPort(strings.Trim(target, "[]"), port)
}

// ── TLS certificate ───────────────────────────────────────────────

// tlsChecker verifies a TLS endpoint and tracks its certificate expiry.
// m.Target is host or host:port (443 by default). Certificate warnings use
// the monitor's SSLWarnDays through the shared degradation rules.
type tlsChecker struct{}

func (tlsChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	c := m.Clone()
	c.Target = withDefaultPort(m.Target, "443")
	c.Settings.TLS = true
	return tcpChecker{}.Check(ctx, c)
}

// ── SMTP ──────────────────────────────────────────────────────────

// smtpChecker connects, reads the greeting, says EHLO and, for the
// "starttls" security mode, upgrades the connection. Settings.Security is
// none, starttls or tls (implicit TLS).
type smtpChecker struct{}

func (smtpChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	start := time.Now()
	sec := m.Settings.Security
	conn, cert, err := dialService(ctx, m, sec == "tls")
	if err != nil {
		return classifyDialErr(m, err)
	}
	defer conn.Close()

	host, _, _ := net.SplitHostPort(m.Target)
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return protoFail("no SMTP greeting: " + smtpReason(err))
	}
	defer c.Close()
	if err := c.Hello("opskest.local"); err != nil {
		return protoFail("EHLO rejected: " + smtpReason(err))
	}
	if sec == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return protoFail("server does not offer STARTTLS")
		}
		if err := c.StartTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: host, InsecureSkipVerify: m.Settings.IgnoreTLS}); err != nil {
			var te *textproto.Error
			if errors.As(err, &te) { // the server refused: its text stays out of the reason
				return protoFail("STARTTLS refused: " + smtpReason(err))
			}
			return classifyDialErr(m, err)
		}
		if cs, ok := c.TLSConnectionState(); ok {
			cert = certInfoFromConnState(&cs)
		}
	}
	_ = c.Quit()
	return okResult(start, cert)
}

// smtpReason describes an SMTP protocol error without repeating what the
// server said: net/textproto errors carry the server's own text (and, for a
// malformed greeting, the whole line), and service monitors never put a
// server reply into a failure reason (see .github/CONTRIBUTING.md).
func smtpReason(err error) string {
	var te *textproto.Error
	var pe textproto.ProtocolError
	switch {
	case errors.As(err, &te):
		return fmt.Sprintf("the server answered with code %d", te.Code)
	case errors.As(err, &pe):
		return "the reply is not valid SMTP"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "connection closed by the server"
	}
	return "the conversation failed"
}

// shortErr trims a protocol error to its first line.
func shortErr(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// ── SSH ───────────────────────────────────────────────────────────

// sshChecker reads the server's identification string ("SSH-2.0-…").
type sshChecker struct{}

func (sshChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	start := time.Now()
	conn, _, err := dialService(ctx, m, false)
	if err != nil {
		return classifyDialErr(m, err)
	}
	defer conn.Close()
	line, err := bufio.NewReaderSize(conn, 512).ReadString('\n')
	if err != nil && line == "" {
		return ioFail(m, err)
	}
	if !strings.HasPrefix(line, "SSH-") {
		return protoFail("not an SSH server: no SSH- identification string")
	}
	return okResult(start, nil)
}

// ── Redis ─────────────────────────────────────────────────────────

// redisChecker sends PING, authenticating first when a password is set
// (Settings.BasicUser is the optional ACL user, Secrets.BasicPass the
// password). Settings.TLS wraps the connection.
type redisChecker struct{}

func respCommand(args ...string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	return b.Bytes()
}

func (redisChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	start := time.Now()
	conn, cert, err := dialService(ctx, m, m.Settings.TLS)
	if err != nil {
		return classifyDialErr(m, err)
	}
	defer conn.Close()
	r := bufio.NewReader(conn)

	roundTrip := func(args ...string) (string, error) {
		if _, err := conn.Write(respCommand(args...)); err != nil {
			return "", err
		}
		line, err := r.ReadString('\n')
		return strings.TrimRight(line, "\r\n"), err
	}

	if pw := m.Secrets.BasicPass; pw != "" {
		args := []string{"AUTH", pw}
		if u := m.Settings.BasicUser; u != "" {
			args = []string{"AUTH", u, pw}
		}
		reply, err := roundTrip(args...)
		if err != nil {
			return ioFail(m, err)
		}
		if !strings.HasPrefix(reply, "+OK") {
			return protoFail("authentication was refused")
		}
	}
	reply, err := roundTrip("PING")
	if err != nil {
		return ioFail(m, err)
	}
	switch {
	case strings.HasPrefix(reply, "+PONG"):
		return okResult(start, cert)
	case strings.HasPrefix(reply, "-NOAUTH"):
		return protoFail("the server requires a password")
	default:
		return protoFail("unexpected reply to PING")
	}
}

// ── PostgreSQL ────────────────────────────────────────────────────

// postgresChecker sends an SSLRequest, the first message of every
// connection: a live server answers with a single 'S' or 'N'. No login is
// attempted, so no credentials are needed and nothing lands in its logs.
type postgresChecker struct{}

func (postgresChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	start := time.Now()
	conn, _, err := dialService(ctx, m, false)
	if err != nil {
		return classifyDialErr(m, err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte{0, 0, 0, 8, 0x04, 0xd2, 0x16, 0x2f}); err != nil {
		return ioFail(m, err)
	}
	var b [1]byte
	if _, err := io.ReadFull(conn, b[:]); err != nil {
		return ioFail(m, err)
	}
	if b[0] != 'S' && b[0] != 'N' {
		return protoFail("not a PostgreSQL server")
	}
	return okResult(start, nil)
}

// ── MySQL / MariaDB ───────────────────────────────────────────────

// mysqlChecker reads the server's initial handshake packet, which it sends
// unprompted. No login is attempted.
type mysqlChecker struct{}

func (mysqlChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	start := time.Now()
	conn, _, err := dialService(ctx, m, false)
	if err != nil {
		return classifyDialErr(m, err)
	}
	defer conn.Close()
	var hdr [5]byte // 3-byte length, sequence id, first payload byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return ioFail(m, err)
	}
	switch hdr[4] {
	case 0x0a:
		return okResult(start, nil)
	case 0xff:
		return protoFail("the server refused the connection")
	default:
		return protoFail("not a MySQL or MariaDB server")
	}
}

// ── MongoDB ───────────────────────────────────────────────────────

// mongoChecker sends an isMaster command (allowed without login) and
// requires ok: 1 in the reply. Settings.TLS wraps the connection.
type mongoChecker struct{}

func mongoIsMaster() []byte {
	var doc bytes.Buffer
	doc.Write([]byte{0, 0, 0, 0})   // length, patched below
	doc.WriteByte(0x10)             // int32
	doc.WriteString("isMaster\x00") //
	_ = binary.Write(&doc, binary.LittleEndian, int32(1))
	doc.WriteByte(0x02) // string
	doc.WriteString("$db\x00")
	_ = binary.Write(&doc, binary.LittleEndian, int32(len("admin")+1))
	doc.WriteString("admin\x00")
	doc.WriteByte(0)
	binary.LittleEndian.PutUint32(doc.Bytes(), uint32(doc.Len()))

	var msg bytes.Buffer
	_ = binary.Write(&msg, binary.LittleEndian, int32(16+4+1+doc.Len()))
	_ = binary.Write(&msg, binary.LittleEndian, int32(1)) // request id
	_ = binary.Write(&msg, binary.LittleEndian, int32(0)) // response to
	_ = binary.Write(&msg, binary.LittleEndian, int32(2013))
	_ = binary.Write(&msg, binary.LittleEndian, uint32(0)) // flags
	msg.WriteByte(0)                                       // section kind 0
	msg.Write(doc.Bytes())
	return msg.Bytes()
}

func (mongoChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	start := time.Now()
	conn, cert, err := dialService(ctx, m, m.Settings.TLS)
	if err != nil {
		return classifyDialErr(m, err)
	}
	defer conn.Close()
	if _, err := conn.Write(mongoIsMaster()); err != nil {
		return ioFail(m, err)
	}
	var hdr [16]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return ioFail(m, err)
	}
	n := int(binary.LittleEndian.Uint32(hdr[0:4]))
	if n < 16 || n > 1<<20 || binary.LittleEndian.Uint32(hdr[12:16]) != 2013 {
		return protoFail("not a MongoDB server")
	}
	body := make([]byte, n-16)
	if _, err := io.ReadFull(conn, body); err != nil {
		return ioFail(m, err)
	}
	// BSON double 1.0 under the key "ok".
	okOne := append([]byte{0x01, 'o', 'k', 0}, 0, 0, 0, 0, 0, 0, 0xf0, 0x3f)
	if !bytes.Contains(body, okOne) {
		return protoFail("the server did not report ok")
	}
	return okResult(start, cert)
}

// ── MQTT ──────────────────────────────────────────────────────────

// mqttChecker performs an MQTT 3.1.1 CONNECT and requires an accepted
// CONNACK. Settings.BasicUser and Secrets.BasicPass are the optional
// credentials; Settings.TLS wraps the connection.
type mqttChecker struct{}

func mqttString(b *bytes.Buffer, s string) {
	_ = binary.Write(b, binary.BigEndian, uint16(len(s)))
	b.WriteString(s)
}

func mqttConnectPacket(user, pass string) []byte {
	var body bytes.Buffer
	mqttString(&body, "MQTT")
	flags := byte(0x02) // clean session
	if user != "" {
		flags |= 0x80
		if pass != "" {
			flags |= 0x40
		}
	}
	body.WriteByte(4) // protocol level 3.1.1
	body.WriteByte(flags)
	_ = binary.Write(&body, binary.BigEndian, uint16(30))
	id := make([]byte, 6)
	_, _ = rand.Read(id)
	mqttString(&body, "opskest-"+hex.EncodeToString(id))
	if user != "" {
		mqttString(&body, user)
		if pass != "" {
			mqttString(&body, pass)
		}
	}
	// Remaining length: MQTT's 7-bit varint.
	var pkt bytes.Buffer
	pkt.WriteByte(0x10)
	for n := body.Len(); ; {
		d := byte(n % 128)
		n /= 128
		if n > 0 {
			d |= 0x80
		}
		pkt.WriteByte(d)
		if n == 0 {
			break
		}
	}
	pkt.Write(body.Bytes())
	return pkt.Bytes()
}

var mqttRefusals = map[byte]string{
	1: "unacceptable protocol version",
	2: "client identifier rejected",
	3: "server unavailable",
	4: "bad username or password",
	5: "not authorized",
}

func (mqttChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	start := time.Now()
	conn, cert, err := dialService(ctx, m, m.Settings.TLS)
	if err != nil {
		return classifyDialErr(m, err)
	}
	defer conn.Close()
	if _, err := conn.Write(mqttConnectPacket(m.Settings.BasicUser, m.Secrets.BasicPass)); err != nil {
		return ioFail(m, err)
	}
	var ack [4]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil {
		return ioFail(m, err)
	}
	if ack[0] != 0x20 || ack[1] != 2 {
		return protoFail("not an MQTT broker")
	}
	if ack[3] != 0 {
		reason, ok := mqttRefusals[ack[3]]
		if !ok {
			reason = "connection refused"
		}
		return protoFail("broker refused the connection: " + reason)
	}
	_, _ = conn.Write([]byte{0xe0, 0}) // DISCONNECT
	return okResult(start, cert)
}

// ── NTP ───────────────────────────────────────────────────────────

// ntpChecker asks an NTP server for the time. With Settings.MaxOffsetMs
// set it also fails when this host's clock is further off than that.
type ntpChecker struct{}

// ntpEpoch is the offset between the NTP (1900) and Unix (1970) epochs.
const ntpEpoch = 2208988800

func ntpTime(b []byte) time.Time {
	sec := binary.BigEndian.Uint32(b[0:4])
	frac := binary.BigEndian.Uint32(b[4:8])
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(int64(sec)-ntpEpoch, int64(float64(frac)/float64(1<<32)*1e9))
}

func (ntpChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	start := time.Now()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", withDefaultPort(m.Target, "123"))
	if err != nil {
		return classifyDialErr(m, err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	req := make([]byte, 48)
	req[0] = 0x23 // LI 0, version 4, mode 3 (client)
	if _, err := conn.Write(req); err != nil {
		return ioFail(m, err)
	}
	resp := make([]byte, 48)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return ioFail(m, err)
	}
	received := time.Now()
	if resp[0]&7 != 4 && resp[0]&7 != 5 {
		return protoFail("not an NTP server reply")
	}
	if resp[1] == 0 {
		return protoFail("the server reported itself unsynchronised (stratum 0)")
	}
	if limit := m.Settings.MaxOffsetMs; limit > 0 {
		if server := ntpTime(resp[40:48]); !server.IsZero() {
			mid := start.Add(received.Sub(start) / 2)
			offset := server.Sub(mid)
			if math.Abs(float64(offset/time.Millisecond)) > float64(limit) {
				return protoFail(fmt.Sprintf("clock is %d ms off the server (limit %d ms)", offset.Milliseconds(), limit))
			}
		}
	}
	return okResult(start, nil)
}

// ── UDP ───────────────────────────────────────────────────────────

// udpChecker sends Settings.Send (text, or "hex:…" for raw bytes) and
// requires a reply containing Settings.Expect. UDP has no handshake, so a
// reply is the only proof the service is alive.
type udpChecker struct{}

func udpPayload(s string) ([]byte, error) {
	if rest, ok := strings.CutPrefix(s, "hex:"); ok {
		return hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(rest), " ", ""))
	}
	return []byte(s), nil
}

func (udpChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	start := time.Now()
	payload, err := udpPayload(m.Settings.Send)
	if err != nil {
		return protoFail("the payload is not valid hex")
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", m.Target)
	if err != nil {
		return classifyDialErr(m, err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if _, err := conn.Write(payload); err != nil {
		return ioFail(m, err)
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return ioFail(m, err)
	}
	want, err := udpPayload(m.Settings.Expect)
	if err != nil {
		want = []byte(m.Settings.Expect)
	}
	if len(want) > 0 && !bytes.Contains(buf[:n], want) {
		return protoFail(fmt.Sprintf("the reply did not contain the expected text (%d bytes received)", n))
	}
	return okResult(start, nil)
}
