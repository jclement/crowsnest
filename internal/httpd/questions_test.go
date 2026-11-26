package httpd

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
)

// questionHarness is a harness with auth on and a question service holding one open question.
func questionHarness(t *testing.T) (*harness, *questions.Service, *http.Cookie) {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service := questions.New(questions.Options{Store: db})
	if _, err := service.Ask(context.Background(), questions.AskInput{
		Title: "Ship?", Kind: questions.KindConfirm, Recommend: "yes", Urgency: questions.UrgencyBlocking,
		Files: []questions.File{{Filename: "a.go", Original: "a", Final: "b"}},
	}, questions.Caller{PaneID: "w1:p1"}); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, harnessOptions{questions: service})
	return h, service, h.login()
}

func TestQuestionRoutesNeedASession(t *testing.T) {
	h, _, _ := questionHarness(t)
	for _, r := range []*http.Request{
		request(http.MethodGet, "/api/v1/questions", ""),
		request(http.MethodGet, "/api/v1/questions/q_1", ""),
		request(http.MethodPost, "/api/v1/questions/q_1/answer", `{"answer_value":"yes"}`),
		request(http.MethodDelete, "/api/v1/questions/q_1", ""),
		request(http.MethodGet, "/api/v1/inbox/summary", ""),
	} {
		if response := h.do(r); response.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d", r.Method, r.URL.Path, response.Code)
		}
	}
}

func TestQuestionAnswerIsGuardedAgainstCrossSiteRequests(t *testing.T) {
	h, service, cookie := questionHarness(t)
	noHeader := withCookie(request(http.MethodPost, "/api/v1/questions/q_1/answer", `{"answer_value":"yes"}`), cookie)
	noHeader.Header.Del("X-Crowsnest")
	foreign := withCookie(request(http.MethodPost, "/api/v1/questions/q_1/answer", `{"answer_value":"yes"}`), cookie)
	foreign.Header.Set("Origin", "https://evil.example")
	cancel := withCookie(request(http.MethodDelete, "/api/v1/questions/q_1", ""), cookie)
	cancel.Header.Del("Origin")
	for _, r := range []*http.Request{noHeader, foreign, cancel} {
		if response := h.do(r); response.Code != http.StatusForbidden {
			t.Errorf("cross-site %s: %d", r.Method, response.Code)
		}
	}
	if question, _ := service.Get(context.Background(), "q_1"); question.Status != questions.StatusOpen {
		t.Fatalf("a refused request changed the question: %s", question.Status)
	}
}

func TestQuestionListGetAnswerCancel(t *testing.T) {
	h, service, cookie := questionHarness(t)
	response := h.do(withCookie(request(http.MethodGet, "/api/v1/questions?status=open", ""), cookie))
	var list struct {
		Data []questions.Question `json:"data"`
		Meta ListMeta             `json:"meta"`
	}
	if response.Code != http.StatusOK || json.NewDecoder(response.Body).Decode(&list) != nil || list.Meta.Total != 1 || list.Data[0].Files != nil {
		t.Fatalf("list %d %+v", response.Code, list)
	}
	response = h.do(withCookie(request(http.MethodGet, "/api/v1/questions/q_1", ""), cookie))
	var one struct{ Data questions.Question }
	if json.NewDecoder(response.Body).Decode(&one) != nil || len(one.Data.Files) != 1 {
		t.Fatalf("detail should carry files: %+v", one)
	}
	response = h.do(withCookie(request(http.MethodGet, "/api/v1/inbox/summary", ""), cookie))
	var summary struct{ Data questions.Summary }
	if json.NewDecoder(response.Body).Decode(&summary) != nil || summary.Data.Open != 1 || summary.Data.Blocking != 1 {
		t.Fatalf("summary %+v", summary)
	}

	response = h.do(withCookie(request(http.MethodPost, "/api/v1/questions/q_1/answer", `{"answer_value":"maybe"}`), cookie))
	if response.Code != http.StatusBadRequest || decodeError(t, response).Code != codeValidation {
		t.Fatalf("bad answer: %d", response.Code)
	}
	response = h.do(withCookie(request(http.MethodPost, "/api/v1/questions/q_1/answer", `{"answer_value":"yes","answer_note":"go"}`), cookie))
	if response.Code != http.StatusOK {
		t.Fatalf("answer: %d %s", response.Code, response.Body)
	}
	response = h.do(withCookie(request(http.MethodPost, "/api/v1/questions/q_1/answer", `{"answer_value":"no"}`), cookie))
	if response.Code != http.StatusConflict {
		t.Fatalf("second answer: %d", response.Code)
	}
	if response = h.do(withCookie(request(http.MethodGet, "/api/v1/questions/q_9", ""), cookie)); response.Code != http.StatusNotFound {
		t.Fatalf("missing: %d", response.Code)
	}

	second, _ := service.Ask(context.Background(), questions.AskInput{Title: "Later", Kind: questions.KindAck}, questions.Caller{PaneID: "w1:p1"})
	response = h.do(withCookie(request(http.MethodDelete, "/api/v1/questions/"+second.ID, ""), cookie))
	if response.Code != http.StatusOK {
		t.Fatalf("cancel: %d", response.Code)
	}
	cancelled, _ := service.Get(context.Background(), second.ID)
	if cancelled.Status != questions.StatusCancelled || cancelled.Delivery != questions.DeliveryType {
		t.Fatalf("a cancel from the inbox is typed back to the asker: %+v", cancelled)
	}
}

func TestQuestionRoutesWithoutAService(t *testing.T) {
	h := newHarness(t, harnessOptions{noAuth: true})
	if response := h.do(request(http.MethodGet, "/api/v1/questions", "")); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("no service: %d", response.Code)
	}
}
