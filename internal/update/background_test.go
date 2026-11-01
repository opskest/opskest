package update

import (
	"context"
	"errors"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestInDockerDetection(t *testing.T) {
	none := func(string) bool { return false }
	noRead := func(string) ([]byte, error) { return nil, os.ErrNotExist }
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }

	cases := []struct {
		name string
		e    Env
		want bool
	}{
		{"plain host", Env{Getenv: env(nil), Exists: none, ReadFile: noRead}, false},
		{"env var", Env{Getenv: env(map[string]string{"OPSKEST_IN_DOCKER": "1"}), Exists: none, ReadFile: noRead}, true},
		{"env var 0", Env{Getenv: env(map[string]string{"OPSKEST_IN_DOCKER": "0"}), Exists: none, ReadFile: noRead}, false},
		{".dockerenv", Env{Getenv: env(nil), Exists: func(p string) bool { return p == "/.dockerenv" }, ReadFile: noRead}, true},
		{"podman", Env{Getenv: env(nil), Exists: func(p string) bool { return p == "/run/.containerenv" }, ReadFile: noRead}, true},
		{"cgroup docker", Env{Getenv: env(nil), Exists: none, ReadFile: func(p string) ([]byte, error) {
			if p == "/proc/1/cgroup" {
				return []byte("12:cpu:/docker/abcdef\n"), nil
			}
			return nil, os.ErrNotExist
		}}, true},
		{"cgroup kubepods", Env{Getenv: env(nil), Exists: none, ReadFile: func(p string) ([]byte, error) {
			return []byte("0::/kubepods/burstable/pod1\n"), nil
		}}, true},
		{"cgroup systemd service", Env{Getenv: env(nil), Exists: none, ReadFile: func(p string) ([]byte, error) {
			return []byte("0::/system.slice/opskest.service\n"), nil
		}}, false},
	}
	for _, c := range cases {
		if got := InDocker(c.e); got != c.want {
			t.Errorf("%s: InDocker = %v, want %v", c.name, got, c.want)
		}
	}
	// Docker wins over the path heuristics.
	e := Env{Getenv: env(map[string]string{"OPSKEST_IN_DOCKER": "1"}), Exists: none, ReadFile: noRead}
	if DetectMethod(e, "/opskest") != MethodDocker {
		t.Error("docker not detected")
	}
}

func TestDetectMethodPaths(t *testing.T) {
	host := Env{Getenv: func(string) string { return "" }, Exists: func(string) bool { return false }, ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist }}
	if got := DetectMethod(host, "/home/me/opskest/opskest"); got != MethodBinary {
		t.Errorf("home install = %s", got)
	}
	if got := DetectMethod(host, "/tmp/go-build123/b001/exe/opskest"); got != MethodOther {
		t.Errorf("go run binary = %s", got)
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{"/usr/bin/opskest", "/snap/opskest/1/opskest", "/nix/store/abc-opskest/bin/opskest"} {
			if DetectMethod(host, p) != MethodOther {
				t.Errorf("%s should be package managed", p)
			}
		}
	}
	for _, p := range []string{"/opt/opskest/opskest", "/home/me/bin/opskest", "/usr/local/bin/opskest"} {
		if DetectMethod(host, p) != MethodBinary {
			t.Errorf("%s flagged as package managed", p)
		}
	}
}

func TestShouldAutoInstall(t *testing.T) {
	utc := time.UTC
	at := func(h int) time.Time { return time.Date(2026, 11, 3, h, 10, 0, 0, utc) }
	cfg := Config{Enabled: true, FeedURL: "https://x/m.json", Mode: ModeAuto, AutoHour: 3, PublicKey: "k"}
	res := &CheckResult{Latest: "1.4.0", Available: true, SignatureVerified: true, Asset: &AssetInfo{OS: "linux", Arch: "amd64", Size: 1}}

	base := func() (Config, *CheckResult) { c := cfg; r := *res; return c, &r }
	yes := func(name string, mut func(c *Config, r *CheckResult), cur, skip string, failed time.Time, now time.Time, day string, want bool) {
		t.Helper()
		c, r := base()
		if mut != nil {
			mut(&c, r)
		}
		if got := ShouldAutoInstall(c, r, cur, skip, failed, now, utc, day); got != want {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}
	yes("inside the hour", nil, "1.3.9", "", time.Time{}, at(3), "", true)
	yes("hour before", nil, "1.3.9", "", time.Time{}, at(2), "", false)
	yes("hour after", nil, "1.3.9", "", time.Time{}, at(4), "", false)
	yes("notify mode", func(c *Config, r *CheckResult) { c.Mode = ModeNotify }, "1.3.9", "", time.Time{}, at(3), "", false)
	yes("off mode", func(c *Config, r *CheckResult) { c.Mode = ModeOff }, "1.3.9", "", time.Time{}, at(3), "", false)
	yes("disabled", func(c *Config, r *CheckResult) { c.Enabled = false }, "1.3.9", "", time.Time{}, at(3), "", false)
	yes("no key", func(c *Config, r *CheckResult) { c.PublicKey = "" }, "1.3.9", "", time.Time{}, at(3), "", false)
	yes("not verified", func(c *Config, r *CheckResult) { r.SignatureVerified = false }, "1.3.9", "", time.Time{}, at(3), "", false)
	yes("not available", func(c *Config, r *CheckResult) { r.Available = false }, "1.3.9", "", time.Time{}, at(3), "", false)
	yes("check error", func(c *Config, r *CheckResult) { r.Error = "x" }, "1.3.9", "", time.Time{}, at(3), "", false)
	yes("no asset", func(c *Config, r *CheckResult) { r.Asset = nil }, "1.3.9", "", time.Time{}, at(3), "", false)
	yes("min version", func(c *Config, r *CheckResult) { r.MinVersionBlocked = true }, "1.3.9", "", time.Time{}, at(3), "", false)
	yes("dev build", nil, "dev", "", time.Time{}, at(3), "", false)
	yes("same version", nil, "1.4.0", "", time.Time{}, at(3), "", false)
	yes("lower version", nil, "1.5.0", "", time.Time{}, at(3), "", false)
	yes("rolled back from this version", nil, "1.3.9", "1.4.0", time.Time{}, at(3), "", false)
	yes("rolled back from another version", nil, "1.3.9", "1.2.0", time.Time{}, at(3), "", true)
	yes("failed 2h ago", nil, "1.3.9", "", at(3).Add(-2*time.Hour), at(3), "", false)
	yes("failed 23h ago", nil, "1.3.9", "", at(3).Add(-23*time.Hour), at(3), "", false)
	yes("failed 25h ago", nil, "1.3.9", "", at(3).Add(-25*time.Hour), at(3), "", true)
	yes("already tried today", nil, "1.3.9", "", time.Time{}, at(3), "2026-11-03", false)
	yes("tried yesterday", nil, "1.3.9", "", time.Time{}, at(3), "2026-11-02", true)
	yes("prerelease to stable", func(c *Config, r *CheckResult) { r.Latest = "1.4.0-rc.1" }, "1.3.9", "", time.Time{}, at(3), "", false)

	// The hour is read in the configured location, not UTC.
	ist := time.FixedZone("IST", 19800)
	c, r := base()
	if !ShouldAutoInstall(c, r, "1.3.9", "", time.Time{}, time.Date(2026, 11, 2, 21, 40, 0, 0, utc), ist, "") { // 03:10 IST
		t.Error("03:10 IST (21:40 UTC) should be inside hour 3")
	}
	if ShouldAutoInstall(c, r, "1.3.9", "", time.Time{}, time.Date(2026, 11, 3, 3, 10, 0, 0, utc), ist, "") { // 08:40 IST
		t.Error("03:10 UTC must not count as hour 3 in IST")
	}
}

func TestBackgroundAutoInstallOncePerDay(t *testing.T) {
	r := newRig(t, rigOpts{})
	now := time.Date(2026, 11, 3, 3, 10, 0, 0, time.UTC)
	r.m.o.Now = func() time.Time { return now }
	r.mu.Lock()
	r.cfg.Mode = ModeAuto
	r.backupErr = errors.New("disk full") // first attempt fails
	r.mu.Unlock()
	var audits []string
	r.m.o.Audit = func(action, detail string) { audits = append(audits, action) }

	r.m.backgroundOnce(context.Background())
	if r.m.State().Phase != PhaseFailed {
		t.Fatalf("state = %+v (events %v)", r.m.State(), r.eventList())
	}
	backups := func() int {
		n := 0
		for _, e := range r.eventList() {
			if e == "backup" {
				n++
			}
		}
		return n
	}
	if backups() != 1 {
		t.Fatalf("backups = %d", backups())
	}
	if len(audits) != 2 || audits[0] != "update_auto_install" || audits[1] != "update_failed" {
		t.Fatalf("audits = %v", audits)
	}
	// Same hour again (the 5-minute tick): no retry loop, even though the
	// problem is fixed.
	r.mu.Lock()
	r.backupErr = nil
	r.mu.Unlock()
	now = now.Add(5 * time.Minute)
	r.m.backgroundOnce(context.Background())
	r.m.backgroundOnce(context.Background())
	if backups() != 1 {
		t.Fatalf("retried within the same day/hour: backups = %d", backups())
	}
	// Next day, inside the hour, but the failure is under 24 h old.
	now = time.Date(2026, 11, 4, 3, 0, 0, 0, time.UTC) // 23 h 50 min after the failure
	r.m.backgroundOnce(context.Background())
	if backups() != 1 {
		t.Fatalf("retried within 24 h of a failure: backups = %d", backups())
	}
	// Two days later it may try again, and now succeeds.
	now = time.Date(2026, 11, 5, 3, 30, 0, 0, time.UTC)
	r.m.backgroundOnce(context.Background())
	if len(r.restartList()) != 1 {
		t.Fatalf("expected a successful auto install, state = %+v", r.m.State())
	}
	if !bytesEqual(readFile(t, r.exe), r.asset) {
		t.Fatal("new binary not installed")
	}
}

func bytesEqual(a, b []byte) bool { return string(a) == string(b) }

func TestBackgroundNotifyModeNeverInstalls(t *testing.T) {
	r := newRig(t, rigOpts{})
	now := time.Date(2026, 11, 3, 3, 10, 0, 0, time.UTC)
	r.m.o.Now = func() time.Time { return now }
	r.m.backgroundOnce(context.Background())
	if r.m.LastCheck() == nil || !r.m.LastCheck().Available {
		t.Fatal("notify mode should still check")
	}
	if len(r.eventList()) != 0 || len(r.restartList()) != 0 {
		t.Fatal("notify mode installed or backed up")
	}
	// A second tick before the 24 h are up does not fetch again.
	hits := r.hit("manifest")
	now = now.Add(10 * time.Minute)
	r.m.backgroundOnce(context.Background())
	if r.hit("manifest") != hits {
		t.Fatal("checked again within the 24 h interval")
	}
	// After a day (+ jitter bound) it does.
	now = now.Add(26 * time.Hour)
	r.m.backgroundOnce(context.Background())
	if r.hit("manifest") == hits {
		t.Fatal("no check after 26 h")
	}
}

func TestBackgroundRespectsDisabledOffAndNoFeed(t *testing.T) {
	for name, mut := range map[string]func(c *Config){
		"disabled": func(c *Config) { c.Enabled = false },
		"off":      func(c *Config) { c.Mode = ModeOff },
		"no feed":  func(c *Config) { c.FeedURL = "" },
	} {
		r := newRig(t, rigOpts{})
		r.mu.Lock()
		mut(&r.cfg)
		r.mu.Unlock()
		r.m.backgroundOnce(context.Background())
		if r.hit("manifest") != 0 {
			t.Errorf("%s: fetched the feed", name)
		}
	}
}

func TestRunStartsAfterDelayAndStops(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.m.o.StartDelay = 50 * time.Millisecond
	r.m.o.Tick = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.m.Run(ctx); close(done) }()
	time.Sleep(10 * time.Millisecond)
	if r.hit("manifest") != 0 {
		t.Fatal("checked before the start delay")
	}
	deadline := time.Now().Add(3 * time.Second)
	for r.hit("manifest") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no check after the start delay")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRunPendingRestart(t *testing.T) {
	orig := startProcess
	defer func() { startProcess = orig }()
	clearPending()

	// Nothing requested: nothing started.
	startProcess = func(string, []string, []string) error { t.Fatal("started without a request"); return nil }
	if started, err := RunPendingRestart(nil, nil); started || err != nil {
		t.Fatalf("no request: %v %v", started, err)
	}

	// Requested: started once with the same args.
	var gotExe string
	var gotArgs []string
	startProcess = func(exe string, args, env []string) error { gotExe, gotArgs = exe, args; return nil }
	RequestRestart("/path/to/opskest")
	if !RestartRequested() {
		t.Fatal("RestartRequested false")
	}
	started, err := RunPendingRestart([]string{"serve", "--x"}, []string{"A=b"})
	if !started || err != nil || gotExe != "/path/to/opskest" || len(gotArgs) != 2 || gotArgs[1] != "--x" {
		t.Fatalf("started=%v err=%v exe=%q args=%v", started, err, gotExe, gotArgs)
	}
	if RestartRequested() {
		t.Fatal("request not cleared")
	}
}

func TestRunPendingRestartFallsBackToPrev(t *testing.T) {
	orig := startProcess
	defer func() { startProcess = orig }()

	dir := t.TempDir()
	exe := dir + string(os.PathSeparator) + "opskest"
	if err := os.WriteFile(exe, []byte("NEW-BROKEN"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe+".prev", []byte("OLD-GOOD"), 0o755); err != nil {
		t.Fatal(err)
	}
	calls := 0
	var contents []string
	startProcess = func(p string, _, _ []string) error {
		calls++
		contents = append(contents, string(readFile(t, p)))
		if calls == 1 {
			return errors.New("exec format error")
		}
		return nil
	}
	RequestRestart(exe)
	started, err := RunPendingRestart(nil, nil)
	if err == nil {
		t.Fatal("the original start failure must still be reported")
	}
	if !started || calls != 2 || contents[1] != "OLD-GOOD" {
		t.Fatalf("started=%v calls=%d contents=%v", started, calls, contents)
	}
}

func TestRunPendingRestartFailureWithoutPrev(t *testing.T) {
	orig := startProcess
	defer func() { startProcess = orig }()
	startProcess = func(string, []string, []string) error { return errors.New("boom") }
	RequestRestart(t.TempDir() + string(os.PathSeparator) + "nonexistent")
	started, err := RunPendingRestart(nil, nil)
	if started || err == nil {
		t.Fatalf("started=%v err=%v", started, err)
	}
}
