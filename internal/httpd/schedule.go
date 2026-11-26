// Scheduled tasks and the orchestrator's context (DESIGN.md 7.8, 7.9 and 10):
//
//	GET  /schedule                    every task with its cadence, stamps and next time
//	POST /schedule/{name}/run         Run now: a poke task goes with the next poke, a job task starts
//	PUT  /schedule/{name}             {enabled}: the switch (kv); the file stays the source
//	GET  /orchestrator/context        the orchestrator's context size and upkeep
//	POST /orchestrator/compact        Compact now (409 when it is busy or has a draft)

package httpd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/schedule"
)

// maxScheduleBody bounds PUT /schedule/{name}.
const maxScheduleBody = 1 << 10

func (s *Server) registerScheduleRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/schedule", s.requireAuth(http.HandlerFunc(s.handleSchedule)))
	mux.Handle("POST /api/v1/schedule/{name}/run", s.requireAuth(http.HandlerFunc(s.handleScheduleRun)))
	mux.Handle("PUT /api/v1/schedule/{name}", s.requireAuth(http.HandlerFunc(s.handleScheduleSet)))
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
	if service := s.scheduleService(w); service != nil {
		writeData(w, http.StatusOK, service.List(r.Context()))
	}
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

type scheduleSetRequest struct {
	Enabled *bool `json:"enabled"`
}

func (s *Server) handleScheduleSet(w http.ResponseWriter, r *http.Request) {
	service := s.scheduleService(w)
	if service == nil {
		return
	}
	var body scheduleSetRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxScheduleBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.Enabled == nil {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON like {"enabled": false}.`)
		return
	}
	if err := service.SetEnabled(r.Context(), r.PathValue("name"), *body.Enabled); err != nil {
		s.scheduleError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, service.List(r.Context()))
}

func (s *Server) scheduleError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, schedule.ErrNoTask) {
		writeError(w, http.StatusNotFound, codeNotFound, "No task file has that name; the Schedule view lists the ones there are.")
		return
	}
	s.internalError(w, r, "changing a scheduled task", err)
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
