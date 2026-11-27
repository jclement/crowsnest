// Socket routes for landing and the pause (DESIGN.md 6.11, 6.13):
//
//	POST /api/v1/jobs/{id}/land   an agent asks the human to merge, push or discard a finished job:
//	                              the request becomes a question; only the human's Allow lands it
//	GET  /api/v1/pause            the pause switch
//	POST /api/v1/pause            pause the automation (any caller: pausing is always safe)
//	POST /api/v1/resume           resume it: refused to an agent (a caller with an agent token);
//	                              `crowsnest resume` in the human's own shell has none

package agentapi

import (
	"net/http"
	"time"
)

// LandRequest is the body of POST /jobs/{id}/land.
type LandRequest struct {
	Action string `json:"action"` // merge | pr | discard
	Reason string `json:"reason,omitempty"`
}

// PauseRequest is the body of POST /pause.
type PauseRequest struct {
	Until  time.Time `json:"until,omitempty"` // auto-resume time; zero: until resumed
	Reason string    `json:"reason,omitempty"`
}

func (s *Server) registerLandingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/jobs/{id}/land", s.handleLandRequest)
	mux.HandleFunc("GET /api/v1/pause", s.handlePauseState)
	mux.HandleFunc("POST /api/v1/pause", s.handlePause)
	mux.HandleFunc("POST /api/v1/resume", s.handleResume)
	mux.HandleFunc("POST /api/v1/digest", s.handleDigestSubmit)
}

// DigestRequest is the body of POST /digest.
type DigestRequest struct {
	Day    string `json:"day,omitempty"`
	BodyMD string `json:"body_md"`
}

// handleDigestSubmit takes the digest the orchestrator was asked to write. It needs an agent
// token, and only a day crowsnest asked for is taken, once.
func (s *Server) handleDigestSubmit(w http.ResponseWriter, r *http.Request) {
	if s.digest == nil {
		writeError(w, http.StatusServiceUnavailable, codeInternal, "The digest is not available on this server.")
		return
	}
	if s.callerOf(r).PaneID == "" {
		writeError(w, http.StatusForbidden, codeForbidden, "Only an agent crowsnest started can submit the digest.")
		return
	}
	var input DigestRequest
	if !decodeBody(w, r, 64<<10, &input) {
		return
	}
	if err := s.digest.Submit(r.Context(), input.Day, input.BodyMD); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
		return
	}
	writeData(w, http.StatusOK, map[string]string{"status": "delivered"})
}

func (s *Server) handleLandRequest(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	var input LandRequest
	if !decodeBody(w, r, maxJobBody, &input) {
		return
	}
	caller := s.callerOf(r)
	if caller.PaneID == "" {
		writeError(w, http.StatusForbidden, codeForbidden, "Only an agent crowsnest started can ask to land a job (no valid CROWSNEST_TOKEN came with this request).")
		return
	}
	question, err := service.RequestLand(r.Context(), r.PathValue("id"), input.Action, caller, input.Reason)
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeData(w, http.StatusAccepted, map[string]any{"question_id": question.ID, "status": "requested",
		"note": "Asked the human. Nothing is landed until they allow it; the answer reaches you as a message."})
}

func (s *Server) handlePauseState(w http.ResponseWriter, r *http.Request) {
	if s.pause == nil {
		writeError(w, http.StatusServiceUnavailable, codeInternal, "The pause switch is not available on this server.")
		return
	}
	writeData(w, http.StatusOK, s.pause.State(r.Context()))
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	if s.pause == nil {
		writeError(w, http.StatusServiceUnavailable, codeInternal, "The pause switch is not available on this server.")
		return
	}
	var input PauseRequest
	if !decodeBody(w, r, 8<<10, &input) {
		return
	}
	by := "cli"
	if caller := s.callerOf(r); caller.JobID != "" {
		by = "job " + caller.JobID
	} else if caller.PaneID != "" {
		by = "agent in " + caller.PaneID
	}
	state, err := s.pause.Pause(r.Context(), by, input.Until, input.Reason)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
		return
	}
	writeData(w, http.StatusOK, state)
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	if s.pause == nil {
		writeError(w, http.StatusServiceUnavailable, codeInternal, "The pause switch is not available on this server.")
		return
	}
	if s.callerOf(r).PaneID != "" {
		writeError(w, http.StatusForbidden, codeForbidden, "Only the human resumes crowsnest's automation (from the web app, or `crowsnest resume` in their own shell).")
		return
	}
	state, err := s.pause.Resume(r.Context(), "cli")
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, err.Error())
		return
	}
	writeData(w, http.StatusOK, state)
}
