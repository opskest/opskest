package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// ── fake binaries ─────────────────────────────────────────────────────
//
// The install tests need a real, runnable executable whose `version`
// subcommand prints a version. It is a tiny Go program compiled once per
// version at test time.

const fakeSrc = `package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version)
		return
	}
	os.Exit(3)
}
`

var (
	fakeDir   string
	fakeMu    sync.Mutex
	fakeCache = map[string]string{}
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "opskest-update-fakes-")
	if err != nil {
		panic(err)
	}
	fakeDir = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// fakeBinary returns the path of a compiled program printing version, or
// skips the test when there is no Go toolchain.
func fakeBinary(t *testing.T, version string) string {
	t.Helper()
	fakeMu.Lock()
	defer fakeMu.Unlock()
	if p, ok := fakeCache[version]; ok {
		return p
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain to build a fake binary")
	}
	src := filepath.Join(fakeDir, "src-"+version)
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(src, "go.mod"), []byte("module fake\n\ngo 1.21\n"), 0o644))
	must(os.WriteFile(filepath.Join(src, "main.go"), []byte(fakeSrc), 0o644))
	out := filepath.Join(fakeDir, "fake-"+version)
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command(goBin, "build", "-o", out, "-ldflags", "-X main.version="+version, ".")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off", "GOFLAGS=")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building fake binary: %v\n%s", err, b)
	}
	fakeCache[version] = out
	return out
}

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ── release server ────────────────────────────────────────────────────

// rig is a publisher (manifest + signature + asset over httptest), an
// install directory with a "running" executable, and a Manager wired to
// both with injectable backup and restart.
type rig struct {
	t       *testing.T
	srv     *httptest.Server
	pub     ed25519.PublicKey
	priv    ed25519.PrivateKey
	pubB64  string
	dir     string
	exe     string
	oldData []byte

	mu          sync.Mutex
	manifest    []byte
	sig         string
	sigStatus   int // 0 = serve sig, else status code
	asset       []byte
	assetSize   int64  // declared size
	assetSHA    string // declared sha
	assetChunky bool   // serve without Content-Length
	hits        map[string]int
	events      []string // ordered: "backup", "asset", ...
	userAgents  []string

	backupErr error
	backupGo  chan struct{} // if non-nil, backup blocks until closed
	restarts  []string

	cfg Config
	m   *Manager
}

type rigOpts struct {
	currentVersion string // version of the running exe and Manager
	newVersion     string // version in the manifest
	assetVersion   string // version the downloaded binary reports ("" = newVersion)
	noKey          bool
	method         string
}

func newRig(t *testing.T, o rigOpts) *rig {
	t.Helper()
	if o.currentVersion == "" {
		o.currentVersion = "1.3.9"
	}
	if o.newVersion == "" {
		o.newVersion = "1.4.0"
	}
	if o.assetVersion == "" {
		o.assetVersion = o.newVersion
	}
	r := &rig{t: t, hits: map[string]int{}}
	r.pub, r.priv, _ = ed25519.GenerateKey(nil)
	r.pubB64 = base64.StdEncoding.EncodeToString(r.pub)

	r.dir = t.TempDir()
	r.exe = filepath.Join(r.dir, "opskest")
	if runtime.GOOS == "windows" {
		r.exe += ".exe"
	}
	r.oldData = readFile(t, fakeBinary(t, o.currentVersion))
	if err := os.WriteFile(r.exe, r.oldData, 0o755); err != nil {
		t.Fatal(err)
	}
	r.asset = readFile(t, fakeBinary(t, o.assetVersion))
	r.assetSize = int64(len(r.asset))
	sum := sha256.Sum256(r.asset)
	r.assetSHA = hex.EncodeToString(sum[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.hits["manifest"]++
		r.userAgents = append(r.userAgents, req.UserAgent())
		_, _ = w.Write(r.manifest)
	})
	mux.HandleFunc("/manifest.json.sig", func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.hits["sig"]++
		if r.sigStatus != 0 {
			http.Error(w, "nope", r.sigStatus)
			return
		}
		_, _ = w.Write([]byte(r.sig + "\n"))
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.hits["asset"]++
		r.events = append(r.events, "asset")
		data, chunky := r.asset, r.assetChunky
		r.mu.Unlock()
		if chunky {
			fl := w.(http.Flusher)
			for i := 0; i < len(data); i += 4096 {
				end := i + 4096
				if end > len(data) {
					end = len(data)
				}
				_, _ = w.Write(data[i:end])
				fl.Flush()
			}
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		_, _ = w.Write(data)
	})
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)

	r.publish(o.newVersion, "")
	r.cfg = Config{Enabled: true, FeedURL: r.srv.URL + "/manifest.json", Mode: ModeNotify, AutoHour: 3}
	if !o.noKey {
		r.cfg.PublicKey = r.pubB64
	}

	r.m = New(Options{
		Version: o.currentVersion,
		Config: func() Config {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.cfg
		},
		Backup: func(context.Context) (string, error) {
			r.mu.Lock()
			r.events = append(r.events, "backup")
			err, gate := r.backupErr, r.backupGo
			r.mu.Unlock()
			if gate != nil {
				<-gate
			}
			return "opskest-20261102-1000.db", err
		},
		Restart: func(exe string) {
			r.mu.Lock()
			r.restarts = append(r.restarts, exe)
			r.mu.Unlock()
		},
		ExePath:       r.exe,
		Method:        o.method,
		Env:           Env{Getenv: func(string) string { return "" }, Exists: func(string) bool { return false }, ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist }},
		RestartDelay:  -1,
		CheckWritable: func(string) bool { return true },
		Loc:           time.UTC,
	})
	if o.method == "" {
		r.m.method = MethodBinary
	}
	return r
}

// publish (re)builds the signed manifest for version, pointing at /asset.
func (r *rig) publish(version, minVersion string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.publishLocked(version, minVersion)
}

func (r *rig) publishLocked(version, minVersion string) {
	man := map[string]any{
		"version":     version,
		"publishedAt": "2026-11-02T10:00:00Z",
		"notes":       "release notes for " + version,
		"assets": []map[string]any{{
			"os": runtime.GOOS, "arch": runtime.GOARCH,
			"url": r.srv.URL + "/asset", "sha256": r.assetSHA, "size": r.assetSize,
		}},
	}
	if minVersion != "" {
		man["minVersion"] = minVersion
	}
	b, _ := json.Marshal(man)
	r.manifest = b
	r.sig = sign(r.priv, b)
}

// setAsset changes what the manifest declares about the asset.
func (r *rig) setAssetDecl(sha string, size int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if sha != "" {
		r.assetSHA = sha
	}
	if size != 0 {
		r.assetSize = size
	}
	var cur struct{ Version, MinVersion string }
	_ = json.Unmarshal(r.manifest, &cur)
	r.publishLocked(cur.Version, cur.MinVersion)
}

func (r *rig) hit(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hits[name]
}

func (r *rig) eventList() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func (r *rig) restartList() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.restarts...)
}

// dirEntries lists file names in the install dir, to prove no temp files
// are left behind.
func (r *rig) dirEntries() []string {
	r.t.Helper()
	es, err := os.ReadDir(r.dir)
	if err != nil {
		r.t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

func shaHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
