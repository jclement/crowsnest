// The JSON API (DESIGN.md section 9): the response envelope, session cookies, and the health
// and auth handlers. Every response is {"data": …}, lists add "meta", errors are
// {"error": {"message", "code"}}.

package httpd

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/auth"
)

// Error codes, shared with the web client.
const (
	codeValidation   = "VALIDATION_ERROR"
	codeUnauthorized = "UNAUTHORIZED"
	codeForbidden    = "FORBIDDEN"
	codeNotFound     = "NOT_FOUND"
	codeConflict     = "CONFLICT"
	codeRateLimited  = "RATE_LIMITED"
	codeInternal     = "INTERNAL_ERROR"
	codeUnavailable  = "UNAVAILABLE" // herdr is not answering
)

// Session cookie names. Over HTTPS the cookie carries the __Host- prefix, which the browser
// accepts only with Secure, Path=/ and no Domain, so nothing on a sibling or parent domain can
// set or shadow it (cookie tossing). Over plain HTTP the prefix is not available (it requires
// Secure), and only the plain name is read; over HTTPS only the prefixed one is (R1 M2).
const (
	SessionCookie       = "crowsnest_session"
	SecureSessionCookie = "__Host-crowsnest_session"
)

// sessionCookieName is the cookie name for this request's scheme.
func sessionCookieName(r *http.Request) string {
	if isHTTPS(r) {
		return SecureSessionCookie
	}
	return SessionCookie
}

// sessionHost is the Host:port a session is bound to: the request's Host, lower-cased, without
// a trailing dot, with the scheme's default port filled in so "host" and "host:80" agree.
func sessionHost(r *http.Request) string {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		host, port = r.Host, ""
	}
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if port == "" {
		port = "80"
		if isHTTPS(r) {
			port = "443"
		}
	}
	return net.JoinHostPort(host, port)
}

// maxLoginBody caps the login request; a username and password never need more.
const maxLoginBody = 4 << 10

// ---- Envelope ----------------------------------------------------------------

type dataEnvelope struct {
	Data any `json:"data"`
	Meta any `json:"meta,omitempty"`
}

type errorBody struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

// ListMeta is the meta block for list responses.
type ListMeta struct {
	Total int `json:"total"`
}

// writeData sends {"data": data}.
func writeData(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, dataEnvelope{Data: data})
}

// writeList sends {"data": items, "meta": meta}.
func writeList(w http.ResponseWriter, items any, meta ListMeta) {
	writeJSON(w, http.StatusOK, dataEnvelope{Data: items, Meta: meta})
}

// writeError sends {"error": {"message", "code"}}. message is shown to the user, so it says what
// to do, never what the code hit.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: errorBody{Message: message, Code: code}})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// internalError logs the detail and tells the user only that something broke.
func (s *Server) internalError(w http.ResponseWriter, r *http.Request, doing string, err error) {
	s.deps.Logger.Error("request failed", "doing", doing, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, codeInternal, "Something went wrong on the server; the log has the detail.")
}

// ---- Cookies -----------------------------------------------------------------

// isHTTPS reports whether the browser reached us over HTTPS: directly, or through a reverse
// proxy that says so. X-Forwarded-Proto is trusted only from loopback or a --trusted-proxy (the
// TCP peer, not the client it names), and only ever to add the Secure flag, which cannot weaken
// anything.
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if r.Header.Get("X-Forwarded-Proto") != "https" {
		return false
	}
	info, _ := r.Context().Value(clientKey{}).(clientInfo)
	return info.viaProxy || peerAddr(r).IsLoopback()
}

// setSessionCookie sets the session cookie. Secure follows the request scheme rather than being
// always on, because the documented tailnet path is plain HTTP over WireGuard, where a Secure
// cookie would never be sent back.
func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is set whenever the request is HTTPS

		Name:     sessionCookieName(r),
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // see setSessionCookie

		Name:     sessionCookieName(r),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func sessionToken(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookieName(r))
	if err != nil {
		return ""
	}
	return cookie.Value
}

// ---- Handlers ----------------------------------------------------------------

// HealthResponse is GET /api/v1/health. LatestVersion and ReleaseURL are set only when a newer
// release has been found.
type HealthResponse struct {
	Status string `json:"status"`
	// SetupRequired is true while crowsnest is locked waiting for first-run setup (4.14).
	SetupRequired   bool   `json:"setup_required"`
	Version         string `json:"version"`
	UpdateAvailable bool   `json:"update_available"`
	LatestVersion   string `json:"latest_version,omitempty"`
	ReleaseURL      string `json:"release_url,omitempty"`
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	health := HealthResponse{Status: "ok", Version: s.deps.Version, SetupRequired: s.deps.Setup != nil && s.deps.Setup.Required()}
	if s.deps.Updater != nil {
		if latest, ok := s.deps.Updater.Available(); ok {
			health.UpdateAvailable, health.LatestVersion, health.ReleaseURL = true, latest.TagName, latest.URL
		}
	}
	writeData(w, http.StatusOK, health)
}

// MeResponse is GET /api/v1/auth/me and the body of a successful login.
type MeResponse struct {
	Username string   `json:"username"`
	Features Features `json:"features"`
}

// Features tells the UI what this server offers.
type Features struct {
	Auth         bool   `json:"auth"`          // false with --no-auth: hide sign-out
	Voice        bool   `json:"voice"`         // talk mode available
	Demo         bool   `json:"demo"`          // running against the fake herdr
	Session      string `json:"session"`       // herdr session name
	HerdrVersion string `json:"herdr_version"` // version the herdr server reported at start
	Name         string `json:"name"`          // this instance's name (--name, else the short hostname)
	// Attachments are the upload limits; nil when attachments are off (8.6).
	Attachments *AttachmentLimits `json:"attachments,omitempty"`
	// Onboarding is true while the first-run checklist is unfinished: the app opens /welcome.
	Onboarding bool `json:"onboarding,omitempty"`
}

func (s *Server) me(ctx context.Context) MeResponse {
	cfg := s.deps.Config
	return MeResponse{
		Username: s.deps.Auth.Username(ctx),
		Features: Features{
			Auth: s.deps.Auth.Enabled(), Voice: cfg.VoiceEnabled(), Demo: cfg.Demo, Session: cfg.Session,
			HerdrVersion: s.herdrVersion(), Name: s.instanceName(), Attachments: s.attachmentLimits(),
			Onboarding: s.deps.System != nil && s.deps.System.OnboardingPending(ctx),
		},
	}
}

// herdrVersion is the herdr version to report: now, when herdr connects after start.
func (s *Server) herdrVersion() string {
	if s.deps.HerdrVersionNow != nil {
		if version := s.deps.HerdrVersionNow(); version != "" {
			return version
		}
	}
	return s.deps.HerdrVersion
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeData(w, http.StatusOK, s.me(r.Context()))
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.deps.Auth.Enabled() {
		writeData(w, http.StatusOK, s.me(r.Context())) // nothing to log in to; the UI just proceeds
		return
	}
	// The body must arrive promptly (R1 L3). A writer that cannot take a deadline (httptest's
	// recorder) has no connection to hold open anyway.
	if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(s.loginBodyTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		s.internalError(w, r, "setting the login read deadline", err)
		return
	}
	var body loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "Send JSON with a username and password.")
		return
	}
	token, session, err := s.deps.Auth.Login(r.Context(), clientIP(r), body.Username, body.Password, r.UserAgent(), sessionHost(r))
	var limited *auth.RateLimitedError
	switch {
	case errors.As(err, &limited):
		seconds := int(limited.RetryAfter / time.Second)
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeError(w, http.StatusTooManyRequests, codeRateLimited,
			"Too many failed sign-ins. Try again in "+strconv.Itoa(seconds)+" seconds.")
	case errors.Is(err, auth.ErrBadCredentials):
		s.deps.Logger.Warn("failed login", "client", clientIP(r))
		writeError(w, http.StatusUnauthorized, codeUnauthorized, "Wrong username or password.")
	case err != nil:
		s.internalError(w, r, "logging in", err)
	default:
		setSessionCookie(w, r, token, session.ExpiresAt)
		writeData(w, http.StatusOK, s.me(r.Context()))
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Auth.Logout(r.Context(), sessionToken(r)); err != nil {
		s.internalError(w, r, "logging out", err)
		return
	}
	clearSessionCookie(w, r)
	writeData(w, http.StatusOK, map[string]bool{"signed_out": true})
}

func (s *Server) handleAPINotFound(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, codeNotFound, "No such API endpoint.")
}
