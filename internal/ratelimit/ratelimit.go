// Package ratelimit is a small keyed token bucket for the agent side: how many questions one
// agent may ask (R1b N6), how many phone notices one agent may trigger (R1b N5). Each key gets a
// burst, refilled one token per interval. It is in memory: a restart refills every bucket, which
// is fine for limits whose job is to stop a loop, not to meter use.

package ratelimit

import (
	"sync"
	"time"
)

// pruneEvery is how many Allow calls pass between sweeps of buckets that have refilled
// completely, so keys that come and go (panes) do not accumulate.
const pruneEvery = 256

// Limiter hands out tokens per key. Build with New; the zero value is not usable.
type Limiter struct {
	burst int
	every time.Duration
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
	calls   int
}

type bucket struct {
	tokens float64
	at     time.Time
}

// New returns a limiter that allows burst requests per key at once and one more every interval.
// now nil means time.Now.
func New(burst int, every time.Duration, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{burst: burst, every: every, now: now, buckets: map[string]*bucket{}}
}

// Allow takes a token for key. When none is left it reports false and how long until one is.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.calls++; l.calls%pruneEvery == 0 {
		l.pruneLocked(now)
	}
	current, ok := l.buckets[key]
	if !ok {
		current = &bucket{tokens: float64(l.burst), at: now}
		l.buckets[key] = current
	}
	current.tokens = l.refilled(current, now)
	current.at = now
	if current.tokens >= 1 {
		current.tokens--
		return true, 0
	}
	return false, time.Duration((1 - current.tokens) * float64(l.every))
}

func (l *Limiter) refilled(b *bucket, now time.Time) float64 {
	return min(float64(l.burst), b.tokens+float64(now.Sub(b.at))/float64(l.every))
}

func (l *Limiter) pruneLocked(now time.Time) {
	for key, b := range l.buckets {
		if l.refilled(b, now) >= float64(l.burst) {
			delete(l.buckets, key)
		}
	}
}
