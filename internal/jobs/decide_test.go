package jobs

import (
	"strings"
	"testing"
	"time"
)

func TestDecide(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	earlier, later := now.Add(-time.Minute), now.Add(-30*time.Second)
	running := func(change func(*Job)) *Job {
		job := &Job{ID: "j_12", State: StateRunning}
		if change != nil {
			change(job)
		}
		return job
	}
	cases := []struct {
		name     string
		job      *Job
		open     int
		input    StopInput
		history  NudgeHistory
		allow    bool
		escalate bool
		rule     Rule
		streak   int
	}{
		// The branches of the disposition rule.
		{name: "no job", job: nil, allow: true, rule: RuleNoJob},
		{name: "done", job: running(func(j *Job) { j.State = StateDone }), allow: true, rule: RuleTerminal},
		{name: "failed", job: running(func(j *Job) { j.State = StateFailed }), allow: true, rule: RuleTerminal},
		{name: "cancelled", job: running(func(j *Job) { j.State = StateCancelled }), allow: true, rule: RuleTerminal},
		{name: "blocked", job: running(func(j *Job) { j.State = StateBlocked }), allow: true, rule: RuleDeclared},
		{name: "needs you", job: running(func(j *Job) { j.State = StateNeedsYou }), allow: true, rule: RuleDeclared},
		{name: "queued", job: running(func(j *Job) { j.State = StateQueued }), allow: true, rule: RuleDeclared},
		{name: "open questions and declared waiting", job: running(func(j *Job) { j.Waiting = true }), open: 2, allow: true, rule: RuleWaiting},
		{name: "declared waiting but no open questions", job: running(func(j *Job) { j.Waiting = true }), open: 0, rule: RuleNudge, streak: 1},
		{name: "open questions, not declared", job: running(nil), open: 1, rule: RuleNudgeQuestions, streak: 1},
		{name: "nothing declared: first nudge", job: running(nil), rule: RuleNudge, streak: 1},

		// The nudge budget.
		{name: "second nudge, no progress", job: running(nil), input: StopInput{StopHookActive: true},
			history: NudgeHistory{Streak: 1, LastNudgeAt: later, LastProgressAt: earlier}, rule: RuleNudge, streak: 2},
		{name: "third nudge, no progress", job: running(nil), input: StopInput{StopHookActive: true},
			history: NudgeHistory{Streak: 2, LastNudgeAt: later, LastProgressAt: earlier}, rule: RuleNudge, streak: 3},
		{name: "budget spent: allow and escalate", job: running(nil), input: StopInput{StopHookActive: true},
			history: NudgeHistory{Streak: 3, LastNudgeAt: later, LastProgressAt: earlier}, allow: true, escalate: true, rule: RuleBudgetSpent, streak: 3},
		{name: "progress since the last nudge resets the streak", job: running(nil), input: StopInput{StopHookActive: true},
			history: NudgeHistory{Streak: 3, LastNudgeAt: earlier, LastProgressAt: later}, rule: RuleNudge, streak: 1},
		{name: "stop_hook_active false keeps the streak without progress", job: running(nil), input: StopInput{StopHookActive: false},
			history: NudgeHistory{Streak: 2, LastNudgeAt: later, LastProgressAt: earlier}, rule: RuleNudge, streak: 3},
		{name: "already unresponsive and nothing new: rest", job: running(func(j *Job) { j.Stuck, j.Cause = true, CauseUnresponsive }),
			history: NudgeHistory{Streak: 3, LastNudgeAt: later, LastProgressAt: earlier}, allow: true, rule: RuleAlreadyStuck, streak: 3},
		{name: "unresponsive but progressed: nudge again", job: running(func(j *Job) { j.Stuck, j.Cause = true, CauseUnresponsive }),
			history: NudgeHistory{Streak: 3, LastNudgeAt: earlier, LastProgressAt: later}, rule: RuleNudge, streak: 1},

		// The rows of DESIGN 6.4, as the supervisor leaves the job when a Stop arrives.
		{name: "6.4 dialog: needs_you", job: running(func(j *Job) { j.State, j.Cause = StateNeedsYou, CauseDialog }), allow: true, rule: RuleDeclared},
		{name: "6.4 permissions: still running, nudged", job: running(func(j *Job) { j.Stuck, j.Cause = true, CausePermissions }), rule: RuleNudge, streak: 1},
		{name: "6.4 limit retried with continue, then stops: nudged", job: running(func(j *Job) { j.Stuck, j.Cause = true, CauseLimit }), rule: RuleNudge, streak: 1},
		{name: "6.4 server error retried, then stops: nudged", job: running(func(j *Job) { j.Stuck, j.Cause = true, CauseServerError }), rule: RuleNudge, streak: 1},
		{name: "6.4 auth or billing: needs_you", job: running(func(j *Job) { j.State, j.Cause = StateNeedsYou, CauseAuth }), allow: true, rule: RuleDeclared},
		{name: "6.4 after compaction: same rule", job: running(nil), rule: RuleNudge, streak: 1},
		{name: "6.4 restarted after exit: same rule", job: running(func(j *Job) { j.Cause = CauseExited }), rule: RuleNudge, streak: 1},
		{name: "6.4 silent while working: nudged when it stops", job: running(func(j *Job) { j.Stuck, j.Cause = true, CauseSilent }), rule: RuleNudge, streak: 1},
		{name: "6.4 waiting on Jeff, unanswered", job: running(func(j *Job) { j.State, j.Cause = StateNeedsYou, CauseQuestions }), open: 1, allow: true, rule: RuleDeclared},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(tc.job, tc.open, tc.input, tc.history, now)
			if got.Allow != tc.allow || got.Escalate != tc.escalate || got.Rule != tc.rule {
				t.Fatalf("got allow=%v escalate=%v rule=%s, want allow=%v escalate=%v rule=%s", got.Allow, got.Escalate, got.Rule, tc.allow, tc.escalate, tc.rule)
			}
			if tc.job != nil && !tc.job.State.Terminal() && got.Streak != tc.streak {
				t.Fatalf("streak %d, want %d", got.Streak, tc.streak)
			}
			if !got.Allow && !strings.Contains(got.Reason, "job j_12") {
				t.Fatalf("a refusal must name the job: %q", got.Reason)
			}
			if got.Allow && got.Reason != "" {
				t.Fatalf("an allowed stop has no reason: %q", got.Reason)
			}
		})
	}
}

func TestDecideBudgetSequence(t *testing.T) {
	// Four stops in a row with nothing done between them: three nudges, then the stop is let
	// through and escalated.
	now := time.Unix(1_700_000_000, 0)
	job := &Job{ID: "j_1", State: StateRunning}
	history := NudgeHistory{LastProgressAt: now.Add(-time.Hour)}
	var rules []Rule
	for i := range 4 {
		at := now.Add(time.Duration(i) * time.Second)
		decision := Decide(job, 0, StopInput{StopHookActive: i > 0}, history, at)
		rules = append(rules, decision.Rule)
		history.Streak, history.LastNudgeAt = decision.Streak, at
	}
	want := []Rule{RuleNudge, RuleNudge, RuleNudge, RuleBudgetSpent}
	for i := range want {
		if rules[i] != want[i] {
			t.Fatalf("sequence %v, want %v", rules, want)
		}
	}
}

func TestCheckTransition(t *testing.T) {
	cases := []struct {
		from, to State
		ok       bool
	}{
		{StateQueued, StateRunning, true},
		{StateRunning, StateNeedsYou, true},
		{StateNeedsYou, StateRunning, true},
		{StateRunning, StateBlocked, true},
		{StateBlocked, StateDone, true},
		{StateRunning, StateRunning, true},
		{StateRunning, StateQueued, false},
		{StateDone, StateRunning, false},
		{StateFailed, StateFailed, false},
		{StateCancelled, StateDone, false},
		{StateRunning, State("bogus"), false},
	}
	for _, tc := range cases {
		if err := CheckTransition(tc.from, tc.to); (err == nil) != tc.ok {
			t.Errorf("%s → %s: err %v, want ok=%v", tc.from, tc.to, err, tc.ok)
		}
	}
}
