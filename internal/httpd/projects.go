// Projects and their leads (DESIGN.md 21.1, 21.2 and 10):
//
//	GET  /projects[?archived=1]          projects with their lead's status and job counts, plus Unfiled's counts
//	POST /projects                       create {name, dir, slug?, description?, default_role?, …}
//	GET  /projects/{ref}                 one project (id, p_<id> or slug); opening it applies lead_start=open
//	PUT  /projects/{ref}                 edit, archive ({archived: true}) or unarchive
//	GET  /leads                          every running lead's status
//	POST /projects/{ref}/lead/start      Start lead
//	POST /projects/{ref}/lead/stop       Stop lead (its conversation is kept)
//	POST /projects/{ref}/lead/restart    Restart (same conversation) · /lead/new: New conversation
//	POST /projects/{ref}/lead/poke       Poke now, for the lead

package httpd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/projects"
)

// maxProjectBody bounds a project create or edit.
const maxProjectBody = 16 << 10

// projectView is a project as the browser sees it.
type projectView struct {
	projects.Project
	Lead   projects.LeadStatus `json:"lead"`
	Counts projects.Counts     `json:"counts"`
}

// projectsView is GET /projects.
type projectsView struct {
	Projects []projectView  `json:"projects"`
	Unfiled  projects.Counts `json:"unfiled"`
}

func (s *Server) registerProjectRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/projects", s.requireAuth(http.HandlerFunc(s.handleProjects)))
	mux.Handle("POST /api/v1/projects", s.requireAuth(http.HandlerFunc(s.handleProjectCreate)))
	mux.Handle("GET /api/v1/projects/{ref}", s.requireAuth(http.HandlerFunc(s.handleProject)))
	mux.Handle("PUT /api/v1/projects/{ref}", s.requireAuth(http.HandlerFunc(s.handleProjectUpdate)))
	mux.Handle("GET /api/v1/leads", s.requireAuth(http.HandlerFunc(s.handleLeads)))
	mux.Handle("POST /api/v1/projects/{ref}/lead/{action}", s.requireAuth(http.HandlerFunc(s.handleLeadAction)))
}

func (s *Server) projectService(w http.ResponseWriter) *projects.Service {
	if s.deps.Projects == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Projects are not available in this server.")
	}
	return s.deps.Projects
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	service := s.projectService(w)
	if service == nil {
		return
	}
	list, err := service.List(r.Context(), r.URL.Query().Get("archived") == "1")
	if err != nil {
		s.internalError(w, r, "reading or writing projects", err)
		return
	}
	counts, _ := service.JobCounts(r.Context())
	view := projectsView{Projects: []projectView{}, Unfiled: counts[0]}
	for _, project := range list {
		view.Projects = append(view.Projects, s.viewOf(r.Context(), project, counts))
	}
	writeData(w, http.StatusOK, view)
}

func (s *Server) viewOf(ctx context.Context, project projects.Project, counts map[int64]projects.Counts) projectView {
	view := projectView{Project: project, Counts: counts[project.ID]}
	if s.deps.Leads != nil {
		view.Lead, _ = s.deps.Leads.Status(ctx, project.ID)
	} else {
		view.Lead = projects.LeadStatus{Status: orch.Status{State: projects.StateOff, Name: projects.AgentName(project.Slug)}, ProjectID: project.ID, Slug: project.Slug}
	}
	return view
}

func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	service := s.projectService(w)
	if service == nil {
		return
	}
	project, ok := s.findProject(w, r)
	if !ok {
		return
	}
	if s.deps.Leads != nil && r.URL.Query().Get("open") == "1" {
		// "Start the lead when the project is opened" (lead_start=open): the app asks with
		// ?open=1 when the human opens the project, not on every refetch.
		s.deps.Leads.Opened(context.WithoutCancel(r.Context()), project.ID)
	}
	counts, _ := service.JobCounts(r.Context())
	writeData(w, http.StatusOK, s.viewOf(r.Context(), project, counts))
}

// findProject resolves {ref}, writing the 404 when it names nothing.
func (s *Server) findProject(w http.ResponseWriter, r *http.Request) (projects.Project, bool) {
	project, err := s.deps.Projects.Get(r.Context(), r.PathValue("ref"))
	if errors.Is(err, projects.ErrNotFound) {
		writeError(w, http.StatusNotFound, codeNotFound, "No such project.")
		return projects.Project{}, false
	}
	if err != nil {
		s.internalError(w, r, "reading or writing projects", err)
		return projects.Project{}, false
	}
	return project, true
}

func (s *Server) decodeProject(w http.ResponseWriter, r *http.Request) (projects.Input, bool) {
	var input projects.Input
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxProjectBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "Send the project as JSON: {name, dir, description, default_role, default_engine, default_model, default_isolate, permission_mode, lead_start}.")
		return projects.Input{}, false
	}
	return input, true
}

func (s *Server) handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	service := s.projectService(w)
	if service == nil {
		return
	}
	input, ok := s.decodeProject(w, r)
	if !ok {
		return
	}
	project, err := service.Create(r.Context(), input)
	if err != nil {
		s.projectError(w, r, err)
		return
	}
	writeData(w, http.StatusCreated, s.viewOf(r.Context(), project, nil))
}

func (s *Server) handleProjectUpdate(w http.ResponseWriter, r *http.Request) {
	service := s.projectService(w)
	if service == nil {
		return
	}
	existing, ok := s.findProject(w, r)
	if !ok {
		return
	}
	input, ok := s.decodeProject(w, r)
	if !ok {
		return
	}
	project, err := service.Update(r.Context(), existing.ID, input)
	if err != nil {
		s.projectError(w, r, err)
		return
	}
	if project.Archived && s.deps.Leads != nil && s.deps.Leads.Get(project.ID) != nil {
		// An archived project's lead is stopped: archived projects take no work.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 45*time.Second)
		defer cancel()
		_, _ = s.deps.Leads.Stop(ctx, project.ID)
	}
	counts, _ := service.JobCounts(r.Context())
	writeData(w, http.StatusOK, s.viewOf(r.Context(), project, counts))
}

func (s *Server) projectError(w http.ResponseWriter, r *http.Request, err error) {
	var validation *projects.ValidationError
	switch {
	case errors.As(err, &validation):
		writeError(w, http.StatusBadRequest, codeValidation, validation.Message)
	case errors.Is(err, projects.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "No such project.")
	case errors.Is(err, projects.ErrArchived):
		writeError(w, http.StatusConflict, codeConflict, "The project is archived; unarchive it to start its lead.")
	default:
		s.internalError(w, r, "reading or writing projects", err)
	}
}

func (s *Server) handleLeads(w http.ResponseWriter, r *http.Request) {
	if s.deps.Leads == nil {
		writeData(w, http.StatusOK, []projects.LeadStatus{})
		return
	}
	out := []projects.LeadStatus{}
	for _, lead := range s.deps.Leads.Running() {
		status, err := s.deps.Leads.Status(r.Context(), lead.Project.ID)
		if err == nil {
			out = append(out, status)
		}
	}
	writeData(w, http.StatusOK, out)
}

// leadActionTimeout bounds a lead's start, stop or restart: a stop waits up to 15 s for the
// shell and a start up to a minute for Claude, as the orchestrator's do.
const leadActionTimeout = 90 * time.Second

func (s *Server) handleLeadAction(w http.ResponseWriter, r *http.Request) {
	if s.projectService(w) == nil {
		return
	}
	if s.deps.Leads == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Project leads are not available in this server.")
		return
	}
	project, ok := s.findProject(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), leadActionTimeout)
	defer cancel()
	leads := s.deps.Leads
	switch r.PathValue("action") {
	case "start":
		status, err := leads.Start(ctx, project.ID)
		s.leadResult(w, r, status, err)
	case "stop":
		status, err := leads.Stop(ctx, project.ID)
		s.leadResult(w, r, status, err)
	case "restart", "new", "poke":
		s.runningLeadAction(ctx, w, r, project, r.PathValue("action"))
	default:
		writeError(w, http.StatusNotFound, codeNotFound, "Lead actions are start, stop, restart, new and poke.")
	}
}

// runningLeadAction is an action on a running lead: Restart, New conversation or Poke now.
func (s *Server) runningLeadAction(ctx context.Context, w http.ResponseWriter, r *http.Request, project projects.Project, action string) {
	lead := s.deps.Leads.Get(project.ID)
	if lead == nil {
		writeError(w, http.StatusConflict, codeConflict, "The project's lead is not running; start it first.")
		return
	}
	switch action {
	case "poke":
		if lead.Poker == nil {
			writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Pokes are not running for this lead.")
			return
		}
		writeData(w, http.StatusOK, lead.Poker.PokeNow(ctx))
		return
	case "restart":
		_, err := lead.Agent.Restart(ctx)
		status, _ := s.deps.Leads.Status(ctx, project.ID)
		s.leadResult(w, r, status, err)
	case "new":
		_, err := lead.Agent.NewConversation(ctx)
		status, _ := s.deps.Leads.Status(ctx, project.ID)
		s.leadResult(w, r, status, err)
	}
}

func (s *Server) leadResult(w http.ResponseWriter, r *http.Request, status projects.LeadStatus, err error) {
	if err != nil {
		var validation *projects.ValidationError
		if errors.As(err, &validation) || errors.Is(err, projects.ErrArchived) || errors.Is(err, projects.ErrNotFound) {
			s.projectError(w, r, err)
			return
		}
		s.deps.Logger.Warn("lead action failed", "err", err)
		writeError(w, http.StatusBadGateway, codeUnavailable, "The lead could not be changed. Its terminal shows why; the server log has the detail.")
		return
	}
	writeData(w, http.StatusOK, status)
}
