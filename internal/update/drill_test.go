package update

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// A feed that is merely unreachable must not make a known update vanish; a
// feed that answers with a bad signature must.
func TestFailedCheckKeepsKnownUpdateOnlyForTransientErrors(t *testing.T) {
	r := newRig(t, rigOpts{})
	if res, err := r.m.Check(context.Background()); err != nil || !res.Available {
		t.Fatalf("first check: %+v %v", res, err)
	}

	// Signature endpoint down: transient.
	r.mu.Lock()
	r.sigStatus = 503
	r.mu.Unlock()
	res, err := r.m.Check(context.Background())
	if err == nil {
		t.Fatal("want an error")
	}
	if res.Error == "" || res.Latest != "1.4.0" || !res.Available || res.Notes == "" {
		t.Fatalf("known update lost on a transient failure: %+v", res)
	}
	if got := r.m.LastCheck(); got == nil || !got.Available || got.Error == "" {
		t.Fatalf("stored result: %+v", got)
	}

	// A persisted copy of that result still offers the update after restart.
	st := r.m.Status().Check
	if st == nil || !st.Available {
		t.Fatalf("status lost the update: %+v", st)
	}

	// Bad signature: not transient, nothing is offered.
	r.mu.Lock()
	r.sigStatus = 0
	r.sig = "AAAA"
	r.mu.Unlock()
	res, err = r.m.Check(context.Background())
	if err == nil || res.Available || res.Latest != "" || !strings.Contains(res.Error, "rejected") {
		t.Fatalf("bad signature must clear the offer: %+v %v", res, err)
	}
}

// Install and rollback write audit rows once the swap has happened.
func TestInstallAndRollbackAudit(t *testing.T) {
	r := newRig(t, rigOpts{})
	var mu sync.Mutex
	var rows []string
	r.m.o.Audit = func(action, detail string) {
		mu.Lock()
		rows = append(rows, action+": "+detail)
		mu.Unlock()
	}
	if err := r.m.Install(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	r.m.setState(State{Phase: PhaseIdle}, nil) // a real restart would be a new process
	r.m.o.Version = "1.4.0"
	if err := r.m.Rollback(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(rows) != 2 || !strings.HasPrefix(rows[0], "update_installed: 1.3.9 -> 1.4.0, backup opskest-") || !strings.HasPrefix(rows[1], "update_rolled_back:") {
		t.Fatalf("audit rows = %q", rows)
	}
}

// OPSKEST_UPDATE_RESTART=exit: shut down only; the supervisor starts it.
func TestRestartModeExitStartsNothing(t *testing.T) {
	orig := startProcess
	defer func() { startProcess = orig; SetRestartMode("") }()
	startProcess = func(string, []string, []string) error { t.Fatal("exit mode must not start a process"); return nil }
	SetRestartMode(" EXIT ")
	if RestartMode() != RestartExit {
		t.Fatalf("mode = %q", RestartMode())
	}
	RequestRestart("/x/opskest")
	started, err := RunPendingRestart(nil, nil)
	if started || err != nil || RestartRequested() {
		t.Fatalf("started=%v err=%v pending=%v", started, err, RestartRequested())
	}
	SetRestartMode("nonsense")
	if RestartMode() != RestartSelf {
		t.Fatal("unknown values mean self")
	}
}

// A loopback http feed is allowed (local test feeds); lookalikes are not.
func TestLoopbackFeedAcceptedInSettings(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1:18990/manifest.json", "http://localhost:9/m.json", "http://[::1]:9/m.json"} {
		if err := ValidateSettingsFeedURL(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range []string{"http://127.0.0.1.nip.io/m.json", "http://10.0.0.1/m.json", "ftp://127.0.0.1/m.json"} {
		if ValidateSettingsFeedURL(u) == nil {
			t.Errorf("%s accepted", u)
		}
	}
}
