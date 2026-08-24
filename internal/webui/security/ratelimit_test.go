package security

import (
	"testing"
	"time"
)

func newTestRateLimiter(limit int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		limit:   limit,
		window:  window,
		now:     time.Now,
		buckets: make(map[string]*rateBucket),
	}
	return rl
}

func TestRateLimiterAllowsUpToLimit(t *testing.T) {
	rl := newTestRateLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !rl.Allow("10.0.0.1") {
			t.Fatalf("request %d: expected allow, got deny", i+1)
		}
	}
	if rl.Allow("10.0.0.1") {
		t.Fatal("request 4: expected deny, got allow")
	}
}

func TestRateLimiterDeniedRequestsDoNotExtendWindow(t *testing.T) {
	window := time.Minute
	rl := newTestRateLimiter(2, window)
	start := time.Now()
	rl.now = func() time.Time { return start }
	if !rl.Allow("ip") || !rl.Allow("ip") {
		t.Fatal("first two requests should be allowed")
	}
	rl.now = func() time.Time { return start.Add(30 * time.Second) }
	if rl.Allow("ip") {
		t.Fatal("third request within window should be denied")
	}
	rl.now = func() time.Time { return start.Add(window) }
	if !rl.Allow("ip") {
		t.Fatal("request after window elapsed should be allowed")
	}
}

func TestRateLimiterPerKeyIsolation(t *testing.T) {
	rl := newTestRateLimiter(2, time.Minute)
	rl.Allow("a")
	rl.Allow("a")
	if rl.Allow("a") {
		t.Fatal("key a should be exhausted")
	}
	if !rl.Allow("b") {
		t.Fatal("key b should be unaffected by key a")
	}
}

func TestRateLimiterCleanupRemovesExpiredBuckets(t *testing.T) {
	window := time.Minute
	rl := newTestRateLimiter(1, window)
	start := time.Now()
	rl.now = func() time.Time { return start }
	rl.Allow("old")
	rl.now = func() time.Time { return start.Add(window + time.Second) }
	rl.Allow("fresh")

	rl.cleanup()

	if got := rl.Len(); got != 1 {
		t.Fatalf("Len after cleanup = %d, want 1", got)
	}
	if !rl.Allow("old") {
		t.Fatal("key old should be allowed again after its window expired")
	}
}

func TestRateLimiterConcurrentAllow(t *testing.T) {
	rl := newTestRateLimiter(1000, time.Minute)
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				rl.Allow("shared")
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if rl.Len() != 1 {
		t.Fatalf("Len = %d, want 1", rl.Len())
	}
}
