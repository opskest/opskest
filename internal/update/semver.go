// Package update implements Opskest's signed self-update: it fetches a
// release manifest the owner published, verifies its ed25519 signature,
// and (when allowed) downloads, verifies and swaps in the new binary.
//
// The package is standard library only and knows nothing about the
// database or the HTTP API; the app injects settings, backup and restart
// behaviour through Options.
package update

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed semantic version. Build metadata is kept for display
// but ignored by Compare, as the semver specification requires.
type Version struct {
	Major, Minor, Patch uint64
	Pre                 []string // dot-separated pre-release identifiers
	Build               string
}

// ErrBadVersion is returned by ParseVersion for anything that is not a
// semantic version (including "dev" and the empty string).
var ErrBadVersion = errors.New("not a semantic version")

// ParseVersion parses "1.2.3", "v1.2.3", "1.2.3-rc.1" and "1.2.3+build.5".
// All three numeric components are required.
func ParseVersion(s string) (Version, error) {
	orig := s
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" || len(s) > 128 {
		return Version{}, fmt.Errorf("%w: %q", ErrBadVersion, truncate(orig, 40))
	}
	var v Version
	if i := strings.IndexByte(s, '+'); i >= 0 {
		v.Build = s[i+1:]
		s = s[:i]
		if !validIdents(v.Build, false) {
			return Version{}, fmt.Errorf("%w: %q", ErrBadVersion, truncate(orig, 40))
		}
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		pre := s[i+1:]
		s = s[:i]
		if !validIdents(pre, true) {
			return Version{}, fmt.Errorf("%w: %q", ErrBadVersion, truncate(orig, 40))
		}
		v.Pre = strings.Split(pre, ".")
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("%w: %q", ErrBadVersion, truncate(orig, 40))
	}
	nums := [3]*uint64{&v.Major, &v.Minor, &v.Patch}
	for i, p := range parts {
		if p == "" || len(p) > 9 { // 9 digits: no overflow, no absurd values
			return Version{}, fmt.Errorf("%w: %q", ErrBadVersion, truncate(orig, 40))
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return Version{}, fmt.Errorf("%w: %q", ErrBadVersion, truncate(orig, 40))
			}
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return Version{}, fmt.Errorf("%w: %q", ErrBadVersion, truncate(orig, 40))
		}
		*nums[i] = n
	}
	return v, nil
}

func validIdents(s string, numericNoLeadingZero bool) bool {
	if s == "" {
		return false
	}
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return false
		}
		numeric := true
		for _, c := range id {
			switch {
			case c >= '0' && c <= '9':
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-':
				numeric = false
			default:
				return false
			}
		}
		if numeric && len(id) > 18 {
			return false
		}
		if numericNoLeadingZero && numeric && len(id) > 1 && id[0] == '0' {
			return false
		}
	}
	return true
}

// String returns the canonical form without a leading "v".
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	if v.Build != "" {
		s += "+" + v.Build
	}
	return s
}

// IsPrerelease reports whether v carries a pre-release tag.
func (v Version) IsPrerelease() bool { return len(v.Pre) > 0 }

// Compare returns -1, 0 or 1 following semver precedence: a pre-release
// sorts below its release, numeric identifiers compare numerically and sort
// below alphanumeric ones, and build metadata is ignored.
func (v Version) Compare(o Version) int {
	for _, p := range [][2]uint64{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}} {
		if p[0] != p[1] {
			if p[0] < p[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(v.Pre) == 0 && len(o.Pre) == 0:
		return 0
	case len(v.Pre) == 0:
		return 1
	case len(o.Pre) == 0:
		return -1
	}
	for i := 0; i < len(v.Pre) && i < len(o.Pre); i++ {
		if c := compareIdent(v.Pre[i], o.Pre[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(v.Pre) < len(o.Pre):
		return -1
	case len(v.Pre) > len(o.Pre):
		return 1
	}
	return 0
}

func compareIdent(a, b string) int {
	an, aok := numericIdent(a)
	bn, bok := numericIdent(b)
	switch {
	case aok && bok:
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		}
		return 0
	case aok:
		return -1
	case bok:
		return 1
	}
	return strings.Compare(a, b)
}

func numericIdent(s string) (uint64, bool) {
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	// Longer than 19 digits cannot fit a uint64; compare by length then text.
	if len(s) > 19 {
		return ^uint64(0), true
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil
}

// CompareVersions parses both strings and compares them. ok is false when
// either side is not a semantic version.
func CompareVersions(a, b string) (cmp int, ok bool) {
	av, err := ParseVersion(a)
	if err != nil {
		return 0, false
	}
	bv, err := ParseVersion(b)
	if err != nil {
		return 0, false
	}
	return av.Compare(bv), true
}

// Normalize returns the canonical form of a version string (leading "v"
// dropped) or the trimmed input when it is not a semantic version.
func Normalize(s string) string {
	if v, err := ParseVersion(s); err == nil {
		return v.String()
	}
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
