// A job whose agent waits on a tool-call approval (DESIGN.md 8.7). While one is pending the job is
// needs_you with cause approval-tool: the Stop hook lets a turn end there, the backstop and the
// idle and silent checks leave needs_you alone, and the card says what is waiting. When none is
// pending it goes back to running. The approvals service calls SetApprovalWaiting from its change
// callback; the tick re-checks with Options.ApprovalPending in case a callback was lost.

package jobs

import (
	"context"
	"errors"

	"github.com/jclement/crowsnest/internal/store"
)

// SetApprovalWaiting records that a job's agent is (or is no longer) waiting on an approval.
// summary is the call in one line, shown as the cause's detail. With Options.ApprovalPending set
// (as in serve), waiting is read from the approvals store under the jobs lock and the argument is
// only a hint: the approvals' change callbacks can run out of order (a supersession landing
// between an approval's insert and its "requested" callback), and the last one to run must still
// leave the job matching what is actually pending.
func (s *Service) SetApprovalWaiting(ctx context.Context, jobID string, waiting bool, summary string) error {
	if jobID == "" {
		return nil
	}
	_, err := s.mutate(ctx, jobID, func(r *store.JobRecord, c *change) error {
		if s.opts.ApprovalPending != nil {
			waiting = s.opts.ApprovalPending(ctx, jobID)
		}
		return s.approvalWaitingLocked(r, c, waiting, summary)
	})
	if errors.Is(err, errSkip) {
		return nil
	}
	return err
}

func (s *Service) approvalWaitingLocked(r *store.JobRecord, c *change, waiting bool, summary string) error {
	switch {
	case waiting && State(r.State) == StateRunning:
		r.State, r.Cause, r.CauseDetail = string(StateNeedsYou), string(CauseApprovalTool), oneLine(summary, 300)
		r.LastActivityAt = s.opts.Now()
		c.event(EventStuck, "Needs you: a tool call waits for approval: "+oneLine(summary, 160), nil)
	case waiting && State(r.State) == StateNeedsYou && Cause(r.Cause) == CauseApprovalTool:
		r.CauseDetail = oneLine(summary, 300) // another call, while one was already waiting
	case !waiting && State(r.State) == StateNeedsYou && Cause(r.Cause) == CauseApprovalTool:
		r.State, r.Cause, r.CauseDetail = string(StateRunning), "", ""
		r.LastActivityAt = s.opts.Now()
		c.event(EventHook, "No tool call waits for approval any more; back to running", nil)
	default:
		return errSkip
	}
	return nil
}

// reconcileApprovalWait is the tick's check: a job left needs_you/approval-tool with nothing
// pending goes back to running.
func (s *Service) reconcileApprovalWait(ctx context.Context, r store.JobRecord) {
	if State(r.State) != StateNeedsYou || Cause(r.Cause) != CauseApprovalTool || s.opts.ApprovalPending == nil {
		return
	}
	id := FormatID(r.ID)
	if s.opts.ApprovalPending(ctx, id) {
		return
	}
	_ = s.SetApprovalWaiting(ctx, id, false, "")
}
