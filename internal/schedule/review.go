// Reviewing task files (R5-S3). A task file is read from a directory an agent can write: the
// orchestrator's own working directory, and ~/.config/crowsnest/tasks, which belongs to the same
// user every agent runs as. So a file is no evidence that the human wanted it. A file task that
// starts a job (`as: job`), or reaches a project's lead (a poke with `project:`), would let an
// agent start unconfined work on a schedule with one file write; such a task runs only once the
// human has approved this exact content, from the Schedule view. Any edit is a new hash and is
// reviewed again; putting an older file back is too (only the content approved last may run).
//
// Poke tasks for the orchestrator are not reviewed: they only add a line to a poke the
// orchestrator receives anyway, telling it to read a file it could read regardless. They are
// listed in the Schedule view like every task. App schedules are never reviewed: only the human
// can make one, through the signed-in browser.

package schedule

import (
	"context"
	"errors"
	"time"

	"github.com/jclement/crowsnest/internal/journal"
)

// Review states of a task, as TaskView.Review shows them.
const (
	ReviewNone     = ""         // not reviewed: an app schedule, or an orchestrator poke file
	ReviewNeeded   = "needed"   // new or changed since the human approved it: it does not run
	ReviewApproved = "approved" // this content was approved
)

// outcomeNeedsReview is the stamp of a task held for review.
const outcomeNeedsReview = "not run: new or changed, waiting for your review in the Schedule view"

// Reviews is what review needs of the store (store.Store). Nil in Options: every task that needs
// review is held, since nothing can record an approval.
type Reviews interface {
	TaskReviewIsApproved(ctx context.Context, path, hash string) (bool, error)
	ApproveTaskFile(ctx context.Context, path, hash string, at time.Time) error
}

// ErrStaleReview is returned when the file changed after the human looked at it.
var ErrStaleReview = errors.New("the task file changed since you looked at it; read it again before approving")

// ErrNeedsReview is returned by Run now for a task held for review.
var ErrNeedsReview = errors.New("this task file is new or changed: approve it before it runs")

// ErrNoReview is returned for a task that needs no review.
var ErrNoReview = errors.New("this task needs no review")

// NeedsReview reports whether a task must be approved before it runs: a task file that starts a
// job or pokes a project's lead.
func NeedsReview(task Task) bool {
	return task.Origin != OriginApp && (task.As == RunAsJob || task.ProjectID != 0)
}

// reviewState is a task's review state. A read error counts as not approved.
func (s *Service) reviewState(ctx context.Context, task Task) string {
	if !NeedsReview(task) {
		return ReviewNone
	}
	if s.opts.Reviews == nil {
		return ReviewNeeded
	}
	ok, err := s.opts.Reviews.TaskReviewIsApproved(ctx, task.Path, task.Hash)
	if err != nil {
		s.opts.Logger.Warn("reading a task review; holding the task", "path", task.Path, "err", err)
		return ReviewNeeded
	}
	if ok {
		return ReviewApproved
	}
	return ReviewNeeded
}

// mayRun reports whether a task may run now as far as review goes. A task held for review is
// journalled once per content (a new hash is a new alert) and stamped so the view says why.
func (s *Service) mayRun(ctx context.Context, task Task) bool {
	if s.reviewState(ctx, task) != ReviewNeeded {
		return true
	}
	s.alertReview(ctx, task)
	return false
}

// alertReview journals a task waiting for review, once per path and hash.
func (s *Service) alertReview(ctx context.Context, task Task) {
	key := fingerprint([]string{"review", task.Path, task.Hash})
	s.mu.Lock()
	s.loadLocked(ctx)
	_, done := s.state.Reported[key]
	if !done {
		if s.state.Reported == nil {
			s.state.Reported = map[string]int64{}
		}
		s.state.Reported[key] = s.opts.Now().Unix()
		s.pruneReportedLocked()
		s.saveLocked(ctx)
	}
	s.mu.Unlock()
	if done {
		return
	}
	s.opts.Journal.Add(ctx, journal.KindAlert, "Scheduled task "+task.Name+" ("+task.Path+") is new or changed and will not run until you review it in the Schedule view",
		map[string]any{"task": task.Name, "path": task.Path, "hash": task.Hash})
}

// Approve records the human's approval of a task file's content. hash is the content the human
// was shown (TaskView.Hash); if the file has changed since, nothing is approved.
func (s *Service) Approve(ctx context.Context, name, hash string) error {
	task, err := s.find(ctx, name)
	if err != nil {
		return err
	}
	if !NeedsReview(task) {
		return ErrNoReview
	}
	if hash == "" || hash != task.Hash {
		return ErrStaleReview
	}
	if s.opts.Reviews == nil {
		return errors.New("task reviews cannot be recorded in this server")
	}
	if err := s.opts.Reviews.ApproveTaskFile(ctx, task.Path, task.Hash, s.opts.Now()); err != nil {
		return err
	}
	s.opts.Journal.Add(ctx, journal.KindTask, "You approved scheduled task "+task.Name+" ("+task.Path+") as it reads now",
		map[string]any{"task": task.Name, "path": task.Path, "hash": task.Hash})
	s.publish()
	return nil
}
