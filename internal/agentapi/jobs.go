// The job protocol on the agent socket (DESIGN.md 6.2, 6.3 and 9): what `crowsnest job …`,
// `crowsnest hook …` and the job MCP tools call.
//
//	POST /api/v1/jobs                    job_create (the caller's job, if any, is the parent)
//	GET  /api/v1/jobs?state=             list
//	GET  /api/v1/jobs/{id}               the job with its timeline and questions
//	GET  /api/v1/jobs/{id}/context       job_get: the job as text for an agent
//	POST /api/v1/jobs/{id}/update        job_update
//	POST /api/v1/jobs/{id}/done          done
//	POST /api/v1/jobs/{id}/fail          fail
//	POST /api/v1/hooks/{event}           a Claude Code hook's stdin; answers {stdout}
//
// The job id is explicit in the path; clients take it from --job / job_id or CROWSNEST_JOB. What
// the caller may do comes from its token, never from that path (R1b N2): reading is open to any
// caller, but job_update, done and fail are allowed only on the job the token was issued for, or
// on the job held in the token's pane (the orchestrator's tokens name no job). Creating a job
// needs an identified caller (it starts an agent), is rate limited per caller, and goes through
// jobs.CreateForAgent: confined to the caller's directory, limited in depth and fan-out, and
// anything wider waits for the human's approval (R2-H1). Its parent is the caller's job. A hook
// belongs to the token's job, else to the job held in the token's pane, else to nothing.

package agentapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jclement/crowsnest/internal/jobs"
)

// maxJobBody caps a job request (a brief, or a result with its lists).
const maxJobBody = 128 << 10

// maxHookBody caps a hook's input (Stop carries the last assistant message, PostToolUse the
// tool's response).
const maxHookBody = 4 << 20

// FailRequest is the body of POST /jobs/{id}/fail.
type FailRequest struct {
	Reason string `json:"reason"`
}

// ContextResponse is GET /jobs/{id}/context.
type ContextResponse struct {
	Text string `json:"text"`
}

func (s *Server) registerJobRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/jobs", s.handleJobCreate)
	mux.HandleFunc("GET /api/v1/jobs", s.handleJobList)
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.handleJobGet)
	mux.HandleFunc("GET /api/v1/jobs/{id}/context", s.handleJobContext)
	mux.HandleFunc("POST /api/v1/jobs/{id}/update", s.handleJobUpdate)
	mux.HandleFunc("POST /api/v1/jobs/{id}/done", s.handleJobDone)
	mux.HandleFunc("POST /api/v1/jobs/{id}/fail", s.handleJobFail)
	mux.HandleFunc("POST /api/v1/hooks/{event}", s.handleHook)
}

// jobService answers 503 when this server has no jobs.
func (s *Server) jobService(w http.ResponseWriter) *jobs.Service {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "Jobs are not available on this crowsnest.")
	}
	return s.jobs
}

func (s *Server) handleJobCreate(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	var input jobs.CreateInput
	if !decodeBody(w, r, maxJobBody, &input) {
		return
	}
	caller := s.callerOf(r)
	if caller.PaneID == "" {
		writeError(w, http.StatusForbidden, codeForbidden, "Only an agent crowsnest started can create jobs (no valid CROWSNEST_TOKEN came with this request).")
		return
	}
	if ok, retry := s.creates.Allow(quotaKey(caller)); !ok {
		writeQuota(w, retry, "Too many jobs created in a short time. Wait, and do small pieces of work yourself rather than delegating each one.")
		return
	}
	job, err := service.CreateForAgent(r.Context(), input, jobs.AgentCaller{PaneID: caller.PaneID, JobID: caller.JobID})
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeData(w, http.StatusCreated, job)
}

func (s *Server) handleJobList(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	var states []jobs.State
	for _, state := range strings.Split(r.URL.Query().Get("state"), ",") {
		if state = strings.TrimSpace(state); state != "" {
			states = append(states, jobs.State(state))
		}
	}
	list, err := service.List(r.Context(), states)
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	_ = writeJSON(w, http.StatusOK, map[string]any{"data": list, "meta": map[string]int{"total": len(list)}})
}

func (s *Server) handleJobGet(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	detail, err := service.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, detail)
}

func (s *Server) handleJobContext(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	text, err := service.GetForAgent(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, ContextResponse{Text: text})
}

func (s *Server) handleJobUpdate(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	var input jobs.UpdateInput
	if !decodeBody(w, r, maxJobBody, &input) || !s.mayActOnJob(w, r, service) {
		return
	}
	job, err := service.Update(r.Context(), r.PathValue("id"), input)
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, job)
}

func (s *Server) handleJobDone(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	var input jobs.DoneInput
	if !decodeBody(w, r, maxJobBody, &input) || !s.mayActOnJob(w, r, service) {
		return
	}
	job, err := service.Done(r.Context(), r.PathValue("id"), input)
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, job)
}

func (s *Server) handleJobFail(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	var input FailRequest
	if !decodeBody(w, r, maxJobBody, &input) || !s.mayActOnJob(w, r, service) {
		return
	}
	job, err := service.Fail(r.Context(), r.PathValue("id"), input.Reason)
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, job)
}

// handleHook takes a hook's input as Claude Code wrote it. Unknown fields are expected (the
// input grows with Claude Code releases), so this decoder is not strict.
func (s *Server) handleHook(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	var input jobs.HookInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxHookBody)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "The hook input must be JSON: "+err.Error())
		return
	}
	caller := s.callerOf(r)
	reply, err := service.HandleHook(r.Context(), r.PathValue("event"), caller.JobID, caller.PaneID, input)
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	if r.PathValue("event") == jobs.HookSessionStart && caller.JobID == "" && caller.PaneID != "" && s.sessionContext != nil {
		if standing := s.sessionContext(r.Context(), caller.PaneID, input.Source); standing != "" {
			reply.Stdout = strings.TrimSpace(reply.Stdout + "\n\n" + standing)
		}
	}
	writeData(w, http.StatusOK, reply)
}

// mayActOnJob reports whether the caller may change the job in the path: its token was issued for
// that job, or for the pane holding it. Otherwise it writes the refusal.
func (s *Server) mayActOnJob(w http.ResponseWriter, r *http.Request, service *jobs.Service) bool {
	caller := s.callerOf(r)
	job, err := service.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeJobError(w, r, err)
		return false
	}
	if caller.PaneID != "" && (caller.JobID == job.ID || (caller.JobID == "" && caller.PaneID == job.PaneID)) {
		return true
	}
	writeError(w, http.StatusForbidden, codeForbidden, "This is not your job: an agent may update, finish or fail only the job it holds.")
	return false
}

func (s *Server) writeJobError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case jobs.IsValidation(err):
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
	case errors.Is(err, jobs.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "No such job. Check the id (j_<n>); your own is in CROWSNEST_JOB.")
	default:
		s.logger.Error("agent API job request failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "crowsnest failed to handle the request; its log has the detail.")
	}
}
