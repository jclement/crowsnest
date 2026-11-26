// Package agentapi is the agent side of crowsnest (DESIGN.md section 9): an HTTP server on a unix
// socket that agents reach through `crowsnest ask|notify|say|mcp`, the client those commands use,
// and the stdio MCP server that proxies Claude's tool calls to the socket.
//
// The socket is the one core agent API (DESIGN.md decision 12); the CLI and the MCP server are
// thin clients of it. Filesystem permissions are its authentication: the directory is 0700 and
// the socket 0600, so only the user crowsnest runs as can connect. It serves only the agent API,
// never terminals.
//
// Routes are path-compatible with Meat Prompt, so its skill can be pointed here later:
//
//	POST   /api/v1/questions             ask (201, the question)
//	GET    /api/v1/questions/{id}?wait=N  read; with wait, long-poll up to N seconds for the answer
//	GET    /api/v1/questions?status=      list
//	DELETE /api/v1/questions/{id}         withdraw
//	POST   /api/v1/say                    speak a sentence in talk mode (M11 voices it)
//	GET    /api/v1/health                 liveness, for the CLI's "is crowsnest running" check
//	…/api/v1/jobs…, /api/v1/hooks/{event}  the job protocol and the hooks (jobs.go, M7)
//
// Each request may carry the caller's identity, set by the CLI from its environment:
// X-Crowsnest-Pane (HERDR_PANE_ID, where answers are typed back) and X-Crowsnest-Job
// (CROWSNEST_JOB, M7).
package agentapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
)

// Identity headers.
const (
	HeaderPane = "X-Crowsnest-Pane"
	HeaderJob  = "X-Crowsnest-Job"
)

// Error codes, the same words the browser API uses.
const (
	codeValidation = "VALIDATION_ERROR"
	codeNotFound   = "NOT_FOUND"
	codeConflict   = "CONFLICT"
	codeInternal   = "INTERNAL_ERROR"
)

const (
	// MaxSayRunes caps a spoken sentence: one breath, not a paragraph.
	MaxSayRunes = 300
	maxSayBody  = 8 << 10
	// maxIdentityLen bounds the identity headers (pane ids are "w1:p2"-short).
	maxIdentityLen = 128
)

// SayEvent is the data of a `say` hub event: a sentence an agent wants spoken (M11 speaks it when
// talk mode is on in a browser; until then the event is simply published).
type SayEvent struct {
	Text   string    `json:"text"`
	PaneID string    `json:"pane_id,omitempty"`
	At     time.Time `json:"at"`
}

// SayRequest is the body of POST /api/v1/say.
type SayRequest struct {
	Text string `json:"text"`
}

// Server is the agent API's handler. Build with NewServer and serve it with Serve.
type Server struct {
	questions *questions.Service
	jobs      *jobs.Service
	hub       *hub.Hub
	logger    *slog.Logger
	version   string
	handler   http.Handler
}

// ServerOptions configure a Server. Questions is required.
type ServerOptions struct {
	Questions *questions.Service
	Jobs      *jobs.Service // nil: the job routes answer 503
	Hub       *hub.Hub      // nil: say is accepted and dropped
	Logger    *slog.Logger
	Version   string
}

// NewServer wires the routes.
func NewServer(opts ServerOptions) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	server := &Server{questions: opts.Questions, jobs: opts.Jobs, hub: opts.Hub, logger: opts.Logger, version: opts.Version}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", server.handleHealth)
	mux.HandleFunc("POST /api/v1/questions", server.handleAsk)
	mux.HandleFunc("GET /api/v1/questions", server.handleList)
	mux.HandleFunc("GET /api/v1/questions/{id}", server.handleGet)
	mux.HandleFunc("DELETE /api/v1/questions/{id}", server.handleCancel)
	mux.HandleFunc("POST /api/v1/say", server.handleSay)
	server.registerJobRoutes(mux)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, codeNotFound, "No such endpoint on the crowsnest agent socket.")
	})
	server.handler = server.logRequests(mux)
	return server
}

// Handler returns the routes, for tests and Serve.
func (s *Server) Handler() http.Handler { return s.handler }

// callerOf reads the identity headers. Values that are too long or carry control characters are
// dropped rather than stored.
func callerOf(r *http.Request) questions.Caller {
	return questions.Caller{PaneID: identity(r.Header.Get(HeaderPane)), JobID: identity(r.Header.Get(HeaderJob))}
}

func identity(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > maxIdentityLen || strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return ""
	}
	return value
}

// ---- Handlers ------------------------------------------------------------------------------

// HealthResponse is GET /api/v1/health on the socket.
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeData(w, http.StatusOK, HealthResponse{Status: "ok", Version: s.version})
}

func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	var input questions.AskInput
	if !decodeBody(w, r, questions.MaxRequestBytes, &input) {
		return
	}
	question, err := s.questions.Ask(r.Context(), input, callerOf(r))
	if err != nil {
		s.writeQuestionError(w, r, err)
		return
	}
	writeData(w, http.StatusCreated, question)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wait, err := waitParam(r.URL.Query().Get("wait"))
	if err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
		return
	}
	var question questions.Question
	if wait > 0 {
		question, err = s.questions.Wait(r.Context(), id, wait)
	} else {
		question, err = s.questions.Get(r.Context(), id)
	}
	if err != nil {
		s.writeQuestionError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, question)
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := s.questions.List(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		s.writeQuestionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": list, "meta": map[string]int{"total": len(list)}})
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	question, err := s.questions.Cancel(r.Context(), r.PathValue("id"), true)
	if err != nil {
		s.writeQuestionError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, question)
}

func (s *Server) handleSay(w http.ResponseWriter, r *http.Request) {
	var request SayRequest
	if !decodeBody(w, r, maxSayBody, &request) {
		return
	}
	text := strings.Join(strings.Fields(request.Text), " ")
	switch {
	case text == "":
		writeError(w, http.StatusBadRequest, codeValidation, "text is required: one sentence to speak")
		return
	case utf8.RuneCountInString(text) > MaxSayRunes:
		writeError(w, http.StatusBadRequest, codeValidation, "text is longer than "+strconv.Itoa(MaxSayRunes)+" characters; say one sentence")
		return
	}
	if s.hub != nil {
		s.hub.Publish(hub.Event{Type: "say", Data: SayEvent{Text: text, PaneID: callerOf(r).PaneID, At: time.Now().UTC()}})
	}
	writeData(w, http.StatusOK, map[string]bool{"accepted": true})
}

// waitParam parses ?wait= as whole seconds (0 to questions.MaxWait).
func waitParam(text string) (time.Duration, error) {
	if text == "" {
		return 0, nil
	}
	seconds, err := strconv.Atoi(text)
	if err != nil || seconds < 0 {
		return 0, errors.New("wait is a number of seconds")
	}
	return min(time.Duration(seconds)*time.Second, questions.MaxWait), nil
}

// writeQuestionError maps the service's errors to responses.
func (s *Server) writeQuestionError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case questions.IsValidation(err):
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
	case errors.Is(err, questions.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "No such question.")
	case errors.Is(err, questions.ErrNotOpen):
		writeError(w, http.StatusConflict, codeConflict, "The question is already answered, expired or cancelled.")
	case errors.Is(err, context.Canceled):
		// The asker went away mid long-poll; nobody reads the answer.
	default:
		s.logger.Error("agent API request failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "crowsnest failed to handle the request; its log has the detail.")
	}
}

// logRequests logs method, path, status and duration; never bodies (they may hold anything).
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.logger.Debug("agent request", "method", r.Method, "path", r.URL.Path, "status", recorder.status,
			"duration", time.Since(started).Round(time.Millisecond), "pane", callerOf(r).PaneID)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// ---- Envelope ------------------------------------------------------------------------------

func decodeBody(w http.ResponseWriter, r *http.Request, limit int64, into any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, codeValidation, "The request body is too large.")
			return false
		}
		writeError(w, http.StatusBadRequest, codeValidation, "The body must be a JSON object with known fields: "+err.Error())
		return false
	}
	return true
}

func writeData(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, map[string]any{"data": data})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"message": message, "code": code}})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
