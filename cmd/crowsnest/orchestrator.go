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
	"sync"

	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/guard"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/instructions"
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

// orchGate is the orchestrator's start gate and launch settings (internal/system).
type orchGate struct {
	gate   func(ctx context.Context) string
	launch func() orch.LaunchSettings
}

// startOrchestrator builds the orchestrator and the transcript manager, starts both running until
// ctx ends, and returns them for the HTTP server. With --no-orchestrator the orchestrator reports
// itself disabled and the conversations of other agents still work.
func startOrchestrator(ctx context.Context, cfg config.Config, client herdr.Client, db *store.Store, identity *agentid.Registry, events *hub.Hub, agentSocket string, talk *voice.Service, journalLog *journal.Journal, guardRules *guard.Rules, layers *instructions.Service, gate orchGate, ctl *controls, logger *slog.Logger) (*orch.Orchestrator, *transcript.Manager) {
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
	// The orchestrator's CROWSNEST.md, then the human's global instruction layer (DESIGN.md 21.3),
	// recorded so "instructions changed since start" works for it too.
	render := func() (string, error) {
		body, err := orch.RenderInstructions(orch.InstructionData{Session: cfg.Session, Dir: cfg.Dir, Version: version})
		if err != nil || layers == nil {
			return body, err
		}
		extra, _, err := layers.ForStandingAgent(ctx, orch.Name, 0, "")
		if err != nil {
			logger.Warn("composing the orchestrator's instruction layers", "err", err)
		}
		if extra != "" {
			body += "\n\n" + extra
		}
		return body, nil
	}
	orchestrator := orch.New(orch.Options{
		Render: render, Paused: ctl.paused, ExtraEnv: ctl.heavyEnv(),
		Client: client, KV: db, Hub: events, Journal: journalLog, Logger: logger, Session: cfg.Session, Dir: cfg.Dir,
		StateDir: cfg.StateDir, Model: cfg.Model, PermissionMode: cfg.PermissionMode, Version: version,
		RemoteControlName: orchestratorRemoteControl(cfg), Gate: gate.gate, Launch: gate.launch,
		BinDir: binDir, Executable: executable, AgentSocket: agentSocket, BasePath: os.Getenv("PATH"), ClaudeConfigDir: claudeDir, Disabled: cfg.NoOrchestrator, Identity: identity, Guard: guardRules,
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
			standingReplies.changed(ctx, paneID)
		},
	})
	if talk != nil {
		replies = voice.NewReplyWatcher(talk, manager, orch.Name)
		standingReplies.talk, standingReplies.source = talk, manager
	}
	go manager.Run(ctx)
	go orchestrator.Run(ctx)
	logger.Info("conversations follow Claude transcripts", "claude_config_dir", claudeDir, "orchestrator", !cfg.NoOrchestrator)
	return orchestrator, manager
}

// leadReplies speaks the SAY lines of project leads (DESIGN.md 13.5): one reply watcher per
// lead, made when the lead first reports a status, fed like the orchestrator's. Without talk
// mode it does nothing.
type leadReplies struct {
	mu       sync.Mutex
	talk     *voice.Service
	source   voice.MessageSource
	watchers map[string]*voice.ReplyWatcher // by agent name
}

// standingReplies is the process's one leadReplies (serve builds one server per process).
var standingReplies = &leadReplies{watchers: map[string]*voice.ReplyWatcher{}}

// status tells the lead's watcher whether it is idle (a finished turn's reply is then read).
func (l *leadReplies) status(ctx context.Context, agent string, idle bool, paneID string) {
	l.mu.Lock()
	if l.talk == nil {
		l.mu.Unlock()
		return
	}
	watcher, ok := l.watchers[agent]
	if !ok {
		watcher = voice.NewReplyWatcher(l.talk, l.source, agent)
		l.watchers[agent] = watcher
	}
	l.mu.Unlock()
	go watcher.Status(ctx, idle, paneID)
}

// changed passes a conversation change to every lead's watcher (each ignores other panes).
func (l *leadReplies) changed(ctx context.Context, paneID string) {
	l.mu.Lock()
	watchers := make([]*voice.ReplyWatcher, 0, len(l.watchers))
	for _, watcher := range l.watchers {
		watchers = append(watchers, watcher)
	}
	l.mu.Unlock()
	for _, watcher := range watchers {
		go watcher.Changed(ctx, paneID)
	}
}
