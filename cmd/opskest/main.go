// Command opskest is the opskest server binary.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	// Distroless has no timezone database; embed it so DISPLAY_TZ (e.g.
	// Europe/Berlin) always resolves.
	_ "time/tzdata"

	"opskest/internal/app"
	"opskest/internal/config"
	"opskest/internal/logx"
	"opskest/internal/update"
)

// updatePublicKey is an optional base64 ed25519 release public key built
// into official binaries: -ldflags "-X main.updatePublicKey=…". A key saved
// in Settings overrides it.
var updatePublicKey = ""

// updateFeedURL is the release manifest address built into official binaries:
// -ldflags "-X main.updateFeedURL=…". A feed saved in Settings wins.
var updateFeedURL = ""

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	os.Exit(mainRun(os.Args[1:], os.Getenv))
}

// mainRun dispatches to the requested subcommand and returns a process exit
// code. It is separated from main so tests can invoke it without exiting
// the test binary.
func mainRun(args []string, getenv func(string) string) int {
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}

	switch cmd {
	case "serve":
		return runServe(withEnvFile(getenv))
	case "healthcheck":
		return runHealthcheck(withEnvFile(getenv))
	case "version":
		fmt.Println(version)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (want: serve, healthcheck, version)\n", cmd)
		return 2
	}
}

// runServe loads configuration, builds the server, and runs it until an
// interrupt or terminate signal triggers graceful shutdown.
func runServe(getenv func(string) string) int {
	cfg, err := config.Load(getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	update.SetBuiltinPublicKey(updatePublicKey)
	update.SetBuiltinFeedURL(updateFeedURL)
	update.SetRestartMode(getenv("OPSKEST_UPDATE_RESTART"))

	if err := app.Run(ctx, cfg, version, logger); err != nil {
		logx.Err(logger, "server exited with error", err)
		return 1
	}
	// A self-update asked for a restart: app.Run has returned, so the
	// database is closed and the port is free. On Unix this execs the new
	// binary in place (same PID); on Windows it starts it detached.
	if update.RestartRequested() && update.RestartMode() == update.RestartExit {
		logger.Info("update installed; exiting so the supervisor starts the new version (OPSKEST_UPDATE_RESTART=exit)")
	}
	if started, err := update.RunPendingRestart(os.Args[1:], nil); err != nil {
		logx.Err(logger, "restart after update failed; start the binary by hand (previous version kept as .prev)", err)
		if !started {
			return 1
		}
	} else if started {
		logger.Info("started the updated binary")
	}
	return 0
}

// healthPort is the port the server listens on: from LISTEN_ADDR or PORT,
// else 8080.
func healthPort(getenv func(string) string) string {
	if addr := strings.TrimSpace(getenv("LISTEN_ADDR")); addr != "" {
		if _, port, err := net.SplitHostPort(addr); err == nil && port != "" {
			return port
		}
	}
	if port := strings.TrimSpace(getenv("PORT")); port != "" {
		return port
	}
	return "8080"
}

// runHealthcheck implements the "healthcheck" subcommand: it GETs
// /healthz on the local server and exits 0 on 200, 1 otherwise. It exists
// because the distroless runtime image has no wget/curl for a Docker
// HEALTHCHECK to shell out to.
func runHealthcheck(getenv func(string) string) int {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + healthPort(getenv) + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return 0
	}
	return 1
}
