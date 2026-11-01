package check

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"opskest/internal/model"
)

func init() {
	Register(model.TypeWebSocket, websocketChecker{})
}

// websocketChecker implements Checker for model.TypeWebSocket. m.Target is
// a ws/wss URL. A fresh connection is opened per check: it dials, sends
// Settings.Send when set, reads the first reply when Settings.Expect is
// set, then closes.
type websocketChecker struct{}

func (websocketChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	start := time.Now()
	s := m.Settings

	hdr := http.Header{}
	applyHeaders(hdr, m)

	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}

	conn, resp, err := websocket.Dial(ctx, m.Target, &websocket.DialOptions{
		HTTPClient: client,
		HTTPHeader: hdr,
	})
	if err != nil {
		return classifyWSErr(m, err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	var cert *model.CertInfo
	if resp != nil && resp.TLS != nil {
		cert = certInfoFromConnState(resp.TLS)
	}

	if s.Send != "" {
		if err := conn.Write(ctx, websocket.MessageText, []byte(s.Send)); err != nil {
			res := classifyWSErr(m, err)
			res.Cert = cert
			return res
		}
	}

	if s.Expect != "" {
		_, data, err := conn.Read(ctx)
		if err != nil {
			res := classifyWSErr(m, err)
			res.Cert = cert
			return res
		}
		if !strings.Contains(string(data), s.Expect) {
			return model.Result{
				OK:        false,
				LatencyMs: -1,
				ErrClass:  model.ErrAssertion,
				Reason:    model.TrimReason(fmt.Sprintf("reply did not contain %q (%d bytes received)", s.Expect, len(data))),
				Cert:      cert,
			}
		}
	}

	return model.Result{
		OK:        true,
		LatencyMs: msSince(start),
		Cert:      cert,
	}
}

// classifyWSErr builds a failure Result for an error dialing, writing to
// or reading from a WebSocket connection.
func classifyWSErr(m *model.Monitor, err error) model.Result {
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
