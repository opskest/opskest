package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"opskest/internal/config"
)

func TestRuntimeUsesDeploymentMetadataWithoutDisclosingHost(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		cfg          *config.Config
		zone, region string
	}{
		{nil, "UTC", "This server"},
		{&config.Config{DisplayTZ: loc, ServerRegion: "US East (Virginia)"}, "America/New_York", "US East (Virginia)"},
		{&config.Config{DisplayTZ: loc}, "America/New_York", "This server"},
	} {
		s := &server{d: Deps{Cfg: tc.cfg}}
		r := httptest.NewRequest("GET", "/api/runtime", nil)
		w := httptest.NewRecorder()
		s.handleRuntime(w, r)
		var got map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || len(got) != 2 || got["timeZone"] != tc.zone || got["serverRegion"] != tc.region {
			t.Fatalf("runtime = %v", got)
		}
	}
}
