// Instructions that changed since the start (DESIGN.md 7.5). crowsnest adopts a live standing agent
// across its own restarts without restarting Claude, so an agent started yesterday still follows
// yesterday's prompt file, hooks, settings and MCP configuration: the orchestrator went on saying
// it writes SAY lines only "when the header says talk=on", a rule the template had since dropped.
//
// Every start records a fingerprint of what it wrote (the rendered instructions and the settings,
// guard and MCP files, the per-start token aside). On adoption and on every watch tick the
// fingerprint of what crowsnest would write now is compared with it; an agent with none recorded
// (started by an older crowsnest) counts as changed. A changed agent is marked
// (Status.InstructionsStale) and, unless the Setting turns it off or the automation is paused, is
// restarted on the same conversation (--resume) at its next idle moment: idle in herdr, nothing in
// its input box, no dialog, and no step crowsnest is taking on a job it holds. At most once per
// instructionRestartGap, so a template that renders differently each time cannot loop.

package orch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
)

const (
	// kvStartFingerprint holds the fingerprint of what the agent's last start wrote.
	kvStartFingerprint = "start_fingerprint"
	// kvConversationFingerprint is the fingerprint the current conversation began with, and
	// kvInstructionsNotice marks a resumed conversation whose instructions changed since.
	kvConversationFingerprint = "conversation_fingerprint"
	kvInstructionsNotice      = "instructions_notice"
	// instructionCheckEvery spaces the comparisons (each renders the instructions).
	instructionCheckEvery = 30 * time.Second
	// instructionRestartGap is the least time between two restarts for new instructions.
	instructionRestartGap = 10 * time.Minute
)

// fingerprint hashes what a start writes for body: the instructions, the Claude settings with its
// hooks, the guard rules and the MCP configuration. The token is left out (it is new every start).
func (o *Orchestrator) fingerprint(body string) string {
	tokenFile, guardRules := "", ""
	if o.opts.Identity != nil && o.mcpEnabled() {
		tokenFile = filepath.Join(o.opts.StateDir, TokenFile)
	}
	if o.opts.Guard != nil && o.mcpEnabled() {
		guardRules = filepath.Join(o.opts.StateDir, GuardRulesFile)
	}
	hash := sha256.New()
	write := func(value any) {
		encoded, _ := json.Marshal(value)
		hash.Write(encoded)
		hash.Write([]byte{0})
	}
	// The version named in the file is not a change of instructions: a rebuild alone (every
	// `mise run dev`) must not restart the agents.
	if o.opts.Version != "" {
		body = strings.ReplaceAll(body, o.opts.Version, "<version>")
	}
	write(body)
	write(o.claudeSettings(tokenFile, guardRules))
	write(o.opts.Guard)
	if o.mcpEnabled() {
		write(MCPConfig(o.opts.Executable, o.opts.AgentSocket, ""))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// recordFingerprint stores the fingerprint of what this start wrote.
func (o *Orchestrator) recordFingerprint(ctx context.Context, body string) {
	if err := o.opts.KV.SetKV(ctx, o.kvKey(kvStartFingerprint), o.fingerprint(body)); err != nil {
		o.opts.Logger.Warn("recording what the agent was started with", "agent", o.agentName(), "err", err)
	}
}

// guidance is what a start would write now, without the side effects of Render (Options.Guidance;
// else Render, which the default and the tests keep free of them).
func (o *Orchestrator) guidance() (string, error) {
	switch {
	case o.opts.Guidance != nil:
		return o.opts.Guidance()
	case o.opts.Render != nil:
		return o.opts.Render()
	}
	return o.Instructions()
}

// instructionsStale reports whether what a start would write now differs from what the agent's
// last start wrote (no record: it was started by an older crowsnest, so yes).
func (o *Orchestrator) instructionsStale(ctx context.Context) (bool, error) {
	body, err := o.guidance()
	if err != nil {
		return false, err
	}
	have, err := o.opts.KV.GetKV(ctx, o.kvKey(kvStartFingerprint))
	if err != nil {
		have = ""
	}
	return have != o.fingerprint(body), nil
}

// checkInstructions compares, marks, and (with restart, from the watch) restarts a live agent
// whose instructions changed at its next idle moment. The lifecycle lock is held.
func (o *Orchestrator) checkInstructions(ctx context.Context, agent herdr.Agent, restart bool) {
	now := o.opts.Now()
	o.mu.Lock()
	due := o.instructionsAt.IsZero() || now.Sub(o.instructionsAt) >= instructionCheckEvery || !restart
	if due {
		o.instructionsAt = now
	}
	lastRestart := o.instructionsRestartAt
	o.mu.Unlock()
	if !due {
		return
	}
	stale, err := o.instructionsStale(ctx)
	if err != nil {
		o.opts.Logger.Debug("comparing the agent's instructions", "agent", o.agentName(), "err", err)
		return
	}
	if stale != o.Status().InstructionsStale {
		o.update(func(s *Status) { s.InstructionsStale = stale })
	}
	switch {
	case !stale || !restart:
		return
	case o.opts.AutoInstructionRestart != nil && !o.opts.AutoInstructionRestart():
		return // the Setting is off: shown, and Restart takes the new instructions
	case o.opts.Paused != nil && o.opts.Paused():
		return
	case !lastRestart.IsZero() && now.Sub(lastRestart) < instructionRestartGap:
		return
	case !agent.AgentStatus.Ready():
		return // mid-turn: at the next idle moment
	case o.opts.HeldJobBusy != nil && o.opts.HeldJobBusy(ctx):
		return
	}
	screen, err := readScreen(ctx, o.opts.Client, agent.PaneID)
	if err != nil || screen.dialog.Present || screen.draft.Present {
		return // a dialog or a draft in the input box: not now
	}
	o.mu.Lock()
	o.instructionsRestartAt = now
	o.mu.Unlock()
	o.opts.Logger.Info("restarting for new instructions", "agent", o.agentName())
	o.update(func(s *Status) { s.State, s.Detail = StateRestarting, "" })
	var status Status
	if err = o.stop(ctx, agent); err == nil {
		status, err = o.ensureLocked(ctx, true)
	} else {
		status, _ = o.fail(err)
	}
	summary := "Restarted " + o.agentName() + " for new instructions (same conversation): its prompt file, hooks or settings changed since it started"
	o.journalRestart(ctx, summary, true, status, err)
}

// noteConversationInstructions runs at every start, after the files are written. Claude Code keeps
// a conversation's original appended system prompt when it is resumed (seen on 2.1.x: an agent
// resumed with a new instructions file still followed the old one), so a restart alone does not
// give a resumed agent its new instructions. A fresh conversation records the fingerprint it began
// with; a resumed one whose instructions changed since is marked, and its SessionStart context
// then carries the current instructions (InstructionsNotice).
func (o *Orchestrator) noteConversationInstructions(ctx context.Context, resuming bool) {
	current, err := o.opts.KV.GetKV(ctx, o.kvKey(kvStartFingerprint))
	if err != nil || current == "" {
		return
	}
	if !resuming {
		_ = o.opts.KV.SetKV(ctx, o.kvKey(kvConversationFingerprint), current)
		_ = o.opts.KV.DeleteKV(ctx, o.kvKey(kvInstructionsNotice))
		return
	}
	began, _ := o.opts.KV.GetKV(ctx, o.kvKey(kvConversationFingerprint))
	if began == current {
		return
	}
	_ = o.opts.KV.SetKV(ctx, o.kvKey(kvInstructionsNotice), "1")
	// From now on the conversation has them (once the notice is read).
	_ = o.opts.KV.SetKV(ctx, o.kvKey(kvConversationFingerprint), current)
}

// InstructionsNotice returns, once, the agent's current instructions for its SessionStart context
// when it resumed a conversation that began with other ones ("" otherwise).
func (o *Orchestrator) InstructionsNotice(ctx context.Context) string {
	if pending, _ := o.opts.KV.GetKV(ctx, o.kvKey(kvInstructionsNotice)); pending == "" {
		return ""
	}
	body, err := os.ReadFile(filepath.Join(o.opts.StateDir, InstructionsFile)) //nolint:gosec // crowsnest's own state file
	if err != nil || strings.TrimSpace(string(body)) == "" {
		return ""
	}
	_ = o.opts.KV.DeleteKV(ctx, o.kvKey(kvInstructionsNotice))
	return "# Crowsnest: your instructions changed\n\n" +
		"Crowsnest's instructions for you changed since this conversation began. The text below is the current version and replaces " +
		"the crowsnest instructions in your system prompt: where the two differ, follow this one.\n\n" + string(body)
}
