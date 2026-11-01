package check

import (
	"context"
	"fmt"
	"runtime"
	"time"

	probing "github.com/prometheus-community/pro-bing"

	"opskest/internal/model"
)

// pingInterval is the wait time between echo requests.
const pingInterval = 500 * time.Millisecond

func init() {
	Register(model.TypePing, pingChecker{})
}

// pingChecker implements Checker for model.TypePing. It always sends
// unprivileged (UDP) ICMP echo requests: privileged raw-socket pings need
// root, which the app should not require.
type pingChecker struct{}

func (pingChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	s := m.Settings
	count := s.Count // Validate guarantees 1..10 (ApplyDefaults fills 3)

	pinger, err := probing.NewPinger(m.Target)
	if err != nil {
		return pingErrResult(err)
	}
	// Unprivileged ICMP sockets on Linux; Windows has no such mode and
	// needs the raw-socket path.
	pinger.SetPrivileged(runtime.GOOS == "windows")
	pinger.Count = count
	pinger.Interval = pingInterval
	pinger.Timeout = time.Duration(m.TimeoutMs) * time.Millisecond

	// pinger.Timeout and the outer context (also TimeoutMs, from Run) race
	// to fire first, and either can win by a hair. Rather than trust which
	// one produced the error, decide "100% packet loss" from the one fact
	// that actually matters: whether any packet came back.
	runErr := pinger.RunWithContext(ctx)
	stats := pinger.Statistics()

	if stats.PacketsRecv == 0 {
		if runErr != nil {
			if class, _ := ClassifyErr(runErr); class != model.ErrTimeout {
				// A real setup failure (e.g. permission denied opening the
				// unprivileged ICMP socket, or DNS resolution failure),
				// not a timed-out round trip: report it as-is.
				return pingErrResult(runErr)
			}
		}
		return model.Result{
			OK:        false,
			LatencyMs: -1,
			ErrClass:  model.ErrTimeout,
			Reason:    model.TrimReason("100% packet loss"),
			LossPct:   stats.PacketLoss,
		}
	}

	res := model.Result{
		OK:        true,
		LatencyMs: int(stats.AvgRtt.Milliseconds()),
		LossPct:   stats.PacketLoss,
	}

	if stats.PacketLoss > float64(s.MaxLossPct) {
		res.Degraded = true
		res.Reason = model.TrimReason(fmt.Sprintf("packet loss %.0f%% > %d%%", stats.PacketLoss, s.MaxLossPct))
	}

	return res
}

// pingErrResult builds a failure Result for an error that prevented the
// pinger from running at all (e.g. bad address, permission denied opening
// the unprivileged ICMP socket) or from completing (context timeout).
func pingErrResult(err error) model.Result {
	class, reason := ClassifyErr(err)
	return model.Result{
		OK:        false,
		LatencyMs: -1,
		ErrClass:  class,
		Reason:    model.TrimReason(reason),
	}
}
