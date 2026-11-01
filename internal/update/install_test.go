package update

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// unchanged asserts the install directory is exactly as before an aborted
// update: the old binary byte for byte, no .prev, no temp files, and no
// restart.
func (r *rig) unchanged() {
	r.t.Helper()
	if !bytes.Equal(readFile(r.t, r.exe), r.oldData) {
		r.t.Fatal("the running executable was modified")
	}
	if names := r.dirEntries(); len(names) != 1 {
		r.t.Fatalf("install dir should hold only the executable, has %v", names)
	}
	if len(r.restartList()) != 0 {
		r.t.Fatal("restart was requested")
	}
}

func TestInstallEndToEnd(t *testing.T) {
	r := newRig(t, rigOpts{})
	var phases []Phase
	var lastPct int
	err := r.m.Install(context.Background(), func(s State) {
		if len(phases) == 0 || phases[len(phases)-1] != s.Phase {
			phases = append(phases, s.Phase)
		}
		if s.Phase == PhaseDownload {
			if s.Percent < lastPct {
				t.Errorf("download percent went backwards: %d -> %d", lastPct, s.Percent)
			}
			lastPct = s.Percent
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	// Backup happened before the asset was touched.
	ev := r.eventList()
	if len(ev) != 2 || ev[0] != "backup" || ev[1] != "asset" {
		t.Fatalf("event order = %v, want [backup asset]", ev)
	}
	want := []Phase{PhaseChecking, PhaseBackingUp, PhaseDownload, PhaseVerifying, PhaseInstalling, PhaseRestarting}
	if len(phases) != len(want) {
		t.Fatalf("phases = %v, want %v", phases, want)
	}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("phases = %v, want %v", phases, want)
		}
	}
	if lastPct != 100 {
		t.Errorf("download progress ended at %d", lastPct)
	}

	// The new binary is in place, the old one kept as .prev.
	if !bytes.Equal(readFile(t, r.exe), r.asset) {
		t.Fatal("executable is not the downloaded binary")
	}
	if !bytes.Equal(readFile(t, r.exe+".prev"), r.oldData) {
		t.Fatal(".prev is not the old binary")
	}
	// Only exe and .prev remain: the temp download is gone.
	if names := r.dirEntries(); len(names) != 2 {
		t.Fatalf("leftovers in install dir: %v", names)
	}
	if rs := r.restartList(); len(rs) != 1 || rs[0] != r.exe {
		t.Fatalf("restart calls = %v", rs)
	}
	if r.m.State().Phase != PhaseRestarting {
		t.Fatalf("final phase = %s", r.m.State().Phase)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(r.exe)
		if fi.Mode().Perm()&0o111 == 0 {
			t.Fatal("installed binary is not executable")
		}
	}
	// The installed file really is runnable at the new version.
	if got, err := probeVersion(context.Background(), r.exe); err != nil || got != "1.4.0" {
		t.Fatalf("installed binary says %q, %v", got, err)
	}
	if !r.m.CanRollback() {
		t.Fatal("rollback should be offered after an install")
	}
}

func TestInstallBackupFailureAborts(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.backupErr = errors.New("disk full")
	err := r.m.Install(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "backup") {
		t.Fatalf("err = %v", err)
	}
	if r.hit("asset") != 0 {
		t.Fatal("asset downloaded even though the backup failed")
	}
	r.unchanged()
	st := r.m.State()
	if st.Phase != PhaseFailed || !strings.Contains(st.Error, "backup") {
		t.Fatalf("state = %+v", st)
	}
	// A failed install can be retried by hand once the cause is fixed.
	r.backupErr = nil
	if err := r.m.Install(context.Background(), nil); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestInstallNoBackupFuncAborts(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.m.o.Backup = nil
	if err := r.m.Install(context.Background(), nil); err == nil {
		t.Fatal("installed without any backup")
	}
	r.unchanged()
}

func TestInstallWrongSHA256Aborts(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.setAssetDecl(strings.Repeat("ab", 32), 0)
	err := r.m.Install(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v", err)
	}
	r.unchanged()
	if r.m.State().Phase != PhaseFailed {
		t.Fatalf("phase = %s", r.m.State().Phase)
	}
}

func TestInstallWrongSizeAborts(t *testing.T) {
	for _, chunky := range []bool{false, true} {
		for _, delta := range []int64{-5, +5} {
			r := newRig(t, rigOpts{})
			r.assetChunky = chunky
			r.setAssetDecl("", r.assetSize+delta)
			err := r.m.Install(context.Background(), nil)
			if err == nil || !strings.Contains(err.Error(), "size") {
				t.Fatalf("chunky=%v delta=%d: err = %v", chunky, delta, err)
			}
			r.unchanged()
		}
	}
}

func TestInstallVersionMismatchAborts(t *testing.T) {
	// The manifest promises 1.4.0 but the binary reports 1.3.9.
	r := newRig(t, rigOpts{assetVersion: "1.3.9"})
	err := r.m.Install(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "reports version") {
		t.Fatalf("err = %v", err)
	}
	r.unchanged()
}

func TestInstallRefusesNonExecutable(t *testing.T) {
	r := newRig(t, rigOpts{})
	junk := []byte("#!/bin/sh\necho 1.4.0\n")
	r.mu.Lock()
	r.asset = junk
	r.mu.Unlock()
	r.setAssetDeclFor(junk)
	err := r.m.Install(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "executable") {
		t.Fatalf("err = %v", err)
	}
	r.unchanged()
}

func TestInstallRefusesBrokenExecutable(t *testing.T) {
	// Correct magic for this OS, but it cannot run.
	r := newRig(t, rigOpts{})
	var magic []byte
	switch runtime.GOOS {
	case "windows":
		magic = []byte("MZ\x90\x00 not really a program")
	case "darwin":
		magic = []byte{0xcf, 0xfa, 0xed, 0xfe, 0, 0, 0, 0}
	default:
		magic = []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0}
	}
	r.mu.Lock()
	r.asset = magic
	r.mu.Unlock()
	r.setAssetDeclFor(magic)
	if err := r.m.Install(context.Background(), nil); err == nil {
		t.Fatal("a file that cannot run was installed")
	}
	r.unchanged()
}

func TestInstallHTTPErrorStatusAborts(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.mu.Lock()
	r.asset = nil
	r.assetSize = 10
	r.mu.Unlock()
	r.setAssetDecl("", 10)
	// /asset serves 0 bytes with Content-Length 0 != 10.
	if err := r.m.Install(context.Background(), nil); err == nil {
		t.Fatal("expected failure")
	}
	r.unchanged()
}

// setAssetDeclFor points the manifest at new asset bytes.
func (r *rig) setAssetDeclFor(b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.assetSize = int64(len(b))
	r.assetSHA = shaHex(b)
	r.publishLocked("1.4.0", "")
}

func TestInstallRefusedWithoutKey(t *testing.T) {
	r := newRig(t, rigOpts{noKey: true})
	err := r.m.Install(context.Background(), nil)
	var cant *CannotInstallError
	if !errors.As(err, &cant) || !strings.Contains(cant.Reason, "add the release public key in Settings to allow installing from here") {
		t.Fatalf("err = %v", err)
	}
	if r.hit("manifest") != 0 || r.hit("asset") != 0 || len(r.eventList()) != 0 {
		t.Fatal("network or backup activity before refusing")
	}
	r.unchanged()
	if err := r.m.StartInstall(); !errors.As(err, &cant) {
		t.Fatalf("StartInstall: %v", err)
	}
	if r.m.State().Phase != PhaseIdle {
		t.Fatalf("refusal must not change the state: %s", r.m.State().Phase)
	}
}

func TestInstallRefusedNonHTTPSAsset(t *testing.T) {
	r := newRig(t, rigOpts{})
	for _, u := range []string{"http://example.com/opskest", "ftp://example.com/opskest", "file:///tmp/opskest"} {
		r.mu.Lock()
		r.publishLocked("1.4.0", "")
		r.manifest = bytes.ReplaceAll(r.manifest, []byte(r.srv.URL+"/asset"), []byte(u))
		r.sig = sign(r.priv, r.manifest)
		r.mu.Unlock()
		err := r.m.Install(context.Background(), nil)
		if err == nil || !strings.Contains(err.Error(), "asset URL") {
			t.Fatalf("%s: err = %v", u, err)
		}
		if len(r.eventList()) != 0 {
			t.Fatalf("%s: backup ran before the URL was refused", u)
		}
	}
	r.unchanged()
}

func TestInstallRefusedOversizeAsset(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.setAssetDecl("", MaxAssetBytes+1)
	if err := r.m.Install(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "200 MiB") {
		t.Fatalf("err = %v", err)
	}
	r.setAssetDecl("", 1)
	r.mu.Lock()
	r.assetSize = 0
	r.publishLocked("1.4.0", "")
	r.mu.Unlock()
	if err := r.m.Install(context.Background(), nil); err == nil {
		t.Fatal("zero-size asset accepted")
	}
	if r.hit("asset") != 0 || len(r.eventList()) != 0 {
		t.Fatal("download or backup ran")
	}
}

func TestInstallNothingToInstall(t *testing.T) {
	r := newRig(t, rigOpts{newVersion: "1.3.9"})
	err := r.m.Install(context.Background(), nil)
	if !errors.Is(err, ErrNoUpdate) {
		t.Fatalf("err = %v", err)
	}
	if len(r.eventList()) != 0 {
		t.Fatal("backup ran for a no-op")
	}
	if !r.m.failedAt.IsZero() {
		t.Fatal("'already up to date' must not count as a failed install")
	}
	r.unchanged()
}

func TestInstallRefusedInDocker(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.m.method = DetectMethod(Env{
		Getenv:   func(k string) string { return map[string]string{"OPSKEST_IN_DOCKER": "1"}[k] },
		Exists:   func(string) bool { return false },
		ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist },
	}, r.exe)
	if r.m.Method() != MethodDocker {
		t.Fatalf("method = %s", r.m.Method())
	}
	st := r.m.Status()
	if st.CanInstall || st.DockerHint != DockerHint || !strings.Contains(st.Reason, "docker compose pull") {
		t.Fatalf("status = %+v", st)
	}
	var cant *CannotInstallError
	if err := r.m.Install(context.Background(), nil); !errors.As(err, &cant) {
		t.Fatalf("Install: %v", err)
	}
	if err := r.m.Rollback(context.Background(), nil); !errors.As(err, &cant) {
		t.Fatalf("Rollback: %v", err)
	}
	if r.hit("manifest") != 0 || len(r.eventList()) != 0 {
		t.Fatal("activity in docker")
	}
	r.unchanged()
}

func TestInstallRefusedWhenNotWritable(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.m.o.CheckWritable = func(string) bool { return false }
	st := r.m.Status()
	if st.CanInstall || !strings.Contains(st.Reason, "not writable") {
		t.Fatalf("status = %+v", st)
	}
	var cant *CannotInstallError
	if err := r.m.Install(context.Background(), nil); !errors.As(err, &cant) {
		t.Fatalf("err = %v", err)
	}
	r.unchanged()
}

func TestInstallRefusedForOtherMethod(t *testing.T) {
	r := newRig(t, rigOpts{method: MethodOther})
	var cant *CannotInstallError
	if err := r.m.Install(context.Background(), nil); !errors.As(err, &cant) {
		t.Fatalf("err = %v", err)
	}
	if r.m.Status().Method != MethodOther {
		t.Fatal("method not reported")
	}
}

func TestConcurrentInstallRejected(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.backupGo = make(chan struct{})
	first := make(chan error, 1)
	go func() { first <- r.m.Install(context.Background(), nil) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ev := r.eventList()
		if len(ev) > 0 && ev[0] == "backup" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first install never reached the backup")
		}
		time.Sleep(time.Millisecond)
	}
	// While the first runs, everything else is refused.
	if err := r.m.Install(context.Background(), nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Install: %v", err)
	}
	if err := r.m.StartInstall(); !errors.Is(err, ErrBusy) {
		t.Fatalf("StartInstall: %v", err)
	}
	if err := r.m.StartRollback(); !errors.Is(err, ErrBusy) {
		var cant *CannotInstallError
		if !errors.As(err, &cant) { // no .prev yet is also a refusal
			t.Fatalf("StartRollback: %v", err)
		}
	}
	if _, err := r.m.Check(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("Check: %v", err)
	}
	if st := r.m.State(); st.Phase != PhaseBackingUp {
		t.Fatalf("phase = %s", st.Phase)
	}
	close(r.backupGo)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if n := len(r.restartList()); n != 1 {
		t.Fatalf("restart called %d times", n)
	}
	var backups int
	for _, e := range r.eventList() {
		if e == "backup" {
			backups++
		}
	}
	if backups != 1 || r.hit("asset") != 1 {
		t.Fatalf("backups=%d asset hits=%d", backups, r.hit("asset"))
	}
}

func TestManyConcurrentStartInstallOnlyOneWins(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.backupGo = make(chan struct{})
	var ok, busy int32
	done := make(chan struct{})
	for i := 0; i < 16; i++ {
		go func() {
			err := r.m.StartInstall()
			switch {
			case err == nil:
				atomic.AddInt32(&ok, 1)
			case errors.Is(err, ErrBusy):
				atomic.AddInt32(&busy, 1)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 16; i++ {
		<-done
	}
	if ok != 1 || busy != 15 {
		t.Fatalf("ok=%d busy=%d, want 1 and 15", ok, busy)
	}
	close(r.backupGo)
	deadline := time.Now().Add(20 * time.Second)
	for len(r.restartList()) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("background install never finished; state=%+v", r.m.State())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r.m.State().Phase != PhaseRestarting {
		t.Fatalf("phase = %s", r.m.State().Phase)
	}
}

func TestRollback(t *testing.T) {
	r := newRig(t, rigOpts{})
	if r.m.CanRollback() {
		t.Fatal("rollback offered with no .prev")
	}
	var cant *CannotInstallError
	if err := r.m.Rollback(context.Background(), nil); !errors.As(err, &cant) {
		t.Fatalf("rollback without .prev: %v", err)
	}
	if err := r.m.Install(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	// The manager keeps running as the "old" process in this test; a real
	// restart would now be a 1.4.0 process, so move the state on.
	r.m.setState(State{Phase: PhaseIdle}, nil)
	r.m.o.Version = "1.4.0"

	if !r.m.CanRollback() {
		t.Fatal("rollback not offered")
	}
	if err := r.m.Rollback(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readFile(t, r.exe), r.oldData) {
		t.Fatal("executable is not the old binary after rollback")
	}
	if !bytes.Equal(readFile(t, r.exe+".prev"), r.asset) {
		t.Fatal("the newer binary should be kept as .prev (rollback is reversible)")
	}
	if rs := r.restartList(); len(rs) != 2 {
		t.Fatalf("restarts = %v", rs)
	}
	if r.m.skipVersion != "1.4.0" {
		t.Fatalf("auto mode must not reinstall the rolled-back version, skip = %q", r.m.skipVersion)
	}
	// Backup was attempted for the rollback as well (install + rollback).
	n := 0
	for _, e := range r.eventList() {
		if e == "backup" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("backups = %d, want 2", n)
	}
}

func TestRollbackSurvivesFailedBackup(t *testing.T) {
	r := newRig(t, rigOpts{})
	if err := r.m.Install(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	r.m.setState(State{Phase: PhaseIdle}, nil)
	r.backupErr = errors.New("disk full")
	if err := r.m.Rollback(context.Background(), nil); err != nil {
		t.Fatalf("a rollback is recovery and must not be blocked by a failed backup: %v", err)
	}
	if !bytes.Equal(readFile(t, r.exe), r.oldData) {
		t.Fatal("not rolled back")
	}
}

func TestRollbackRefusesCorruptPrev(t *testing.T) {
	r := newRig(t, rigOpts{})
	if err := os.WriteFile(r.exe+".prev", []byte("garbage"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Rollback(context.Background(), nil); err == nil {
		t.Fatal("rolled back to a corrupt file")
	}
	if !bytes.Equal(readFile(t, r.exe), r.oldData) {
		t.Fatal("executable changed")
	}
	if len(r.restartList()) != 0 {
		t.Fatal("restart requested")
	}
}

func TestSwapHelpersBothStyles(t *testing.T) {
	// Both swap strategies are exercised on every OS: the unix one is pure
	// link/rename and the windows one pure rename, so each is testable
	// anywhere as long as the target is not a running program.
	for name, fns := range map[string]struct {
		in   func(exe, n string) error
		swap func(exe string) error
	}{
		"unix":    {swapInUnix, swapWithPrevUnix},
		"windows": {swapInWindows, swapWithPrevWindows},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "app")
			nw := filepath.Join(dir, "new")
			must := func(err error) {
				if err != nil {
					t.Fatal(err)
				}
			}
			must(os.WriteFile(exe, []byte("OLD"), 0o755))
			must(os.WriteFile(nw, []byte("NEW"), 0o755))
			// An older .prev is replaced.
			must(os.WriteFile(exe+".prev", []byte("ANCIENT"), 0o755))

			must(fns.in(exe, nw))
			if string(readFile(t, exe)) != "NEW" || string(readFile(t, exe+".prev")) != "OLD" {
				t.Fatalf("after install: exe=%q prev=%q", readFile(t, exe), readFile(t, exe+".prev"))
			}
			if _, err := os.Stat(nw); err == nil {
				t.Fatal("temp file still exists after the swap")
			}
			must(fns.swap(exe))
			if string(readFile(t, exe)) != "OLD" || string(readFile(t, exe+".prev")) != "NEW" {
				t.Fatalf("after rollback: exe=%q prev=%q", readFile(t, exe), readFile(t, exe+".prev"))
			}
			must(fns.swap(exe)) // and back again
			if string(readFile(t, exe)) != "NEW" {
				t.Fatal("second swap did not return to NEW")
			}
			for _, left := range []string{exe + ".swap", exe + ".prev.tmp"} {
				if _, err := os.Stat(left); err == nil {
					t.Errorf("leftover %s", left)
				}
			}
			// Without .prev a swap is refused.
			must(os.Remove(exe + ".prev"))
			if fns.swap(exe) == nil {
				t.Fatal("swap without .prev succeeded")
			}
			if string(readFile(t, exe)) != "NEW" {
				t.Fatal("failed swap changed the executable")
			}
		})
	}
}

func TestSwapInFailureLeavesExecutableAlone(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "app")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "does-not-exist")
	for name, in := range map[string]func(string, string) error{"unix": swapInUnix, "windows": swapInWindows} {
		if err := in(exe, missing); err == nil {
			t.Fatalf("%s: swap with a missing new file succeeded", name)
		}
		if string(readFile(t, exe)) != "OLD" {
			t.Fatalf("%s: executable changed after a failed swap", name)
		}
		if _, err := os.Stat(exe + ".prev.tmp"); err == nil {
			t.Fatalf("%s: temp prev left behind", name)
		}
	}
}

func TestCheckExecutableMagic(t *testing.T) {
	dir := t.TempDir()
	write := func(b []byte) string {
		p := filepath.Join(dir, "f")
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		goos string
		data []byte
		ok   bool
	}{
		{"linux", []byte{0x7f, 'E', 'L', 'F', 2}, true},
		{"linux", []byte("MZ\x90\x00"), false},
		{"linux", []byte("#!/bin/sh"), false},
		{"windows", []byte("MZ\x90\x00"), true},
		{"windows", []byte{0x7f, 'E', 'L', 'F'}, false},
		{"darwin", []byte{0xcf, 0xfa, 0xed, 0xfe}, true},
		{"darwin", []byte{0xfe, 0xed, 0xfa, 0xcf}, true},
		{"darwin", []byte{0xca, 0xfe, 0xba, 0xbe}, true},
		{"darwin", []byte{0x7f, 'E', 'L', 'F'}, false},
		{"linux", []byte("ab"), false}, // too short
		{"linux", nil, false},
	}
	for _, c := range cases {
		err := checkExecutable(write(c.data), c.goos)
		if (err == nil) != c.ok {
			t.Errorf("%s %q: err = %v, want ok=%v", c.goos, c.data, err, c.ok)
		}
	}
}

func TestFailedStateClearedByNextCheckAndInstall(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.backupErr = errors.New("disk full")
	if err := r.m.Install(context.Background(), nil); err == nil {
		t.Fatal("expected failure")
	}
	if st := r.m.State(); st.Phase != PhaseFailed || st.Error == "" {
		t.Fatalf("state = %+v", st)
	}
	// A refused request leaves the failure visible...
	r.m.o.CheckWritable = func(string) bool { return false }
	r.m.writableAt = time.Time{}
	if err := r.m.StartInstall(); err == nil {
		t.Fatal("expected refusal")
	}
	if r.m.State().Phase != PhaseFailed {
		t.Fatal("a refused install must not clear the failed state")
	}
	r.m.o.CheckWritable = func(string) bool { return true }
	r.m.writableAt = time.Time{}
	// ...a check clears it (and ends idle, not failed).
	if _, err := r.m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := r.m.State(); st.Phase != PhaseIdle || st.Error != "" {
		t.Fatalf("after check: %+v", st)
	}
	// A started install clears it synchronously, before StartInstall returns.
	r.backupErr = nil
	r.backupGo = make(chan struct{})
	r.m.setState(State{Phase: PhaseFailed, Error: "old"}, nil)
	if err := r.m.StartInstall(); err != nil {
		t.Fatal(err)
	}
	if st := r.m.State(); st.Phase == PhaseFailed || st.Error != "" {
		t.Fatalf("stale failure visible after StartInstall: %+v", st)
	}
	close(r.backupGo)
}

func TestBackupNameAndPreviousVersion(t *testing.T) {
	r := newRig(t, rigOpts{})
	var saved Saved
	r.m.o.SaveSaved = func(s Saved) { saved = s }
	if got := r.m.Status().PrevVersion; got != "" {
		t.Fatalf("previous version before any update = %q", got)
	}
	var sawName bool
	if err := r.m.Install(context.Background(), func(s State) {
		if s.Phase == PhaseDownload && s.BackupName == "opskest-20261102-1000.db" {
			sawName = true
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !sawName {
		t.Fatal("backupName not exposed during the install")
	}
	if st := r.m.State(); st.BackupName != "opskest-20261102-1000.db" {
		t.Fatalf("final state = %+v", st)
	}
	if got := r.m.Status().PrevVersion; got != "1.3.9" || saved.PrevVersion != "1.3.9" {
		t.Fatalf("previous version = %q, saved %q", got, saved.PrevVersion)
	}
	// Survives a restart as the new version.
	m2 := New(Options{Version: "1.4.0", ExePath: r.exe, Method: MethodBinary, LoadSaved: func() Saved { return saved }})
	if got := m2.Status().PrevVersion; got != "1.3.9" {
		t.Fatalf("after restart = %q", got)
	}
	// A failed install keeps the backup name too (the backup exists).
	r2 := newRig(t, rigOpts{assetVersion: "1.3.9"})
	_ = r2.m.Install(context.Background(), nil)
	st := r2.m.State()
	if st.Phase != PhaseFailed || st.BackupName == "" {
		t.Fatalf("failed state = %+v", st)
	}
}

func TestCheckRefusedWhenDisabled(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.mu.Lock()
	r.cfg.Enabled = false
	r.mu.Unlock()
	if _, err := r.m.Check(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("err = %v", err)
	}
	if r.hit("manifest") != 0 {
		t.Fatal("fetched while disabled")
	}
	if r.m.State().Phase != PhaseIdle {
		t.Fatal("state changed")
	}
}
