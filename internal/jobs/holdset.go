// Holds (DESIGN.md 6.20, R6-C01): what keeps a job where it is until someone other than its
// agent lets it go, kept apart from the state the agent declares.
//
// A job's state is partly the agent's word (running, blocked, waiting) and partly crowsnest's
// disposition: it reached its budget, its check still fails after the last round, its final review
// objects, it waits for the human's approval, a blocking question is open, a tool call waits for
// approval, the engine is under a usage limit, crowsnest is checking or reviewing it. Before
// holds, those dispositions lived only in the state and cause columns, so any write that changed
// the state (an agent declaring blocked and then running, a hook, a recovery path) could clear
// them. Now each disposition is a Hold in the job's set, with who may lift it:
//
//   - every mutation ends with applyHoldsLocked: a held job is put back in the state its holds
//     imply, whatever the change did to the state column (only a terminal state clears them);
//   - every agent-originated call (job_update in any form, done, fail, job_create, the hooks)
//     passes agentGateLocked first: a verb the holds do not allow is refused with the hold named;
//   - only the human's actions and crowsnest's own recovery lift a hold (liftHoldLocked).

package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// HoldKind is what holds a job.
type HoldKind string

// Hold kinds, in priority order (the first present decides the state and cause shown).
const (
	// HoldApproval: a job an agent asked for beyond its own limits waits, queued, for the human's
	// approval (R2-H1).
	HoldApproval HoldKind = "approval"
	// HoldChecking: crowsnest is running the job's check (6.15); in review until it finishes.
	HoldChecking HoldKind = "checking"
	// HoldReviewing: a review round is running (6.7); in review until the reviewer settles.
	HoldReviewing HoldKind = "reviewing"
	// HoldBudget: the job, or a job above it (From), reached its budget (6.16).
	HoldBudget HoldKind = "budget"
	// HoldCheck: the check still fails after the last round, or could not run (6.15).
	HoldCheck HoldKind = "check"
	// HoldReview: the final review still objects, or could not run (6.7).
	HoldReview HoldKind = "review"
	// HoldApprovalTool: one of the agent's tool calls waits for the human's decision (8.7).
	HoldApprovalTool HoldKind = "approval_tool"
	// HoldQuestions: a blocking question (wait=true) is open; its answer lifts it.
	HoldQuestions HoldKind = "questions"
	// HoldUsageLimit: the engine is under a usage limit (6.17); crowsnest resumes the job after
	// the reset, one at a time, or the human clears it.
	HoldUsageLimit HoldKind = "usage_limit"
)

// holdOrder is the priority of the kinds.
var holdOrder = []HoldKind{HoldApproval, HoldChecking, HoldReviewing, HoldBudget, HoldCheck, HoldReview, HoldApprovalTool, HoldQuestions, HoldUsageLimit}

// AllHoldKinds lists every kind (the table test walks it).
func AllHoldKinds() []HoldKind { return append([]HoldKind{}, holdOrder...) }

// Lifter is who may lift a hold.
type Lifter string

// Lifters. No hold is the agent's to lift: what the agent may change is its declared state.
const (
	LiftHuman     Lifter = "human"
	LiftCrowsnest Lifter = "crowsnest"
)

// Hold is one entry of a job's set.
type Hold struct {
	Kind  HoldKind  `json:"kind"`
	Lift  Lifter    `json:"lift"`
	Why   string    `json:"why"`
	Since time.Time `json:"since"`
	// From is, for a budget hold, the job whose budget ran out ("" for the job's own).
	From string `json:"from,omitempty"`
}

// LifterOf is who lifts a kind: the human decides budgets, failed checks, objecting reviews and
// approvals; crowsnest lifts what its own observation ends (an answer, a settled approval, the
// reset, a finished check or review).
func LifterOf(kind HoldKind) Lifter {
	switch kind {
	case HoldBudget, HoldCheck, HoldReview, HoldApproval:
		return LiftHuman
	}
	return LiftCrowsnest
}

// causeOfHold is the cause a needs_you hold shows.
func causeOfHold(kind HoldKind) Cause {
	switch kind {
	case HoldBudget:
		return CauseBudget
	case HoldCheck:
		return CauseCheck
	case HoldReview:
		return CauseReview
	case HoldApprovalTool:
		return CauseApprovalTool
	case HoldQuestions:
		return CauseQuestions
	case HoldApproval:
		return CauseApproval
	case HoldUsageLimit:
		return CauseLimit
	}
	return CauseNone
}

// holdsOf decodes a record's holds, in priority order.
func holdsOf(r store.JobRecord) []Hold {
	var holds []Hold
	if strings.TrimSpace(r.HoldsJSON) != "" {
		_ = json.Unmarshal([]byte(r.HoldsJSON), &holds)
	}
	rank := map[HoldKind]int{}
	for i, kind := range holdOrder {
		rank[kind] = i
	}
	sort.SliceStable(holds, func(i, j int) bool { return rank[holds[i].Kind] < rank[holds[j].Kind] })
	return holds
}

func setHolds(r *store.JobRecord, holds []Hold) {
	if len(holds) == 0 {
		r.HoldsJSON = "[]"
		return
	}
	body, _ := json.Marshal(holds)
	r.HoldsJSON = string(body)
}

// hasHold reports whether r carries a hold of kind.
func hasHold(r store.JobRecord, kind HoldKind) bool {
	_, ok := holdOf(r, kind)
	return ok
}

// holdOf returns r's hold of kind.
func holdOf(r store.JobRecord, kind HoldKind) (Hold, bool) {
	for _, hold := range holdsOf(r) {
		if hold.Kind == kind {
			return hold, true
		}
	}
	return Hold{}, false
}

// humanHold returns the first hold only the human lifts, if any.
func humanHold(r store.JobRecord) (Hold, bool) {
	for _, hold := range holdsOf(r) {
		if hold.Lift == LiftHuman {
			return hold, true
		}
	}
	return Hold{}, false
}

// placeHoldLocked adds a hold (or refreshes one of the same kind and source), journalling a new
// one (s.mu held). The state follows at the end of the mutation (applyHoldsLocked).
func (s *Service) placeHoldLocked(r *store.JobRecord, c *change, kind HoldKind, why, from string) {
	if State(r.State).Terminal() {
		return
	}
	holds := holdsOf(*r)
	for i := range holds {
		if holds[i].Kind == kind && holds[i].From == from {
			holds[i].Why = oneLine(why, 500)
			setHolds(r, holds)
			return
		}
	}
	holds = append(holds, Hold{Kind: kind, Lift: LifterOf(kind), Why: oneLine(why, 500), Since: s.opts.Now().UTC(), From: from})
	setHolds(r, holds)
	if c != nil {
		c.event(EventHold, "Held ("+string(kind)+", lifted by "+string(LifterOf(kind))+"): "+oneLine(why, 200), map[string]any{"hold": kind, "lift": LifterOf(kind), "from": from})
	}
}

// liftHoldLocked removes r's holds of kind that match (nil: all of that kind), by the human or
// crowsnest, and reports whether any was there. A job shown in needs_you for that hold goes back
// to running unless another hold keeps it; a usage-limit hold clears the stuck flag it set. The
// caller sets any other state the lift means (Land: done; a finished check: what it found).
func liftHoldLocked(r *store.JobRecord, c *change, kind HoldKind, by Lifter, match func(Hold) bool) bool {
	holds := holdsOf(*r)
	kept := holds[:0]
	lifted := false
	for _, hold := range holds {
		if hold.Kind == kind && (match == nil || match(hold)) {
			lifted = true
			continue
		}
		kept = append(kept, hold)
	}
	if !lifted {
		return false
	}
	setHolds(r, kept)
	if c != nil {
		c.event(EventHold, "Hold lifted ("+string(kind)+") by "+string(by), map[string]any{"hold": kind, "by": by})
	}
	if hasHold(*r, kind) {
		return true // another source of the same kind still holds it
	}
	cause := causeOfHold(kind)
	switch {
	case kind == HoldUsageLimit:
		if r.Stuck && Cause(r.Cause) == CauseLimit {
			r.Stuck, r.Cause, r.CauseDetail = false, "", ""
		}
	case State(r.State) == StateNeedsYou && cause != CauseNone && Cause(r.Cause) == cause:
		r.State, r.Cause, r.CauseDetail = string(StateRunning), "", ""
		clearWake(r)
	case State(r.State) == StateQueued && Cause(r.Cause) == CauseHeld && strings.HasPrefix(r.CauseDetail, holdPrefix):
		r.Cause, r.CauseDetail = "", ""
	}
	return true
}

// holdPrefix starts a queued job's start-hold reason when a hold keeps it from starting.
const holdPrefix = "held: "

// applyHoldsLocked puts a held job in the state its holds imply (every mutation, after the change
// and the question reconciliation). A terminal state clears them: nothing is left to hold. A hold
// that does not apply to the job's state yet stays latent: a budget reached while crowsnest checks
// a done holds the job when the check sends it back to work, not before.
func applyHoldsLocked(r *store.JobRecord) {
	holds := holdsOf(*r)
	if State(r.State).Terminal() {
		if len(holds) > 0 {
			setHolds(r, nil)
		}
		return
	}
	if len(holds) == 0 {
		return
	}
	top := holds[0]
	state := State(r.State)
	switch top.Kind {
	case HoldApproval:
		if state == StateQueued {
			r.Cause = string(CauseApproval)
		}
	case HoldChecking, HoldReviewing:
		if state != StateQueued {
			r.State = string(StateInReview)
			if Cause(r.Cause) != CauseNone && !r.Stuck {
				r.Cause, r.CauseDetail = "", ""
			}
		}
	case HoldUsageLimit:
		if state == StateRunning || state == StateBlocked || (state == StateNeedsYou && !needsYouHoldCause(Cause(r.Cause))) {
			r.Stuck, r.Cause, r.CauseDetail = true, string(CauseLimit), top.Why
		}
	default:
		if top.Kind == HoldApprovalTool && state == StateBlocked {
			return // a blocked agent's tool call: the job stays blocked, its own wait (8.7)
		}
		switch state {
		case StateRunning, StateBlocked, StateNeedsYou:
			r.State, r.Cause, r.CauseDetail, r.Stuck = string(StateNeedsYou), string(causeOfHold(top.Kind)), top.Why, false
		case StateQueued:
			if r.PaneID == "" && top.Lift == LiftHuman {
				r.Cause, r.CauseDetail = string(CauseHeld), holdPrefix+top.Why
			}
		}
	}
}

// needsYouHoldCause reports whether a needs_you cause is one a hold shows.
func needsYouHoldCause(cause Cause) bool {
	switch cause {
	case CauseBudget, CauseCheck, CauseReview, CauseApprovalTool, CauseQuestions:
		return true
	}
	return false
}

// ---- The agent's side ----------------------------------------------------------------------

// AgentVerb is one kind of agent-originated change.
type AgentVerb string

// Agent verbs: every way an agent's call reaches a job (the socket, the CLI, MCP and the hooks).
const (
	VerbProgress AgentVerb = "progress"   // job_update with a progress line or waiting
	VerbBlocked  AgentVerb = "blocked"    // job_update state=blocked
	VerbRunning  AgentVerb = "running"    // job_update state=running
	VerbDone     AgentVerb = "done"       // done
	VerbFail     AgentVerb = "fail"       // fail
	VerbCreate   AgentVerb = "job_create" // delegating a child (its spend counts to the job)
	VerbAsk      AgentVerb = "ask"        // asking the human (adds a question, lifts nothing)
	VerbToolUse  AgentVerb = "tool_use"   // the PostToolUse hook
	VerbStop     AgentVerb = "stop"       // the Stop hook ending a turn
)

// AllAgentVerbs lists every verb (the table test walks it).
func AllAgentVerbs() []AgentVerb {
	return []AgentVerb{VerbProgress, VerbBlocked, VerbRunning, VerbDone, VerbFail, VerbCreate, VerbAsk, VerbToolUse, VerbStop}
}

// AgentMay is the table: whether an agent may use verb while a hold of kind is on its job. What
// it may always do is say where it is (progress, ask) and what its hooks report (a tool call, the
// end of a turn); none of those changes the state, and applyHoldsLocked would put it back if one
// did. It may never declare a state, finish or give up: that would move the job out of the state
// its holds imply. It may delegate only while it waits on something crowsnest will end by itself
// (an answer, an approval, the reset); never past a human's hold or while crowsnest checks or
// reviews its done.
func AgentMay(kind HoldKind, verb AgentVerb) bool {
	switch verb {
	case VerbProgress, VerbAsk, VerbToolUse, VerbStop:
		return true
	case VerbCreate:
		return kind == HoldQuestions || kind == HoldApprovalTool || kind == HoldUsageLimit
	}
	return false
}

// effectiveHolds is r's holds plus the questions hold its open blocking questions imply now (the
// stored one follows at the end of the next mutation).
func effectiveHolds(r store.JobRecord, q store.JobQuestionSummary) []Hold {
	holds := holdsOf(r)
	if q.Blocking > 0 && !hasHold(r, HoldQuestions) {
		holds = append(holds, Hold{Kind: HoldQuestions, Lift: LiftCrowsnest, Why: fmt.Sprintf("%d blocking question(s) open", q.Blocking)})
	}
	return holds
}

// agentGateLocked is the one check every agent-originated change passes (s.mu held): a verb the
// job's holds do not allow is refused, naming the hold and who lifts it.
func agentGateLocked(r store.JobRecord, q store.JobQuestionSummary, verb AgentVerb) error {
	if State(r.State).Terminal() {
		return nil // each verb words its own refusal for a finished job
	}
	for _, hold := range effectiveHolds(r, q) {
		if AgentMay(hold.Kind, verb) {
			continue
		}
		return invalid("%s is refused: job %s is held (%s: %s) and only %s can lift that. %s", verbWords(verb), FormatID(r.ID), hold.Kind, oneLine(hold.Why, 160),
			lifterWords(hold.Lift), heldAdvice(hold.Kind))
	}
	return nil
}

// ---- crowsnest's side: automatic prompts (R7-C04) ------------------------------------------

// continuationRefusal is the one gate every automatic prompt that starts a turn passes, checked
// right before it is typed: a timed wake, a dependency's wake, a review's findings, a check's
// findings, a retry, a nudge. It returns why the prompt may not start a turn on job id now ("" when
// it may): the automation is paused, the job is finished, any hold is on it (its own set, the
// questions it has open, a budget above it), or its engine is under the fleet's usage limit. It
// reads the row fresh under the lock (s.mu must not be held), so a hold placed after the caller's
// own read is seen; an unreadable row refuses (fail closed). Not atomic with the keystrokes
// themselves: a hold placed in the milliseconds between this check and herdr typing is not seen.
// ignore names hold kinds the caller is itself lifting (a retry lifts the usage-limit hold).
func (s *Service) continuationRefusal(ctx context.Context, id string, ignore ...HoldKind) string {
	numeric, err := ParseID(id)
	if err != nil {
		return "not a job"
	}
	if s.paused() {
		return pausedText
	}
	s.mu.Lock()
	r, err := s.opts.Store.GetJob(ctx, numeric)
	var q store.JobQuestionSummary
	if err == nil {
		q, err = s.opts.Store.SummarizeJobQuestions(ctx, id)
	}
	s.mu.Unlock()
	if err != nil {
		return "crowsnest could not read the job's holds: " + oneLine(err.Error(), 120)
	}
	if State(r.State).Terminal() {
		return "the job is " + r.State
	}
	for _, hold := range effectiveHolds(r, q) {
		if containsKind(ignore, hold.Kind) {
			continue
		}
		return fmt.Sprintf("held (%s: %s)", hold.Kind, oneLine(hold.Why, 160))
	}
	if why := s.ancestorBudgetHold(ctx, r); why != "" {
		return "held (" + why + ")"
	}
	if held, state := s.limitHeld(ctx, r.Engine); held && !containsKind(ignore, HoldUsageLimit) {
		return "the fleet is under a usage limit (" + limitText(state) + ")"
	}
	return ""
}

// typeContinuation types an automatic message that starts a turn (a budget raise's "carry on",
// "finish the step", a budget notice, a spinning nudge) only if the continuation gate lets the job
// go on now (R7-C04 residual): a budget change is not the human lifting a pause, another hold, a
// budget above the job or the fleet's usage limit. A refused message is not queued: the backstop
// keeps the agent going once nothing holds it. ignore names holds the caller itself is lifting.
func (s *Service) typeContinuation(ctx context.Context, job Job, what, text string, ignore ...HoldKind) {
	if job.PaneID == "" || text == "" {
		return
	}
	if why := s.continuationRefusal(ctx, job.ID, ignore...); why != "" {
		s.noteHeldContinuation(ctx, job.ID, what, why)
		return
	}
	if err := s.sendText(ctx, job.PaneID, job.AgentName, text); err != nil {
		_ = s.Journal(ctx, job.ID, EventHold, "Could not type "+what+" to the agent: "+oneLine(err.Error(), 160), nil)
	}
}

// HeldRecord reports whether a job's row carries any hold or waiting feedback: a standing agent's
// pane holding such a job is not restarted or compacted under it (R7-C04 residual).
func HeldRecord(r store.JobRecord) bool { return len(holdsOf(r)) > 0 || hasFeedback(r) }

// noteHeldContinuation journals, at most every 30 minutes per job, that an automatic prompt waits.
// The pause journals its own waits (Pause.Held), so it is not repeated here.
func (s *Service) noteHeldContinuation(ctx context.Context, id, what, why string) {
	if why == pausedText {
		s.heldWhilePaused(ctx, what+":"+id, what+" for "+id)
		return
	}
	watch := s.watchOf(id)
	if now := s.opts.Now(); now.Sub(watch.heldNoteAt) > 30*time.Minute {
		watch.heldNoteAt = now
		_ = s.Journal(ctx, id, EventHold, "Not typed yet: "+what+" waits, because the job is "+why, nil)
	}
}

func containsKind(kinds []HoldKind, kind HoldKind) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// agentGateByIDLocked reads job id now and applies the gate to verb (s.mu held).
func (s *Service) agentGateByIDLocked(ctx context.Context, id int64, verb AgentVerb) error {
	r, err := s.opts.Store.GetJob(ctx, id)
	if err != nil {
		return fmt.Errorf("reading job %s for its holds: %w", FormatID(id), err) // fail closed
	}
	q, err := s.opts.Store.SummarizeJobQuestions(ctx, FormatID(id))
	if err != nil {
		return err
	}
	return agentGateLocked(r, q, verb)
}

// agentMutate is mutate for an agent-originated change: the gate before (agentGateLocked) and
// after (checkAgentChangeLocked) the change. Every agent verb that changes a job goes through it.
func (s *Service) agentMutate(ctx context.Context, id string, verb AgentVerb, fn func(record *store.JobRecord, c *change) error) (Job, error) {
	return s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if err := agentGateLocked(*r, c.questions, verb); err != nil {
			return err
		}
		before := *r
		if err := fn(r, c); err != nil {
			return err
		}
		return checkAgentChangeLocked(before, *r, verb)
	})
}

// checkAgentChangeLocked is the gate's second half, after the change was applied: the agent's
// change kept every hold it found, and did not finish a held job.
func checkAgentChangeLocked(before, after store.JobRecord, verb AgentVerb) error {
	if State(before.State).Terminal() {
		return nil
	}
	held := holdsOf(before)
	for _, hold := range held {
		if !hasHold(after, hold.Kind) {
			return invalid("%s is refused: it would clear job %s's %s hold, which only %s lifts", verbWords(verb), FormatID(before.ID), hold.Kind, lifterWords(hold.Lift))
		}
	}
	if len(held) > 0 && State(after.State).Terminal() {
		return invalid("%s is refused: job %s is held (%s) and cannot finish until that is lifted", verbWords(verb), FormatID(before.ID), held[0].Kind)
	}
	return nil
}

func verbWords(verb AgentVerb) string {
	switch verb {
	case VerbBlocked:
		return "job_update with state blocked"
	case VerbRunning:
		return "job_update with state running"
	case VerbProgress:
		return "job_update"
	}
	return string(verb)
}

func lifterWords(lift Lifter) string {
	if lift == LiftHuman {
		return "the human"
	}
	return "crowsnest"
}

// heldAdvice says what the agent can do instead.
func heldAdvice(kind HoldKind) string {
	switch kind {
	case HoldBudget:
		return "Report where you got to with job_update (progress) and end your turn; the human raises the budget, lets you finish the step, or stops the job."
	case HoldCheck, HoldReview:
		return "Report with job_update (progress) and end your turn; this waits for the human's decision (a message from them sends you back to work)."
	case HoldChecking, HoldReviewing:
		return "Wait for crowsnest's result; it tells you what to do next."
	case HoldQuestions:
		return "Wait for the answer (or cancel your question); keep reporting with job_update."
	case HoldApprovalTool:
		return "Wait for the human's decision on the tool call."
	case HoldUsageLimit:
		return "End your turn; crowsnest resumes you after the reset."
	}
	return "End your turn and wait."
}
