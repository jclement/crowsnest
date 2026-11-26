// Project leads (DESIGN.md 21.2): a lead is a long-lived Claude agent in its project's main
// checkout, kept by the very lifecycle that keeps the orchestrator (internal/orch: the same
// start-up files, hooks, MCP tools, SAY contract, Remote Control, recovery), with a poker of its
// own for its project's jobs and its scheduled tasks. This file owns which leads run; serve
// builds each one (LeadFactory), because only serve holds every dependency.
//
// A lead that was running when crowsnest stopped is started again at the next start (adopted
// if its Claude is still alive: herdr and its agents outlive crowsnest). "Running" is a kv flag
// set by Start and cleared by Stop, so a lead the human stopped stays stopped.

package projects

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/jclement/crowsnest/internal/orch"
)

// AgentPrefix starts every lead's herdr agent name ("lead-<slug>").
const AgentPrefix = "lead-"

// AgentName is the herdr agent name of a project's lead.
func AgentName(slug string) string { return AgentPrefix + slug }

// Subject is how a lead is named in spend, instruction records and journal lines.
func Subject(slug string) string { return "lead:" + slug }

// kvRunning is the per-lead flag that it should be running ("1").
func kvRunning(slug string) string { return "lead." + slug + ".running" }

// Lead is one running lead: its lifecycle and its poker.
type Lead struct {
	Project Project
	Agent   *orch.Orchestrator
	Poker   *orch.Poker // nil when the factory gives none (tests)
	cancel  context.CancelFunc
}

// LeadFactory builds a project's lead and its poker; serve supplies it.
type LeadFactory func(project Project) (*orch.Orchestrator, *orch.Poker)

// KV is the persistent store the running flags live in.
type KV interface {
	GetKV(ctx context.Context, key string) (string, error)
	SetKV(ctx context.Context, key, value string) error
	DeleteKV(ctx context.Context, key string) error
}

// LeadStatus is a project's lead as the API shows it: the lifecycle's status, or state "off"
// when it is not running.
type LeadStatus struct {
	orch.Status
	ProjectID int64  `json:"project_id"`
	Slug      string `json:"slug"`
	Running   bool   `json:"running"`
}

// StateOff is the state of a lead crowsnest is not keeping.
const StateOff orch.State = "off"

// Leads keeps the running leads.
type Leads struct {
	projects *Service
	kv       KV
	factory  LeadFactory
	logger   *slog.Logger
	base     context.Context // the server's lifetime; each lead runs under a child of it
	onChange func(LeadStatus)

	mu      sync.Mutex
	running map[int64]*Lead
}

// NewLeads returns the manager. base bounds every lead's watch and poker; onChange (may be nil)
// is told when a lead starts or stops (serve publishes a `lead` event).
func NewLeads(base context.Context, projects *Service, kv KV, factory LeadFactory, logger *slog.Logger, onChange func(LeadStatus)) *Leads {
	if logger == nil {
		logger = slog.Default()
	}
	return &Leads{projects: projects, kv: kv, factory: factory, logger: logger, base: base, onChange: onChange, running: map[int64]*Lead{}}
}

// ErrArchived refuses starting the lead of an archived project.
var ErrArchived = errors.New("the project is archived")

// Start starts (or adopts) a project's lead and returns its status. A lead already running is
// left as it is (and its lifecycle asked to ensure it, which restarts a stopped one).
func (l *Leads) Start(ctx context.Context, projectID int64) (LeadStatus, error) {
	project, err := l.projects.ByID(ctx, projectID)
	if err != nil {
		return LeadStatus{}, err
	}
	if project.Archived {
		return LeadStatus{}, ErrArchived
	}
	l.mu.Lock()
	lead, ok := l.running[projectID]
	if !ok {
		lead = l.launchLocked(project)
	}
	l.mu.Unlock()
	if err := l.kv.SetKV(ctx, kvRunning(project.Slug), "1"); err != nil {
		l.logger.Warn("remembering a running lead", "project", project.Slug, "err", err)
	}
	if ok && lead.Agent.Status().State == orch.StateStopped {
		go func() { _, _ = lead.Agent.Ensure(l.base) }()
	}
	status := l.statusOf(project, lead)
	l.changed(status)
	return status, nil
}

// launchLocked builds the lead and runs its watch and poker; called with l.mu held.
func (l *Leads) launchLocked(project Project) *Lead {
	agent, poker := l.factory(project)
	runCtx, cancel := context.WithCancel(l.base)
	lead := &Lead{Project: project, Agent: agent, Poker: poker, cancel: cancel}
	l.running[project.ID] = lead
	go agent.Run(runCtx)
	if poker != nil {
		go poker.Run(runCtx)
	}
	l.logger.Info("project lead started", "project", project.Slug, "agent", AgentName(project.Slug))
	return lead
}

// Stop stops a project's lead (its Claude exits; the conversation is kept for the next start)
// and forgets that it should be running.
func (l *Leads) Stop(ctx context.Context, projectID int64) (LeadStatus, error) {
	project, err := l.projects.ByID(ctx, projectID)
	if err != nil {
		return LeadStatus{}, err
	}
	if err := l.kv.DeleteKV(ctx, kvRunning(project.Slug)); err != nil {
		l.logger.Warn("forgetting a running lead", "project", project.Slug, "err", err)
	}
	l.mu.Lock()
	lead, ok := l.running[projectID]
	delete(l.running, projectID)
	l.mu.Unlock()
	if !ok {
		return l.statusOf(project, nil), nil
	}
	lead.cancel()
	status, stopErr := lead.Agent.Stop(ctx)
	out := LeadStatus{Status: status, ProjectID: project.ID, Slug: project.Slug}
	out.State = StateOff
	l.changed(out)
	if stopErr != nil {
		return out, fmt.Errorf("stopping the lead of %s: %w", project.Slug, stopErr)
	}
	return out, nil
}

// Resume starts every lead whose running flag survived a restart. Called once at start.
func (l *Leads) Resume(ctx context.Context) {
	projects, err := l.projects.List(ctx, false)
	if err != nil {
		l.logger.Warn("listing projects to resume their leads", "err", err)
		return
	}
	for _, project := range projects {
		if flag, err := l.kv.GetKV(ctx, kvRunning(project.Slug)); err == nil && flag == "1" {
			if _, err := l.Start(ctx, project.ID); err != nil {
				l.logger.Warn("resuming a project lead", "project", project.Slug, "err", err)
			}
		}
	}
}

// Opened applies the "start when opened" policy: the human opened the project in the app.
func (l *Leads) Opened(ctx context.Context, projectID int64) {
	project, err := l.projects.ByID(ctx, projectID)
	if err != nil || project.Archived || project.LeadStart != LeadOnOpen {
		return
	}
	if _, err := l.Start(ctx, projectID); err != nil {
		l.logger.Warn("starting a lead on open", "project", project.Slug, "err", err)
	}
}

// GivenJob applies the start policies when a job is given to a project's lead: "open" and "job"
// both start it. It reports whether the lead is (now) running.
func (l *Leads) GivenJob(ctx context.Context, projectID int64) bool {
	if l.Get(projectID) != nil {
		return true
	}
	project, err := l.projects.ByID(ctx, projectID)
	if err != nil || project.Archived || project.LeadStart == LeadManual {
		return false
	}
	_, err = l.Start(ctx, projectID)
	return err == nil
}

// Get returns a running lead, or nil.
func (l *Leads) Get(projectID int64) *Lead {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running[projectID]
}

// ByPane returns the running lead whose agent is in paneID, or nil.
func (l *Leads) ByPane(paneID string) *Lead {
	if paneID == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, lead := range l.running {
		if lead.Agent.Pane() == paneID {
			return lead
		}
	}
	return nil
}

// ByAgentName returns the running lead with that herdr agent name, or nil.
func (l *Leads) ByAgentName(name string) *Lead {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, lead := range l.running {
		if lead.Agent.AgentName() == name {
			return lead
		}
	}
	return nil
}

// Running returns the running leads, by project name.
func (l *Leads) Running() []*Lead {
	l.mu.Lock()
	out := make([]*Lead, 0, len(l.running))
	for _, lead := range l.running {
		out = append(out, lead)
	}
	l.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Project.Name < out[j].Project.Name })
	return out
}

// Status returns a project's lead status ("off" when not running).
func (l *Leads) Status(ctx context.Context, projectID int64) (LeadStatus, error) {
	project, err := l.projects.ByID(ctx, projectID)
	if err != nil {
		return LeadStatus{}, err
	}
	return l.statusOf(project, l.Get(projectID)), nil
}

func (l *Leads) statusOf(project Project, lead *Lead) LeadStatus {
	if lead == nil {
		return LeadStatus{Status: orch.Status{State: StateOff, Name: AgentName(project.Slug), Dir: project.Dir}, ProjectID: project.ID, Slug: project.Slug}
	}
	return LeadStatus{Status: lead.Agent.Status(), ProjectID: project.ID, Slug: project.Slug, Running: true}
}

func (l *Leads) changed(status LeadStatus) {
	if l.onChange != nil {
		l.onChange(status)
	}
}
