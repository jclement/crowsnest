// Wiring for a diff at any time (DESIGN.md 6.21): the resolver that turns a job, a project or the
// orchestrator into the checkout internal/changes reads, and the service's background loop.

package main

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"

	"github.com/jclement/crowsnest/internal/changes"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/store"
)

// changesResolver answers internal/changes from the jobs and projects services.
type changesResolver struct {
	cfg      config.Config
	db       *store.Store
	jobs     *jobs.Service
	projects *projects.Service
}

func (r changesResolver) jobTarget(ctx context.Context, job jobs.Job) changes.Target {
	target := changes.Target{Subject: job.ID, Kind: changes.KindJob, JobID: job.ID, JobState: string(job.State), Label: job.ID + ": " + job.Title,
		ProjectID: job.ProjectID, BaseBranch: job.BaseBranch, Root: job.WorkDir, Walkthrough: job.WalkthroughMD}
	if target.Root == "" {
		target.Root = job.Dir
	}
	if job.StartedAt != nil {
		target.Since = *job.StartedAt
	}
	if id, err := strconv.ParseInt(strings.TrimPrefix(job.ID, "j_"), 10, 64); err == nil {
		if record, err := r.db.GetJob(ctx, id); err == nil {
			target.BaseCommit = record.BaseCommit
		}
	}
	switch {
	case job.TornDownAt != nil:
		target.Gone = job.ID + "'s worktree is gone (it was landed or discarded); its review is kept on the job"
	case job.LandState != "" && job.WorktreeBranch != "":
		target.Gone = job.ID + " was already landed; there is no diff left to review"
	case job.WorkDir == "" && job.Dir == "":
		target.Root = ""
	}
	if job.ProjectID != 0 && r.projects != nil {
		if project, err := r.projects.ByID(ctx, job.ProjectID); err == nil {
			target.Project = project.Slug
		}
	}
	return target
}

func (r changesResolver) Job(ctx context.Context, id string) (changes.Target, error) {
	detail, err := r.jobs.Get(ctx, id)
	if err != nil {
		if errors.Is(err, jobs.ErrNotFound) {
			return changes.Target{}, &changes.Error{Kind: "not_found", Message: "no job " + id}
		}
		return changes.Target{}, err
	}
	return r.jobTarget(ctx, detail.Job), nil
}

func (r changesResolver) project(ctx context.Context, ref string) (projects.Project, error) {
	if r.projects == nil {
		return projects.Project{}, &changes.Error{Kind: "not_found", Message: "projects are not available"}
	}
	project, err := r.projects.Get(ctx, ref)
	if err != nil {
		return projects.Project{}, &changes.Error{Kind: "not_found", Message: "no project " + ref}
	}
	return project, nil
}

func (r changesResolver) Project(ctx context.Context, ref string) (changes.Target, error) {
	project, err := r.project(ctx, ref)
	if err != nil {
		return changes.Target{}, err
	}
	target := changes.Target{Subject: "project:" + strconv.FormatInt(project.ID, 10), Kind: changes.KindProject, Label: project.Name,
		Root: project.Dir, ProjectID: project.ID, Project: project.Slug}
	if layers, err := r.db.GetAgentLayers(ctx, "lead:"+project.Slug); err == nil {
		target.Since = layers.StartedAt
	}
	return target, nil
}

func (r changesResolver) Orchestrator(ctx context.Context) (changes.Target, error) {
	target := changes.Target{Subject: "orchestrator", Kind: changes.KindOrchestrator, Label: "orchestrator", Root: r.cfg.Dir}
	if layers, err := r.db.GetAgentLayers(ctx, "orchestrator"); err == nil {
		target.Since = layers.StartedAt
	}
	return target, nil
}

func (r changesResolver) ProjectJobs(ctx context.Context, ref string) ([]changes.Target, error) {
	project, err := r.project(ctx, ref)
	if err != nil {
		return nil, err
	}
	list, err := r.jobs.List(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := []changes.Target{}
	for _, job := range list {
		if job.ProjectID != project.ID || job.ReviewOf != "" || job.WorktreeBranch == "" {
			continue
		}
		out = append(out, r.jobTarget(ctx, job))
	}
	return out, nil
}

func (r changesResolver) Retired(ctx context.Context, subject string) bool {
	if !strings.HasPrefix(subject, "j_") {
		return false
	}
	detail, err := r.jobs.Get(ctx, subject)
	if errors.Is(err, jobs.ErrNotFound) {
		return true
	}
	if err != nil {
		return false
	}
	return detail.Job.LandState != "" || detail.Job.TornDownAt != nil
}

// startChanges builds the live diff service and runs its loop until ctx ends.
func startChanges(ctx context.Context, cfg config.Config, db *store.Store, events *hub.Hub, jobService *jobs.Service, projectService *projects.Service, logger *slog.Logger) *changes.Service {
	service := changes.New(changes.Options{Store: db, Hub: events, Logger: logger,
		Resolver: changesResolver{cfg: cfg, db: db, jobs: jobService, projects: projectService}})
	go service.Run(ctx)
	return service
}
