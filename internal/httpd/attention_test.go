package httpd

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/attention"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
)

type kvMap struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *kvMap) GetKV(_ context.Context, key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if value, ok := k.m[key]; ok {
		return value, nil
	}
	return "", errors.New("not found")
}

func (k *kvMap) SetKV(_ context.Context, key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[key] = value
	return nil
}

func attentionHarness(t *testing.T) *harness {
	t.Helper()
	open := questions.Question{ID: "q_1", Title: "Ship it?", Kind: questions.KindConfirm, Status: questions.StatusOpen, Urgency: questions.UrgencyBlocking, CreatedAt: time.Now()}
	service := attention.New(attention.Options{
		Questions: func(context.Context) ([]questions.Question, error) { return []questions.Question{open}, nil },
		KV:        &kvMap{m: map[string]string{}},
	})
	done := time.Now().Add(-time.Minute)
	return newHarness(t, harnessOptions{attention: service, now: NowSources{
		Since: func(*http.Request) (attention.SinceSources, error) {
			return attention.SinceSources{Jobs: []jobs.Job{{ID: "j_1", Title: "Done thing", State: jobs.StateDone, SettledAt: &done}}}, nil
		},
	}})
}

// The routes need a session and, for writes, the same-origin header; they answer from the one
// snapshot, keep a snooze, and keep "since you left" per browser.
func TestAttentionRoutes(t *testing.T) {
	h := attentionHarness(t)
	if response := h.do(request(http.MethodGet, "/api/v1/attention", "")); response.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", response.Code)
	}
	cookie := h.login()
	got := decodeData[attention.Snapshot](t, h.do(withCookie(request(http.MethodGet, "/api/v1/attention", ""), cookie)).Body.Bytes())
	if got.Counts.Total != 1 || got.Items[0].ID != "question:q_1" {
		t.Fatalf("snapshot %+v", got)
	}
	forged := withCookie(request(http.MethodPost, "/api/v1/attention/snooze", `{"id":"question:q_1"}`), cookie)
	forged.Header.Del("X-Crowsnest")
	if response := h.do(forged); response.Code != http.StatusForbidden {
		t.Fatalf("snooze without X-Crowsnest: %d", response.Code)
	}
	snoozed := decodeData[attention.Snapshot](t, h.do(withCookie(request(http.MethodPost, "/api/v1/attention/snooze", `{"id":"question:q_1"}`), cookie)).Body.Bytes())
	if snoozed.Counts.Total != 0 || snoozed.Counts.Snoozed != 1 {
		t.Fatalf("after snooze %+v", snoozed.Counts)
	}
	if response := h.do(withCookie(request(http.MethodPost, "/api/v1/attention/snooze", `{"id":"question:q_9"}`), cookie)); response.Code != http.StatusNotFound {
		t.Fatalf("unknown item: %d", response.Code)
	}
	if response := h.do(withCookie(request(http.MethodPost, "/api/v1/attention/dismiss", `{"id":"question:q_1"}`), cookie)); response.Code != http.StatusConflict {
		t.Fatalf("dismiss a question: %d", response.Code)
	}
	if response := h.do(withCookie(request(http.MethodPost, "/api/v1/now/beat", `{"browser":"bad!"}`), cookie)); response.Code != http.StatusBadRequest {
		t.Fatalf("bad browser: %d", response.Code)
	}
	since := decodeData[attention.Since](t, h.do(withCookie(request(http.MethodGet, "/api/v1/now/since?browser=phone-1234", ""), cookie)).Body.Bytes())
	if !since.First || len(since.Events) != 1 {
		t.Fatalf("first visit %+v", since)
	}
	if response := h.do(withCookie(request(http.MethodPost, "/api/v1/now/seen", `{"browser":"phone-1234"}`), cookie)); response.Code != http.StatusOK {
		t.Fatalf("mark seen: %d", response.Code)
	}
	after := decodeData[attention.Since](t, h.do(withCookie(request(http.MethodGet, "/api/v1/now/since?browser=phone-1234", ""), cookie)).Body.Bytes())
	other := decodeData[attention.Since](t, h.do(withCookie(request(http.MethodGet, "/api/v1/now/since?browser=desk-12345", ""), cookie)).Body.Bytes())
	if len(after.Events) != 0 || len(other.Events) != 1 {
		t.Fatalf("per browser: phone %d events, desk %d", len(after.Events), len(other.Events))
	}
	now := decodeData[nowResponse](t, h.do(withCookie(request(http.MethodGet, "/api/v1/now", ""), cookie)).Body.Bytes())
	if len(now.Rows) != 1 || now.Rows[0].Kind != "unfiled" {
		t.Fatalf("now rows %+v", now.Rows)
	}
}

func TestAttentionRoutesWithoutService(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	cookie := h.login()
	if response := h.do(withCookie(request(http.MethodGet, "/api/v1/attention", ""), cookie)); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("no service: %d", response.Code)
	}
}
