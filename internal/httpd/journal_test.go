// Tests for GET /journal (paging, the kind filter, validation, auth) and POST
// /orchestrator/poke (the manual poke's result and its journal entry), plus the journal entries
// written for a prompt sent from the web app.

package httpd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
)

const journalEmptyInput = "\x1b[38;5;244m────────────\x1b[39m\n❯ \x1b[7m \x1b[27m\n\x1b[38;5;244m────────────\x1b[39m\n"

type journalRig struct {
	h       *harness
	journal *journal.Journal
	poker   *orch.Poker
	fake    *fake.Fake
	cookie  *http.Cookie
}

func newJournalRig(t *testing.T) *journalRig {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	log := journal.New(db, nil, slog.New(slog.DiscardHandler), nil)
	f := fake.New()
	_, _, pane := f.AddWorkspace("orchestrator", "/o")
	_ = f.AddAgent(pane, orch.Name, "claude", herdr.StatusIdle, "s")
	_ = f.SetScreen(pane, fleet.StripANSI(journalEmptyInput), journalEmptyInput)
	poker := orch.NewPoker(orch.PokerOptions{
		Client: f, KV: db, Journal: log, Logger: slog.New(slog.DiscardHandler),
		Status: func() orch.Status {
			agent, _ := f.AgentGet(context.Background(), orch.Name)
			state := orch.StateIdle
			if agent.AgentStatus == herdr.StatusWorking {
				state = orch.StateWorking
			}
			return orch.Status{State: state, PaneID: pane}
		},
	})
	h := newHarness(t, harnessOptions{journal: log, poker: poker, herdr: f})
	return &journalRig{h: h, journal: log, poker: poker, fake: f, cookie: h.login()}
}

func (r *journalRig) page(t *testing.T, query string) journal.Page {
	t.Helper()
	response := r.h.do(withCookie(request(http.MethodGet, "/api/v1/journal"+query, ""), r.cookie))
	if response.Code != http.StatusOK {
		t.Fatalf("GET /journal%s: %d %s", query, response.Code, response.Body)
	}
	var body struct{ Data journal.Page }
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}

func TestJournalRoutesNeedASession(t *testing.T) {
	r := newJournalRig(t)
	for _, req := range []*http.Request{request(http.MethodGet, "/api/v1/journal", ""), request(http.MethodPost, "/api/v1/orchestrator/poke", "")} {
		if response := r.h.do(req); response.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d", req.Method, req.URL.Path, response.Code)
		}
	}
}

func TestJournalPagesNewestFirstAndFilters(t *testing.T) {
	r := newJournalRig(t)
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		kind := journal.KindPoke
		if i%2 == 0 {
			kind = journal.KindSkip
		}
		r.journal.Add(ctx, kind, fmt.Sprintf("entry %d", i), map[string]any{"n": i})
	}
	first := r.page(t, "?limit=2")
	if first.Total != 5 || len(first.Entries) != 2 || first.Entries[0].Summary != "entry 5" || first.Entries[1].Summary != "entry 4" || first.NextBefore == 0 {
		t.Fatalf("first page %+v", first)
	}
	second := r.page(t, fmt.Sprintf("?limit=2&before=%d", first.NextBefore))
	if len(second.Entries) != 2 || second.Entries[0].Summary != "entry 3" {
		t.Fatalf("second page %+v", second)
	}
	last := r.page(t, fmt.Sprintf("?limit=2&before=%d", second.NextBefore))
	if len(last.Entries) != 1 || last.NextBefore != 0 || last.Entries[0].Detail["n"] != float64(1) {
		t.Fatalf("last page %+v", last)
	}
	skips := r.page(t, "?kind=skip")
	if skips.Total != 2 || len(skips.Entries) != 2 || skips.Entries[0].Kind != journal.KindSkip {
		t.Fatalf("skips %+v", skips)
	}
	both := r.page(t, "?kind=skip,poke")
	if both.Total != 5 {
		t.Fatalf("both kinds %+v", both)
	}
}

func TestJournalRejectsBadQueries(t *testing.T) {
	r := newJournalRig(t)
	for _, query := range []string{"?kind=bogus", "?limit=-1", "?before=x"} {
		response := r.h.do(withCookie(request(http.MethodGet, "/api/v1/journal"+query, ""), r.cookie))
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", query, response.Code)
		}
	}
}

func TestPokeNowRoute(t *testing.T) {
	r := newJournalRig(t)
	response := r.h.do(withCookie(request(http.MethodPost, "/api/v1/orchestrator/poke", ""), r.cookie))
	var body struct{ Data orch.PokeResult }
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != http.StatusOK || body.Data.Outcome != orch.OutcomeSkipped || body.Data.Reason != "nothing is due" {
		t.Fatalf("nothing due: %d %s", response.Code, response.Body)
	}
	r.poker.Add(context.Background(), orch.Reason{Key: "manual:1", Source: orch.SourceManual, Text: "a test reason"})
	response = r.h.do(withCookie(request(http.MethodPost, "/api/v1/orchestrator/poke", ""), r.cookie))
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	if body.Data.Outcome != orch.OutcomeSent || len(body.Data.Reasons) != 1 {
		t.Fatalf("sent: %s", response.Body)
	}
	prompts := r.fake.Prompts()
	if len(prompts) != 1 || !strings.HasPrefix(prompts[0].Text, "[crowsnest poke ") || !strings.HasSuffix(prompts[0].Text, "\n- a test reason") {
		t.Fatalf("prompts %+v", prompts)
	}
	page := r.page(t, "")
	if page.Total != 2 || page.Entries[0].Kind != journal.KindPoke || page.Entries[1].Kind != journal.KindSkip {
		t.Fatalf("journal %+v", page)
	}
}

func TestPokeRouteWithoutAPoker(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	response := h.do(withCookie(request(http.MethodPost, "/api/v1/orchestrator/poke", ""), h.login()))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("no poker: %d", response.Code)
	}
	if response := h.do(withCookie(request(http.MethodGet, "/api/v1/journal", ""), h.login())); response.Code != http.StatusOK {
		t.Fatalf("no journal: %d", response.Code)
	}
}

func TestPromptFromTheWebAppIsJournalled(t *testing.T) {
	r := newJournalRig(t)
	response := r.h.do(withCookie(request(http.MethodPost, "/api/v1/agents/orchestrator/prompt", `{"text":"run the tests"}`), r.cookie))
	if response.Code != http.StatusOK {
		t.Fatalf("prompt: %d %s", response.Code, response.Body)
	}
	page := r.page(t, "?kind=send")
	if page.Total != 1 || page.Entries[0].Summary != "You sent orchestrator: run the tests" {
		t.Fatalf("journal %+v", page)
	}
}
