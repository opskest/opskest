package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"opskest/internal/logx"
)

// Modes for Config.Mode.
const (
	ModeOff    = "off"
	ModeNotify = "notify"
	ModeAuto   = "auto"
)

// Phase is the state machine position of the single update operation.
type Phase string

const (
	PhaseIdle       Phase = "idle"
	PhaseChecking   Phase = "checking"
	PhaseBackingUp  Phase = "backing_up"
	PhaseDownload   Phase = "downloading"
	PhaseVerifying  Phase = "verifying"
	PhaseInstalling Phase = "installing"
	PhaseRestarting Phase = "restarting"
	PhaseFailed     Phase = "failed"
)

// State is what the UI polls.
type State struct {
	Phase   Phase  `json:"phase"`
	Percent int    `json:"percent"`
	Error   string `json:"error"`
	// BackupName is the file (in the backups folder) taken before the
	// current install, set once the backup has been made.
	BackupName string `json:"backupName"`
}

// Errors callers can match with errors.Is.
var (
	ErrBusy     = errors.New("another update operation is already running")
	ErrNoFeed   = errors.New("no release feed is configured: add one in Settings")
	ErrNoKey    = errors.New("add the release public key in Settings to allow installing from here")
	ErrNoUpdate = errors.New("no update is available")
	// ErrDisabled: update checks are turned off in Settings.
	ErrDisabled = errors.New("update checks are turned off in Settings")
)

// CannotInstallError explains why installation is refused.
type CannotInstallError struct{ Reason string }

func (e *CannotInstallError) Error() string { return e.Reason }

// Config is the live configuration, read on every use so Settings changes
// apply immediately. PublicKey is the effective key (settings value, else
// the one built into the binary).
type Config struct {
	Enabled   bool
	FeedURL   string
	Mode      string
	AutoHour  int
	PublicKey string
}

// AssetInfo is the part of the matching asset that is safe to show.
type AssetInfo struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	Size int64  `json:"size"`
}

// CheckResult is the outcome of the last check.
type CheckResult struct {
	CheckedAt         time.Time  `json:"checkedAt"`
	Error             string     `json:"error"`
	Latest            string     `json:"latest"`
	Available         bool       `json:"available"`
	Notes             string     `json:"notes"`
	PublishedAt       time.Time  `json:"publishedAt"`
	SignatureVerified bool       `json:"signatureVerified"`
	Asset             *AssetInfo `json:"asset"`
	MinVersion        string     `json:"minVersion"`
	// MinVersionBlocked: an update exists but this build is older than the
	// release's minVersion, so it cannot jump there directly.
	MinVersionBlocked bool `json:"minVersionBlocked"`
}

// Saved is the part of the manager's memory that survives a restart.
type Saved struct {
	CheckJSON       string // JSON of CheckResult, "" if none
	InstallFailedAt int64  // unix ms of the last failed install attempt, 0 none
	SkipVersion     string // version auto mode must not install (rolled back)
	PrevVersion     string // version of the kept .prev binary, "" unknown
}

// Options wires the manager to the app. Only Version and Config are
// required; everything else has a sensible default.
type Options struct {
	Version string
	Config  func() Config

	// Backup must make a database backup and return its file name; a
	// failure aborts the update.
	Backup func(ctx context.Context) (name string, err error)
	// Restart is called once the new binary is in place. It must cause the
	// process to exit gracefully and (after it has exited) start exe.
	Restart func(exe string)
	// Audit records background (auto) actions that have no admin user.
	Audit func(action, detail string)

	LoadSaved func() Saved
	SaveSaved func(Saved)

	ExePath string // default: the running executable, symlinks resolved
	Env     Env    // default: the real host
	Method  string // force an install method (tests); "" = detect
	HTTP    *http.Client
	Now     func() time.Time
	Loc     *time.Location // time zone for autoHour; default Local
	Log     *slog.Logger

	// CheckWritable overrides the "can I replace the executable" probe.
	CheckWritable func(dir string) bool

	GOOS, GOARCH    string        // default runtime; selects the asset
	StartDelay      time.Duration // background first check (default 2m)
	Tick            time.Duration // background wake-up (default 5m)
	RestartDelay    time.Duration // pause before Restart so the UI can poll (default 1s)
	DownloadTimeout time.Duration // default 30m
}

// Manager owns the update state machine. All methods are safe for
// concurrent use; at most one operation runs at a time.
type Manager struct {
	o      Options
	client *http.Client
	ua     string
	exe    string
	method string

	mu          sync.Mutex
	state       State
	last        *CheckResult
	failedAt    time.Time
	skipVersion string
	prevVersion string // version of the kept .prev binary
	lastManual  time.Time
	nextCheck   time.Time
	autoDay     string
	base        context.Context
	writableAt  time.Time
	writableOK  bool
}

// New builds a Manager.
func New(o Options) *Manager {
	if o.Config == nil {
		o.Config = func() Config { return Config{} }
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Loc == nil {
		o.Loc = time.Local
	}
	if o.Log == nil {
		o.Log = slog.New(slog.NewTextHandler(discard{}, nil))
	}
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.GOARCH == "" {
		o.GOARCH = runtime.GOARCH
	}
	if o.StartDelay == 0 {
		o.StartDelay = 2 * time.Minute
	}
	if o.Tick == 0 {
		o.Tick = 5 * time.Minute
	}
	if o.RestartDelay == 0 {
		o.RestartDelay = time.Second
	}
	if o.DownloadTimeout == 0 {
		o.DownloadTimeout = 30 * time.Minute
	}
	if o.Env.Getenv == nil && o.Env.Exists == nil && o.Env.ReadFile == nil {
		o.Env = HostEnv()
	}
	exe := o.ExePath
	if exe == "" {
		// Resolved once, at start: after an update /proc/self/exe would
		// name the renamed .prev file.
		if p, err := os.Executable(); err == nil {
			if r, err := filepath.EvalSymlinks(p); err == nil {
				p = r
			}
			exe = p
		}
	}
	m := &Manager{o: o, exe: exe, client: o.HTTP, base: context.Background()}
	if m.client == nil {
		m.client = newHTTPClient()
	}
	m.ua = "opskest/" + o.Version
	m.method = o.Method
	if m.method == "" {
		m.method = DetectMethod(o.Env, exe)
	}
	m.state = State{Phase: PhaseIdle}
	m.restore()
	return m
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// restore loads persisted state. "available" is recomputed against the
// running version, so a check that found 1.4.0 does not keep claiming an
// update after the process has become 1.4.0.
func (m *Manager) restore() {
	if m.o.LoadSaved == nil {
		return
	}
	s := m.o.LoadSaved()
	if s.InstallFailedAt > 0 {
		m.failedAt = time.UnixMilli(s.InstallFailedAt)
	}
	m.skipVersion = s.SkipVersion
	m.prevVersion = s.PrevVersion
	if s.CheckJSON == "" {
		return
	}
	var r CheckResult
	if json.Unmarshal([]byte(s.CheckJSON), &r) != nil {
		return
	}
	m.recompute(&r)
	m.last = &r
}

func (m *Manager) recompute(r *CheckResult) {
	r.Available, r.MinVersionBlocked = false, false
	// A result that carries both Latest and Error is an earlier good result
	// kept across a failed (network) check; it still counts as offered.
	if r.Latest == "" {
		return
	}
	latest, err := ParseVersion(r.Latest)
	if err != nil {
		return
	}
	cur, err := ParseVersion(m.o.Version)
	if err != nil {
		return
	}
	r.Available = offered(cur, latest)
	if r.Available && r.MinVersion != "" {
		if min, err := ParseVersion(r.MinVersion); err == nil && cur.Compare(min) < 0 {
			r.MinVersionBlocked = true
		}
	}
}

// offered: latest is newer than current. A pre-release is only offered to
// a build that is itself a pre-release, so a stable install never moves
// onto a release candidate by itself.
func offered(cur, latest Version) bool {
	if latest.Compare(cur) <= 0 {
		return false
	}
	if latest.IsPrerelease() && !cur.IsPrerelease() {
		return false
	}
	return true
}

func (m *Manager) save() {
	if m.o.SaveSaved == nil {
		return
	}
	s := Saved{SkipVersion: m.skipVersion, PrevVersion: m.prevVersion}
	if !m.failedAt.IsZero() {
		s.InstallFailedAt = m.failedAt.UnixMilli()
	}
	if m.last != nil {
		if b, err := json.Marshal(m.last); err == nil {
			s.CheckJSON = string(b)
		}
	}
	m.o.SaveSaved(s)
}

// Current returns the running version string.
func (m *Manager) Current() string { return m.o.Version }

// Method returns "binary", "docker" or "other".
func (m *Manager) Method() string { return m.method }

// ExePath is the executable this manager would replace.
func (m *Manager) ExePath() string { return m.exe }

// ── state machine ──────────────────────────────────────────────────────

func (m *Manager) begin(p Phase) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Phase != PhaseIdle && m.state.Phase != PhaseFailed {
		return State{}, ErrBusy
	}
	prev := m.state
	m.state = State{Phase: p}
	return prev, nil
}

func (m *Manager) setPhase(p Phase, pct int, cb func(State)) {
	m.mu.Lock()
	m.state = State{Phase: p, Percent: pct, BackupName: m.state.BackupName}
	s := m.state
	m.mu.Unlock()
	if cb != nil {
		cb(s)
	}
}

// fail moves to the failed state, keeping the backup name for the UI.
func (m *Manager) fail(err error, cb func(State)) {
	m.mu.Lock()
	m.state = State{Phase: PhaseFailed, Error: errText(err), BackupName: m.state.BackupName}
	s := m.state
	m.mu.Unlock()
	if cb != nil {
		cb(s)
	}
}

// State returns the current state.
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// ── check ──────────────────────────────────────────────────────────────

// LastCheck returns a copy of the last check result (nil if none).
func (m *Manager) LastCheck() *CheckResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.last == nil {
		return nil
	}
	c := *m.last
	if c.Asset != nil {
		a := *c.Asset
		c.Asset = &a
	}
	return &c
}

// AllowManualCheck implements the "once per 10 s" limit for the API.
func (m *Manager) AllowManualCheck() (ok bool, retryAfter time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.o.Now()
	if !m.lastManual.IsZero() && now.Sub(m.lastManual) < 10*time.Second {
		return false, 10*time.Second - now.Sub(m.lastManual)
	}
	m.lastManual = now
	return true, 0
}

// Check fetches and verifies the manifest now. It fails with ErrBusy while
// another operation runs. The result (or its error text) is also stored
// as the last check.
func (m *Manager) Check(ctx context.Context) (*CheckResult, error) {
	if cfg := m.o.Config(); !cfg.Enabled {
		return nil, ErrDisabled
	}
	// Starting a check clears an earlier failed state.
	if _, err := m.begin(PhaseChecking); err != nil {
		return nil, err
	}
	res, _, cerr := m.doCheck(ctx)
	m.mu.Lock()
	m.state = State{Phase: PhaseIdle}
	m.mu.Unlock()
	return res, cerr
}

// doCheck does the work of a check without touching the phase. It always
// records a result; on error the result carries the error text and no
// offered update.
func (m *Manager) doCheck(ctx context.Context) (*CheckResult, *Manifest, error) {
	res, man, err := m.fetchManifest(ctx)
	if err != nil {
		res = &CheckResult{CheckedAt: m.o.Now(), Error: errText(err)}
		// A feed that is merely unreachable must not make an already known
		// update disappear from the banner: keep the last good result and
		// attach the error. Rejections (bad signature, invalid manifest) are
		// not transient and clear it.
		var te *transientError
		if errors.As(err, &te) {
			if prev := m.LastCheck(); prev != nil && prev.Latest != "" {
				prev.CheckedAt, prev.Error = res.CheckedAt, res.Error
				res = prev
			}
		}
	}
	m.mu.Lock()
	m.last = res
	m.save()
	m.mu.Unlock()
	if err != nil {
		return res, nil, err
	}
	return res, man, nil
}

func errText(err error) string { return truncate(err.Error(), 300) }

// transientError marks a failure to reach the feed (as opposed to a feed
// that answered with something unacceptable).
type transientError struct{ err error }

func (e *transientError) Error() string { return e.err.Error() }
func (e *transientError) Unwrap() error { return e.err }

func (m *Manager) fetchManifest(ctx context.Context) (*CheckResult, *Manifest, error) {
	cfg := m.o.Config()
	if cfg.FeedURL == "" {
		return nil, nil, ErrNoFeed
	}
	if err := ValidateFeedURL(cfg.FeedURL); err != nil {
		return nil, nil, fmt.Errorf("feed URL: %w", err)
	}
	data, err := fetchLimited(ctx, m.client, cfg.FeedURL, m.ua, MaxManifestBytes, feedTimeout)
	if err != nil {
		return nil, nil, &transientError{fmt.Errorf("could not fetch the release manifest: %w", err)}
	}

	verified := false
	if cfg.PublicKey != "" {
		pub, err := ParsePublicKey(cfg.PublicKey)
		if err != nil {
			return nil, nil, fmt.Errorf("the configured public key is invalid: %w", err)
		}
		sigURL, err := withSigSuffix(cfg.FeedURL)
		if err != nil {
			return nil, nil, errors.New("feed URL is not valid")
		}
		sig, err := fetchLimited(ctx, m.client, sigURL, m.ua, MaxSigBytes, feedTimeout)
		if err != nil {
			return nil, nil, &transientError{fmt.Errorf("could not fetch the manifest signature: %w", err)}
		}
		if err := VerifySignature(pub, data, string(sig)); err != nil {
			return nil, nil, fmt.Errorf("release manifest rejected: %w", err)
		}
		verified = true
	}

	man, err := ParseManifest(data)
	if err != nil {
		return nil, nil, err
	}
	res := &CheckResult{
		CheckedAt:         m.o.Now(),
		Latest:            man.ver.String(),
		Notes:             man.Notes,
		PublishedAt:       man.published,
		SignatureVerified: verified,
		MinVersion:        man.MinVersion,
	}
	if min, err := ParseVersion(man.MinVersion); err == nil {
		res.MinVersion = min.String()
	}
	if a := man.SelectAsset(m.o.GOOS, m.o.GOARCH); a != nil {
		res.Asset = &AssetInfo{OS: a.OS, Arch: a.Arch, Size: a.Size}
	}
	m.recompute(res)
	return res, man, nil
}

// ── capability ─────────────────────────────────────────────────────────

func (m *Manager) writable() bool {
	m.mu.Lock()
	if time.Since(m.writableAt) < 30*time.Second && !m.writableAt.IsZero() {
		ok := m.writableOK
		m.mu.Unlock()
		return ok
	}
	m.mu.Unlock()
	dir := filepath.Dir(m.exe)
	var ok bool
	if m.o.CheckWritable != nil {
		ok = m.o.CheckWritable(dir)
	} else {
		ok = m.exe != "" && canWriteDir(dir)
	}
	m.mu.Lock()
	m.writableAt, m.writableOK = time.Now(), ok
	m.mu.Unlock()
	return ok
}

// CanInstall reports whether this process may replace itself, and if not,
// why. It looks at the build, install method, key and file permissions; it
// does not consider whether an update exists.
func (m *Manager) CanInstall() (bool, string) {
	switch m.method {
	case MethodDocker:
		return false, "running in Docker: update the container with (" + upgradeHint() + ")"
	case MethodOther:
		return false, "installed by a package manager or run from a temporary location: update it the way you installed it"
	}
	if _, err := ParseVersion(m.o.Version); err != nil {
		return false, "this is a development build; it never updates itself"
	}
	if strings.TrimSpace(m.o.Config().PublicKey) == "" {
		return false, ErrNoKey.Error()
	}
	if m.exe == "" || !m.writable() {
		return false, "the application file or its folder is not writable by this process"
	}
	return true, ""
}

// CanRollback reports whether a previous version is kept and the swap is
// possible.
func (m *Manager) CanRollback() bool {
	if m.method != MethodBinary || m.exe == "" {
		return false
	}
	if _, err := os.Stat(prevPath(m.exe)); err != nil {
		return false
	}
	return m.writable()
}

// Status is a consistent snapshot for the API.
type Status struct {
	Current     string
	Method      string
	CanInstall  bool
	Reason      string
	Check       *CheckResult
	State       State
	CanRollback bool
	DockerHint  string
	Config      Config
	SkipVersion string
	PrevVersion string // version kept as .prev ("" = none or unknown)
	InstallFail time.Time
	NextCheck   time.Time
	RestartMode string // "self" or "exit" (see RestartMode)
}

// Status returns the snapshot.
func (m *Manager) Status() Status {
	can, reason := m.CanInstall()
	chk := m.LastCheck()
	if can && chk != nil && chk.Available {
		switch {
		case chk.MinVersionBlocked:
			can, reason = false, "this release needs at least version "+chk.MinVersion+" first; install that version, then update again"
		case chk.Asset == nil:
			can, reason = false, "this release has no download for "+m.o.GOOS+"/"+m.o.GOARCH
		}
	}
	m.mu.Lock()
	skip, failed, next := m.skipVersion, m.failedAt, m.nextCheck
	m.mu.Unlock()
	prevVer := ""
	if _, err := os.Stat(prevPath(m.exe)); err == nil && m.exe != "" {
		m.mu.Lock()
		prevVer = m.prevVersion
		m.mu.Unlock()
	}
	s := Status{
		PrevVersion: prevVer,
		Current:     m.o.Version, Method: m.method, CanInstall: can, Reason: reason,
		Check: chk, State: m.State(), CanRollback: m.CanRollback(),
		Config: m.o.Config(), SkipVersion: skip, InstallFail: failed, NextCheck: next,
		RestartMode: RestartMode(),
	}
	if m.method == MethodDocker {
		s.DockerHint = upgradeHint()
	}
	return s
}

// ── install ────────────────────────────────────────────────────────────

// reserve checks every precondition that does not need the network and
// takes the single operation slot.
func (m *Manager) reserve(p Phase) error {
	if can, reason := m.CanInstall(); !can {
		return &CannotInstallError{Reason: reason}
	}
	_, err := m.begin(p)
	return err
}

// Install runs a full install synchronously: check, backup, download,
// verify, swap, restart. progress (optional) is called on each state
// change. It returns ErrBusy if another operation is running and a
// *CannotInstallError if installation is not allowed here. On success the
// restart function has been called and the state is "restarting".
func (m *Manager) Install(ctx context.Context, progress func(State)) error {
	if err := m.reserve(PhaseChecking); err != nil {
		return err
	}
	return m.runInstall(ctx, progress, false)
}

// StartInstall reserves the operation synchronously (so the caller can
// answer 409 immediately) and runs the install in the background.
func (m *Manager) StartInstall() error {
	if err := m.reserve(PhaseChecking); err != nil {
		return err
	}
	go func() { _ = m.runInstall(m.baseCtx(), nil, false) }()
	return nil
}

func (m *Manager) baseCtx() context.Context {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.base
}

func (m *Manager) runInstall(ctx context.Context, progress func(State), auto bool) error {
	from := m.o.Version
	var to string
	err := m.install(ctx, progress, &to)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNoUpdate) {
		m.fail(err, progress)
		return err
	}
	m.mu.Lock()
	m.failedAt = m.o.Now()
	m.save()
	m.mu.Unlock()
	m.fail(err, progress)
	logx.Err(m.o.Log, "update install failed", err, "from", from, "to", to)
	if m.o.Audit != nil {
		m.o.Audit("update_failed", fmt.Sprintf("%s -> %s: %s", from, to, truncate(err.Error(), 200)))
	}
	return err
}

func (m *Manager) install(ctx context.Context, cb func(State), to *string) error {
	m.setPhase(PhaseChecking, 0, cb)
	res, man, err := m.doCheck(ctx)
	if err != nil {
		return err
	}
	*to = res.Latest
	if !res.Available {
		return ErrNoUpdate
	}
	if !res.SignatureVerified {
		return ErrNoKey
	}
	if res.MinVersionBlocked {
		return fmt.Errorf("this release needs at least version %s first", res.MinVersion)
	}
	asset := man.SelectAsset(m.o.GOOS, m.o.GOARCH)
	if asset == nil {
		return fmt.Errorf("this release has no download for %s/%s", m.o.GOOS, m.o.GOARCH)
	}
	if err := ValidateFeedURL(asset.URL); err != nil {
		return fmt.Errorf("asset URL refused: %w", err)
	}
	if asset.Size <= 0 || asset.Size > MaxAssetBytes {
		return errors.New("asset size is missing or larger than 200 MiB")
	}
	if len(asset.SHA256) != 64 {
		return errors.New("asset has no sha256")
	}

	// 1. Backup. Nothing else happens if it fails.
	m.setPhase(PhaseBackingUp, 0, cb)
	if m.o.Backup == nil {
		return errors.New("no database backup is available; update aborted")
	}
	name, err := m.o.Backup(ctx)
	if err != nil {
		return fmt.Errorf("database backup failed, update aborted: %s", truncate(err.Error(), 200))
	}
	m.mu.Lock()
	m.state.BackupName = name
	cur := m.state
	m.mu.Unlock()
	if cb != nil {
		cb(cur)
	}

	// 2. Download into the executable's directory (same filesystem, so
	// the final rename is atomic).
	m.setPhase(PhaseDownload, 0, cb)
	tmp, err := download(ctx, m.client, m.ua, asset, filepath.Dir(m.exe), m.o.DownloadTimeout, func(p int) {
		m.setPhase(PhaseDownload, p, cb)
	})
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()

	// 3. Verify it is a runnable executable of the announced version.
	m.setPhase(PhaseVerifying, 100, cb)
	makeExecutable(tmp, m.exe)
	if err := verifyBinary(ctx, tmp, res.Latest); err != nil {
		return err
	}

	// 4. Swap; the old binary stays as .prev.
	m.setPhase(PhaseInstalling, 100, cb)
	if err := swapIn(m.exe, tmp); err != nil {
		return err
	}
	cleanup = false // tmp was renamed into place
	m.mu.Lock()
	m.prevVersion = Normalize(m.o.Version)
	m.save()
	m.mu.Unlock()

	// 5. Restart.
	if m.o.Audit != nil {
		m.o.Audit("update_installed", fmt.Sprintf("%s -> %s, backup %s; previous binary kept as .prev", m.o.Version, res.Latest, name))
	}
	m.setPhase(PhaseRestarting, 100, cb)
	m.restart()
	return nil
}

func (m *Manager) restart() {
	if m.o.RestartDelay > 0 {
		time.Sleep(m.o.RestartDelay)
	}
	if m.o.Restart != nil {
		m.o.Restart(m.exe)
	}
}

// ── rollback ───────────────────────────────────────────────────────────

// Rollback swaps the kept .prev binary back in and restarts. Like install
// it refuses in Docker and when the files are not writable. It does not
// need the release key: the file is already on disk.
func (m *Manager) Rollback(ctx context.Context, progress func(State)) error {
	if err := m.reserveRollback(); err != nil {
		return err
	}
	return m.runRollback(ctx, progress)
}

// StartRollback is Rollback in the background after a synchronous check.
func (m *Manager) StartRollback() error {
	if err := m.reserveRollback(); err != nil {
		return err
	}
	go func() { _ = m.runRollback(m.baseCtx(), nil) }()
	return nil
}

func (m *Manager) reserveRollback() error {
	switch m.method {
	case MethodDocker:
		return &CannotInstallError{Reason: "running in Docker: roll back by running the previous image tag"}
	case MethodOther:
		return &CannotInstallError{Reason: "installed by a package manager: roll back with the package manager"}
	}
	if m.exe == "" || !m.writable() {
		return &CannotInstallError{Reason: "the application file or its folder is not writable by this process"}
	}
	if _, err := os.Stat(prevPath(m.exe)); err != nil {
		return &CannotInstallError{Reason: "there is no previous version to roll back to"}
	}
	_, err := m.begin(PhaseChecking)
	return err
}

func (m *Manager) runRollback(ctx context.Context, cb func(State)) error {
	err := func() error {
		m.setPhase(PhaseVerifying, 100, cb)
		prev := prevPath(m.exe)
		if err := verifyBinary(ctx, prev, ""); err != nil {
			return fmt.Errorf("the previous version is not usable: %w", err)
		}
		// A rollback is recovery: take a backup if possible, but do not
		// refuse to recover because the backup failed.
		if m.o.Backup != nil {
			m.setPhase(PhaseBackingUp, 0, cb)
			if name, err := m.o.Backup(ctx); err != nil {
				logx.Err(m.o.Log, "backup before rollback failed", err)
			} else {
				m.mu.Lock()
				m.state.BackupName = name
				m.mu.Unlock()
			}
		}
		m.setPhase(PhaseInstalling, 100, cb)
		if err := swapWithPrev(m.exe); err != nil {
			return err
		}
		m.mu.Lock()
		m.skipVersion = Normalize(m.o.Version) // auto mode must not bounce back
		m.prevVersion = Normalize(m.o.Version) // the version we just left is now .prev
		m.save()
		m.mu.Unlock()
		if m.o.Audit != nil {
			m.o.Audit("update_rolled_back", "from "+m.o.Version+" to the previous binary")
		}
		m.setPhase(PhaseRestarting, 100, cb)
		m.restart()
		return nil
	}()
	if err != nil {
		m.fail(err, cb)
		logx.Err(m.o.Log, "update rollback failed", err)
	}
	return err
}

// ── background ─────────────────────────────────────────────────────────

// ShouldAutoInstall is the pure decision behind auto mode: install only
// inside the configured local hour, only a signature-verified newer
// version that was not rolled back, never on a development build, at most
// once per local day, and not within 24 h of a failed install.
func ShouldAutoInstall(cfg Config, res *CheckResult, current, skip string, failedAt, now time.Time, loc *time.Location, lastAttemptDay string) bool {
	if !cfg.Enabled || cfg.Mode != ModeAuto || cfg.FeedURL == "" || cfg.PublicKey == "" {
		return false
	}
	if res == nil || res.Error != "" || !res.Available || !res.SignatureVerified || res.MinVersionBlocked || res.Asset == nil {
		return false
	}
	cur, err := ParseVersion(current)
	if err != nil {
		return false
	}
	latest, err := ParseVersion(res.Latest)
	if err != nil || !offered(cur, latest) {
		return false
	}
	if skip != "" && Normalize(skip) == latest.String() {
		return false
	}
	local := now.In(loc)
	if local.Hour() != cfg.AutoHour {
		return false
	}
	if !failedAt.IsZero() && now.Sub(failedAt) < 24*time.Hour {
		return false
	}
	if lastAttemptDay == dayKey(local) {
		return false
	}
	return true
}

func dayKey(t time.Time) string { return t.Format("2006-01-02") }

// Run is the background checker. It blocks until ctx ends: the first check
// happens after StartDelay, then about every 24 h (with jitter) while
// updates are enabled and the mode is not "off". In auto mode it installs
// inside the configured hour.
func (m *Manager) Run(ctx context.Context) {
	m.mu.Lock()
	m.base = ctx
	m.nextCheck = m.o.Now().Add(m.o.StartDelay) // shown as "next check" until the first one ran
	m.mu.Unlock()

	select {
	case <-ctx.Done():
		return
	case <-time.After(m.o.StartDelay):
	}
	t := time.NewTicker(m.o.Tick)
	defer t.Stop()
	for {
		m.backgroundOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (m *Manager) backgroundOnce(ctx context.Context) {
	cfg := m.o.Config()
	if !cfg.Enabled || cfg.Mode == ModeOff || cfg.FeedURL == "" {
		return
	}
	now := m.o.Now()
	m.mu.Lock()
	due := m.nextCheck.IsZero() || !now.Before(m.nextCheck)
	m.mu.Unlock()
	if due {
		_, err := m.Check(ctx)
		if err != nil && !errors.Is(err, ErrBusy) {
			m.o.Log.Info("update check failed", "err", truncate(err.Error(), 200))
		}
		if !errors.Is(err, ErrBusy) {
			jitter := time.Duration(rand.Int64N(int64(2*time.Hour))) - time.Hour
			m.mu.Lock()
			m.nextCheck = now.Add(24*time.Hour + jitter)
			m.mu.Unlock()
		}
	}
	m.mu.Lock()
	res, skip, failed, day := m.last, m.skipVersion, m.failedAt, m.autoDay
	m.mu.Unlock()
	if !ShouldAutoInstall(cfg, res, m.o.Version, skip, failed, now, m.o.Loc, day) {
		return
	}
	if can, _ := m.CanInstall(); !can {
		return
	}
	m.mu.Lock()
	m.autoDay = dayKey(now.In(m.o.Loc)) // one attempt per day, success or not
	m.mu.Unlock()
	if err := m.reserve(PhaseChecking); err != nil {
		return
	}
	if m.o.Audit != nil {
		m.o.Audit("update_auto_install", m.o.Version+" -> "+res.Latest)
	}
	_ = m.runInstall(ctx, nil, true)
}

// upgradeHint is the command shown to Docker users. A source build started by
// scripts/compose-build sets OPSKEST_UPGRADE_HINT to rebuild instead of pull.
func upgradeHint() string {
	if h := strings.TrimSpace(os.Getenv("OPSKEST_UPGRADE_HINT")); h != "" {
		return h
	}
	return DockerHint
}
