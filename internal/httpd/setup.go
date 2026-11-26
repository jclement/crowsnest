// First-run setup over HTTP (DESIGN.md 4.14). A server built with Deps.Setup is the locked
// first-run server: it answers /health, the two setup endpoints and the web app's files, and
// refuses everything else, so no API, event stream or terminal exists before an owner does
// (serve does not even start herdr or the orchestrator until setup is done). The normal server
// answers GET /setup with required=false and POST /setup with 410.
//
// The token travels in the POST body only. The page reads it from the URL fragment, which the
// browser never sends, and the request log records no query string or body, so the token never
// reaches a log line, a Referer or a proxy.

package httpd

import (
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/setup"
)

const (
	codeSetupRequired = "SETUP_REQUIRED"
	codeStarting      = "STARTING"
	codeGone          = "GONE"
	codeBadToken      = "BAD_TOKEN"
)

// setupRoutes are the locked first-run server's only routes.
func (s *Server) setupRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/setup", s.handleSetupInfo)
	mux.HandleFunc("POST /api/v1/setup", s.handleSetup)
	mux.HandleFunc("/api/", s.handleSetupLocked)
	mux.Handle("/", setupSPA{next: s.spaHandler()})
	return mux
}

// handleSetupLocked refuses every other API route, the event stream and the terminal sockets
// included (they are /api/ paths, and this answer comes before any upgrade).
func (s *Server) handleSetupLocked(w http.ResponseWriter, _ *http.Request) {
	if s.deps.Setup.Required() {
		writeError(w, http.StatusForbidden, codeSetupRequired,
			"Crowsnest is not set up yet. Open the setup link from its log to choose a username and password.")
		return
	}
	// Setup is done and the full server is on its way (herdr and the orchestrator are starting).
	w.Header().Set("Retry-After", "2")
	writeError(w, http.StatusServiceUnavailable, codeStarting, "Crowsnest is starting. Try again in a moment.")
}

// setupSPA serves the web app's files, and sends every page except /setup to /setup.
type setupSPA struct{ next http.Handler }

func (h setupSPA) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	isFile := name == "assets" || strings.HasPrefix(name, "assets/") || path.Ext(name) != ""
	if !isFile && name != "setup" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	h.next.ServeHTTP(w, r)
}

// SetupInfo is GET /api/v1/setup.
type SetupInfo struct {
	Required          bool   `json:"required"`
	DefaultName       string `json:"default_name,omitempty"`
	MinPasswordLength int    `json:"min_password_length"`
}

func (s *Server) handleSetupInfo(w http.ResponseWriter, _ *http.Request) {
	info := SetupInfo{MinPasswordLength: auth.MinPasswordLength}
	if s.deps.Setup != nil && s.deps.Setup.Required() {
		info.Required, info.DefaultName = true, s.deps.Setup.DefaultName()
	}
	writeData(w, http.StatusOK, info)
}

// SetupResult is the body of a completed setup.
type SetupResult struct {
	SignedIn bool   `json:"signed_in"`
	Username string `json:"username"`
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if s.deps.Setup == nil {
		writeError(w, http.StatusGone, codeGone, "Crowsnest is already set up. Sign in instead.")
		return
	}
	if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(s.loginBodyTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		s.internalError(w, r, "setting the setup read deadline", err)
		return
	}
	var body setup.Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "Send JSON with token, username, password and name.")
		return
	}
	token, session, err := s.deps.Setup.Submit(r.Context(), body, r.UserAgent(), sessionHost(r))
	var (
		invalid *setup.ValidationError
		limited *setup.RateLimitedError
	)
	switch {
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, codeValidation, capitalise(invalid.Message)+".")
	case errors.As(err, &limited):
		seconds := max(1, int(limited.RetryAfter.Round(time.Second)/time.Second))
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeError(w, http.StatusTooManyRequests, codeRateLimited,
			"Too many setup attempts. Try again in "+strconv.Itoa(seconds)+" seconds.")
	case errors.Is(err, setup.ErrBadToken):
		s.deps.Logger.Warn("wrong setup token", "client", clientIP(r))
		writeError(w, http.StatusForbidden, codeBadToken, "That setup token is not right. Copy the link again from crowsnest's log.")
	case errors.Is(err, setup.ErrDone):
		writeError(w, http.StatusGone, codeGone, "Crowsnest is already set up. Sign in instead.")
	case err != nil:
		s.internalError(w, r, "completing setup", err)
	default:
		s.deps.Logger.Info("setup complete: owner stored, starting crowsnest", "client", clientIP(r))
		setSessionCookie(w, r, token, session.ExpiresAt)
		writeData(w, http.StatusCreated, SetupResult{SignedIn: true, Username: session.Username})
	}
}

func capitalise(message string) string {
	if message == "" {
		return message
	}
	return strings.ToUpper(message[:1]) + message[1:]
}
