package store

import (
	"context"
	"testing"
)

func TestPageSettingsDefaultsAndRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// A page saved without settings gets the defaults.
	p := &Page{Slug: "plain", Title: "Plain", Visibility: "public", Layout: "list", HistoryDays: 90}
	if err := st.CreatePage(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetPage(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if *got.Settings != DefaultPageSettings() {
		t.Fatalf("defaults = %+v", *got.Settings)
	}

	// An update without settings keeps them; one with settings replaces them.
	custom := DefaultPageSettings()
	custom.Font, custom.ShowCert, custom.Banner = "mono", true, PageBanner{Text: "hi", Severity: "success"}
	got.Settings = &custom
	if err := st.UpdatePage(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, _ := st.GetPage(ctx, p.ID)
	again.Settings = nil
	again.Title = "Renamed"
	if err := st.UpdatePage(ctx, again); err != nil {
		t.Fatal(err)
	}
	final, _ := st.GetPage(ctx, p.ID)
	if final.Settings.Font != "mono" || !final.Settings.ShowCert || final.Settings.Banner.Text != "hi" {
		t.Fatalf("settings after an update that omitted them = %+v", *final.Settings)
	}
}

func TestDecodePageSettingsIsTolerant(t *testing.T) {
	d := DefaultPageSettings()
	for _, raw := range []string{"", "{}", "not json", "[]", `{"font":`} {
		if got := decodePageSettings(raw); got != d {
			t.Errorf("decode(%q) = %+v, want the defaults", raw, got)
		}
	}
	// Missing fields keep their defaults; present ones win.
	got := decodePageSettings(`{"showUptime":false,"refreshSeconds":120}`)
	if got.ShowUptime || got.RefreshSeconds != 120 || !got.FeedEnabled || got.Font != "system" {
		t.Errorf("partial decode = %+v", got)
	}
}

// A document saved before showMaintenance existed reads as "on".
func TestShowMaintenanceDefaultsOn(t *testing.T) {
	if !DefaultPageSettings().ShowMaintenance {
		t.Fatal("default is off")
	}
	for _, raw := range []string{"", "{}", `{"font":"mono"}`, `{"showUptime":false,"feedEnabled":true}`} {
		if !decodePageSettings(raw).ShowMaintenance {
			t.Errorf("decode(%q): showMaintenance is off", raw)
		}
	}
	if decodePageSettings(`{"showMaintenance":false}`).ShowMaintenance {
		t.Error("an explicit false was ignored")
	}
}
