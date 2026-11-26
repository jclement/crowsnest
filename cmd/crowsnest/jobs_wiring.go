// Wiring for jobs (M7): the jobs service with its launcher configuration, the roles from
// ~/.config/crowsnest/roles, --notify-url for "tell Jeff", the transcript-based heartbeat (M4's
// tailer), and the supervisor tick. Under --demo the board is seeded with scripted jobs.

package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

// jobRefs are the services built after the jobs service that it reads through closures.
type jobRefs struct {
	orchestrator  *orch.Orchestrator
	conversations *transcript.Manager
	// questions asks the human about jobs an agent wants beyond its own directory (R2-H1). It is
	// set once startQuestions returns, while the agent socket may already be serving, hence atomic.
	questions atomic.Pointer[questions.Service]
	// notify tells Jeff something (the log, and the --notify-url POST when set); the poker's
	// draft alert goes through it too.
	notify func(text string)
	// projects are the projects and their leads (DESIGN.md 21); nil in tests that need none.
	projects *projectWiring
}

// newJobService builds the jobs service. refs is filled in by serve once the orchestrator and
// the transcript manager exist.
func newJobService(cfg config.Config, client herdr.Client, db *store.Store, events *hub.Hub, socket string, identity *agentid.Registry, refs *jobRefs, journalLog *journal.Journal, files *attachWiring, meter jobs.SpendReader, logger *slog.Logger) *jobs.Service {
	binDir, executable := executablePaths()
	roles, problems := jobs.LoadRoles(rolesDir(cfg))
	for _, problem := range problems {
		logger.Warn("skipping a role file", "err", problem)
	}
	notify := func(text string) { logger.Info("job notice", "text", text) }
	if cfg.NotifyURL != "" {
		post := questions.NewTextNotifier(cfg.NotifyURL, nil, logger)
		notify = func(text string) {
			logger.Info("job notice", "text", text)
			post(text)
		}
	}
	refs.notify = notify
	alert := func(text string) {
		// A supervisor alert is journalled as well as sent, so the Journal shows what Jeff was told.
		journalLog.Add(context.Background(), journal.KindAlert, text, nil)
		notify(text)
	}
	claudeDir := claudeConfigDir(cfg)
	var projectSource jobs.ProjectSource
	if refs.projects != nil {
		projectSource = refs.projects.source()
	}
	return jobs.New(jobs.Options{
		Projects: projectSource,
		Store: db, Hub: events, Logger: logger, Herdr: client, Roles: roles, Notify: alert, DefaultModel: cfg.JobModel,
		Attachments: files, Spend: meter,
		Launch: jobs.LaunchOptions{
			StateDir: cfg.StateDir, Executable: executable, AgentSocket: socket, BinDir: binDir, BasePath: os.Getenv("PATH"),
			Session: cfg.Session, PermissionMode: cfg.PermissionMode, ClaudeConfigDir: claudeDir,
			Identity: identity, RemoteControlPrefix: jobRemoteControlPrefix(cfg),
		},
		Orchestrator: func() jobs.OrchestratorInfo {
			if refs.orchestrator == nil {
				return jobs.OrchestratorInfo{}
			}
			status := refs.orchestrator.Status()
			ready := status.State == orch.StateIdle || status.State == orch.StateWorking
			return jobs.OrchestratorInfo{PaneID: status.PaneID, Ready: ready}
		},
		Activity: transcriptActivity(claudeDir, cfg.Dir, refs),
		AskHuman: func(ctx context.Context, input questions.AskInput, caller questions.Caller) (questions.Question, error) {
			service := refs.questions.Load()
			if service == nil {
				return questions.Question{}, errors.New("the inbox is not ready yet")
			}
			return service.Ask(ctx, input, caller)
		},
	})
}

// transcriptActivity reports when a job agent's transcript last grew (its file's mtime) and the
// last tool call the tailer saw.
func transcriptActivity(claudeDir, orchestratorDir string, refs *jobRefs) jobs.ActivityFunc {
	return func(ctx context.Context, job jobs.Job) (time.Time, string) {
		dir, session := job.WorkDir, job.SessionID
		if job.AgentName == jobs.OrchestratorName && refs.orchestrator != nil {
			dir, session = orchestratorDir, refs.orchestrator.Status().SessionID
		}
		if session == "" || dir == "" {
			return time.Time{}, ""
		}
		path, err := transcript.Locate(claudeDir, dir, session)
		if err != nil {
			return time.Time{}, ""
		}
		info, err := os.Stat(path)
		if err != nil {
			return time.Time{}, ""
		}
		return info.ModTime(), lastTool(ctx, refs.conversations, job.PaneID)
	}
}

// lastTool is the summary of the newest tool card in the agent's conversation.
func lastTool(ctx context.Context, conversations *transcript.Manager, paneID string) string {
	if conversations == nil || paneID == "" {
		return ""
	}
	page, err := conversations.Messages(ctx, paneID, "", 20)
	if err != nil {
		return ""
	}
	for i := len(page.Messages) - 1; i >= 0; i-- {
		if tool := page.Messages[i].Tool; tool != nil {
			if tool.Summary != "" {
				return tool.Name + ": " + tool.Summary
			}
			return tool.Name
		}
	}
	return ""
}

// executablePaths is the running binary and its directory, symlinks resolved.
func executablePaths() (binDir, executable string) {
	path, err := os.Executable()
	if err != nil {
		return "", ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Dir(path), path
}

// rolesDir is ~/.config/crowsnest/roles (next to the config file).
func rolesDir(cfg config.Config) string {
	if cfg.ConfigFile != "" {
		return filepath.Join(filepath.Dir(cfg.ConfigFile), "roles")
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "crowsnest", "roles")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "crowsnest", "roles")
	}
	return ""
}

// startJobs runs the supervisor (and, under --demo, seeds the board).
func startJobs(ctx context.Context, service *jobs.Service, client herdr.Client, logger *slog.Logger) {
	if demo, ok := client.(*fake.Demo); ok {
		panes := jobs.DemoPanes{
			Orchestrator: demo.PaneOf(fake.DemoOrchestrator), Builder: demo.PaneOf(fake.DemoBuilder),
			Reviewer: demo.PaneOf(fake.DemoReviewer), Docs: demo.PaneOf(fake.DemoDocs), Linter: demo.PaneOf(fake.DemoLinter),
		}
		if err := jobs.SeedDemo(ctx, service, panes); err != nil {
			logger.Warn("seeding the demo jobs", "err", err)
		}
		return // the demo's jobs are scripted; the supervisor would "recover" them
	}
	service.ReconcileApprovals(ctx) // a decision that landed just before a restart (R2-H1)
	go service.Run(ctx, 0)
}
