package engine

import (
	"context"
	"net"
	"sync"
	"time"
)

// Guard reports whether opskest itself can reach the internet. The
// engine asks it before letting a failure move a non-push monitor toward
// PENDING/DOWN.
type Guard interface {
	Online(ctx context.Context) bool
}

// Defaults.
var defaultGuardTargets = []string{"1.1.1.1:53", "8.8.8.8:53", "208.67.222.222:53"}

const (
	defaultGuardTimeout = 2 * time.Second
	defaultGuardCache   = 30 * time.Second
)

// netGuard probes a few well-known anycast resolvers with plain TCP
// connects, in parallel. It is offline only when every probe fails. The
// answer is cached for cache, and concurrent callers during a probe share
// that one probe (the "shared assessment" idea from Checkmate's egress
// service), so a fleet of simultaneous failures costs one round of dials.
type netGuard struct {
	targets []string
	timeout time.Duration
	cache   time.Duration
	clk     func() time.Time
	dialer  net.Dialer

	mu       sync.Mutex
	valid    bool
	online   bool
	at       time.Time
	inflight chan struct{}
}

// NewNetGuard builds the internet-loss guard. Zero values take the
// defaults: targets 1.1.1.1:53, 8.8.8.8:53, 208.67.222.222:53; timeout
// 2 s; cache 30 s; clk time.Now.
func NewNetGuard(targets []string, timeout, cache time.Duration, clk func() time.Time) Guard {
	if len(targets) == 0 {
		targets = defaultGuardTargets
	}
	if timeout <= 0 {
		timeout = defaultGuardTimeout
	}
	if cache <= 0 {
		cache = defaultGuardCache
	}
	if clk == nil {
		clk = time.Now
	}
	return &netGuard{targets: append([]string(nil), targets...), timeout: timeout, cache: cache, clk: clk}
}

func (g *netGuard) Online(ctx context.Context) bool {
	g.mu.Lock()
	if g.valid && g.clk().Sub(g.at) < g.cache {
		v := g.online
		g.mu.Unlock()
		return v
	}
	if ch := g.inflight; ch != nil {
		g.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return true // shutting down: never turn that into "offline"
		}
		g.mu.Lock()
		v := g.online
		g.mu.Unlock()
		return v
	}
	ch := make(chan struct{})
	g.inflight = ch
	g.mu.Unlock()

	v := g.probe()

	g.mu.Lock()
	g.valid, g.online, g.at = true, v, g.clk()
	g.inflight = nil
	g.mu.Unlock()
	close(ch)
	return v
}

// probe dials every target at once and returns true on the first success.
// It deliberately ignores the caller's context: a cancelled check must not
// be mistaken for (and cached as) lost connectivity.
func (g *netGuard) probe() bool {
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()
	res := make(chan bool, len(g.targets))
	for _, t := range g.targets {
		go func(addr string) {
			c, err := g.dialer.DialContext(ctx, "tcp", addr)
			if err == nil {
				c.Close()
			}
			res <- err == nil
		}(t)
	}
	for range g.targets {
		if <-res {
			return true
		}
	}
	return false
}
