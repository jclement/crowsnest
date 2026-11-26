// Package httpd is crowsnest's HTTP server: routing, the security middleware (host, origin,
// auth, headers, request log), the JSON API, the SSE event stream and the embedded SPA.
//
// Request flow, outermost first: request log → security headers → host check → origin check →
// route. Authentication is applied per route, because /health and /auth/login must answer a
// browser that has no session yet. DESIGN.md section 4 is the specification for every check.
package httpd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/push"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/ratelimit"
	"github.com/jclement/crowsnest/internal/schedule"
	"github.com/jclement/crowsnest/internal/spend"
	"github.com/jclement/crowsnest/internal/term"
	"github.com/jclement/crowsnest/internal/transcript"
	"github.com/jclement/crowsnest/internal/voice"
)

const (
	// DefaultHeartbeat is how often an idle SSE stream sends a heartbeat event. Under the
	// common 30-60 s proxy idle timeouts, and often enough for the UI to notice a dead link.
	DefaultHeartbeat = 20 * time.Second
	// shutdownGrace is how long in-flight requests get to finish on SIGINT/SIGTERM.
	shutdownGrace = 10 * time.Second
	// readHeaderTimeout bounds slow-loris header dribbling.
	readHeaderTimeout = 10 * time.Second
)

// Deps are the server's collaborators. Config, Auth, Hub and Logger are required.
type Deps struct {
	Config  config.Config
	Auth    *auth.Service
	Hub     *hub.Hub
	Logger  *slog.Logger
	Version string
	// Web is the built SPA (the contents of web/dist, index.html at its root). Nil is allowed
	// only with Config.WebDev, when Vite serves the UI.
	Web fs.FS
	// Heartbeat overrides DefaultHeartbeat; tests shorten it.
	Heartbeat time.Duration

	// Herdr is the session's client and Fleet its cached view. Either may be nil (tests of
	// other routes); the routes that need them then answer 503.
	Herdr        herdr.Client
	Fleet        *fleet.Cache
	HerdrVersion string // reported by /auth/me

	// Terminals builds the bridges behind /term/tui and /term/pane/{id}: herdr's, or the demo's
	// toy. Nil: both routes answer 503.
	Terminals term.Launcher
	// TermQueueBytes overrides DefaultTermQueueBytes; tests shrink it.
	TermQueueBytes int
	// MaxTerminals overrides DefaultMaxTerminals, the cap on open terminal sockets.
	MaxTerminals int

	// Conversations serves /agents/{target}/messages and Orchestrator the /orchestrator routes
	// (M4). Either may be nil; their routes then answer 503.
	Conversations *transcript.Manager
	Orchestrator  *orch.Orchestrator

	// Questions serves the Inbox's /questions routes (M6). Nil: they answer 503.
	Questions *questions.Service

	// Jobs serves /jobs, /roles and /dirs (M7). Nil: they answer 503.
	Jobs *jobs.Service
	// Voice is talk mode (M11): nil unless an ElevenLabs key is configured, and then every
	// /voice route answers 404.
	Voice *voice.Service
	// Journal is read by GET /journal and written for prompts and answers sent from the web
	// app (M8). Nil: an empty journal, nothing recorded.
	Journal *journal.Journal
	// Poker serves POST /orchestrator/poke (M8). Nil: it answers 503.
	Poker *orch.Poker
	// Push serves the /push routes (Web Push, 8.5). Nil: they answer 503.
	Push *push.Service
	// Schedule serves /schedule (scheduled tasks, 7.8). Nil: those routes answer 503.
	Schedule *schedule.Service
	// Spend serves /spend and /spend/summary (7.11). Nil: they answer 503.
	Spend *spend.Meter
	// Tender serves /orchestrator/context and /orchestrator/compact (7.9). Nil: 503.
	Tender *orch.Tender
	// Updater is self-update (16.1): /health reports what it found, POST /admin/update applies
	// it. Nil (dev builds, --demo, containers): update_available stays false and the route
	// answers 409 with the reason.
	Updater Updater
}

// Server is the HTTP server. Build it with New, then call Run.
type Server struct {
	deps      Deps
	hosts     hostSet
	handler   http.Handler
	heartbeat time.Duration
	// sseWriteTimeout is sseWriteTimeout unless a test shortens it.
	sseWriteTimeout time.Duration
	// openStreams counts SSE handlers still running, so tests can see one return.
	openStreams atomic.Int32
	// loginBodyTimeout is loginBodyTimeout unless a test shortens it.
	loginBodyTimeout time.Duration
	// terminalSlots holds one token per open terminal socket (R1 I5).
	terminalSlots chan struct{}
	// pushTests limits POST /push/test (internal/httpd/push.go).
	pushTests *ratelimit.Limiter

	// stopping is closed when shutdown begins, so long-lived SSE handlers return promptly
	// instead of holding Shutdown open until its grace period runs out.
	stopping     chan struct{}
	stoppingOnce sync.Once
}

// New wires the routes and middleware.
func New(deps Deps) (*Server, error) {
	if deps.Auth == nil || deps.Hub == nil || deps.Logger == nil {
		return nil, errors.New("httpd: Auth, Hub and Logger are required")
	}
	if deps.Web == nil && !deps.Config.WebDev {
		return nil, errors.New("httpd: no web assets; build them (mise run build) or run with --web-dev")
	}
	server := &Server{
		deps:      deps,
		hosts:     newHostSet(deps.Config.HostNames),
		heartbeat: deps.Heartbeat,
		stopping:  make(chan struct{}),

		sseWriteTimeout:  sseWriteTimeout,
		loginBodyTimeout: loginBodyTimeout,
	}
	if server.heartbeat <= 0 {
		server.heartbeat = DefaultHeartbeat
	}
	maxTerminals := deps.MaxTerminals
	if maxTerminals <= 0 {
		maxTerminals = DefaultMaxTerminals
	}
	server.terminalSlots = make(chan struct{}, maxTerminals)
	server.pushTests = ratelimit.New(testBurst, testRefill, nil)
	server.handler = server.middleware(server.routes())
	return server, nil
}

// Handler is the full handler with middleware, for tests and for Run.
func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.Handle("POST /api/v1/admin/update", s.requireAuth(http.HandlerFunc(s.handleAdminUpdate)))
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	mux.Handle("GET /api/v1/auth/me", s.requireAuth(http.HandlerFunc(s.handleMe)))
	mux.Handle("GET /api/v1/events", s.requireAuth(http.HandlerFunc(s.handleEvents)))
	mux.Handle("GET /api/v1/fleet", s.requireAuth(http.HandlerFunc(s.handleFleet)))
	mux.Handle("GET /api/v1/agents/{target}/screen", s.requireAuth(http.HandlerFunc(s.handleScreen)))
	mux.Handle("POST /api/v1/agents/{target}/keys", s.requireAuth(http.HandlerFunc(s.handleKeys)))
	s.registerConversationRoutes(mux)
	s.registerQuestionRoutes(mux)
	s.registerJobRoutes(mux)
	s.registerJournalRoutes(mux)
	s.registerScheduleRoutes(mux)
	s.registerVoiceRoutes(mux)
	s.registerPushRoutes(mux)
	s.registerSpendRoutes(mux)
	mux.Handle("GET /api/v1/term/tui", s.requireAuth(http.HandlerFunc(s.handleTermTUI)))
	mux.Handle("GET /api/v1/term/pane/{pane_id}", s.requireAuth(http.HandlerFunc(s.handleTermPane)))
	mux.HandleFunc("/api/", s.handleAPINotFound)
	mux.Handle("/", s.spaHandler())
	return mux
}

// middleware wraps the router in the checks every request passes, outermost first.
func (s *Server) middleware(next http.Handler) http.Handler {
	next = s.checkOrigin(next)
	next = s.checkHost(next)
	next = securityHeaders(next)
	return s.trustProxies(s.logRequests(next))
}

// ---- Running -----------------------------------------------------------------

// Run listens on every configured address and serves until ctx is cancelled, then shuts down
// gracefully. An optional listener (::1 from `localhost`) that fails to bind is logged and
// skipped; any other bind failure stops the server before it serves anything.
func (s *Server) Run(ctx context.Context) error {
	listeners, err := s.listen(ctx)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: readHeaderTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(s.deps.Logger.Handler(), slog.LevelWarn),
	}
	httpServer.RegisterOnShutdown(s.beginShutdown)

	errs := make(chan error, len(listeners))
	for _, listener := range listeners {
		go func() { errs <- s.serve(httpServer, listener) }()
	}
	select {
	case <-ctx.Done():
	case err := <-errs:
		s.closeAll(httpServer)
		return err
	}
	s.deps.Logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutting down: %w", err)
	}
	return nil
}

func (s *Server) listen(ctx context.Context) ([]net.Listener, error) {
	var listeners []net.Listener
	var listenConfig net.ListenConfig
	limiter := newConnLimiter(maxConnections, maxConnectionsPerPeer)
	for _, target := range s.deps.Config.Listeners {
		listener, err := listenConfig.Listen(ctx, "tcp", target.Addr.String())
		if err != nil && target.Optional {
			s.deps.Logger.Warn("skipping optional listen address", "addr", target.Addr.String(), "err", err)
			continue
		}
		if err != nil {
			for _, open := range listeners {
				_ = open.Close()
			}
			return nil, fmt.Errorf("listening on %s (from --bind %s): %w", target.Addr, target.Source, err)
		}
		listeners = append(listeners, limiter.wrap(listener))
		s.deps.Logger.Info("listening", "url", s.urlFor(listener.Addr()))
	}
	if len(listeners) == 0 {
		return nil, errors.New("no address could be bound; check --bind")
	}
	return listeners, nil
}

func (s *Server) serve(httpServer *http.Server, listener net.Listener) error {
	var err error
	if s.deps.Config.TLS() {
		err = httpServer.ServeTLS(listener, s.deps.Config.TLSCert, s.deps.Config.TLSKey)
	} else {
		err = httpServer.Serve(listener)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) closeAll(httpServer *http.Server) {
	s.beginShutdown()
	_ = httpServer.Close()
}

func (s *Server) beginShutdown() {
	s.stoppingOnce.Do(func() { close(s.stopping) })
}

func (s *Server) urlFor(addr net.Addr) string {
	scheme := "http"
	if s.deps.Config.TLS() {
		scheme = "https"
	}
	return scheme + "://" + addr.String()
}
