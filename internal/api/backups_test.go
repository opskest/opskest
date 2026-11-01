package api

import (
	"context"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opskest/internal/store"
)

func TestBackupsListEmpty(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(http.MethodGet, "/api/backups", nil, "admin")
	if resp.status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.status, resp.body)
	}
	var out struct {
		Backups  []backupOut `json:"backups"`
		LastRun  *lastRunOut `json:"lastRun"`
		Schedule struct {
			Hour     int    `json:"hour"`
			TimeZone string `json:"timeZone"`
			Keep     int    `json:"keep"`
		} `json:"schedule"`
	}
	resp.json(t, &out)
	if out.Schedule.Hour != store.DefaultSettings().BackupHour || out.Schedule.TimeZone != env.store.Loc.String() {
		t.Fatalf("schedule = %+v, want configured hour and display timezone", out.Schedule)
	}
	if len(out.Backups) != 0 {
		t.Fatalf("backups = %v, want empty", out.Backups)
	}
	if out.LastRun != nil {
		t.Fatalf("lastRun = %+v, want nil before any backup", out.LastRun)
	}
}

func TestBackupsRequireAdmin(t *testing.T) {
	env := newTestEnv(t)
	for _, path := range []string{"/api/backups"} {
		if resp := env.do(http.MethodGet, path, nil, "viewer"); resp.status != http.StatusForbidden {
			t.Fatalf("viewer GET %s status = %d, want 403", path, resp.status)
		}
		if resp := env.do(http.MethodGet, path, nil, ""); resp.status != http.StatusUnauthorized {
			t.Fatalf("anon GET %s status = %d, want 401", path, resp.status)
		}
	}
}

func TestBackupNowThenListDownloadDelete(t *testing.T) {
	env := newTestEnv(t)
	env.createMonitor(httpMonitorIn("m1", "http://example.invalid"))

	resp := env.do(http.MethodPost, "/api/backups", nil, "admin")
	if resp.status != http.StatusCreated {
		t.Fatalf("POST /api/backups status = %d, body = %s", resp.status, resp.body)
	}
	var created backupOut
	resp.json(t, &created)
	if created.Name == "" || created.Size == 0 {
		t.Fatalf("created backup = %+v, want a name and non-zero size", created)
	}
	if !store.IsBackupFileName(created.Name) {
		t.Fatalf("created backup name %q doesn't match the opskest-*.db shape", created.Name)
	}

	// It shows up in the listing, and lastRun now reflects it.
	listResp := env.do(http.MethodGet, "/api/backups", nil, "admin")
	var list struct {
		Backups []backupOut `json:"backups"`
		LastRun *lastRunOut `json:"lastRun"`
	}
	listResp.json(t, &list)
	if len(list.Backups) != 1 || list.Backups[0].Name != created.Name {
		t.Fatalf("backups = %+v, want exactly the one just created", list.Backups)
	}
	if list.LastRun == nil || !list.LastRun.OK {
		t.Fatalf("lastRun = %+v, want ok=true", list.LastRun)
	}

	// Download round-trips the actual file bytes.
	dlResp := env.do(http.MethodGet, "/api/backups/"+created.Name+"/download", nil, "admin")
	if dlResp.status != http.StatusOK {
		t.Fatalf("download status = %d", dlResp.status)
	}
	onDisk, err := os.ReadFile(filepath.Join(env.cfg.BackupDir, created.Name))
	if err != nil {
		t.Fatalf("read backup on disk: %v", err)
	}
	if string(dlResp.body) != string(onDisk) {
		t.Fatalf("downloaded body doesn't match the file on disk")
	}

	// A name that doesn't match the strict opskest-*.db shape 404s, never
	// leaking whether some other file exists.
	if resp := env.do(http.MethodGet, "/api/backups/not-a-backup.db/download", nil, "admin"); resp.status != http.StatusNotFound {
		t.Fatalf("bad name status = %d, want 404", resp.status)
	}

	// Delete removes it; deleting again 404s.
	delResp := env.do(http.MethodDelete, "/api/backups/"+created.Name, nil, "admin")
	if delResp.status != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", delResp.status, delResp.body)
	}
	if resp := env.do(http.MethodDelete, "/api/backups/"+created.Name, nil, "admin"); resp.status != http.StatusNotFound {
		t.Fatalf("delete again status = %d, want 404", resp.status)
	}

	listResp2 := env.do(http.MethodGet, "/api/backups", nil, "admin")
	var list2 struct {
		Backups []backupOut `json:"backups"`
	}
	listResp2.json(t, &list2)
	if len(list2.Backups) != 0 {
		t.Fatalf("backups after delete = %v, want empty", list2.Backups)
	}
}

func TestRestoreByNameStagesAndAudits(t *testing.T) {
	env := newTestEnv(t)
	env.createMonitor(httpMonitorIn("m1", "http://example.invalid"))

	resp := env.do(http.MethodPost, "/api/backups", nil, "admin")
	var created backupOut
	resp.json(t, &created)

	restoreResp := env.do(http.MethodPost, "/api/backups/"+created.Name+"/restore", nil, "admin")
	if restoreResp.status != http.StatusAccepted {
		t.Fatalf("restore status = %d, body = %s", restoreResp.status, restoreResp.body)
	}
	var out map[string]bool
	restoreResp.json(t, &out)
	if !out["restarting"] {
		t.Fatalf("restore response = %v, want restarting=true", out)
	}

	pending := filepath.Join(env.cfg.DataDir, store.RestorePendingFileName)
	if _, err := os.Stat(pending); err != nil {
		t.Fatalf("restore-pending.db should exist: %v", err)
	}
	if err := store.ValidateBackupFile(pending); err != nil {
		t.Fatalf("staged file should validate: %v", err)
	}

	entries, err := env.store.ListAudit(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Action == "restore_stage" {
			found = true
		}
	}
	if !found {
		t.Fatalf("audit log = %+v, want a restore_stage entry", entries)
	}
}

func TestRestoreUnknownNameNotFound(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(http.MethodPost, "/api/backups/opskest-20200101-0000.db/restore", nil, "admin")
	if resp.status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.status)
	}
}

func TestUploadRestoreValidatesAndStages(t *testing.T) {
	env := newTestEnv(t)
	env.createMonitor(httpMonitorIn("m1", "http://example.invalid"))

	resp := env.do(http.MethodPost, "/api/backups", nil, "admin")
	var created backupOut
	resp.json(t, &created)
	goodBytes, err := os.ReadFile(filepath.Join(env.cfg.BackupDir, created.Name))
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}

	body, contentType := multipartFile(t, "file", "my-upload.db", goodBytes)
	upResp := env.doRaw(http.MethodPost, "/api/backups/upload", contentType, body, "admin")
	if upResp.status != http.StatusAccepted {
		t.Fatalf("upload restore status = %d, body = %s", upResp.status, upResp.body)
	}

	pending := filepath.Join(env.cfg.DataDir, store.RestorePendingFileName)
	staged, err := os.ReadFile(pending)
	if err != nil {
		t.Fatalf("read staged file: %v", err)
	}
	if string(staged) != string(goodBytes) {
		t.Fatalf("staged file doesn't match the uploaded bytes")
	}
}

func TestUploadRestoreRejectsGarbage(t *testing.T) {
	env := newTestEnv(t)
	body, contentType := multipartFile(t, "file", "bad.db", []byte("not a real database"))
	resp := env.doRaw(http.MethodPost, "/api/backups/upload", contentType, body, "admin")
	if resp.status != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s, want 400", resp.status, resp.body)
	}
	if _, err := os.Stat(filepath.Join(env.cfg.DataDir, store.RestorePendingFileName)); !os.IsNotExist(err) {
		t.Fatalf("a rejected upload must never be staged")
	}
}

func TestUploadRestoreRejectsWrongExtension(t *testing.T) {
	env := newTestEnv(t)
	body, contentType := multipartFile(t, "file", "not-a-db.txt", []byte("whatever"))
	resp := env.doRaw(http.MethodPost, "/api/backups/upload", contentType, body, "admin")
	if resp.status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.status)
	}
}

// multipartFile builds a single-file multipart/form-data body and returns
// it with its Content-Type header value (including the boundary).
func multipartFile(t *testing.T, field, filename string, content []byte) ([]byte, string) {
	t.Helper()
	var buf strings.Builder
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return []byte(buf.String()), mw.FormDataContentType()
}
