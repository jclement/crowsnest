// Tests for an agent's land request (landrequest.go): only the human's plain Allow on the exact
// question crowsnest asked for that request runs it (R5-S1, R5-C01, R5-C09).

package jobs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/questions"
)

// landRequestRig is a landing rig whose AskHuman is a real questions service on the same database,
// with a hook that runs while the question is being asked (before RequestLand links it).
type landRequestRig struct {
	*rig
	questions *questions.Service
	duringAsk func(questions.Question)
}

func newLandRequestRig(t *testing.T) *landRequestRig {
	t.Helper()
	l := &landRequestRig{}
	l.rig = newLandRig(t, LandingOptions{}, func(o *Options) {
		o.AskHuman = func(ctx context.Context, input questions.AskInput, caller questions.Caller) (questions.Question, error) {
			question, err := l.questions.Ask(ctx, input, caller)
			if err == nil && l.duringAsk != nil {
				l.duringAsk(question)
			}
			return question, err
		}
	})
	l.questions = questions.New(questions.Options{Store: l.db})
	return l
}

func (l *landRequestRig) answer(id, key, note string) questions.Question {
	l.t.Helper()
	question, err := l.questions.Answer(context.Background(), id, questions.AnswerInput{Value: json.RawMessage(`"` + key + `"`), Note: note})
	if err != nil {
		l.t.Fatal(err)
	}
	return question
}

// own is the caller a job's own agent's token stands for.
func own(job Detail) questions.Caller { return questions.Caller{PaneID: "w1:p9", JobID: job.ID} }

// TestLandRequestBecomesApproval: an agent's request is a question; a qualified allow runs
// nothing; a plain Allow on that question lands.
func TestLandRequestBecomesApproval(t *testing.T) {
	l := newLandRequestRig(t)
	ctx := context.Background()
	job := landedJob(t, l.rig, landRepo(t))
	question, err := l.svc.RequestLand(ctx, job.ID, LandMerge, own(job), "tests pass")
	if err != nil {
		t.Fatal(err)
	}
	if question.Context[questions.ContextLandFor] != job.ID || question.Context[questions.ContextLandToken] == "" || !strings.Contains(question.BodyMD, "Merge job/1") {
		t.Fatalf("asked %+v", question)
	}
	if d := l.get(job.ID); d.LandRequest != LandMerge || d.LandState != "" {
		t.Fatal("the request landed by itself")
	}
	if _, err := l.svc.RequestLand(ctx, job.ID, LandMerge, own(job), ""); err == nil {
		t.Fatal("a second request while one waits must be refused")
	}
	l.svc.LandRequestSettled(ctx, job.ID, l.answer(question.ID, "allow", "only after lunch"))
	if d := l.get(job.ID); d.LandState != "" || d.LandRequest != "" {
		t.Fatalf("a qualified allow landed: %+v", d.Job)
	}
	question, err = l.svc.RequestLand(ctx, job.ID, LandMerge, own(job), "")
	if err != nil {
		t.Fatal(err)
	}
	l.svc.LandRequestSettled(ctx, job.ID, l.answer(question.ID, "allow", ""))
	if d := l.get(job.ID); d.LandState != LandedMerged || d.TornDownAt == nil {
		t.Fatalf("allowed request did not land: %+v", d.Job)
	}
}

// TestLandAnswerMustMatchItsRequest is R5-C01 and R5-S1: an answer runs only the request its
// question was asked for. A forged question (one an agent could have made before keys were
// reserved), a stale question from an earlier request, and a question for another action do
// nothing; the honest request still waits.
func TestLandAnswerMustMatchItsRequest(t *testing.T) {
	l := newLandRequestRig(t)
	ctx := context.Background()
	job := landedJob(t, l.rig, landRepo(t))

	stale, err := l.svc.RequestLand(ctx, job.ID, LandDiscard, own(job), "")
	if err != nil {
		t.Fatal(err)
	}
	l.svc.LandRequestSettled(ctx, job.ID, l.answer(stale.ID, "deny", ""))
	honest, err := l.svc.RequestLand(ctx, job.ID, LandMerge, own(job), "")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]questions.Question{
		"forged, no token":    {ID: "q_900", Status: questions.StatusAnswered, AnswerValue: "allow", Context: map[string]string{questions.ContextLandFor: job.ID}},
		"forged, wrong token": {ID: "q_901", Status: questions.StatusAnswered, AnswerValue: "allow", Context: map[string]string{questions.ContextLandFor: job.ID, questions.ContextLandAction: LandMerge, questions.ContextLandToken: "00"}},
		"stale question":      withAnswer(stale, "allow"),
		"other action":        withContext(withAnswer(honest, "allow"), questions.ContextLandAction, LandDiscard),
		"other job":           withContext(withAnswer(honest, "allow"), questions.ContextLandFor, "j_77"),
	}
	for name, question := range cases {
		l.svc.LandRequestSettled(ctx, job.ID, question)
		if d := l.get(job.ID); d.LandState != "" || d.LandRequest != LandMerge {
			t.Fatalf("%s: the answer acted on the request: %+v", name, d.Job)
		}
	}
	l.svc.LandRequestSettled(ctx, job.ID, l.answer(honest.ID, "allow", ""))
	if d := l.get(job.ID); d.LandState != LandedMerged {
		t.Fatalf("the honest answer did not land: %+v", d.Job)
	}
}

func withAnswer(q questions.Question, key string) questions.Question {
	q.Status, q.AnswerValue = questions.StatusAnswered, key
	return q
}

func withContext(q questions.Question, key, value string) questions.Question {
	context := make(map[string]string, len(q.Context))
	for k, v := range q.Context {
		context[k] = v
	}
	context[key] = value
	q.Context = context
	return q
}

// TestLandRequestScopedToCaller is R5-S1: an agent may ask to land only its own job or a child
// it delegated; a token for some other job, or none, is refused.
func TestLandRequestScopedToCaller(t *testing.T) {
	l := newLandRequestRig(t)
	ctx := context.Background()
	job := landedJob(t, l.rig, landRepo(t))
	if _, err := l.svc.RequestLand(ctx, job.ID, LandMerge, questions.Caller{PaneID: "w1:p3", JobID: "j_99"}, ""); err == nil {
		t.Fatal("another job's agent may not ask to land this job")
	}
	if _, err := l.svc.RequestLand(ctx, job.ID, LandMerge, questions.Caller{}, ""); err == nil {
		t.Fatal("a caller without a token may not ask")
	}
	if _, err := l.svc.RequestLand(ctx, job.ID, LandMerge, questions.Caller{PaneID: "w9:p9"}, ""); err == nil {
		t.Fatal("a pane that is no standing agent may not ask")
	}
	if d := l.get(job.ID); d.LandRequest != "" {
		t.Fatalf("a refused request was recorded: %+v", d.Job)
	}
}

// TestLandAnswerBeforeLinkAndAfterRestart is R5-C09: an answer that arrives before RequestLand
// stores the question's id still lands it, and one that settled with no callback (crowsnest was
// down) is applied by the reconciler; a reserved request with no question is dropped.
func TestLandAnswerBeforeLinkAndAfterRestart(t *testing.T) {
	ctx := context.Background()
	t.Run("answered before the link", func(t *testing.T) {
		l := newLandRequestRig(t)
		job := landedJob(t, l.rig, landRepo(t))
		l.duringAsk = func(q questions.Question) { l.svc.LandRequestSettled(ctx, job.ID, l.answer(q.ID, "allow", "")) }
		if _, err := l.svc.RequestLand(ctx, job.ID, LandMerge, own(job), ""); err != nil {
			t.Fatal(err)
		}
		if d := l.get(job.ID); d.LandState != LandedMerged || d.LandRequest != "" {
			t.Fatalf("an early answer was lost: %+v", d.Job)
		}
	})
	t.Run("settled while down", func(t *testing.T) {
		l := newLandRequestRig(t)
		job := landedJob(t, l.rig, landRepo(t))
		question, err := l.svc.RequestLand(ctx, job.ID, LandMerge, own(job), "")
		if err != nil {
			t.Fatal(err)
		}
		l.answer(question.ID, "allow", "") // no callback: the process "died"
		if d := l.get(job.ID); d.LandState != "" {
			t.Fatal("landed without the callback or the reconciler")
		}
		l.svc.ReconcileLandRequests(ctx)
		if d := l.get(job.ID); d.LandState != LandedMerged {
			t.Fatalf("the reconciler did not apply the answer: %+v", d.Job)
		}
		l.svc.ReconcileLandRequests(ctx) // replay is a no-op
	})
	t.Run("never asked", func(t *testing.T) {
		l := newLandRequestRig(t)
		job := landedJob(t, l.rig, landRepo(t))
		if _, err := l.svc.reserveLandRequest(ctx, job.ID, LandMerge); err != nil {
			t.Fatal(err)
		}
		l.svc.ReconcileLandRequests(ctx)
		if d := l.get(job.ID); d.LandRequest != LandMerge {
			t.Fatal("dropped inside the grace")
		}
		l.clock.Advance(2 * landRequestGrace)
		l.svc.ReconcileLandRequests(ctx)
		if d := l.get(job.ID); d.LandRequest != "" {
			t.Fatalf("a request never asked was kept: %+v", d.Job)
		}
		if _, err := l.svc.RequestLand(ctx, job.ID, LandMerge, own(job), ""); err != nil {
			t.Fatalf("a new request after the drop: %v", err)
		}
	})
}
