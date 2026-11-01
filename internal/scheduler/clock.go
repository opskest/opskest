package scheduler

import "time"

// Clock is the scheduler's source of time. RealClock is the wall clock;
// tests use a fake (fakeclock_test.go) that moves by hand.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// RealClock returns a Clock backed by the time package.
func RealClock() Clock { return realClock{} }
