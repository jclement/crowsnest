// R1-C10 / M1: concurrent logins must not get more guesses through than the caps allow.

package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// burst runs one wrong login per entry of ips, all at once. Each admitted login waits between
// admission and the credential check until every login has either got there or been refused
// (300 ms at most), which is the interleaving that let a burst through. It returns how many
// guesses were actually checked.
func burst(t *testing.T, service *Service, ips []string) int {
	t.Helper()
	var arrived sync.WaitGroup
	arrived.Add(len(ips))
	allIn := make(chan struct{})
	go func() { arrived.Wait(); close(allIn) }()
	service.admitted = func() {
		arrived.Done()
		select {
		case <-allIn:
		case <-time.After(300 * time.Millisecond):
		}
	}
	var checked atomic.Int32
	var done sync.WaitGroup
	for index, ip := range ips {
		done.Add(1)
		go func() {
			defer done.Done()
			_, _, err := service.Login(context.Background(), ip, "jeff", fmt.Sprint("guess-", index), "", testHost)
			var limited *RateLimitedError
			if errors.As(err, &limited) {
				arrived.Done() // refused before the hook
			}
			if errors.Is(err, ErrBadCredentials) {
				checked.Add(1)
			}
		}()
	}
	done.Wait()
	return int(checked.Load())
}

func TestConcurrentLoginsRespectThePerClientCap(t *testing.T) {
	c := newClock()
	service := newService(t, newStore(t), c, "right")
	for range PerClientFailures - 1 {
		service.Limiter().RecordFailure("10.0.0.1")
	}
	ips := make([]string, 10)
	for i := range ips {
		ips[i] = "10.0.0.1"
	}
	if checked := burst(t, service, ips); checked != 1 {
		t.Fatalf("%d guesses were checked with one left under the per-client cap", checked)
	}
}

func TestConcurrentLoginsRespectTheGlobalCap(t *testing.T) {
	c := newClock()
	service := newService(t, newStore(t), c, "right")
	for i := range GlobalFailures - 1 {
		service.Limiter().RecordFailure(fmt.Sprint("10.1.0.", i))
	}
	ips := make([]string, 10)
	for i := range ips {
		ips[i] = fmt.Sprint("10.2.0.", i)
	}
	if checked := burst(t, service, ips); checked != 1 {
		t.Fatalf("%d guesses were checked with one left under the global cap", checked)
	}
}

func TestSuccessfulLoginTakesBackItsCount(t *testing.T) {
	service := newService(t, newStore(t), newClock(), "right")
	for range PerClientFailures - 1 {
		service.Limiter().RecordFailure("10.0.0.9")
	}
	if _, _, err := service.Login(context.Background(), "10.0.0.9", "jeff", "right", "", testHost); err != nil {
		t.Fatal(err)
	}
	for i := range PerClientFailures {
		if _, _, err := service.Login(context.Background(), "10.0.0.9", "jeff", "wrong", "", testHost); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("wrong login %d after a success: %v (the success should have cleared the count)", i+1, err)
		}
	}
}
