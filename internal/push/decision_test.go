package push

import (
	"context"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/risk"
)

// describedWatcher is approvalWatcher with a describer that files everything under Website j_15.
func describedWatcher(t *testing.T) (*Watcher, *recordingSender) {
	t.Helper()
	watcher, sender, _, _ := approvalWatcher()
	watcher.opts.Describe = func(_ context.Context, jobID, paneID string) Where {
		return Where{Project: "Website", JobID: "j_15", JobTitle: "Homepage hero"}
	}
	return watcher, sender
}

func TestQuestionNoticeIsDecisionGrade(t *testing.T) {
	watcher, sender := describedWatcher(t)
	options := []questions.Option{{Key: "a", Label: "Short headline"}, {Key: "b", Label: "Long headline"}}
	watcher.QuestionAsked(questions.Question{ID: "q_4", Title: "Which hero copy?", Kind: questions.KindChoice, Options: options, Recommend: "a", Urgency: questions.UrgencyBlocking, SourcePane: "w5:p1"})
	got := sender.got()[0]
	if got.Title != "Website: blocking question" {
		t.Fatalf("title %q", got.Title)
	}
	if got.Body != "j_15 Homepage hero: Which hero copy? · Recommended: Short headline" {
		t.Fatalf("body %q", got.Body)
	}
	if got.URL != "/decide?item=question%3Aq_4" {
		t.Fatalf("url %q", got.URL)
	}
}

func TestGateQuestionsGetNoButtonsAndNoToken(t *testing.T) {
	cases := map[string]questions.Question{
		"land context":  {Context: map[string]string{"land_for": "j_3"}, Options: []questions.Option{{Key: "yes", Label: "Yes"}, {Key: "no", Label: "No"}}, Recommend: "yes"},
		"allow option":  {Options: []questions.Option{{Key: "allow", Label: "Allow: start it"}, {Key: "deny", Label: "Deny"}}, Recommend: "allow"},
		"approve label": {Options: []questions.Option{{Key: "approve", Label: "Approve"}, {Key: "no", Label: "No"}}, Recommend: "approve"},
	}
	for name, question := range cases {
		watcher, sender := describedWatcher(t)
		question.ID, question.Title, question.Kind, question.Urgency, question.SourcePane = "q_9", "Go?", questions.KindChoice, questions.UrgencyBlocking, "w1:p1"
		watcher.QuestionAsked(question)
		got := sender.got()[0]
		if len(got.Actions) != 0 || got.ActionToken != "" {
			t.Errorf("%s: a gate must carry no button and no token: %+v", name, got)
		}
	}
}

func TestConsequentialQuestionsGetSnoozeAlone(t *testing.T) {
	options := []questions.Option{{Key: "yes", Label: "Yes"}, {Key: "no", Label: "No"}}
	cases := map[string]questions.Question{
		"files":        {Files: []questions.File{{Filename: "a.go"}}},
		"long body":    {BodyMD: strings.Repeat("x", 401)},
		"no recommend": {Recommend: "-"},
	}
	for name, question := range cases {
		watcher, sender := describedWatcher(t)
		question.ID, question.Title, question.Kind, question.Urgency, question.SourcePane = "q_9", "Go?", questions.KindConfirm, questions.UrgencyBlocking, "w1:p1"
		if question.Options == nil {
			question.Options = options
		}
		if question.Recommend == "" {
			question.Recommend = "yes"
		}
		watcher.QuestionAsked(question)
		got := sender.got()[0]
		if len(got.Actions) != 1 || got.Actions[0].Action != ActionSnooze {
			t.Errorf("%s: actions %+v, want Snooze alone", name, got.Actions)
		}
	}
}

func TestApprovalNoticeButtons(t *testing.T) {
	long := "Bash: " + strings.Repeat("echo ok && ", 20) + "…"
	cases := []struct {
		name    string
		risk    risk.Level
		summary string
		allow   bool
	}{
		{"low and whole", risk.Low, "Bash: go test ./...", true},
		{"low but cut", risk.Low, long, false},
		{"elevated", risk.Elevated, "Bash: npm install left-pad", false},
		{"dangerous", risk.Dangerous, "Bash: rm -rf build", false},
	}
	for _, c := range cases {
		watcher, sender := describedWatcher(t)
		watcher.ApprovalRequested(approvals.Approval{ID: "ap_2", Risk: c.risk, AgentName: "job-15", Summary: c.summary, PaneID: "w5:p1", JobID: "j_15"})
		got := sender.got()[0]
		hasAllow := len(got.Actions) > 0 && got.Actions[0].Action == "allow"
		if hasAllow != c.allow {
			t.Errorf("%s: actions %+v", c.name, got.Actions)
		}
		switch {
		case c.risk != risk.Low && (len(got.Actions) != 0 || got.ActionToken != ""):
			t.Errorf("%s: elevated or dangerous carries nothing: %+v", c.name, got)
		case c.risk == risk.Low && !hasAllow && (len(got.Actions) != 1 || got.Actions[0].Action != ActionSnooze):
			t.Errorf("%s: a cut summary offers Snooze alone: %+v", c.name, got.Actions)
		}
		if !strings.HasPrefix(got.Title, "Website: ") || got.URL != "/decide?item=approval%3Aap_2" || !strings.HasPrefix(got.Body, "j_15 Homepage hero: job-15 wants Bash:") {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

func TestGateShaped(t *testing.T) {
	if !GateShaped(map[string]string{"approval_for": "j_2"}, nil) || !GateShaped(nil, []string{"Merge"}) {
		t.Fatal("gate context keys and granting options are gates")
	}
	if GateShaped(map[string]string{"repo": "be"}, []string{"yes", "no"}) {
		t.Fatal("an ordinary confirm is not a gate")
	}
}
