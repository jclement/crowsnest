package auth

import (
	"fmt"
	"testing"
	"time"
)

func TestPerClientLimit(t *testing.T) {
	c := newClock()
	limiter := NewLimiter(c.Now)
	for i := range PerClientFailures {
		if _, ok := limiter.Allow("a"); !ok {
			t.Fatalf("attempt %d refused too early", i+1)
		}
		limiter.RecordFailure("a")
		c.Advance(time.Second)
	}
	retry, ok := limiter.Allow("a")
	if ok {
		t.Fatal("sixth attempt within a minute allowed")
	}
	// The first failure was 5s ago, so it leaves the window in 55s.
	if retry != 55*time.Second {
		t.Fatalf("retry after %v, want 55s", retry)
	}
	c.Advance(55 * time.Second)
	if _, ok := limiter.Allow("a"); !ok {
		t.Fatal("should be allowed once the oldest failure ages out")
	}
}

func TestGlobalLimit(t *testing.T) {
	c := newClock()
	limiter := NewLimiter(c.Now)
	for i := range GlobalFailures {
		ip := fmt.Sprintf("10.0.0.%d", i) // a different IP each time: per-client never trips
		limiter.RecordFailure(ip)
	}
	retry, ok := limiter.Allow("192.168.9.9")
	if ok || retry != time.Minute {
		t.Fatalf("global cap: ok=%v retry=%v", ok, retry)
	}
}

func TestResetClearsOnlyThatClient(t *testing.T) {
	limiter := NewLimiter(newClock().Now)
	for range PerClientFailures {
		limiter.RecordFailure("a")
		limiter.RecordFailure("b")
	}
	limiter.Reset("a")
	if _, ok := limiter.Allow("a"); !ok {
		t.Error("reset client still locked")
	}
	if _, ok := limiter.Allow("b"); ok {
		t.Error("other client was reset too")
	}
}

func TestRetryAfterIsAtLeastOneSecond(t *testing.T) {
	c := newClock()
	limiter := NewLimiter(c.Now)
	for range PerClientFailures {
		limiter.RecordFailure("a")
	}
	c.Advance(FailureWindow - 100*time.Millisecond)
	if retry, ok := limiter.Allow("a"); ok || retry != time.Second {
		t.Fatalf("ok=%v retry=%v, want a 1s floor", ok, retry)
	}
}

func TestPruneBoundsMemory(t *testing.T) {
	c := newClock()
	limiter := NewLimiter(c.Now)
	for i := range 100 {
		limiter.RecordFailure(fmt.Sprintf("ip%d", i))
	}
	c.Advance(2 * FailureWindow)
	limiter.Allow("x")
	if len(limiter.perClient) != 0 || len(limiter.global) != 0 {
		t.Fatalf("stale failures kept: %d clients, %d global", len(limiter.perClient), len(limiter.global))
	}
}
