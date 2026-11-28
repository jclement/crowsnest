package attention

import (
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/questions"
)

// TestAcceptanceBudgetAndWaitItems: the states of DESIGN.md 6.15 to 6.18 are items in the one
// queue: a failed check, a budget reached and a spinning agent block; a finished job that needs
// a rebase can wait; one usage-limit item stands for every job it holds; a job with no owner is
// listed once, and not when something else already lists it.
func TestAcceptanceBudgetAndWaitItems(t *testing.T) {
	checkFailed := job("j_1", 1, "w1:p1", jobs.StateNeedsYou, false, jobs.CauseCheck)
	budget := job("j_2", 1, "w2:p1", jobs.StateNeedsYou, false, jobs.CauseBudget)
	spinning := job("j_3", 2, "w3:p1", jobs.StateRunning, true, jobs.CauseSpinning)
	rebase := job("j_4", 1, "", jobs.StateDone, false, "")
	rebase.WorktreeBranch, rebase.AcceptState, rebase.AcceptDetail = "job/4", jobs.AcceptNeedsRebase, "main moved on by 2 commit(s)"
	limited := job("j_5", 2, "w5:p1", jobs.StateRunning, true, jobs.CauseLimit)
	limited.Engine = jobs.EngineClaude
	orphan := job("j_6", 0, "", jobs.StateRunning, false, "")
	humanBlocked := job("j_7", 1, "w7:p1", jobs.StateBlocked, false, "")
	humanBlocked.WakeKind, humanBlocked.BlockedReason = jobs.WakeHuman, "needs the staging password"
	src := Sources{
		Projects: []projects.Project{crow, site},
		Jobs:     []jobs.Job{checkFailed, budget, spinning, rebase, limited, orphan, humanBlocked},
		Limits:   []jobs.LimitState{{Engine: jobs.EngineClaude, Since: ago(10 * time.Minute), ResetAt: testNow.Add(40 * time.Minute), Job: "j_5", Held: 1}},
		Violations: []jobs.Violation{
			{JobID: "j_6", Title: "Job j_6", State: jobs.StateRunning, Rule: "running with no agent holding it", Since: ago(5 * time.Minute)},
			{JobID: "j_1", Title: "Job j_1", State: jobs.StateNeedsYou, Rule: "already listed"},
		},
	}
	items := Build(src, testNow)
	byID := map[string]Item{}
	for _, item := range items {
		byID[item.ID] = item
	}
	for _, want := range []struct {
		id      string
		urgency Urgency
	}{{"job:j_1", Blocking}, {"job:j_2", Blocking}, {"job:j_3", Blocking}, {"job:j_4", CanWait}, {"limit:claude", CanWait}, {"wait:j_6", Blocking}, {"job:j_7", Blocking}} {
		item, ok := byID[want.id]
		if !ok {
			t.Fatalf("%s missing from %v", want.id, ids(items))
		}
		if item.Urgency != want.urgency {
			t.Errorf("%s: urgency %s, want %s", want.id, item.Urgency, want.urgency)
		}
	}
	if _, ok := byID["job:j_5"]; ok {
		t.Error("a job held by the fleet's usage limit is listed by itself as well as by the limit")
	}
	if _, ok := byID["wait:j_1"]; ok {
		t.Error("a violation for a job already listed is listed twice")
	}
	if byID["job:j_4"].Detail != "main moved on by 2 commit(s)" || byID["job:j_4"].Action.Label != "Ask an agent to rebase" {
		t.Errorf("needs rebase: %+v", byID["job:j_4"])
	}
	if byID["job:j_2"].Project == nil || byID["job:j_2"].Project.Name != "Crowsnest" {
		t.Error("the budget item does not name its project")
	}
	if counts := Count(items); counts.Total != len(items) {
		t.Errorf("counts %+v for %d items", counts, len(items))
	}
}

func TestSinceListsAnswersWithTheirReceipt(t *testing.T) {
	settled := ago(5 * time.Minute)
	answered := questions.Question{ID: "q_9", Title: "Ship it?", Kind: questions.KindConfirm, Status: questions.StatusAnswered,
		CreatedAt: ago(time.Hour), SettledAt: &settled, Receipt: questions.ReceiptUncertain}
	events := SinceEvents(SinceSources{Questions: []questions.Question{answered}}, ago(30*time.Minute))
	if len(events) != 1 || events[0].Kind != "answered" || events[0].Ref != "q_9" || events[0].Detail != "delivery uncertain: check whether it arrived" {
		t.Fatalf("events %+v", events)
	}
}

// R10 O-F16: "Since you left" repeated the questions still open in Needs you. A question asked in
// the window is news only once it is no longer waiting there.
func TestSinceLeavesOutQuestionsStillOpen(t *testing.T) {
	settled := ago(5 * time.Minute)
	open := questions.Question{ID: "q_1", Title: "Which colour?", Kind: questions.KindChoice, Status: questions.StatusOpen, CreatedAt: ago(10 * time.Minute)}
	gone := questions.Question{ID: "q_2", Title: "Still needed?", Kind: questions.KindConfirm, Status: questions.StatusCancelled, CreatedAt: ago(10 * time.Minute), SettledAt: &settled}
	events := SinceEvents(SinceSources{Questions: []questions.Question{open, gone}}, ago(30*time.Minute))
	if len(events) != 1 || events[0].Ref != "q_2" || events[0].Kind != "asked" {
		t.Fatalf("events %+v, want only the settled q_2", events)
	}
}
