// The start gate and the launch settings (DESIGN.md 7.12, the guided first run).
//
// The gate is how crowsnest comes up on a machine that is not ready yet: herdr or every agent
// missing, or the owner still on the first-run checklist. Run does not start anything while the
// gate gives a reason; the orchestrator shows StateWaiting with that reason, and the watch keeps
// asking, so the orchestrator starts by itself once herdr or an agent appears, without a
// restart. Kick asks again at once (the checklist's Start button, a settings change).
//
// Launch settings are read at every start rather than fixed in Options, so a model, permission
// mode, Remote Control or engine changed in the app's Settings takes effect on the next
// (re)start of the orchestrator.

package orch

import (
	"context"
	"time"
)

// StateWaiting: the gate said not yet (herdr or an agent missing, first run not finished).
const StateWaiting State = "waiting"

// gatePollInterval is how often a waiting orchestrator asks the gate again.
const gatePollInterval = 3 * time.Second

// Engines the orchestrator can run on.
const (
	EngineClaude = "claude"
	EngineCodex  = "codex"
)

// LaunchSettings are what the orchestrator is started with.
type LaunchSettings struct {
	Engine            string // EngineClaude (default) or EngineCodex
	Model             string
	PermissionMode    string
	RemoteControlName string // "" for none
}

// launchSettings returns the current launch settings: Options.Launch when set, else the fixed
// Options fields.
func (o *Orchestrator) launchSettings() LaunchSettings {
	settings := LaunchSettings{Engine: EngineClaude, Model: o.opts.Model, PermissionMode: o.opts.PermissionMode, RemoteControlName: o.opts.RemoteControlName}
	if o.opts.Launch != nil {
		settings = o.opts.Launch()
	}
	if settings.Engine == "" {
		settings.Engine = EngineClaude
	}
	return settings
}

// gateReason asks the gate; "" means go.
func (o *Orchestrator) gateReason(ctx context.Context) string {
	if o.opts.Gate == nil {
		return ""
	}
	return o.opts.Gate(ctx)
}

// Kick makes a waiting orchestrator ask its gate again now.
func (o *Orchestrator) Kick() {
	select {
	case o.kick <- struct{}{}:
	default:
	}
}

// waitForGate blocks until the gate says go or ctx ends, showing the reason meanwhile.
func (o *Orchestrator) waitForGate(ctx context.Context) bool {
	ticker := time.NewTicker(gatePollInterval)
	defer ticker.Stop()
	for {
		reason := o.gateReason(ctx)
		if reason == "" {
			return true
		}
		o.setWaiting(reason)
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		case <-o.kick:
		}
	}
}

// setWaiting shows StateWaiting with reason, publishing only when something changed.
func (o *Orchestrator) setWaiting(reason string) {
	current := o.Status()
	if current.State == StateWaiting && current.Detail == reason {
		return
	}
	o.update(func(s *Status) { s.State, s.Detail = StateWaiting, reason })
}

// codexFirstPrompt is typed into a Codex orchestrator once it is ready: Codex has no flag for an
// extra system prompt, so it is pointed at the instructions file instead.
func codexFirstPrompt(instructions string) string {
	return "[crowsnest] You are this crowsnest's orchestrator. Read " + instructions +
		" now and follow it for the rest of this session (you have no MCP tools: use the `crowsnest` command it describes). Then reply exactly: ready"
}
