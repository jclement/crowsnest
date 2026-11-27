// Scheduled tasks and the orchestrator's context (DESIGN.md 7.8, 7.9 and 10):
//
//	GET    /schedule?project=<id>       every task with its cadence, stamps, next time and runs
//	POST   /schedule                    a new app schedule
//	POST   /schedule/{name}/run         Run now: a poke task goes with the next poke, a job task starts
//	PUT    /schedule/{name}             {enabled}: the switch (kv), any task; the full schedule: an
//	                                    app schedule's edit (a task file is read-only: 409)
//	DELETE /schedule/{name}             an app schedule (a task file: 409)
//	GET    /orchestrator/context        the orchestrator's context size and upkeep
//	POST   /orchestrator/compact        Compact now (409 when it is busy or has a draft)

package httpd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/schedule"
)

// maxScheduleBody bounds POST /schedule and PUT /schedule/{name}: a 64 KB body, JSON-escaped.
const maxScheduleBody = 256 << 10

func (s *Server) registerScheduleRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/schedule", s.requireAuth(http.HandlerFunc(s.handleSchedule)))
	mux.Handle("POST /api/v1/schedule", s.requireAuth(http.HandlerFunc(s.handleScheduleCreate)))
	mux.Handle("POST /api/v1/schedule/{name}/run", s.requireAuth(http.HandlerFunc(s.handleScheduleRun)))
	mux.Handle("POST /api/v1/schedule/{name}/approve", s.requireAuth(http.HandlerFunc(s.handleScheduleApprove)))
	mux.Handle("PUT /api/v1/schedule/{name}", s.requireAuth(http.HandlerFunc(s.handleScheduleSet)))
	mux.Handle("DELETE /api/v1/schedule/{name}", s.requireAuth(http.HandlerFunc(s.handleScheduleDelete)))
	mux.Handle("GET /api/v1/orchestrator/context", s.requireAuth(http.HandlerFunc(s.handleOrchestratorContext)))
	mux.Handle("POST /api/v1/orchestrator/compact", s.requireAuth(http.HandlerFunc(s.handleCompact)))
}

func (s *Server) scheduleService(w http.ResponseWriter) *schedule.Service {
	if s.deps.Schedule == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Scheduled tasks are not running in this server.")
	}
	return s.deps.Schedule
}

func (s *Server) handleSchedule(w http.ResponseWriter, r *http.Request) {
	service := s.scheduleService(w)
	if service == nil {
		return
	}
	raw := r.URL.Query().Get("project")
	if raw == "" {
		writeData(w, http.StatusOK, service.List(r.Context()))
		return
	}
	projectID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || projectID < 0 {
		writeError(w, http.StatusBadRequest, codeValidation, "project is a project id (0 for the orchestrator's and unfiled tasks).")
		return
	}
	writeData(w, http.StatusOK, service.ListProject(r.Context(), projectID))
}

func (s *Server) handleScheduleCreate(w http.ResponseWriter, r *http.Request) {
	service := s.scheduleService(w)
	if service == nil {
		return
	}
	var input schedule.ScheduleInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxScheduleBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON like {"name": "digest", "every": "1d", "after": "17:00", "body_md": "…"}.`)
		return
	}
	if err := service.Create(r.Context(), input); err != nil {
		s.scheduleError(w, r, err)
		return
	}
	writeData(w, http.StatusCreated, service.List(r.Context()))
}

func (s *Server) handleScheduleRun(w http.ResponseWriter, r *http.Request) {
	service := s.scheduleService(w)
	if service == nil {
		return
	}
	// A job task creates its job here; a phone locking its screen must not cut that short.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	if err := service.RunNow(ctx, r.PathValue("name")); err != nil {
		s.scheduleError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, service.List(ctx))
}

// scheduleApproveRequest is POST /schedule/{name}/approve: the content hash the human was shown.
type scheduleApproveRequest struct {
	Hash string `json:"hash"`
}

// handleScheduleApprove records the human's review of a task file that starts a job or reaches a
// lead (R5-S3). It is a browser route behind the session: the agent socket has no such verb.
func (s *Server) handleScheduleApprove(w http.ResponseWriter, r *http.Request) {
	service := s.scheduleService(w)
	if service == nil {
		return
	}
	var input scheduleApproveRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxScheduleBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Hash == "" {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON like {"hash": "<the hash the Schedule view showed>"}.`)
		return
	}
	if err := service.Approve(r.Context(), r.PathValue("name"), input.Hash); err != nil {
		s.scheduleError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, service.List(r.Context()))
}

// scheduleSetRequest is PUT /schedule/{name}: the switch, the full schedule, or both.
type scheduleSetRequest struct {
	Enabled *bool `json:"enabled"`
	schedule.ScheduleInput
}

func (s *Server) handleScheduleSet(w http.ResponseWriter, r *http.Request) {
	service := s.scheduleService(w)
	if service == nil {
		return
	}
	body, edits, err := decodeScheduleSet(w, r)
	if err != nil || (body.Enabled == nil && !edits) {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON like {"enabled": false}, or an app schedule's every field to edit it.`)
		return
	}
	name := r.PathValue("name")
	if edits {
		if err := service.Update(r.Context(), name, body.ScheduleInput); err != nil {
			s.scheduleError(w, r, err)
			return
		}
	}
	if body.Enabled != nil {
		if err := service.SetEnabled(r.Context(), name, *body.Enabled); err != nil {
			s.scheduleError(w, r, err)
			return
		}
	}
	writeData(w, http.StatusOK, service.List(r.Context()))
}

// decodeScheduleSet reads a PUT body strictly; edits reports whether it carries any field but
// enabled (an edit replaces the whole schedule, so a missing field is a validation error, not
// "unchanged").
func decodeScheduleSet(w http.ResponseWriter, r *http.Request) (scheduleSetRequest, bool, error) {
	var body scheduleSetRequest
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxScheduleBody))
	if err != nil {
		return body, false, err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return body, false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return body, false, err
	}
	_, hasEnabled := keys["enabled"]
	edits := len(keys) > 1 || (len(keys) == 1 && !hasEnabled)
	return body, edits, nil
}

func (s *Server) handleScheduleDelete(w http.ResponseWriter, r *http.Request) {
	service := s.scheduleService(w)
	if service == nil {
		return
	}
	if err := service.Delete(r.Context(), r.PathValue("name")); err != nil {
		s.scheduleError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, service.List(r.Context()))
}

func (s *Server) scheduleError(w http.ResponseWriter, r *http.Request, err error) {
	var invalid *schedule.ValidationError
	switch {
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, codeValidation, invalid.Message)
	case errors.Is(err, schedule.ErrNoTask):
		writeError(w, http.StatusNotFound, codeNotFound, "No scheduled task has that name; the Schedule view lists the ones there are.")
	case errors.Is(err, schedule.ErrReadOnly):
		writeError(w, http.StatusConflict, codeConflict, "read-only: edit the file. This task is a file in a tasks directory; only its switch is the app's.")
	case errors.Is(err, schedule.ErrNameTaken):
		writeError(w, http.StatusConflict, codeConflict, "A scheduled task (a file or an app schedule) already has that name; pick another.")
	case errors.Is(err, schedule.ErrStaleReview):
		writeError(w, http.StatusConflict, codeConflict, err.Error())
	case errors.Is(err, schedule.ErrNeedsReview), errors.Is(err, schedule.ErrNoReview):
		writeError(w, http.StatusConflict, codeConflict, err.Error())
	case errors.Is(err, schedule.ErrNoAppSchedules):
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Schedules made in the app are not available in this server.")
	default:
		s.internalError(w, r, "changing a scheduled task", err)
	}
}

func (s *Server) handleOrchestratorContext(w http.ResponseWriter, r *http.Request) {
	if s.deps.Tender == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "The orchestrator's upkeep is not running in this server.")
		return
	}
	writeData(w, http.StatusOK, s.deps.Tender.Info(r.Context()))
}

func (s *Server) handleCompact(w http.ResponseWriter, r *http.Request) {
	if s.deps.Tender == nil || s.deps.Orchestrator == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "The orchestrator is not running in this server.")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), pokeTimeout)
	defer cancel()
	s.deps.Orchestrator.Watch(ctx) // the status follows herdr every 2 s; be current for this
	err := s.deps.Tender.CompactNow(ctx, s.deps.Orchestrator.Status())
	if errors.Is(err, orch.ErrNotReady) {
		writeError(w, http.StatusConflict, codeConflict, "The orchestrator is busy, at a dialog, or has a draft in its input box; try again when it is idle.")
		return
	}
	if err != nil {
		s.internalError(w, r, "compacting the orchestrator", err)
		return
	}
	writeData(w, http.StatusOK, s.deps.Tender.Info(ctx))
}
