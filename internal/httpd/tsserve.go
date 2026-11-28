// Publish to your tailnet (DESIGN.md 3.5.1, internal/tsserve): the System page's card and the
// first-run checklist's step.
//
//	GET    /api/v1/tailscale/serve   the card: the offer (the exact URL it would create), or the
//	                                 state (publishing, stopped, failed with the reason), with a
//	                                 QR code of the URL while publishing
//	POST   /api/v1/tailscale/serve   publish: turns the tailscale-serve setting on (saved, so every
//	                                 start publishes) once the plan says it can
//	DELETE /api/v1/tailscale/serve   stop: turns the setting off; when a flag or variable holds it
//	                                 on, stops for this run only and says so
//
// Who may: a browser signed in with the password (or the owner's session from first-run setup).
// A session signed in with Tailscale may look but not change it (403), the same rule as the
// Sign in with Tailscale list: a tailnet login must not widen how crowsnest is reached. The
// middleware's Origin and X-Crowsnest checks apply to POST and DELETE as to every change.

package httpd

import (
	"context"
	"errors"
	"net/http"

	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/system"
	"github.com/jclement/crowsnest/internal/tsserve"
)

// TailscaleServe is what the routes need of internal/tsserve.
type TailscaleServe interface {
	View(ctx context.Context) tsserve.View
	SetEnabled(on bool)
	Enabled() bool
}

// tailscaleServeSetting is the option the card turns on and off.
const tailscaleServeSetting = "tailscale-serve"

func (s *Server) registerTailscaleServeRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/tailscale/serve", s.requireAuth(http.HandlerFunc(s.handleTailscaleServe)))
	mux.Handle("POST /api/v1/tailscale/serve", s.requireAuth(http.HandlerFunc(s.handleTailscaleServeStart)))
	mux.Handle("DELETE /api/v1/tailscale/serve", s.requireAuth(http.HandlerFunc(s.handleTailscaleServeStop)))
}

func (s *Server) tailscaleServe(w http.ResponseWriter) TailscaleServe {
	if s.deps.TailscaleServe == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "This server cannot publish itself to a tailnet.")
	}
	return s.deps.TailscaleServe
}

// tailscaleServeView is the card's state, with the setting's lock and the QR code.
func (s *Server) tailscaleServeView(r *http.Request, service TailscaleServe) tsserve.View {
	view := service.View(r.Context())
	view.Locked = s.tailscaleServeLock()
	view.LocalRisk = s.tailscaleLocalRisk()
	if view.State == tsserve.StatePublishing {
		view.QR = tsserve.QRDataURL(view.URL)
	}
	return view
}

// tailscaleLocalRisk is the sentence both the Publish card and the Sign in with Tailscale card
// show while publishing and --tailscale-auth are on together under --tailscale-auth-local-risk
// (R9-S3); without that flag crowsnest refuses to publish, and the cards say that instead.
func (s *Server) tailscaleLocalRisk() string {
	cfg := s.deps.Config
	if s.deps.System != nil {
		cfg = s.deps.System.Config()
	}
	switch {
	case !cfg.TailscaleAuth || !cfg.TailscaleServe:
		return ""
	case cfg.TailscaleAuthConflict():
		return "Sign in with Tailscale is on, so crowsnest refuses to publish itself: together, any program on this machine could sign in as you with one forged request. Turn one of them off (or start with --tailscale-auth-local-risk to accept that)."
	default:
		return "Publishing and Sign in with Tailscale are both on (--tailscale-auth-local-risk): any program on this machine, an agent included, can sign in as you with one forged request to crowsnest's port. Turn one of them off unless you accept that."
	}
}

// tailscaleServeLock says why the app cannot change the setting ("" when it can).
func (s *Server) tailscaleServeLock() string {
	cfg := s.deps.Config
	if s.deps.System != nil {
		cfg = s.deps.System.Config()
	}
	where := cfg.Sources[tailscaleServeSetting]
	switch config.SourceKind(where) {
	case config.KindFlag:
		return "Set by " + where + " on crowsnest's command line: change it there"
	case config.KindEnv:
		return "Set by the environment (" + where + "): change it there"
	}
	return ""
}

func (s *Server) handleTailscaleServe(w http.ResponseWriter, r *http.Request) {
	if service := s.tailscaleServe(w); service != nil {
		writeData(w, http.StatusOK, s.tailscaleServeView(r, service))
	}
}

// tailscaleServeEditable answers the request itself when it may not change publishing.
func (s *Server) tailscaleServeEditable(w http.ResponseWriter, r *http.Request) bool {
	if session, _ := r.Context().Value(sessionContextKey{}).(auth.Session); session.TailscaleLogin != "" {
		writeError(w, http.StatusForbidden, codeForbidden, "Only a browser signed in with the password can publish crowsnest to the tailnet or stop it.")
		return false
	}
	return true
}

func (s *Server) handleTailscaleServeStart(w http.ResponseWriter, r *http.Request) {
	service := s.tailscaleServe(w)
	if service == nil || !s.tailscaleServeEditable(w, r) {
		return
	}
	if !s.deps.Auth.Enabled() {
		writeError(w, http.StatusConflict, codeConflict, "crowsnest runs with --no-auth, so publishing it would give every device on your tailnet a shell. Start it with a login first.")
		return
	}
	switch view := service.View(r.Context()); view.State {
	case tsserve.StateOff, tsserve.StateStopped, tsserve.StateFailed, tsserve.StateStarting, tsserve.StatePublishing:
	default:
		reason := view.Reason
		if reason == "" {
			reason = "Tailscale is not ready to publish crowsnest on this machine."
		}
		writeError(w, http.StatusConflict, codeConflict, reason+" "+view.Fix)
		return
	}
	if !s.setTailscaleServe(w, r, service, true) {
		return
	}
	writeData(w, http.StatusOK, s.tailscaleServeView(r, service))
}

func (s *Server) handleTailscaleServeStop(w http.ResponseWriter, r *http.Request) {
	service := s.tailscaleServe(w)
	if service == nil || !s.tailscaleServeEditable(w, r) {
		return
	}
	if s.tailscaleServeLock() != "" {
		// A flag or variable holds it on: stopping is always allowed (it only narrows), for this
		// run; the next start publishes again, as whoever set the flag asked.
		service.SetEnabled(false)
		view := s.tailscaleServeView(r, service)
		view.Note = "Stopped until crowsnest restarts: " + view.Locked + " to stop it for good."
		writeData(w, http.StatusOK, view)
		return
	}
	if !s.setTailscaleServe(w, r, service, false) {
		return
	}
	writeData(w, http.StatusOK, s.tailscaleServeView(r, service))
}

// setTailscaleServe saves the setting (the system service applies it, which turns the
// publisher on or off), or, with no settings store, turns the publisher alone.
func (s *Server) setTailscaleServe(w http.ResponseWriter, r *http.Request, service TailscaleServe, on bool) bool {
	if s.deps.System == nil {
		service.SetEnabled(on)
		return true
	}
	value := "false"
	if on {
		value = "true"
	}
	_, err := s.deps.System.UpdateSetting(r.Context(), tailscaleServeSetting, value, false)
	switch {
	case errors.Is(err, system.ErrNotEditable):
		writeError(w, http.StatusConflict, codeConflict, capitalise(trimSentinel(err, system.ErrNotEditable))+".")
		return false
	case err != nil:
		s.internalError(w, r, "saving the tailscale-serve setting", err)
		return false
	}
	return true
}
