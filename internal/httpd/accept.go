// Browser routes for acceptance, budgets, wake-ups, the fleet's usage limit, the wait invariant
// and answer receipt (DESIGN.md 6.15 to 6.19). Every route needs a session; the state-changing
// ones pass the origin check like every other.
//
//	POST /api/v1/jobs/{id}/recheck            run crowsnest's check of the job again
//	POST /api/v1/jobs/{id}/rebase             start a job that brings the branch up to date with its base
//	POST /api/v1/jobs/{id}/changes/resolve    accept the changes you asked for (or set the request aside): it may land again (R9-C1)
//	PUT  /api/v1/jobs/{id}/budget             {usd?, tokens?, minutes?, turns?}: change (raise) the job's budget
//	POST /api/v1/jobs/{id}/budget/finish-step a job at its budget may finish the step it is in
//	POST /api/v1/jobs/{id}/wake               wake a blocked job now
//	GET  /api/v1/limits                       the fleet's usage limits, with their reset times
//	POST /api/v1/limits/{engine}/clear        the human's Clear hold: forget the limit, let its jobs go (R6-S3)
//	GET  /api/v1/system/waits                 live jobs that break "every wait has an owner and a wake-up"
//	GET  /api/v1/projects/{id}/accept, PUT    a project's check command and budget defaults
//	POST /api/v1/questions/{id}/reconcile     look for the answer in the asker's conversation
//	POST /api/v1/questions/{id}/retype        {confirm: true}: type an uncertain or failed answer again

package httpd

import (
	"context"
	"errors"
	"net/http"

	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
)

func (s *Server) registerAcceptRoutes(mux *http.ServeMux) {
	auth := func(handler http.HandlerFunc) http.Handler { return s.requireAuth(handler) }
	action := func(fn func(context.Context, *jobs.Service, string) (jobs.Job, error)) http.Handler {
		return auth(s.handleJobAction(fn))
	}
	mux.Handle("POST /api/v1/jobs/{id}/recheck", action(func(ctx context.Context, service *jobs.Service, id string) (jobs.Job, error) {
		return service.Recheck(ctx, id)
	}))
	mux.Handle("POST /api/v1/jobs/{id}/rebase", action(func(ctx context.Context, service *jobs.Service, id string) (jobs.Job, error) {
		return service.AskRebase(ctx, id)
	}))
	mux.Handle("POST /api/v1/jobs/{id}/changes/resolve", action(func(ctx context.Context, service *jobs.Service, id string) (jobs.Job, error) {
		return service.ResolveChanges(ctx, id)
	}))
	mux.Handle("POST /api/v1/jobs/{id}/budget/finish-step", action(func(ctx context.Context, service *jobs.Service, id string) (jobs.Job, error) {
		return service.BudgetFinishStep(ctx, id)
	}))
	mux.Handle("POST /api/v1/jobs/{id}/wake", action(func(ctx context.Context, service *jobs.Service, id string) (jobs.Job, error) {
		return service.WakeNow(ctx, id)
	}))
	mux.Handle("PUT /api/v1/jobs/{id}/budget", auth(s.handleJobBudget))
	mux.Handle("GET /api/v1/limits", auth(s.handleLimits))
	mux.Handle("POST /api/v1/limits/{engine}/clear", auth(s.handleLimitClear))
	mux.Handle("GET /api/v1/system/waits", auth(s.handleWaits))
	mux.Handle("GET /api/v1/projects/{id}/accept", auth(s.handleProjectAccept))
	mux.Handle("PUT /api/v1/projects/{id}/accept", auth(s.handleProjectAcceptSave))
	mux.Handle("POST /api/v1/questions/{id}/reconcile", auth(s.handleQuestionReconcile))
	mux.Handle("POST /api/v1/questions/{id}/retype", auth(s.handleQuestionRetype))
}

func (s *Server) handleJobBudget(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	var body jobs.BudgetInput
	if !decodeSmall(w, r, &body, `{"usd":5,"minutes":120}`) {
		return
	}
	job, err := service.SetJobBudget(r.Context(), r.PathValue("id"), body)
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, job)
}

func (s *Server) handleLimits(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	list := service.FleetLimitsNow(r.Context())
	writeList(w, list, ListMeta{Total: len(list)})
}

// handleLimitClear is POST /limits/{engine}/clear.
func (s *Server) handleLimitClear(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	engine := r.PathValue("engine")
	if engine != jobs.EngineClaude && engine != jobs.EngineCodex {
		writeError(w, http.StatusBadRequest, codeValidation, "engine must be claude or codex")
		return
	}
	released, err := service.ClearLimit(r.Context(), engine)
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"engine": engine, "released": released})
}

// waitsView is GET /system/waits.
type waitsView struct {
	Violations []jobs.Violation `json:"violations"`
	CheckedAt  string           `json:"checked_at,omitempty"`
}

func (s *Server) handleWaits(w http.ResponseWriter, _ *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	list, at := service.WaitViolations()
	view := waitsView{Violations: list}
	if view.Violations == nil {
		view.Violations = []jobs.Violation{}
	}
	if !at.IsZero() {
		view.CheckedAt = at.UTC().Format("2006-01-02T15:04:05Z")
	}
	writeData(w, http.StatusOK, view)
}

func (s *Server) projectID(w http.ResponseWriter, r *http.Request) (int64, string, bool) {
	projectService := s.projectService(w)
	if projectService == nil {
		return 0, "", false
	}
	project, err := projectService.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, codeNotFound, "No such project.")
		return 0, "", false
	}
	return project.ID, project.Dir, true
}

func (s *Server) handleProjectAccept(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	id, dir, ok := s.projectID(w, r)
	if !ok {
		return
	}
	view, err := service.ProjectAcceptOf(r.Context(), id, dir)
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, view)
}

func (s *Server) handleProjectAcceptSave(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	id, dir, ok := s.projectID(w, r)
	if !ok {
		return
	}
	var body jobs.ProjectAccept
	if !decodeSmall(w, r, &body, `{"check_command":"mise run check","budget_usd":5}`) {
		return
	}
	if err := service.SetProjectAccept(r.Context(), id, body); err != nil {
		s.writeJobError(w, r, err)
		return
	}
	view, err := service.ProjectAcceptOf(r.Context(), id, dir)
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, view)
}

func (s *Server) handleQuestionReconcile(w http.ResponseWriter, r *http.Request) {
	questionsService, jobService := s.questionService(w), s.jobService(w)
	if questionsService == nil || jobService == nil {
		return
	}
	question, err := questionsService.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeQuestionError(w, r, err)
		return
	}
	if question.JobID != "" && question.Receipt != questions.ReceiptReceived {
		numeric, _ := questions.ParseID(question.ID)
		if _, err := jobService.ReconcileAnswer(r.Context(), question.JobID, numeric); err != nil && jobs.IsValidation(err) {
			writeError(w, http.StatusConflict, codeConflict, err.Error())
			return
		}
		if question, err = questionsService.Get(r.Context(), question.ID); err != nil {
			s.writeQuestionError(w, r, err)
			return
		}
	}
	writeData(w, http.StatusOK, question)
}

func (s *Server) handleQuestionRetype(w http.ResponseWriter, r *http.Request) {
	service := s.questionService(w)
	if service == nil {
		return
	}
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if !decodeSmall(w, r, &body, `{"confirm":true}`) {
		return
	}
	if !body.Confirm {
		writeError(w, http.StatusConflict, "CONFIRM_REQUIRED", "Confirm it: the answer may already be in the agent's conversation, and it would get it twice.")
		return
	}
	question, err := service.Retype(r.Context(), r.PathValue("id"))
	if errors.Is(err, questions.ErrNotRetypable) {
		writeError(w, http.StatusConflict, codeConflict, err.Error())
		return
	}
	if err != nil {
		s.writeQuestionError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, question)
}
