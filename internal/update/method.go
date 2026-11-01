package update

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Install methods reported to the UI.
const (
	MethodBinary = "binary" // a plain executable this process may replace
	MethodDocker = "docker" // container: pull a new image instead
	MethodOther  = "other"  // package-managed or temporary location
)

// DockerHint is the command shown to Docker users.
const DockerHint = "docker compose pull && docker compose up -d"

// Env abstracts the bits of the host that method detection reads, so tests
// can fake a container.
type Env struct {
	Getenv   func(string) string
	Exists   func(path string) bool
	ReadFile func(path string) ([]byte, error)
}

// HostEnv is the real host.
func HostEnv() Env {
	return Env{
		Getenv: os.Getenv,
		Exists: func(p string) bool { _, err := os.Stat(p); return err == nil },
		ReadFile: func(p string) ([]byte, error) {
			f, err := os.Open(p)
			if err != nil {
				return nil, err
			}
			defer f.Close()
			buf := make([]byte, 64<<10)
			n, _ := f.Read(buf)
			return buf[:n], nil
		},
	}
}

// InDocker reports whether the process runs in a container: the explicit
// OPSKEST_IN_DOCKER=1, /.dockerenv, podman's /run/.containerenv, or a
// docker/containerd/kubernetes hint in the cgroup or mount tables.
func InDocker(e Env) bool {
	if e.Getenv != nil && e.Getenv("OPSKEST_IN_DOCKER") == "1" {
		return true
	}
	if e.Exists != nil && (e.Exists("/.dockerenv") || e.Exists("/run/.containerenv")) {
		return true
	}
	if e.ReadFile != nil {
		for _, p := range []string{"/proc/1/cgroup", "/proc/self/cgroup"} {
			if b, err := e.ReadFile(p); err == nil && hasContainerHint(string(b)) {
				return true
			}
		}
	}
	return false
}

func hasContainerHint(s string) bool {
	for _, h := range []string{"docker", "kubepods", "containerd", "libpod"} {
		if strings.Contains(s, h) {
			return true
		}
	}
	return false
}

// DetectMethod classifies how this executable is installed.
func DetectMethod(e Env, exe string) string {
	if InDocker(e) {
		return MethodDocker
	}
	if packageManaged(exe) {
		return MethodOther
	}
	return MethodBinary
}

// packageManaged recognises paths owned by a package manager or created
// transiently (go run), where replacing the file would fight the owner.
func packageManaged(exe string) bool {
	p := filepath.ToSlash(exe)
	lp := strings.ToLower(p)
	if strings.Contains(lp, "/go-build") && strings.Contains(lp, "/exe/") {
		return true // go run
	}
	if runtime.GOOS == "windows" {
		return strings.Contains(lp, "/windowsapps/")
	}
	for _, prefix := range []string{
		"/usr/bin/", "/usr/sbin/", "/bin/", "/sbin/", "/usr/lib/", "/snap/",
		"/nix/store/", "/opt/homebrew/cellar/", "/usr/local/cellar/", "/var/lib/flatpak/",
	} {
		if strings.HasPrefix(lp, prefix) {
			return true
		}
	}
	return false
}
