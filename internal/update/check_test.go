package update

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCheckVerifiedUpdateAvailable(t *testing.T) {
	r := newRig(t, rigOpts{})
	res, err := r.m.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || res.Latest != "1.4.0" || !res.SignatureVerified {
		t.Fatalf("result = %+v", res)
	}
	if res.Notes != "release notes for 1.4.0" || res.PublishedAt.IsZero() {
		t.Fatalf("notes/published missing: %+v", res)
	}
	if res.Asset == nil || res.Asset.Size != r.assetSize {
		t.Fatalf("asset = %+v", res.Asset)
	}
	if r.m.State().Phase != PhaseIdle {
		t.Fatalf("phase after check = %s", r.m.State().Phase)
	}
	if got := r.m.LastCheck(); got == nil || got.Latest != "1.4.0" {
		t.Fatalf("last check not stored: %+v", got)
	}
	ua := r.userAgents[0]
	if ua != "opskest/1.3.9" {
		t.Fatalf("user agent = %q", ua)
	}
}

func TestCheckUpToDateAndOlder(t *testing.T) {
	for _, latest := range []string{"1.3.9", "v1.3.9", "1.3.8", "1.0.0"} {
		r := newRig(t, rigOpts{newVersion: latest})
		res, err := r.m.Check(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if res.Available {
			t.Errorf("latest %s offered over 1.3.9", latest)
		}
	}
}

func TestCheckNoKeyReportsButDoesNotVerify(t *testing.T) {
	r := newRig(t, rigOpts{noKey: true})
	res, err := r.m.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || res.SignatureVerified {
		t.Fatalf("notify mode without key: %+v", res)
	}
	if r.hit("sig") != 0 {
		t.Error("signature must not be fetched without a key")
	}
	ok, reason := r.m.CanInstall()
	if ok || !strings.Contains(reason, "add the release public key in Settings") {
		t.Fatalf("CanInstall = %v, %q", ok, reason)
	}
}

func TestCheckRejectsBadSignatures(t *testing.T) {
	t.Run("tampered manifest", func(t *testing.T) {
		r := newRig(t, rigOpts{})
		r.mu.Lock()
		r.manifest = []byte(strings.Replace(string(r.manifest), "1.4.0", "9.9.9", 1))
		r.mu.Unlock()
		assertRejected(t, r)
	})
	t.Run("wrong key", func(t *testing.T) {
		r := newRig(t, rigOpts{})
		other, _ := newKeys(t)
		r.mu.Lock()
		r.cfg.PublicKey = b64(other)
		r.mu.Unlock()
		assertRejected(t, r)
	})
	t.Run("signature missing", func(t *testing.T) {
		r := newRig(t, rigOpts{})
		r.mu.Lock()
		r.sigStatus = http.StatusNotFound
		r.mu.Unlock()
		assertRejected(t, r)
	})
	t.Run("signature garbage", func(t *testing.T) {
		r := newRig(t, rigOpts{})
		r.mu.Lock()
		r.sig = "not-a-signature"
		r.mu.Unlock()
		assertRejected(t, r)
	})
	t.Run("invalid configured key", func(t *testing.T) {
		r := newRig(t, rigOpts{})
		r.mu.Lock()
		r.cfg.PublicKey = "bm9wZQ=="
		r.mu.Unlock()
		assertRejected(t, r)
	})
}

func assertRejected(t *testing.T, r *rig) {
	t.Helper()
	res, err := r.m.Check(context.Background())
	if err == nil {
		t.Fatalf("check succeeded: %+v", res)
	}
	if res == nil || res.Available || res.Latest != "" || res.SignatureVerified || res.Error == "" {
		t.Fatalf("a rejected manifest must never be offered: %+v", res)
	}
	last := r.m.LastCheck()
	if last.Available || last.Error == "" {
		t.Fatalf("stored result offers a rejected manifest: %+v", last)
	}
	if err := r.m.Install(context.Background(), nil); err == nil {
		t.Fatal("Install proceeded on a rejected manifest")
	}
	if r.hit("asset") != 0 {
		t.Fatal("asset downloaded from a rejected manifest")
	}
}

func b64(k []byte) string { return base64.StdEncoding.EncodeToString(k) }

func TestCheckFeedProblems(t *testing.T) {
	r := newRig(t, rigOpts{})

	set := func(url string) {
		r.mu.Lock()
		r.cfg.FeedURL = url
		r.mu.Unlock()
	}
	set("")
	if _, err := r.m.Check(context.Background()); !errors.Is(err, ErrNoFeed) {
		t.Fatalf("empty feed: %v", err)
	}
	for _, bad := range []string{"http://example.com/m.json", "ftp://x/y", "https://u:p@example.com/m.json", "nonsense"} {
		set(bad)
		if _, err := r.m.Check(context.Background()); err == nil {
			t.Errorf("feed %q accepted", bad)
		}
	}
	// HTTP error status.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	set(srv.URL + "/m.json")
	if _, err := r.m.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("500: %v", err)
	}
	// Not JSON.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) }))
	defer srv2.Close()
	r.mu.Lock()
	r.cfg.PublicKey = ""
	r.mu.Unlock()
	set(srv2.URL + "/m.json")
	if _, err := r.m.Check(context.Background()); err == nil {
		t.Fatal("html accepted as manifest")
	}
}

func TestCheckManifestSizeLimit(t *testing.T) {
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"9.9.9","assets":[],"notes":"` + strings.Repeat("a", MaxManifestBytes) + `"}`))
	}))
	defer big.Close()
	r := newRig(t, rigOpts{noKey: true})
	r.mu.Lock()
	r.cfg.FeedURL = big.URL + "/m.json"
	r.mu.Unlock()
	_, err := r.m.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("err = %v", err)
	}
	// Chunked (no Content-Length) over the limit is caught too.
	chunk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fl := w.(http.Flusher)
		for i := 0; i < 20; i++ {
			_, _ = w.Write([]byte(strings.Repeat("x", 100<<10)))
			fl.Flush()
		}
	}))
	defer chunk.Close()
	r.mu.Lock()
	r.cfg.FeedURL = chunk.URL + "/m.json"
	r.mu.Unlock()
	if _, err := r.m.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("chunked err = %v", err)
	}
}

func TestCheckRedirects(t *testing.T) {
	r := newRig(t, rigOpts{})
	target := r.srv.URL + "/manifest.json"

	chain := func(hops int) string {
		mux := http.NewServeMux()
		for i := 0; i < hops; i++ {
			next := fmt.Sprintf("/hop%d", i+1)
			if i == hops-1 {
				next = ""
			}
			i := i
			mux.HandleFunc(fmt.Sprintf("/hop%d", i), func(w http.ResponseWriter, req *http.Request) {
				if next == "" {
					http.Redirect(w, req, target, http.StatusFound)
					return
				}
				http.Redirect(w, req, next, http.StatusFound)
			})
		}
		s := httptest.NewServer(mux)
		t.Cleanup(s.Close)
		return s.URL + "/hop0"
	}
	set := func(u string) {
		r.mu.Lock()
		r.cfg.FeedURL = u
		r.cfg.PublicKey = ""
		r.mu.Unlock()
	}
	// 3 redirects: allowed (hop0 -> hop1 -> hop2 -> target is 3).
	set(chain(3))
	if _, err := r.m.Check(context.Background()); err != nil {
		t.Fatalf("3 redirects should be followed: %v", err)
	}
	// 4: refused.
	set(chain(4))
	if _, err := r.m.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("4 redirects: %v", err)
	}
	// Redirect to plain http on a non-loopback host: refused.
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "http://example.com/manifest.json", http.StatusFound)
	}))
	defer down.Close()
	set(down.URL + "/m.json")
	if _, err := r.m.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "non-https") {
		t.Fatalf("downgrade redirect: %v", err)
	}
}

func TestCheckErrorsNeverContainFeedQuery(t *testing.T) {
	r := newRig(t, rigOpts{noKey: true})
	// Connection refused on a closed port, with a secret in the query.
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL + "/manifest.json?token=SUPERSECRET"
	dead.Close()
	r.mu.Lock()
	r.cfg.FeedURL = url
	r.mu.Unlock()
	res, err := r.m.Check(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, s := range []string{err.Error(), res.Error, r.m.LastCheck().Error} {
		if strings.Contains(s, "SUPERSECRET") || strings.Contains(s, "token=") {
			t.Fatalf("error leaks the feed URL: %q", s)
		}
	}
	// A 404 with a signature URL keeping the query also stays clean.
	r.mu.Lock()
	r.cfg.FeedURL = r.srv.URL + "/missing?token=SUPERSECRET"
	r.mu.Unlock()
	if _, err := r.m.Check(context.Background()); err == nil || strings.Contains(err.Error(), "SUPERSECRET") {
		t.Fatalf("err = %v", err)
	}
}

func TestSignatureURLKeepsQuery(t *testing.T) {
	// The feed carries a query (e.g. a token); the .sig sits next to it.
	r := newRig(t, rigOpts{})
	r.mu.Lock()
	r.cfg.FeedURL = r.srv.URL + "/manifest.json?token=abc"
	r.mu.Unlock()
	res, err := r.m.Check(context.Background())
	if err != nil || !res.SignatureVerified {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestCheckTimeoutIsBounded(t *testing.T) {
	// A hung server must not hold a check forever: the context deadline
	// is applied by fetchLimited. Use a cancelled parent to make it fast.
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { <-req.Context().Done() }))
	defer hang.Close()
	r := newRig(t, rigOpts{noKey: true})
	r.mu.Lock()
	r.cfg.FeedURL = hang.URL + "/m.json"
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := r.m.Check(ctx); err == nil {
		t.Fatal("expected a timeout")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("check did not honour the deadline")
	}
	if r.m.State().Phase != PhaseIdle {
		t.Fatalf("phase after failed check = %s", r.m.State().Phase)
	}
}

func TestDevAndUnparsableBuildsNeverOffered(t *testing.T) {
	for _, cur := range []string{"dev", "", "test", "nightly-123"} {
		r := newRig(t, rigOpts{})
		r.m.o.Version = cur
		res, err := r.m.Check(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if res.Available {
			t.Errorf("version %q was offered an update", cur)
		}
		if ok, _ := r.m.CanInstall(); ok {
			t.Errorf("version %q may install", cur)
		}
		if err := r.m.Install(context.Background(), nil); err == nil {
			t.Errorf("version %q installed", cur)
		}
		if r.hit("asset") != 0 {
			t.Errorf("version %q downloaded", cur)
		}
	}
}

func TestPrereleaseOfferingRules(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"1.3.9", "1.4.0-rc.1", false}, // stable builds are not moved onto an rc
		{"1.4.0-rc.1", "1.4.0-rc.2", true},
		{"1.4.0-rc.1", "1.4.0", true},
		{"1.4.0", "1.4.0-rc.9", false},
		{"1.3.9", "1.4.0", true},
		{"v1.3.9", "v1.4.0", true},
	}
	for _, c := range cases {
		cur, _ := ParseVersion(c.cur)
		lat, _ := ParseVersion(c.latest)
		if got := offered(cur, lat); got != c.want {
			t.Errorf("offered(%s, %s) = %v, want %v", c.cur, c.latest, got, c.want)
		}
	}
}

func TestMinVersion(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.publish("2.0.0", "1.5.0")
	res, err := r.m.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || !res.MinVersionBlocked || res.MinVersion != "1.5.0" {
		t.Fatalf("res = %+v", res)
	}
	st := r.m.Status()
	if st.CanInstall || !strings.Contains(st.Reason, "1.5.0") {
		t.Fatalf("status = %+v", st)
	}
	if err := r.m.Install(context.Background(), nil); err == nil {
		t.Fatal("install passed minVersion")
	}
	if r.hit("asset") != 0 || len(r.eventList()) != 0 {
		t.Fatalf("backup/download ran: %v", r.eventList())
	}
	// Current at or above minVersion: fine.
	r.publish("2.0.0", "1.3.9")
	res, _ = r.m.Check(context.Background())
	if res.MinVersionBlocked {
		t.Fatalf("1.3.9 >= minVersion 1.3.9 must not block: %+v", res)
	}
}

func TestStatusNoAssetForPlatform(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.m.o.GOARCH = "riscv64"
	res, err := r.m.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || res.Asset != nil {
		t.Fatalf("res = %+v", res)
	}
	st := r.m.Status()
	if st.CanInstall || !strings.Contains(st.Reason, "riscv64") {
		t.Fatalf("status = %+v", st)
	}
}

func TestPersistenceAcrossRestart(t *testing.T) {
	r := newRig(t, rigOpts{})
	var saved Saved
	r.m.o.SaveSaved = func(s Saved) { saved = s }
	if _, err := r.m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if saved.CheckJSON == "" {
		t.Fatal("check was not persisted")
	}
	if strings.Contains(saved.CheckJSON, r.srv.URL) {
		t.Fatal("persisted state contains the feed/asset URL")
	}

	// Restart as the same version: still offered, with notes.
	m2 := New(Options{Version: "1.3.9", Config: r.m.o.Config, LoadSaved: func() Saved { return saved }, ExePath: r.exe, Method: MethodBinary})
	if c := m2.LastCheck(); c == nil || !c.Available || c.Latest != "1.4.0" || c.Notes == "" {
		t.Fatalf("restored = %+v", c)
	}
	// Restart as the new version: no longer claims an update.
	m3 := New(Options{Version: "1.4.0", Config: r.m.o.Config, LoadSaved: func() Saved { return saved }, ExePath: r.exe, Method: MethodBinary})
	if c := m3.LastCheck(); c == nil || c.Available {
		t.Fatalf("after updating, restored = %+v", c)
	}
	// Garbage in the store is ignored.
	m4 := New(Options{Version: "1.3.9", LoadSaved: func() Saved { return Saved{CheckJSON: "{{{"} }, ExePath: r.exe, Method: MethodBinary})
	if m4.LastCheck() != nil {
		t.Fatal("garbage accepted")
	}
}

func TestManualCheckRateLimit(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	m := New(Options{Version: "1.0.0", Now: func() time.Time { return now }, ExePath: "x", Method: MethodBinary})
	if ok, _ := m.AllowManualCheck(); !ok {
		t.Fatal("first check refused")
	}
	now = now.Add(5 * time.Second)
	if ok, retry := m.AllowManualCheck(); ok || retry <= 0 || retry > 5*time.Second {
		t.Fatalf("second check within 10 s: ok=%v retry=%v", ok, retry)
	}
	now = now.Add(5 * time.Second)
	if ok, _ := m.AllowManualCheck(); !ok {
		t.Fatal("check after 10 s refused")
	}
}

func TestConcurrentChecksAreSerialised(t *testing.T) {
	gate := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-gate
		_, _ = w.Write([]byte(`{"version":"1.4.0","assets":[]}`))
	}))
	defer srv.Close()
	m := New(Options{
		Version: "1.3.9", ExePath: "x", Method: MethodBinary,
		Config: func() Config { return Config{Enabled: true, FeedURL: srv.URL + "/m.json"} },
	})
	done := make(chan error, 1)
	go func() { _, err := m.Check(context.Background()); done <- err }()
	deadline := time.Now().Add(2 * time.Second)
	for m.State().Phase != PhaseChecking {
		if time.Now().After(deadline) {
			t.Fatal("check never started")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := m.Check(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("second check: %v", err)
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
