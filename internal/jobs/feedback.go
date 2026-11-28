// Feedback waiting to be typed to a job's agent (DESIGN.md 6.20, R9-C3, C4, C14).
//
// Three things reach a job agent as a message crowsnest types for it, not as the human typing it
// now: a failed check's findings, a review's requested changes, and the human's review comments
// sent while the job may not continue (paused, held, under the usage limit, or blocked on a wait).
// Each is a batch in the job's feedback queue (jobs.feedback_json), and a batch never changes once
// it is queued:
//
//   - a new check or review finding replaces a finding not yet typed (the newer result is the one
//     the agent must act on); comments are always a new batch behind what is queued, never merged
//     into one being typed;
//   - deliverFeedback claims the first pending batch (state sending, on the row, with the claim)
//     and settles exactly that batch by its id when the typing ends: removed when it was (or may
//     have been) typed, put back when provably nothing was typed;
//   - a claim a dead crowsnest left is settled the same way, for the batch marked sending only:
//     findings are not typed again (they stay readable in job_get), comments go back to the human
//     as notes that may or may not have arrived, and every other batch stays queued;
//   - a batch of comments keeps the human's draft protection (Force is what the send asked for),
//     so a deferred send never types over a draft in the agent's input box.
//
// Comments carry Ref, the comment send (internal/changes) that holds their notes: the notes are
// marked sent only when their batch was delivered (FeedbackSettled), and handed back otherwise.

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// Feedback kinds.
const (
	FeedbackCheck    = "check"    // a failed check's findings (accept.go)
	FeedbackReview   = "review"   // a review round's requested changes (review.go)
	FeedbackComments = "comments" // the human's review comments from the diff (comments.go)
)

// Feedback batch states.
const (
	feedbackPending = "pending" // waiting for a delivery to claim it
	feedbackSending = "sending" // the one batch a delivery has claimed and is typing
)

// FeedbackOutcome is what became of a batch of comments, for the send that owns their notes.
type FeedbackOutcome string

// Feedback outcomes.
const (
	FeedbackDelivered FeedbackOutcome = "delivered" // typed (or queued by the agent behind its turn)
	FeedbackUncertain FeedbackOutcome = "uncertain" // may or may not have arrived: due again, the human decides
	FeedbackReturned  FeedbackOutcome = "returned"  // never typed (the job finished first): due again
)

// FeedbackSettled is told what became of a batch of comments (Ref is the send's claim). It must
// not block, and it is called with no jobs lock held.
type FeedbackSettled func(ctx context.Context, ref string, outcome FeedbackOutcome, why string)

// feedbackBatch is one message waiting for the agent. It is immutable once queued; only State
// changes (pending ↔ sending).
type feedbackBatch struct {
	ID    int64     `json:"id"`
	Kind  string    `json:"kind"`
	Text  string    `json:"text"`
	Count int       `json:"count,omitempty"` // comments in it
	Ref   string    `json:"ref,omitempty"`   // the comment send that owns the notes
	Force bool      `json:"force,omitempty"` // may type over a draft in the input box
	State string    `json:"state"`
	Since time.Time `json:"since"`
}

// feedbackQueue is a job's waiting messages, oldest first. Seq numbers the batches.
type feedbackQueue struct {
	Seq     int64           `json:"seq"`
	Batches []feedbackBatch `json:"batches"`
}

// feedbackOf decodes r's queue (an unreadable one is empty: it was written by this package).
func feedbackOf(r store.JobRecord) feedbackQueue {
	var q feedbackQueue
	if strings.TrimSpace(r.FeedbackJSON) != "" {
		_ = json.Unmarshal([]byte(r.FeedbackJSON), &q)
	}
	return q
}

// setFeedback stores q on r.
func setFeedback(r *store.JobRecord, q feedbackQueue) {
	if len(q.Batches) == 0 {
		r.FeedbackJSON = ""
		if q.Seq > 0 {
			body, _ := json.Marshal(feedbackQueue{Seq: q.Seq, Batches: []feedbackBatch{}})
			r.FeedbackJSON = string(body)
		}
		return
	}
	body, _ := json.Marshal(q)
	r.FeedbackJSON = string(body)
}

// hasFeedback reports whether anything waits to be typed to r's agent.
func hasFeedback(r store.JobRecord) bool { return len(feedbackOf(r).Batches) > 0 }

// queueFinding queues a check's or a review's findings on r. A finding of either kind not yet
// claimed is replaced: the newest result is what the agent must answer. One being typed is not
// touched (it settles by its own id).
func queueFinding(r *store.JobRecord, kind, text string, now time.Time) {
	q := feedbackOf(*r)
	kept := q.Batches[:0]
	for _, batch := range q.Batches {
		if batch.State == feedbackPending && (batch.Kind == FeedbackCheck || batch.Kind == FeedbackReview) {
			continue
		}
		kept = append(kept, batch)
	}
	q.Batches = kept
	q.Seq++
	q.Batches = append(q.Batches, feedbackBatch{ID: q.Seq, Kind: kind, Text: text, State: feedbackPending, Since: now})
	setFeedback(r, q)
}

// queueComments adds the human's comments as a new batch behind everything queued.
func queueComments(r *store.JobRecord, text string, count int, ref string, force bool, now time.Time) int64 {
	q := feedbackOf(*r)
	q.Seq++
	q.Batches = append(q.Batches, feedbackBatch{ID: q.Seq, Kind: FeedbackComments, Text: text, Count: count, Ref: ref, Force: force,
		State: feedbackPending, Since: now})
	setFeedback(r, q)
	return q.Seq
}

// dropFindings removes the pending findings (the agent called done again: they are answered).
// Comments stay: the human's notes are not answered by a done.
func dropFindings(r *store.JobRecord) {
	q := feedbackOf(*r)
	kept := q.Batches[:0]
	for _, batch := range q.Batches {
		if batch.State == feedbackPending && batch.Kind != FeedbackComments {
			continue
		}
		kept = append(kept, batch)
	}
	q.Batches = kept
	setFeedback(r, q)
}

// nextFeedback is the first pending batch, if no batch is being typed.
func nextFeedback(r store.JobRecord) (feedbackBatch, bool) {
	for _, batch := range feedbackOf(r).Batches {
		if batch.State == feedbackSending {
			return feedbackBatch{}, false
		}
		if batch.State == feedbackPending {
			return batch, true
		}
	}
	return feedbackBatch{}, false
}

// markFeedback sets batch id's state; it reports whether the batch is still queued.
func markFeedback(r *store.JobRecord, id int64, state string) bool {
	q := feedbackOf(*r)
	for i := range q.Batches {
		if q.Batches[i].ID == id {
			q.Batches[i].State = state
			setFeedback(r, q)
			return true
		}
	}
	return false
}

// takeFeedback removes batch id from r's queue and returns it.
func takeFeedback(r *store.JobRecord, id int64) (feedbackBatch, bool) {
	q := feedbackOf(*r)
	for i, batch := range q.Batches {
		if batch.ID == id {
			q.Batches = append(q.Batches[:i], q.Batches[i+1:]...)
			setFeedback(r, q)
			return batch, true
		}
	}
	return feedbackBatch{}, false
}

// sendingFeedback is the batch a delivery claimed, if any.
func sendingFeedback(r store.JobRecord) (feedbackBatch, bool) {
	for _, batch := range feedbackOf(r).Batches {
		if batch.State == feedbackSending {
			return batch, true
		}
	}
	return feedbackBatch{}, false
}

// settleComments records, with the change, what became of a batch of comments, for the send that
// owns their notes (told after the change is saved, with no lock held).
func (c *change) settleComments(batch feedbackBatch, outcome FeedbackOutcome, why string) {
	if batch.Kind != FeedbackComments || batch.Ref == "" {
		return
	}
	c.settled = append(c.settled, settledFeedback{ref: batch.Ref, outcome: outcome, why: why})
}

// returnAllComments hands every queued batch of comments back to its send (the job finished, or
// was cancelled, before they could be typed) and clears the queue.
func returnAllComments(r *store.JobRecord, c *change, why string) {
	q := feedbackOf(*r)
	if len(q.Batches) == 0 {
		return
	}
	for _, batch := range q.Batches {
		outcome := FeedbackReturned
		if batch.State == feedbackSending {
			outcome = FeedbackUncertain
		}
		c.settleComments(batch, outcome, why)
	}
	q.Batches = nil
	setFeedback(r, q)
}

// settledFeedback is one outcome waiting to be told to FeedbackSettled.
type settledFeedback struct {
	ref     string
	outcome FeedbackOutcome
	why     string
}

// SetFeedbackSettled installs the hook told what became of each batch of comments (serve wires
// internal/changes' claim settlement). Nil removes it.
func (s *Service) SetFeedbackSettled(fn FeedbackSettled) {
	if fn == nil {
		s.feedbackSettled.Store(nil)
		return
	}
	s.feedbackSettled.Store(&fn)
}

// tellSettled passes outcomes to the hook. s.mu must not be held.
func (s *Service) tellSettled(ctx context.Context, settled []settledFeedback) {
	if len(settled) == 0 {
		return
	}
	hook := s.feedbackSettled.Load()
	if hook == nil || *hook == nil {
		return
	}
	for _, one := range settled {
		(*hook)(context.WithoutCancel(ctx), one.ref, one.outcome, one.why)
	}
}

// FeedbackRefLive reports whether a comment send's notes are still held by a batch queued on job
// id (internal/changes asks before releasing an old claim as uncertain).
func (s *Service) FeedbackRefLive(ctx context.Context, id, ref string) (bool, error) {
	numeric, err := ParseID(id)
	if err != nil {
		return false, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.opts.Store.GetJob(ctx, numeric)
	if err != nil {
		return false, err
	}
	for _, batch := range feedbackOf(r).Batches {
		if batch.Ref == ref {
			return true, nil
		}
	}
	return false, nil
}

// WaitingFeedback is one batch as the API shows it on a job.
type WaitingFeedback struct {
	Kind    string    `json:"kind"`
	Count   int       `json:"count,omitempty"`
	Sending bool      `json:"sending"`
	Since   time.Time `json:"since"`
}

// waitingFeedbackOf summarises r's queue for the API.
func waitingFeedbackOf(r store.JobRecord) []WaitingFeedback {
	q := feedbackOf(r)
	if len(q.Batches) == 0 {
		return nil
	}
	out := make([]WaitingFeedback, 0, len(q.Batches))
	for _, batch := range q.Batches {
		out = append(out, WaitingFeedback{Kind: batch.Kind, Count: batch.Count, Sending: batch.State == feedbackSending, Since: batch.Since})
	}
	return out
}

// ForceWaitingComments is the human's Send anyway for comments already waiting on job id: every
// pending batch of comments may now type over a draft in the agent's input box (R9-C14). The text
// of a batch never changes; only this policy does, and only by the human's say-so. It returns how
// many comments that covers, and tries a delivery at once.
func (s *Service) ForceWaitingComments(ctx context.Context, id string) (int, error) {
	count := 0
	_, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		q := feedbackOf(*r)
		for i := range q.Batches {
			if q.Batches[i].Kind == FeedbackComments && q.Batches[i].State == feedbackPending && !q.Batches[i].Force {
				q.Batches[i].Force = true
				count += q.Batches[i].Count
			}
		}
		if count == 0 {
			return errSkip
		}
		setFeedback(r, q)
		c.event(EventReview, "You said the waiting review comments may be typed over a draft in the agent's input box", nil)
		return nil
	})
	if errors.Is(err, errSkip) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	s.deliverFeedback(ctx, id)
	return count, nil
}

// ErrMaybeTyped means a message was handed to herdr and its answer lost: it may or may not have
// been typed. The caller must not treat it as delivered or as provably not sent.
var ErrMaybeTyped = errors.New("crowsnest lost track of typing the message; it may or may not have arrived")
