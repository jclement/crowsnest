// The disposition check (DESIGN.md 6.3): what happens when a Claude agent holding a job tries to
// end its turn. It is a pure function of the job, its open questions, the Stop hook's input, the
// nudge history and the time, so every rule is a table row in decide_test.go.
//
// The rule: the turn may end only if the job is done, failed, cancelled, blocked or needs_you;
// or there are open questions and the agent has declared (job_update waiting=true) that nothing
// is left that they do not block; or the session holds no job. One addition found by the stop
// test: an answer typed back in the last two minutes, after the agent's last progress, also lets
// the turn end, because Claude queues it and it starts the next turn. Otherwise the stop is refused
// with a reason that tells the agent exactly which call ends the refusal.
//
// Loop control: a nudge counts against the budget only when nothing happened since the previous
// one (no tool call, no job_update). After MaxNudges such nudges the stop is allowed and the job
// is marked stuck: unresponsive, so an agent in a loop costs neither money nor Jeff's attention
// silently. Claude Code's own cap (8 consecutive continuations, reset by a tool call) is a second
// fence behind this one.

package jobs

import (
	"fmt"
	"time"
)

// MaxNudges is the nudge budget: consecutive refused stops with no progress between them.
const MaxNudges = 3

// StopInput is the part of Claude Code's Stop hook input the check reads (verified keys on
// 2.1.332: session_id, stop_hook_active, last_assistant_message, …).
type StopInput struct {
	SessionID            string `json:"session_id"`
	StopHookActive       bool   `json:"stop_hook_active"`
	LastAssistantMessage string `json:"last_assistant_message"`
	TranscriptPath       string `json:"transcript_path"`
}

// NudgeHistory is the supervisor's bookkeeping the check reads.
type NudgeHistory struct {
	Streak         int       // consecutive nudges with no progress between them
	LastNudgeAt    time.Time // zero: never nudged
	LastProgressAt time.Time // last tool call or job_update
	// AnswerIncomingAt is when an outcome of one of the job's questions was last typed back into
	// the agent, or, for one still genuinely on its way, when it was settled (zero: none).
	// Deliveries that failed count for nothing (R2-C04). An answer typed while the turn is ending
	// is queued by Claude and starts the next turn, so a stop just after it is not an undeclared
	// stop.
	AnswerIncomingAt time.Time
	// InputIncoming is true while crowsnest itself is typing into the agent (a brief, a retry or
	// a nudge): that text starts the next turn, so this stop needs no nudge of its own (R2-C10).
	InputIncoming bool
}

// answerGrace is how long after an answer was settled a stop is let through for it.
const answerGrace = 2 * time.Minute

// Rule names which branch of the check decided; it is journalled and asserted in tests.
type Rule string

// Rules.
const (
	RuleNoJob          Rule = "no_job"          // the session holds no job
	RuleTerminal       Rule = "terminal"        // done, failed or cancelled
	RuleDeclared       Rule = "declared"        // blocked, needs_you, in_review or queued
	RuleWaiting        Rule = "waiting"         // open questions, and nothing else workable
	RuleAnswerIncoming Rule = "answer_incoming" // an answer is being typed in; it starts the next turn
	RuleInputIncoming  Rule = "input_incoming"  // crowsnest is typing into the agent right now
	RuleRecovering     Rule = "recovering"      // a limit, an API error or an exit has its own recovery
	RuleAlreadyStuck   Rule = "already_stuck"   // already marked unresponsive; no new progress
	RuleBudgetSpent    Rule = "budget_spent"    // MaxNudges with no progress: allow, escalate
	RuleNudge          Rule = "nudge"           // refused: nothing declared
	RuleNudgeQuestions Rule = "nudge_questions" // refused: questions open, but not declared waiting
)

// Decision is the check's answer.
type Decision struct {
	Allow    bool
	Reason   string // the text Claude is sent back with, when refused
	Escalate bool   // allow, and mark the job stuck: unresponsive and tell Jeff
	Streak   int    // the nudge streak after this decision
	Progress bool   // progress happened since the last nudge (the streak was reset)
	Rule     Rule
}

// Decide applies the disposition rule. job is nil when the session holds no job; openQuestions
// counts the job's open questions.
func Decide(job *Job, openQuestions int, input StopInput, history NudgeHistory, now time.Time) Decision {
	if job == nil {
		return Decision{Allow: true, Rule: RuleNoJob}
	}
	if job.State.Terminal() {
		return Decision{Allow: true, Rule: RuleTerminal, Streak: history.Streak}
	}
	switch job.State {
	case StateBlocked, StateNeedsYou, StateInReview, StateQueued:
		return Decision{Allow: true, Rule: RuleDeclared, Streak: history.Streak}
	}
	if openQuestions > 0 && job.Waiting {
		return Decision{Allow: true, Rule: RuleWaiting, Streak: history.Streak}
	}
	if job.Stuck && (job.Cause == CauseLimit || job.Cause == CauseServerError || job.Cause == CauseExited) {
		// The retry schedule (or the restart) owns this job; a nudge would bypass its back-off,
		// and a retry exhausted has already told Jeff (R2-C09).
		return Decision{Allow: true, Rule: RuleRecovering, Streak: history.Streak}
	}
	if history.InputIncoming {
		return Decision{Allow: true, Rule: RuleInputIncoming, Streak: history.Streak}
	}
	// Times are whole seconds in the store, so an answer in the same second as the last progress
	// (the stop test's job_update(waiting) and its answer, often) counts as after it; the grace
	// is short, and the backstop looks again when it runs out (R2-C04).
	if !history.AnswerIncomingAt.IsZero() && now.Sub(history.AnswerIncomingAt) < answerGrace && !history.AnswerIncomingAt.Before(history.LastProgressAt) {
		return Decision{Allow: true, Rule: RuleAnswerIncoming, Streak: history.Streak}
	}
	progress := !history.LastProgressAt.IsZero() && history.LastProgressAt.After(history.LastNudgeAt)
	streak := history.Streak
	if progress {
		streak = 0
	}
	if job.Stuck && job.Cause == CauseUnresponsive && !progress {
		// Already escalated and nothing has happened since: let it rest rather than nudge again.
		return Decision{Allow: true, Rule: RuleAlreadyStuck, Streak: streak}
	}
	if streak >= MaxNudges {
		return Decision{Allow: true, Escalate: true, Rule: RuleBudgetSpent, Streak: streak}
	}
	decision := Decision{Streak: streak + 1, Progress: progress, Rule: RuleNudge, Reason: NudgeText(job.ID)}
	if openQuestions > 0 {
		decision.Rule = RuleNudgeQuestions
		decision.Reason = NudgeText(job.ID) + " " + fmt.Sprintf(questionsNudge, openQuestions)
	}
	return decision
}

// NudgeText is the refusal (and the herdr-side backstop's prompt): what to do, and which call
// ends it.
func NudgeText(jobID string) string {
	return fmt.Sprintf("You have not finished job %s and have not said why you are stopping. Continue the work. "+
		"If you are finished, call done. If you need the human, call ask and keep going on what it does not block. "+
		"If you are stuck on something else, call job_update with state blocked and the reason.", jobID)
}

const questionsNudge = "You have %d open question(s). If truly nothing is left that they do not block, call job_update with waiting=true and then end your turn; the answers will arrive as messages."
