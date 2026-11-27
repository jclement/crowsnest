// Recovery and the heartbeat (DESIGN.md 6.4 and 6.5): a small scheduler that looks at every
// live job on each tick and acts on what it finds. The clock is Options.Now, so tests drive
// Tick with a fake one. Every action is a job_event.
//
// Per job, in order:
//   - a queued no-directory job is offered to the orchestrator; a queued job whose launch was
//     interrupted (crowsnest restarted mid-launch) is launched again;
//   - a brief held back by a start-up dialog is sent once the agent is ready;
//   - needs_you for a dialog returns to running when the dialog is gone;
//   - an agent whose pane is back at a shell is restarted with --resume (at most 3 an hour,
//     then the job fails and Jeff is told);
//   - a limit or API error whose retry is due gets "continue" when the agent is idle;
//   - transcript activity advances last_activity_at; a running job silent for 20 minutes while
//     herdr says working is stuck: silent; idle for 10 minutes and not working is stuck: idle;
//   - an open question older than its urgency allows is re-notified once.

package jobs

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

const (
	// DefaultTickInterval is how often the supervisor looks at live jobs.
	DefaultTickInterval = 10 * time.Second
	// silentAfter: herdr says working but the transcript has not moved (6.4).
	silentAfter = 20 * time.Minute
	// idleAfter: no activity and the agent is not working (6.5).
	idleAfter = 10 * time.Minute
	// maxRestartsPerHour caps automatic restarts of a job's agent (6.4).
	maxRestartsPerHour = 3
	// relaunchAfter is how long a queued job with a directory may sit unlaunched before the tick
	// launches it (its launch was interrupted by a crowsnest restart).
	relaunchAfter = 2 * time.Minute
	// exitedGrace is how long an agent must be seen alive, after its last sign of life, before an
	// "exited" cause nobody owns is taken back (R3-C01): long enough for a process that announced
	// SessionEnd to actually leave its pane.
	exitedGrace = 30 * time.Second
	// continueText is what a retry sends after a limit or API error.
	continueText = "continue"
)

// renotifyAfter is how long an open question may wait, by urgency, before Jeff is told again.
var renotifyAfter = map[string]time.Duration{"blocking": 30 * time.Minute, "normal": 4 * time.Hour, "low": 24 * time.Hour}

// Run ticks until ctx ends.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultTickInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Tick(ctx)
		}
	}
}

// Tick runs one pass of the supervisor over every live job (all of them, a page at a time).
func (s *Service) Tick(ctx context.Context) {
	if s.opts.Herdr == nil {
		return
	}
	s.DispatchStanding(ctx)
	s.ReconcileLandRequests(ctx) // R5-C09: requests sit on finished jobs, which the live pass skips
	// The review gate's own states (R5-C05): in_review is not a live state of the pass below.
	if err := s.eachLive(ctx, []State{StateInReview}, func(record store.JobRecord) { s.reconcileReview(ctx, record) }); err != nil && ctx.Err() == nil {
		s.opts.Logger.Warn("supervisor: listing jobs in review", "err", err)
	}
	if err := s.eachLive(ctx, liveStates, func(record store.JobRecord) { s.tickJob(ctx, record) }); err != nil && ctx.Err() == nil {
		s.opts.Logger.Warn("supervisor: listing jobs", "err", err)
	}
	// A check nobody is running (crowsnest restarted mid-check), in review or after done (6.15).
	if checking, err := s.opts.Store.ListCheckingJobs(ctx, superviseBatch); err == nil {
		for _, record := range checking {
			s.tickChecking(ctx, record)
		}
	}
	s.tickLimits(ctx)
	s.tickInvariants(ctx)
}

// tickJob looks at one live job. Every intermediate state a launch, a restart or a crash can
// leave behind is handled here (R2-C02): a stale claim is settled first; a queued job with a pane
// is an interrupted launch, treated like any job whose agent is gone or whose brief is unsent.
func (s *Service) tickJob(ctx context.Context, r store.JobRecord) {
	id := FormatID(r.ID)
	if s.inflight(id) || s.claimLive(r) {
		return
	}
	if r.LifecycleOp != "" {
		record, ok := s.settleStale(ctx, id)
		if !ok {
			return
		}
		r = record
	}
	if record, ok := s.reconcileQuestions(ctx, r); ok {
		r = record
	}
	s.reconcileApprovalWait(ctx, r) // a lost "settled" callback (8.7); liveness below still runs
	now := s.opts.Now()
	if State(r.State) == StateQueued && Cause(r.Cause) == CauseApproval {
		// Held for the human: apply a decision the callback missed, repair a missing link, and
		// remind them of a question left too long (R3-C02).
		s.ApprovalSettled(ctx, id)
		s.renotifyQuestions(ctx, r)
		return
	}
	if State(r.State) == StateQueued && r.PaneID == "" {
		// A held job is tried on every pass, oldest first (holds.go); any other queued job with
		// a directory only once its own launch has had time to run.
		if r.Dir != "" && (Cause(r.Cause) == CauseHeld || now.Sub(r.CreatedAt) > relaunchAfter) {
			s.goBackground(ctx, id, func(ctx context.Context) { s.launch(ctx, id) })
		}
		return // a no-directory job waits for its standing agent (DispatchStanding)
	}
	if r.PaneID == "" {
		return
	}
	s.renotifyQuestions(ctx, r)
	s.tickBlocked(ctx, r) // a wake-up that is due, or a job waited on that settled (6.18)
	s.checkBudget(ctx, r) // the 80% warning and the limit (6.16)
	if IsStanding(r.AgentName) {
		s.checkActivity(ctx, r, now) // a standing agent's own lifecycle is internal/orch's
		return
	}
	agent, seen := s.observeAgent(ctx, r.PaneID)
	if seen == livenessAlive && r.Stuck && Cause(r.Cause) == CauseExited {
		if record, ok := s.agentIsBack(ctx, r, now); ok {
			r = record
		}
	}
	switch {
	case seen == livenessUnknown:
		// herdr could not be asked: look again next tick, change nothing (R2-C07).
	case seen == livenessGone:
		if !s.heldWhilePaused(ctx, "restart:"+id, "restarting the agent of "+id+" (it exited)") {
			s.restart(ctx, r, false)
		}
	case !r.BriefSent:
		// A job the human started anyway gets its brief even while paused: that start was theirs.
		if !r.StartAnyway && s.heldWhilePaused(ctx, "brief:"+id, "typing the brief of "+id) {
			return
		}
		if agent.InteractiveReady && agent.AgentStatus != herdr.StatusBlocked && !s.hasDialog(ctx, r.PaneID) {
			s.sendHeldBrief(ctx, id)
		}
	case State(r.State) == StateNeedsYou && Cause(r.Cause) == CauseDialog:
		if agent.AgentStatus != herdr.StatusBlocked && !s.hasDialog(ctx, r.PaneID) {
			_, _ = s.mutate(ctx, id, func(rec *store.JobRecord, c *change) error {
				if State(rec.State) == StateNeedsYou && Cause(rec.Cause) == CauseDialog {
					rec.State, rec.Cause, rec.CauseDetail = string(StateRunning), "", ""
					c.event(EventHook, "The dialog is gone; back to running", nil)
				}
				return nil
			})
		}
	case r.Stuck && (Cause(r.Cause) == CauseLimit || Cause(r.Cause) == CauseServerError):
		if !s.heldWhilePaused(ctx, "retry:"+id, "the retry of "+id+" (\"continue\" after a limit or an API error)") {
			s.retry(ctx, r, agent, now)
		}
	default:
		s.checkActivity(ctx, r, now)
		s.tickSpinning(ctx, r)
		s.reconcileReceipts(ctx, r)
	}
}

// agentIsBack re-validates an exit against what herdr shows (R3-C01). Stuck/exited is written by
// the SessionEnd hook and by a restart's claim, and the restart owns it while that claim lives. A
// restart interrupted by a crowsnest crash leaves the cause behind with nobody owning it: the
// stale claim is dropped, the replacement agent may be up and idle with its brief already sent,
// and every other path (the Stop hook, the backstop, the idle check) defers to a recovery that is
// not running. So once the agent has been seen alive for exitedGrace after the last sign of
// life, the exit is over: the cause is cleared, and the agent is supervised like any other (an
// idle one is nudged within the budget, then Jeff is told). It reports the row after the write.
func (s *Service) agentIsBack(ctx context.Context, observed store.JobRecord, now time.Time) (store.JobRecord, bool) {
	if now.Sub(observed.LastActivityAt) < exitedGrace {
		return observed, false // the process may still be on its way out; look again next tick
	}
	var out store.JobRecord
	_, err := s.mutate(ctx, FormatID(observed.ID), func(rec *store.JobRecord, c *change) error {
		if !rec.Stuck || Cause(rec.Cause) != CauseExited || rec.PaneID != observed.PaneID || rec.LifecycleOp != "" {
			return errSkip
		}
		rec.Stuck, rec.Cause, rec.CauseDetail = false, "", ""
		c.event(EventRestart, "The agent is running, so it has not exited (a restart crowsnest lost track of, or an exit that never "+
			"happened). It is supervised again: if it sits idle it is nudged, then you are told", nil)
		out = *rec
		return nil
	})
	return out, err == nil
}

// settleStale resolves a claim left by a dead crowsnest process (or abandoned by this one) and
// returns the row after it. ok is false when the job is gone, finished, or claimed afresh.
func (s *Service) settleStale(ctx context.Context, id string) (store.JobRecord, bool) {
	if _, err := s.mutate(ctx, id, func(rec *store.JobRecord, c *change) error {
		if s.claimLive(*rec) {
			return errBusy
		}
		if rec.LifecycleOp != "" {
			s.resolveStaleLocked(rec, c)
		}
		return nil
	}); err != nil {
		return store.JobRecord{}, false
	}
	record, err := s.opts.Store.GetJob(ctx, mustParse(id))
	if err != nil {
		return store.JobRecord{}, false
	}
	return record, !State(record.State).Terminal()
}

// reconcileQuestions re-derives a job's question-dependent state on every pass, not only when
// something mutates the job (R2-C03): a question settled while crowsnest was down, or whose
// settle callback never ran, must still bring its job back from needs_you. It writes only when
// the derived state differs, and reports the row after the write.
func (s *Service) reconcileQuestions(ctx context.Context, r store.JobRecord) (store.JobRecord, bool) {
	summary, err := s.opts.Store.SummarizeJobQuestions(ctx, FormatID(r.ID))
	if err != nil {
		return r, false
	}
	probe := r
	if !deriveFromQuestions(&probe, summary) {
		return r, false
	}
	var out store.JobRecord
	if _, err := s.mutate(ctx, FormatID(r.ID), func(rec *store.JobRecord, c *change) error {
		if deriveFromQuestions(rec, c.questions) {
			c.event(EventQuestion, "Questions re-checked by the supervisor: the job's state follows them", nil)
		}
		out = *rec
		return nil
	}); err != nil {
		return r, false
	}
	return out, true
}

// sendHeldBrief types a brief that was held back (a start-up dialog, an interrupted launch).
func (s *Service) sendHeldBrief(ctx context.Context, id string) {
	cl, err := s.claimStep(ctx, id, opBrief, func(r *store.JobRecord, _ *change) error {
		if r.BriefSent || r.PaneID == "" {
			return errSkip
		}
		return nil
	})
	if err != nil {
		return
	}
	ran := s.goBackground(ctx, id, func(ctx context.Context) {
		detail, err := s.Get(ctx, id)
		if err == nil {
			err = s.sendBrief(ctx, cl, detail.Job)
		}
		if err != nil && !errors.Is(err, errClaimLost) {
			s.opts.Logger.Warn("sending a held brief", "job", id, "err", err)
			s.finishQuietly(ctx, cl, func(_ *store.JobRecord, c *change) {
				c.event(EventLaunch, "The brief could not be sent: "+oneLine(err.Error(), 200), nil)
			})
		}
	})
	if !ran {
		s.finishQuietly(ctx, cl, nil)
	}
}

// ---- Restart -------------------------------------------------------------------------------

// restart starts a job's agent again in its pane, within the hourly budget. The caller has seen
// the agent gone; the budget, the new session and the claim are taken together under the lock,
// on the current row, so a job cancelled or finished meanwhile is never restarted (R2-C08).
// manual clears the hourly budget (the human's Resume).
func (s *Service) restart(ctx context.Context, observed store.JobRecord, manual bool) {
	id := FormatID(observed.ID)
	now := s.opts.Now()
	var resume, overBudget bool
	cl, err := s.claimStep(ctx, id, opStart, func(rec *store.JobRecord, c *change) error {
		if rec.PaneID == "" || rec.PaneID != observed.PaneID {
			return errSkip // the job moved since the look
		}
		if manual {
			rec.RestartTimes = ""
		}
		recent := recentRestarts(rec.RestartTimes, now)
		if len(recent) >= maxRestartsPerHour {
			overBudget = true
			return errSkip
		}
		rec.Restarts++
		rec.RestartTimes = joinTimes(append(recent, now))
		rec.Stuck, rec.Cause, rec.CauseDetail = true, string(CauseExited), "Restarting the agent"
		c.event(EventRestart, fmt.Sprintf("The agent is gone; restarting it (%d of %d this hour)", len(recent)+1, maxRestartsPerHour), nil)
		resume = rec.Engine == EngineClaude && rec.ClaudeSessionID != "" && s.transcriptExists(s.toJob(*rec, 0))
		if !resume && rec.Engine == EngineClaude {
			rec.ClaudeSessionID = newUUID()
			rec.BriefSent = false
		}
		return nil
	})
	if overBudget {
		s.failRestarts(ctx, id)
		return
	}
	if err != nil {
		return
	}
	ran := s.goBackground(ctx, id, func(ctx context.Context) {
		detail, err := s.Get(ctx, id)
		if err == nil {
			err = s.startInPane(ctx, cl, detail.Job, resume)
		}
		if err != nil && !errors.Is(err, errClaimLost) {
			s.opts.Logger.Warn("restarting a job agent", "job", id, "err", err)
			s.finishQuietly(ctx, cl, func(_ *store.JobRecord, c *change) {
				c.event(EventRestart, "Restart failed: "+oneLine(err.Error(), 200), nil)
			})
		}
	})
	if !ran {
		s.finishQuietly(ctx, cl, nil) // the bookkeeping stays: the attempt counts, the start did not run
	}
}

// failRestarts fails a job whose agent kept exiting, and tells Jeff.
func (s *Service) failRestarts(ctx context.Context, id string) {
	_, _ = s.mutate(ctx, id, func(rec *store.JobRecord, c *change) error {
		if State(rec.State).Terminal() || s.claimLive(*rec) {
			return errSkip
		}
		rec.State, rec.FailReason = string(StateFailed), fmt.Sprintf("The agent exited %d times within an hour; crowsnest stopped restarting it.", maxRestartsPerHour)
		rec.Stuck, rec.Cause = false, ""
		c.event(EventFail, rec.FailReason, nil)
		c.notify = fmt.Sprintf("crowsnest: job %s failed: its agent kept exiting: %s", id, oneLine(rec.Title, 80))
		return nil
	})
}

func (s *Service) transcriptExists(job Job) bool {
	if s.opts.Launch.ClaudeConfigDir == "" || job.WorkDir == "" {
		return false
	}
	_, err := transcript.Locate(s.opts.Launch.ClaudeConfigDir, job.WorkDir, job.SessionID)
	return err == nil
}

func recentRestarts(encoded string, now time.Time) []time.Time {
	var out []time.Time
	for _, part := range strings.Split(encoded, ",") {
		seconds, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil {
			continue
		}
		if at := time.Unix(seconds, 0); now.Sub(at) < time.Hour {
			out = append(out, at)
		}
	}
	return out
}

func joinTimes(times []time.Time) string {
	parts := make([]string, len(times))
	for i, at := range times {
		parts[i] = strconv.FormatInt(at.Unix(), 10)
	}
	return strings.Join(parts, ",")
}

// ---- Retries -------------------------------------------------------------------------------

// retry acts on a limit or API error: when due and the agent is idle, it types "continue".
// An agent that is working again has recovered by itself, but only the error that was observed
// is cleared: a newer hook outcome (an auth failure, say) recorded since stays (R2-C11).
func (s *Service) retry(ctx context.Context, r store.JobRecord, agent herdr.Agent, now time.Time) {
	id := FormatID(r.ID)
	if agent.AgentStatus == herdr.StatusWorking {
		_, _ = s.mutate(ctx, id, func(rec *store.JobRecord, c *change) error {
			if !rec.Stuck || rec.Cause != r.Cause || !rec.RetryAt.Equal(r.RetryAt) || rec.RetryCount != r.RetryCount || rec.State != r.State {
				return errSkip
			}
			rec.Stuck, rec.Cause, rec.CauseDetail, rec.RetryAt = false, "", "", zeroTime
			c.event(EventRetry, "Working again; the "+r.Cause+" has cleared", nil)
			return nil
		})
		return
	}
	if r.RetryAt.IsZero() || now.Before(r.RetryAt) || !agent.AgentStatus.Ready() {
		return
	}
	if Cause(r.Cause) == CauseLimit && s.opts.Limits != nil {
		// The fleet's usage limit (6.17): nothing before its reset, then one job at a time,
		// oldest first (the tick walks live jobs oldest first).
		if held, _ := s.limitHeld(ctx, r.Engine); held || !s.opts.Limits.mayResume(r.Engine) {
			return
		}
	}
	s.retryStep(ctx, id, true)
}

// RetryNow types "continue" into a job stuck on a limit or API error and schedules the next
// attempt: the card's "Retry now" (whether or not the retry is due).
func (s *Service) RetryNow(ctx context.Context, id string) {
	s.retryStep(ctx, id, false)
}

// retryStep claims a retry (the job must still be stuck on a limit or API error, and, for the
// tick, the retry still due), types "continue", and records it. The attempt is counted and the
// next one scheduled when the retry is claimed, before anything is typed (R3-C06): a crash between
// typing and recording must not leave the old count and the old, already-due time, or crash after
// crash would bypass the back-off and the API-error cap. A send that provably typed nothing gives
// the attempt back; a stale retry claim found after a crash stays counted.
func (s *Service) retryStep(ctx context.Context, id string, onlyIfDue bool) {
	var spent retrySpent
	cl, err := s.claimStep(ctx, id, opRetry, func(rec *store.JobRecord, c *change) error {
		recovering := rec.Stuck && (Cause(rec.Cause) == CauseLimit || Cause(rec.Cause) == CauseServerError)
		if !recovering || rec.PaneID == "" || (onlyIfDue && (rec.RetryAt.IsZero() || s.opts.Now().Before(rec.RetryAt))) {
			return errSkip
		}
		spent = retrySpent{count: rec.RetryCount, at: rec.RetryAt}
		s.scheduleNextRetryLocked(rec, c)
		spent.next = rec.RetryAt
		return nil
	})
	if err != nil {
		return
	}
	detail, err := s.Get(ctx, id)
	if err != nil {
		s.finishQuietly(ctx, cl, spent.refund)
		return
	}
	var result orch.PromptResult
	var sendErr error
	if err := s.effect(ctx, cl, detail.PaneID, func(ctx context.Context) {
		result, sendErr = orch.Prompt(ctx, s.opts.Herdr, detail.PaneID, orch.PromptRequest{Text: continueText, ExpectAgent: detail.AgentName})
	}); err != nil {
		return
	}
	outcome := typedOutcome(result, sendErr)
	s.finishQuietly(ctx, cl, func(rec *store.JobRecord, c *change) {
		switch outcome {
		case typedNo:
			c.event(EventRetry, "Retry could not type yet: "+oneLine(sendErr.Error(), 160), nil)
			spent.refund(rec, c)
		case typedMaybe:
			c.event(EventRetry, "crowsnest lost track of that retry's send; counted as given", nil)
		}
	})
}

// scheduleNextRetryLocked counts a retry attempt and schedules the next one; an API error out of
// retries tells Jeff and stops.
func (s *Service) scheduleNextRetryLocked(rec *store.JobRecord, c *change) {
	now := s.opts.Now()
	rec.RetryCount++
	c.event(EventRetry, fmt.Sprintf("Retry %d: typing \"continue\"", rec.RetryCount), nil)
	switch Cause(rec.Cause) {
	case CauseLimit:
		rec.RetryAt = now.Add(retryDelay(limitRetries, limitEvery, rec.RetryCount))
	case CauseServerError:
		if rec.RetryCount < len(serverRetries) {
			rec.RetryAt = now.Add(serverRetries[rec.RetryCount])
		} else {
			rec.RetryAt = zeroTime
			c.notify = fmt.Sprintf("crowsnest: job %s still hits API errors after %d retries", FormatID(rec.ID), rec.RetryCount)
		}
	default:
		rec.RetryAt = zeroTime
	}
}

// retrySpent is the retry bookkeeping before an attempt was counted, so a send that provably
// typed nothing can give the attempt back.
type retrySpent struct {
	count    int
	at, next time.Time // RetryAt before the attempt, and the one the attempt scheduled
}

// refund restores the attempt, unless a hook has changed the schedule since (then it stands).
func (b retrySpent) refund(rec *store.JobRecord, _ *change) {
	if rec.RetryCount != b.count+1 || rec.RetryAt.Unix() != b.next.Unix() {
		return
	}
	rec.RetryCount, rec.RetryAt = b.count, b.at
}

// ---- Heartbeat -----------------------------------------------------------------------------

// checkActivity advances last_activity_at from the transcript and flags silent or idle jobs.
func (s *Service) checkActivity(ctx context.Context, r store.JobRecord, now time.Time) {
	if State(r.State) != StateRunning || !r.BriefSent {
		return
	}
	id := FormatID(r.ID)
	lastAppend, lastTool := time.Time{}, ""
	if s.opts.Activity != nil {
		lastAppend, lastTool = s.opts.Activity(ctx, s.toJob(r, 0))
	}
	agent, err := s.agentGet(ctx, r.PaneID)
	if err != nil && !herdr.IsNotFound(err) {
		return // herdr could not be asked: no verdict on silent or idle this pass
	}
	working := agent.AgentStatus == herdr.StatusWorking
	_, _ = s.mutate(ctx, id, func(rec *store.JobRecord, c *change) error {
		if State(rec.State) != StateRunning {
			return nil
		}
		if lastAppend.After(rec.LastActivityAt) {
			rec.LastActivityAt = lastAppend
			if lastTool != "" {
				rec.LastTool = oneLine(lastTool, maxProgressRunes)
			}
			if rec.Stuck && (Cause(rec.Cause) == CauseSilent || Cause(rec.Cause) == CauseIdle) {
				rec.Stuck, rec.Cause, rec.CauseDetail = false, "", ""
				c.event(EventHook, "Activity again; no longer stuck", nil)
			}
		}
		quiet := now.Sub(rec.LastActivityAt)
		switch {
		case rec.Stuck:
		case working && quiet >= silentAfter:
			rec.Stuck, rec.Cause = true, string(CauseSilent)
			rec.CauseDetail = fmt.Sprintf("herdr says working, but nothing has happened for %d minutes", int(quiet.Minutes()))
			c.event(EventStuck, "Stuck: silent while working", nil)
			c.notify = fmt.Sprintf("crowsnest: job %s has been silent for %d minutes", id, int(quiet.Minutes()))
		case !working && quiet >= idleAfter:
			rec.Stuck, rec.Cause = true, string(CauseIdle)
			rec.CauseDetail = fmt.Sprintf("Not working and nothing declared for %d minutes", int(quiet.Minutes()))
			c.event(EventStuck, "Stuck: idle with nothing declared", nil)
			// Recovery has run out (the hook and the backstop did not get it moving): tell Jeff.
			c.notify = fmt.Sprintf("crowsnest: job %s has sat idle for %d minutes with nothing declared: %s", id, int(quiet.Minutes()), oneLine(rec.Title, 80))
		}
		return nil
	})
}

// ---- Questions -----------------------------------------------------------------------------

// renotifyQuestions tells Jeff once more about a job's question that has waited too long.
// Expiry is never consent: the job stays where it is and says so.
func (s *Service) renotifyQuestions(ctx context.Context, r store.JobRecord) {
	questionList, err := s.questionsOf(ctx, r.ID)
	if err != nil {
		return
	}
	now := s.opts.Now()
	for _, q := range questionList {
		limit, ok := renotifyAfter[q.Urgency]
		if q.Status != "open" || !ok || now.Sub(q.CreatedAt) < limit {
			continue
		}
		marker := `"question":"` + q.ID + `"`
		if done, err := s.opts.Store.HasJobEvent(ctx, r.ID, EventNotify, marker); err != nil || done {
			continue
		}
		_, _ = s.mutate(ctx, FormatID(r.ID), func(_ *store.JobRecord, c *change) error {
			c.event(EventNotify, fmt.Sprintf("Reminded you about %s (open for %s)", q.ID, now.Sub(q.CreatedAt).Round(time.Minute)), map[string]any{"question": q.ID})
			c.notify = fmt.Sprintf("crowsnest: %s is still waiting for you: %s", q.ID, oneLine(q.Title, 100))
			return nil
		})
	}
}

// ---- Helpers -------------------------------------------------------------------------------

// hasDialog reads the pane's screen for a dialog (herdr sometimes reports one as idle).
func (s *Service) hasDialog(ctx context.Context, paneID string) bool {
	ctx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
	defer cancel()
	read, err := s.opts.Herdr.PaneRead(ctx, paneID, herdr.ReadOptions{Source: herdr.SourceVisible})
	if err != nil {
		return false
	}
	return fleet.DetectDialog(fleet.StripANSI(read.Text)).Present
}

// inflight reports whether background work (a launch, a restart) is running for a job.
func (s *Service) inflight(id string) bool {
	s.busyMu.Lock()
	defer s.busyMu.Unlock()
	return s.busy[id]
}

// goBackground runs fn for job id unless something already runs for it, and reports whether it
// took it (a caller holding a claim releases it when not).
func (s *Service) goBackground(ctx context.Context, id string, fn func(context.Context)) bool {
	s.busyMu.Lock()
	if s.busy == nil {
		s.busy = map[string]bool{}
	}
	if s.busy[id] {
		s.busyMu.Unlock()
		return false
	}
	s.busy[id] = true
	s.busyMu.Unlock()
	run := func() {
		defer func() {
			s.busyMu.Lock()
			delete(s.busy, id)
			s.busyMu.Unlock()
		}()
		fn(context.WithoutCancel(ctx))
	}
	if s.opts.Synchronous {
		run()
		return true
	}
	go run()
	return true
}
