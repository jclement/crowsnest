// The conversation and orchestrator endpoints (DESIGN.md section 10):
//
//	GET  /agents/{target}/messages?after=<cursor>&limit=<n>  a page of the agent's conversation
//	POST /agents/{target}/prompt                             {text, force_over_draft}
//	GET  /orchestrator                                       the orchestrator's status
//	POST /orchestrator/restart, /orchestrator/new            restart it; start a new conversation
//
// The routes are registered from one function so that the routes table in server.go gains a
// single line for all of them.

package httpd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/transcript"
)

const (
	// maxPromptBody caps POST /prompt: the text limit plus room for the JSON around it.
	maxPromptBody = orch.MaxPromptBytes + 4<<10
	// promptTimeout covers the screen checks plus agent.prompt's 30 s wait for the turn.
	promptTimeout = 45 * time.Second
	// relaunchTimeout covers /exit, waiting for the shell, and agent.start's own 60 s.
	relaunchTimeout = 2 * time.Minute
)

// Error codes of the send path, beyond the shared ones.
const (
	codeBlocked = "BLOCKED" // a dialog is up; nothing was typed
	codeDraft   = "DRAFT"   // the input box holds a draft; resend with force_over_draft
)

func (s *Server) registerConversationRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/agents/{target}/messages", s.requireAuth(http.HandlerFunc(s.handleMessages)))
	mux.Handle("POST /api/v1/agents/{target}/prompt", s.requireAuth(http.HandlerFunc(s.handlePrompt)))
	mux.Handle("GET /api/v1/orchestrator", s.requireAuth(http.HandlerFunc(s.handleOrchestrator)))
	mux.Handle("POST /api/v1/orchestrator/restart", s.requireAuth(http.HandlerFunc(s.handleOrchestratorRestart)))
	mux.Handle("POST /api/v1/orchestrator/new", s.requireAuth(http.HandlerFunc(s.handleOrchestratorNew)))
}

// errorDetailEnvelope is an error with a machine-readable detail the UI acts on (the dialog
// that blocked a send, the draft that stopped it).
type errorDetailEnvelope struct {
	Error struct {
		errorBody
		Detail any `json:"detail,omitempty"`
	} `json:"error"`
}

func writeErrorDetail(w http.ResponseWriter, status int, code, message string, detail any) {
	var body errorDetailEnvelope
	body.Error.Message, body.Error.Code, body.Error.Detail = message, code, detail
	writeJSON(w, status, body)
}

// ---- Messages ------------------------------------------------------------------------

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if s.deps.Conversations == nil {
		writeHerdrUnavailable(w)
		return
	}
	target := r.PathValue("target")
	if !targetPattern.MatchString(target) {
		writeError(w, http.StatusBadRequest, codeValidation, "The agent target must be an agent name or a pane id.")
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > transcript.MaxPageLimit {
			writeError(w, http.StatusBadRequest, codeValidation, "limit must be a number from 1 to 1000.")
			return
		}
		limit = parsed
	}
	ctx, cancel := context.WithTimeout(r.Context(), herdrCallTimeout)
	defer cancel()
	page, err := s.deps.Conversations.Messages(ctx, target, r.URL.Query().Get("after"), limit)
	switch {
	case errors.Is(err, transcript.ErrNoAgent) || herdr.IsNotFound(err):
		writeError(w, http.StatusNotFound, codeNotFound, "No agent called "+target+" in this session.")
	case err != nil:
		s.herdrError(w, r, "reading the conversation", err)
	default:
		writeData(w, http.StatusOK, page)
	}
}

// ---- Prompt --------------------------------------------------------------------------

// BlockedDetail is the detail of a BLOCKED refusal: the dialog to answer instead.
type BlockedDetail struct {
	PaneID     string       `json:"pane_id"`
	ScreenHash string       `json:"screen_hash"`
	Dialog     fleet.Dialog `json:"dialog"`
}

func (s *Server) handlePrompt(w http.ResponseWriter, r *http.Request) {
	if s.deps.Herdr == nil {
		writeHerdrUnavailable(w)
		return
	}
	target := r.PathValue("target")
	if !targetPattern.MatchString(target) {
		writeError(w, http.StatusBadRequest, codeValidation, "The agent target must be an agent name or a pane id.")
		return
	}
	var body orch.PromptRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPromptBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON like {"text":"…","force_over_draft":false}, at most 32 KB of text.`)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), promptTimeout)
	defer cancel()
	result, err := orch.Prompt(ctx, s.deps.Herdr, target, body)
	if err != nil {
		s.promptError(w, r, err)
		return
	}
	s.deps.Logger.Info("sent prompt", "target", target, "pane", result.PaneID, "bytes", len(body.Text), "started", result.Started, "queued", result.Queued)
	if s.deps.Fleet != nil {
		s.deps.Fleet.Invalidate()
	}
	if s.deps.Orchestrator != nil {
		s.deps.Orchestrator.Watch(ctx) // the status chip flips to working now, not at the next tick
	}
	writeData(w, http.StatusOK, result)
}

func (s *Server) promptError(w http.ResponseWriter, r *http.Request, err error) {
	var blocked *orch.BlockedError
	var draft *orch.DraftError
	switch {
	case errors.Is(err, orch.ErrEmptyPrompt):
		writeError(w, http.StatusBadRequest, codeValidation, "Type something to send.")
	case errors.Is(err, orch.ErrPromptTooLong):
		writeError(w, http.StatusBadRequest, codeValidation, "That is longer than 32 KB; put it in a file and point the agent at it.")
	case errors.As(err, &blocked):
		writeErrorDetail(w, http.StatusConflict, codeBlocked, "The agent is waiting at a dialog, so nothing was sent. Answer the dialog first.",
			BlockedDetail{PaneID: blocked.PaneID, ScreenHash: blocked.ScreenHash, Dialog: blocked.Dialog})
	case errors.As(err, &draft):
		writeErrorDetail(w, http.StatusConflict, codeDraft, "Someone has typed a draft into this agent's input box at the terminal. Send anyway to append to it.", draft.Draft)
	default:
		s.herdrError(w, r, "sending a prompt", err)
	}
}

// ---- Orchestrator --------------------------------------------------------------------

func (s *Server) handleOrchestrator(w http.ResponseWriter, _ *http.Request) {
	if s.deps.Orchestrator == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "The orchestrator is not running in this server.")
		return
	}
	writeData(w, http.StatusOK, s.deps.Orchestrator.Status())
}

func (s *Server) handleOrchestratorRestart(w http.ResponseWriter, r *http.Request) {
	s.relaunchOrchestrator(w, r, "restart", s.deps.Orchestrator.Restart)
}

func (s *Server) handleOrchestratorNew(w http.ResponseWriter, r *http.Request) {
	s.relaunchOrchestrator(w, r, "new conversation", s.deps.Orchestrator.NewConversation)
}

// relaunchOrchestrator runs Restart or NewConversation. The work outlives a dropped request (a
// phone locking its screen must not leave Claude half-stopped), so it runs on a context of its
// own, bounded by relaunchTimeout.
func (s *Server) relaunchOrchestrator(w http.ResponseWriter, r *http.Request, what string, operation func(context.Context) (orch.Status, error)) {
	if s.deps.Orchestrator == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "The orchestrator is not running in this server.")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), relaunchTimeout)
	defer cancel()
	status, err := operation(ctx)
	switch {
	case errors.Is(err, orch.ErrDisabled):
		writeError(w, http.StatusConflict, codeConflict, "The orchestrator is disabled (--no-orchestrator).")
	case err != nil:
		s.deps.Logger.Warn("orchestrator operation failed", "operation", what, "err", err)
		writeError(w, http.StatusBadGateway, codeUnavailable, "Could not "+what+" the orchestrator: "+err.Error())
	default:
		s.deps.Logger.Info("orchestrator relaunched", "operation", what, "session", status.SessionID)
		writeData(w, http.StatusOK, status)
	}
}
