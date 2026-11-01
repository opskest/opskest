package check

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"opskest/internal/model"
)

func init() {
	Register(model.TypeAMQP, amqpChecker{})
	Register(model.TypeMemcache, memcachedChecker{})
	Register(model.TypeBanner, bannerChecker{})
	Register(model.TypeGRPC, grpcChecker{})
}

// ── AMQP (RabbitMQ and other AMQP 0-9-1 brokers) ──────────────────

// amqpChecker sends the AMQP 0-9-1 protocol header and requires the broker's
// Connection.Start method in reply. No login is attempted.
type amqpChecker struct{}

func (amqpChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	start := time.Now()
	conn, cert, err := dialService(ctx, m, m.Settings.TLS)
	if err != nil {
		return classifyDialErr(m, err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("AMQP\x00\x00\x09\x01")); err != nil {
		return ioFail(m, err)
	}
	var f [11]byte // frame type, channel, size, then class and method ids
	if _, err := io.ReadFull(conn, f[:]); err != nil {
		return ioFail(m, err)
	}
	class, method := binary.BigEndian.Uint16(f[7:9]), binary.BigEndian.Uint16(f[9:11])
	if f[0] != 1 || class != 10 || method != 10 {
		return protoFail("not an AMQP 0-9-1 broker")
	}
	return okResult(start, cert)
}

// ── Memcached ─────────────────────────────────────────────────────

// memcachedChecker asks for the server version.
type memcachedChecker struct{}

func (memcachedChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	start := time.Now()
	conn, _, err := dialService(ctx, m, false)
	if err != nil {
		return classifyDialErr(m, err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("version\r\n")); err != nil {
		return ioFail(m, err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil && line == "" {
		return ioFail(m, err)
	}
	if !strings.HasPrefix(line, "VERSION ") {
		return protoFail("not a memcached server")
	}
	return okResult(start, nil)
}

// ── Banner ────────────────────────────────────────────────────────

// bannerChecker reads the first line a server sends on connect (FTP, IMAP,
// POP3, a custom daemon) and requires it to contain Settings.Expect. The
// banner itself is never put in a failure reason.
type bannerChecker struct{}

func (bannerChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	start := time.Now()
	conn, cert, err := dialService(ctx, m, m.Settings.TLS)
	if err != nil {
		return classifyDialErr(m, err)
	}
	defer conn.Close()
	line, err := bufio.NewReaderSize(conn, 2048).ReadString('\n')
	if err != nil && line == "" {
		return ioFail(m, err)
	}
	if !strings.Contains(line, m.Settings.Expect) {
		return protoFail("the greeting did not contain the expected text")
	}
	return okResult(start, cert)
}

// ── gRPC health ───────────────────────────────────────────────────

// grpcChecker calls grpc.health.v1.Health/Check and requires SERVING.
// Settings.GRPCService names the service ("" = the server overall);
// Settings.TLS selects https (and h2 over ALPN) instead of plain HTTP/2.
type grpcChecker struct{}

func (grpcChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	start := time.Now()
	s := m.Settings

	tr := &http.Transport{
		DisableKeepAlives: true,
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: s.IgnoreTLS},
		DialContext:       (&net.Dialer{}).DialContext,
	}
	tr.Protocols = new(http.Protocols)
	if s.TLS {
		tr.Protocols.SetHTTP2(true)
	} else {
		tr.Protocols.SetUnencryptedHTTP2(true)
	}
	defer tr.CloseIdleConnections()

	scheme := "http"
	if s.TLS {
		scheme = "https"
	}
	var msg []byte
	if s.GRPCService != "" {
		// HealthCheckRequest{service = 1}: tag, varint length (two bytes from
		// 128 on; a single byte(len) corrupted names that long), the name.
		msg = binary.AppendUvarint([]byte{0x0a}, uint64(len(s.GRPCService)))
		msg = append(msg, s.GRPCService...)
	}
	frame := make([]byte, 5, 5+len(msg))
	binary.BigEndian.PutUint32(frame[1:], uint32(len(msg)))
	frame = append(frame, msg...)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s://%s/grpc.health.v1.Health/Check", scheme, m.Target), bytes.NewReader(frame))
	if err != nil {
		return protoFail("invalid target")
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("TE", "trailers")

	// A health service never redirects; following one would let the target
	// bounce the check to another host.
	resp, err := (&http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return classifyDialErr(m, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return ioFail(m, err)
	}
	var cert *model.CertInfo
	if resp.TLS != nil {
		cert = certInfoFromConnState(resp.TLS)
	}

	status := resp.Trailer.Get("Grpc-Status")
	if status == "" {
		status = resp.Header.Get("Grpc-Status") // "trailers only" reply
	}
	switch {
	case resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/grpc"):
		return protoFail("not a gRPC server")
	case status == "12":
		return protoFail("the server does not implement the gRPC health service")
	case status == "5":
		return protoFail("the health service does not know that service name")
	case status != "0":
		return protoFail("gRPC status " + status)
	}
	// HealthCheckResponse{status = 1} is SERVING; its bytes are 08 01 after
	// the 5-byte message frame header.
	if len(body) < 7 || body[5] != 0x08 || body[6] != 1 {
		return protoFail("the service reports it is not serving")
	}
	res := okResult(start, cert)
	return res
}
