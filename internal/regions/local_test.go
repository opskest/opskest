package regions

import (
	"strings"
	"testing"
)

func TestLocalRegionModeRetainsLegacyMeaning(t *testing.T) {
	for _, mode := range []string{"local", "india"} {
		m := httpMon("https://example.com")
		m.Settings.RegionMode = mode
		if fe := m.Validate(); len(fe) != 0 {
			t.Fatalf("%s validation: %v", mode, fe)
		}
		p := PlanFor(m, Config{Countries: []string{"US", "IN"}})
		if p.Reason == "" || len(p.Countries) != 0 || strings.Contains(p.Reason, "India") {
			t.Fatalf("local plan = %+v", p)
		}
	}
}
