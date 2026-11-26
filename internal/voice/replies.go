// The reply watcher: when the orchestrator finishes a turn, its final assistant message is read
// from the conversation and its SAY line (if any) spoken. "Finished a turn" is the orchestrator
// going idle; because the transcript can land a moment after herdr reports idle, a conversation
// change while idle is checked too, for a short window after the turn ended.

package voice

import (
	"context"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/transcript"
)

const (
	// replyWindow is how long after going idle a late transcript append still counts as the
	// turn's final reply.
	replyWindow = 20 * time.Second
	// replyPage is how many of the newest messages are read to find the final one.
	replyPage = 20
	// replyCheckTimeout bounds one read of the conversation.
	replyCheckTimeout = 5 * time.Second
)

// MessageSource reads a page of an agent's conversation; *transcript.Manager is one.
type MessageSource interface {
	Messages(ctx context.Context, target, after string, limit int) (transcript.Page, error)
}

// ReplyWatcher speaks the orchestrator's replies. Feed it with Status and Changed; it is safe for
// concurrent use.
type ReplyWatcher struct {
	service *Service
	source  MessageSource
	target  string // the orchestrator's agent name
	now     func() time.Time

	mu        sync.Mutex
	started   time.Time // replies older than this are history, never spoken
	idle      bool
	idleSince time.Time
	paneID    string
	handled   map[string]bool // message ids already spoken (or deliberately not)
	checking  bool
	again     bool
}

// NewReplyWatcher watches target's conversation through source and speaks through service.
func NewReplyWatcher(service *Service, source MessageSource, target string) *ReplyWatcher {
	return &ReplyWatcher{
		service: service, source: source, target: target, now: service.now,
		started: service.now(), handled: map[string]bool{},
	}
}

// Status is told the orchestrator's state and pane each time it changes. Going idle checks for
// the turn's final reply.
func (r *ReplyWatcher) Status(ctx context.Context, idle bool, paneID string) {
	r.mu.Lock()
	becameIdle := idle && !r.idle
	r.idle, r.paneID = idle, paneID
	if becameIdle {
		r.idleSince = r.now()
	}
	r.mu.Unlock()
	if becameIdle {
		r.check(ctx)
	}
}

// Changed is told that paneID's conversation changed. While the orchestrator is newly idle, that
// is the final reply arriving.
func (r *ReplyWatcher) Changed(ctx context.Context, paneID string) {
	r.mu.Lock()
	relevant := r.idle && paneID == r.paneID && r.now().Sub(r.idleSince) <= replyWindow
	r.mu.Unlock()
	if relevant {
		r.check(ctx)
	}
}

// check reads the newest messages and speaks the final reply if it is new. Overlapping calls
// collapse into one more pass, so a burst of events reads the conversation at most twice.
func (r *ReplyWatcher) check(ctx context.Context) {
	r.mu.Lock()
	if r.checking {
		r.again = true
		r.mu.Unlock()
		return
	}
	r.checking = true
	r.mu.Unlock()
	for {
		r.checkOnce(ctx)
		r.mu.Lock()
		if !r.again {
			r.checking = false
			r.mu.Unlock()
			return
		}
		r.again = false
		r.mu.Unlock()
	}
}

func (r *ReplyWatcher) checkOnce(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, replyCheckTimeout)
	defer cancel()
	page, err := r.source.Messages(ctx, r.target, "", replyPage)
	if err != nil || page.Source != "transcript" {
		return // the pane-text fallback has no reply structure to read
	}
	final, ok := finalReply(page.Messages)
	if !ok {
		return
	}
	r.mu.Lock()
	if r.handled[final.ID] || final.At.Before(r.started) {
		r.mu.Unlock()
		return
	}
	if len(r.handled) > 512 {
		// Only the newest reply can be final again; older ids will never come back round.
		r.handled = map[string]bool{}
	}
	r.handled[final.ID] = true
	paneID := r.paneID
	r.mu.Unlock()

	result := ParseSay(final.Text)
	switch result.Kind {
	case SaySpeak:
		r.service.Say(result.Sentence, SourceReply, paneID)
	case SayNone:
		r.service.Chime(paneID)
	case SaySilent:
	}
}

// finalReply is the last message of the conversation when it is the assistant's text: a turn
// that ended on a tool call or a user message has no reply to speak. Thinking is skipped.
func finalReply(messages []transcript.Message) (transcript.Message, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		switch messages[i].Kind {
		case transcript.KindThinking:
			continue
		case transcript.KindAssistant:
			return messages[i], true
		default:
			return transcript.Message{}, false
		}
	}
	return transcript.Message{}, false
}
