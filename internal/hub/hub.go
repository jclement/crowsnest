// Package hub is crowsnest's in-process pub/sub: producers (fleet, orchestrator, questions)
// publish events, and each SSE connection subscribes and forwards them to one browser.
//
// Delivery is best effort with one firm rule: a subscriber that cannot keep up is closed, not
// silently skipped. The browser refetches all state when its event stream reconnects (DESIGN.md
// section 9), so a dropped connection costs one refetch, while a silently dropped event would
// leave the UI quietly wrong.
package hub

import (
	"sync"
)

// DefaultBuffer is a subscriber's queue length. Events are small invalidations, so a burst of
// this many before the SSE writer drains them means the client is stuck, not busy.
const DefaultBuffer = 64

// Event is one message to the browser. Type becomes the SSE event name; Data is JSON-encoded.
type Event struct {
	Type string
	Data any
}

// Hub fans events out to subscribers. The zero value is not usable; call New.
type Hub struct {
	mu          sync.Mutex
	subscribers map[*Subscription]struct{}
	closed      bool
}

// Subscription is one subscriber's queue. Read Events until it is closed.
type Subscription struct {
	events chan Event
	hub    *Hub
	once   sync.Once
}

// New returns an empty hub.
func New() *Hub {
	return &Hub{subscribers: map[*Subscription]struct{}{}}
}

// Subscribe registers a subscriber with a queue of buffer events (DefaultBuffer if buffer < 1).
// The caller must call Close when done. On a closed hub the subscription starts closed.
func (h *Hub) Subscribe(buffer int) *Subscription {
	if buffer < 1 {
		buffer = DefaultBuffer
	}
	sub := &Subscription{events: make(chan Event, buffer), hub: h}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		sub.closeChannel()
		return sub
	}
	h.subscribers[sub] = struct{}{}
	return sub
}

// Publish delivers event to every subscriber without blocking. A subscriber whose queue is full
// is closed and removed (see the package comment). It returns how many subscribers received it.
func (h *Hub) Publish(event Event) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	delivered := 0
	for sub := range h.subscribers {
		select {
		case sub.events <- event:
			delivered++
		default:
			delete(h.subscribers, sub)
			sub.closeChannel()
		}
	}
	return delivered
}

// Subscribers is the number of live subscriptions.
func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subscribers)
}

// Close closes every subscription and makes later subscriptions start closed. Used at shutdown
// so SSE handlers return promptly.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for sub := range h.subscribers {
		delete(h.subscribers, sub)
		sub.closeChannel()
	}
}

// Events is the subscriber's queue. It is closed when the subscription ends for any reason.
func (s *Subscription) Events() <-chan Event { return s.events }

// Close ends the subscription. Safe to call more than once and after the hub closed it.
func (s *Subscription) Close() {
	s.hub.mu.Lock()
	delete(s.hub.subscribers, s)
	s.hub.mu.Unlock()
	s.closeChannel()
}

// closeChannel closes the queue exactly once. Callers that touch the subscriber map hold the
// hub lock; the channel itself only ever closes here.
func (s *Subscription) closeChannel() {
	s.once.Do(func() { close(s.events) })
}
