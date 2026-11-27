// Review comments from the diff view, sent to a job (DESIGN.md 6.21). internal/changes keeps the
// notes and composes them into one message body; this file decides how that message reaches the
// job, by the paths crowsnest already has, so no gate is bypassed:
//
//   - a live job (running, blocked, needs_you) that nothing holds: the human's message path
//     (Message), which types it now (or Claude queues it mid-turn) and, as a message does, lifts a
//     final review's objection (the human sends it back to the builder);
//   - a live job that is paused, held (a budget, a check, open questions, an approval) or under
//     the fleet's usage limit: the message waits as the job's pending feedback and is typed by
//     deliverFeedback, which passes the continuation gate (R7-C04) and so never starts a turn a hold
//     forbids; the tick types it once nothing holds the job any more;
//   - a finished job (done: accepted, or with the review's approval): a finished job does not
//     change (CheckTransition; its agent's tokens are revoked), so "request changes" is a fix job
//     in its own worktree, briefed with the comments, as AskRebase's is; when the fix is done
//     crowsnest checks the job again (fixSettled). Queued like any job, so the pause, throttle and
//     budgets hold it as they hold any other;
//   - a job crowsnest is checking or reviewing (in_review) is refused until that ends.

package jobs

import (
	"context"
	"fmt"

	"github.com/jclement/crowsnest/internal/store"
)

// Ways review comments reach a job.
const (
	CommentTyped   = "typed"   // typed into the agent now
	CommentQueued  = "queued"  // Claude queued it behind the turn it is in
	CommentWaiting = "waiting" // kept as pending feedback until nothing holds the job
	CommentFixJob  = "fix_job" // a finished job: a fix job in its worktree makes the changes
)

// CommentPlan is how comments would reach a job, decided before anything is sent (the caller
// attaches a long message as a file only for the message paths: a fix job's brief is a file
// already).
type CommentPlan struct {
	Route string `json:"route"` // message | fix_job
}

// CommentDelivery is what became of a send.
type CommentDelivery struct {
	How string `json:"how"`
	// Why is what holds a waiting message; FixJob the job making the changes for a finished one.
	Why    string `json:"why,omitempty"`
	FixJob string `json:"fix_job,omitempty"`
}

// PlanReviewComments says how comments would reach job id, or why they cannot.
func (s *Service) PlanReviewComments(ctx context.Context, id string) (CommentPlan, error) {
	detail, err := s.Get(ctx, id)
	if err != nil {
		return CommentPlan{}, err
	}
	job := detail.Job
	switch {
	case job.LandState != "" || job.TornDownAt != nil:
		return CommentPlan{}, invalid("job %s was already %s; there is nothing left to change", job.ID, landWord(job.LandState))
	case job.State == StateDone:
		if job.WorktreeBranch == "" || job.WorkDir == "" {
			return CommentPlan{}, invalid("job %s is finished and has no worktree of its own for a fix job; give a new job with these comments", job.ID)
		}
		return CommentPlan{Route: CommentFixJob}, nil
	case job.State == StateFailed || job.State == StateCancelled:
		return CommentPlan{}, invalid("job %s is %s; give it again as a new job with these comments", job.ID, job.State)
	case job.State == StateInReview:
		return CommentPlan{}, invalid("crowsnest is checking or reviewing job %s right now; send the comments when that ends", job.ID)
	case job.PaneID == "" || s.opts.Herdr == nil:
		return CommentPlan{}, ErrNoAgent
	}
	return CommentPlan{Route: "message"}, nil
}

// SendReviewComments delivers a composed review message (count notes) to job id by the route
// PlanReviewComments gives. body is the message without a header; for the message paths it may
// already point at an attached file.
func (s *Service) SendReviewComments(ctx context.Context, id, body string, count int, force bool) (CommentDelivery, error) {
	plan, err := s.PlanReviewComments(ctx, id)
	if err != nil {
		return CommentDelivery{}, err
	}
	if plan.Route == CommentFixJob {
		fix, err := s.AskChanges(ctx, id, body, count)
		if err != nil {
			return CommentDelivery{}, err
		}
		return CommentDelivery{How: CommentFixJob, FixJob: fix.ID}, nil
	}
	// The human's send is what lifts a final review's objection, so that hold does not count here.
	if why := s.continuationRefusal(ctx, id, HoldReview); why != "" {
		return s.holdComments(ctx, id, body, count, why)
	}
	result, err := s.Message(ctx, id, MessageInput{Text: body, ForceOverDraft: force})
	if err != nil {
		return CommentDelivery{}, err
	}
	_ = s.Journal(ctx, id, EventReview, fmt.Sprintf("You sent %d review comment%s from the diff", count, plural(count)), map[string]any{"comments": count})
	if result.Queued {
		return CommentDelivery{How: CommentQueued}, nil
	}
	return CommentDelivery{How: CommentTyped}, nil
}

// holdComments keeps the message as the job's pending feedback (after any finding already
// waiting), lifting a final review's objection as a message would, and lets deliverFeedback type
// it when nothing holds the job.
func (s *Service) holdComments(ctx context.Context, id, body string, count int, why string) (CommentDelivery, error) {
	text := fmt.Sprintf("[crowsnest review comments %s] %s", id, body)
	_, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if State(r.State).Terminal() || State(r.State) == StateInReview {
			return invalid("job %s is %s now; the comments were not sent", id, r.State)
		}
		if liftHoldLocked(r, c, HoldReview, LiftHuman, nil) {
			c.event(EventReview, "You sent it back to the builder after its review", nil)
		}
		if r.PendingFeedback != "" {
			text = r.PendingFeedback + "\n\n" + text
		}
		r.PendingFeedback = text
		r.NudgeStreak = 0
		c.event(EventReview, fmt.Sprintf("You sent %d review comment%s from the diff; they wait until the job may continue (%s)", count, plural(count), oneLine(why, 160)),
			map[string]any{"comments": count})
		return nil
	})
	if err != nil {
		return CommentDelivery{}, err
	}
	s.deliverFeedback(ctx, id) // refused while held; the tick types it later
	numeric, _ := ParseID(id)
	if record, err := s.opts.Store.GetJob(ctx, numeric); err == nil && record.PendingFeedback == "" {
		return CommentDelivery{How: CommentTyped}, nil
	}
	if now := s.continuationRefusal(ctx, id); now != "" {
		why = now
	}
	return CommentDelivery{How: CommentWaiting, Why: why}, nil
}

// AskChanges starts a fix job in a finished job's worktree that makes the changes the human's
// review comments ask for. When it is done, crowsnest checks the job again (fixSettled), exactly
// as after a rebase.
func (s *Service) AskChanges(ctx context.Context, id, comments string, count int) (Job, error) {
	detail, err := s.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	job := detail.Job
	switch {
	case job.WorktreeBranch == "" || job.WorkDir == "" || job.TornDownAt != nil:
		return Job{}, invalid("job %s has no worktree for a fix job", job.ID)
	case job.State != StateDone:
		return Job{}, invalid("job %s is %s; only a finished job gets a fix job", job.ID, job.State)
	case job.LandState != "":
		return Job{}, invalid("job %s was already %s", job.ID, landWord(job.LandState))
	}
	numeric, _ := ParseID(job.ID)
	if err := s.fixInFlight(ctx, job.ID, numeric); err != nil {
		return Job{}, err
	}
	off := false
	brief := fmt.Sprintf("Make the changes the human asked for in review comments on job %s.\n\n"+
		"You are in the job's own worktree (%s, branch %s). The human reviewed its diff and left the comments below, each on a file "+
		"and lines with the code quoted. Address each one, run the project's checks (through `crowsnest heavy`), commit, and call done "+
		"saying what you changed for each comment. Do not push, and do not switch branches. crowsnest runs the check again when you are done.\n\n"+
		"## The comments\n\n%s\n\n## The job's brief\n\n%s", job.ID, job.WorkDir, job.WorktreeBranch, comments, indent(job.BriefMD))
	fix, err := s.Create(ctx, CreateInput{Brief: brief, Dir: job.WorkDir, Role: "builder", Engine: job.Engine, Model: job.Model, Isolate: &off,
		Reviewer: ReviewerOff, fixOf: numeric}, job.ID)
	if err != nil {
		return Job{}, err
	}
	_ = s.Journal(ctx, job.ID, EventReview, fmt.Sprintf("You requested changes: %d review comment%s from the diff went to %s, a fix job in this worktree; the job is checked again when it is done",
		count, plural(count), fix.ID), map[string]any{"fix": fix.ID, "comments": count})
	return fix, nil
}

// fixInFlight refuses a second fix job while one is running on the job, or while a finished
// one's result is not applied yet (R6-C11).
func (s *Service) fixInFlight(ctx context.Context, id string, numeric int64) error {
	applied := int64(0)
	if record, err := s.opts.Store.GetJob(ctx, numeric); err == nil {
		applied = record.FixApplied
	}
	fixes, err := s.opts.Store.ListFixJobs(ctx, numeric)
	if err != nil {
		return nil //nolint:nilerr // as AskRebase: an unreadable list does not block the human
	}
	for _, fix := range fixes {
		if !State(fix.State).Terminal() {
			return invalid("job %s already has a fix job running (%s); wait for it to finish", id, FormatID(fix.ID))
		}
		if fix.ID > applied {
			return invalid("job %s's fix job %s has finished and is being checked; wait for that result", id, FormatID(fix.ID))
		}
	}
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
