// Wiring for the attention queue and the Now view (DESIGN.md 22): the attention service reads
// the approvals, questions, fleet, jobs, projects and standing agents serve already holds, and
// the Now view adds today's spend per subject. Kept out of serve.go so the wiring of one feature
// sits in one file.

package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/attention"
	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/httpd"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/spend"
	"github.com/jclement/crowsnest/internal/store"
)

// attentionDeps are the services the queue reads.
type attentionDeps struct {
	db           *store.Store
	events       *hub.Hub
	fleet        *fleet.Cache
	approvals    *approvals.Service
	questions    *questions.Service
	jobs         *jobs.Service
	projects     *projects.Service
	orchestrator *orch.Orchestrator
	leads        *projects.Leads
	meter        *spend.Meter
	logger       *slog.Logger
}

// liveJobStates are the states the queue looks at besides recent failures.
var liveJobStates = []jobs.State{jobs.StateQueued, jobs.StateRunning, jobs.StateNeedsYou, jobs.StateBlocked, jobs.StateInReview}

// startAttention builds the attention service, runs its watcher until ctx ends, and returns it
// with the Now view's sources.
func startAttention(ctx context.Context, deps attentionDeps) (*attention.Service, httpd.NowSources) {
	standing := standingAgents(deps.orchestrator, deps.leads)
	listProjects := func(ctx context.Context) ([]projects.Project, error) { return deps.projects.List(ctx, false) }
	service := attention.New(attention.Options{
		Approvals: func(ctx context.Context) ([]approvals.Approval, error) {
			return deps.approvals.List(ctx, []approvals.Status{approvals.StatusPending}, 0)
		},
		Questions: func(ctx context.Context) ([]questions.Question, error) { return deps.questions.List(ctx, "open") },
		Agents: func(ctx context.Context) ([]fleet.AgentEntry, error) {
			view, err := deps.fleet.Fleet(ctx)
			return view.Agents, err
		},
		Jobs: func(ctx context.Context) ([]jobs.Job, error) {
			live, err := deps.jobs.List(ctx, liveJobStates)
			if err != nil {
				return nil, err
			}
			// Failed jobs (recent ones count) and done ones (a worktree waiting to be landed counts).
			settled, err := deps.jobs.List(ctx, []jobs.State{jobs.StateFailed, jobs.StateDone})
			return append(live, settled...), err
		},
		Standing: standing,
		Projects: listProjects,
		KV:       deps.db,
		Hub:      deps.events,
		Logger:   deps.logger,
	})
	go service.Run(ctx)
	now := httpd.NowSources{
		Overview: attention.OverviewOptions{
			Projects: listProjects,
			Jobs:     func(ctx context.Context) ([]jobs.Job, error) { return deps.jobs.List(ctx, nil) },
			Standing: standing,
			Spend:    spendToday(deps.meter),
			Location: meterLocation(deps.meter),
		},
		Since: func(r *http.Request) (attention.SinceSources, error) { return sinceSources(r.Context(), deps) },
	}
	return service, now
}

// standingAgents lists the orchestrator and every running lead as the queue sees them.
func standingAgents(orchestrator *orch.Orchestrator, leads *projects.Leads) func(context.Context) []attention.StandingAgent {
	return func(context.Context) []attention.StandingAgent {
		var out []attention.StandingAgent
		if orchestrator != nil {
			status := orchestrator.Status()
			if status.State != orch.StateDisabled {
				out = append(out, attention.StandingAgent{Name: orch.Name, Label: "The orchestrator", PaneID: status.PaneID,
					State: string(status.State), Detail: status.Detail})
			}
		}
		if leads != nil {
			for _, lead := range leads.Running() {
				status := lead.Agent.Status()
				out = append(out, attention.StandingAgent{Name: lead.Agent.AgentName(), Label: "The lead of " + lead.Project.Name,
					ProjectID: lead.Project.ID, PaneID: status.PaneID, State: string(status.State), Detail: status.Detail})
			}
		}
		return out
	}
}

// spendToday is each spend subject's estimated cost today, from the meter's daily rollups.
func spendToday(meter *spend.Meter) func(context.Context) (map[string]float64, error) {
	return func(ctx context.Context) (map[string]float64, error) {
		if meter == nil {
			return nil, nil
		}
		day := time.Now().In(meter.Location()).Format(time.DateOnly)
		report, err := meter.Report(ctx, day, day, spend.GroupJob, nil)
		if err != nil {
			return nil, err
		}
		out := map[string]float64{}
		for _, row := range report.Rows {
			if row.CostUSD != nil {
				out[row.Key] = *row.CostUSD
			}
		}
		return out, nil
	}
}

func meterLocation(meter *spend.Meter) *time.Location {
	if meter == nil {
		return time.Local
	}
	return meter.Location()
}

// sinceSources reads the jobs, questions and project names "since you left" lists from.
func sinceSources(ctx context.Context, deps attentionDeps) (attention.SinceSources, error) {
	list, err := deps.jobs.List(ctx, nil)
	if err != nil {
		return attention.SinceSources{}, err
	}
	asked, err := deps.questions.List(ctx, "")
	if err != nil {
		return attention.SinceSources{}, err
	}
	names := map[int64]attention.ProjectRef{}
	if all, err := deps.projects.List(ctx, true); err == nil {
		for _, project := range all {
			names[project.ID] = attention.ProjectRef{ID: project.ID, Slug: project.Slug, Name: project.Name}
		}
	}
	return attention.SinceSources{Jobs: list, Questions: asked, Projects: names}, nil
}
