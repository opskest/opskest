package store

import (
	"testing"
	"time"

	"opskest/internal/secret"
)

// testKey is a fixed, obviously-not-secret AES-256 key: 32 bytes of 0x01.
func testKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = 0x01
	}
	return key
}

// testLoc is DISPLAY_TZ for tests: a fixed +05:30 zone (a deliberately
// non-whole-hour offset), so tests never depend on the host's tzdata.
func testLoc() *time.Location {
	return time.FixedZone("IST", 19800)
}

// newTestStore opens a Store backed by a fresh temp directory, cleaned up
// automatically when the test ends.
func newTestStore(t *testing.T) *Store {
	t.Helper()

	box, err := secret.New(testKey())
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}

	s, err := Open(t.TempDir(), box, testLoc())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})
	return s
}
