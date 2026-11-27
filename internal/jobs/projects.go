// Jobs and projects (DESIGN.md 21.1, 21.2). A job may belong to a project: given "to the
// project" with no directory it goes to that project's lead (a standing agent, like the
// orchestrator, that holds it and may delegate inside the project's directory); given with a
// directory it launches a job agent as before, and a directory inside a project's directory
// joins that project by itself. The project's defaults fill what the human left unset.
//
// internal/projects owns projects and leads; this file sees them through ProjectSource, so the
// jobs package does not import it (projects' leads are built from internal/orch, which jobs
// already imports, and serve ties the two together).

package jobs

import (
	"context"
	"strings"

	"github.com/jclement/crowsnest/internal/store"
)

// LeadPrefix starts every project lead's herdr agent name (projects.AgentPrefix).
const LeadPrefix = "lead-"

// IsStanding reports whether an agent name is a standing agent's (the orchestrator or a project
// lead): one whose own lifecycle is internal/orch's, which holds jobs in its conversation
// rather than running one job each.
func IsStanding(agentName string) bool {
	return agentName == OrchestratorName || strings.HasPrefix(agentName, LeadPrefix)
}

// Targets of a job given in a project.
const (
	ToLead = "lead" // no directory: the project's lead holds it
	ToJob  = "job"  // a job agent of its own, in the project's directory unless one is given
)

// ProjectInfo is what jobs need of a project.
type ProjectInfo struct {
	ID             int64
	Slug, Name     string
	Dir, RootDir   string
	DefaultRole    string
	DefaultEngine  string
	DefaultModel   string
	DefaultIsolate *bool
	Archived       bool
}

// StandingAgent is a standing agent that can be given jobs: the orchestrator (ProjectID 0) or a
// project's running lead.
type StandingAgent struct {
	Name      string // herdr agent name
	PaneID    string // empty: not up yet
	Ready     bool   // idle, working or blocked: it can be given a job
	ProjectID int64
	RootDir   string // a lead's project root: what its children may use without asking
}

// ProjectSource is internal/projects as the jobs service sees it (serve adapts it). Nil: no
// projects; every job is Unfiled.
type ProjectSource interface {
	Project(ctx context.Context, id int64) (ProjectInfo, bool)
	ForDir(ctx context.Context, dir string) (ProjectInfo, bool)
	// Leads lists the running leads.
	Leads() []StandingAgent
	// LeadByPane finds the running lead in a pane.
	LeadByPane(paneID string) (StandingAgent, bool)
	// GivenJob applies the project's lead start policy for a job given to its lead.
	GivenJob(ctx context.Context, projectID int64)
}

// applyProject resolves a new job's project and fills the project's defaults. It runs before
// newRecord validates the input.
func (s *Service) applyProject(ctx context.Context, input CreateInput) (CreateInput, int64, error) {
	projects := s.opts.Projects
	switch {
	case projects == nil && input.ProjectID != 0:
		return input, 0, invalid("projects are not available on this server")
	case projects == nil:
		return input, 0, nil
	}
	to := strings.TrimSpace(input.To)
	if to != "" && to != ToLead && to != ToJob {
		return input, 0, invalid("to is %q (the project's lead) or %q (a job agent of its own)", ToLead, ToJob)
	}
	dir := strings.TrimSpace(input.Dir)
	var project ProjectInfo
	if input.ProjectID != 0 {
		found, ok := projects.Project(ctx, input.ProjectID)
		if !ok {
			return input, 0, invalid("no project %d", input.ProjectID)
		}
		if found.Archived {
			return input, 0, invalid("project %s is archived; unarchive it to give it jobs", found.Name)
		}
		project = found
	} else if dir != "" {
		found, ok := projects.ForDir(ctx, dir)
		if !ok {
			return input, 0, nil // Unfiled
		}
		project = found
	} else {
		return input, 0, nil // no project, no directory: the orchestrator's
	}
	if err := placeInProject(ctx, projects, project, &input, to, dir); err != nil {
		return input, 0, err
	}
	return withProjectDefaults(input, project), project.ID, nil
}

// placeInProject decides the directory of a job given in project: none for its lead, the
// project's own (or one inside it) for a job agent.
func placeInProject(ctx context.Context, projects ProjectSource, project ProjectInfo, input *CreateInput, to, dir string) error {
	switch {
	case to == ToLead && dir != "":
		return invalid("a job for the project's lead takes no directory; the lead works in %s", project.Dir)
	case to == ToLead || (to == "" && dir == ""):
		input.Dir = ""
		return nil
	case dir == "":
		input.Dir = project.Dir
		return nil
	}
	if owner, ok := projects.ForDir(ctx, dir); !ok || owner.ID != project.ID {
		return invalid("%s is not inside project %s (%s)", dir, project.Name, project.Dir)
	}
	return nil
}

// withProjectDefaults fills unset fields from the project. A job for the lead keeps the lead's
// own model (it is typed into the lead's conversation), so only job agents take the model.
func withProjectDefaults(input CreateInput, project ProjectInfo) CreateInput {
	if strings.TrimSpace(input.Role) == "" && project.DefaultRole != "" && input.Dir != "" {
		input.Role = project.DefaultRole
	}
	if strings.TrimSpace(input.Engine) == "" && project.DefaultEngine != "" && input.Dir != "" {
		input.Engine = project.DefaultEngine
	}
	if strings.TrimSpace(input.Model) == "" && project.DefaultModel != "" && input.Dir != "" {
		input.Model = project.DefaultModel
	}
	if input.Isolate == nil && project.DefaultIsolate != nil {
		isolate := *project.DefaultIsolate
		input.Isolate = &isolate
	}
	return input
}

// ---- Dispatch to standing agents ---------------------------------------------------------

// DispatchStanding gives each standing agent (the orchestrator, then every running lead) the
// oldest queued job waiting for it, when it is ready and holds no other running job.
func (s *Service) DispatchStanding(ctx context.Context) {
	if s.paused() {
		return // no new starts while paused; a queued job waits (holds.go)
	}
	s.DispatchOrchestrator(ctx)
	if s.opts.Projects == nil {
		return
	}
	for _, lead := range s.opts.Projects.Leads() {
		s.dispatchTo(ctx, lead)
	}
}

// standingOf returns the running standing agent in paneID: the orchestrator or a lead.
func (s *Service) standingOf(paneID string) (StandingAgent, bool) {
	if paneID == "" {
		return StandingAgent{}, false
	}
	if s.opts.Orchestrator != nil {
		if info := s.opts.Orchestrator(); info.PaneID == paneID {
			return StandingAgent{Name: OrchestratorName, PaneID: paneID, Ready: info.Ready}, true
		}
	}
	if s.opts.Projects != nil {
		return s.opts.Projects.LeadByPane(paneID)
	}
	return StandingAgent{}, false
}

// oldestFor is the next job a standing agent takes: an Unfiled one for the orchestrator, its
// project's for a lead.
func (s *Service) oldestFor(ctx context.Context, agent StandingAgent) (store.JobRecord, bool, error) {
	if agent.ProjectID == 0 {
		return s.opts.Store.OldestUnassignedQueued(ctx)
	}
	return s.opts.Store.OldestUnassignedQueuedIn(ctx, agent.ProjectID)
}

// standingLine is the one line typed to give a standing agent its job.
func standingLine(job Job, path string) string { return orchestratorLine(job, path) }
