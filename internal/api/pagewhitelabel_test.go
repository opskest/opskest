package api

import (
	"net/http"
	"testing"
)

// "Powered by Opskest" is on for every page unless its owner turns it off,
// including a page created by a client that knows nothing about the option.
func TestPoweredByDefaultsOnAndCanBeTurnedOff(t *testing.T) {
	env := newTestEnv(t)
	public := func(slug string) (showPoweredBy any, favicon any) {
		t.Helper()
		r := env.do(http.MethodGet, "/api/public/pages/"+slug, nil, "")
		if r.status != http.StatusOK {
			t.Fatalf("%s: status %d", slug, r.status)
		}
		var body struct {
			Page struct {
				Settings map[string]any `json:"settings"`
			} `json:"page"`
		}
		r.json(t, &body)
		return body.Page.Settings["showPoweredBy"], body.Page.Settings["faviconUrl"]
	}

	if r := env.do(http.MethodPost, "/api/pages", settingsPage("wl-default", nil, nil), "admin"); r.status != http.StatusCreated {
		t.Fatalf("create default: %d %s", r.status, r.body)
	}
	if on, fav := public("wl-default"); on != true || fav != "" {
		t.Errorf("default page: showPoweredBy=%v faviconUrl=%v, want true and empty", on, fav)
	}

	r := env.do(http.MethodPost, "/api/pages", settingsPage("wl-off", map[string]any{
		"showPoweredBy": false, "faviconUrl": " https://cdn.example.com/f.png ",
	}, nil), "admin")
	if r.status != http.StatusCreated {
		t.Fatalf("create white-label: %d %s", r.status, r.body)
	}
	if on, fav := public("wl-off"); on != false || fav != "https://cdn.example.com/f.png" {
		t.Errorf("white-label page: showPoweredBy=%v faviconUrl=%v, want false and the trimmed address", on, fav)
	}
}
