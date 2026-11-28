// A finished job's branch: one change at a time, and the human's requested changes hold it
// (DESIGN.md 6.11, 6.15, R9-C1, R9-C2).
//
// A done job's branch changes in three ways after it finished: a fix job makes the changes the
// human's review comments asked for (AskChanges), a fix job brings it up to date with its base
// (AskRebase), and Land merges, pushes or discards it. crowsnest's check also reads it (Recheck,
// and the re-check after a fix). Before R9 each read the job and then acted, so two sends could
// both start a fix job in the same worktree, a fix could start while the check ran, and Land
// could merge the commit the human had just sent back.
//
// Now:
//
//   - branch_op is the one operation working on the branch: changes:<fix>, rebase:<fix> or land.
//     It is taken under the jobs lock together with the fix job's insertion (startFix), so two
//     sends cannot both pass; a check running (accept_state checking) counts as one too. Every
//     path refuses while another holds it, and an unreadable job refuses (fail closed).
//   - changes_state is the human's request for changes: requested when the comments go to a fix
//     job, fix_failed when that job did not finish, made when it did. Land refuses every one of
//     them: a passing check says the code builds, not that prose comments were addressed, so only
//     the human's resolution (Accept the changes, or Set them aside) lets the job land again.
//   - recovery: a fix job alive with no reservation on its parent (crowsnest stopped between the
//     insert and the parent's save) is adopted; a land reservation a dead process left is
//     dropped, journalled (the land's own recovery covers an approved request).

package jobs

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jclement/crowsnest/internal/store"
)

// Changes states (jobs.changes_state).
const (
	ChangesRequested = "requested"  // a fix job is making the changes the human asked for
	ChangesFixFailed = "fix_failed" // that fix job failed or was cancelled; the human decides
	ChangesMade      = "made"       // the fix job is done; the human re-accepts before it lands
)

// Branch operations (jobs.branch_op).
const (
	branchChanges = "changes" // changes:<fix id>
	branchRebase  = "rebase"  // rebase:<fix id>
	branchLand    = "land"
)

// branchOpOf splits r's reservation into its kind and fix job ("", 0 when none).
func branchOpOf(r store.JobRecord) (string, int64) {
	kind, id, _ := strings.Cut(r.BranchOp, ":")
	fix, _ := strconv.ParseInt(id, 10, 64)
	return kind, fix
}

// branchBusy is why r's branch cannot take another operation now ("" when it can).
func branchBusy(r store.JobRecord) string {
	kind, fix := branchOpOf(r)
	switch {
	case kind == branchChanges:
		return fmt.Sprintf("fix job %s is making the changes you asked for in its worktree; wait for it (or cancel it)", FormatID(fix))
	case kind == branchRebase:
		return fmt.Sprintf("fix job %s is bringing the branch up to date in its worktree; wait for it (or cancel it)", FormatID(fix))
	case kind == branchLand:
		return "crowsnest is landing this job right now"
	case r.AcceptState == AcceptChecking:
		return "crowsnest is running the check on this branch; wait for its result"
	}
	return ""
}

// changesBlock is why the human's requested changes stop job from landing ("" when they do not).
func changesBlock(job Job) string {
	switch job.ChangesState {
	case ChangesRequested:
		return "you asked for changes and " + firstNonEmptyDir(job.FixJob, "a fix job") + " is making them; it lands once they are made and you accept them"
	case ChangesFixFailed:
		return "you asked for changes and the fix job did not finish; send the comments again, or set the request aside to land it as it is"
	case ChangesMade:
		return "you asked for changes and the fix job made them; look at the diff and accept the changes before it lands"
	}
	return ""
}

// startFix creates fix job input for finished job id and reserves the branch for it, under one
// hold of the jobs lock: the parent's preconditions are read, the child inserted and the parent's
// reservation (and, for comments, its requested-changes state) saved with nothing in between, so
// two requests cannot both pass (R9-C2). kind is branchChanges or branchRebase.
func (s *Service) startFix(ctx context.Context, id, kind string, input CreateInput, journal string) (Job, error) {
	numeric, err := ParseID(id)
	if err != nil {
		return Job{}, ErrNotFound
	}
	input, projectID, err := s.applyProject(ctx, input)
	if err != nil {
		return Job{}, err
	}
	record, err := s.newRecord(input, id)
	if err != nil {
		return Job{}, err
	}
	record.ProjectID = projectID
	s.mu.Lock()
	parent, err := s.opts.Store.GetJob(ctx, numeric)
	if err == nil {
		err = s.fixAdmissible(ctx, parent, kind)
	}
	if err == nil {
		err = s.insertLocked(ctx, &record)
	}
	if err == nil {
		_, err = s.mutateLocked(ctx, id, func(r *store.JobRecord, c *change) error {
			r.BranchOp, r.BranchOpOwner, r.BranchOpAt = kind+":"+strconv.FormatInt(record.ID, 10), s.instance, s.opts.Now()
			if kind == branchChanges {
				r.ChangesState = ChangesRequested
			}
			c.event(EventReview, strings.ReplaceAll(journal, "{fix}", FormatID(record.ID)), map[string]any{"fix": FormatID(record.ID)})
			return nil
		})
		if err != nil {
			// The parent could not be reserved: the child must not run unowned.
			_, _ = s.mutateLocked(ctx, FormatID(record.ID), func(r *store.JobRecord, c *change) error {
				r.State = string(StateCancelled)
				c.event(EventState, "Cancelled: its parent's branch could not be reserved for it", nil)
				return nil
			})
		}
	}
	settled := s.pendingSettled
	s.pendingSettled = nil
	s.mu.Unlock()
	s.tellSettled(ctx, settled)
	if err != nil {
		return Job{}, err
	}
	job := s.toJob(record, 0)
	s.publish(job, "created")
	s.goBackground(ctx, job.ID, func(ctx context.Context) { s.launch(ctx, job.ID) })
	return job, nil
}

// fixAdmissible checks, under the lock, that a fix of kind may start on parent now.
func (s *Service) fixAdmissible(ctx context.Context, parent store.JobRecord, kind string) error {
	id := FormatID(parent.ID)
	switch {
	case State(parent.State) != StateDone:
		return invalid("job %s is %s; only a finished job gets a fix job", id, parent.State)
	case parent.LandState != "" || !parent.TornDownAt.IsZero():
		return invalid("job %s was already %s", id, landWord(parent.LandState))
	}
	if why := branchBusy(parent); why != "" {
		return invalid("job %s: %s", id, why)
	}
	// A fix not yet taken in (finished, its parent not re-checked yet) still owns the branch.
	fixes, err := s.opts.Store.ListFixJobs(ctx, parent.ID)
	if err != nil {
		return fmt.Errorf("reading job %s's fix jobs (refused, so two cannot run at once): %w", id, err)
	}
	for _, fix := range fixes {
		if !State(fix.State).Terminal() {
			return invalid("job %s already has a fix job running (%s); wait for it to finish", id, FormatID(fix.ID))
		}
		if fix.ID > parent.FixApplied {
			return invalid("job %s's fix job %s has finished and is being checked; wait for that result", id, FormatID(fix.ID))
		}
	}
	return nil
}

// releaseFixLocked ends r's reservation for fix (fixSettled), moving the requested changes on by
// how the fix ended.
func releaseFixLocked(r *store.JobRecord, c *change, fix Job) {
	kind, reserved := branchOpOf(*r)
	if reserved != mustParse(fix.ID) {
		return
	}
	r.BranchOp, r.BranchOpOwner, r.BranchOpAt = "", "", zeroTime
	if kind != branchChanges || r.ChangesState == "" {
		return
	}
	if fix.State == StateDone {
		r.ChangesState = ChangesMade
		c.event(EventReview, "The changes you asked for were made by "+fix.ID+"; look at the diff and accept them before it lands", nil)
		return
	}
	r.ChangesState = ChangesFixFailed
	c.event(EventReview, "The fix job "+fix.ID+" did not finish ("+string(fix.State)+"); the changes you asked for are not made. Send the comments again, or set the request aside", nil)
}

// ResolveChanges is the human's decision on requested changes: accept (the fix job's changes, or
// the job as it is) and let it land again. Refused while a fix job or the check is still working.
func (s *Service) ResolveChanges(ctx context.Context, id string) (Job, error) {
	return s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if r.ChangesState == "" {
			return invalid("job %s has no requested changes to resolve", FormatID(r.ID))
		}
		if why := branchBusy(*r); why != "" {
			return invalid("job %s: %s", FormatID(r.ID), why)
		}
		switch r.ChangesState {
		case ChangesMade:
			c.event(EventReview, "You accepted the changes; the job may land", nil)
		default:
			c.event(EventReview, "You set the requested changes aside; the job may land as it is", nil)
		}
		r.ChangesState = ""
		return nil
	})
}

// takeLand reserves the branch for a land action; release gives it back. A merge or a push is
// refused while changes are requested; a discard is not (throwing the work away settles them).
func (s *Service) takeLand(ctx context.Context, id, action string) (release func(), err error) {
	_, err = s.mutate(ctx, id, func(r *store.JobRecord, _ *change) error {
		if why := branchBusy(*r); why != "" {
			return invalid("job %s: %s", FormatID(r.ID), why)
		}
		if r.ChangesState != "" && action != LandDiscard {
			return invalid("%s", changesBlock(Job{ChangesState: r.ChangesState}))
		}
		r.BranchOp, r.BranchOpOwner, r.BranchOpAt = branchLand, s.instance, s.opts.Now()
		return nil
	})
	if err != nil {
		return func() {}, err
	}
	return func() {
		_, _ = s.mutate(context.WithoutCancel(ctx), id, func(r *store.JobRecord, _ *change) error {
			if r.BranchOp != branchLand || r.BranchOpOwner != s.instance {
				return errSkip
			}
			r.BranchOp, r.BranchOpOwner, r.BranchOpAt = "", "", zeroTime
			return nil
		})
	}, nil
}

// ReconcileBranchOps drops every land reservation a dead crowsnest left (R9-C2). serve calls it at
// start and Tick on every pass, each time before ReconcileLandRequests (R10 new-M3): an allowed
// land request whose land was cut short by a crash is then finished, not refused by the dead
// process's reservation.
func (s *Service) ReconcileBranchOps(ctx context.Context) {
	reserved, err := s.opts.Store.ListBranchOps(ctx, superviseBatch)
	if err != nil {
		if ctx.Err() == nil {
			s.opts.Logger.Warn("reading branch reservations", "err", err)
		}
		return
	}
	for _, record := range reserved {
		s.reconcileBranchOps(ctx, record)
	}
}

// reconcileBranchOps settles one job's reservation: a land a dead crowsnest left is dropped.
func (s *Service) reconcileBranchOps(ctx context.Context, r store.JobRecord) {
	kind, _ := branchOpOf(r)
	if kind == branchLand && r.BranchOpOwner != s.instance {
		_, _ = s.mutate(ctx, FormatID(r.ID), func(rec *store.JobRecord, c *change) error {
			if rec.BranchOp != branchLand || rec.BranchOpOwner == s.instance {
				return errSkip
			}
			rec.BranchOp, rec.BranchOpOwner, rec.BranchOpAt = "", "", zeroTime
			c.event(EventLand, "A land was interrupted by a crowsnest restart; nothing more is done for it (look at the branch, and land it again if it did not happen)", nil)
			return nil
		})
	}
}

// adoptFixes gives a live fix job whose parent holds no reservation for it (crowsnest stopped
// between inserting it and saving the parent) its reservation.
func (s *Service) adoptFixes(ctx context.Context) {
	live, err := s.opts.Store.ListLiveFixJobs(ctx, superviseBatch)
	if err != nil {
		return
	}
	for _, fix := range live {
		parent := FormatID(fix.FixOf)
		_, _ = s.mutate(ctx, parent, func(r *store.JobRecord, c *change) error {
			if _, reserved := branchOpOf(*r); reserved == fix.ID {
				return errSkip
			}
			if r.BranchOp != "" && (r.BranchOp != branchLand || r.BranchOpOwner == s.instance) {
				return errSkip // another fix, or a land of this process, holds it: that one settles first
			}
			kind := branchRebase
			if strings.HasPrefix(fix.BriefMD, commentsFixBrief) {
				kind = branchChanges
				if r.ChangesState == "" {
					r.ChangesState = ChangesRequested
				}
			}
			r.BranchOp, r.BranchOpOwner, r.BranchOpAt = kind+":"+strconv.FormatInt(fix.ID, 10), s.instance, s.opts.Now()
			c.event(EventReview, "Fix job "+FormatID(fix.ID)+" was started just before crowsnest stopped; it owns the branch now", nil)
			return nil
		})
	}
}
