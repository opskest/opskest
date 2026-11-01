package regions

import (
	"testing"

	"opskest/internal/model"
)

// A monitor that needs a proxy, a custom CA, a client certificate or OAuth2
// would give a misleading answer from another network (and would send its URL
// there for nothing): it is never planned for worldwide checks.
func TestPlanForSkipsMonitorsOtherNetworksCannotReproduce(t *testing.T) {
	mk := func(mut func(*model.Monitor)) *model.Monitor {
		m := model.NewMonitor(model.TypeHTTP)
		m.Name, m.Target = "site", "https://example.com/health"
		mut(m)
		return m
	}
	cfg := Config{Countries: []string{"US"}}
	if p := PlanFor(mk(func(*model.Monitor) {}), cfg); p.Reason != "" {
		t.Fatalf("a plain public monitor should be eligible, got %q", p.Reason)
	}
	for name, mut := range map[string]func(*model.Monitor){
		"proxy":  func(m *model.Monitor) { m.Settings.ProxyURL = "http://proxy.internal:3128" },
		"CA":     func(m *model.Monitor) { m.Settings.CACert = "-----BEGIN CERTIFICATE-----" },
		"mTLS":   func(m *model.Monitor) { m.Secrets.ClientCert = "pem" },
		"oauth2": func(m *model.Monitor) { m.Settings.AuthType = "oauth2" },
	} {
		if p := PlanFor(mk(mut), cfg); p.Reason == "" {
			t.Errorf("%s: monitor was planned for other countries", name)
		}
	}
}
