// Package config loads opskest's process configuration from environment
// variables. All required values are validated up front so the process
// fails fast with a single, clear error rather than panicking deep inside
// some other package later.
package config

import (
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Config holds the fully validated process configuration.
type Config struct {
	AppURL     string
	ListenAddr string
	// Listener, when non-nil, is used by the server instead of dialing
	// ListenAddr. It exists only so tests can bind an ephemeral port; it is
	// never populated from the environment.
	Listener net.Listener

	DataDir   string
	BackupDir string

	// AppURLDefaulted is true when APP_URL was not set and AppURL was
	// derived from the listen port (fine for trying it out locally; set
	// APP_URL for anything reachable by others).
	AppURLDefaulted bool

	SecretKey []byte

	MetricsToken     string
	CheckConcurrency int
	DisplayTZ        *time.Location
	ServerRegion     string
	LogLevel         slog.Level
	SecureCookies    bool
}

const (
	defaultListenAddr  = ":8080"
	defaultDataDir     = "data"
	defaultBackupDir   = "backups"
	defaultConcurrency = 256
	defaultLogLevel    = "info"

	minConcurrency = 1
	maxConcurrency = 2048
)

// Load reads and validates configuration from environment variables,
// resolved through getenv (so callers and tests never touch the real
// process environment directly).
func Load(getenv func(string) string) (*Config, error) {
	env := func(key string) string { return strings.TrimSpace(getenv(key)) }
	appURLRaw := env("APP_URL")
	secretKeyRaw := env("SECRET_ENCRYPTION_KEY")

	listenAddr, err := listenAddress(env("LISTEN_ADDR"), env("PORT"))
	if err != nil {
		return nil, err
	}

	appURLDefaulted := appURLRaw == ""
	if appURLDefaulted {
		appURLRaw = "http://localhost:" + portOf(listenAddr)
	}
	appURL, secure, err := parseAppURL(appURLRaw)
	if err != nil {
		return nil, fmt.Errorf("APP_URL: %w", err)
	}

	// An empty key is generated and kept in the data directory on first
	// start; see ResolveSecretKey.
	var secretKey []byte
	if secretKeyRaw != "" {
		secretKey, err = parseSecretKey(secretKeyRaw)
		if err != nil {
			return nil, fmt.Errorf("SECRET_ENCRYPTION_KEY: %w", err)
		}
	}

	concurrency, err := parseConcurrency(env("CHECK_CONCURRENCY"))
	if err != nil {
		return nil, fmt.Errorf("CHECK_CONCURRENCY: %w", err)
	}

	tz, err := displayLocation(env("DISPLAY_TZ"), env("TZ"), systemTimeZone)
	if err != nil {
		return nil, fmt.Errorf("DISPLAY_TZ: %w", err)
	}

	logLevel, err := parseLogLevel(env("LOG_LEVEL"))
	if err != nil {
		return nil, fmt.Errorf("LOG_LEVEL: %w", err)
	}

	dataDir := env("DATA_DIR")
	if dataDir == "" {
		dataDir = defaultDataDir
	}
	backupDir := env("BACKUP_DIR")
	if backupDir == "" {
		backupDir = defaultBackupDir
	}

	return &Config{
		AppURL:           appURL,
		AppURLDefaulted:  appURLDefaulted,
		ListenAddr:       listenAddr,
		DataDir:          dataDir,
		BackupDir:        backupDir,
		SecretKey:        secretKey,
		MetricsToken:     env("METRICS_TOKEN"),
		CheckConcurrency: concurrency,
		DisplayTZ:        tz,
		ServerRegion:     env("SERVER_REGION"),
		LogLevel:         logLevel,
		SecureCookies:    secure,
	}, nil
}

// listenAddress picks the address to serve on: LISTEN_ADDR, else the PORT
// most hosts (cPanel, PaaS, process managers) hand an app, else :8080.
func listenAddress(listen, port string) (string, error) {
	switch {
	case listen != "":
		return listen, nil
	case port != "":
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("PORT: must be 1-65535, got %q", port)
		}
		return ":" + port, nil
	}
	return defaultListenAddr, nil
}

// portOf returns the port of a listen address such as ":8080" or
// "127.0.0.1:9000", or "8080" when it cannot tell.
func portOf(addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil && port != "" {
		return port
	}
	return "8080"
}

// parseAppURL validates that raw is an absolute http(s) URL, strips a single
// trailing slash, and reports whether cookies should be marked Secure
// (true for https).
func parseAppURL(raw string) (string, bool, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, fmt.Errorf("invalid URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false, fmt.Errorf("must be an absolute http(s) URL, got %q", raw)
	}
	s := strings.TrimSuffix(raw, "/")
	return s, u.Scheme == "https", nil
}

// parseSecretKey hex-decodes raw and requires exactly 32 bytes.
func parseSecretKey(raw string) ([]byte, error) {
	b, err := hex.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("must be hex-encoded: %w", err)
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("must decode to 32 bytes, got %d", len(b))
	}
	return b, nil
}

// parseConcurrency parses raw as an int in [minConcurrency, maxConcurrency],
// defaulting to defaultConcurrency when raw is empty.
func parseConcurrency(raw string) (int, error) {
	if raw == "" {
		return defaultConcurrency, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("must be an integer: %w", err)
	}
	if n < minConcurrency || n > maxConcurrency {
		return 0, fmt.Errorf("must be between %d and %d, got %d", minConcurrency, maxConcurrency, n)
	}
	return n, nil
}

// parseLogLevel parses raw as one of debug/info/warn/error, defaulting to
// info when raw is empty.
func parseLogLevel(raw string) (slog.Level, error) {
	if raw == "" {
		raw = defaultLogLevel
	}
	switch strings.ToLower(raw) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("must be one of debug/info/warn/error, got %q", raw)
	}
}
