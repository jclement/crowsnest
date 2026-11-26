package ratelimit

import (
	"testing"
	"time"
)

func TestBurstThenRefill(t *testing.T) {
	now := time.Unix(1000, 0)
	limiter := New(3, time.Minute, func() time.Time { return now })
	for i := range 3 {
		if ok, _ := limiter.Allow("w1:p1"); !ok {
			t.Fatalf("request %d of the burst refused", i+1)
		}
	}
	ok, wait := limiter.Allow("w1:p1")
	if ok || wait <= 0 || wait > time.Minute {
		t.Fatalf("past the burst: ok %v wait %v", ok, wait)
	}
	if ok, _ := limiter.Allow("w2:p1"); !ok {
		t.Fatal("another key shares the first key's bucket")
	}
	now = now.Add(time.Minute)
	if ok, _ := limiter.Allow("w1:p1"); !ok {
		t.Fatal("a token was not refilled after the interval")
	}
	if ok, _ := limiter.Allow("w1:p1"); ok {
		t.Fatal("one interval refilled more than one token")
	}
}

func TestPruneForgetsFullBuckets(t *testing.T) {
	now := time.Unix(1000, 0)
	limiter := New(1, time.Second, func() time.Time { return now })
	for i := range pruneEvery - 1 {
		limiter.Allow(string(rune('a' + i%26)))
	}
	now = now.Add(time.Hour)
	limiter.Allow("x") // the pruneEvery-th call sweeps the 26 full buckets, then adds x
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if len(limiter.buckets) != 1 {
		t.Fatalf("%d buckets kept", len(limiter.buckets))
	}
}
