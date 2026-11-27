// The setup link's handoff (DESIGN.md 3.3). The opener is a separate program and its argv is
// visible to the machine's other processes while it runs, so it is never given the setup token.
// It gets http://127.0.0.1:<port>/setup/open/<id> instead: an id that is random, works once,
// only from a loopback peer, and only for HandoffTTL. The server answers it with a redirect to
// the relative "/setup#token=…", so the token travels only inside that one loopback response and
// the browser keeps it in the fragment, as with the printed link. A relative Location also works
// through a port-rewriting tunnel (devtun), where the browser's host:port is not crowsnest's.

package opener

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// HandoffTTL is how long a handoff id works.
const HandoffTTL = 2 * time.Minute

// Handoffs keeps the live handoff ids. The zero value is not usable; use NewHandoffs.
type Handoffs struct {
	mu     sync.Mutex
	ids    map[string]time.Time // id -> expiry
	target func() string        // the redirect target at redemption ("/setup#token=…")
	now    func() time.Time
}

// NewHandoffs returns a store whose ids redirect to target() when redeemed. now nil means
// time.Now.
func NewHandoffs(target func() string, now func() time.Time) *Handoffs {
	if now == nil {
		now = time.Now
	}
	return &Handoffs{ids: map[string]time.Time{}, target: target, now: now}
}

// New mints an id.
func (h *Handoffs) New() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	id := hex.EncodeToString(buf)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ids[id] = h.now().Add(HandoffTTL)
	return id
}

// Redeem spends id and returns where to send the browser; false when id is unknown, spent or
// expired.
func (h *Handoffs) Redeem(id string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	expiry, ok := h.ids[id]
	delete(h.ids, id)
	for other, at := range h.ids { // drop expired ones while here
		if h.now().After(at) {
			delete(h.ids, other)
		}
	}
	if !ok || h.now().After(expiry) {
		return "", false
	}
	return h.target(), true
}
