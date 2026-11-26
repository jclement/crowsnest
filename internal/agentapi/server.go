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
//	POST   /api/v1/questions/{id}/collected  the asker read the settled answer (R1b-C06)
//	POST   /api/v1/say                    speak a sentence in talk mode (M11 voices it)
//	GET    /api/v1/health                 liveness, for the CLI's "is crowsnest running" check
//	…/api/v1/jobs…, /api/v1/hooks/{event}  the job protocol and the hooks (jobs.go, M7)
//
// A request may carry the caller's token (X-Crowsnest-Token, which the client takes from
// CROWSNEST_TOKEN). The socket derives the caller's pane and job from that token alone, through
// the agentid registry; it never believes a pane or job the caller names (R1b N2, N7). A caller
// without a known token is unidentified: it may ask, but its questions have no pane to be typed
// back to (it reads the answer from the long-poll); it may not withdraw questions, create jobs,
// or act on a job. A job verb on job j is allowed when the token was issued for j, or for the pane
// that holds j (the orchestrator, whose tokens name no job; DESIGN.md 6.2).
//
// Quotas (R1b N6), per caller (its pane, or one shared bucket for every unidentified caller):
// asks, says and job creations are rate limited, open questions are capped per caller and in
// total, and concurrent long-polls are capped. Each refusal is a 429 that says what to do. A job
// agent may hold fewer open questions than the orchestrator, and the orchestrator is not counted
// against the inbox-wide cap, so a handful of job agents cannot lock the lead out (R2-L5).
package agentapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/attach"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/instructions"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/ratelimit"
)

// Error codes, the same words the browser API uses.
const (
	codeValidation = "VALIDATION_ERROR"
	codeNotFound   = "NOT_FOUND"
	codeConflict   = "CONFLICT"
	codeForbidden  = "FORBIDDEN"
	codeQuota      = "RATE_LIMITED"
	codeInternal   = "INTERNAL_ERROR"
)

const (
	// MaxSayRunes caps a spoken sentence: one breath, not a paragraph.
	MaxSayRunes = 300
	maxSayBody  = 8 << 10
)

// Quotas on the socket (R1b N6). They are set to stop a looping or hijacked agent, not to meter
// a working one: an agent that asks a question every thirty seconds for an hour is already
// something the human should hear about.
const (
	askBurst          = 10               // asks one caller may make at once
	askRefill         = 30 * time.Second // then one more every this long
	sayBurst          = 10
	sayRefill         = 6 * time.Second
	maxOpenPerCaller  = 30  // open questions the orchestrator, or the unidentified bucket, may have
	maxOpenPerJob     = 10  // open questions one job agent may have (R2-L5)
	maxOpenTotal      = 200 // open questions across every caller but the orchestrator
	maxWaitsPerCaller = 8   // concurrent long-polls one caller may hold
	// createBurst job_create calls one caller may make at once, then one every createRefill
	// (R2-H1; the jobs service caps depth, fan-out and the live total as well).
	createBurst  = 5
	createRefill = time.Minute
	// unidentifiedKey is the one quota bucket every caller without a token shares.
	unidentifiedKey = "unidentified"
)

// Speaker voices a `say` (M11: *voice.Service). It is an interface so this package does not
// depend on talk mode.
type Speaker interface {
	Say(sentence, source, paneID string) string
}

// SayEvent is the data of the `say` hub event published when no Speaker is configured (talk mode
// off): nothing plays it, but the event still shows an agent said something.
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
	identity  *agentid.Registry
	jobs      *jobs.Service
	speaker   Speaker
	hub       *hub.Hub
	logger    *slog.Logger
	version   string
	handler   http.Handler
	// sessionContext is ServerOptions.SessionContext.
	sessionContext func(ctx context.Context, paneID, source string) string

	asks    *ratelimit.Limiter
	says    *ratelimit.Limiter
	creates *ratelimit.Limiter
	waits   *waitCounter

	attachments AgentAttacher
	attachRoots func(ctx context.Context, caller questions.Caller) []string

	instructions LoadedRecorder
	subjectOf    func(ctx context.Context, paneID string) string
}

// ServerOptions configure a Server. Questions is required.
type ServerOptions struct {
	Questions *questions.Service
	Jobs      *jobs.Service // nil: the job routes answer 503
	// Identity resolves callers' tokens. nil: every caller is unidentified.
	Identity *agentid.Registry
	Hub      *hub.Hub // nil: say is accepted and dropped
	// Speaker, when set (talk mode on), voices `say` instead of the plain hub event.
	Speaker Speaker
	Logger  *slog.Logger
	Version string
	Now     func() time.Time // for the quotas; nil means time.Now
	// SessionContext, when set, adds to a SessionStart hook's output for a caller that holds no
	// job token (the orchestrator): its standing context (DESIGN.md 7.9). It returns "" for a
	// pane that is not the orchestrator's.
	SessionContext func(ctx context.Context, paneID, source string) string
	// Attachments stores files agents attach to a question or a result (internal/attach). Nil:
	// an ask or done naming files is refused.
	Attachments AgentAttacher
	// AttachRoots returns the directories a caller may attach files from: its working directory
	// and its job directory (DESIGN.md 8.6). Nil or empty: the caller may attach nothing.
	AttachRoots func(ctx context.Context, caller questions.Caller) []string
	// Instructions records the instruction files agents report loading (the InstructionsLoaded
	// hook, DESIGN.md 21.3). Nil: those hooks are accepted and dropped.
	Instructions LoadedRecorder
	// SubjectOf names the agent behind a pane whose token holds no job ("lead:<slug>" for a
	// project lead). Nil, or "" from it: such a caller is the orchestrator.
	SubjectOf func(ctx context.Context, paneID string) string
}

// LoadedRecorder is what the socket needs of internal/instructions.
type LoadedRecorder interface {
	RecordLoaded(ctx context.Context, subject string, input instructions.LoadedInput) error
}

// AgentAttacher is what the socket needs of internal/attach.
type AgentAttacher interface {
	ImportAgentFiles(ctx context.Context, paths, roots []string, pane string) ([]attach.Attachment, error)
	Adopt(ctx context.Context, ids []string, kind, ownerID, role string) error
	Discard(ctx context.Context, id string) error
}

// NewServer wires the routes.
func NewServer(opts ServerOptions) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	server := &Server{
		questions: opts.Questions, identity: opts.Identity, jobs: opts.Jobs, hub: opts.Hub, speaker: opts.Speaker,
		logger: opts.Logger, version: opts.Version, sessionContext: opts.SessionContext,
		attachments: opts.Attachments, attachRoots: opts.AttachRoots,
		instructions: opts.Instructions, subjectOf: opts.SubjectOf,
		asks:    ratelimit.New(askBurst, askRefill, opts.Now),
		says:    ratelimit.New(sayBurst, sayRefill, opts.Now),
		creates: ratelimit.New(createBurst, createRefill, opts.Now),
		waits:   &waitCounter{active: map[string]int{}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", server.handleHealth)
	mux.HandleFunc("POST /api/v1/questions", server.handleAsk)
	mux.HandleFunc("GET /api/v1/questions", server.handleList)
	mux.HandleFunc("GET /api/v1/questions/{id}", server.handleGet)
	mux.HandleFunc("DELETE /api/v1/questions/{id}", server.handleCancel)
	mux.HandleFunc("POST /api/v1/questions/{id}/collected", server.handleCollected)
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

// callerOf derives the caller from its token. No token, an unknown one, or a lookup failure all
// mean unidentified: such a caller can still ask, and the answer reaches it only by polling.
func (s *Server) callerOf(r *http.Request) questions.Caller {
	if s.identity == nil {
		return questions.Caller{}
	}
	binding, ok, err := s.identity.Resolve(r.Context(), r.Header.Get(agentid.HeaderToken))
	if err != nil {
		s.logger.Warn("resolving an agent token failed; treating the caller as unidentified", "err", err)
	}
	if !ok {
		return questions.Caller{}
	}
	return questions.Caller{PaneID: binding.PaneID, JobID: binding.JobID}
}

// quotaKey is the caller's quota bucket.
func quotaKey(caller questions.Caller) string {
	if caller.PaneID == "" {
		return unidentifiedKey
	}
	return caller.PaneID
}

// waitCounter counts concurrent long-polls per quota key.
type waitCounter struct {
	mu     sync.Mutex
	active map[string]int
}

// enter takes a long-poll slot for key, or reports false when key holds maxWaitsPerCaller.
func (c *waitCounter) enter(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active[key] >= maxWaitsPerCaller {
		return false
	}
	c.active[key]++
	return true
}

func (c *waitCounter) leave(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active[key]--; c.active[key] <= 0 {
		delete(c.active, key)
	}
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
	caller := s.callerOf(r)
	if ok, retry := s.asks.Allow(quotaKey(caller)); !ok {
		if caller.PaneID == "" {
			s.logger.Warn("the shared quota of callers without a token is spent: a local process without CROWSNEST_TOKEN is asking a lot")
		}
		writeQuota(w, retry, "Too many questions in a short time. Wait, then ask one question that covers what you need; carry on with other work meanwhile.")
		return
	}
	var input questions.AskInput
	if !decodeBody(w, r, questions.MaxRequestBytes, &input) {
		return
	}
	if message := s.openQuota(r, caller); message != "" {
		writeQuota(w, time.Minute, message)
		return
	}
	caller, ok := s.askAboutJob(w, r, caller, input.JobID)
	if !ok {
		return
	}
	files, ok := s.importFiles(w, r, caller, input.Attachments)
	if !ok {
		return
	}
	question, err := s.questions.Ask(r.Context(), input, caller)
	if err != nil {
		s.discardFiles(r.Context(), files)
		s.writeQuestionError(w, r, err)
		return
	}
	question.Attachments = s.adoptFiles(r.Context(), files, attach.OwnerQuestion, question.ID, attach.RoleAsk)
	writeData(w, http.StatusCreated, question)
}

// askAboutJob attaches a question to the job it names (R2-C06). A job agent's token already names
// its job, and it may name no other. The orchestrator's token names only its pane: it may name a
// live job it holds (the job's pane is its pane), and the question then belongs to that job, so
// the job's needs_you, its Stop check and its answer callback all see it. ok is false when a
// refusal was written.
func (s *Server) askAboutJob(w http.ResponseWriter, r *http.Request, caller questions.Caller, jobID string) (questions.Caller, bool) {
	jobID = strings.TrimSpace(jobID)
	switch {
	case jobID == "" || jobID == caller.JobID:
		return caller, true
	case caller.JobID != "":
		writeError(w, http.StatusForbidden, codeForbidden, fmt.Sprintf("You hold job %s; a question can only be about your own job. Leave job_id empty.", caller.JobID))
		return caller, false
	case caller.PaneID == "" || s.jobs == nil:
		writeError(w, http.StatusForbidden, codeForbidden, "Only the agent holding a job can ask about it (no valid CROWSNEST_TOKEN came with this request). Leave job_id empty.")
		return caller, false
	}
	job, err := s.jobs.Get(r.Context(), jobID)
	if err != nil || job.PaneID != caller.PaneID || job.State.Terminal() {
		writeError(w, http.StatusForbidden, codeForbidden, fmt.Sprintf("You do not hold a live job %s, so the question cannot be about it. Check the id in your [crowsnest job …] header, or leave job_id empty.", jobID))
		return caller, false
	}
	caller.JobID = job.ID
	return caller, true
}

// openQuota returns a refusal when the caller, or the inbox as a whole, has too many open
// questions; "" lets the ask through.
func (s *Server) openQuota(r *http.Request, caller questions.Caller) string {
	mine, total, err := s.questions.OpenCounts(r.Context(), caller.PaneID)
	if err != nil {
		s.logger.Warn("counting open questions for the quota", "err", err)
		return ""
	}
	// An identified caller whose token names no job is the orchestrator (job agents' tokens
	// always name their job): the lead the human talks to keeps its voice when agents fill the
	// inbox.
	orchestrator := caller.PaneID != "" && caller.JobID == ""
	limit := maxOpenPerCaller
	if caller.JobID != "" {
		limit = maxOpenPerJob
	}
	switch {
	case total >= maxOpenTotal && !orchestrator:
		return fmt.Sprintf("The human's inbox already holds %d open questions. Do not ask more now: carry on with what you can, and mention the question in your reply instead.", total)
	case mine >= limit:
		if caller.PaneID == "" {
			s.logger.Warn("callers without a token hold the most open questions their shared quota allows")
		}
		return fmt.Sprintf("You already have %d open questions. Wait for answers, or withdraw ones that no longer matter, before asking more.", mine)
	}
	return ""
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wait, err := waitParam(r.URL.Query().Get("wait"))
	if err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
		return
	}
	caller := s.callerOf(r)
	var question questions.Question
	if wait > 0 {
		key := quotaKey(caller)
		if !s.waits.enter(key) {
			writeQuota(w, 10*time.Second, fmt.Sprintf("You are already waiting on %d questions at once. Wait on one at a time.", maxWaitsPerCaller))
			return
		}
		defer s.waits.leave(key)
		question, err = s.questions.Wait(r.Context(), id, wait, caller)
	} else {
		question, err = s.questions.Get(r.Context(), id)
	}
	if err != nil {
		s.writeQuestionError(w, r, err)
		return
	}
	// Writing the answer is not delivering it (R1b-C06): the response can reach the socket and
	// still never be read, if the asker's tool call is cancelled in that moment. The client
	// acknowledges a settled answer it has decoded (POST …/collected); until then the sweeper
	// may switch it to typing.
	writeData(w, http.StatusOK, question)
}

// handleCollected is the asker's acknowledgement that it read a settled answer: only then is a
// poll delivery confirmed. Anyone else's acknowledgement changes nothing.
func (s *Server) handleCollected(w http.ResponseWriter, r *http.Request) {
	caller := s.callerOf(r)
	question, err := s.questions.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeQuestionError(w, r, err)
		return
	}
	if question.Status != questions.StatusOpen {
		s.questions.Collected(context.WithoutCancel(r.Context()), question, caller)
	}
	writeData(w, http.StatusOK, map[string]bool{"acknowledged": true})
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := s.questions.List(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		s.writeQuestionError(w, r, err)
		return
	}
	_ = writeJSON(w, http.StatusOK, map[string]any{"data": list, "meta": map[string]int{"total": len(list)}})
}

// handleCancel withdraws a question; only the agent that asked it may (R1b N2). The human cancels
// from the Inbox, which is the browser API.
func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	caller := s.callerOf(r)
	current, err := s.questions.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeQuestionError(w, r, err)
		return
	}
	if caller.PaneID == "" || caller.PaneID != current.SourcePane {
		writeError(w, http.StatusForbidden, codeForbidden, "Only the agent that asked a question can withdraw it. The human can cancel it from the Inbox.")
		return
	}
	question, err := s.questions.Cancel(r.Context(), current.ID, true)
	if err != nil {
		s.writeQuestionError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, question)
}

func (s *Server) handleSay(w http.ResponseWriter, r *http.Request) {
	caller := s.callerOf(r)
	if caller.PaneID == "" {
		// R2-I4: `say` speaks in the orchestrator's voice into the human's ear; a process without
		// a token (not an agent crowsnest started) does not get that channel.
		writeError(w, http.StatusForbidden, codeForbidden, "Only an agent crowsnest started can speak (no valid CROWSNEST_TOKEN came with this request).")
		return
	}
	if ok, retry := s.says.Allow(quotaKey(caller)); !ok {
		writeQuota(w, retry, "Too many spoken lines in a short time; say less, less often.")
		return
	}
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
	switch {
	case s.speaker != nil:
		s.speaker.Say(text, "say", caller.PaneID)
	case s.hub != nil:
		s.hub.Publish(hub.Event{Type: "say", Data: SayEvent{Text: text, PaneID: caller.PaneID, At: time.Now().UTC()}})
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
			"duration", time.Since(started).Round(time.Millisecond))
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

// Unwrap lets http.ResponseController reach the connection (Flush, deadlines).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

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
	_ = writeJSON(w, status, map[string]any{"data": data})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	_ = writeJSON(w, status, map[string]any{"error": map[string]string{"message": message, "code": code}})
}

// writeQuota is a 429 with Retry-After.
func writeQuota(w http.ResponseWriter, retry time.Duration, message string) {
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retry.Round(time.Second)/time.Second))))
	writeError(w, http.StatusTooManyRequests, codeQuota, message)
}

func writeJSON(w http.ResponseWriter, status int, body any) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}
