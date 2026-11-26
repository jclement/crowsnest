// The journal and the manual poke (DESIGN.md sections 7.4 and 10):
//
//	GET  /journal?kind=poke,skip&before=<id>&limit=<n>  a page of the journal, newest first
//	POST /orchestrator/poke                            "Poke now": {outcome, reason, reasons, …}
//
// The journal is also written from here for what the human does through the web app: prompts
// sent (Chat and job messages) and questions answered or cancelled.

package httpd

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/jclement/crowsnest/internal/journal"
)

// pokeTimeout covers the screen reads plus Prompt's 30 s wait for the turn to start.
const pokeTimeout = 50 * time.Second

func (s *Server) registerJournalRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/journal", s.requireAuth(http.HandlerFunc(s.handleJournal)))
	mux.Handle("POST /api/v1/orchestrator/poke", s.requireAuth(http.HandlerFunc(s.handlePoke)))
}

func (s *Server) handleJournal(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	kinds, err := journal.ParseKinds(query.Get("kind"))
	if err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "kind must be a comma-separated list of: poke, skip, send, answer, restart, alert, backstop, task, upkeep.")
		return
	}
	before, okBefore := optionalInt(query.Get("before"))
	limit, okLimit := optionalInt(query.Get("limit"))
	if !okBefore || !okLimit {
		writeError(w, http.StatusBadRequest, codeValidation, "before and limit must be positive whole numbers.")
		return
	}
	page, err := s.deps.Journal.List(r.Context(), journal.Query{Kinds: kinds, BeforeID: int64(before), Limit: limit})
	if err != nil {
		s.internalError(w, r, "reading the journal", err)
		return
	}
	writeData(w, http.StatusOK, page)
}

// optionalInt parses a non-negative integer query value; empty is 0.
func optionalInt(text string) (int, bool) {
	if text == "" {
		return 0, true
	}
	value, err := strconv.Atoi(text)
	return value, err == nil && value >= 0
}

func (s *Server) handlePoke(w http.ResponseWriter, r *http.Request) {
	if s.deps.Poker == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Pokes are off: the orchestrator is not running in this server.")
		return
	}
	// A poke must not be cut short by a phone locking its screen halfway through the send.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), pokeTimeout)
	defer cancel()
	writeData(w, http.StatusOK, s.deps.Poker.PokeNow(ctx))
}
