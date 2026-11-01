package store

import (
	"context"
	"errors"
	"testing"
)

func TestUpdateSettingsDefaultsAndRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	got, err := s.GetUpdateSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := DefaultUpdateSettings(); got != want {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}
	if got.FeedURL != "" || got.PublicKey != "" || !got.Enabled || got.Mode != "notify" || got.AutoHour != 3 {
		t.Fatalf("unexpected defaults: %+v", got)
	}

	in := UpdateSettings{Enabled: false, FeedURL: "https://example.com/m.json", Mode: "auto", AutoHour: 22, PublicKey: "abc"}
	if err := s.PutUpdateSettings(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetUpdateSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Fatalf("round trip = %+v, want %+v", got, in)
	}
}

func TestUpdateSettingsValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, bad := range []UpdateSettings{
		{Enabled: true, Mode: "sometimes", AutoHour: 3},
		{Enabled: true, Mode: "auto", AutoHour: 24},
		{Enabled: true, Mode: "auto", AutoHour: -1},
	} {
		if err := s.PutUpdateSettings(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: err = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestUpdateLastRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	got, err := s.GetUpdateLast(ctx)
	if err != nil || got != (UpdateLast{}) {
		t.Fatalf("empty = %+v, %v", got, err)
	}
	in := UpdateLast{CheckJSON: `{"latest":"1.2.3"}`, InstallFailedAt: 1700000000000, SkipVersion: "1.2.3", PrevVersion: "1.1.0"}
	if err := s.PutUpdateLast(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetUpdateLast(ctx)
	if err != nil || got != in {
		t.Fatalf("round trip = %+v, %v; want %+v", got, err, in)
	}
}

func TestUpdateKeysDoNotDisturbGeneralSettings(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.PutUpdateSettings(ctx, UpdateSettings{Enabled: true, Mode: "notify", AutoHour: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutUpdateLast(ctx, UpdateLast{CheckJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.BackupHour != DefaultSettings().BackupHour {
		t.Fatalf("general settings changed: %+v", got)
	}
}
