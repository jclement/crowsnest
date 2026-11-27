// Package engines is the one statement of what crowsnest gives a job on each engine (DESIGN.md
// 20.1): which of its guarantees hold, which rescue actions work, and which models it offers.
// GET /api/v1/engines serves it, the job form and project defaults read it, the API refuses a
// rescue action an engine does not support with its sentence, and the manual's reference page
// (docs/manual/reference/engines.md) is generated from it, so the four cannot disagree.
//
// Every Codex line was checked against codex-cli 0.167.0 on 2026-11-27 (DESIGN.md 20.1 says how);
// a capability is "available" only when a test exercises it (`mise run test:codex` for Codex).
package engines

import (
	"context"
	"encoding/json"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// Engine ids.
const (
	Claude = "claude"
	Codex  = "codex"
)

// Capability ids, in the order the table shows them.
const (
	CapStop         = "stop"         // the stop guarantee: a turn may not end undeclared
	CapApprovals    = "approvals"    // structured tool approvals, answered from the browser
	CapGuard        = "guard"        // the guard: protected branches and hosts, holds while checking
	CapSpend        = "spend"        // spend measured per job
	CapReceipts     = "receipts"     // answer receipts: an answer seen arriving in the conversation
	CapUsageLimit   = "usage_limit"  // the plan's usage limit recognised, the fleet held, resumed at reset
	CapSpinning     = "spinning"     // spinning detection: the same failing step over and over
	CapInstrRestart = "instructions" // instruction layers: changed-since-start, and restart to apply
	CapTrustSkip    = "folder_trust" // a project folder trusted at creation starts without the prompt
)

// Rescue action ids (POST /jobs/{id}/rescue), as internal/jobs names them.
const (
	RescueInterrupt = "interrupt"
	RescueContinue  = "continue"
	RescueNudge     = "nudge"
	RescueUnstick   = "unstick"
	RescueRestart   = "restart"
	RescueCompact   = "compact"
	RescueModel     = "model"
)

// Capability is one guarantee as it holds on one engine.
type Capability struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
	// Partial marks a capability that holds with a stated gap (Note says which).
	Partial bool   `json:"partial,omitempty"`
	Note    string `json:"note"`
}

// Model is one model an engine offers.
type Model struct {
	ID    string `json:"id"`    // what --model takes
	Label string `json:"label"` // how the form shows it
}

// Rescue is one rescue action and whether this engine supports it.
type Rescue struct {
	ID        string `json:"id"`
	Available bool   `json:"available"`
	// Refusal is the sentence the API answers when the action is asked of this engine.
	Refusal string `json:"refusal,omitempty"`
}

// Engine is what one engine gives a job.
type Engine struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	// Summary is one sentence for the job form and the project's default engine.
	Summary      string       `json:"summary"`
	Capabilities []Capability `json:"capabilities"`
	Rescue       []Rescue     `json:"rescue"`
	Models       []Model      `json:"models"`
	// Without is the labels of the guarantees it runs without, for a job's card and detail.
	Without []string `json:"without"`
}

// label of each capability, shared by both engines.
var labels = map[string]string{
	CapStop:         "Stop guarantee",
	CapApprovals:    "Approvals from the browser",
	CapGuard:        "Guard (protected branches and hosts)",
	CapSpend:        "Spend measured",
	CapReceipts:     "Answer receipts",
	CapUsageLimit:   "Usage-limit detection",
	CapSpinning:     "Spinning detection",
	CapInstrRestart: "Instruction changes: restart to apply",
	CapTrustSkip:    "No folder-trust prompt in a trusted project",
}

var order = []string{CapStop, CapApprovals, CapGuard, CapSpend, CapReceipts, CapUsageLimit, CapSpinning, CapInstrRestart, CapTrustSkip}

func capability(id string, available bool, note string) Capability {
	return Capability{ID: id, Label: labels[id], Available: available, Note: note}
}

// claudeModels are Claude Code's own aliases, which follow the newest model of each family.
var claudeModels = []Model{{ID: "sonnet", Label: "Sonnet"}, {ID: "opus", Label: "Opus"}, {ID: "haiku", Label: "Haiku"}}

// ClaudeEngine is what a Claude job gets.
func ClaudeEngine(installed bool) Engine {
	return finish(Engine{
		ID: Claude, Name: "Claude Code", Installed: installed,
		Summary: "Every guarantee crowsnest has: the stop guarantee, approvals from the browser, spend, receipts, usage-limit and spinning detection, and every rescue action.",
		Capabilities: []Capability{
			capability(CapStop, true, "the Stop hook refuses a turn that ends without done, fail, blocked or a question"),
			capability(CapApprovals, true, "the PermissionRequest hook brings each tool approval to the browser"),
			capability(CapGuard, true, "the PreToolUse hook checks shell commands and holds writes while crowsnest checks the worktree"),
			capability(CapSpend, true, "tokens and an estimated cost, from Claude's transcript"),
			capability(CapReceipts, true, "an answer is marked received when it shows up in the conversation"),
			capability(CapUsageLimit, true, "the StopFailure hook reports the limit; the fleet holds and resumes at the reset"),
			capability(CapSpinning, true, "read from the transcript: the same failing step, over and over"),
			capability(CapInstrRestart, true, "changed-since-start is shown, and Restart applies the new layers in the same conversation"),
			capability(CapTrustSkip, true, "a linked worktree inherits its checkout's trust (Claude Code 2.1.332)"),
		},
		Rescue: rescues(nil),
		Models: claudeModels,
	})
}

// codexRefusals are the rescue actions a Codex job cannot take, with the API's sentence.
var codexRefusals = map[string]string{
	RescueRestart: "A Codex job cannot be restarted from crowsnest: Codex has no resume crowsnest drives. Interrupt it and send a message, or cancel it and give the job again.",
	RescueCompact: "Compact is a Claude Code command; a Codex job has no compact crowsnest can send.",
	RescueModel:   "Switching model is offered for Claude jobs only; give the job again with the model you want.",
}

// CodexEngine is what a Codex job gets.
func CodexEngine(installed bool, models []Model) Engine {
	return finish(Engine{
		ID: Codex, Name: "Codex", Installed: installed,
		Summary: "The stop guarantee, approvals from the browser and the guard (through Codex's hooks), and spend in tokens; no answer receipts, usage-limit or spinning detection, and no restart, compact or model switch in Rescue.",
		Capabilities: []Capability{
			capability(CapStop, true, "Codex's Stop hook, handed to the job for its session (--dangerously-bypass-hook-trust, the hooks crowsnest wrote)"),
			capability(CapApprovals, true, "Codex asks on request inside its workspace-write sandbox (writes outside the workspace; the network is on, as for a Claude job, so the shell reaches crowsnest); its PermissionRequest hook brings each request to the browser, and an unanswered one falls back to Codex's own dialog, shown as a card. Codex keeps .git read-only in its sandbox, so every commit asks: a project approval rule for git commit answers those"),
			{ID: CapGuard, Label: labels[CapGuard], Available: true, Partial: true, Note: "shell commands are checked by the PreToolUse hook; apply_patch edits are held while crowsnest checks the worktree but not otherwise inspected"},
			{ID: CapSpend, Label: labels[CapSpend], Available: true, Partial: true, Note: "tokens from Codex's session log; no cost estimate (crowsnest has no prices for OpenAI's models)"},
			capability(CapReceipts, false, "receipts read Claude's transcript; a Codex answer stops at \"delivered\""),
			capability(CapUsageLimit, false, "Codex's \"You've hit your usage limit\" is not recognised: a job that hits it sits idle until the backstop nudges it"),
			capability(CapSpinning, false, "spinning is read from Claude's transcript"),
			{ID: CapInstrRestart, Label: labels[CapInstrRestart], Available: false, Partial: true, Note: "changed-since-start is shown, but a Codex job cannot be restarted from crowsnest to apply it"},
			capability(CapTrustSkip, true, "Codex resolves a worktree to its repository root's trust (codex-cli 0.167)"),
		},
		Rescue: rescues(codexRefusals),
		Models: models,
	})
}

func rescues(refused map[string]string) []Rescue {
	ids := []string{RescueInterrupt, RescueContinue, RescueNudge, RescueUnstick, RescueRestart, RescueCompact, RescueModel}
	out := make([]Rescue, 0, len(ids))
	for _, id := range ids {
		refusal, no := refused[id]
		out = append(out, Rescue{ID: id, Available: !no, Refusal: refusal})
	}
	return out
}

// finish orders the capabilities and lists what the engine runs without.
func finish(engine Engine) Engine {
	index := map[string]int{}
	for i, id := range order {
		index[id] = i
	}
	sort.SliceStable(engine.Capabilities, func(i, j int) bool { return index[engine.Capabilities[i].ID] < index[engine.Capabilities[j].ID] })
	engine.Without = []string{}
	for _, c := range engine.Capabilities {
		if !c.Available {
			engine.Without = append(engine.Without, c.Label)
		}
	}
	if engine.Models == nil {
		engine.Models = []Model{}
	}
	return engine
}

// RescueRefusal is the sentence refusing action on engine, or "" when the engine supports it.
func RescueRefusal(engine, action string) string {
	if engine == Codex {
		return codexRefusals[action]
	}
	return ""
}

// ---- The live table ---------------------------------------------------------------------------

// Catalog answers the table for this machine: which engines are installed and Codex's models
// (`codex debug models`, cached).
type Catalog struct {
	// LookPath finds a binary (exec.LookPath); Run runs one and returns its output.
	LookPath func(string) (string, error)
	Run      func(ctx context.Context, name string, args ...string) ([]byte, error)

	mu       sync.Mutex
	models   []Model
	loadedAt time.Time
}

// modelsTTL is how long Codex's model list is kept: it changes with a Codex update, not hourly.
const modelsTTL = 30 * time.Minute

// NewCatalog returns a Catalog over the real PATH.
func NewCatalog() *Catalog {
	return &Catalog{LookPath: exec.LookPath, Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).Output() //nolint:gosec // fixed binary and arguments
	}}
}

// Engines is the table: Claude, then Codex.
func (c *Catalog) Engines(ctx context.Context) []Engine {
	_, claudeErr := c.LookPath("claude")
	_, codexErr := c.LookPath("codex")
	var models []Model
	if codexErr == nil {
		models = c.codexModels(ctx)
	}
	return []Engine{ClaudeEngine(claudeErr == nil), CodexEngine(codexErr == nil, models)}
}

// codexModels lists the models Codex shows in its own picker (visibility "list").
func (c *Catalog) codexModels(ctx context.Context) []Model {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.models != nil && time.Since(c.loadedAt) < modelsTTL {
		return c.models
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := c.Run(ctx, "codex", "debug", "models")
	if err != nil {
		return c.models // the last good list, or none
	}
	models := ParseCodexModels(out)
	c.models, c.loadedAt = models, time.Now()
	return models
}

// ParseCodexModels reads `codex debug models` (codex-cli 0.167: {"models": [{slug, display_name,
// visibility, priority}, …]}): the listed ones, by priority.
func ParseCodexModels(out []byte) []Model {
	var catalog struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Visibility  string `json:"visibility"`
			Priority    int    `json:"priority"`
		} `json:"models"`
	}
	if json.Unmarshal(out, &catalog) != nil {
		return []Model{}
	}
	sort.SliceStable(catalog.Models, func(i, j int) bool { return catalog.Models[i].Priority < catalog.Models[j].Priority })
	models := []Model{}
	for _, m := range catalog.Models {
		if m.Visibility != "list" || m.Slug == "" {
			continue
		}
		label := strings.TrimSpace(m.DisplayName)
		if label == "" {
			label = m.Slug
		}
		models = append(models, Model{ID: m.Slug, Label: label})
	}
	return models
}
