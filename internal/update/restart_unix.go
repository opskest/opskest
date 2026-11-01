//go:build !windows

package update

import (
	"syscall"
)

// startNew replaces the current process image with the binary at exe,
// keeping the PID, arguments and environment. It returns only on failure.
func startNew(exe string, args []string, env []string) error {
	argv := append([]string{exe}, args...)
	return syscall.Exec(exe, argv, env)
}
