// Talk-mode presence: which browsers have talk mode switched on right now. Each browser with the
// toggle on sends a heartbeat every TalkHeartbeat; a browser that stops (closed tab, phone asleep)
// drops out after TalkExpiry. Talk mode is "on" while at least one browser is present, and that
// is what the orchestrator is told (`talk=on` in pokes and voice prompts), so it only spends
// effort on a `SAY:` line when someone can hear it.

package voice

import (
	"regexp"
	"sync"
	"time"
)

const (
	// TalkHeartbeat is how often the web app reports; it is told this value so the two agree.
	TalkHeartbeat = 15 * time.Second
	// TalkExpiry is three missed heartbeats: a backgrounded mobile tab is throttled, not dead.
	TalkExpiry = 45 * time.Second
	// maxTalkClients bounds the presence map; a client id is per browser tab.
	maxTalkClients = 32
)

// clientIDPattern is what a browser may call itself: a random id the web app makes per tab.
var clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// ValidClientID reports whether id is an acceptable talk client id.
func ValidClientID(id string) bool { return clientIDPattern.MatchString(id) }

// Talk tracks the browsers with talk mode on. The zero value is not usable; call NewTalk.
type Talk struct {
	mu       sync.Mutex
	lastBeat map[string]time.Time
	now      func() time.Time
}

// NewTalk returns an empty tracker. now may be nil (time.Now).
func NewTalk(now func() time.Time) *Talk {
	if now == nil {
		now = time.Now
	}
	return &Talk{lastBeat: map[string]time.Time{}, now: now}
}

// Beat records a heartbeat from clientID with talk mode on, or its departure when on is false,
// and returns whether talk mode is now on anywhere.
func (t *Talk) Beat(clientID string, on bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expireLocked()
	if !on {
		delete(t.lastBeat, clientID)
		return len(t.lastBeat) > 0
	}
	if _, known := t.lastBeat[clientID]; !known && len(t.lastBeat) >= maxTalkClients {
		t.dropOldestLocked()
	}
	t.lastBeat[clientID] = t.now()
	return true
}

// On reports whether at least one browser has talk mode on.
func (t *Talk) On() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expireLocked()
	return len(t.lastBeat) > 0
}

func (t *Talk) expireLocked() {
	now := t.now()
	for id, at := range t.lastBeat {
		if now.Sub(at) > TalkExpiry {
			delete(t.lastBeat, id)
		}
	}
}

func (t *Talk) dropOldestLocked() {
	oldestID, oldest := "", time.Time{}
	for id, at := range t.lastBeat {
		if oldestID == "" || at.Before(oldest) {
			oldestID, oldest = id, at
		}
	}
	delete(t.lastBeat, oldestID)
}

// TalkFlag is the `talk=on` / `talk=off` word the orchestrator reads in a poke or voice header.
func TalkFlag(on bool) string {
	if on {
		return "talk=on"
	}
	return "talk=off"
}
