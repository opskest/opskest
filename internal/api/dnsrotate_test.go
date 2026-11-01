package api

import (
	"net"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/miekg/dns"

	"opskest/internal/model"
)

// albIPs is the fake ALB's current A records. The DNS server goroutine
// reads it while the test rotates it, hence the lock.
type albIPs struct {
	mu sync.Mutex
	v  []string
}

func (a *albIPs) set(v ...string) { a.mu.Lock(); a.v = v; a.mu.Unlock() }
func (a *albIPs) get() []string   { a.mu.Lock(); defer a.mu.Unlock(); return a.v }

// startALBDNS serves alb.test. A records from ips, so a test can "rotate"
// the ALB with ips.set.
func startALBDNS(t *testing.T, ips *albIPs) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	srv := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		if len(r.Question) == 1 && r.Question[0].Qtype == dns.TypeA && r.Question[0].Name == "alb.test." {
			for _, ip := range ips.get() {
				rr, _ := dns.NewRR("alb.test. 60 IN A " + ip)
				m.Answer = append(m.Answer, rr)
			}
		}
		_ = w.WriteMsg(m)
	})}
	go srv.ActivateAndServe()
	t.Cleanup(func() { srv.Shutdown() })
	return pc.LocalAddr().String()
}

func TestDNSResolveAndRotate(t *testing.T) {
	env := newTestEnv(t)
	ips := &albIPs{}
	ips.set("10.0.0.2", "10.0.0.1")
	addr := startALBDNS(t, ips)

	// Form flow: resolve an unsaved target.
	resp := env.do(http.MethodPost, "/api/monitors/dns/resolve", map[string]string{"target": "alb.test", "record": "A", "resolver": addr}, "admin")
	if resp.status != http.StatusOK {
		t.Fatalf("resolve: status %d body %s", resp.status, resp.body)
	}
	var res dnsResolveOut
	resp.json(t, &res)
	if want := []string{"10.0.0.1", "10.0.0.2"}; !slices.Equal(res.Values, want) {
		t.Fatalf("resolve values = %v, want %v", res.Values, want)
	}

	in := MonitorIn{Name: "alb", Type: model.TypeDNS, Target: "alb.test", IntervalS: 60, RetryIntervalS: 20, TimeoutMs: 5000}
	in.Settings.Record = "A"
	in.Settings.Resolver = addr
	in.Settings.ExpectedValues = res.Values
	mon := env.createMonitor(in)
	if mon.Settings.Match != "exact" {
		t.Errorf("default match = %q, want exact", mon.Settings.Match)
	}

	// The ALB rotates; preview by id shows old vs new.
	ips.set("10.0.0.1", "10.0.0.7")
	resp = env.do(http.MethodPost, "/api/monitors/dns/resolve", map[string]any{"id": mon.ID}, "admin")
	resp.json(t, &res)
	if !slices.Equal(res.Values, []string{"10.0.0.1", "10.0.0.7"}) || !slices.Equal(res.Expected, []string{"10.0.0.1", "10.0.0.2"}) {
		t.Fatalf("preview = %+v", res)
	}

	// Viewers can't rotate.
	if r := env.do(http.MethodPost, "/api/monitors/"+itoa(mon.ID)+"/dns/rotate", map[string]any{"values": res.Values}, "viewer"); r.status != http.StatusForbidden && r.status != http.StatusNotFound {
		t.Errorf("viewer rotate status = %d", r.status)
	}

	resp = env.do(http.MethodPost, "/api/monitors/"+itoa(mon.ID)+"/dns/rotate", map[string]any{"values": res.Values}, "admin")
	if resp.status != http.StatusOK {
		t.Fatalf("rotate: status %d body %s", resp.status, resp.body)
	}
	var rot struct{ Previous, Current []string }
	resp.json(t, &rot)
	if !slices.Equal(rot.Previous, []string{"10.0.0.1", "10.0.0.2"}) || !slices.Equal(rot.Current, []string{"10.0.0.1", "10.0.0.7"}) {
		t.Fatalf("rotate = %+v", rot)
	}

	var got MonitorOut
	env.do(http.MethodGet, "/api/monitors/"+itoa(mon.ID), nil, "admin").json(t, &got)
	if !slices.Equal(got.Settings.ExpectedValues, []string{"10.0.0.1", "10.0.0.7"}) {
		t.Errorf("stored values = %v", got.Settings.ExpectedValues)
	}

	// Bad values are rejected; non-dns and unknown ids are 404.
	if r := env.do(http.MethodPost, "/api/monitors/"+itoa(mon.ID)+"/dns/rotate", map[string]any{"values": []string{"nope"}}, "admin"); r.status != http.StatusBadRequest {
		t.Errorf("bad value status = %d", r.status)
	}
	httpMon := env.createMonitor(httpMonitorIn("web", "http://127.0.0.1:1"))
	for _, id := range []int64{httpMon.ID, 999999} {
		if r := env.do(http.MethodPost, "/api/monitors/"+itoa(id)+"/dns/rotate", map[string]any{"values": []string{"10.0.0.1"}}, "admin"); r.status != http.StatusNotFound {
			t.Errorf("rotate id %d status = %d, want 404", id, r.status)
		}
	}
}
