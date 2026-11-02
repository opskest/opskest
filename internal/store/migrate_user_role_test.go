package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigration0034KeepsUsers upgrades a database that is at 0033 with one
// user of each old role plus rows that point at users (a session, local
// credentials, a monitor's created_by): the rebuild of the users table must
// keep every row and link, and then accept the new 'user' role.
func TestMigration0034KeepsUsers(t *testing.T) {
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
		if n >= "0034" {
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
			t.Fatalf("%v: %v", args, err)
		}
	}
	mustExec(`INSERT INTO users(id, email, name, role, created_at, last_login_at) VALUES (1, 'a@x', 'A', 'admin', 10, 99)`)
	mustExec(`INSERT INTO users(id, email, name, role, created_at) VALUES (2, 'v@x', 'V', 'viewer', 11)`)
	mustExec(`INSERT INTO users(id, email, name, role, created_at) VALUES (3, 'd@x', 'D', 'disabled', 12)`)
	mustExec(`INSERT INTO local_credentials(user_id, username, password_hash) VALUES (2, 'vee', 'h')`)
	mustExec(`INSERT INTO sessions(id_hash, user_id, csrf_token, created_at, expires_at) VALUES (x'01', 2, 'c', 1, 2)`)
	mustExec(`INSERT INTO monitors(id, name, type, interval_s, retry_interval_s, retries, timeout_ms, created_by, created_at, updated_at) VALUES (1, 'm', 'tcp', 60, 20, 0, 5000, 1, 1, 1)`)

	// The old schema refuses the new role.
	if _, err := db.Exec(`INSERT INTO users(email, role, created_at) VALUES ('u@x', 'user', 1)`); err == nil {
		t.Fatal("old schema unexpectedly accepts role 'user'")
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	want := map[int64]string{1: "admin", 2: "viewer", 3: "disabled"}
	rows, err := db.Query(`SELECT id, role FROM users ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]string{}
	for rows.Next() {
		var id int64
		var role string
		if err := rows.Scan(&id, &role); err != nil {
			t.Fatal(err)
		}
		got[id] = role
	}
	rows.Close()
	if len(got) != len(want) {
		t.Fatalf("users after migration = %v, want %v", got, want)
	}
	for id, role := range want {
		if got[id] != role {
			t.Errorf("user %d role = %q, want %q", id, got[id], role)
		}
	}
	var lastLogin sql.NullInt64
	db.QueryRow(`SELECT last_login_at FROM users WHERE id = 1`).Scan(&lastLogin)
	if !lastLogin.Valid || lastLogin.Int64 != 99 {
		t.Errorf("last_login_at = %v, want 99", lastLogin)
	}
	for _, q := range []string{
		`SELECT count(*) FROM local_credentials WHERE user_id = 2`,
		`SELECT count(*) FROM sessions WHERE user_id = 2`,
		`SELECT count(*) FROM monitors WHERE created_by = 1`,
	} {
		var n int
		db.QueryRow(q).Scan(&n)
		if n != 1 {
			t.Errorf("%s = %d, want 1 (cascade or SET NULL fired during the rebuild)", q, n)
		}
	}

	if _, err := db.Exec(`INSERT INTO users(email, role, created_at) VALUES ('u@x', 'user', 1)`); err != nil {
		t.Fatalf("new schema rejects role 'user': %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users(email, role, created_at) VALUES ('b@x', 'bogus', 1)`); err == nil {
		t.Fatal("new schema accepts an unknown role")
	}
	var fks int
	db.QueryRow(`PRAGMA foreign_keys`).Scan(&fks)
	if fks != 1 {
		t.Fatalf("foreign_keys = %d after migrate, want 1", fks)
	}
}
