// The guided first run (DESIGN.md 4.14.1): after the setup page's password step, a short
// checklist driven by the doctor's checks, then Start. Its state is one kv row: setup marks it
// pending when it stores the owner, Start marks it done. An instance set up before the checklist
// existed, or started with a login from the environment, has no row and is never asked.

package system

import (
	"context"
	"errors"
	"fmt"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/doctor"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
)

// OnboardingKey is the kv row holding the first run's state.
const OnboardingKey = "onboarding.state"

// Onboarding states.
const (
	OnboardingPending = "pending"
	OnboardingDone    = "done"
)

// ErrNoAgent is Start with no agent to run.
var ErrNoAgent = errors.New("no agent is installed")

// MarkOnboardingPending records that the checklist should be shown (first-run setup calls it).
func MarkOnboardingPending(ctx context.Context, kv interface {
	SetKV(ctx context.Context, key, value string) error
}) error {
	return kv.SetKV(ctx, OnboardingKey, OnboardingPending)
}

// OnboardingPending reports whether the checklist is still to be finished.
func (s *Service) OnboardingPending(ctx context.Context) bool {
	value, err := s.opts.KV.GetKV(ctx, OnboardingKey)
	return err == nil && value == OnboardingPending
}

// OnboardingView is GET /onboarding.
type OnboardingView struct {
	Pending bool          `json:"pending"`
	Report  doctor.Report `json:"report"`
	// ChooseEngine is true when both Claude Code and Codex are usable: the checklist asks which
	// runs the orchestrator.
	ChooseEngine bool        `json:"choose_engine"`
	Engine       string      `json:"engine"` // the setting: auto, claude or codex
	Dir          string      `json:"dir"`    // the orchestrator's working directory
	Orchestrator orch.Status `json:"orchestrator"`
	// CodexGaps lists what Codex mode lacks (sent whenever Codex is installed); the checklist shows
	// them when Codex runs the orchestrator or is chosen to.
	CodexGaps []string `json:"codex_gaps,omitempty"`
	Bind      string   `json:"bind"` // the bind, for the optional tailnet step
	Tailnet   string   `json:"tailnet,omitempty"`
	Name      string   `json:"name"`
}

// CodexGaps is what the orchestrator lacks when Codex runs it.
var CodexGaps = []string{
	"No stop-hook guarantee: a turn that ends without declaring done is caught only by the herdr-side backstop (after a minute idle).",
	"No structured approvals: Codex runs with --ask-for-approval never inside its workspace-write sandbox, its network off, so nothing reaches the Inbox as an approval card.",
	"No SAY lines or spoken replies: talk mode reads Claude's transcript.",
	"The Chat view shows the terminal's text, not a formatted conversation, and no spend is metered.",
	"No resume: a restart begins a new Codex session.",
}

// Onboarding returns the checklist's state, re-checking the machine when recheck is set.
func (s *Service) Onboarding(ctx context.Context, recheck bool) OnboardingView {
	report := s.Report(ctx, recheck)
	cfg := s.Config()
	claude, codex := report.Agent(config.EngineClaude), report.Agent(config.EngineCodex)
	view := OnboardingView{
		Pending: s.OnboardingPending(ctx), Report: report, Engine: cfg.Engine, Dir: cfg.Dir, Name: cfg.Name,
		ChooseEngine: claude.Usable && codex.Usable,
	}
	if check, ok := report.Find("machine.bind"); ok {
		view.Bind = check.Detail
	}
	s.mu.Lock()
	orchestrator := s.orchestrator
	s.mu.Unlock()
	if orchestrator != nil {
		view.Orchestrator = orchestrator.Status()
	}
	if codex.Installed {
		view.CodexGaps = CodexGaps // the checklist shows them when Codex runs the orchestrator, or is chosen to
	}
	return view
}

// StartOnboarding finishes the checklist: saves the engine choice (when one was asked for),
// marks the first run done, journals it and lets the orchestrator start.
func (s *Service) StartOnboarding(ctx context.Context, engine string) error {
	if engine != "" && engine != s.Config().Engine {
		if _, err := s.UpdateSetting(ctx, "engine", engine, false); err != nil && !errors.Is(err, ErrNotEditable) {
			return err
		}
	}
	selection := doctor.Select(s.Config().Engine, s.Agents(ctx, 0))
	if selection.Orchestrator == "" {
		return fmt.Errorf("%w: install Claude Code or Codex first, then re-check", ErrNoAgent)
	}
	if err := s.opts.KV.SetKV(ctx, OnboardingKey, OnboardingDone); err != nil {
		return err
	}
	s.journal(ctx, "First run finished: the orchestrator starts on "+selection.Orchestrator, map[string]any{"engine": selection.Orchestrator})
	s.kickOrchestrator()
	if s.opts.OnStarted != nil {
		s.opts.OnStarted()
	}
	return nil
}

// onboardingState reads the raw row, for tests.
func (s *Service) onboardingState(ctx context.Context) string {
	value, err := s.opts.KV.GetKV(ctx, OnboardingKey)
	if errors.Is(err, store.ErrNotFound) {
		return ""
	}
	return value
}
