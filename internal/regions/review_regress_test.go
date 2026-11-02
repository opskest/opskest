package regions

import (
	"testing"

	"opskest/internal/model"
)

// Names that only look public: they must never be sent to Globalping.
func TestPublicHostBypasses(t *testing.T) {
	for _, host := range []string{
		"127.1", "10.1", "0x7f.0.0.1", "0177.0.0.1", "010.1.2.3", "1.2.3.4.5", // IPv4 shorthand a resolver expands
		"db.internal.", "db.internal..", "DB.Internal", // trailing dots, case
		"db" + "。" + "internal", "a.db．internal", // IDNA full stops
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "fd00::10", "fe80::1", "[::1]",
		"0.1.2.3", "240.0.0.1", "255.255.255.255", "224.0.0.251", "ff02::1",
		"printer.localdomain", "app.test", "x.ts.net", "1.0.0.127.in-addr.arpa", "svc.cluster.local",
	} {
		if PublicHost(host) {
			t.Errorf("PublicHost(%q) = true, want false", host)
		}
	}
	for _, host := range []string{"example.com", "EXAMPLE.com.", "xn--bcher-kva.example.org", "8.8.8.8", "2001:4860:4860::8888", "1e100.net"} {
		if !PublicHost(host) {
			t.Errorf("PublicHost(%q) = false, want true", host)
		}
	}
}

func TestRequestKeepsIPv6LiteralsIntact(t *testing.T) {
	req, ok := Request(model.TypePing, "2001:4860:4860::8888", nil)
	if !ok || req.Target.Hostname() != "2001:4860:4860::8888" {
		t.Fatalf("a public IPv6 ping target must survive: %+v ok=%v", req, ok)
	}
	if _, ok := Request(model.TypePing, "fd00::10", nil); ok {
		t.Fatal("a ULA ping target is private")
	}
}
