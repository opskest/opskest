//go:build !windows

package config

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

func systemTimeZone() string {
	if raw, err := os.ReadFile("/etc/timezone"); err == nil {
		if name := strings.TrimSpace(string(raw)); name != "" {
			return name
		}
	}
	if target, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		if _, name, found := strings.Cut(filepath.ToSlash(target), "/zoneinfo/"); found {
			return name
		}
	}
	if name := time.Local.String(); name != "Local" {
		return name
	}
	return "UTC"
}
