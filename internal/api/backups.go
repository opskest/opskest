package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"opskest/internal/auth"
	"opskest/internal/logx"
	"opskest/internal/store"
)

// maxRestoreUploadBytes bounds POST /api/backups/upload: 1 GiB of
// database content plus a little slack for multipart framing overhead.
const maxRestoreUploadBytes = 1<<30 + (1 << 20)

type backupOut struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	CreatedAt int64  `json:"createdAt"`
}

type lastRunOut struct {
	At    int64  `json:"at"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (s *server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	files, err := store.ListAllBackupFiles(s.d.Cfg.BackupDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed", nil)
		return
	}
	out := make([]backupOut, 0, len(files))
	for _, f := range files {
		out = append(out, backupOut{Name: f.Name, Size: f.Size, CreatedAt: f.ModTime.UnixMilli()})
	}

	settings, err := s.d.Store.GetSettings(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}

	resp := map[string]any{
		"backups": out,
		// The scheduler's calendar is the configured display timezone.
		"schedule": map[string]any{"hour": settings.BackupHour, "timeZone": s.d.Store.Loc.String(), "keep": settings.BackupKeep},
	}
	if run, err := s.d.Store.GetLastBackupRun(r.Context()); err == nil && run != nil {
		resp["lastRun"] = lastRunOut{At: run.At.UnixMilli(), OK: run.OK, Error: run.Error}
	} else {
		resp["lastRun"] = nil
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleRunBackupNow(w http.ResponseWriter, r *http.Request) {
	discardBody(r)

	settings, err := s.d.Store.GetSettings(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}

	now := s.d.Now()
	path, backupErr := s.d.Store.Backup(r.Context(), s.d.Cfg.BackupDir, now, settings.BackupKeep)
	errMsg := ""
	if backupErr != nil {
		errMsg = backupErr.Error()
	}
	if err := s.d.Store.RecordBackupRun(r.Context(), now, backupErr == nil, errMsg); err != nil {
		logx.Err(s.d.Log, "record backup run failed", err)
	}
	if backupErr != nil {
		logx.Err(s.d.Log, "manual backup failed", backupErr)
		writeErr(w, http.StatusInternalServerError, "backup failed", nil)
		return
	}

	u, _ := auth.UserFrom(r.Context())
	name := filepath.Base(path)
	s.d.Store.Audit(r.Context(), &u.ID, "backup_now", "backup", nil, name)

	out := backupOut{Name: name}
	if info, err := os.Stat(path); err == nil {
		out.Size = info.Size()
		out.CreatedAt = info.ModTime().UnixMilli()
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *server) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path, err := store.UptimeBackupFilePath(s.d.Cfg.BackupDir, name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}

	f, err := os.Open(path)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "stat failed", nil)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	http.ServeContent(w, r, name, info.ModTime(), f)
}

func (s *server) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := store.DeleteBackupFile(s.d.Cfg.BackupDir, name); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "delete failed", nil)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "backup_delete", "backup", nil, name)
	w.WriteHeader(http.StatusNoContent)
}

// handleRestoreBackup stages an existing backup (or pre-restore safety
// copy) in BackupDir as the next boot's database, audits it, tells the
// caller the process is restarting, then triggers that restart. The
// actual swap happens at the top of the next boot (store.BootSwapRestore),
// never here: this handler's own store is still the live database.
func (s *server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	discardBody(r)
	name := r.PathValue("name")

	path, err := store.BackupFilePath(s.d.Cfg.BackupDir, name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	if err := store.ValidateBackupFile(path); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid backup file: "+err.Error(), nil)
		return
	}
	if _, err := store.StageRestore(s.d.Cfg.DataDir, path); err != nil {
		logx.Err(s.d.Log, "stage restore failed", err)
		writeErr(w, http.StatusInternalServerError, "stage failed", nil)
		return
	}

	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "restore_stage", "backup", nil, name)

	writeJSON(w, http.StatusAccepted, map[string]bool{"restarting": true})
	s.triggerRestart()
}

// handleUploadRestore accepts a multipart-uploaded .db file, validates
// and stages it exactly like handleRestoreBackup, then restarts.
func (s *server) handleUploadRestore(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRestoreUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid upload (over 1 GiB, or not multipart/form-data)", nil)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing file field", nil)
		return
	}
	defer file.Close()
	if !strings.HasSuffix(strings.ToLower(hdr.Filename), ".db") {
		writeErr(w, http.StatusBadRequest, "file must be a .db file", nil)
		return
	}

	tmp, err := os.CreateTemp(s.d.Cfg.DataDir, "upload-restore-*.db")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "upload failed", nil)
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, file); err != nil {
		tmp.Close()
		writeErr(w, http.StatusBadRequest, "upload failed", nil)
		return
	}
	if err := tmp.Close(); err != nil {
		writeErr(w, http.StatusInternalServerError, "upload failed", nil)
		return
	}

	if err := store.ValidateBackupFile(tmpPath); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid backup file: "+err.Error(), nil)
		return
	}
	if _, err := store.StageRestore(s.d.Cfg.DataDir, tmpPath); err != nil {
		logx.Err(s.d.Log, "stage uploaded restore failed", err)
		writeErr(w, http.StatusInternalServerError, "stage failed", nil)
		return
	}

	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "restore_upload", "backup", nil, hdr.Filename)

	writeJSON(w, http.StatusAccepted, map[string]bool{"restarting": true})
	s.triggerRestart()
}

// triggerRestart asks the app to shut down gracefully shortly after the
// response has had a chance to flush; docker-compose's `restart:
// unless-stopped` brings the process back, running BootSwapRestore at the
// top of the next boot. Tests that never wire Deps.Shutdown see this as a
// no-op, so they can exercise staging without also tearing down their
// httptest server.
func (s *server) triggerRestart() {
	if s.d.Shutdown == nil {
		return
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.d.Shutdown()
	}()
}
