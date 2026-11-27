// Wiring for projects and their leads (DESIGN.md 21): the projects service, the adapter the jobs
// service sees projects and running leads through, and the factory that builds a project's lead
// from the same pieces as the orchestrator (internal/orch's lifecycle, its own poker with the
// timer and the upkeep the orchestrator has, Remote Control named "<instance> <project>", the
// CROWSNEST.md template with the project named, plus the human's instruction layers).

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/guard"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/instructions"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

// projectWiring holds the projects service and, once serve has built them, the leads.
type projectWiring struct {
	service *projects.Service
	leads   atomic.Pointer[projects.Leads]
	// extra renders the human's instruction layers for a lead (internal/instructions); nil adds
	// nothing.
	extra atomic.Pointer[func(ctx context.Context, project projects.Project, record bool) string]
	// taskSource gives a lead's poker its project's scheduled tasks; nil gives none.
	taskSource atomic.Pointer[func(projectID int64) orch.TaskSource]
	// jobs is the jobs service, for a lead poker's spend ceiling and usage-limit hold (6.16).
	jobs atomic.Pointer[jobs.Service]
	// upkeep gives a lead's poker its timer list and its tender; nil gives neither.
	upkeep atomic.Pointer[leadUpkeep]
}

// leadUpkeep is what a lead's poker shares with the orchestrator's (DESIGN.md 7.4, 7.9): the
// services its timer list reads, the conversations its context size is read from, and the
// orchestrator's poker and tender, whose live settings a lead's timer and thresholds follow.
type leadUpkeep struct {
	jobs          *jobs.Service
	questions     *questions.Service
	conversations *transcript.Manager
	poker         *orch.Poker  // the orchestrator's; nil: the lead keeps --poke-interval
	tender        *orch.Tender // the orchestrator's; nil: the lead keeps the flags' thresholds
}

// useUpkeep gives leads started from now on their timer list and their tender.
func (w *projectWiring) useUpkeep(upkeep leadUpkeep) { w.upkeep.Store(&upkeep) }

func newProjectWiring(cfg config.Config, db *store.Store, events *hub.Hub) *projectWiring {
	roles, _ := jobs.LoadRoles(rolesDir(cfg))
	home, _ := os.UserHomeDir()
	return &projectWiring{service: projects.New(projects.Options{Store: db, Hub: events, Roles: roles.Has, Home: home})}
}

// source is the jobs service's view of projects and leads.
func (w *projectWiring) source() jobs.ProjectSource { return projectSource{w: w} }

type projectSource struct{ w *projectWiring }

func projectInfo(project projects.Project) jobs.ProjectInfo {
	return jobs.ProjectInfo{
		ID: project.ID, Slug: project.Slug, Name: project.Name, Dir: project.Dir, RootDir: project.RootDir,
		DefaultRole: project.DefaultRole, DefaultEngine: project.DefaultEngine, DefaultModel: project.DefaultModel,
		DefaultIsolate: project.DefaultIsolate, Archived: project.Archived,
	}
}

func (p projectSource) Project(ctx context.Context, id int64) (jobs.ProjectInfo, bool) {
	project, err := p.w.service.ByID(ctx, id)
	if err != nil {
		return jobs.ProjectInfo{}, false
	}
	return projectInfo(project), true
}

func (p projectSource) ForDir(ctx context.Context, dir string) (jobs.ProjectInfo, bool) {
	project, ok := p.w.service.ForDir(ctx, dir)
	if !ok {
		return jobs.ProjectInfo{}, false
	}
	return projectInfo(project), true
}

func standingOf(lead *projects.Lead) jobs.StandingAgent {
	status := lead.Agent.Status()
	ready := status.State == orch.StateIdle || status.State == orch.StateWorking
	return jobs.StandingAgent{Name: lead.Agent.AgentName(), PaneID: status.PaneID, Ready: ready, ProjectID: lead.Project.ID, RootDir: lead.Project.RootDir}
}

func (p projectSource) Leads() []jobs.StandingAgent {
	leads := p.w.leads.Load()
	if leads == nil {
		return nil
	}
	var out []jobs.StandingAgent
	for _, lead := range leads.Running() {
		out = append(out, standingOf(lead))
	}
	return out
}

func (p projectSource) LeadByPane(paneID string) (jobs.StandingAgent, bool) {
	leads := p.w.leads.Load()
	if leads == nil {
		return jobs.StandingAgent{}, false
	}
	lead := leads.ByPane(paneID)
	if lead == nil {
		return jobs.StandingAgent{}, false
	}
	return standingOf(lead), true
}

func (p projectSource) GivenJob(ctx context.Context, projectID int64) {
	if leads := p.w.leads.Load(); leads != nil {
		go leads.GivenJob(context.WithoutCancel(ctx), projectID)
	}
}

// leadDeps are what a lead is built from: the orchestrator's own dependencies.
type leadDeps struct {
	cfg         config.Config
	client      herdr.Client
	db          *store.Store
	events      *hub.Hub
	journal     *journal.Journal
	identity    *agentid.Registry
	agentSocket string
	guard       *guard.Rules // the guard hook's rules (8.7), as the orchestrator's
	logger      *slog.Logger
	// onStatus is told every status change of every lead (serve pins its conversation).
	onStatus func(project projects.Project, status orch.Status)
	// ctl are the pause and presence (landing_wiring.go); nil: neither.
	ctl *controls
}

// leadRemoteControl is a lead's Remote Control name, "<instance> <project>", or "" when off.
func leadRemoteControl(cfg config.Config, project projects.Project) string {
	if cfg.NoRemoteControl || cfg.Demo {
		return ""
	}
	return strings.TrimSpace(cfg.Name + " " + project.Name)
}

// leadFactory builds a project's lead and its poker.
func (w *projectWiring) leadFactory(deps leadDeps) projects.LeadFactory {
	return func(project projects.Project) (*orch.Orchestrator, *orch.Poker) {
		binDir, executable := executablePaths()
		permission := project.PermissionMode
		if permission == "" {
			permission = deps.cfg.PermissionMode
		}
		var agent *orch.Orchestrator
		agent = orch.New(orch.Options{
			Client: deps.client, KV: deps.db, Hub: nil, Journal: deps.journal, Logger: deps.logger.With("lead", project.Slug),
			Session: deps.cfg.Session, Dir: project.Dir, StateDir: filepath.Join(deps.cfg.StateDir, "leads", project.Slug),
			Model: deps.cfg.Model, PermissionMode: permission, Version: version,
			RemoteControlName: leadRemoteControl(deps.cfg, project),
			DemoRemoteControl: demoRemoteControl(deps.cfg, project.Name, "lead"+project.Slug),
			BinDir:            binDir, Executable: executable, AgentSocket: deps.agentSocket, BasePath: os.Getenv("PATH"),
			ClaudeConfigDir: claudeConfigDir(deps.cfg), Identity: deps.identity, Guard: deps.guard,
			AgentName: projects.AgentName(project.Slug), Workspace: "lead " + project.Slug, KVPrefix: "lead." + project.Slug,
			Render:   func() (string, error) { return w.leadInstructions(deps.cfg.Session, project, true) },
			Guidance: func() (string, error) { return w.leadInstructions(deps.cfg.Session, project, false) },
			// An adopted lead started with older instructions is restarted for them (7.5).
			AutoInstructionRestart: deps.ctl.instructionRestart,
			HeldJobBusy:            func(ctx context.Context) bool { return heldJobBusy(deps.db, agent)(ctx) },
			Paused:                 deps.ctl.paused,
			Gate:                   herdrGate(deps.client),
			ExtraEnv:               deps.ctl.heavyEnv(),
			OnStatus: func(status orch.Status) {
				if deps.onStatus != nil {
					deps.onStatus(project, status)
				}
			},
		})
		options := w.leadPokerOptions(deps, project, agent)
		if service := w.jobs.Load(); service != nil {
			options.Hold = standingHold(service, "lead:"+project.Slug)
		}
		return agent, orch.NewPoker(options)
	}
}

// leadPokerOptions are a lead's poker: its own bookkeeping and label, no "another agent
// settled" reasons, its project's tasks, the orchestrator's timer interval with its project's
// standing list, and a tender of its own.
func (w *projectWiring) leadPokerOptions(deps leadDeps, project projects.Project, agent *orch.Orchestrator) orch.PokerOptions {
	options := orch.PokerOptions{
		Client: deps.client, KV: deps.db, Journal: deps.journal, Hub: deps.events, Logger: deps.logger.With("lead", project.Slug),
		Status: agent.Status, ManualOnly: deps.cfg.Demo, NoAgentReasons: true,
		StateKey: "lead." + project.Slug + ".poke.state", Label: "the lead of " + project.Name,
		Interval: deps.cfg.PokeInterval,
	}
	if deps.ctl != nil { // the pause holds a lead's pokes, timer, escalations and upkeep too (6.13)
		options.Paused, options.Held, options.Presence = deps.ctl.paused, deps.ctl.held, deps.ctl.presenceWord
	}
	if source := w.taskSource.Load(); source != nil {
		options.Tasks = (*source)(project.ID)
	}
	upkeep := w.upkeep.Load()
	if upkeep == nil {
		return options
	}
	if upkeep.poker != nil {
		options.IntervalFrom = upkeep.poker.Interval
	}
	options.Standing = leadStandingWork(upkeep.jobs, upkeep.questions, project.ID)
	if upkeep.conversations != nil {
		options.Tender = newLeadTender(deps, project, agent, *upkeep)
	}
	return options
}

// leadInstructions is a lead's CROWSNEST.md: the orchestrator's template with the project named,
// then the human's instruction layers (global, the lead role, the project).
func (w *projectWiring) leadInstructions(session string, project projects.Project, record bool) (string, error) {
	body, err := orch.RenderInstructions(orch.InstructionData{
		Session: session, Project: project.Slug, ProjectName: project.Name, Description: project.Description, Dir: project.Dir, Version: version,
	})
	if err != nil {
		return "", err
	}
	if extra := w.extra.Load(); extra != nil {
		if layers := (*extra)(context.Background(), project, record); layers != "" {
			body += "\n\n" + layers
		}
	}
	return body, nil
}

// subjectOf names the agent in a pane for the instruction records: "lead:<slug>" for a lead's
// pane, "" for anything else (the agent socket then files it as the orchestrator's).
func (w *projectWiring) subjectOf(_ context.Context, paneID string) string {
	if leads := w.leads.Load(); leads != nil {
		if lead := leads.ByPane(paneID); lead != nil {
			return projects.Subject(lead.Project.Slug)
		}
	}
	return ""
}

// leadProjectInPane is the project whose lead is in paneID.
func (w *projectWiring) leadProjectInPane(paneID string) (projects.Project, bool) {
	if leads := w.leads.Load(); leads != nil {
		if lead := leads.ByPane(paneID); lead != nil {
			return lead.Project, true
		}
	}
	return projects.Project{}, false
}

// leadInPane is the agent name of the lead in paneID, "" when none is there.
func (w *projectWiring) leadInPane(paneID string) string {
	if leads := w.leads.Load(); leads != nil {
		if lead := leads.ByPane(paneID); lead != nil {
			return lead.Agent.AgentName()
		}
	}
	return ""
}

// useInstructions makes leads start with the human's global and project layers, recorded per
// lead so "instructions changed since start" works for them as for jobs.
func (w *projectWiring) useInstructions(layers *instructions.Service, logger *slog.Logger) {
	if layers == nil {
		return
	}
	extra := func(ctx context.Context, project projects.Project, record bool) string {
		if !record {
			text, _ := layers.StandingAgentText(ctx, projects.Subject(project.Slug), project.ID, project.Name)
			return text
		}
		text, _, err := layers.ForStandingAgent(ctx, projects.Subject(project.Slug), project.ID, project.Name)
		if err != nil {
			logger.Warn("composing a lead's instruction layers", "project", project.Slug, "err", err)
		}
		return text
	}
	w.extra.Store(&extra)
}

// startLeads builds the lead manager, publishes `lead` events, and resumes the leads that were
// running when crowsnest last stopped.
func (w *projectWiring) startLeads(ctx context.Context, deps leadDeps) *projects.Leads {
	leads := projects.NewLeads(ctx, w.service, deps.db, w.leadFactory(deps), deps.logger, func(status projects.LeadStatus) {
		deps.events.Publish(hub.Event{Type: "lead", Data: status})
	})
	w.leads.Store(leads)
	go leads.Resume(ctx)
	return leads
}

// herdrGate is a lead's start gate: wait while the lazy herdr client has not connected yet (at
// start-up, or herdr not installed), instead of failing the lead's start. The orchestrator's gate
// (system.Gate) also waits for the first-run checklist; a lead needs only herdr.
func herdrGate(client herdr.Client) func(context.Context) string {
	lazy, ok := client.(*herdr.Lazy)
	if !ok {
		return nil
	}
	return func(context.Context) string {
		if lazy.Connected() {
			return ""
		}
		return "herdr is not running yet; the lead starts as soon as it is."
	}
}
