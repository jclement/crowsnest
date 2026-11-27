package httpd

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jclement/crowsnest/internal/actions"
	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
)

type approvalFixture struct {
	h         *harness
	approvals *approvals.Service
	questions *questions.Service
	tokens    *actions.Tokens
	cookie    *http.Cookie
	low       string
	elevated  string
	dangerous string
}

func newApprovalFixture(t *testing.T) *approvalFixture {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := &approvalFixture{approvals: approvals.New(approvals.Options{Store: db}), questions: questions.New(questions.Options{Store: db}), tokens: &actions.Tokens{Store: db}}
	for _, item := range []struct {
		command string
		into    *string
	}{{"ls", &f.low}, {"git push origin x", &f.elevated}, {"rm -rf build", &f.dangerous}} {
		approval, err := f.approvals.Request(context.Background(), approvals.RequestInput{PaneID: "w1:p1", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"` + item.command + `"}`), Hooked: true})
		if err != nil {
			t.Fatal(err)
		}
		*item.into = approval.ID
	}
	f.h = newHarness(t, harnessOptions{approvals: f.approvals, questions: f.questions, actions: f.tokens})
	f.cookie = f.h.login()
	return f
}

func (f *approvalFixture) decide(id, body string) int {
	return f.h.do(withCookie(request(http.MethodPost, "/api/v1/approvals/"+id+"/decide", body), f.cookie)).Code
}

func (f *approvalFixture) status(t *testing.T, id string) approvals.Status {
	t.Helper()
	approval, err := f.approvals.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return approval.Status
}

func TestApprovalRoutesNeedASession(t *testing.T) {
	f := newApprovalFixture(t)
	for _, r := range []*http.Request{
		request(http.MethodGet, "/api/v1/approvals", ""),
		request(http.MethodGet, "/api/v1/approvals/"+f.low, ""),
		request(http.MethodPost, "/api/v1/approvals/"+f.low+"/decide", `{"decision":"allow"}`),
	} {
		if response := f.h.do(r); response.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d", r.Method, r.URL.Path, response.Code)
		}
	}
}

func TestApprovalDecideIsGuardedAgainstCrossSiteRequests(t *testing.T) {
	f := newApprovalFixture(t)
	noHeader := withCookie(request(http.MethodPost, "/api/v1/approvals/"+f.low+"/decide", `{"decision":"allow"}`), f.cookie)
	noHeader.Header.Del("X-Crowsnest")
	foreign := withCookie(request(http.MethodPost, "/api/v1/approvals/"+f.low+"/decide", `{"decision":"allow"}`), f.cookie)
	foreign.Header.Set("Origin", "https://evil.example")
	for _, r := range []*http.Request{noHeader, foreign} {
		if response := f.h.do(r); response.Code != http.StatusForbidden {
			t.Errorf("cross-site decide: %d", response.Code)
		}
	}
	if f.status(t, f.low) != approvals.StatusPending {
		t.Fatal("a refused request decided the approval")
	}
}

func TestApprovalListAndDecide(t *testing.T) {
	f := newApprovalFixture(t)
	response := f.h.do(withCookie(request(http.MethodGet, "/api/v1/approvals", ""), f.cookie))
	var list struct{ Data []approvals.Approval }
	if response.Code != http.StatusOK || json.NewDecoder(response.Body).Decode(&list) != nil || len(list.Data) != 3 {
		t.Fatalf("list %d %+v", response.Code, list)
	}
	if list.Data[0].Risk != "dangerous" || list.Data[0].Summary != "Bash: rm -rf build" {
		t.Fatalf("newest first, with risk and summary: %+v", list.Data[0])
	}
	if code := f.decide(f.low, `{"decision":"maybe"}`); code != http.StatusBadRequest {
		t.Fatalf("a bad decision: %d", code)
	}
	if code := f.decide(f.low, `{"decision":"allow"}`); code != http.StatusOK || f.status(t, f.low) != approvals.StatusAllowed {
		t.Fatalf("allow: %d", code)
	}
	if code := f.decide(f.low, `{"decision":"deny"}`); code != http.StatusConflict {
		t.Fatalf("a second decision: %d", code)
	}
	if code := f.decide(f.elevated, `{"decision":"deny","note":"not yet"}`); code != http.StatusOK {
		t.Fatalf("deny: %d", code)
	}
	if approval, _ := f.approvals.Get(context.Background(), f.elevated); approval.Note != "not yet" || approval.DecidedVia != "browser" {
		t.Fatalf("deny kept %+v", approval)
	}
	response = f.h.do(withCookie(request(http.MethodGet, "/api/v1/approvals?status=all", ""), f.cookie))
	if json.NewDecoder(response.Body).Decode(&list) != nil || len(list.Data) != 3 {
		t.Fatalf("status=all: %+v", list)
	}
}

func TestDangerousApprovalNeedsConfirm(t *testing.T) {
	f := newApprovalFixture(t)
	response := f.h.do(withCookie(request(http.MethodPost, "/api/v1/approvals/"+f.dangerous+"/decide", `{"decision":"allow"}`), f.cookie))
	if response.Code != http.StatusConflict || decodeError(t, response).Code != "CONFIRM_REQUIRED" {
		t.Fatalf("an unconfirmed dangerous allow: %d", response.Code)
	}
	if f.status(t, f.dangerous) != approvals.StatusPending {
		t.Fatal("still pending")
	}
	if code := f.decide(f.dangerous, `{"decision":"allow","confirm":true}`); code != http.StatusOK {
		t.Fatalf("confirmed: %d", code)
	}
}

func (f *approvalFixture) action(token, action string) int {
	r := request(http.MethodPost, "/api/v1/push/action", `{"token":"`+token+`","action":"`+action+`"}`)
	return f.h.do(r).Code // no cookie: the service worker sends the token alone
}

func TestPushActionAnswersWithTheTokenAlone(t *testing.T) {
	f := newApprovalFixture(t)
	token, err := f.tokens.Mint(context.Background(), "approval:"+f.low, []string{"allow", "deny"})
	if err != nil {
		t.Fatal(err)
	}
	if code := f.action(token, "allow"); code != http.StatusOK {
		t.Fatalf("a valid token: %d", code)
	}
	approval, _ := f.approvals.Get(context.Background(), f.low)
	if approval.Status != approvals.StatusAllowed || approval.DecidedVia != approvals.ViaNotification {
		t.Fatalf("decided %+v", approval)
	}
	if code := f.action(token, "allow"); code != http.StatusForbidden {
		t.Fatalf("a reused token: %d", code)
	}
	if code := f.action("cna_forged", "allow"); code != http.StatusForbidden {
		t.Fatalf("a forged token: %d", code)
	}
	if code := f.action("", "allow"); code != http.StatusForbidden {
		t.Fatalf("no token: %d", code)
	}
}

func TestPushActionNeverAnswersAnElevatedApproval(t *testing.T) {
	f := newApprovalFixture(t)
	// Even with a token (which crowsnest never mints for one), the service refuses.
	for _, id := range []string{f.elevated, f.dangerous} {
		token, _ := f.tokens.Mint(context.Background(), "approval:"+id, []string{"allow"})
		if code := f.action(token, "allow"); code != http.StatusForbidden {
			t.Fatalf("%s from a notification: %d", id, code)
		}
		if f.status(t, id) != approvals.StatusPending {
			t.Fatalf("%s was decided", id)
		}
	}
}

func TestPushActionSnoozeDecidesNothing(t *testing.T) {
	f := newApprovalFixture(t)
	token, _ := f.tokens.Mint(context.Background(), "approval:"+f.low, []string{"snooze"})
	// This fixture has no attention service, so the snooze itself is unavailable; what matters
	// is that a Snooze press can never become a decision.
	if code := f.action(token, "snooze"); code == http.StatusOK && f.status(t, f.low) != approvals.StatusPending {
		t.Fatal("a snooze decided the approval")
	}
	if f.status(t, f.low) != approvals.StatusPending {
		t.Fatal("a snooze must leave the approval pending")
	}
}

func TestPushActionIsGuardedAgainstCrossSiteRequests(t *testing.T) {
	f := newApprovalFixture(t)
	token, _ := f.tokens.Mint(context.Background(), "approval:"+f.low, []string{"allow"})
	r := request(http.MethodPost, "/api/v1/push/action", `{"token":"`+token+`","action":"allow"}`)
	r.Header.Set("Origin", "https://evil.example")
	if code := f.h.do(r).Code; code != http.StatusForbidden {
		t.Fatalf("cross-site: %d", code)
	}
	r = request(http.MethodPost, "/api/v1/push/action", `{"token":"`+token+`","action":"allow"}`)
	r.Header.Del("X-Crowsnest")
	if code := f.h.do(r).Code; code != http.StatusForbidden {
		t.Fatalf("no X-Crowsnest: %d", code)
	}
	if f.status(t, f.low) != approvals.StatusPending {
		t.Fatal("refused requests must not spend the decision")
	}
}

func TestPushActionAnswersAQuestion(t *testing.T) {
	f := newApprovalFixture(t)
	question, err := f.questions.Ask(context.Background(), questions.AskInput{Title: "Ship?", Kind: questions.KindConfirm, Recommend: "yes"}, questions.Caller{})
	if err != nil {
		t.Fatal(err)
	}
	token, _ := f.tokens.Mint(context.Background(), "question:"+question.ID, []string{"yes", "no"})
	if code := f.action(token, "maybe"); code != http.StatusForbidden {
		t.Fatalf("an action outside the token: %d", code)
	}
	token, _ = f.tokens.Mint(context.Background(), "question:"+question.ID, []string{"yes", "no"})
	if code := f.action(token, "no"); code != http.StatusOK {
		t.Fatalf("answer: %d", code)
	}
	answered, _ := f.questions.Get(context.Background(), question.ID)
	if answered.Status != questions.StatusAnswered || answered.AnswerValue != "no" {
		t.Fatalf("answered %+v", answered)
	}
}
