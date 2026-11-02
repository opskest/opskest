package scheduler

import (
	"sync"
	"time"
)

// FakeClock is a Clock that only moves when Advance is called. After registers
// a waiter that fires once Advance moves the time to or past its deadline.
// It is safe for concurrent use.
type FakeClock struct {
	mu      sync.Mutex
	changed *sync.Cond // signalled whenever waiters change
	now     time.Time
	waiters []fakeWaiter
}

type fakeWaiter struct {
	at time.Time
	ch chan time.Time
}

// NewFakeClock returns a FakeClock that reads t until advanced.
func NewFakeClock(t time.Time) *FakeClock {
	c := &FakeClock{now: t}
	c.changed = sync.NewCond(&c.mu)
	return c
}

// Now returns the fake time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// After returns a channel that receives the fake time once it reaches now+d.
// A d <= 0 fires immediately and never counts as a waiter.
func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	defer c.mu.Unlock()
	if d <= 0 {
		ch <- c.now
		return ch
	}
	c.waiters = append(c.waiters, fakeWaiter{at: c.now.Add(d), ch: ch})
	c.changed.Broadcast()
	return ch
}

// Advance moves the time forward by d and fires every waiter whose deadline
// has been reached. Channels are buffered, so Advance never blocks on a
// receiver that has gone away.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if w.at.After(c.now) {
			kept = append(kept, w)
			continue
		}
		w.ch <- c.now
	}
	clear(c.waiters[len(kept):])
	c.waiters = kept
	c.changed.Broadcast()
}

// BlockUntilWaiters blocks until at least n waiters are pending. A waiter
// whose receiver stopped listening still counts until its deadline passes.
func (c *FakeClock) BlockUntilWaiters(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.waiters) < n {
		c.changed.Wait()
	}
}

// Waiters returns the number of pending waiters.
func (c *FakeClock) Waiters() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiters)
}
