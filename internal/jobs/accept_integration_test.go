//go:build integration

// The acceptance test (DESIGN.md 6.15, 6.16, 6.18) with real Claude (haiku) in a throwaway
// crowsnest-test-* herdr session, on the stop test's rig (stop_integration_test.go):
//
//	(a) a builder commits answer.txt with the wrong number and calls done; crowsnest's own check
//	    fails, the output comes back as a [crowsnest check] message, the agent fixes it, and the
//	    job ends accepted with a passing check pinned to its HEAD;
//	(b) a job over a tiny budget (one minute) is put in front of the human (needs_you, budget),
//	    told to stop at a safe point, and its agent ends its turn rather than carrying on;
//	(c) an agent that ends its turn asking the human in prose is refused with "you asked a question
//	    in prose", and turns it into a real question with ask.
//
// Opt-in and paid: -tags integration and CROWSNEST_E2E_CLAUDE=1 (`mise run test:claude`).

package jobs_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
)

// acceptCheck fails unless answer.txt holds exactly 42, and says why on its last line.
const acceptCheck = `v="$(cat answer.txt 2>/dev/null)"; if [ "$v" = 42 ]; then echo "answer.txt is 42: ok"; else echo "FAIL: answer.txt must contain exactly 42, but it contains: $v"; exit 1; fi`

func TestIntegrationClaudeAcceptance(t *testing.T) {
	if os.Getenv("CROWSNEST_E2E_CLAUDE") != "1" {
		t.Skip("real-Claude test: set CROWSNEST_E2E_CLAUDE=1 (it costs a few cents)")
	}
	for _, tool := range []string{"herdr", "claude", "git", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	r := newStopRig(t, func(o *jobs.Options) {
		o.Accept = jobs.AcceptOptions{Enabled: true, DefaultCommand: acceptCheck, MaxRounds: 3, Timeout: 2 * time.Minute}
		o.DefaultModel = "haiku"
	})
	if os.Getenv("CROWSNEST_ACCEPT_ONLY") != "c" {
		t.Run("a_failed_check_goes_back_and_is_fixed", r.testCheckFixed)
		t.Run("b_tiny_budget_stops_and_asks", r.testTinyBudget)
	}
	t.Run("c_prose_question_becomes_ask", r.testProseQuestion)
	if path := os.Getenv("CROWSNEST_ACCEPT_TEST_OUT"); path != "" {
		_ = os.WriteFile(path, []byte(r.out.String()), 0o600)
	}
}

func (r *stopRig) testCheckFixed(t *testing.T) {
	job := r.create(t, "Create a file named answer.txt containing exactly the number 41 (and nothing else). Commit it on your branch "+
		"(git add answer.txt && git commit -m 'Add answer.txt'). Then call your crowsnest done tool with a one-line result, verified as an empty list "+
		"and not_verified as an empty list. If a [crowsnest check] message says the check failed, read the output file it names, fix what it says, "+
		"commit, and call done again: the project's check is the authority on the right value, so do what it says without asking.")
	detail := r.waitFor(t, job.ID, 8*time.Minute, func(d jobs.Detail) bool {
		return d.State.Terminal() || (d.State == jobs.StateNeedsYou && d.Cause == jobs.CauseCheck)
	})
	r.record(t, "(a) check failed then fixed", detail)
	if detail.State != jobs.StateDone || detail.AcceptState != jobs.AcceptAccepted {
		t.Fatalf("the job did not end accepted: %s / %s (%s)", detail.State, detail.AcceptState, detail.AcceptDetail)
	}
	if detail.CheckRounds < 1 || !hasSummary(detail, "Not accepted") {
		t.Fatalf("the first check never failed (rounds %d)", detail.CheckRounds)
	}
	head, err := exec.Command("git", "-C", detail.WorkDir, "rev-parse", "HEAD").Output() //nolint:gosec // the test's worktree
	if err != nil {
		t.Fatal(err)
	}
	if detail.Evidence == nil || detail.Evidence.Status != jobs.CheckPassed || detail.Evidence.Commit != strings.TrimSpace(string(head)) {
		t.Fatalf("the evidence is not a pass at HEAD: %+v", detail.Evidence)
	}
	if body, _ := os.ReadFile(filepath.Join(detail.WorkDir, "answer.txt")); strings.TrimSpace(string(body)) != "42" {
		t.Fatalf("answer.txt is %q", body)
	}
}

func (r *stopRig) testTinyBudget(t *testing.T) {
	job := r.create(t, "Run the shell command `sleep 20` eight times, one Bash call each, and after each one call job_update with how many you have done. "+
		"Then create done.txt, commit it, and call done.")
	minute := 1
	if _, err := r.jobs.SetJobBudget(context.Background(), job.ID, jobs.BudgetInput{Minutes: &minute}); err != nil {
		t.Fatal(err)
	}
	detail := r.waitFor(t, job.ID, 5*time.Minute, func(d jobs.Detail) bool {
		return d.State.Terminal() || (d.State == jobs.StateNeedsYou && d.Cause == jobs.CauseBudget)
	})
	if detail.State != jobs.StateNeedsYou || detail.Cause != jobs.CauseBudget {
		r.record(t, "(b) tiny budget", detail)
		t.Fatalf("the budget did not stop the job: %s / %s", detail.State, detail.Cause)
	}
	// The agent is told to stop at a safe point: its turn ends (the Stop hook allows it, the job
	// needs the human) and it does not finish the work.
	detail = r.waitFor(t, job.ID, 3*time.Minute, func(d jobs.Detail) bool { return turnEndedAfterBudget(d) || d.State.Terminal() })
	r.record(t, "(b) tiny budget", detail)
	if detail.State.Terminal() {
		t.Fatalf("the job carried on to %s past its budget", detail.State)
	}
	if detail.Budget == nil || detail.Budget.State != jobs.BudgetExceeded {
		t.Fatalf("budget view %+v", detail.Budget)
	}
}

// turnEndedAfterBudget reports whether a Stop hook let a turn end after the budget was reached.
func turnEndedAfterBudget(d jobs.Detail) bool {
	var reached time.Time
	for _, event := range d.Timeline {
		if event.Kind == jobs.EventBudget && strings.HasPrefix(event.Summary, "Reached its budget") {
			reached = event.At
		}
	}
	if reached.IsZero() {
		return false
	}
	for _, event := range d.Timeline {
		if event.Kind == jobs.EventHook && strings.HasPrefix(event.Summary, "Turn ended") && !event.At.Before(reached) {
			return true
		}
	}
	return false
}

// proseBrief makes a turn that ends with a question in prose the natural move: it says why the
// tool would spoil it, as the browser journey's brief does (web/e2e/claude/supervise.spec.ts).
const proseBrief = "This is a test of what crowsnest does with a question asked in plain text, so asking it any other way would spoil it. " +
	"Your first reply must be exactly this one sentence, written as plain text, with no tool call before it (not the ask tool, not any other), " +
	"and then end your turn: Should I write hello or hi in the greeting file? " +
	"After that, if crowsnest sends you a message about it, do what that message says."

// proseAttempts is how many fresh jobs the scenario gives when haiku skips the prose turn and asks
// with the tool at once (seen three times in the lead's runs): then the situation under test never
// arose, so another job tries it. What is asserted is never relaxed.
const proseAttempts = 3

func (r *stopRig) testProseQuestion(t *testing.T) {
	var job jobs.Job
	var question questions.Question
	var detail jobs.Detail
	prose := false
	for attempt := 1; attempt <= proseAttempts; attempt++ {
		job = r.create(t, proseBrief)
		question = r.waitForQuestion(t, job.ID)
		detail = r.get(t, job.ID)
		r.record(t, fmt.Sprintf("(c) prose question, attempt %d", attempt), detail)
		nudged := false
		for _, event := range detail.Timeline {
			if event.Kind == jobs.EventNudge && event.Detail != nil {
				nudged = true
				t.Logf("nudge %v; its last message: %q", event.Detail["rule"], event.Detail["last_message"])
				if event.Detail["rule"] == string(jobs.RuleProseQuestion) {
					prose = true
				}
			}
		}
		if prose || nudged || attempt == proseAttempts {
			break // a prose turn happened (refused or not): that is the result
		}
		t.Logf("attempt %d: %s asked with the tool at once (no prose turn); trying a fresh job", attempt, job.ID)
		r.call(http.MethodPost, "/api/v1/jobs/"+job.ID+"/cancel", nil, nil)
		r.call(http.MethodDelete, "/api/v1/questions/"+question.ID, nil, nil)
	}
	if !prose {
		t.Fatalf("the stop was never refused as a prose question; the agent asked %s anyway", question.ID)
	}
	if !strings.Contains(strings.ToLower(question.Title+question.BodyMD), "hello") {
		t.Errorf("the question does not carry the prose question: %q", question.Title)
	}
	if detail.LastMessage == "" {
		t.Error("the agent's last message was not kept")
	}
}

func hasSummary(d jobs.Detail, prefix string) bool {
	for _, event := range d.Timeline {
		if strings.HasPrefix(event.Summary, prefix) {
			return true
		}
	}
	return false
}
