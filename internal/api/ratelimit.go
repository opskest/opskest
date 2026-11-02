package api

import (
	"sync"
	"time"
)

// rateLimiter is a simple per-key token bucket rate limiter. Each key gets
// a bucket that refills at 1 token per interval, holding at most burst
// tokens. Expired entries are cleaned every minute.
type rateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	rate      float64 // tokens per second
	burst     int
	idle      time.Duration // a bucket untouched this long is dropped (and starts full again)
	lastClean time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(requests int, window time.Duration, burst int) *rateLimiter {
	return &rateLimiter{
		buckets:   make(map[string]*bucket),
		rate:      float64(requests) / window.Seconds(),
		burst:     burst,
		idle:      2 * time.Minute,
		lastClean: time.Now(),
	}
}

// newSlowRateLimiter is newRateLimiter for long windows: buckets live for
// the whole window, so pausing between bursts doesn't refill them early.
func newSlowRateLimiter(requests int, window time.Duration, burst int) *rateLimiter {
	rl := newRateLimiter(requests, window, burst)
	rl.idle = window
	return rl
}

// allow checks if key is allowed to proceed. Returns true if allowed.
func (rl *rateLimiter) allow(key string, now time.Time) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Clean expired entries every minute.
	if now.Sub(rl.lastClean) > time.Minute {
		cutoff := now.Add(-rl.idle)
		for k, b := range rl.buckets {
			if b.last.Before(cutoff) {
				delete(rl.buckets, k)
			}
		}
		rl.lastClean = now
	}

	b, ok := rl.buckets[key]
	if !ok {
		b = &bucket{tokens: float64(rl.burst), last: now}
		rl.buckets[key] = b
	}

	// Refill tokens.
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * rl.rate
	if b.tokens > float64(rl.burst) {
		b.tokens = float64(rl.burst)
	}
	b.last = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// pushLimiter is a per-token rate limiter for push endpoints: 1 req/s.
type pushLimiter struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func newPushLimiter() *pushLimiter {
	return &pushLimiter{last: make(map[string]time.Time)}
}

// allow returns true if the push token hasn't been seen in the last second.
func (pl *pushLimiter) allow(token string, now time.Time) bool {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	t, ok := pl.last[token]
	if ok && now.Sub(t) < time.Second {
		return false
	}
	pl.last[token] = now
	return true
}
