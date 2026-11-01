package update

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// The restart is split in two so the database is closed before the new
// binary starts: the install code only records that a restart is wanted
// (RequestRestart) and asks the app to shut down gracefully; main calls
// RunPendingRestart after app.Run has returned and every defer (store,
// writer) has run. On Unix that is an exec in the same PID; on Windows a
// detached child is started and this process exits.

var pending struct {
	mu  sync.Mutex
	on  bool
	exe string
}

// startProcess is overridable in tests.
var startProcess = startNew

// Restart modes (OPSKEST_UPDATE_RESTART).
//
//	"self" (default): the process starts the new binary itself (exec on
//	      Unix, a detached child on Windows).
//	"exit": the process only shuts down cleanly with status 0 and relies on
//	      its supervisor (systemd Restart=always, a Windows service wrapper
//	      such as NSSM or WinSW, a Docker-like manager) to start the new file.
//	      Use it when a supervisor restarts the program: spawning a second
//	      copy would fight the supervisor's copy for the port.
const (
	RestartSelf = "self"
	RestartExit = "exit"
)

var restartMode atomic.Value // string

// SetRestartMode records OPSKEST_UPDATE_RESTART. Unknown values mean "self".
func SetRestartMode(m string) {
	m = strings.ToLower(strings.TrimSpace(m))
	if m != RestartExit {
		m = RestartSelf
	}
	restartMode.Store(m)
}

// RestartMode returns "self" or "exit".
func RestartMode() string {
	if s, _ := restartMode.Load().(string); s == RestartExit {
		return RestartExit
	}
	return RestartSelf
}

// RequestRestart records that the process should start the binary at exe
// once it has shut down.
func RequestRestart(exe string) {
	pending.mu.Lock()
	pending.on, pending.exe = true, exe
	pending.mu.Unlock()
}

// RestartRequested reports whether RequestRestart was called.
func RestartRequested() bool {
	pending.mu.Lock()
	defer pending.mu.Unlock()
	return pending.on
}

func clearPending() {
	pending.mu.Lock()
	pending.on, pending.exe = false, ""
	pending.mu.Unlock()
}

// RunPendingRestart starts the new binary if a restart was requested. args
// are the process arguments without argv[0]; env is the environment to
// hand over (nil = current). On Unix a successful call never returns.
// started is true when a detached child was started (Windows): the caller
// should exit 0.
//
// If the new binary cannot be started, the previous one (kept as .prev)
// is swapped back and started instead, so a broken download cannot leave
// the service down; the original error is still returned.
func RunPendingRestart(args, env []string) (started bool, err error) {
	pending.mu.Lock()
	on, exe := pending.on, pending.exe
	pending.mu.Unlock()
	if !on {
		return false, nil
	}
	clearPending()
	if RestartMode() == RestartExit {
		return false, nil // the supervisor starts the new file
	}
	if env == nil {
		env = os.Environ()
	}
	if err := startProcess(exe, args, env); err != nil {
		first := fmt.Errorf("could not start the updated binary: %w", err)
		if _, perr := os.Stat(prevPath(exe)); perr == nil {
			if serr := swapWithPrev(exe); serr == nil {
				if err2 := startProcess(exe, args, env); err2 == nil {
					return true, first
				}
			}
		}
		return false, first
	}
	return true, nil
}
