package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"opskest/internal/model"
	"opskest/internal/secret"
)

func sortInt64s(s []int64) {
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
}

func isAlnum(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

func TestOpenAppliesPragmas(t *testing.T) {
	dir := t.TempDir()
	box, err := secret.New(testKey())
	if err != nil {
		t.Fatal(err)
	}
	loc := testLoc()

	s, err := Open(dir, box, loc)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var journalMode string
	if err := s.W.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want %q", journalMode, "wal")
	}

	var autoVacuum int
	if err := s.W.QueryRow(`PRAGMA auto_vacuum`).Scan(&autoVacuum); err != nil {
		t.Fatal(err)
	}
	if autoVacuum != 2 {
		t.Errorf("auto_vacuum = %d, want 2 (incremental)", autoVacuum)
	}

	var fk int
	if err := s.W.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}

	names, err := migrationNames()
	if err != nil {
		t.Fatal(err)
	}
	wantMig := len(names)
	var migCount int
	if err := s.W.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&migCount); err != nil {
		t.Fatal(err)
	}
	if migCount != wantMig {
		t.Errorf("schema_migrations rows = %d, want %d", migCount, wantMig)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening the same dir must be idempotent: migrations don't run
	// twice and schema_migrations still has exactly one row per file.
	s2, err := Open(dir, box, loc)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	if err := s2.W.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&migCount); err != nil {
		t.Fatal(err)
	}
	if migCount != wantMig {
		t.Errorf("after reopen, schema_migrations rows = %d, want %d", migCount, wantMig)
	}
}

func TestReadPoolIsReadOnly(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.R.Exec(`INSERT INTO settings VALUES('a','b')`); err == nil {
		t.Fatal("expected an error writing through the read pool, got nil")
	}
}

func TestMonitorCRUDRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	tag1 := &Tag{Name: "prod"}
	if err := s.CreateTag(ctx, tag1); err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	tag2 := &Tag{Name: "web"}
	if err := s.CreateTag(ctx, tag2); err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	ch := &Channel{
		Name: "ops-slack",
		Type: "slack",
		Config: ChannelConfig{
			URL: "https://hooks.slack.com/services/T000/B000/xyz",
		},
	}
	if err := s.CreateChannel(ctx, ch); err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	m := model.NewMonitor(model.TypeHTTP)
	m.Name = "API"
	m.Target = "https://api.example.com/health"
	m.Settings.Headers = []model.Header{
		{Name: "X-Env", Value: "prod"},
		{Name: "Authorization", Secret: true},
	}
	m.Secrets.Headers = map[string]string{"Authorization": "Bearer abc"}
	m.TagIDs = []int64{tag1.ID, tag2.ID}
	m.ChannelIDs = []int64{ch.ID}

	if err := s.CreateMonitor(ctx, m); err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	if m.ID == 0 {
		t.Fatal("CreateMonitor did not set ID")
	}
	if m.CreatedAt.IsZero() || m.UpdatedAt.IsZero() {
		t.Fatal("CreateMonitor did not set CreatedAt/UpdatedAt")
	}

	got, err := s.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if got.Name != m.Name || got.Target != m.Target {
		t.Errorf("GetMonitor name/target = %q/%q, want %q/%q", got.Name, got.Target, m.Name, m.Target)
	}
	if len(got.Settings.Headers) != 2 {
		t.Errorf("Settings.Headers = %+v, want 2 entries", got.Settings.Headers)
	}
	if got.Secrets.Headers["Authorization"] != "Bearer abc" {
		t.Errorf(`Secrets.Headers["Authorization"] = %q, want "Bearer abc"`, got.Secrets.Headers["Authorization"])
	}

	wantTags := []int64{tag1.ID, tag2.ID}
	sortInt64s(wantTags)
	gotTags := append([]int64(nil), got.TagIDs...)
	sortInt64s(gotTags)
	if !int64SlicesEqual(gotTags, wantTags) {
		t.Errorf("GetMonitor TagIDs = %v, want %v", gotTags, wantTags)
	}
	if !int64SlicesEqual(got.ChannelIDs, []int64{ch.ID}) {
		t.Errorf("GetMonitor ChannelIDs = %v, want %v", got.ChannelIDs, []int64{ch.ID})
	}

	var enc []byte
	if err := s.W.QueryRowContext(ctx, `SELECT secrets_enc FROM monitors WHERE id=?`, m.ID).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte("Bearer abc")) {
		t.Error("raw secrets_enc column contains the plaintext secret")
	}

	list, err := s.ListMonitors(ctx)
	if err != nil {
		t.Fatalf("ListMonitors: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListMonitors returned %d monitors, want 1", len(list))
	}
	lm := list[0]
	lmTags := append([]int64(nil), lm.TagIDs...)
	sortInt64s(lmTags)
	if !int64SlicesEqual(lmTags, wantTags) {
		t.Errorf("ListMonitors TagIDs = %v, want %v", lmTags, wantTags)
	}
	if !int64SlicesEqual(lm.ChannelIDs, []int64{ch.ID}) {
		t.Errorf("ListMonitors ChannelIDs = %v, want %v", lm.ChannelIDs, []int64{ch.ID})
	}
}

func TestPushTokenGenerated(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m := model.NewMonitor(model.TypePush)
	m.Name = "push-1"
	if err := s.CreateMonitor(ctx, m); err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	if len(m.PushToken) != 32 {
		t.Fatalf("PushToken length = %d, want 32", len(m.PushToken))
	}
	for _, r := range m.PushToken {
		if !isAlnum(r) {
			t.Fatalf("PushToken %q contains a non [A-Za-z0-9] character", m.PushToken)
		}
	}

	id, err := s.MonitorByPushToken(ctx, m.PushToken)
	if err != nil {
		t.Fatalf("MonitorByPushToken: %v", err)
	}
	if id != m.ID {
		t.Errorf("MonitorByPushToken id = %d, want %d", id, m.ID)
	}

	if _, err := s.MonitorByPushToken(ctx, "not-a-real-token-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("MonitorByPushToken unknown token error = %v, want ErrNotFound", err)
	}
}

func TestUpdateKeepsSecretsWhenNil(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m := model.NewMonitor(model.TypeHTTP)
	m.Name = "svc"
	m.Target = "https://svc.example.com"
	m.Secrets.BearerToken = "secret-token"
	if err := s.CreateMonitor(ctx, m); err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	got, err := s.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	got.Name = "svc-renamed"
	// got.Secrets is exactly what GetMonitor returned: the store contract
	// is that this is carried through unchanged.
	if err := s.UpdateMonitor(ctx, got); err != nil {
		t.Fatalf("UpdateMonitor (carry secrets): %v", err)
	}

	after, err := s.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if after.Secrets.BearerToken != "secret-token" {
		t.Errorf("BearerToken after update carrying secrets = %q, want %q", after.Secrets.BearerToken, "secret-token")
	}

	after.Secrets = model.Secrets{}
	if err := s.UpdateMonitor(ctx, after); err != nil {
		t.Fatalf("UpdateMonitor (clear secrets): %v", err)
	}

	cleared, err := s.GetMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if cleared.Secrets.BearerToken != "" {
		t.Errorf("BearerToken after clearing = %q, want empty", cleared.Secrets.BearerToken)
	}
}

func TestDeleteMonitorCascades(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	m1 := model.NewMonitor(model.TypeHTTP)
	m1.Name, m1.Target = "m1", "https://a.example.com"
	if err := s.CreateMonitor(ctx, m1); err != nil {
		t.Fatalf("CreateMonitor m1: %v", err)
	}
	m2 := model.NewMonitor(model.TypeHTTP)
	m2.Name, m2.Target = "m2", "https://b.example.com"
	if err := s.CreateMonitor(ctx, m2); err != nil {
		t.Fatalf("CreateMonitor m2: %v", err)
	}

	seed := func(id int64) {
		t.Helper()
		now := time.Now().UnixMilli()
		if _, err := s.W.ExecContext(ctx, `INSERT INTO heartbeats(monitor_id, ts, status) VALUES (?, ?, 1)`, id, now); err != nil {
			t.Fatalf("seed heartbeats: %v", err)
		}
		if _, err := s.W.ExecContext(ctx, `INSERT INTO rollup_hourly(monitor_id, bucket) VALUES (?, 0)`, id); err != nil {
			t.Fatalf("seed rollup_hourly: %v", err)
		}
		if _, err := s.W.ExecContext(ctx, `INSERT INTO rollup_daily(monitor_id, bucket) VALUES (?, 0)`, id); err != nil {
			t.Fatalf("seed rollup_daily: %v", err)
		}
		if _, err := s.W.ExecContext(ctx, `INSERT INTO outages(monitor_id, started_at) VALUES (?, ?)`, id, now); err != nil {
			t.Fatalf("seed outages: %v", err)
		}
		if _, err := s.W.ExecContext(ctx, `INSERT INTO monitor_runtime(monitor_id) VALUES (?)`, id); err != nil {
			t.Fatalf("seed monitor_runtime: %v", err)
		}
		if _, err := s.W.ExecContext(ctx, `INSERT INTO status_events(monitor_id, ts, from_status, to_status) VALUES (?, ?, 1, 4)`, id, now); err != nil {
			t.Fatalf("seed status_events: %v", err)
		}
	}
	seed(m1.ID)
	seed(m2.ID)

	if err := s.DeleteMonitor(ctx, m1.ID); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}

	for _, table := range []string{"heartbeats", "rollup_hourly", "rollup_daily", "outages", "monitor_runtime", "status_events"} {
		var n int
		if err := s.W.QueryRowContext(ctx, `SELECT count(*) FROM `+table+` WHERE monitor_id=?`, m1.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s: %d rows remain for deleted monitor m1", table, n)
		}

		if err := s.W.QueryRowContext(ctx, `SELECT count(*) FROM `+table+` WHERE monitor_id=?`, m2.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("%s: %d rows for untouched monitor m2, want 1", table, n)
		}
	}

	if _, err := s.GetMonitor(ctx, m1.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetMonitor after delete = %v, want ErrNotFound", err)
	}
}

func TestUsersAndAdmins(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	u, err := s.CreateUser(ctx, "Alice@Example.com", "Alice", RoleViewer, now)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.Role != RoleViewer {
		t.Errorf("new user role = %q, want %q", u.Role, RoleViewer)
	}
	if u.Email != "alice@example.com" {
		t.Errorf("email not lower-cased: %q", u.Email)
	}

	if err := s.SetUserRole(ctx, u.ID, RoleAdmin); err != nil {
		t.Fatalf("SetUserRole admin: %v", err)
	}

	got, err := s.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Role != RoleAdmin {
		t.Errorf("role after promotion = %q, want %q", got.Role, RoleAdmin)
	}

	if err := s.SetUserRole(ctx, u.ID, RoleViewer); !errors.Is(err, ErrConflict) {
		t.Errorf("SetUserRole demoting the only admin = %v, want ErrConflict", err)
	}

	// A second admin, so alice can safely be disabled.
	u2, err := s.CreateUser(ctx, "bob@example.com", "Bob", RoleViewer, now)
	if err != nil {
		t.Fatalf("CreateUser bob: %v", err)
	}
	if err := s.SetUserRole(ctx, u2.ID, RoleAdmin); err != nil {
		t.Fatalf("SetUserRole bob: %v", err)
	}
	if err := s.SetUserRole(ctx, u.ID, RoleDisabled); err != nil {
		t.Fatalf("SetUserRole disable alice: %v", err)
	}

	tokenHash := []byte("token-hash-1")
	if err := s.CreateSession(ctx, tokenHash, u.ID, "csrf-1", now.Add(24*time.Hour)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, _, err := s.GetSession(ctx, tokenHash, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSession for disabled user = %v, want ErrNotFound", err)
	}

	list, err := s.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("ListUsers = %d users, want 2", len(list))
	}
	_ = u2
}

func TestSessionsExpire(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	u, err := s.CreateUser(ctx, "carol@example.com", "Carol", RoleViewer, now)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	tokenHash := []byte("token-hash-expire")
	expires := now.Add(time.Hour)
	if err := s.CreateSession(ctx, tokenHash, u.ID, "csrf", expires); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sess, gotUser, err := s.GetSession(ctx, tokenHash, now)
	if err != nil {
		t.Fatalf("GetSession before expiry: %v", err)
	}
	if sess.UserID != u.ID || gotUser.ID != u.ID {
		t.Errorf("GetSession returned user %d/%d, want %d", sess.UserID, gotUser.ID, u.ID)
	}

	if _, _, err := s.GetSession(ctx, tokenHash, expires.Add(time.Second)); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSession after expiry = %v, want ErrNotFound", err)
	}

	n, err := s.PruneSessions(ctx, expires.Add(time.Second))
	if err != nil {
		t.Fatalf("PruneSessions: %v", err)
	}
	if n != 1 {
		t.Errorf("PruneSessions removed %d rows, want 1", n)
	}

	if _, _, err := s.GetSession(ctx, tokenHash, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSession after prune = %v, want ErrNotFound", err)
	}
}

func TestGroupTagUnique(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	g1 := &Group{Name: "Payments", Sort: 0}
	if err := s.CreateGroup(ctx, g1); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	g2 := &Group{Name: "PAYMENTS", Sort: 1}
	if err := s.CreateGroup(ctx, g2); !errors.Is(err, ErrConflict) {
		t.Errorf("CreateGroup duplicate (case-insensitive) name = %v, want ErrConflict", err)
	}
	g3 := &Group{Name: "Auth", Sort: 1}
	if err := s.CreateGroup(ctx, g3); err != nil {
		t.Fatalf("CreateGroup g3: %v", err)
	}

	if err := s.ReorderGroups(ctx, []int64{g3.ID, g1.ID}); err != nil {
		t.Fatalf("ReorderGroups: %v", err)
	}

	list, err := s.ListGroups(ctx)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(list) != 2 || list[0].ID != g3.ID || list[1].ID != g1.ID {
		t.Errorf("ListGroups after reorder = %+v, want [g3, g1]", list)
	}

	t1 := &Tag{Name: "prod", Color: "#111111"}
	if err := s.CreateTag(ctx, t1); err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	t2 := &Tag{Name: "PROD"}
	if err := s.CreateTag(ctx, t2); !errors.Is(err, ErrConflict) {
		t.Errorf("CreateTag duplicate (case-insensitive) name = %v, want ErrConflict", err)
	}
}

func TestChannelsEncrypted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	c := &Channel{
		Name: "webhook-1",
		Type: "webhook",
		Config: ChannelConfig{
			URL:     "https://hooks.example.com/secret-path-xyz",
			Headers: map[string]string{"X-Token": "shh"},
		},
	}
	if err := s.CreateChannel(ctx, c); err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	got, err := s.GetChannel(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if got.Config.URL != c.Config.URL {
		t.Errorf("Config.URL = %q, want %q", got.Config.URL, c.Config.URL)
	}

	var enc []byte
	if err := s.W.QueryRowContext(ctx, `SELECT config_enc FROM channels WHERE id=?`, c.ID).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte(c.Config.URL)) {
		t.Error("raw config_enc column contains the plaintext URL")
	}

	list, err := s.ListChannels(ctx)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if len(list) != 1 || list[0].Config.URL != c.Config.URL {
		t.Errorf("ListChannels = %+v", list)
	}
}

func TestSettingsDefaultsAndValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	got, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if !reflect.DeepEqual(got, DefaultSettings()) {
		t.Errorf("GetSettings on empty table = %+v, want %+v", got, DefaultSettings())
	}

	bad := DefaultSettings()
	bad.RetentionRawDays = 0
	if err := s.PutSettings(ctx, bad); err == nil {
		t.Error("PutSettings with retention 0 should fail")
	}

	good := DefaultSettings()
	good.RetentionRawDays = 30
	if err := s.PutSettings(ctx, good); err != nil {
		t.Fatalf("PutSettings: %v", err)
	}

	got2, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got2.RetentionRawDays != 30 {
		t.Errorf("RetentionRawDays after PutSettings = %d, want 30", got2.RetentionRawDays)
	}
}

// TestSettingsBackupHourLegacyKey proves a database that still carries the
// pre-rename backup_hour_ist key keeps its schedule, and that saving moves it
// to the neutral backup_hour key.
func TestSettingsBackupHourLegacyKey(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.W.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES ('backup_hour_ist', '5')`); err != nil {
		t.Fatalf("seed legacy key: %v", err)
	}
	got, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.BackupHour != 5 {
		t.Fatalf("BackupHour from legacy key = %d, want 5", got.BackupHour)
	}

	got.BackupHour = 9
	if err := s.PutSettings(ctx, got); err != nil {
		t.Fatalf("PutSettings: %v", err)
	}
	var n int
	if err := s.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key = 'backup_hour_ist'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("legacy key rows after save = %d (err %v), want 0", n, err)
	}
	got2, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got2.BackupHour != 9 {
		t.Fatalf("BackupHour after save = %d, want 9", got2.BackupHour)
	}
}

// TestSettingsJSONLegacyBackupHour proves the old JSON key is accepted, the
// new one wins when both are present, and unknown keys are still rejected.
func TestSettingsJSONLegacyBackupHour(t *testing.T) {
	var v Settings
	if err := json.Unmarshal([]byte(`{"backupHourIst": 4}`), &v); err != nil || v.BackupHour != 4 {
		t.Fatalf("legacy key: hour = %d, err = %v; want 4", v.BackupHour, err)
	}
	v = Settings{}
	if err := json.Unmarshal([]byte(`{"backupHourIst": 4, "backupHour": 6}`), &v); err != nil || v.BackupHour != 6 {
		t.Fatalf("both keys: hour = %d, err = %v; want 6", v.BackupHour, err)
	}
	out, err := json.Marshal(Settings{BackupHour: 7})
	if err != nil || !strings.Contains(string(out), `"backupHour":7`) || strings.Contains(string(out), "Ist") {
		t.Fatalf("marshal = %s, err = %v", out, err)
	}
	if err := json.Unmarshal([]byte(`{"nope": 1}`), &v); err == nil {
		t.Fatal("unknown field should be rejected")
	}
}

func int64SlicesEqual(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Every channel type the alert package accepts must be storable, and rows
// created before the type list was opened up must survive the migration.
func TestChannelsStoreEveryType(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, typ := range []string{"slack", "email", "teams", "gchat", "webhook", "pagerduty", "opsgenie",
		"discord", "telegram", "ntfy", "gotify", "pushover", "matrix", "mattermost", "rocketchat"} {
		c := &Channel{Name: "ch " + typ, Type: typ, Config: ChannelConfig{URL: "https://example.invalid/" + typ, Target: "t"}}
		if err := s.CreateChannel(ctx, c); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		got, err := s.GetChannel(ctx, c.ID)
		if err != nil || got.Type != typ || got.Config.Target != "t" {
			t.Fatalf("%s round trip: %+v %v", typ, got, err)
		}
	}
	if err := s.CreateChannel(ctx, &Channel{Name: "empty", Type: "", Config: ChannelConfig{}}); err == nil {
		t.Fatal("an empty type must still be rejected")
	}
}

// A fresh database must be able to store a monitor of every type the model
// accepts: a hard-coded type list in the schema would reject newer ones.
func TestMonitorsStoreEveryType(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, typ := range model.AllTypes {
		m := model.NewMonitor(typ)
		m.Name = "m " + string(typ)
		if typ != model.TypePush {
			m.Target = "127.0.0.1:9"
		}
		if err := s.CreateMonitor(ctx, m); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		got, err := s.GetMonitor(ctx, m.ID)
		if err != nil || got.Type != typ {
			t.Fatalf("%s round trip: %+v %v", typ, got, err)
		}
	}
}
