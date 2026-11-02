package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"opskest/internal/alert"
	"opskest/internal/api"
	"opskest/internal/auth"
	"opskest/internal/check"
	"opskest/internal/config"
	"opskest/internal/engine"
	"opskest/internal/hub"
	"opskest/internal/latalert"
	"opskest/internal/model"
	"opskest/internal/notify"
	"opskest/internal/regions"
	"opskest/internal/scheduler"
	"opskest/internal/secret"
	"opskest/internal/store"
)

// checkWritable creates and removes a probe file in dir to prove the
// process can actually write there, and fails with a message that tells
// the operator exactly what to run. This is checked before
// store.Open so a read-only bind mount (the most common first-boot
// mistake: host dirs created by root, the container running as uid 65532)
// fails fast with a clear instruction instead of a deep, confusing sqlite
// error.
func checkWritable(dir string) error {
	notWritable := fmt.Errorf("cannot write %s: on the host run  sudo chown -R 65532:65532 %s  (the container runs as uid 65532)", dir, dir)

	// The bind mount is expected to already exist (docker-compose creates
	// it), but create it if missing so a fresh `./data`/`./backups` on the
	// host doesn't itself count as the failure.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return notWritable
	}
	probe := filepath.Join(dir, ".opskest-writable-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return notWritable
	}
	_ = f.Close()
	if err := os.Remove(probe); err != nil {
		return notWritable
	}
	return nil
}

type alertLookup struct {
	eng *engine.Engine
}

func (l *alertLookup) AlertInfo(id int64) (alert.Info, bool) {
	if l.eng == nil {
		return alert.Info{}, false
	}
	info, ok := l.eng.AlertInfo(id)
	if !ok {
		return alert.Info{}, false
	}
	return alert.Info{
		Name:       info.Name,
		Group:      info.Group,
		Target:     info.Target,
		Runbook:    info.Runbook,
		ChannelIDs: info.ChannelIDs,
	}, true
}

// Options allows callers (like tests) to override or mock parts of the system.
type Options struct {
	Checker func(context.Context, *model.Monitor) model.Result
	Now     func() time.Time
}

// Run executes the opskest application lifecycle until ctx is cancelled.
func Run(ctx context.Context, cfg *config.Config, version string, log *slog.Logger) error {
	return RunWithOptions(ctx, cfg, version, log, Options{})
}

// RunWithOptions runs the application with custom options (used for testing).
func RunWithOptions(ctx context.Context, cfg *config.Config, version string, log *slog.Logger, opts Options) error {
	if log == nil {
		log = slog.Default()
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	// 1. Writability check: fail fast and clearly, before opening
	// the database, if the bind-mounted data/backup dirs aren't writable by
	// the container's uid. This is the single most common first-boot
	// failure (host dirs created by root, container running as 65532).
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	if cfg.BackupDir != "" {
		if err := os.MkdirAll(cfg.BackupDir, 0o755); err != nil {
			return fmt.Errorf("create backup dir: %w", err)
		}
	}
	if err := checkWritable(cfg.DataDir); err != nil {
		return err
	}
	if cfg.BackupDir != "" {
		if err := checkWritable(cfg.BackupDir); err != nil {
			return err
		}
	}

	// 2. Secret box. With no SECRET_ENCRYPTION_KEY the key is generated once
	// and kept beside the database, so a plain install needs no setup.
	created, err := cfg.ResolveSecretKey()
	if err != nil {
		return fmt.Errorf("encryption key: %w", err)
	}
	if created {
		log.Warn("generated a new encryption key; keep it with your backups, they cannot be read without it",
			"file", filepath.Join(cfg.DataDir, config.KeyFileName))
	}
	if cfg.AppURLDefaulted {
		log.Warn("APP_URL is not set; using " + cfg.AppURL + ". Set it to the address people open (https for a public install)")
	}
	box, err := secret.New(cfg.SecretKey)
	if err != nil {
		return fmt.Errorf("secret box: %w", err)
	}

	// 2b. Boot-time restore swap: if a restore was staged (POST
	// /api/backups/{name}/restore or /api/backups/upload), swap it into
	// place now, before Open. This must run after the writability checks
	// (it needs to write both dirs) and before Open (it replaces the file
	// Open is about to open). It never fails boot: see BootSwapRestore's
	// doc comment for why every internal failure is logged and swallowed
	// instead of propagated.
	if err := store.BootSwapRestore(cfg.DataDir, cfg.BackupDir, nowFn(), log); err != nil {
		return fmt.Errorf("boot restore: %w", err)
	}

	// 3. Open store
	st, err := store.Open(cfg.DataDir, box, cfg.DisplayTZ)
	if err != nil {
		return fmt.Errorf("store open: %w", err)
	}
	defer st.Close()

	// runCtx/cancelRun let an admin-triggered restart (POST
	// /api/backups/{name}/restore, /api/backups/upload) shut the process
	// down through the exact same graceful path as a SIGTERM, without
	// tearing down the parent ctx a caller (e.g. a test) still owns:
	// cancelRun only cancels this derived context, while ctx.Done() still
	// cascades into it normally.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	// 4. Batched writer
	writer := st.NewWriter(log)
	writer.Start(runCtx)
	defer writer.Close()

	// 5. Alert dispatcher
	lookup := &alertLookup{}
	dispatcher := alert.NewDispatcher(st, writer, lookup, alert.Options{
		AppURL: cfg.AppURL,
		Loc:    st.Loc,
		Now:    nowFn,
		Log:    log,
		Settings: func() (int, time.Duration) {
			s, err := st.GetSettings(context.Background())
			if err != nil {
				return 5, 30 * time.Second
			}
			return s.StormThreshold, time.Duration(s.StormWindowS) * time.Second
		},
	})
	dispatcher.Start(runCtx)
	defer dispatcher.Stop()

	// 5b. Multi-region outage confirmation (opt-in), wrapping the alert sink.
	confirmer := regions.New(dispatcher, st, writer, func() regions.Config {
		ctx := context.Background()
		s, err := st.GetSettings(ctx)
		if err != nil {
			return regions.Config{}
		}
		token, _ := st.GlobalpingToken(ctx)
		return regions.Config{
			Enabled: s.RegionConfirm == 1, Sweep: s.RegionSweep == 1, Alerts: s.RegionAlerts == 1,
			Countries: s.RegionCountries, TestsPerHour: s.RegionTestsPerHour, Token: token,
		}
	}, log, nowFn)
	confirmer.Start(runCtx)

	// 6. Engine
	checker := opts.Checker
	if checker == nil {
		checker = func(ctx context.Context, m *model.Monitor) model.Result {
			return check.Run(ctx, m, nowFn)
		}
	}
	eng := engine.New(engine.Deps{
		Store:  st,
		Writer: writer,
		Alerts: confirmer,
		// Guard: the internet-loss guard.
		// Without it the engine treats itself as always online, so a real
		// outage of opskest's own connectivity would be recorded as
		// hundreds of individual monitor failures instead of being
		// suppressed and surfaced once as a connectivity banner.
		Guard:   engine.NewNetGuard(nil, 0, 0, nowFn),
		Log:     log,
		Now:     nowFn,
		Checker: checker,
		RDAP:    check.NewRDAP(&http.Client{Timeout: 15 * time.Second}, ""),
	})
	lookup.eng = eng
	confirmer.SetHooks(regions.Hooks{
		Monitors: eng.Monitors,
		Monitor:  eng.Monitor,
		Status: func(id int64) model.Status {
			sum, _ := eng.Summary(id)
			return sum.Status
		},
	})

	// 7. Scheduler
	sched := scheduler.New(nil, cfg.CheckConcurrency, eng.RunCheck, nil)
	eng.SetScheduler(sched)

	// 8. Load monitors into engine
	if err := eng.Load(ctx); err != nil {
		return fmt.Errorf("engine load: %w", err)
	}

	// 9. Start scheduler & background maintenance / checks
	sched.Start(runCtx)
	defer sched.Stop()
	go eng.RunBackground(runCtx)
	// Response-time alerts: read the engine's rings, alert via the
	// dispatcher directly (no multi-region confirmation for slowness).
	go latalert.New(eng, st, dispatcher, nowFn, log).Run(runCtx)

	// 10. SSE Hub
	h := hub.New(eng, version, nowFn, log)
	go h.Run(runCtx)

	// 11. Auth
	// Local accounts are created through the one-time setup screen. No external
	// provider or environment-seeded administrator is required.
	a := auth.New(st, cfg, nowFn)

	// 12. API Router
	// 11b. Status page subscriber notifications (email via a page's email
	// channel, Slack webhooks), fed by the incidents API.
	notifier := notify.New(st, mailer{}, poster{}, cfg.AppURL, log)
	notifier.Start(runCtx)

	// Self-update manager: settings, backup-before-install and the
	// graceful restart are wired in api.NewUpdateManager; its background
	// checker runs until shutdown.
	updMgr := api.NewUpdateManager(api.Deps{Cfg: cfg, Store: st, Version: version, Log: log, Now: nowFn, Shutdown: cancelRun})
	go updMgr.Run(runCtx)

	apiHandler := api.New(api.Deps{
		Updates:    updMgr,
		Notify:     notifier,
		Cfg:        cfg,
		Store:      st,
		Writer:     writer,
		Engine:     eng,
		Hub:        h,
		Auth:       a,
		Alerts:     dispatcher,
		Regions:    confirmer,
		SchedStats: sched.Stats,
		Version:    version,
		Started:    nowFn(),
		Log:        log,
		Now:        nowFn,
		Checker:    checker,
		// Shutdown lets a staged restore trigger the same graceful
		// shutdown path as SIGTERM; docker-compose's `restart:
		// unless-stopped` brings the process back up, at which point
		// BootSwapRestore (above) swaps the staged database into place.
		Shutdown: cancelRun,
	})

	// 13. Background jobs
	go runBackgroundJobs(runCtx, st, cfg, log, nowFn)

	// 14. HTTP Server
	srv := &http.Server{
		Handler:           apiHandler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ln := cfg.Listener
	if ln == nil {
		var err error
		ln, err = net.Listen("tcp", cfg.ListenAddr)
		if err != nil {
			return fmt.Errorf("listen %s: %w", cfg.ListenAddr, err)
		}
	}

	serverErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	log.Info("opskest server listening", "addr", ln.Addr().String(), "version", version)

	// Wait for shutdown or error. runCtx.Done() fires both on a real
	// SIGTERM (ctx cancels, which cascades into runCtx) and on an
	// admin-triggered restart (a handler calls cancelRun directly via
	// Deps.Shutdown).
	select {
	case <-runCtx.Done():
		log.Info("shutting down opskest...")
	case err := <-serverErr:
		return fmt.Errorf("server error: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("graceful http shutdown error", "err", err)
	}

	return nil
}

func runBackgroundJobs(ctx context.Context, st *store.Store, cfg *config.Config, log *slog.Logger, nowFn func() time.Time) {
	pruneTicker := time.NewTicker(10 * time.Minute)
	defer pruneTicker.Stop()

	hourlyTicker := time.NewTicker(1 * time.Hour)
	defer hourlyTicker.Stop()

	var lastBackupDay int = -1

	for {
		select {
		case <-ctx.Done():
			return
		case <-pruneTicker.C:
			settings, err := st.GetSettings(ctx)
			if err == nil {
				if err := st.Prune(ctx, nowFn(), settings.RetentionRawDays); err != nil {
					log.Warn("prune heartbeats failed", "err", err)
				}
			}
		case <-hourlyTicker.C:
			now := nowFn()
			// Prune sessions
			if _, err := st.PruneSessions(ctx, now); err != nil {
				log.Warn("prune sessions failed", "err", err)
			}
			// Prune status events older than 365 days
			cutoff := now.AddDate(-1, 0, 0).UnixMilli()
			if err := st.PrunePendingSubscribers(ctx, now); err != nil {
				log.Warn("prune pending subscribers failed", "err", err)
			}
			if err := st.PruneStatusEvents(ctx, cutoff); err != nil {
				log.Warn("prune status events failed", "err", err)
			}

			// Daily backup check in DisplayTZ
			settings, err := st.GetSettings(ctx)
			if err == nil && cfg.BackupDir != "" {
				nowInTZ := now.In(cfg.DisplayTZ)
				if nowInTZ.Hour() == settings.BackupHour && nowInTZ.YearDay() != lastBackupDay {
					_, backupErr := st.Backup(ctx, cfg.BackupDir, now, settings.BackupKeep)
					runErrMsg := ""
					if backupErr != nil {
						runErrMsg = backupErr.Error()
						log.Warn("scheduled backup failed", "err", runErrMsg)
					} else {
						lastBackupDay = nowInTZ.YearDay()
						log.Info("scheduled backup completed", "dir", cfg.BackupDir)
					}
					if err := st.RecordBackupRun(ctx, now, backupErr == nil, runErrMsg); err != nil {
						log.Warn("record backup run failed", "err", err)
					}
				}
			}
		}
	}
}

// mailer and poster adapt the alert package's SMTP and webhook helpers to
// notify's interfaces.
type mailer struct{}

func (mailer) SendMail(ctx context.Context, cfg store.ChannelConfig, rcpts []string, msg []byte) error {
	return alert.SendMail(ctx, cfg, rcpts, msg)
}

type poster struct{}

func (poster) PostJSON(ctx context.Context, url string, payload any) error {
	return alert.PostJSON(ctx, url, payload)
}
