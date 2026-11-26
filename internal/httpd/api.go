// The JSON API (DESIGN.md section 9): the response envelope, session cookies, and the health
// and auth handlers. Every response is {"data": …}, lists add "meta", errors are
// {"error": {"message", "code"}}.

package httpd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/jclement/crowsnest/internal/auth"
)

// Error codes, shared with the web client.
const (
	codeValidation   = "VALIDATION_ERROR"
	codeUnauthorized = "UNAUTHORIZED"
	codeForbidden    = "FORBIDDEN"
	codeNotFound     = "NOT_FOUND"
	codeRateLimited  = "RATE_LIMITED"
	codeInternal     = "INTERNAL_ERROR"
)

// SessionCookie is the session cookie's name.
const SessionCookie = "crowsnest_session"

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
// proxy on this machine (tailscale serve) that says so. X-Forwarded-Proto is trusted only from
// loopback, and only ever to add the Secure flag, which cannot weaken anything.
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if r.Header.Get("X-Forwarded-Proto") != "https" {
		return false
	}
	addr, err := netip.ParseAddr(clientIP(r))
	return err == nil && addr.Unmap().IsLoopback()
}

// setSessionCookie sets the session cookie. Secure follows the request scheme rather than being
// always on, because the documented tailnet path is plain HTTP over WireGuard, where a Secure
// cookie would never be sent back.
func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is set whenever the request is HTTPS

		Name:     SessionCookie,
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

		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func sessionToken(r *http.Request) string {
	cookie, err := r.Cookie(SessionCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// ---- Handlers ----------------------------------------------------------------

// HealthResponse is GET /api/v1/health.
type HealthResponse struct {
	Status          string `json:"status"`
	Version         string `json:"version"`
	UpdateAvailable bool   `json:"update_available"`
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeData(w, http.StatusOK, HealthResponse{Status: "ok", Version: s.deps.Version})
}

// MeResponse is GET /api/v1/auth/me and the body of a successful login.
type MeResponse struct {
	Username string   `json:"username"`
	Features Features `json:"features"`
}

// Features tells the UI what this server offers.
type Features struct {
	Auth    bool   `json:"auth"`    // false with --no-auth: hide sign-out
	Voice   bool   `json:"voice"`   // talk mode available
	Demo    bool   `json:"demo"`    // running against the fake herdr
	Session string `json:"session"` // herdr session name
}

func (s *Server) me() MeResponse {
	cfg := s.deps.Config
	return MeResponse{
		Username: s.deps.Auth.Username(),
		Features: Features{Auth: s.deps.Auth.Enabled(), Voice: cfg.VoiceEnabled(), Demo: cfg.Demo, Session: cfg.Session},
	}
}

func (s *Server) handleMe(w http.ResponseWriter, _ *http.Request) {
	writeData(w, http.StatusOK, s.me())
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.deps.Auth.Enabled() {
		writeData(w, http.StatusOK, s.me()) // nothing to log in to; the UI just proceeds
		return
	}
	var body loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "Send JSON with a username and password.")
		return
	}
	token, session, err := s.deps.Auth.Login(r.Context(), clientIP(r), body.Username, body.Password, r.UserAgent())
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
		writeData(w, http.StatusOK, s.me())
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
