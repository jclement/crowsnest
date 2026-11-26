// POST /api/v1/admin/update: self-update from the browser (DESIGN.md section 16.1). crowsnest
// has one user, so "admin" is anyone signed in (or anyone at all under --no-auth, who has a
// shell already). The route verifies and installs the newer release, answers, and then asks the
// process to exit so a supervisor (launchd, systemd, a restart policy) starts the new binary.
// herdr and every agent keep running across that exit, as across any restart.

package httpd

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jclement/crowsnest/internal/update"
)

// Updater is what the HTTP server needs from self-update; cmd/crowsnest implements it.
type Updater interface {
	// Available is the newer release the background check found, if any.
	Available() (update.Release, bool)
	// Apply re-checks GitHub, verifies and installs the newer release, and returns it. A
	// *update.Refusal says why nothing was done in words fit to show (a container, a
	// development build, a Homebrew install, already current).
	Apply(ctx context.Context) (update.Release, error)
	// Restart ends the process after a successful update, once the response is out.
	Restart()
}

// updateApplyTimeout bounds the whole download-verify-install step.
const updateApplyTimeout = 5 * time.Minute

// restartDelay lets the success response reach the browser before the process exits.
const restartDelay = 500 * time.Millisecond

// AdminUpdateResponse is the success body of POST /admin/update.
type AdminUpdateResponse struct {
	Updated    bool   `json:"updated"`
	Version    string `json:"version"`    // the version now installed
	Restarting bool   `json:"restarting"` // the process is exiting so it can be restarted
}

func (s *Server) handleAdminUpdate(w http.ResponseWriter, r *http.Request) {
	if s.deps.Updater == nil {
		writeError(w, http.StatusConflict, "UNSUPPORTED", "Self-update is off for this build (a development build, --demo or a container). Update it the way it was installed.")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), updateApplyTimeout)
	defer cancel()
	installed, err := s.deps.Updater.Apply(ctx)
	var refusal *update.Refusal
	switch {
	case errors.As(err, &refusal):
		writeError(w, http.StatusConflict, "REFUSED", refusal.Error())
		return
	case err != nil:
		// The detail (URLs, verification internals) goes to the log, not the response (4.9).
		s.deps.Logger.Error("self-update failed", "err", err)
		writeError(w, http.StatusBadGateway, "UPDATE_FAILED", "The update could not be downloaded, verified or installed; nothing was changed. The server log says why.")
		return
	}
	s.deps.Logger.Info("self-update installed; exiting so the new version starts", "version", installed.TagName)
	writeData(w, http.StatusOK, AdminUpdateResponse{Updated: true, Version: installed.TagName, Restarting: true})
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	time.AfterFunc(restartDelay, s.deps.Updater.Restart)
}
