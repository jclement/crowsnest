// The disposition check (DESIGN.md 6.3): what happens when a Claude agent holding a job tries to
// end its turn. It is a pure function of the job, its open questions, the Stop hook's input, the
// nudge history and the time, so every rule is a table row in decide_test.go.
//
// The rule: the turn may end only if the job is done, failed, cancelled, blocked or needs_you;
// or there are open questions and the agent has declared (job_update waiting=true) that nothing
// is left that they do not block; or the session holds no job. Otherwise the stop is refused
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
}

// Rule names which branch of the check decided; it is journalled and asserted in tests.
type Rule string

// Rules.
const (
	RuleNoJob          Rule = "no_job"          // the session holds no job
	RuleTerminal       Rule = "terminal"        // done, failed or cancelled
	RuleDeclared       Rule = "declared"        // blocked, needs_you, in_review or queued
	RuleWaiting        Rule = "waiting"         // open questions, and nothing else workable
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
// counts the job's open questions; now is unused by the current rules but kept so time-based
// rules (a minimum gap between nudges) stay pure.
func Decide(job *Job, openQuestions int, input StopInput, history NudgeHistory, now time.Time) Decision {
	_ = now
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
