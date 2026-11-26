// Wiring for jobs (M7): the jobs service with its launcher configuration, the roles from
// ~/.config/crowsnest/roles, --notify-url for "tell Jeff", the transcript-based heartbeat (M4's
// tailer), and the supervisor tick. Under --demo the board is seeded with scripted jobs.

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

// jobRefs are the services built after the jobs service that it reads through closures.
type jobRefs struct {
	orchestrator  *orch.Orchestrator
	conversations *transcript.Manager
}

// newJobService builds the jobs service. refs is filled in by serve once the orchestrator and
// the transcript manager exist.
func newJobService(cfg config.Config, client herdr.Client, db *store.Store, events *hub.Hub, socket string, identity *agentid.Registry, refs *jobRefs, logger *slog.Logger) *jobs.Service {
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
	claudeDir := claudeConfigDir(cfg)
	return jobs.New(jobs.Options{
		Store: db, Hub: events, Logger: logger, Herdr: client, Roles: roles, Notify: notify,
		Launch: jobs.LaunchOptions{
			StateDir: cfg.StateDir, Executable: executable, AgentSocket: socket, BinDir: binDir, BasePath: os.Getenv("PATH"),
			Session: cfg.Session, PermissionMode: cfg.PermissionMode, ClaudeConfigDir: claudeDir,
			Identity: identity,
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
	go service.Run(ctx, 0)
}
