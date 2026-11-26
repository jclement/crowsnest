package hub

import (
	"sync"
	"testing"
)

func TestPublishReachesEverySubscriber(t *testing.T) {
	h := New()
	first, second := h.Subscribe(4), h.Subscribe(4)
	defer first.Close()
	defer second.Close()

	if got := h.Publish(Event{Type: "fleet", Data: 1}); got != 2 {
		t.Fatalf("delivered to %d, want 2", got)
	}
	for _, sub := range []*Subscription{first, second} {
		event := <-sub.Events()
		if event.Type != "fleet" || event.Data != 1 {
			t.Errorf("got %+v", event)
		}
	}
}

func TestEventsArriveInOrder(t *testing.T) {
	h := New()
	sub := h.Subscribe(10)
	defer sub.Close()
	for i := range 5 {
		h.Publish(Event{Type: "n", Data: i})
	}
	for i := range 5 {
		if event := <-sub.Events(); event.Data != i {
			t.Fatalf("event %d carried %v", i, event.Data)
		}
	}
}

func TestSlowSubscriberIsClosedNotSkipped(t *testing.T) {
	h := New()
	slow := h.Subscribe(1)
	fast := h.Subscribe(10)
	defer fast.Close()

	h.Publish(Event{Type: "a"}) // fills slow's queue
	h.Publish(Event{Type: "b"}) // slow cannot take it: closed

	if h.Subscribers() != 1 {
		t.Fatalf("subscribers = %d, want only the fast one", h.Subscribers())
	}
	if event, ok := <-slow.Events(); !ok || event.Type != "a" {
		t.Fatalf("slow should still drain what it had: %+v %v", event, ok)
	}
	if _, ok := <-slow.Events(); ok {
		t.Fatal("slow subscriber should be closed after overflowing")
	}
	if len(fast.Events()) != 2 {
		t.Fatalf("fast subscriber has %d events, want 2", len(fast.Events()))
	}
	slow.Close() // closing an already-closed subscription is safe
}

func TestCloseUnsubscribes(t *testing.T) {
	h := New()
	sub := h.Subscribe(1)
	sub.Close()
	sub.Close()
	if h.Subscribers() != 0 || h.Publish(Event{Type: "x"}) != 0 {
		t.Fatal("closed subscription still receives events")
	}
	if _, ok := <-sub.Events(); ok {
		t.Fatal("channel should be closed")
	}
}

func TestHubCloseEndsAllSubscriptions(t *testing.T) {
	h := New()
	sub := h.Subscribe(1)
	h.Close()
	if _, ok := <-sub.Events(); ok {
		t.Fatal("subscription should end when the hub closes")
	}
	late := h.Subscribe(1)
	if _, ok := <-late.Events(); ok {
		t.Fatal("a subscription on a closed hub should start closed")
	}
	late.Close()
}

func TestDefaultBuffer(t *testing.T) {
	sub := New().Subscribe(0)
	defer sub.Close()
	if cap(sub.events) != DefaultBuffer {
		t.Fatalf("buffer %d, want %d", cap(sub.events), DefaultBuffer)
	}
}

// TestConcurrentUse is for the race detector: publishers, subscribers and closers at once.
func TestConcurrentUse(t *testing.T) {
	h := New()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			sub := h.Subscribe(4)
			for range 20 {
				select {
				case <-sub.Events():
				default:
				}
			}
			sub.Close()
		})
		wg.Go(func() {
			for i := range 50 {
				h.Publish(Event{Type: "tick", Data: i})
			}
		})
	}
	wg.Wait()
	h.Close()
}
