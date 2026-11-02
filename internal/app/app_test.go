package app

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"opskest/internal/config"
	"opskest/internal/secret"
	"opskest/internal/store"
)

func TestRunServesAndShutsDown(t *testing.T) {
	dir := t.TempDir()
	key := make([]byte, 32)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		AppURL:           "http://" + ln.Addr().String(),
		ListenAddr:       ln.Addr().String(),
		Listener:         ln,
		DataDir:          dir,
		BackupDir:        filepath.Join(dir, "backups"),
		SecretKey:        key,
		CheckConcurrency: 10,
		DisplayTZ:        time.UTC,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- RunWithOptions(ctx, cfg, "test-v1", nil, Options{})
	}()

	// Wait for server to respond to healthz
	client := &http.Client{Timeout: 1 * time.Second}
	var ready bool
	for i := 0; i < 30; i++ {
		resp, err := client.Get(cfg.AppURL + "/healthz")
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			ready = true
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatal("/healthz never became ready")
	}

	// Cancel context to trigger graceful shutdown
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error on shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not exit within 10 seconds")
	}

	// Verify DB file is not locked by reopening
	box, _ := secret.New(key)
	st, err := store.Open(dir, box, time.UTC)
	if err != nil {
		t.Fatalf("failed to reopen DB after shutdown: %v", err)
	}
	st.Close()
}

// TestRunFailsClearlyOnReadOnlyDataDir proves the writability
// check fires before store.Open, with an error that names the exact chown
// command an operator needs to run. Skipped when running as root, since
// root ignores Unix file permissions and the probe write would succeed.
func TestRunFailsClearlyOnReadOnlyDataDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix directory permission bits are not enforced on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits don't apply")
	}

	parent := t.TempDir()
	dataDir := filepath.Join(parent, "data")
	if err := os.Mkdir(dataDir, 0o500); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dataDir, 0o700) }) // let t.TempDir() clean up

	key := make([]byte, 32)
	cfg := &config.Config{
		AppURL:           "http://127.0.0.1:0",
		DataDir:          dataDir,
		BackupDir:        filepath.Join(parent, "backups"),
		SecretKey:        key,
		CheckConcurrency: 10,
		DisplayTZ:        time.UTC,
	}

	err := RunWithOptions(context.Background(), cfg, "test-v1", nil, Options{})
	if err == nil {
		t.Fatal("Run succeeded, want a writability error")
	}
	msg := err.Error()
	if !strings.Contains(msg, dataDir) || !strings.Contains(msg, "chown -R 65532:65532") {
		t.Fatalf("error = %q, want it to name %q and the chown command", msg, dataDir)
	}
}
