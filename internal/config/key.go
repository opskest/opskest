package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// KeyFileName is where the generated encryption key lives, inside DataDir.
const KeyFileName = "encryption.key"

// ResolveSecretKey makes sure SecretKey is set. When SECRET_ENCRYPTION_KEY
// was not provided it reads DataDir/encryption.key, creating it (mode 0600)
// with a random 32-byte key the first time. It reports whether a new key
// was created, so the caller can tell the operator to keep it with their
// backups: without this key the encrypted channel, monitor and two-factor
// secrets in a database copy cannot be read.
func (c *Config) ResolveSecretKey() (created bool, err error) {
	if len(c.SecretKey) == 32 {
		return false, nil
	}
	path := filepath.Join(c.DataDir, KeyFileName)

	raw, err := os.ReadFile(path)
	// The winner of a first-start race creates the file before it writes the
	// key into it; a loser that reads in between sees it empty. Give the winner
	// a moment instead of failing the start with "must decode to 32 bytes".
	for i := 0; err == nil && len(strings.TrimSpace(string(raw))) == 0 && i < 40; i++ {
		time.Sleep(25 * time.Millisecond)
		raw, err = os.ReadFile(path)
	}
	switch {
	case err == nil:
		key, perr := parseSecretKey(strings.TrimSpace(string(raw)))
		if perr != nil {
			return false, fmt.Errorf("%s: %w", path, perr)
		}
		c.SecretKey = key
		return false, nil
	case !errors.Is(err, fs.ErrNotExist):
		return false, fmt.Errorf("read %s: %w", path, err)
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return false, fmt.Errorf("generate encryption key: %w", err)
	}
	// O_EXCL: if two processes race on first start, one wins and the other
	// reads the winner's key rather than overwriting it.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return c.ResolveSecretKey()
		}
		return false, fmt.Errorf("create %s: %w", path, err)
	}
	_, werr := f.WriteString(hex.EncodeToString(key) + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(path)
		return false, fmt.Errorf("write %s: %w", path, werr)
	}
	c.SecretKey = key
	return true, nil
}
