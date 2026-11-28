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
//
// What the human sees as "sent" follows what really happened (R9-C3, C4, C5, C14): a waiting
// message is a batch of the job's feedback queue (feedback.go) holding the send's claim on its
// notes, which are marked sent only when that batch is delivered; it keeps the send's draft
// protection; and a finished job's request for changes holds it from landing (changes.go).

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

// CommentSend is one send of review comments to a job: the composed message (without a header;
// for the message paths it may point at an attached file), how many notes it carries, whether it
// may type over a draft in the agent's input box, and Ref, the send that holds the notes
// (internal/changes' claim): a deferred batch carries it so the notes are marked sent only when
// the batch was delivered (R9-C5).
type CommentSend struct {
	Body  string
	Count int
	Force bool
	Ref   string
}

// SendReviewComments delivers a composed review message to job id by the route
// PlanReviewComments gives.
func (s *Service) SendReviewComments(ctx context.Context, id string, send CommentSend) (CommentDelivery, error) {
	plan, err := s.PlanReviewComments(ctx, id)
	if err != nil {
		return CommentDelivery{}, err
	}
	if plan.Route == CommentFixJob {
		fix, err := s.AskChanges(ctx, id, send.Body, send.Count)
		if err != nil {
			return CommentDelivery{}, err
		}
		return CommentDelivery{How: CommentFixJob, FixJob: fix.ID}, nil
	}
	// The human's send is what lifts a final review's objection, so that hold does not count here.
	// A job waiting on a wake-up, or with other feedback queued, takes the comments behind it.
	if why := s.commentsWait(ctx, id); why != "" {
		return s.holdComments(ctx, id, send, why)
	}
	result, err := s.Message(ctx, id, MessageInput{Text: send.Body, ForceOverDraft: send.Force})
	if err != nil {
		return CommentDelivery{}, err
	}
	if result.Uncertain {
		_ = s.Journal(ctx, id, EventReview, fmt.Sprintf("crowsnest lost track of typing your %d review comment%s; they may or may not have arrived", send.Count, plural(send.Count)), nil)
		return CommentDelivery{}, ErrMaybeTyped
	}
	_ = s.Journal(ctx, id, EventReview, fmt.Sprintf("You sent %d review comment%s from the diff", send.Count, plural(send.Count)), map[string]any{"comments": send.Count})
	if result.Queued {
		return CommentDelivery{How: CommentQueued}, nil
	}
	return CommentDelivery{How: CommentTyped}, nil
}

// commentsWait is why comments to job id wait in its feedback queue instead of being typed now:
// the continuation gate refuses (pause, a hold other than a final review's, the usage limit), or
// something is queued already and they must not jump ahead of it ("" when they go now).
func (s *Service) commentsWait(ctx context.Context, id string) string {
	if why := s.continuationRefusal(ctx, id, HoldReview); why != "" {
		return why
	}
	numeric, _ := ParseID(id)
	if record, err := s.opts.Store.GetJob(ctx, numeric); err == nil && hasFeedback(record) {
		return "earlier feedback is still waiting to be typed"
	}
	return ""
}

// holdComments queues the message as a new batch of the job's feedback (behind anything waiting,
// never merged into it), lifting a final review's objection as a message would, and lets
// deliverFeedback type it when nothing holds the job. The batch keeps the send's draft protection
// (R9-C14) and its Ref (R9-C5).
func (s *Service) holdComments(ctx context.Context, id string, send CommentSend, why string) (CommentDelivery, error) {
	text := fmt.Sprintf("[crowsnest review comments %s] %s", id, send.Body)
	_, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if State(r.State).Terminal() || State(r.State) == StateInReview {
			return invalid("job %s is %s now; the comments were not sent", id, r.State)
		}
		if liftHoldLocked(r, c, HoldReview, LiftHuman, nil) {
			c.event(EventReview, "You sent it back to the builder after its review", nil)
		}
		queueComments(r, text, send.Count, send.Ref, send.Force, s.opts.Now())
		r.NudgeStreak = 0
		c.event(EventReview, fmt.Sprintf("You sent %d review comment%s from the diff; they wait until the job may continue (%s)", send.Count, plural(send.Count), oneLine(why, 160)),
			map[string]any{"comments": send.Count})
		return nil
	})
	if err != nil {
		return CommentDelivery{}, err
	}
	s.deliverFeedback(ctx, id) // refused while held; the tick types it later
	if now := s.continuationRefusal(ctx, id); now != "" {
		why = now
	}
	return CommentDelivery{How: CommentWaiting, Why: why}, nil
}

// commentsFixBrief starts every fix job's brief for review comments (recovery tells it from a
// rebase by it).
const commentsFixBrief = "Make the changes the human asked for in review comments on job "

// AskChanges starts a fix job in a finished job's worktree that makes the changes the human's
// review comments ask for, and marks the job's changes requested (R9-C1): Land refuses it until
// the fix is done and the human accepts the changes (or sets them aside). When the fix is done,
// crowsnest checks the job again (fixSettled), exactly as after a rebase.
func (s *Service) AskChanges(ctx context.Context, id, comments string, count int) (Job, error) {
	detail, err := s.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	job := detail.Job
	if job.WorktreeBranch == "" || job.WorkDir == "" || job.TornDownAt != nil {
		return Job{}, invalid("job %s has no worktree for a fix job", job.ID)
	}
	numeric, _ := ParseID(job.ID)
	off := false
	brief := fmt.Sprintf(commentsFixBrief+"%s.\n\n"+
		"You are in the job's own worktree (%s, branch %s). The human reviewed its diff and left the comments below, each on a file "+
		"and lines with the code quoted. Address each one, run the project's checks (through `crowsnest heavy`), commit, and call done "+
		"saying what you changed for each comment. Do not push, and do not switch branches. crowsnest runs the check again when you are done.\n\n"+
		"## The comments\n\n%s\n\n## The job's brief\n\n%s", job.ID, job.WorkDir, job.WorktreeBranch, comments, indent(job.BriefMD))
	input := CreateInput{Brief: brief, Dir: job.WorkDir, Role: "builder", Engine: job.Engine, Model: job.Model, Isolate: &off, Reviewer: ReviewerOff, fixOf: numeric}
	journal := fmt.Sprintf("You requested changes: %d review comment%s from the diff went to {fix}, a fix job in this worktree. "+
		"The job does not land until the changes are made and you accept them", count, plural(count))
	return s.startFix(ctx, job.ID, branchChanges, input, journal)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
