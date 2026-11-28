// Lifecycle steps (DESIGN.md 6.8, R2 fixes): how the supervisor acts outside crowsnest on a job
// without acting on a stale read.
//
// Starting an agent, typing a brief, a retry's "continue" and a nudge all happen outside the jobs
// lock, because they wait on herdr for seconds. Before R2 each read the row, let go of the lock,
// acted, and wrote its outcome back, so a job cancelled (or finished, or nudged by its Stop hook)
// in between was still restarted or prompted. Every such step now goes through one pattern:
//
//  1. claim: under the lock, recheck what the step depends on and record the claim on the row
//     (lifecycle_op, the owning process, the time, and a generation that the claim bumps);
//  2. before each external effect, holds: under the lock, is the claim still the row's and the
//     job not finished? A terminal transition bumps the generation, so it fails;
//  3. finish: under the lock and only if the claim still holds, commit what happened and clear
//     the claim. A step that provably did nothing releases without spending anything.
//
// The claim is persisted. A claim whose owner is another (dead) crowsnest process, or that is
// older than claimTTL, is stale; resolveStaleLocked settles it by what the step was. A brief or
// a dispatch whose typing may have happened is counted as sent, never typed twice (at most once,
// like answer delivery): the SessionStart hook re-injects the brief, and the Stop hook and the
// backstop keep the agent going.

package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
)

// Lifecycle steps.
const (
	opLaunch   = "launch"   // preparing a pane and starting the agent for the first time
	opStart    = "start"    // starting the agent again in its pane (a restart, an interrupted launch)
	opBrief    = "brief"    // typing the brief into a job agent
	opDispatch = "dispatch" // typing the brief into the orchestrator
	opRetry    = "retry"    // typing "continue" after a limit or an API error
	opNudge    = "nudge"    // typing a nudge (the backstop, or the human's Nudge)
	opFeedback = "feedback" // typing a check's or a review's findings (pending_feedback, R7-C05)
)

// claimTTL is how long a claim held by this process counts as live. Every step finishes well
// inside it (a start is bounded by ShellWait, startRetryWait and LaunchTimeout); a claim older
// than this was lost (a panic, a bug) and is settled like one left by a dead process.
const claimTTL = 10 * time.Minute

var (
	// errClaimLost means the job finished, or another step took it, while this one was acting.
	errClaimLost = errors.New("the job changed while crowsnest was acting on it")
	// errBusy means another step holds the job right now.
	errBusy = errors.New("another step is acting on the job")
	// errSkip abandons a claim attempt whose precondition no longer holds; nothing is written.
	errSkip = errors.New("nothing to do")
)

// claim is a held lifecycle step.
type claim struct {
	id  string
	op  string
	gen int64
	at  time.Time
	// owned is the job's disposition when the step claimed it (after its own bookkeeping). A step
	// may replace that disposition when it finishes, and only that: a newer one the agent or a
	// hook wrote meanwhile (blocked, an auth failure, a limit) is not the step's to erase (R3-C04).
	owned disposition
}

// disposition is the part of a job's row that says what the job is doing and why.
type disposition struct {
	state string
	stuck bool
	cause string
}

func dispositionOf(r store.JobRecord) disposition {
	return disposition{state: r.State, stuck: r.Stuck, cause: r.Cause}
}

// owns reports whether r still carries the disposition cl claimed, so the step may replace it.
func (cl claim) owns(r store.JobRecord) bool { return dispositionOf(r) == cl.owned }

// newInstance names this crowsnest process in the claims it writes.
func newInstance() string {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return time.Now().Format(time.RFC3339Nano)
	}
	return hex.EncodeToString(raw)
}

// claimLive reports whether r carries a claim this process holds and has not abandoned.
func (s *Service) claimLive(r store.JobRecord) bool {
	return r.LifecycleOp != "" && r.LifecycleOwner == s.instance && s.opts.Now().Sub(r.LifecycleAt) < claimTTL
}

// claimStep takes op on job id. check runs under the lock on the fresh row and may change it
// (bookkeeping that belongs with the claim); it returns errSkip (or any error) to take nothing.
func (s *Service) claimStep(ctx context.Context, id, op string, check func(r *store.JobRecord, c *change) error) (claim, error) {
	var out claim
	_, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		return s.claimLocked(r, c, op, check, &out)
	})
	return out, err
}

// claimLocked is claimStep's body, for callers already inside a mutation.
func (s *Service) claimLocked(r *store.JobRecord, c *change, op string, check func(r *store.JobRecord, c *change) error, out *claim) error {
	if State(r.State).Terminal() {
		return errClaimLost
	}
	if s.claimLive(*r) {
		return errBusy
	}
	if r.LifecycleOp != "" {
		s.resolveStaleLocked(r, c)
		if State(r.State).Terminal() {
			return errClaimLost
		}
	}
	if check != nil {
		if err := check(r, c); err != nil {
			return err
		}
	}
	now := s.opts.Now()
	r.LifecycleGen++
	r.LifecycleOp, r.LifecycleOwner, r.LifecycleAt = op, s.instance, now
	*out = claim{id: FormatID(r.ID), op: op, gen: r.LifecycleGen, at: now, owned: dispositionOf(*r)}
	return nil
}

// holds reports whether cl is still the job's claim and the job is not finished: the check made
// right before each external effect.
func (s *Service) holds(ctx context.Context, cl claim) error {
	numeric, err := ParseID(cl.id)
	if err != nil {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.opts.Store.GetJob(ctx, numeric)
	if err != nil {
		return err
	}
	if !holdsLocked(record, cl) {
		return errClaimLost
	}
	return nil
}

func holdsLocked(r store.JobRecord, cl claim) bool {
	return !State(r.State).Terminal() && r.LifecycleGen == cl.gen && r.LifecycleOp != ""
}

// advance moves a held claim to the next step (a launch reaching the brief), so a crash after
// it is settled by what was really in flight.
func (s *Service) advance(ctx context.Context, cl claim, op string) (claim, error) {
	_, err := s.mutate(ctx, cl.id, func(r *store.JobRecord, _ *change) error {
		if !holdsLocked(*r, cl) {
			return errClaimLost
		}
		r.LifecycleOp = op
		return nil
	})
	if err != nil {
		return claim{}, err
	}
	cl.op = op
	return cl, nil
}

// update changes the row while the claim holds, keeping the claim (a launch recording its pane).
func (s *Service) update(ctx context.Context, cl claim, fn func(r *store.JobRecord, c *change)) error {
	_, err := s.mutate(ctx, cl.id, func(r *store.JobRecord, c *change) error {
		if !holdsLocked(*r, cl) {
			return errClaimLost
		}
		fn(r, c)
		return nil
	})
	return err
}

// finish commits a step's outcome and clears the claim, only if the claim still holds. fn may be
// nil (a release that changes nothing else).
func (s *Service) finish(ctx context.Context, cl claim, fn func(r *store.JobRecord, c *change)) (Job, error) {
	return s.mutate(ctx, cl.id, func(r *store.JobRecord, c *change) error {
		if !holdsLocked(*r, cl) {
			return errClaimLost
		}
		if fn != nil {
			fn(r, c)
		}
		clearClaim(r)
		return nil
	})
}

// finishQuietly is finish for a step whose caller has nothing left to do with the outcome.
func (s *Service) finishQuietly(ctx context.Context, cl claim, fn func(r *store.JobRecord, c *change)) {
	if _, err := s.finish(ctx, cl, fn); err != nil && !errors.Is(err, errClaimLost) {
		s.opts.Logger.Warn("recording a job step", "job", cl.id, "step", cl.op, "err", err)
	}
}

func clearClaim(r *store.JobRecord) {
	r.LifecycleOp, r.LifecycleOwner, r.LifecycleAt = "", "", zeroTime
}

// resolveStaleLocked settles a claim left by a crowsnest process that is gone (or by a step of
// this one that never finished), by what the step was:
//   - a brief or a dispatch may have been typed: it counts as sent and is not typed again;
//   - a launch or a start leaves the pane as it is; the tick looks at the agent next;
//   - a retry or a nudge is dropped: it was counted against its budget when it was claimed
//     (R3-C06), so one that may have been typed is never handed out again for free.
func (s *Service) resolveStaleLocked(r *store.JobRecord, c *change) {
	op := r.LifecycleOp
	clearClaim(r)
	r.LifecycleGen++
	switch op {
	case opBrief, opDispatch:
		if r.BriefSent {
			return
		}
		now := s.opts.Now()
		r.BriefSent, r.StartedAt, r.LastActivityAt = true, now, now
		switch State(r.State) {
		case StateQueued:
			r.State = string(StateRunning)
		case StateNeedsYou:
			if Cause(r.Cause) == CauseDialog || Cause(r.Cause) == CauseLaunch {
				r.State, r.Cause, r.CauseDetail = string(StateRunning), "", ""
			}
		}
		c.event(EventLaunch, "crowsnest stopped while typing the brief; it may or may not have arrived, so it is not typed again. "+
			"The agent reads it at session start; if it sits idle it is nudged", map[string]any{"step": op})
	case opRetry, opNudge:
		c.event(EventLaunch, "A "+op+" interrupted by a crowsnest restart may or may not have been typed; it stays counted", map[string]any{"step": op})
	case opFeedback:
		// R7-C05, R9-C4: the batch being typed may have arrived; typing it again would start a
		// second turn on it. Only that batch is settled: anything queued behind it stays due.
		batch, ok := sendingFeedback(*r)
		if !ok {
			return
		}
		takeFeedback(r, batch.ID)
		if batch.Kind == FeedbackComments {
			// The human's notes go back to them, marked as may-or-may-not-have-arrived, to send again or not.
			c.settleComments(batch, FeedbackUncertain, "crowsnest stopped while typing them")
			c.event(EventReview, fmt.Sprintf("crowsnest stopped while typing your %d review comment%s; they may or may not have arrived, so they are not typed again. "+
				"The diff shows them as unsent; send them again if the agent did not get them", batch.Count, plural(batch.Count)), map[string]any{"step": op})
			return
		}
		c.event(EventAccept, "crowsnest stopped while typing the findings to the agent; they may or may not have arrived, so they are not typed again "+
			"(job_get shows the check's output and the review; send a message if the agent did not get them)", map[string]any{"step": op})
	default:
		c.event(EventLaunch, "A step interrupted by a crowsnest restart was dropped: "+op, map[string]any{"step": op})
	}
}

// ---- In-flight effects (R2-C08 residual) ---------------------------------------------------

// A claim check is not an execution fence: between a step's last holds and herdr typing, orch.Prompt
// still looks up the agent and reads its screen, and a Cancel landing there used to commit, send
// Esc, and then have the text typed after the Esc, starting unsupervised work on a cancelled job.
// So each external effect registers itself first, then checks the claim, then runs under a
// context Cancel can end. Cancel, after committing, cancels a registered effect and leaves the Esc
// to it: the effect sends Esc when it returns, so the interrupt always follows anything the
// effect managed to type. Registration comes before the claim check, so either the check sees the
// cancel, or Cancel (which commits before it looks) sees the effect.

// inflightEffect is one step's external effect while it runs.
type inflightEffect struct {
	cancel   context.CancelFunc
	escAfter bool // a Cancel came in: interrupt the agent once the effect is over
}

// effect runs fn, the external effect of the held claim cl on the agent in paneID. It returns
// errClaimLost, without running fn, when the claim no longer holds.
func (s *Service) effect(ctx context.Context, cl claim, paneID string, fn func(context.Context)) error {
	effectCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	e := &inflightEffect{cancel: cancel}
	s.effectsMu.Lock()
	if s.effects == nil {
		s.effects = map[string]*inflightEffect{}
	}
	s.effects[cl.id] = e
	s.effectsMu.Unlock()
	defer func() {
		s.effectsMu.Lock()
		if s.effects[cl.id] == e {
			delete(s.effects, cl.id)
		}
		esc := e.escAfter
		s.effectsMu.Unlock()
		if esc {
			s.interrupt(context.WithoutCancel(ctx), paneID)
		}
	}()
	if err := s.holds(effectCtx, cl); err != nil {
		return err
	}
	fn(effectCtx)
	return nil
}

// cancelEffect ends the effect running for job id, if any, and reports whether one was: that
// effect then sends the Esc when it returns.
func (s *Service) cancelEffect(id string) bool {
	s.effectsMu.Lock()
	defer s.effectsMu.Unlock()
	e := s.effects[id]
	if e == nil {
		return false
	}
	e.cancel()
	e.escAfter = true
	return true
}

// interrupt sends Esc to the agent in paneID, ending its turn.
func (s *Service) interrupt(ctx context.Context, paneID string) {
	if paneID == "" || s.opts.Herdr == nil {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
	defer cancel()
	_ = s.opts.Herdr.AgentSendKeys(callCtx, paneID, []string{"esc"})
}

// ---- Typing outcomes -----------------------------------------------------------------------

// typed is what is known about a prompt after orch.Prompt.
type typed int

const (
	typedNo    typed = iota // provably nothing was typed (dialog, draft, agent not found)
	typedYes                // typed
	typedMaybe              // handed to herdr and the answer was lost, or failed mid-send
)

// typedOutcome classifies orch.Prompt's result.
func typedOutcome(result orch.PromptResult, err error) typed {
	var blocked *orch.BlockedError
	var draft *orch.DraftError
	switch {
	case err == nil && result.Uncertain:
		return typedMaybe
	case err == nil:
		return typedYes
	case errors.As(err, &blocked), errors.As(err, &draft), errors.Is(err, orch.ErrNotTyped),
		errors.Is(err, orch.ErrEmptyPrompt), errors.Is(err, orch.ErrPromptTooLong):
		return typedNo
	}
	return typedMaybe
}
