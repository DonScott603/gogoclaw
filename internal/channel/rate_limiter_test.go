package channel

import (
	"testing"
	"time"
)

func TestRateLimiterAllowsUnderLimit(t *testing.T) {
	rl := newRateLimiter(10) // 10 per minute

	for i := 0; i < 10; i++ {
		wait := rl.allow("key1")
		if wait > 0 {
			t.Fatalf("request %d should be allowed, got wait=%v", i, wait)
		}
	}
}

func TestRateLimiterBlocksOverLimit(t *testing.T) {
	rl := newRateLimiter(5) // 5 per minute

	// Exhaust all tokens.
	for i := 0; i < 5; i++ {
		rl.allow("key1")
	}

	// Next request should be blocked.
	wait := rl.allow("key1")
	if wait <= 0 {
		t.Fatal("request over limit should be blocked")
	}
}

func TestRateLimiterRefill(t *testing.T) {
	rl := newRateLimiter(60) // 60 per minute = 1 per second

	// Use all tokens.
	for i := 0; i < 60; i++ {
		rl.allow("key1")
	}

	// Should be blocked now.
	wait := rl.allow("key1")
	if wait <= 0 {
		t.Fatal("should be blocked after exhausting tokens")
	}

	// Manually advance the last time to simulate time passing.
	rl.mu.Lock()
	b := rl.buckets["key1"]
	b.lastTime = b.lastTime.Add(-2 * time.Second) // simulate 2 seconds passing
	rl.mu.Unlock()

	// Should now have ~2 tokens refilled.
	wait = rl.allow("key1")
	if wait > 0 {
		t.Fatalf("should be allowed after refill, got wait=%v", wait)
	}
}

func TestRateLimiterSeparateKeys(t *testing.T) {
	rl := newRateLimiter(2) // 2 per minute

	// Exhaust key1.
	rl.allow("key1")
	rl.allow("key1")
	wait := rl.allow("key1")
	if wait <= 0 {
		t.Fatal("key1 should be blocked")
	}

	// key2 should still be allowed.
	wait = rl.allow("key2")
	if wait > 0 {
		t.Fatal("key2 should not be blocked")
	}
}
