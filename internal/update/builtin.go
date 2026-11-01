package update

import (
	"strings"
	"sync/atomic"
)

// builtinKey is the release public key compiled into an official build
// (main.updatePublicKey, set with -ldflags -X). A key saved in Settings
// always wins over it.
var builtinKey atomic.Value // string

// SetBuiltinPublicKey records the build-time key. Called once from main.
func SetBuiltinPublicKey(b64 string) { builtinKey.Store(strings.TrimSpace(b64)) }

// BuiltinPublicKey returns the build-time key, or "".
func BuiltinPublicKey() string {
	s, _ := builtinKey.Load().(string)
	return s
}

// builtinFeed is the release manifest address compiled into an official build
// (main.updateFeedURL, set with -ldflags -X). A feed saved in Settings always
// wins over it.
var builtinFeed atomic.Value // string

// SetBuiltinFeedURL records the build-time feed address. Called once from main.
func SetBuiltinFeedURL(u string) { builtinFeed.Store(strings.TrimSpace(u)) }

// BuiltinFeedURL returns the build-time feed address, or "".
func BuiltinFeedURL() string {
	s, _ := builtinFeed.Load().(string)
	return s
}
