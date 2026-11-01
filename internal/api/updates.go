package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"opskest/internal/auth"
	"opskest/internal/logx"
	"opskest/internal/model"
	"opskest/internal/store"
	"opskest/internal/update"
)

// The update endpoints are admin only. They never reveal or log the feed
// URL beyond its host, nor the release key; the key is write-only (the
// API says whether one is set and shows a short fingerprint).

// NewUpdateManager wires the self-update manager to the app: settings from
// the store (the build-time key as fallback), a database backup before any
// install, a graceful-shutdown restart and audit rows for background
// actions. app.Run builds one and also runs its background checker; New
// builds a default one when Deps.Updates is nil.
func NewUpdateManager(d Deps) *update.Manager {
	log := d.Log
	now := d.Now
	if now == nil {
		now = time.Now
	}
	var loc *time.Location
	if d.Store != nil {
		loc = d.Store.Loc
	}
	if loc == nil && d.Cfg != nil {
		loc = d.Cfg.DisplayTZ
	}
	if loc == nil {
		loc = time.Local
	}
	bg := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 10*time.Second)
	}
	return update.New(update.Options{
		Version: d.Version,
		Log:     log,
		Now:     now,
		Loc:     loc,
		Config: func() update.Config {
			ctx, cancel := bg()
			defer cancel()
			cfg, _ := effectiveUpdateConfig(ctx, d.Store)
			return cfg
		},
		Backup: func(ctx context.Context) (string, error) {
			if d.Cfg == nil || d.Cfg.BackupDir == "" {
				return "", errors.New("no backup directory is configured")
			}
			keep := store.DefaultSettings().BackupKeep
			if s, err := d.Store.GetSettings(ctx); err == nil {
				keep = s.BackupKeep
			}
			t := now()
			path, err := d.Store.Backup(ctx, d.Cfg.BackupDir, t, keep)
			errMsg := ""
			if err != nil {
				errMsg = err.Error()
			}
			if rerr := d.Store.RecordBackupRun(ctx, t, err == nil, errMsg); rerr != nil && log != nil {
				logx.Err(log, "record backup run failed", rerr)
			}
			if err != nil {
				return "", err
			}
			return filepath.Base(path), nil
		},
		Restart: func(exe string) {
			update.RequestRestart(exe)
			if d.Shutdown != nil {
				d.Shutdown()
			}
		},
		Audit: func(action, detail string) {
			ctx, cancel := bg()
			defer cancel()
			d.Store.Audit(ctx, nil, action, "update", nil, detail)
		},
		LoadSaved: func() update.Saved {
			ctx, cancel := bg()
			defer cancel()
			l, err := d.Store.GetUpdateLast(ctx)
			if err != nil {
				return update.Saved{}
			}
			return update.Saved{CheckJSON: l.CheckJSON, InstallFailedAt: l.InstallFailedAt, SkipVersion: l.SkipVersion, PrevVersion: l.PrevVersion}
		},
		SaveSaved: func(s update.Saved) {
			ctx, cancel := bg()
			defer cancel()
			err := d.Store.PutUpdateLast(ctx, store.UpdateLast{CheckJSON: s.CheckJSON, InstallFailedAt: s.InstallFailedAt, SkipVersion: s.SkipVersion, PrevVersion: s.PrevVersion})
			if err != nil && log != nil {
				logx.Err(log, "save update state failed", err)
			}
		},
	})
}

// effectiveUpdateConfig merges the stored settings with the build-time key.
// keySource is "settings", "build" or "".
func effectiveUpdateConfig(ctx context.Context, st *store.Store) (cfg update.Config, keySource string) {
	us := store.DefaultUpdateSettings()
	if st != nil {
		if v, err := st.GetUpdateSettings(ctx); err == nil {
			us = v
		}
	}
	cfg = update.Config{Enabled: us.Enabled, FeedURL: us.FeedURL, Mode: us.Mode, AutoHour: us.AutoHour}
	if cfg.FeedURL == "" && update.ValidateFeedURL(update.BuiltinFeedURL()) == nil {
		cfg.FeedURL = update.BuiltinFeedURL()
	}
	switch {
	case strings.TrimSpace(us.PublicKey) != "":
		cfg.PublicKey, keySource = strings.TrimSpace(us.PublicKey), "settings"
	case update.BuiltinPublicKey() != "":
		cfg.PublicKey, keySource = update.BuiltinPublicKey(), "build"
	}
	return cfg, keySource
}

type updateAssetOut struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	Size int64  `json:"size"`
}

type updateCheckOut struct {
	CheckedAt         *int64          `json:"checkedAt"`
	Error             string          `json:"error"`
	Latest            string          `json:"latest"`
	Available         bool            `json:"available"`
	Notes             string          `json:"notes"`
	PublishedAt       *int64          `json:"publishedAt"`
	SignatureVerified bool            `json:"signatureVerified"`
	Asset             *updateAssetOut `json:"asset"`
	MinVersion        string          `json:"minVersion"`
	MinVersionBlocked bool            `json:"minVersionBlocked"`
}

type updateSettingsOut struct {
	Enabled              bool   `json:"enabled"`
	FeedURL              string `json:"feedUrl"`
	Mode                 string `json:"mode"`
	AutoHour             int    `json:"autoHour"`
	PublicKeySet         bool   `json:"publicKeySet"`
	PublicKeySource      string `json:"publicKeySource"`
	PublicKeyFingerprint string `json:"publicKeyFingerprint"`
}

type updateStateOut struct {
	Phase   string `json:"phase"`
	Percent int    `json:"percent"`
	Error   string `json:"error"`
	// BackupName is the pre-update backup in the backups folder.
	BackupName string `json:"backupName"`
}

type updatesOut struct {
	Current             string            `json:"current"`
	InstallMethod       string            `json:"installMethod"`
	CanInstall          bool              `json:"canInstall"`
	CannotInstallReason string            `json:"cannotInstallReason"`
	Settings            updateSettingsOut `json:"settings"`
	Check               updateCheckOut    `json:"check"`
	State               updateStateOut    `json:"state"`
	CanRollback         bool              `json:"canRollback"`
	PreviousVersion     string            `json:"previousVersion"`
	DockerHint          string            `json:"dockerHint,omitempty"`
	// RestartMode is how the process restarts after an install or rollback:
	// "self" (it starts the new binary itself) or "exit" (it shuts down and
	// the process manager must start it; OPSKEST_UPDATE_RESTART=exit).
	// RestartHint is a plain-words sentence for the "restarting" screen.
	RestartMode string `json:"restartMode"`
	RestartHint string `json:"restartHint"`
	// NextCheckAt is when the next automatic check is due (unix ms, null =
	// not scheduled yet or checks are off). AutoSkipVersion is a version that
	// auto mode will not install (it was rolled back from). LastInstallFailedAt
	// is the time of the last failed install (unix ms, null = none).
	NextCheckAt         *int64 `json:"nextCheckAt"`
	AutoSkipVersion     string `json:"autoSkipVersion"`
	LastInstallFailedAt *int64 `json:"lastInstallFailedAt"`
}

// restartHint explains, for the screen shown while the instance restarts,
// what is about to happen on this platform.
func restartHint(mode string) string {
	switch {
	case mode == update.RestartExit:
		return "Opskest is shutting down. Your process manager (systemd, a Windows service wrapper) must start the new version; the page reconnects once it is back."
	case runtime.GOOS == "windows":
		return "Opskest is restarting itself in the background. If you started it in a terminal window it keeps running without that window's output; close it with Task Manager or taskkill."
	default:
		return "Opskest is restarting itself; the page reconnects in a few seconds."
	}
}

func msPtr(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}
	v := t.UnixMilli()
	return &v
}

// updatesPayload builds the GET payload.
func (s *server) updatesPayload(ctx context.Context) updatesOut {
	m := s.d.Updates
	st := m.Status()
	cfg, src := effectiveUpdateConfig(ctx, s.d.Store)
	out := updatesOut{
		Current:             st.Current,
		InstallMethod:       st.Method,
		CanInstall:          st.CanInstall,
		CannotInstallReason: st.Reason,
		CanRollback:         st.CanRollback,
		PreviousVersion:     st.PrevVersion,
		DockerHint:          st.DockerHint,
		RestartMode:         st.RestartMode,
		RestartHint:         restartHint(st.RestartMode),
		AutoSkipVersion:     st.SkipVersion,
		LastInstallFailedAt: msPtr(st.InstallFail),
		Settings: updateSettingsOut{
			Enabled: cfg.Enabled, FeedURL: cfg.FeedURL, Mode: cfg.Mode, AutoHour: cfg.AutoHour,
			PublicKeySet: cfg.PublicKey != "", PublicKeySource: src,
			PublicKeyFingerprint: update.KeyFingerprint(cfg.PublicKey),
		},
		State: updateStateOut{Phase: string(st.State.Phase), Percent: st.State.Percent, Error: st.State.Error, BackupName: st.State.BackupName},
	}
	if cfg.Enabled && cfg.Mode != update.ModeOff && cfg.FeedURL != "" {
		out.NextCheckAt = msPtr(st.NextCheck)
	}
	if c := st.Check; c != nil {
		out.Check = updateCheckOut{
			CheckedAt: msPtr(c.CheckedAt), Error: c.Error, Latest: c.Latest, Available: c.Available,
			Notes: c.Notes, PublishedAt: msPtr(c.PublishedAt), SignatureVerified: c.SignatureVerified,
			MinVersion: c.MinVersion, MinVersionBlocked: c.MinVersionBlocked,
		}
		if c.Asset != nil {
			out.Check.Asset = &updateAssetOut{OS: c.Asset.OS, Arch: c.Asset.Arch, Size: c.Asset.Size}
		}
	}
	return out
}

func (s *server) handleGetUpdates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.updatesPayload(r.Context()))
}

// feedHost is the only part of the feed URL that goes into audit rows.
func feedHost(raw string) string {
	if raw == "" {
		return "none"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "invalid"
	}
	return u.Host
}

func (s *server) handlePutUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled   *bool   `json:"enabled"`
		FeedURL   *string `json:"feedUrl"`
		Mode      *string `json:"mode"`
		AutoHour  *int    `json:"autoHour"`
		PublicKey *string `json:"publicKey"` // null/absent keeps, "" clears
	}
	if err := decode(r, &in, 16<<10); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON", nil)
		return
	}
	cur, err := s.d.Store.GetUpdateSettings(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	next := cur
	fields := model.FieldErrors{}
	if in.Enabled != nil {
		next.Enabled = *in.Enabled
	}
	if in.FeedURL != nil {
		v := strings.TrimSpace(*in.FeedURL)
		if err := update.ValidateSettingsFeedURL(v); err != nil {
			fields["feedUrl"] = "must be an https URL (http only for localhost or 127.0.0.1), or empty"
		} else {
			next.FeedURL = v
		}
	}
	if in.Mode != nil {
		switch *in.Mode {
		case update.ModeOff, update.ModeNotify, update.ModeAuto:
			next.Mode = *in.Mode
		default:
			fields["mode"] = "must be off, notify or auto"
		}
	}
	if in.AutoHour != nil {
		if *in.AutoHour < 0 || *in.AutoHour > 23 {
			fields["autoHour"] = "must be between 0 and 23"
		} else {
			next.AutoHour = *in.AutoHour
		}
	}
	keyChange := "unchanged"
	if in.PublicKey != nil {
		v := strings.TrimSpace(*in.PublicKey)
		if v == "" {
			next.PublicKey = ""
			keyChange = "cleared"
		} else if _, err := update.ParsePublicKey(v); err != nil {
			fields["publicKey"] = "must be a base64 ed25519 public key (32 bytes)"
		} else {
			next.PublicKey = v
			keyChange = "set"
		}
	}
	if len(fields) > 0 {
		writeErr(w, http.StatusBadRequest, "invalid update settings", fields)
		return
	}
	if err := s.d.Store.PutUpdateSettings(r.Context(), next); err != nil {
		if errors.Is(err, store.ErrInvalid) {
			writeErr(w, http.StatusBadRequest, "invalid update settings", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "save failed", nil)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "update_settings", "update", nil,
		fmt.Sprintf("enabled=%t mode=%s hour=%d feed=%s key=%s", next.Enabled, next.Mode, next.AutoHour, feedHost(next.FeedURL), keyChange))
	writeJSON(w, http.StatusOK, s.updatesPayload(r.Context()))
}

func (s *server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	discardBody(r)
	m := s.d.Updates
	if st := m.State(); st.Phase != update.PhaseIdle && st.Phase != update.PhaseFailed {
		writeErr(w, http.StatusConflict, "an update operation is already running", nil)
		return
	}
	if ok, retry := m.AllowManualCheck(); !ok {
		secs := int(retry/time.Second) + 1
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeErr(w, http.StatusTooManyRequests, "checked a moment ago: try again in a few seconds", nil)
		return
	}
	// Detached from the request so a closed tab does not record a
	// "canceled" check; the fetch has its own timeouts.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 45*time.Second)
	defer cancel()
	if _, err := m.Check(ctx); errors.Is(err, update.ErrBusy) {
		writeErr(w, http.StatusConflict, "an update operation is already running", nil)
		return
	} else if errors.Is(err, update.ErrDisabled) {
		writeErr(w, http.StatusConflict, err.Error(), nil)
		return
	}
	// Any other failure is part of the payload (check.error).
	writeJSON(w, http.StatusOK, s.updatesPayload(r.Context()))
}

func (s *server) handleUpdateInstall(w http.ResponseWriter, r *http.Request) {
	discardBody(r)
	m := s.d.Updates
	st := m.Status()
	if !st.CanInstall {
		writeErr(w, http.StatusConflict, st.Reason, nil)
		return
	}
	if c := st.Check; c != nil && c.Error == "" && c.Latest != "" && !c.Available {
		writeErr(w, http.StatusConflict, "no update is available: this is the latest version", nil)
		return
	}
	if err := m.StartInstall(); err != nil {
		s.writeUpdateStartErr(w, err)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	to := ""
	if st.Check != nil {
		to = st.Check.Latest
	}
	s.d.Store.Audit(r.Context(), &u.ID, "update_install", "update", nil, fmt.Sprintf("%s -> %s", st.Current, to))
	writeJSON(w, http.StatusAccepted, s.updatesPayload(r.Context()))
}

func (s *server) handleUpdateRollback(w http.ResponseWriter, r *http.Request) {
	discardBody(r)
	m := s.d.Updates
	if err := m.StartRollback(); err != nil {
		s.writeUpdateStartErr(w, err)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "update_rollback", "update", nil, "from "+m.Current())
	writeJSON(w, http.StatusAccepted, s.updatesPayload(r.Context()))
}

func (s *server) writeUpdateStartErr(w http.ResponseWriter, err error) {
	var cant *update.CannotInstallError
	switch {
	case errors.Is(err, update.ErrBusy):
		writeErr(w, http.StatusConflict, "an update operation is already running", nil)
	case errors.As(err, &cant):
		writeErr(w, http.StatusConflict, cant.Reason, nil)
	default:
		if s.d.Log != nil {
			logx.Err(s.d.Log, "update start failed", err)
		}
		writeErr(w, http.StatusInternalServerError, "could not start the update", nil)
	}
}
