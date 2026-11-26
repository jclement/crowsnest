// The speakable texts: a small server-side map from an unguessable id to a sentence an agent
// said (a `say`, a parsed `SAY:` line) or a question the human asked to hear. GET /voice/tts
// takes only an id from this map, so a browser can make crowsnest speak what agents said and
// nothing else: nobody can spend the ElevenLabs credit on arbitrary text through it.

package voice

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

const (
	// maxTexts bounds the map; the oldest entry goes first. Talk mode speaks a few sentences a
	// minute, so this is hours of history.
	maxTexts = 128
	// textTTL is how long a text can be fetched. A browser that was asleep longer has missed it.
	textTTL = time.Hour
	// maxCachedAudio caps the audio kept per text, so a replay (a second browser, the Replay
	// button) does not pay ElevenLabs twice. One sentence of mp3 is tens of kilobytes.
	maxCachedAudio = 1 << 20
)

// Text is one speakable entry.
type Text struct {
	ID        string
	Text      string
	CreatedAt time.Time
	// audio is the finished mp3 of a previous fetch, when it fit under maxCachedAudio.
	audio       []byte
	contentType string
}

// Texts is the id → text map. The zero value is not usable; call NewTexts.
type Texts struct {
	mu    sync.Mutex
	byID  map[string]*Text
	order []string // ids, oldest first
	now   func() time.Time
}

// NewTexts returns an empty map. now may be nil (time.Now).
func NewTexts(now func() time.Time) *Texts {
	if now == nil {
		now = time.Now
	}
	return &Texts{byID: map[string]*Text{}, now: now}
}

// Add stores text and returns its new id ("t_" and 24 hex characters of crypto randomness).
func (t *Texts) Add(text string) string {
	raw := make([]byte, 12)
	_, _ = rand.Read(raw) // crypto/rand.Read never fails on supported platforms
	id := "t_" + hex.EncodeToString(raw)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.byID[id] = &Text{ID: id, Text: text, CreatedAt: t.now()}
	t.order = append(t.order, id)
	for len(t.order) > maxTexts {
		delete(t.byID, t.order[0])
		t.order = t.order[1:]
	}
	return id
}

// Get returns the text with id, or false when it is unknown or expired.
func (t *Texts) Get(id string) (Text, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.byID[id]
	if !ok || t.now().Sub(entry.CreatedAt) > textTTL {
		return Text{}, false
	}
	return *entry, true
}

// cachedAudio returns the stored audio for id, if a previous fetch completed.
func (t *Texts) cachedAudio(id string) ([]byte, string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.byID[id]
	if !ok || entry.audio == nil {
		return nil, "", false
	}
	return entry.audio, entry.contentType, true
}

// storeAudio keeps a completed fetch's audio for replays.
func (t *Texts) storeAudio(id string, audio []byte, contentType string) {
	if len(audio) == 0 || len(audio) > maxCachedAudio {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if entry, ok := t.byID[id]; ok {
		entry.audio, entry.contentType = audio, contentType
	}
}
