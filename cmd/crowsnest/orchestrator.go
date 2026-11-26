// Wiring for the orchestrator and the conversations (M4): the transcript manager that follows
// agents' Claude transcripts and publishes `message` events, and the orchestrator lifecycle that
// keeps the session's lead Claude running. Under --demo the transcripts are the demo's own,
// written under the state directory, never under the user's ~/.claude.

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
	"github.com/jclement/crowsnest/internal/voice"
)

// MessageEvent is the data of a `message` event: the conversation of the agent in PaneID changed,
// and Cursor covers the change. The web app refetches that agent's messages after its own cursor.
type MessageEvent struct {
	PaneID string `json:"pane_id"`
	Cursor string `json:"cursor"`
}

// claudeConfigDir is where transcripts are read from: Claude's own config dir, or the demo's.
func claudeConfigDir(cfg config.Config) string {
	if cfg.Demo {
		return filepath.Join(cfg.StateDir, "demo-claude")
	}
	dir, err := transcript.ConfigDir(os.Getenv)
	if err != nil {
		return filepath.Join(cfg.StateDir, "no-claude-config")
	}
	return dir
}

// startOrchestrator builds the orchestrator and the transcript manager, starts both running until
// ctx ends, and returns them for the HTTP server. With --no-orchestrator the orchestrator reports
// itself disabled and the conversations of other agents still work.
func startOrchestrator(ctx context.Context, cfg config.Config, client herdr.Client, db *store.Store, identity *agentid.Registry, events *hub.Hub, agentSocket string, talk *voice.Service, journalLog *journal.Journal, logger *slog.Logger) (*orch.Orchestrator, *transcript.Manager) {
	claudeDir := claudeConfigDir(cfg)
	binDir, executable := "", ""
	if path, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
		binDir, executable = filepath.Dir(path), path
	}
	var manager *transcript.Manager
	var replies *voice.ReplyWatcher // talk mode: speaks the orchestrator's SAY lines
	orchestrator := orch.New(orch.Options{
		Client: client, KV: db, Hub: events, Journal: journalLog, Logger: logger, Session: cfg.Session, Dir: cfg.Dir,
		StateDir: cfg.StateDir, Model: cfg.Model, PermissionMode: cfg.PermissionMode, Version: version,
		BinDir: binDir, Executable: executable, AgentSocket: agentSocket, BasePath: os.Getenv("PATH"), ClaudeConfigDir: claudeDir, Disabled: cfg.NoOrchestrator, Identity: identity,
		OnStatus: func(status orch.Status) {
			// Follow the orchestrator's conversation from the moment it has a pane, so its
			// `message` events flow before any browser asks.
			if status.PaneID != "" && manager != nil {
				go func() { _ = manager.Pin(ctx, orch.Name) }()
			}
			if replies != nil {
				go replies.Status(ctx, status.State == orch.StateIdle, status.PaneID)
			}
		},
	})
	manager = transcript.NewManager(transcript.ManagerOptions{
		ConfigDir: claudeDir, Logger: logger,
		Lookup:   orch.TranscriptLookup(client, orchestrator),
		Fallback: orch.PaneFallback(client),
		Notify: func(paneID, cursor string) {
			events.Publish(hub.Event{Type: "message", Data: MessageEvent{PaneID: paneID, Cursor: cursor}})
			if replies != nil {
				go replies.Changed(ctx, paneID)
			}
		},
	})
	if talk != nil {
		replies = voice.NewReplyWatcher(talk, manager, orch.Name)
	}
	go manager.Run(ctx)
	go orchestrator.Run(ctx)
	logger.Info("conversations follow Claude transcripts", "claude_config_dir", claudeDir, "orchestrator", !cfg.NoOrchestrator)
	return orchestrator, manager
}
