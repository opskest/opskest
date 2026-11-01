package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigration0019KeepsChannelData upgrades a database that is at 0018 and
// holds a channel with links pointing at it: the rebuild of the channels table
// must keep the row and every foreign-key child (monitor_channels rows are
// ON DELETE CASCADE, so a careless DROP TABLE would erase them silently).
func TestMigration0019KeepsChannelData(t *testing.T) {
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
	for _, n := range names {
		if n >= "0019" {
			break
		}
		b, err := migrationsFS.ReadFile("migrations/" + n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(b)); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, 1)`, n); err != nil {
			t.Fatal(err)
		}
	}

	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", strings.Fields(q)[0], err)
		}
	}
	mustExec(`INSERT INTO channels(id, name, type, config_enc, is_default, created_at) VALUES (7, 'ops', 'slack', x'0102', 1, 5)`)
	mustExec(`INSERT INTO monitors(id, name, type, interval_s, retry_interval_s, retries, timeout_ms, created_at, updated_at) VALUES (1, 'm', 'tcp', 60, 20, 0, 5000, 1, 1)`)
	mustExec(`INSERT INTO monitor_channels(monitor_id, channel_id) VALUES (1, 7)`)
	mustExec(`INSERT INTO status_pages(id, slug, title, visibility, created_at, updated_at, subscriber_channel_id) VALUES (1, 's', 't', 'public', 1, 1, 7)`)

	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var name, typ string
	if err := db.QueryRow(`SELECT name, type FROM channels WHERE id = 7`).Scan(&name, &typ); err != nil || name != "ops" || typ != "slack" {
		t.Fatalf("channel not preserved: %q %q %v", name, typ, err)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM monitor_channels WHERE channel_id = 7`).Scan(&n)
	if n != 1 {
		t.Fatalf("monitor_channels rows = %d, want 1 (cascade fired during the rebuild)", n)
	}
	var sub sql.NullInt64
	db.QueryRow(`SELECT subscriber_channel_id FROM status_pages WHERE id = 1`).Scan(&sub)
	if !sub.Valid || sub.Int64 != 7 {
		t.Fatalf("subscriber_channel_id = %v, want 7 (SET NULL fired during the rebuild)", sub)
	}
	// New types are accepted, foreign keys still enforced on the new table.
	mustExec(`INSERT INTO channels(name, type, config_enc, is_default, created_at) VALUES ('d', 'discord', x'00', 0, 1)`)
	var fks int
	db.QueryRow(`PRAGMA foreign_keys`).Scan(&fks)
	if fks != 1 {
		t.Fatalf("foreign_keys = %d after migrate, want 1", fks)
	}
	if _, err := db.Exec(`DELETE FROM channels WHERE id = 7`); err != nil {
		t.Fatal(err)
	}
	db.QueryRow(`SELECT count(*) FROM monitor_channels WHERE channel_id = 7`).Scan(&n)
	if n != 0 {
		t.Fatalf("cascade on the rebuilt channels table is gone: %d rows left", n)
	}
}
