package check

import (
	"bufio"
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"

	"opskest/internal/model"
)

// serveTCP runs handler for each connection on an ephemeral port.
func serveTCP(t *testing.T, handler func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				handler(c)
			}()
		}
	}()
	return ln.Addr().String()
}

func runService(t *testing.T, typ model.MonitorType, target string, tweak func(*model.Monitor)) model.Result {
	t.Helper()
	m := model.NewMonitor(typ)
	m.Name, m.Target, m.TimeoutMs = "svc", target, 3000
	if tweak != nil {
		tweak(m)
	}
	if fe := m.Validate(); len(fe) > 0 {
		t.Fatalf("invalid monitor: %v", fe)
	}
	return Run(context.Background(), m, time.Now)
}

func TestSSHChecker(t *testing.T) {
	good := serveTCP(t, func(c net.Conn) { c.Write([]byte("SSH-2.0-OpenSSH_9.6\r\n")) })
	if r := runService(t, model.TypeSSH, good, nil); !r.OK {
		t.Fatalf("good: %+v", r)
	}
	bad := serveTCP(t, func(c net.Conn) { c.Write([]byte("HTTP/1.1 400 Bad Request\r\n")) })
	if r := runService(t, model.TypeSSH, bad, nil); r.OK || r.ErrClass != model.ErrAssertion {
		t.Fatalf("bad: %+v", r)
	}
}

func TestSMTPChecker(t *testing.T) {
	addr := serveTCP(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		c.Write([]byte("220 mail.example.test ESMTP\r\n"))
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"):
				c.Write([]byte("250-mail.example.test\r\n250 8BITMIME\r\n"))
			case strings.HasPrefix(cmd, "QUIT"):
				c.Write([]byte("221 bye\r\n"))
				return
			default:
				c.Write([]byte("502 no\r\n"))
			}
		}
	})
	if r := runService(t, model.TypeSMTP, addr, nil); !r.OK {
		t.Fatalf("plain: %+v", r)
	}
	// The server offers no STARTTLS, so requiring it must fail.
	r := runService(t, model.TypeSMTP, addr, func(m *model.Monitor) { m.Settings.Security = "starttls" })
	if r.OK || !strings.Contains(r.Reason, "STARTTLS") {
		t.Fatalf("starttls: %+v", r)
	}
}

func TestRedisChecker(t *testing.T) {
	addr := serveTCP(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		authed := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if !strings.HasPrefix(line, "*") {
				continue
			}
			var args []string
			for i := 0; i < int(line[1]-'0'); i++ {
				r.ReadString('\n')
				v, _ := r.ReadString('\n')
				args = append(args, strings.TrimSpace(v))
			}
			switch strings.ToUpper(args[0]) {
			case "AUTH":
				if args[len(args)-1] == "s3cret" {
					authed = true
					c.Write([]byte("+OK\r\n"))
				} else {
					c.Write([]byte("-WRONGPASS invalid\r\n"))
				}
			case "PING":
				if authed {
					c.Write([]byte("+PONG\r\n"))
				} else {
					c.Write([]byte("-NOAUTH Authentication required\r\n"))
				}
			}
		}
	})
	if r := runService(t, model.TypeRedis, addr, func(m *model.Monitor) { m.Secrets.BasicPass = "s3cret" }); !r.OK {
		t.Fatalf("authed: %+v", r)
	}
	if r := runService(t, model.TypeRedis, addr, nil); r.OK || !strings.Contains(r.Reason, "password") {
		t.Fatalf("no password: %+v", r)
	}
	r := runService(t, model.TypeRedis, addr, func(m *model.Monitor) { m.Secrets.BasicPass = "wrong" })
	if r.OK || strings.Contains(r.Reason, "wrong") {
		t.Fatalf("wrong password: %+v", r)
	}
}

func TestPostgresAndMySQLCheckers(t *testing.T) {
	pg := serveTCP(t, func(c net.Conn) {
		var b [8]byte
		c.Read(b[:])
		if b[4] == 0x04 && b[5] == 0xd2 {
			c.Write([]byte("N"))
		}
	})
	if r := runService(t, model.TypePostgres, pg, nil); !r.OK {
		t.Fatalf("postgres: %+v", r)
	}
	my := serveTCP(t, func(c net.Conn) { c.Write([]byte{0x20, 0, 0, 0, 0x0a, '8', '.', '0', 0}) })
	if r := runService(t, model.TypeMySQL, my, nil); !r.OK {
		t.Fatalf("mysql: %+v", r)
	}
	denied := serveTCP(t, func(c net.Conn) { c.Write([]byte{0x10, 0, 0, 0, 0xff, 0x6a, 0x04}) })
	if r := runService(t, model.TypeMySQL, denied, nil); r.OK {
		t.Fatalf("mysql refusal should fail: %+v", r)
	}
	other := serveTCP(t, func(c net.Conn) { c.Write([]byte("hello world")) })
	if r := runService(t, model.TypePostgres, other, nil); r.OK {
		t.Fatalf("non-postgres should fail: %+v", r)
	}
}

func TestMongoChecker(t *testing.T) {
	addr := serveTCP(t, func(c net.Conn) {
		hdr := make([]byte, 16)
		if _, err := c.Read(hdr); err != nil {
			return
		}
		n := int(binary.LittleEndian.Uint32(hdr))
		rest := make([]byte, n-16)
		for got := 0; got < len(rest); {
			k, err := c.Read(rest[got:])
			if err != nil {
				return
			}
			got += k
		}
		doc := []byte{0, 0, 0, 0, 0x01, 'o', 'k', 0, 0, 0, 0, 0, 0, 0, 0xf0, 0x3f, 0}
		binary.LittleEndian.PutUint32(doc, uint32(len(doc)))
		body := append([]byte{0, 0, 0, 0, 0}, doc...)
		out := make([]byte, 16)
		binary.LittleEndian.PutUint32(out, uint32(16+len(body)))
		binary.LittleEndian.PutUint32(out[12:], 2013)
		c.Write(append(out, body...))
	})
	if r := runService(t, model.TypeMongoDB, addr, nil); !r.OK {
		t.Fatalf("mongo: %+v", r)
	}
	junk := serveTCP(t, func(c net.Conn) { c.Write(make([]byte, 40)) })
	if r := runService(t, model.TypeMongoDB, junk, nil); r.OK {
		t.Fatalf("junk should fail: %+v", r)
	}
}

func TestMQTTChecker(t *testing.T) {
	mk := func(rc byte) string {
		return serveTCP(t, func(c net.Conn) {
			buf := make([]byte, 256)
			if n, _ := c.Read(buf); n == 0 || buf[0] != 0x10 {
				return
			}
			c.Write([]byte{0x20, 0x02, 0, rc})
		})
	}
	if r := runService(t, model.TypeMQTT, mk(0), nil); !r.OK {
		t.Fatalf("accepted: %+v", r)
	}
	r := runService(t, model.TypeMQTT, mk(4), func(m *model.Monitor) { m.Settings.BasicUser = "u"; m.Secrets.BasicPass = "p" })
	if r.OK || !strings.Contains(r.Reason, "bad username or password") {
		t.Fatalf("refused: %+v", r)
	}
	if got := mqttConnectPacket("u", "p"); got[0] != 0x10 || int(got[1]) != len(got)-2 {
		t.Fatalf("packet: % x", got)
	}
}

func TestNTPChecker(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	skew := make(chan time.Duration, 1)
	skew <- 0
	go func() {
		buf := make([]byte, 48)
		for {
			_, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			s := <-skew
			skew <- s
			resp := make([]byte, 48)
			resp[0], resp[1] = 0x24, 2 // mode 4, stratum 2
			now := time.Now().Add(s)
			binary.BigEndian.PutUint32(resp[40:], uint32(now.Unix()+ntpEpoch))
			binary.BigEndian.PutUint32(resp[44:], uint32(float64(now.Nanosecond())/1e9*float64(1<<32)))
			pc.WriteTo(resp, addr)
		}
	}()
	addr := pc.LocalAddr().String()
	limit := func(m *model.Monitor) { m.Settings.MaxOffsetMs = 500 }
	if r := runService(t, model.TypeNTP, addr, limit); !r.OK {
		t.Fatalf("in sync: %+v", r)
	}
	<-skew
	skew <- 10 * time.Second
	r := runService(t, model.TypeNTP, addr, limit)
	if r.OK || !strings.Contains(r.Reason, "off the server") {
		t.Fatalf("skewed: %+v", r)
	}
	if r := runService(t, model.TypeNTP, addr, nil); !r.OK {
		t.Fatalf("offset check off: %+v", r)
	}
}

func TestUDPChecker(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if string(buf[:n]) == "ping" {
				pc.WriteTo([]byte("pong from service"), addr)
			}
		}
	}()
	addr := pc.LocalAddr().String()
	set := func(send, expect string) func(*model.Monitor) {
		return func(m *model.Monitor) { m.Settings.Send, m.Settings.Expect = send, expect }
	}
	if r := runService(t, model.TypeUDP, addr, set("ping", "pong")); !r.OK {
		t.Fatalf("text: %+v", r)
	}
	if r := runService(t, model.TypeUDP, addr, set("hex:70696e67", "hex:706f6e67")); !r.OK {
		t.Fatalf("hex: %+v", r)
	}
	if r := runService(t, model.TypeUDP, addr, set("ping", "nope")); r.OK {
		t.Fatalf("mismatch should fail: %+v", r)
	}
	m := model.NewMonitor(model.TypeUDP)
	m.Name, m.Target = "u", addr
	if fe := m.Validate(); fe["settings.expect"] == "" {
		t.Fatalf("expect must be required: %v", fe)
	}
}

func TestWithDefaultPort(t *testing.T) {
	for in, want := range map[string]string{
		"example.com":      "example.com:443",
		"example.com:8443": "example.com:8443",
		"::1":              "[::1]:443",
	} {
		if got := withDefaultPort(in, "443"); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestServiceTargetsValidated(t *testing.T) {
	for _, typ := range []model.MonitorType{model.TypeSMTP, model.TypeSSH, model.TypeRedis, model.TypePostgres, model.TypeMySQL, model.TypeMongoDB, model.TypeMQTT} {
		m := model.NewMonitor(typ)
		m.Name, m.Target = "x", "no-port"
		if fe := m.Validate(); fe["target"] == "" {
			t.Errorf("%s: host without port should be rejected", typ)
		}
	}
	for _, typ := range []model.MonitorType{model.TypeTLS, model.TypeNTP} {
		m := model.NewMonitor(typ)
		m.Name, m.Target = "x", "example.com"
		if fe := m.Validate(); len(fe) != 0 {
			t.Errorf("%s: bare host should be fine: %v", typ, fe)
		}
	}
	m := model.NewMonitor(model.TypeSMTP)
	m.Name, m.Target, m.Settings.Security = "x", "mail:25", "bogus"
	if fe := m.Validate(); fe["settings.security"] == "" {
		t.Error("bogus security accepted")
	}
}
