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

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
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
func startOrchestrator(ctx context.Context, cfg config.Config, client herdr.Client, db *store.Store, events *hub.Hub, logger *slog.Logger) (*orch.Orchestrator, *transcript.Manager) {
	claudeDir := claudeConfigDir(cfg)
	binDir := ""
	if executable, err := os.Executable(); err == nil {
		binDir = filepath.Dir(executable)
	}
	var manager *transcript.Manager
	orchestrator := orch.New(orch.Options{
		Client: client, KV: db, Hub: events, Logger: logger, Session: cfg.Session, Dir: cfg.Dir,
		StateDir: cfg.StateDir, Model: cfg.Model, PermissionMode: cfg.PermissionMode, Version: version,
		BinDir: binDir, BasePath: os.Getenv("PATH"), ClaudeConfigDir: claudeDir, Disabled: cfg.NoOrchestrator,
		OnStatus: func(status orch.Status) {
			// Follow the orchestrator's conversation from the moment it has a pane, so its
			// `message` events flow before any browser asks.
			if status.PaneID != "" && manager != nil {
				go func() { _ = manager.Pin(ctx, orch.Name) }()
			}
		},
	})
	manager = transcript.NewManager(transcript.ManagerOptions{
		ConfigDir: claudeDir, Logger: logger,
		Lookup:   orch.TranscriptLookup(client, orchestrator),
		Fallback: orch.PaneFallback(client),
		Notify: func(paneID, cursor string) {
			events.Publish(hub.Event{Type: "message", Data: MessageEvent{PaneID: paneID, Cursor: cursor}})
		},
	})
	go manager.Run(ctx)
	go orchestrator.Run(ctx)
	logger.Info("conversations follow Claude transcripts", "claude_config_dir", claudeDir, "orchestrator", !cfg.NoOrchestrator)
	return orchestrator, manager
}
