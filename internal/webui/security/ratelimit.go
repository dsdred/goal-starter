package security

import (
	"sync"
	"time"
)

const rateLimiterCleanupInterval = 5 * time.Minute

// RateLimiter is a fixed-window per-key request limiter.
// It is designed to bound brute-force attempts on the login endpoint:
// each key (client address) gets at most Limit requests per Window.
type RateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	now     func() time.Time
	buckets map[string]*rateBucket
}

type rateBucket struct {
	windowStart time.Time
	count       int
}

// NewRateLimiter creates a limiter allowing up to limit requests per window
// per key and starts a background cleanup of expired buckets.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		limit:   limit,
		window:  window,
		now:     time.Now,
		buckets: make(map[string]*rateBucket),
	}
	go rl.cleanupLoop()
	return rl
}

// Allow reports whether the request for key may proceed within the current window.
// Allowed requests consume one slot; rejected requests do not extend the window.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	b, ok := rl.buckets[key]
	if !ok || now.Sub(b.windowStart) >= rl.window {
		rl.buckets[key] = &rateBucket{windowStart: now, count: 1}
		return true
	}
	if b.count >= rl.limit {
		return false
	}
	b.count++
	return true
}

// cleanupLoop removes expired buckets periodically to bound memory.
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rateLimiterCleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		rl.cleanup()
	}
}

// cleanup removes all buckets whose window has fully elapsed.
func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	for key, b := range rl.buckets {
		if now.Sub(b.windowStart) >= rl.window {
			delete(rl.buckets, key)
		}
	}
}

// Len returns the number of tracked keys (for tests and diagnostics).
func (rl *RateLimiter) Len() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.buckets)
}
