// Answer receipt (DESIGN.md 6.19): an answer typed into an agent is not the same as an answer
// the agent took in. Claude Code writes what it is given into its transcript as a user message,
// so crowsnest looks there for the answer line (`[crowsnest answer q_7] …`): found, the
// question is received. This is also how an uncertain delivery is reconciled, without typing it
// twice: if the line is in the transcript, it arrived.

package jobs

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

const (
	// receiptEvery is how often one job's transcript is searched for its answers.
	receiptEvery = 15 * time.Second
	// receiptReadBytes is how much of the transcript's end is searched.
	receiptReadBytes = 2 << 20
	// settledReceiptWindow is how long after its delivery an answer to a job that has finished is
	// still looked for in its transcript.
	settledReceiptWindow = time.Hour
)

// reconcileReceipts marks answers received when their line is in the asker's transcript.
func (s *Service) reconcileReceipts(ctx context.Context, r store.JobRecord) {
	if r.Engine != EngineClaude || r.ClaudeSessionID == "" || s.opts.Launch.ClaudeConfigDir == "" {
		return
	}
	id := FormatID(r.ID)
	watch := s.watchOf(id)
	now := s.opts.Now()
	if !watch.receiptAt.IsZero() && now.Sub(watch.receiptAt) < receiptEvery {
		return
	}
	watch.receiptAt = now
	pending, err := s.opts.Store.DeliveredUnreceived(ctx, id, 20)
	if err != nil || len(pending) == 0 {
		return
	}
	path, err := transcript.Locate(s.opts.Launch.ClaudeConfigDir, workDirOf(r), r.ClaudeSessionID)
	if err != nil {
		return
	}
	s.markReceived(ctx, id, pending, readTail(path, receiptReadBytes))
}

// reconcileSettledReceipts looks for the answers of jobs that left the live pass before their
// transcript was read: an agent that takes its answer, does the work and calls done inside one
// receipt interval (seen with haiku in e2e:claude: answered, done 10 s later, and the answer
// stayed "delivered" for good). Live jobs are the live pass's.
func (s *Service) reconcileSettledReceipts(ctx context.Context) {
	ids, err := s.opts.Store.JobsWithUnreceivedAnswers(ctx, s.opts.Now().Add(-settledReceiptWindow), superviseBatch)
	if err != nil {
		return
	}
	for _, id := range ids {
		numeric, err := ParseID(id)
		if err != nil {
			continue
		}
		r, err := s.opts.Store.GetJob(ctx, numeric)
		if err != nil || slices.Contains(liveStates, State(r.State)) {
			continue
		}
		s.reconcileReceipts(ctx, r)
	}
}

// markReceived marks each pending question whose answer line is in body received.
func (s *Service) markReceived(ctx context.Context, jobID string, pending []int64, body []byte) {
	for _, questionID := range pending {
		marker := []byte(fmt.Sprintf("[crowsnest answer q_%d]", questionID))
		if !bytes.Contains(body, marker) {
			continue
		}
		if fresh, err := s.opts.Store.MarkQuestionReceived(ctx, questionID, s.opts.Now()); err == nil && fresh {
			_ = s.Journal(ctx, jobID, EventQuestion, fmt.Sprintf("q_%d's answer was received: it is in the agent's conversation", questionID), map[string]any{"question": fmt.Sprintf("q_%d", questionID)})
			if s.opts.OnReceived != nil {
				s.opts.OnReceived(questionID)
			}
		}
	}
}

// ReconcileAnswer looks for one question's answer in its asker's transcript now (the Inbox's
// "Check whether it arrived" for an uncertain delivery). It reports whether it is received.
func (s *Service) ReconcileAnswer(ctx context.Context, jobID string, questionID int64) (bool, error) {
	numeric, err := ParseID(jobID)
	if err != nil {
		return false, ErrNotFound
	}
	r, err := s.opts.Store.GetJob(ctx, numeric)
	if err != nil {
		return false, err
	}
	if r.ClaudeSessionID == "" || s.opts.Launch.ClaudeConfigDir == "" {
		return false, invalid("crowsnest cannot read job %s's conversation, so it cannot tell; look in its terminal", jobID)
	}
	path, err := transcript.Locate(s.opts.Launch.ClaudeConfigDir, workDirOf(r), r.ClaudeSessionID)
	if err != nil {
		return false, invalid("job %s's conversation file was not found; look in its terminal", jobID)
	}
	s.markReceived(ctx, jobID, []int64{questionID}, readTail(path, receiptReadBytes))
	receipt, err := s.opts.Store.GetQuestionReceipt(ctx, questionID)
	return err == nil && !receipt.ReceivedAt.IsZero(), err
}
