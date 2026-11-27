// Every wait has an owner and a wake-up (DESIGN.md 6.18). A live job is always somebody's: its
// agent's (running, or blocked on something it will look at again), crowsnest's (launching,
// checking, retrying, waiting for a slot), a reviewer's, or the human's (a question, a dialog,
// a budget, the pause). And every wait names what ends it.
//
// `blocked` is no longer an exit from supervision: declaring it needs a wake-up, one of
// recheck_in (a duration), recheck_at (a time), until_job (another job settling) or
// until_human. crowsnest wakes the agent then ("check whether it has cleared"), counts the
// rechecks and tells the human after three; a job waiting on another is woken when that one
// settles, whether or not crowsnest was running at the time (the tick looks too). A blocked job
// from before wake-ups gets a recheck in 30 minutes.
//
// A periodic invariant check lists every live job that breaks the rule: on the System page, and
// once on the job's timeline.

package jobs

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// Wake kinds (jobs.wake_kind).
const (
	WakeNone  = ""
	WakeTime  = "time"
	WakeJob   = "job"
	WakeHuman = "human"
)

const (
	// maxRecheckIn bounds how far ahead a recheck may be set.
	maxRecheckIn = 24 * time.Hour
	// minRecheckIn is the earliest a recheck may be.
	minRecheckIn = time.Minute
	// legacyRecheck is given to a blocked job that has no wake-up (from before 6.18).
	legacyRecheck = 30 * time.Minute
	// recheckTellAfter is how many rechecks of one blocked spell before the human is told.
	recheckTellAfter = 3
	// invariantEvery is how often the invariant check runs.
	invariantEvery = time.Minute
	// ownerlessGrace is how long a job may sit in a step's in-between before it counts as a
	// violation (a launch, a restart).
	ownerlessGrace = 3 * time.Minute
)

// parseWake reads a blocked declaration's wake-up into r (validated); "" is fine only when
// nothing was given and the caller decides.
func (s *Service) parseWake(ctx context.Context, r *store.JobRecord, input UpdateInput) error {
	given := 0
	for _, set := range []bool{strings.TrimSpace(input.RecheckIn) != "", strings.TrimSpace(input.RecheckAt) != "", strings.TrimSpace(input.UntilJob) != "", input.UntilHuman} {
		if set {
			given++
		}
	}
	if given == 0 {
		return invalid("state blocked needs a wake-up: recheck_in (such as 30m or 2h: crowsnest wakes you then to look again), " +
			"until_job (j_N: you are woken when that job finishes) or until_human (it waits for the human). Nothing waits for ever")
	}
	if given > 1 {
		return invalid("give one wake-up: recheck_in, recheck_at, until_job or until_human")
	}
	now := s.opts.Now()
	r.WakeKind, r.WakeAt, r.WakeJob = WakeNone, zeroTime, 0
	switch {
	case strings.TrimSpace(input.RecheckIn) != "":
		delay, err := time.ParseDuration(strings.TrimSpace(input.RecheckIn))
		if err != nil {
			return invalid("recheck_in %q is not a duration; give one like 30m, 2h or 1h30m", termCap(input.RecheckIn))
		}
		if delay < minRecheckIn || delay > maxRecheckIn {
			return invalid("recheck_in must be between 1m and 24h")
		}
		r.WakeKind, r.WakeAt = WakeTime, now.Add(delay)
	case strings.TrimSpace(input.RecheckAt) != "":
		at, err := parseRecheckAt(strings.TrimSpace(input.RecheckAt), now)
		if err != nil {
			return err
		}
		r.WakeKind, r.WakeAt = WakeTime, at
	case strings.TrimSpace(input.UntilJob) != "":
		target, err := ParseID(strings.TrimSpace(input.UntilJob))
		if err != nil {
			return invalid("until_job: %v", err)
		}
		if target == r.ID {
			return invalid("a job cannot wait on itself")
		}
		other, err := s.opts.Store.GetJob(ctx, target)
		if err != nil {
			return invalid("until_job %s: no such job", FormatID(target))
		}
		if State(other.State).Terminal() {
			return invalid("job %s is already %s; read its result (it is not worth waiting on)", FormatID(target), other.State)
		}
		r.WakeKind, r.WakeJob = WakeJob, target
	default:
		r.WakeKind = WakeHuman
	}
	return nil
}

// parseRecheckAt reads an RFC 3339 time or a local HH:MM (today, or tomorrow when past).
func parseRecheckAt(text string, now time.Time) (time.Time, error) {
	if at, err := time.Parse(time.RFC3339, text); err == nil {
		if at.Before(now.Add(minRecheckIn)) || at.After(now.Add(maxRecheckIn)) {
			return time.Time{}, invalid("recheck_at must be between a minute and 24 hours from now")
		}
		return at, nil
	}
	if minutes, ok := hhmm(text); ok {
		local := now.Local()
		at := time.Date(local.Year(), local.Month(), local.Day(), minutes/60, minutes%60, 0, 0, local.Location())
		if !at.After(local.Add(minRecheckIn)) {
			at = at.Add(24 * time.Hour)
		}
		return at, nil
	}
	return time.Time{}, invalid("recheck_at %q is not a time; give HH:MM (local) or an RFC 3339 time", termCap(text))
}

func hhmm(text string) (int, bool) {
	var hour, minute int
	if n, err := fmt.Sscanf(text, "%d:%d", &hour, &minute); err != nil || n != 2 || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

func termCap(text string) string { return oneLine(text, 60) }

// clearWake drops a wake-up (the job is no longer blocked).
func clearWake(r *store.JobRecord) {
	r.WakeKind, r.WakeAt, r.WakeJob = WakeNone, zeroTime, 0
}

// wakeText is a blocked job's wake-up in words.
func wakeText(r store.JobRecord) string {
	switch r.WakeKind {
	case WakeTime:
		return "rechecks at " + r.WakeAt.Local().Format("15:04")
	case WakeJob:
		return "woken when " + FormatID(r.WakeJob) + " finishes"
	case WakeHuman:
		return "waits for you (Wake it)"
	}
	return "no wake-up"
}

// waitOf is who owns a job's wait and what ends it, in words.
func waitOf(r store.JobRecord, paused bool) (owner, wake string) {
	agent := firstNonEmptyDir(r.AgentName, "its agent")
	state := State(r.State)
	if state.Terminal() {
		return "", ""
	}
	if paused && state != StateNeedsYou {
		return "you", "automation is paused: Resume"
	}
	switch state {
	case StateQueued:
		switch Cause(r.Cause) {
		case CauseApproval:
			return "you", "your answer in the Inbox"
		case CauseHeld:
			return "crowsnest", oneLine(r.CauseDetail, 120)
		}
		if r.Dir == "" {
			return "its standing agent", "dispatched when it is free"
		}
		return "crowsnest", "the launch"
	case StateRunning:
		if r.Stuck {
			switch Cause(r.Cause) {
			case CauseLimit, CauseServerError:
				if !r.RetryAt.IsZero() {
					return "crowsnest", "retry at " + r.RetryAt.Local().Format("15:04")
				}
				return "you", "Retry now"
			case CauseExited:
				return "crowsnest", "restarting the agent"
			default:
				return "you", Cause(r.Cause).Describe()
			}
		}
		if r.BudgetState == BudgetGrace {
			return agent, "finishing its step, then back to you for the budget"
		}
		return agent, "its next turn (an undeclared stop is refused)"
	case StateBlocked:
		if r.WakeKind == WakeHuman {
			return "you", wakeText(r)
		}
		return agent, wakeText(r)
	case StateNeedsYou:
		detail := Cause(r.Cause).Describe()
		if detail == "" {
			detail = "something in the job view"
		}
		return "you", detail
	case StateInReview:
		if r.AcceptState == AcceptChecking {
			return "crowsnest", "the check"
		}
		return "the reviewer", "its verdict"
	}
	return "", ""
}

// ---- Waking ---------------------------------------------------------------------------------

// tickBlocked looks after one blocked job: a wake-up that is due, a job waited on that settled,
// and a blocked job from before wake-ups (it is given one).
func (s *Service) tickBlocked(ctx context.Context, r store.JobRecord) {
	if State(r.State) != StateBlocked {
		return
	}
	id := FormatID(r.ID)
	now := s.opts.Now()
	switch r.WakeKind {
	case WakeNone:
		_, _ = s.mutate(ctx, id, func(rec *store.JobRecord, c *change) error {
			if State(rec.State) != StateBlocked || rec.WakeKind != WakeNone {
				return errSkip
			}
			rec.WakeKind, rec.WakeAt = WakeTime, now.Add(legacyRecheck)
			c.event(EventWake, "Blocked with no wake-up; crowsnest will look again at "+rec.WakeAt.Local().Format("15:04"), nil)
			return nil
		})
	case WakeTime:
		if !now.Before(r.WakeAt) {
			s.wake(ctx, id, r.WakeAt, fmt.Sprintf("[crowsnest recheck] You declared job %s blocked: %s. It is time to look again: check whether that has cleared. "+
				"If it has, carry on; if not, call job_update with state blocked and a new wake-up.", id, oneLine(r.BlockedReason, 300)))
		}
	case WakeJob:
		other, err := s.opts.Store.GetJob(ctx, r.WakeJob)
		switch {
		case err != nil:
			s.wake(ctx, id, time.Time{}, fmt.Sprintf("[crowsnest recheck] You waited on job %s, which crowsnest cannot find. Look at what you were blocked on (%s) and carry on or block again.",
				FormatID(r.WakeJob), oneLine(r.BlockedReason, 200)))
		case State(other.State).Terminal():
			s.wake(ctx, id, time.Time{}, dependencyText(id, other))
		}
	}
}

// dependencyText tells an agent the job it waited on settled, and how.
func dependencyText(id string, other store.JobRecord) string {
	outcome := oneLine(firstNonEmptyDir(other.ResultMD, other.FailReason), 240)
	if outcome != "" {
		outcome = ": " + outcome
	}
	return fmt.Sprintf("[crowsnest recheck] Job %s, which job %s was waiting on, is %s%s. Read it with `crowsnest job get %s` if you need more, then carry on.",
		FormatID(other.ID), id, other.State, outcome, FormatID(other.ID))
}

// wakeWaiters wakes the jobs blocked until job settled (called when it settles).
func (s *Service) wakeWaiters(ctx context.Context, job Job) {
	if !job.State.Terminal() {
		return
	}
	numeric, err := ParseID(job.ID)
	if err != nil {
		return
	}
	waiters, err := s.opts.Store.ListJobsWaitingOn(ctx, numeric)
	if err != nil {
		return
	}
	record, err := s.opts.Store.GetJob(ctx, numeric)
	if err != nil {
		return
	}
	for _, waiter := range waiters {
		s.wake(ctx, FormatID(waiter.ID), time.Time{}, dependencyText(FormatID(waiter.ID), record))
	}
}

// wake brings a blocked job back to running and types why. due is the wake-up's time (zero for
// a dependency). While paused it waits (journalled once); the tick tries again after.
func (s *Service) wake(ctx context.Context, id string, due time.Time, text string) {
	if s.heldWhilePaused(ctx, "wake:"+id, "waking "+id+" (its blocked wake-up is due)") {
		return
	}
	job, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if State(r.State) != StateBlocked {
			return errSkip
		}
		if !due.IsZero() && (r.WakeKind != WakeTime || !r.WakeAt.Equal(due)) {
			return errSkip // a newer declaration replaced the wake-up this pass saw
		}
		r.Rechecks++
		reason := r.BlockedReason
		r.State, r.BlockedReason = string(StateRunning), ""
		clearWake(r)
		s.markProgressLocked(r)
		c.event(EventWake, fmt.Sprintf("Woken (recheck %d): %s", r.Rechecks, oneLine(reason, 160)), map[string]any{"reason": reason})
		if r.Rechecks >= recheckTellAfter {
			c.notify = fmt.Sprintf("crowsnest: job %s is still blocked after %d rechecks (%s): %s", FormatID(r.ID), r.Rechecks, oneLine(reason, 80), oneLine(r.Title, 60))
		}
		return nil
	})
	if err != nil || job.PaneID == "" {
		return
	}
	if err := s.sendText(ctx, job.PaneID, job.AgentName, text); err != nil {
		_ = s.Journal(ctx, id, EventWake, "The wake-up could not be typed: "+oneLine(err.Error(), 160)+"; the supervisor keeps it going", nil)
	}
}

// WakeNow is the human's "Wake it" for a blocked job (until_human, or any wake-up).
func (s *Service) WakeNow(ctx context.Context, id string) (Job, error) {
	detail, err := s.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if detail.State != StateBlocked {
		return Job{}, invalid("job %s is not blocked", id)
	}
	s.wake(ctx, id, time.Time{}, fmt.Sprintf("[crowsnest recheck] The human asks you to look again at what blocked job %s (%s): check whether it has cleared, and carry on or block again.",
		id, oneLine(detail.BlockedReason, 200)))
	return s.jobNow(ctx, id, detail.Job), nil
}

// ---- The invariant -------------------------------------------------------------------------

// Violation is one live job that breaks "every wait has an owner and a wake-up".
type Violation struct {
	JobID string    `json:"job_id"`
	Title string    `json:"title"`
	State State     `json:"state"`
	Cause Cause     `json:"cause"`
	Rule  string    `json:"rule"` // what is missing, in words
	Since time.Time `json:"since"`
}

// invariants is the latest check's result.
type invariants struct {
	mu        sync.Mutex
	at        time.Time
	list      []Violation
	journaled map[string]string // job → the rule already journalled
}

// checkInvariant returns what is wrong with r's wait ("" when nothing is).
func (s *Service) checkInvariant(ctx context.Context, r store.JobRecord, now time.Time) string {
	owner, wake := waitOf(r, false)
	settledFor := now.Sub(firstNonZero(r.LastActivityAt, r.StartedAt, r.CreatedAt))
	switch State(r.State) {
	case StateRunning:
		if r.PaneID == "" && r.LifecycleOp == "" && settledFor > ownerlessGrace {
			return "running with no agent holding it"
		}
	case StateNeedsYou:
		if r.Cause == "" {
			if q, err := s.opts.Store.SummarizeJobQuestions(ctx, FormatID(r.ID)); err == nil && q.Open == 0 {
				return "needs you, but says for what (no cause, no open question)"
			}
		}
	case StateBlocked:
		if r.WakeKind == WakeNone {
			return "blocked with no wake-up"
		}
		if r.WakeKind == WakeJob {
			if _, err := s.opts.Store.GetJob(ctx, r.WakeJob); err != nil {
				return "blocked on a job that does not exist"
			}
		}
	case StateInReview:
		if r.AcceptState == AcceptChecking {
			if !s.checkRunning(FormatID(r.ID)) && settledFor > ownerlessGrace {
				return "waiting on a check nobody is running"
			}
			break
		}
		reviewers, err := s.opts.Store.ListReviewJobs(ctx, r.ID)
		if err == nil {
			live := false
			for _, reviewer := range reviewers {
				if !State(reviewer.State).Terminal() {
					live = true
				}
			}
			if !live && settledFor > ownerlessGrace {
				return "in review with no reviewer running"
			}
		}
	case StateQueued:
		if Cause(r.Cause) == CauseApproval && r.LandQuestionID == 0 && r.CauseDetail == "" && settledFor > ownerlessGrace {
			return "held for an approval with no question"
		}
	}
	if owner == "" || wake == "" {
		return "no owner or wake-up"
	}
	return ""
}

func firstNonZero(times ...time.Time) time.Time {
	for _, at := range times {
		if !at.IsZero() {
			return at
		}
	}
	return time.Time{}
}

// tickInvariants runs the invariant check over every live job (at most once a minute).
func (s *Service) tickInvariants(ctx context.Context) {
	now := s.opts.Now()
	s.inv.mu.Lock()
	if !s.inv.at.IsZero() && now.Sub(s.inv.at) < invariantEvery {
		s.inv.mu.Unlock()
		return
	}
	s.inv.at = now
	s.inv.mu.Unlock()
	var found []Violation
	states := append(append([]State{}, liveStates...), StateInReview)
	_ = s.eachLive(ctx, states, func(r store.JobRecord) {
		if rule := s.checkInvariant(ctx, r, now); rule != "" {
			found = append(found, Violation{JobID: FormatID(r.ID), Title: r.Title, State: State(r.State), Cause: Cause(r.Cause), Rule: rule,
				Since: firstNonZero(r.LastActivityAt, r.CreatedAt).UTC()})
		}
	})
	sort.Slice(found, func(i, j int) bool { return found[i].JobID < found[j].JobID })
	s.inv.mu.Lock()
	if s.inv.journaled == nil {
		s.inv.journaled = map[string]string{}
	}
	var fresh []Violation
	current := map[string]string{}
	for _, violation := range found {
		current[violation.JobID] = violation.Rule
		if s.inv.journaled[violation.JobID] != violation.Rule {
			fresh = append(fresh, violation)
		}
	}
	s.inv.journaled = current
	s.inv.list = found
	s.inv.mu.Unlock()
	for _, violation := range fresh {
		_ = s.Journal(ctx, violation.JobID, EventWake, "Invariant: "+violation.Rule+" (every wait needs an owner and a wake-up)", map[string]any{"invariant": violation.Rule})
		s.opts.Logger.Warn("wait invariant", "job", violation.JobID, "rule", violation.Rule)
		if s.opts.OnViolation != nil {
			s.opts.OnViolation(violation)
		}
	}
}

// WaitViolations is the latest invariant check: live jobs with no owner or no wake-up.
func (s *Service) WaitViolations() ([]Violation, time.Time) {
	s.inv.mu.Lock()
	defer s.inv.mu.Unlock()
	return append([]Violation{}, s.inv.list...), s.inv.at
}
