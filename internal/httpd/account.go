// The Account sheet's routes (DESIGN.md 10): who is signed in and where that login comes from,
// changing the stored owner's password, and changing the instance name. A login set by
// --username/CROWSNEST_PASSWORD, and a name set by --name/CROWSNEST_NAME, are managed outside
// crowsnest: the sheet shows them read-only and these routes refuse to change them.

package httpd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/store"
)

const codeWrongPassword = "WRONG_PASSWORD"

// NameStore keeps the instance name chosen in the web app; *store.Store implements it.
type NameStore interface {
	SetKV(ctx context.Context, key, value string) error
}

func (s *Server) registerAccountRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/account", s.requireAuth(http.HandlerFunc(s.handleAccount)))
	mux.Handle("POST /api/v1/account/password", s.requireAuth(http.HandlerFunc(s.handleChangePassword)))
	mux.Handle("PUT /api/v1/account/name", s.requireAuth(http.HandlerFunc(s.handleSetName)))
}

// Account is GET /api/v1/account.
type Account struct {
	Username string `json:"username"`
	// Source is where the login comes from: "database" (chosen at setup; changeable here),
	// "config" (flags, environment or config file; read-only here) or "none" (--no-auth).
	Source            string `json:"source"`
	Name              string `json:"name"`
	NameManaged       bool   `json:"name_managed"` // set by --name or CROWSNEST_NAME: read-only here
	MinPasswordLength int    `json:"min_password_length"`
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	writeData(w, http.StatusOK, Account{
		Username: s.deps.Auth.Username(r.Context()), Source: string(s.deps.Auth.Source()),
		Name: s.instanceName(), NameManaged: s.deps.Config.NameExplicit, MinPasswordLength: auth.MinPasswordLength,
	})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// handleChangePassword checks the current password, stores the new one (which signs every
// session out, this one included) and signs this browser straight back in with a new session.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(s.loginBodyTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		s.internalError(w, r, "setting the password read deadline", err)
		return
	}
	var body changePasswordRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "Send JSON with current_password and new_password.")
		return
	}
	err := s.deps.Auth.ChangePassword(r.Context(), clientIP(r), body.CurrentPassword, body.NewPassword)
	var (
		invalid *auth.ValidationError
		limited *auth.RateLimitedError
	)
	switch {
	case errors.Is(err, auth.ErrManagedOutside):
		writeError(w, http.StatusConflict, codeConflict,
			"This login is set by --username and CROWSNEST_PASSWORD (or a password file). Change it there and restart crowsnest.")
		return
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, codeValidation, capitalise(invalid.Error())+".")
		return
	case errors.As(err, &limited):
		seconds := int(limited.RetryAfter / time.Second)
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeError(w, http.StatusTooManyRequests, codeRateLimited, "Too many wrong passwords. Try again in "+strconv.Itoa(seconds)+" seconds.")
		return
	case errors.Is(err, auth.ErrBadCredentials):
		s.deps.Logger.Warn("wrong current password on a password change", "client", clientIP(r))
		// 403, not 401: the session is fine, and a 401 would send the browser to the login page.
		writeError(w, http.StatusForbidden, codeWrongPassword, "The current password is not right.")
		return
	case err != nil:
		s.internalError(w, r, "changing the password", err)
		return
	}
	s.deps.Logger.Info("password changed; every other session is signed out", "client", clientIP(r))
	token, session, err := s.deps.Auth.NewSession(r.Context(), r.UserAgent(), sessionHost(r))
	if err != nil {
		s.internalError(w, r, "signing back in after a password change", err)
		return
	}
	setSessionCookie(w, r, token, session.ExpiresAt)
	writeData(w, http.StatusOK, map[string]bool{"changed": true, "signed_out_others": true})
}

type setNameRequest struct {
	Name string `json:"name"`
}

// handleSetName stores a new instance name. The header and title follow at once (through
// /auth/me); push titles and the orchestrator's Remote Control name take it at the next start.
func (s *Server) handleSetName(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config.NameExplicit {
		writeError(w, http.StatusConflict, codeConflict, "The name is set by --name or CROWSNEST_NAME. Change it there and restart crowsnest.")
		return
	}
	if s.deps.Names == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "The name cannot be changed on this server.")
		return
	}
	var body setNameRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "Send JSON with a name.")
		return
	}
	name := strings.TrimSpace(body.Name)
	if !config.ValidName(name) {
		writeError(w, http.StatusBadRequest, codeValidation, "A name is 1 to 32 letters, digits, '-' or '_', starting with a letter or digit.")
		return
	}
	if err := s.deps.Names.SetKV(r.Context(), store.InstanceNameKey, name); err != nil {
		s.internalError(w, r, "saving the name", err)
		return
	}
	s.name.Store(&name)
	writeData(w, http.StatusOK, map[string]string{"name": name})
}

// instanceName is the name in effect: the configured one, or the last one saved here.
func (s *Server) instanceName() string {
	if name := s.name.Load(); name != nil {
		return *name
	}
	return s.deps.Config.Name
}
