// The login rate limiter: at most 5 failed logins per minute from one client IP, and at most 20
// per minute across all clients (DESIGN.md 4.2). Past either cap every attempt, right or wrong,
// is refused with a Retry-After until the oldest failure leaves the window. The global cap is
// what stops a distributed guesser that rotates IPs; it can lock the real user out for up to a
// minute during an attack, which is the intended trade.

package auth

import (
	"sync"
	"time"
)

const (
	// PerClientFailures is how many failures one IP may make per window.
	PerClientFailures = 5
	// GlobalFailures is how many failures everyone together may make per window.
	GlobalFailures = 20
	// FailureWindow is the sliding window both caps count over.
	FailureWindow = time.Minute
)

// Limiter counts failed logins in a sliding window. Safe for concurrent use.
type Limiter struct {
	mu        sync.Mutex
	now       func() time.Time
	perClient map[string][]time.Time // failure times per IP, oldest first
	global    []time.Time            // all failure times, oldest first
}

// NewLimiter returns an empty limiter using clock now (time.Now if nil).
func NewLimiter(now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{now: now, perClient: map[string][]time.Time{}}
}

// Allow reports whether clientIP may try to log in now. When it may not, retryAfter is how long
// until it may (at least one second, as Retry-After is in whole seconds).
func (l *Limiter) Allow(clientIP string) (retryAfter time.Duration, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.allowLocked(clientIP, l.now())
}

// allowLocked is Allow with l.mu held.
func (l *Limiter) allowLocked(clientIP string, now time.Time) (retryAfter time.Duration, ok bool) {
	l.prune(now)
	var wait time.Duration
	if failures := l.perClient[clientIP]; len(failures) >= PerClientFailures {
		wait = max(wait, failures[len(failures)-PerClientFailures].Add(FailureWindow).Sub(now))
	}
	if len(l.global) >= GlobalFailures {
		wait = max(wait, l.global[len(l.global)-GlobalFailures].Add(FailureWindow).Sub(now))
	}
	if wait <= 0 {
		return 0, true
	}
	return max(wait.Round(time.Second), time.Second), false
}

// Attempt is one admitted login attempt, already counted as a failure until Succeeded says
// otherwise.
type Attempt struct {
	clientIP string
	at       time.Time
}

// Admit decides and counts in one step: when clientIP may try now, it records the attempt as a
// failure straight away and returns it; otherwise it returns how long to wait, as Allow does.
// Checking and counting separately (Allow, then RecordFailure after the credential compare) let
// every request that arrived before the first failure was recorded through, so a burst of
// parallel guesses could exceed both caps (R1-C10, M1). Counting pessimistically and taking the
// count back on success closes that window.
func (l *Limiter) Admit(clientIP string) (attempt Attempt, retryAfter time.Duration, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if retryAfter, ok = l.allowLocked(clientIP, now); !ok {
		return Attempt{}, retryAfter, false
	}
	l.perClient[clientIP] = append(l.perClient[clientIP], now)
	l.global = append(l.global, now)
	return Attempt{clientIP: clientIP, at: now}, 0, true
}

// Succeeded takes back the failure Admit counted for attempt, and forgets the client's other
// failures as Reset does.
func (l *Limiter) Succeeded(attempt Attempt) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for index := len(l.global) - 1; index >= 0; index-- {
		if l.global[index].Equal(attempt.at) {
			l.global = append(l.global[:index], l.global[index+1:]...)
			break
		}
	}
	delete(l.perClient, attempt.clientIP)
}

// RecordFailure counts one failed login from clientIP.
func (l *Limiter) RecordFailure(clientIP string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.perClient[clientIP] = append(l.perClient[clientIP], now)
	l.global = append(l.global, now)
}

// Reset forgets clientIP's failures after a successful login, so a typo or two earlier in the
// minute does not count against the next session. Global failures are kept.
func (l *Limiter) Reset(clientIP string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.perClient, clientIP)
}

// prune drops failures older than the window, and empty clients, so memory stays bounded by
// the number of IPs that failed within the last minute.
func (l *Limiter) prune(now time.Time) {
	cutoff := now.Add(-FailureWindow)
	l.global = dropBefore(l.global, cutoff)
	for ip, failures := range l.perClient {
		if failures = dropBefore(failures, cutoff); len(failures) == 0 {
			delete(l.perClient, ip)
		} else {
			l.perClient[ip] = failures
		}
	}
}

// dropBefore removes leading times at or before cutoff from an ascending slice.
func dropBefore(times []time.Time, cutoff time.Time) []time.Time {
	index := 0
	for index < len(times) && !times[index].After(cutoff) {
		index++
	}
	return times[index:]
}
