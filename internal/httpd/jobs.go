// Jobs from the browser (DESIGN.md section 10): the board, creating a job from the text box, a
// job's detail with its timeline, and the actions (message, cancel, nudge, resume); plus the
// new-job form's roles and remembered directories. Every route needs a session; the non-GET ones
// pass the origin check and need the X-Crowsnest header like every other state change.

package httpd

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jclement/crowsnest/internal/redact"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/attach"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/journal"
)

// maxJobBody caps a create or message body.
const maxJobBody = 64 << 10

// jobActionTimeout bounds an action that types into an agent.
const jobActionTimeout = 45 * time.Second

func (s *Server) registerJobRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/jobs", s.requireAuth(http.HandlerFunc(s.handleJobList)))
	mux.Handle("POST /api/v1/jobs", s.requireAuth(http.HandlerFunc(s.handleJobCreate)))
	mux.Handle("GET /api/v1/jobs/{id}", s.requireAuth(http.HandlerFunc(s.handleJobGet)))
	mux.Handle("POST /api/v1/jobs/{id}/message", s.requireAuth(http.HandlerFunc(s.handleJobMessage)))
	mux.Handle("POST /api/v1/jobs/{id}/cancel", s.requireAuth(http.HandlerFunc(s.handleJobAction(func(ctx context.Context, service *jobs.Service, id string) (jobs.Job, error) {
		return service.Cancel(ctx, id)
	}))))
	mux.Handle("POST /api/v1/jobs/{id}/nudge", s.requireAuth(http.HandlerFunc(s.handleJobAction(func(ctx context.Context, service *jobs.Service, id string) (jobs.Job, error) {
		return service.Nudge(ctx, id)
	}))))
	mux.Handle("POST /api/v1/jobs/{id}/resume", s.requireAuth(http.HandlerFunc(s.handleJobAction(func(ctx context.Context, service *jobs.Service, id string) (jobs.Job, error) {
		return service.Resume(ctx, id)
	}))))
	mux.Handle("POST /api/v1/jobs/{id}/restart-agent", s.requireAuth(http.HandlerFunc(s.handleJobAction(func(ctx context.Context, service *jobs.Service, id string) (jobs.Job, error) {
		return service.RestartAgent(ctx, id) // "restart with new instructions" (DESIGN.md 21.3)
	}))))
	mux.Handle("GET /api/v1/roles", s.requireAuth(http.HandlerFunc(s.handleRoles)))
	mux.Handle("GET /api/v1/dirs", s.requireAuth(http.HandlerFunc(s.handleDirs)))
}

func (s *Server) jobService(w http.ResponseWriter) *jobs.Service {
	if s.deps.Jobs == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Jobs are not available on this server.")
	}
	return s.deps.Jobs
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
	query := r.URL.Query()
	if s.deps.Ops != nil && (query.Get("archived") == "1" || strings.TrimSpace(query.Get("q")) != "") {
		s.searchJobs(w, r, service, query.Get("q"), query.Get("archived") == "1")
		return
	}
	list, err := service.List(r.Context(), states)
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeList(w, s.withoutArchived(r.Context(), list), ListMeta{Total: len(list)})
}

// maxJobSearch bounds a search's results.
const maxJobSearch = 100

// withoutArchived leaves archived jobs off the board (DESIGN.md 4.19); they stay searchable.
func (s *Server) withoutArchived(ctx context.Context, list []jobs.Job) []jobs.Job {
	if s.deps.Ops == nil {
		return list
	}
	archived, err := s.deps.Ops.ArchivedJobs(ctx)
	if err != nil || len(archived) == 0 {
		return list
	}
	out := make([]jobs.Job, 0, len(list))
	for _, job := range list {
		if id, err := strconv.ParseInt(strings.TrimPrefix(job.ID, "j_"), 10, 64); err != nil || !archived[id] {
			out = append(out, job)
		}
	}
	return out
}

// searchJobs answers GET /jobs?q=…&archived=1: jobs whose title, brief or result holds q,
// newest first, archived ones only with archived=1.
func (s *Server) searchJobs(w http.ResponseWriter, r *http.Request, service *jobs.Service, query string, archivedOnly bool) {
	ids, err := s.deps.Ops.SearchJobs(r.Context(), query, archivedOnly)
	if err != nil {
		s.internalError(w, r, "searching jobs", err)
		return
	}
	out := make([]jobs.Job, 0, len(ids))
	for _, id := range ids {
		if len(out) >= maxJobSearch {
			break
		}
		if detail, err := service.Get(r.Context(), "j_"+strconv.FormatInt(id, 10)); err == nil {
			out = append(out, detail.Job)
		}
	}
	writeList(w, out, ListMeta{Total: len(out)})
}

func (s *Server) handleJobCreate(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	var input jobs.CreateInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJobBody)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON like {"brief":"…","dir":"/path","role":"builder"}.`)
		return
	}
	job, err := service.Create(r.Context(), input, "")
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	s.deps.Logger.Info("job created", "job", job.ID, "role", job.Role, "dir", job.Dir, "engine", job.Engine)
	writeData(w, http.StatusCreated, job)
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

// jobMessageRequest is the body of POST /jobs/{id}/message: the message, plus uploads (a_…)
// copied into the job's inbox and named after the text (8.6).
type jobMessageRequest struct {
	jobs.MessageInput
	Attachments []string `json:"attachments,omitempty"`
}

func (s *Server) handleJobMessage(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	var request jobMessageRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJobBody)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON like {"text":"…"}.`)
		return
	}
	input := request.MessageInput
	ctx, cancel := context.WithTimeout(r.Context(), jobActionTimeout)
	defer cancel()
	var delivered []attach.Attachment
	if len(request.Attachments) > 0 {
		if _, err := service.Get(ctx, r.PathValue("id")); err != nil {
			s.writeJobError(w, r, err)
			return
		}
		var ok bool
		if delivered, ok = s.deliverAttachments(w, r, request.Attachments, s.inboxForJob(ctx, r.PathValue("id")), attach.RoleMessage, ""); !ok {
			return
		}
		input.Text = attach.WithLines(input.Text, delivered)
	}
	result, err := service.Message(ctx, r.PathValue("id"), input)
	if err != nil {
		if errors.Is(err, jobs.ErrNoAgent) && s.deps.Attachments != nil {
			s.deps.Attachments.Undeliver(context.WithoutCancel(ctx), delivered) // no agent: nothing was typed
		}
		s.takeBack(ctx, delivered, err)
		s.writeJobError(w, r, err)
		return
	}
	s.deps.Journal.Add(r.Context(), journal.KindSend, "You sent job "+r.PathValue("id")+": "+redact.String(input.Text), // R2-I2: no pasted token persists
		map[string]any{"job": r.PathValue("id"), "pane_id": result.PaneID, "started": result.Started, "queued": result.Queued})
	writeData(w, http.StatusOK, result)
}

// handleJobAction serves cancel, nudge and resume: no body, the job back.
func (s *Server) handleJobAction(action func(context.Context, *jobs.Service, string) (jobs.Job, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		service := s.jobService(w)
		if service == nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), jobActionTimeout)
		defer cancel()
		job, err := action(ctx, service, r.PathValue("id"))
		if err != nil {
			s.writeJobError(w, r, err)
			return
		}
		writeData(w, http.StatusOK, job)
	}
}

func (s *Server) handleRoles(w http.ResponseWriter, _ *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	roles := service.Roles().List()
	writeList(w, roles, ListMeta{Total: len(roles)})
}

// DirEntry is one entry of GET /dirs.
type DirEntry struct {
	Path   string `json:"path"`
	Label  string `json:"label"`
	Source string `json:"source"` // remembered | workspace
}

// handleDirs lists remembered directories, then the cwds of the session's workspaces.
func (s *Server) handleDirs(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	seen := map[string]bool{}
	out := []DirEntry{}
	for _, dir := range service.RememberedDirs(r.Context()) {
		if !seen[dir] {
			seen[dir] = true
			out = append(out, DirEntry{Path: dir, Label: filepath.Base(dir), Source: "remembered"})
		}
	}
	var workspaceDirs []string
	if s.deps.Fleet != nil {
		if view, err := s.deps.Fleet.Fleet(r.Context()); err == nil {
			for _, workspace := range view.Workspaces {
				for _, tab := range workspace.Tabs {
					for _, pane := range tab.Panes {
						if pane.Cwd != "" && !seen[pane.Cwd] {
							seen[pane.Cwd] = true
							workspaceDirs = append(workspaceDirs, pane.Cwd)
						}
					}
				}
			}
		}
	}
	sort.Strings(workspaceDirs)
	for _, dir := range workspaceDirs {
		out = append(out, DirEntry{Path: dir, Label: filepath.Base(dir), Source: "workspace"})
	}
	writeList(w, out, ListMeta{Total: len(out)})
}

func (s *Server) writeJobError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case jobs.IsValidation(err):
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
	case errors.Is(err, jobs.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "No such job.")
	case errors.Is(err, jobs.ErrNoAgent):
		writeError(w, http.StatusConflict, codeConflict, "This job has no agent to talk to yet.")
	default:
		s.promptError(w, r, err)
	}
}
