// Projects and their leads (DESIGN.md 21.1, 21.2 and 10):
//
//	GET  /projects[?archived=1]          projects with their lead's status and job counts, plus Unfiled's counts
//	POST /projects                       create {name, dir, slug?, description?, default_role?, …}
//	GET  /projects/{ref}                 one project (id, p_<id> or slug); opening it applies lead_start=open
//	PUT  /projects/{ref}                 edit, archive ({archived: true}) or unarchive
//	POST /projects/{ref}/trust           record the agents' folder trust for its folder (8.2.1)
//	POST /projects/{ref}/codex-config    allow the folder's own .codex/ for Codex jobs {fingerprint} (R9-S5)
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
	"strconv"
	"time"

	"github.com/jclement/crowsnest/internal/agenttrust"
	"github.com/jclement/crowsnest/internal/jobs"
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
	// FolderTrusted says whether Claude Code trusts the project's folder (or a parent), so its
	// agents start without the trust prompt; nil when it is not known (8.2.1).
	FolderTrusted *bool `json:"folder_trusted,omitempty"`
	// Trust is what recording the folder's trust did, on a create or POST …/trust that asked.
	Trust []agenttrust.Result `json:"trust,omitempty"`
	// AgentStart is what the folder carries that an agent runs (hooks, MCP servers, Codex config)
	// or reads (CLAUDE.md, AGENTS.md) the moment it starts there (R9-S4): the trust banner names
	// it, because trusting lets it run with no prompt.
	AgentStart []agenttrust.Finding `json:"agent_start"`
	// CodexConfig is the folder's own .codex/, when it has one, and whether the human has allowed
	// it for Codex jobs as it is now (R9-S5); nil without one.
	CodexConfig *jobs.CodexConfigState `json:"codex_config,omitempty"`
}

// FolderTrust is the agents' folder trust, as the server uses it (8.2.1).
type FolderTrust interface {
	// Enabled is the trust-project-folders setting: false and nothing is ever written.
	Enabled() bool
	// Trust records trust for dirs, which the human chose.
	Trust(dirs []string) []agenttrust.Result
	// Trusted reports whether Claude Code trusts dir (or a parent).
	Trusted(dir string) bool
}

// projectCreateRequest is POST /projects: a project, and whether to trust its folder.
type projectCreateRequest struct {
	projects.Input
	// TrustFolder asks for the agents' folder trust to be recorded for the new project's folder
	// ("Claude may work in this folder without asking"; the form's box, ticked by default). Left
	// out it is false: an API client that does not ask gets no write.
	TrustFolder *bool `json:"trust_folder"`
}

// projectsView is GET /projects.
type projectsView struct {
	Projects []projectView   `json:"projects"`
	Unfiled  projects.Counts `json:"unfiled"`
}

func (s *Server) registerProjectRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/projects", s.requireAuth(http.HandlerFunc(s.handleProjects)))
	mux.Handle("POST /api/v1/projects", s.requireAuth(http.HandlerFunc(s.handleProjectCreate)))
	mux.Handle("GET /api/v1/projects/{ref}", s.requireAuth(http.HandlerFunc(s.handleProject)))
	mux.Handle("PUT /api/v1/projects/{ref}", s.requireAuth(http.HandlerFunc(s.handleProjectUpdate)))
	mux.Handle("POST /api/v1/projects/{ref}/trust", s.requireAuth(http.HandlerFunc(s.handleProjectTrust)))
	mux.Handle("POST /api/v1/projects/{ref}/codex-config", s.requireAuth(http.HandlerFunc(s.handleProjectCodexConfig)))
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
	counts := s.projectCounts(r.Context(), service)
	view := projectsView{Projects: []projectView{}, Unfiled: counts[0]}
	for _, project := range list {
		view.Projects = append(view.Projects, s.viewOf(r.Context(), project, counts))
	}
	writeData(w, http.StatusOK, view)
}

func (s *Server) viewOf(ctx context.Context, project projects.Project, counts map[int64]projects.Counts) projectView {
	view := projectView{Project: project, Counts: counts[project.ID], AgentStart: agenttrust.Inspect(project.Dir)}
	if view.AgentStart == nil {
		view.AgentStart = []agenttrust.Finding{}
	}
	if s.deps.Jobs != nil {
		if state := s.deps.Jobs.CodexConfig(ctx, project.Dir); len(state.Files) > 0 || state.Error != "" {
			view.CodexConfig = &state
		}
	}
	if s.deps.FolderTrust != nil {
		trusted := s.deps.FolderTrust.Trusted(project.RootDir) || s.deps.FolderTrust.Trusted(project.Dir)
		view.FolderTrusted = &trusted
	}
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
	counts := s.projectCounts(r.Context(), service)
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
	var request projectCreateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxProjectBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "Send the project as JSON: {name, dir, description, default_role, default_engine, default_model, default_isolate, permission_mode, lead_start, trust_folder}.")
		return
	}
	project, err := service.Create(r.Context(), request.Input)
	if err != nil {
		s.projectError(w, r, err)
		return
	}
	var trust []agenttrust.Result
	if request.TrustFolder != nil && *request.TrustFolder {
		trust = s.trustProjectFolder(r, project)
	}
	view := s.viewOf(r.Context(), project, nil)
	view.Trust = trust
	writeData(w, http.StatusCreated, view)
}

// handleProjectTrust records the agents' folder trust for an existing project's folder: the
// human pressing "Trust this folder" on the project (8.2.1). Refused while the setting is off.
func (s *Server) handleProjectTrust(w http.ResponseWriter, r *http.Request) {
	if s.projectService(w) == nil {
		return
	}
	project, ok := s.findProject(w, r)
	if !ok {
		return
	}
	if s.deps.FolderTrust == nil || !s.deps.FolderTrust.Enabled() {
		writeError(w, http.StatusConflict, codeConflict, "Recording folder trust is turned off (Settings, Agents: \"Trust the folders of projects you create\"). Answer the trust question on the agent's card instead.")
		return
	}
	trust := s.trustProjectFolder(r, project)
	view := s.viewOf(r.Context(), project, nil)
	view.Trust = trust
	writeData(w, http.StatusOK, view)
}

// trustProjectFolder records trust for a project's folder, as given and resolved. Only ever
// reached from a signed-in human's request naming a project (create or POST …/trust): never
// for a job's worktree (it inherits its checkout's trust) and never for a folder an agent chose.
func (s *Server) trustProjectFolder(r *http.Request, project projects.Project) []agenttrust.Result {
	if s.deps.FolderTrust == nil || !s.deps.FolderTrust.Enabled() {
		return []agenttrust.Result{{Agent: "claude", Status: agenttrust.StatusSkipped, Detail: "recording folder trust is turned off in Settings"}}
	}
	dirs := []string{project.Dir}
	if project.RootDir != "" && project.RootDir != project.Dir {
		dirs = append(dirs, project.RootDir)
	}
	results := s.deps.FolderTrust.Trust(dirs)
	for _, result := range results {
		s.deps.Logger.Info("project folder trust", "project", project.Slug, "agent", result.Agent, "status", result.Status, "detail", result.Detail)
	}
	// The form and the banner listed the folder's .codex files beside the trust ("these run when
	// an agent starts"), so trusting it is seeing them (R9-S5).
	if s.deps.Jobs != nil {
		if state := s.deps.Jobs.CodexConfig(r.Context(), project.Dir); state.Fingerprint != "" && !state.Seen {
			if _, err := s.deps.Jobs.SeeCodexConfig(r.Context(), project.Dir, state.Fingerprint); err != nil {
				s.deps.Logger.Warn("recording a trusted folder's .codex/ as seen", "project", project.Slug, "err", err)
			}
		}
	}
	return results
}

// codexConfigRequest is POST /projects/{ref}/codex-config: the fingerprint the page showed.
type codexConfigRequest struct {
	Fingerprint string `json:"fingerprint"`
}

// handleProjectCodexConfig allows the project folder's own .codex/ for Codex jobs, as the page
// showed it: the human pressing "Allow it for Codex jobs" on the banner that lists its files
// (R9-S5). A fingerprint that no longer matches (the files changed since) is refused.
func (s *Server) handleProjectCodexConfig(w http.ResponseWriter, r *http.Request) {
	if s.projectService(w) == nil {
		return
	}
	project, ok := s.findProject(w, r)
	if !ok {
		return
	}
	if s.deps.Jobs == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Jobs are not running on this instance.")
		return
	}
	var request codexConfigRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxProjectBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Fingerprint == "" {
		writeError(w, http.StatusBadRequest, codeValidation, "Send the fingerprint the page showed: {fingerprint}.")
		return
	}
	if _, err := s.deps.Jobs.SeeCodexConfig(r.Context(), project.Dir, request.Fingerprint); err != nil {
		if jobs.IsValidation(err) {
			writeError(w, http.StatusConflict, codeConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, codeInternal, err.Error())
		return
	}
	s.deps.Logger.Info("project folder's .codex/ allowed for Codex jobs", "project", project.Slug)
	writeData(w, http.StatusOK, s.viewOf(r.Context(), project, nil))
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
	counts := s.projectCounts(r.Context(), service)
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

// projectCounts are the job counts per project (0 is Unfiled), with needs_you replaced by the
// attention queue's own count for that project (DESIGN.md 22), so the switcher, the project
// pages and the palette show the badge's number and not a second definition of it.
func (s *Server) projectCounts(ctx context.Context, service *projects.Service) map[int64]projects.Counts {
	counts, _ := service.JobCounts(ctx)
	if counts == nil {
		counts = map[int64]projects.Counts{}
	}
	if s.deps.Attention == nil {
		return counts
	}
	waiting := s.deps.Attention.Snapshot(ctx).Counts.ByProject
	for id, entry := range counts {
		entry.NeedsYou = waiting[strconv.FormatInt(id, 10)]
		counts[id] = entry
	}
	for key, n := range waiting {
		id, err := strconv.ParseInt(key, 10, 64)
		if _, ok := counts[id]; err == nil && !ok {
			counts[id] = projects.Counts{NeedsYou: n}
		}
	}
	return counts
}
