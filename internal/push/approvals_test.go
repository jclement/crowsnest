package push

import (
	"context"
	"testing"

	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/risk"
)

type fakeMinter struct{ minted []string }

func (m *fakeMinter) Mint(_ context.Context, item string, _ []string) (string, error) {
	m.minted = append(m.minted, item)
	return "cna_test", nil
}

func approvalWatcher() (*Watcher, *recordingSender, *fakeMinter, *fakeClock) {
	watcher, sender, clock := newTestWatcher()
	minter := &fakeMinter{}
	watcher.opts.Tokens = minter
	return watcher, sender, minter, clock
}

func TestOnlyLowApprovalsCarryActions(t *testing.T) {
	watcher, sender, minter, _ := approvalWatcher()
	for _, level := range []risk.Level{risk.Low, risk.Elevated, risk.Dangerous} {
		watcher.ApprovalRequested(approvals.Approval{ID: "ap_" + string(level), Risk: level, PaneID: "p-" + string(level), AgentName: "builder", Summary: "Bash: ls"})
	}
	got := sender.got()
	if len(got) != 3 {
		t.Fatalf("got %d notices", len(got))
	}
	if len(got[0].Actions) != 2 || got[0].Actions[0].Action != "allow" || got[0].Actions[1].Action != "deny" || got[0].ActionToken != "cna_test" {
		t.Fatalf("a low approval carries Allow and Deny with a token: %+v", got[0])
	}
	for _, notice := range got[1:] {
		if len(notice.Actions) != 0 || notice.ActionToken != "" {
			t.Fatalf("an elevated or dangerous approval must never carry actions: %+v", notice)
		}
		if notice.URL != "/inbox?q=approval%3A"+notice.Tag[len("approval:"):] {
			t.Fatalf("a tap opens the card: %s", notice.URL)
		}
	}
	if len(minter.minted) != 1 || minter.minted[0] != "approval:ap_low" {
		t.Fatalf("a token is minted only for the low one: %v", minter.minted)
	}
}

func TestNoMinterNoActions(t *testing.T) {
	watcher, sender, _ := newTestWatcher()
	watcher.ApprovalRequested(approvals.Approval{ID: "ap_1", Risk: risk.Low, PaneID: "p1", Summary: "Bash: ls"})
	if got := sender.got(); len(got[0].Actions) != 0 || got[0].ActionToken != "" {
		t.Fatalf("without a minter a notice has no buttons: %+v", got[0])
	}
}

func TestQuestionActions(t *testing.T) {
	watcher, sender, minter, _ := approvalWatcher()
	options := []questions.Option{{Key: "yes", Label: "Ship it"}, {Key: "no", Label: "Not yet"}, {Key: "later", Label: "Later"}}
	watcher.QuestionAsked(questions.Question{ID: "q_1", Title: "Ship?", Kind: questions.KindChoice, Options: options, Urgency: questions.UrgencyBlocking, SourcePane: "a"})
	watcher.QuestionAsked(questions.Question{ID: "q_2", Title: "Which?", Kind: questions.KindChoice, Multi: true, Options: options, Urgency: questions.UrgencyBlocking, SourcePane: "b"})
	watcher.QuestionAsked(questions.Question{ID: "q_3", Title: "Explain", Kind: questions.KindManual, Urgency: questions.UrgencyBlocking, SourcePane: "c"})
	got := sender.got()
	if len(got[0].Actions) != 2 || got[0].Actions[0].Action != "yes" || got[0].Actions[1].Title != "Not yet" {
		t.Fatalf("a choice carries its first two options: %+v", got[0].Actions)
	}
	if len(got[1].Actions) != 0 || len(got[2].Actions) != 0 {
		t.Fatalf("multi-select and manual questions carry none: %+v %+v", got[1].Actions, got[2].Actions)
	}
	if len(minter.minted) != 1 || minter.minted[0] != "question:q_1" {
		t.Fatalf("minted %v", minter.minted)
	}
}

func TestApprovalDialogIsNotPushedTwice(t *testing.T) {
	watcher, sender, _, clock := approvalWatcher()
	watcher.ApprovalRequested(approvals.Approval{ID: "ap_1", Risk: risk.Elevated, PaneID: "w1:p1", Summary: "Bash: git push"})
	watcher.fleetChanged(fleet.Fleet{Agents: []fleet.AgentEntry{{PaneID: "w1:p1", NeedsYou: true}}})
	clock.now = clock.now.Add(dialogHold * 2)
	watcher.CheckDialogs(context.Background())
	if got := sender.got(); len(got) != 1 || got[0].Kind != KindApproval {
		t.Fatalf("the approval's own dialog must not be pushed as well: %+v", got)
	}
}
