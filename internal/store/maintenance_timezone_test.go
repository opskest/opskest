package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMaintenanceIANAZonePersistence(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, zone := range []string{"America/New_York", "Europe/Berlin", "Asia/Kathmandu"} {
		window := &Maintenance{Title: zone, Kind: "weekly", TZ: zone, Weekdays: 1, StartMinute: 120, DurationMin: 60, Active: true}
		if err := s.CreateMaintenance(ctx, window); err != nil {
			t.Fatalf("persist %s: %v", zone, err)
		}
	}
	windows, err := s.ListMaintenance(ctx)
	if err != nil || len(windows) != 3 {
		t.Fatalf("read windows: count=%d, err=%v", len(windows), err)
	}
	for i, zone := range []string{"America/New_York", "Europe/Berlin", "Asia/Kathmandu"} {
		if windows[i].TZ != zone {
			t.Fatalf("zone changed: got %q, want %q", windows[i].TZ, zone)
		}
	}
}

func TestMaintenanceTimezoneUpgradePreservesTargets(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(migrationsTableDDL); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names, err := migrationNames()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name >= "0017_maintenance_timezones.sql" {
			break
		}
		if err := applyMigration(ctx, conn, name); err != nil {
			t.Fatalf("old migration %s: %v", name, err)
		}
	}
	conn.Close()
	if _, err := db.Exec(`INSERT INTO groups(id,name) VALUES(9,'Existing project');
		INSERT INTO maintenance(id,title,kind,tz,created_at,updated_at) VALUES(42,'Existing window','weekly','UTC',1,1);
		INSERT INTO maintenance_targets(maintenance_id,group_id) VALUES(42,9)`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	var title, zone string
	if err := db.QueryRow(`SELECT title,tz FROM maintenance WHERE id=42`).Scan(&title, &zone); err != nil {
		t.Fatal(err)
	}
	if title != "Existing window" || zone != "UTC" {
		t.Fatalf("existing data changed: %s/%s", title, zone)
	}
	var targets int
	if err := db.QueryRow(`SELECT count(*) FROM maintenance_targets WHERE maintenance_id=42 AND group_id=9`).Scan(&targets); err != nil || targets != 1 {
		t.Fatalf("targets lost: %d, %v", targets, err)
	}
	if _, err := db.Exec(`UPDATE maintenance SET tz='America/New_York' WHERE id=42`); err != nil {
		t.Fatalf("IANA zone rejected after upgrade: %v", err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("repeat migrate: %v", err)
	}
}
