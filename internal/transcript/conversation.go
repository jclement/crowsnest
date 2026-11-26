// Conversation: the ordered messages of one transcript, and paging over them by cursor.
//
// A cursor is "<generation>.<seq>". The generation changes whenever the conversation starts
// over (a new session id, a truncated or replaced file, the switch to or from the pane-text
// fallback), so a client holding a cursor from before is told to reset rather than being handed
// a confusing diff. Within a generation, seq only grows.

package transcript

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Sources of a conversation.
const (
	SourceTranscript = "transcript" // Claude's JSONL transcript
	SourcePane       = "pane"       // no transcript: the pane's recent text as one message
)

// DefaultPageLimit and MaxPageLimit bound one page of messages.
const (
	DefaultPageLimit = 200
	MaxPageLimit     = 1000
)

// generations numbers every conversation reset in the process, so two conversations never hand
// out the same cursor.
var generations struct {
	sync.Mutex
	next uint64
}

func nextGeneration() uint64 {
	generations.Lock()
	defer generations.Unlock()
	generations.next++
	return generations.next
}

// Conversation is a thread-safe list of messages with revision cursors.
type Conversation struct {
	mu        sync.Mutex
	gen       uint64
	seq       uint64
	messages  []Message
	byID      map[string]int
	source    string
	sessionID string
}

// NewConversation returns an empty conversation from source for a session.
func NewConversation(source, sessionID string) *Conversation {
	return &Conversation{gen: nextGeneration(), byID: map[string]int{}, source: source, sessionID: sessionID}
}

// Reset empties the conversation and starts a new generation.
func (c *Conversation) Reset(source, sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen, c.seq, c.messages, c.byID = nextGeneration(), 0, nil, map[string]int{}
	c.source, c.sessionID = source, sessionID
}

// Apply adds new messages and replaces changed ones (matched by ID), assigning Index and Seq.
// It reports whether anything changed; an upsert identical to what is stored is not a change.
func (c *Conversation) Apply(upserts []Message) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	changed := false
	for _, msg := range upserts {
		if at, ok := c.byID[msg.ID]; ok {
			existing := c.messages[at]
			msg.Index, msg.Seq = existing.Index, existing.Seq
			if sameMessage(existing, msg) {
				continue
			}
			c.seq++
			msg.Seq = c.seq
			c.messages[at] = msg.clone()
			changed = true
			continue
		}
		c.seq++
		msg.Index, msg.Seq = len(c.messages), c.seq
		c.byID[msg.ID] = len(c.messages)
		c.messages = append(c.messages, msg.clone())
		changed = true
	}
	return changed
}

func sameMessage(a, b Message) bool {
	if a.Kind != b.Kind || a.Tag != b.Tag || a.Text != b.Text || (a.Tool == nil) != (b.Tool == nil) {
		return false
	}
	return a.Tool == nil || *a.Tool == *b.Tool
}

// Len is the number of messages.
func (c *Conversation) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.messages)
}

// Cursor is the cursor that covers everything currently in the conversation.
func (c *Conversation) Cursor() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return formatCursor(c.gen, c.seq)
}

// Page is one response of the messages API.
type Page struct {
	Source    string `json:"source"`     // transcript or pane
	SessionID string `json:"session_id"` // Claude's session id, when known
	// Cursor covers everything up to and including this page; pass it as `after` next time.
	Cursor string `json:"cursor"`
	// Reset is true when Messages is the conversation from the start (or its newest messages,
	// see Earlier) rather than changes since `after`: drop what you had and use this.
	Reset bool `json:"reset"`
	// Earlier is true on a reset page that left older messages out.
	Earlier bool `json:"earlier"`
	// More is true when there are further changes after Cursor: ask again straight away.
	More     bool      `json:"more"`
	Messages []Message `json:"messages"`
}

// Page returns the messages changed after cursor, at most limit of them, in seq order. An empty,
// malformed or stale cursor (from another generation, or ahead of this one) gets a reset page:
// the newest limit messages in index order.
func (c *Conversation) Page(after string, limit int) Page {
	if limit <= 0 {
		limit = DefaultPageLimit
	}
	limit = min(limit, MaxPageLimit)
	c.mu.Lock()
	defer c.mu.Unlock()
	page := Page{Source: c.source, SessionID: c.sessionID, Messages: []Message{}}
	gen, seq, ok := parseCursor(after)
	if !ok || gen != c.gen || seq > c.seq {
		start := max(0, len(c.messages)-limit)
		for _, msg := range c.messages[start:] {
			page.Messages = append(page.Messages, msg.clone())
		}
		page.Reset, page.Earlier, page.Cursor = true, start > 0, formatCursor(c.gen, c.seq)
		return page
	}
	var changed []Message
	for _, msg := range c.messages {
		if msg.Seq > seq {
			changed = append(changed, msg.clone())
		}
	}
	sort.Slice(changed, func(i, j int) bool { return changed[i].Seq < changed[j].Seq })
	page.Cursor = formatCursor(c.gen, c.seq)
	if len(changed) > limit {
		changed, page.More = changed[:limit], true
		page.Cursor = formatCursor(c.gen, changed[len(changed)-1].Seq)
	}
	page.Messages = append(page.Messages, changed...)
	return page
}

func formatCursor(gen, seq uint64) string { return fmt.Sprintf("%d.%d", gen, seq) }

func parseCursor(cursor string) (gen, seq uint64, ok bool) {
	genText, seqText, found := strings.Cut(cursor, ".")
	if !found {
		return 0, 0, false
	}
	gen, errGen := strconv.ParseUint(genText, 10, 64)
	seq, errSeq := strconv.ParseUint(seqText, 10, 64)
	return gen, seq, errGen == nil && errSeq == nil
}
