package config

import (
	"bytes"
	"sync"
	"testing"
)

// Several processes (or a restart loop) starting at once on a fresh data
// directory must all end up with the same key and none may fail.
func TestResolveSecretKeyFirstStartRace(t *testing.T) {
	for round := 0; round < 20; round++ {
		dir := t.TempDir()
		const n = 8
		keys := make([][]byte, n)
		errs := make([]error, n)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				c := &Config{DataDir: dir}
				<-start
				_, errs[i] = c.ResolveSecretKey()
				keys[i] = c.SecretKey
			}(i)
		}
		close(start)
		wg.Wait()
		for i := 0; i < n; i++ {
			if errs[i] != nil {
				t.Fatalf("round %d, starter %d: %v", round, i, errs[i])
			}
			if !bytes.Equal(keys[i], keys[0]) {
				t.Fatalf("round %d: starters ended with different keys", round)
			}
		}
	}
}
