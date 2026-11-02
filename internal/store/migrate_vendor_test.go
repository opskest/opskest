package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigration0033VendorAlertOnly upgrades a database that is at 0032 and
// holds a 3rd-party feed with outages, notes, a status-page component, a
// maintenance target, an incident link, and monitors that depend on it or
// contain it. The feed's records go; everything else stays; a second run
// of the SQL finds nothing to do.
func TestMigration0033VendorAlertOnly(t *testing.T) {
	db, err := sql.Open("sqlite", writerDSN(filepath.Join(t.TempDir(), "old.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(migrationsTableDDL); err != nil {
		t.Fatal(err)
	}
	names, err := migrationNames()
	if err != nil {
		t.Fatal(err)
	}
	const target = "0033_vendor_alert_only.sql"
	var sqlText string
	for _, n := range names {
		b, err := migrationsFS.ReadFile("migrations/" + n)
		if err != nil {
			t.Fatal(err)
		}
		if n >= "0033" {
			if n == target {
				sqlText = string(b)
			}
			continue
		}
		if _, err := db.Exec(string(b)); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, 1)`, n); err != nil {
			t.Fatal(err)
		}
	}
	if sqlText == "" {
		t.Fatal("migration 0033 missing")
	}

	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", strings.Fields(q)[0], err)
		}
	}
	count := func(q string) int {
		t.Helper()
		var n int
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	mon := func(id int, name, settings string) {
		mustExec(`INSERT INTO monitors(id, name, type, interval_s, retry_interval_s, retries, timeout_ms, settings_json, created_at, updated_at)
			VALUES (?, ?, 'http', 60, 20, 0, 5000, ?, 1, 1)`, id, name, settings)
	}
	mon(1, "own", `{"dependsOn":[2,3],"method":"GET"}`)
	mon(2, "feed", `{"vendor":true,"dependsOn":[3]}`)
	mon(3, "other", `{}`)
	mon(4, "grp", `{"children":[2]}`)
	mon(5, "grp2", `{"children":[2,3]}`)
	mon(6, "only-feed-parent", `{"dependsOn":[2]}`)

	for _, id := range []int{1, 2} {
		mustExec(`INSERT INTO outages(id, monitor_id, started_at, ended_at, first_reason) VALUES (?, ?, 100, NULL, 'x')`, id, id)
		mustExec(`INSERT INTO outage_notes(outage_id, body, created_at) VALUES (?, 'n', 1)`, id)
	}
	mustExec(`INSERT INTO rollup_daily(monitor_id, bucket, n_up, n_down) VALUES (1, 0, 1, 0), (2, 0, 1, 1)`)
	mustExec(`INSERT INTO status_pages(id, slug, title, visibility, created_at, updated_at) VALUES (1, 's', 't', 'public', 1, 1)`)
	mustExec(`INSERT INTO status_page_items(page_id, sort, monitor_id) VALUES (1, 0, 1), (1, 1, 2)`)
	mustExec(`INSERT INTO maintenance(id, title, kind, tz, created_at, updated_at) VALUES (1, 'w', 'once', 'UTC', 1, 1)`)
	mustExec(`INSERT INTO maintenance_targets(maintenance_id, monitor_id) VALUES (1, 1), (1, 2)`)
	mustExec(`INSERT INTO incidents(id, title, status, impact, created_at, updated_at) VALUES (1, 'i', 'investigating', 'minor', 1, 1)`)
	mustExec(`INSERT INTO incident_monitors(incident_id, monitor_id) VALUES (1, 1), (1, 2)`)

	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if n := count(`SELECT count(*) FROM outages WHERE monitor_id=2`); n != 0 {
		t.Fatalf("vendor outages left: %d", n)
	}
	if n := count(`SELECT count(*) FROM outage_notes WHERE outage_id=2`); n != 0 {
		t.Fatalf("vendor outage notes left: %d", n)
	}
	if n := count(`SELECT count(*) FROM outages WHERE monitor_id=1`) + count(`SELECT count(*) FROM outage_notes WHERE outage_id=1`); n != 2 {
		t.Fatalf("own outage or note lost: %d", n)
	}
	if n := count(`SELECT count(*) FROM rollup_daily WHERE monitor_id=2`); n != 0 {
		t.Fatalf("vendor rollups left: %d", n)
	}
	if n := count(`SELECT count(*) FROM rollup_daily WHERE monitor_id=1`); n != 1 {
		t.Fatalf("own rollup lost: %d", n)
	}
	if n := count(`SELECT count(*) FROM status_page_items WHERE monitor_id=2`); n != 0 {
		t.Fatalf("vendor still on a page: %d", n)
	}
	if n := count(`SELECT count(*) FROM status_page_items WHERE monitor_id=1`); n != 1 {
		t.Fatalf("own page item lost: %d", n)
	}
	if n := count(`SELECT count(*) FROM maintenance_targets WHERE monitor_id=2`); n != 0 {
		t.Fatalf("vendor still a maintenance target: %d", n)
	}
	if n := count(`SELECT count(*) FROM incident_monitors WHERE monitor_id=2`); n != 0 {
		t.Fatalf("vendor still linked to an incident: %d", n)
	}
	if n := count(`SELECT count(*) FROM incident_monitors WHERE monitor_id=1`); n != 1 {
		t.Fatalf("own incident link lost: %d", n)
	}

	setting := func(id int, path string) sql.NullString {
		t.Helper()
		var v sql.NullString
		if err := db.QueryRow(`SELECT json_extract(settings_json, ?) FROM monitors WHERE id=?`, path, id).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if v := setting(1, "$.dependsOn"); !v.Valid || v.String != "[3]" {
		t.Fatalf("own dependsOn = %v, want [3]", v)
	}
	if v := setting(1, "$.method"); v.String != "GET" {
		t.Fatalf("other settings lost: %v", v)
	}
	if v := setting(2, "$.dependsOn"); v.Valid {
		t.Fatalf("vendor still depends on something: %v", v)
	}
	if v := setting(2, "$.vendor"); v.String != "1" {
		t.Fatalf("vendor flag lost: %v", v)
	}
	if v := setting(4, "$.children"); v.Valid {
		t.Fatalf("empty children kept: %v", v)
	}
	if v := setting(5, "$.children"); !v.Valid || v.String != "[3]" {
		t.Fatalf("children = %v, want [3]", v)
	}
	if v := setting(6, "$.dependsOn"); v.Valid {
		t.Fatalf("dependsOn on only a vendor kept: %v", v)
	}

	// Idempotent: running the statements again changes nothing and fails nothing.
	mustExec(sqlText)
	if v := setting(1, "$.dependsOn"); v.String != "[3]" {
		t.Fatalf("second run changed dependsOn: %v", v)
	}
	if n := count(`SELECT count(*) FROM outages`); n != 1 {
		t.Fatalf("outages after second run = %d, want 1", n)
	}
}
