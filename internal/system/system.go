// Package system is the server's view of itself (DESIGN.md 3.4 and 12.x): the doctor report, run
// as the running server (cached, re-checked on demand), its live state (uptime, hooks seen
// firing, recent errors), the settings changed in the app, and the guided first run's state.
//
// It also owns the orchestrator's start gate: the orchestrator waits while the first run is not
// finished, herdr is not connected, or no agent is installed, and starts by itself when that
// changes, so crowsnest comes up on an unprepared machine and gets going without a restart.
package system

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/doctor"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/orch"
)

// reportMaxAge is how long a doctor report is served from cache. The checks spawn a handful of
// short commands (`claude --version`, `--help`, `auth status`), so the System page's polling and
// the gate share one result rather than running them every few seconds.
const reportMaxAge = 30 * time.Second

// agentsMaxAge is how long the gate trusts its agent detection while waiting.
const agentsMaxAge = 10 * time.Second

// KV is the database the service keeps settings and the first-run state in.
type KV interface {
	GetKV(ctx context.Context, key string) (string, error)
	SetKV(ctx context.Context, key, value string) error
	DeleteKV(ctx context.Context, key string) error
	KVPrefix(ctx context.Context, prefix string) (map[string]string, error)
}

// Orchestrator is what the service needs of internal/orch.
type Orchestrator interface {
	Status() orch.Status
	Kick()
}

// Options configure a Service.
type Options struct {
	Config      config.Config
	Version     string
	KV          KV
	Journal     *journal.Journal // nil: changes are not journalled (tests)
	Hub         *hub.Hub         // nil: no events
	Logs        *LogRing         // nil: no recent errors
	Herdr       *herdr.Lazy      // nil: herdr counts as connected (tests, demo)
	AgentSocket string
	// Reload loads the config again with the given saved settings: config.Load with the
	// process's own flags and environment. It validates a change before it is saved.
	Reload func(stored map[string]string) (config.Config, error)
	// Apply is told the new config after a change is saved, to apply what can be applied live.
	Apply  func(previous, next config.Config)
	Env    doctor.Env // the doctor's view of the machine; the demo's fake states replace it
	// OnStarted is called when the first-run checklist's Start is pressed (serve greets the
	// person from the orchestrator once it is up). Nil: nothing.
	OnStarted func()
	Logger *slog.Logger
	Now    func() time.Time
}

// Service is the server's view of itself.
type Service struct {
	opts    Options
	started time.Time

	mu           sync.Mutex
	cfg          config.Config
	orchestrator Orchestrator
	hooks        map[string]time.Time
	report       *doctor.Report
	agents       []doctor.Agent
	agentsAt     time.Time
	checking     sync.Mutex // one doctor run at a time
}

// New returns a Service.
func New(opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{opts: opts, started: opts.Now(), cfg: opts.Config, hooks: map[string]time.Time{}}
}

// SetOrchestrator connects the orchestrator (built after the service, since its gate is ours).
func (s *Service) SetOrchestrator(orchestrator Orchestrator) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orchestrator = orchestrator
}

// SetEnv replaces the doctor's view of the machine (the demo's injectable states) and drops the
// cached report.
func (s *Service) SetEnv(env doctor.Env) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opts.Env = env
	s.report, s.agents = nil, nil
}

// Config is the configuration in effect, with settings changed in the app applied.
func (s *Service) Config() config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// RecordHook notes that a hook event reached the agent socket.
func (s *Service) RecordHook(event string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks[event] = s.opts.Now()
}

// Report returns the doctor report, from cache unless it is old or recheck is set.
func (s *Service) Report(ctx context.Context, recheck bool) doctor.Report {
	s.mu.Lock()
	cached := s.report
	s.mu.Unlock()
	if cached != nil && !recheck && s.opts.Now().Sub(cached.CheckedAt) < reportMaxAge {
		return s.withLive(ctx, *cached)
	}
	s.checking.Lock()
	defer s.checking.Unlock()
	s.mu.Lock()
	env, cfg := s.opts.Env, s.cfg
	s.mu.Unlock()
	report := doctor.Run(ctx, doctor.Inputs{Config: cfg, Version: s.opts.Version, AgentSocket: s.opts.AgentSocket, Running: true, Env: env})
	s.mu.Lock()
	s.report, s.agents, s.agentsAt = &report, report.Agents, s.opts.Now()
	s.mu.Unlock()
	if s.opts.Hub != nil {
		s.opts.Hub.Publish(hub.Event{Type: "system", Data: map[string]any{"verdict": report.Verdict, "summary": report.Summary}})
	}
	return s.withLive(ctx, report)
}

// withLive adds the running instance's own view.
func (s *Service) withLive(ctx context.Context, report doctor.Report) doctor.Report {
	live := s.Live(ctx)
	report.Live = &live
	return report
}

// Live is the running instance's own view: uptime, the orchestrator, herdr, hooks seen firing,
// recent errors.
func (s *Service) Live(_ context.Context) doctor.Live {
	s.mu.Lock()
	orchestrator := s.orchestrator
	hooks := make(map[string]string, len(s.hooks))
	for event, at := range s.hooks {
		hooks[event] = at.UTC().Format(time.RFC3339)
	}
	s.mu.Unlock()
	now := s.opts.Now()
	live := doctor.Live{Running: true, Version: s.opts.Version, StartedAt: s.started, Uptime: humanDuration(now.Sub(s.started)), Hooks: hooks, HerdrConnected: s.herdrConnected()}
	if orchestrator != nil {
		status := orchestrator.Status()
		live.Orchestrator, live.Detail = string(status.State), status.Detail
		live.OrchestratorOK = status.State == orch.StateIdle || status.State == orch.StateWorking
	}
	if s.opts.Logs != nil {
		live.LastErrors = s.opts.Logs.Lines()
	}
	return live
}

// herdrConnected reports whether herdr is reachable. Without a Lazy client (the demo's fake
// herdr, tests) it asks the doctor's view of the machine whether herdr is installed, which is how
// the demo's "herdr missing" state holds the orchestrator.
func (s *Service) herdrConnected() bool {
	if s.opts.Herdr != nil {
		return s.opts.Herdr.Connected()
	}
	s.mu.Lock()
	lookPath := s.opts.Env.LookPath
	s.mu.Unlock()
	if lookPath == nil {
		return true
	}
	_, err := lookPath("herdr")
	return err == nil
}

// Agents returns the agents found, re-detected when the cached detection is older than maxAge.
func (s *Service) Agents(ctx context.Context, maxAge time.Duration) []doctor.Agent {
	s.mu.Lock()
	agents, at, env := s.agents, s.agentsAt, s.opts.Env
	s.mu.Unlock()
	if agents != nil && s.opts.Now().Sub(at) < maxAge {
		return agents
	}
	agents = doctor.DetectAgents(ctx, env)
	s.mu.Lock()
	s.agents, s.agentsAt = agents, s.opts.Now()
	s.mu.Unlock()
	return agents
}

// Selection is what the orchestrator and jobs run on now.
func (s *Service) Selection(ctx context.Context) doctor.Selection {
	return doctor.Select(s.Config().Engine, s.Agents(ctx, agentsMaxAge))
}

// Gate is the orchestrator's start gate: "" when it may start, else the reason it waits.
func (s *Service) Gate(ctx context.Context) string {
	if s.OnboardingPending(ctx) {
		return "Waiting for the first-run checklist: press Start there to start the orchestrator."
	}
	if !s.herdrConnected() {
		reason := "herdr is not running yet."
		if s.opts.Herdr == nil {
			reason = "herdr is not installed."
		} else if last := s.opts.Herdr.LastError(); last != "" {
			reason = "herdr is not available: " + last
		}
		return reason + " The orchestrator starts as soon as it is."
	}
	if s.Selection(ctx).Orchestrator == "" {
		return "No agent is installed: install Claude Code (or Codex). The orchestrator starts as soon as one is found."
	}
	return ""
}

// LaunchSettings is the orchestrator's launch settings from the config in effect (orch.Options.Launch).
func (s *Service) LaunchSettings(remoteControlName func(config.Config) string) func() orch.LaunchSettings {
	return func() orch.LaunchSettings {
		cfg := s.Config()
		engine := s.Selection(context.Background()).Orchestrator
		if engine == "" {
			engine = orch.EngineClaude
		}
		return orch.LaunchSettings{Engine: engine, Model: cfg.Model, PermissionMode: cfg.PermissionMode, RemoteControlName: remoteControlName(cfg)}
	}
}

func (s *Service) kickOrchestrator() {
	s.mu.Lock()
	orchestrator := s.orchestrator
	s.mu.Unlock()
	if orchestrator != nil {
		orchestrator.Kick()
	}
}

func (s *Service) journal(ctx context.Context, summary string, detail map[string]any) {
	if s.opts.Journal != nil {
		s.opts.Journal.Add(ctx, journal.KindSettings, summary, detail)
	}
}

// humanDuration renders an uptime: 3d 4h, 2h 5m, 4m 10s.
func humanDuration(d time.Duration) string {
	d = d.Round(time.Second)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60
	switch {
	case days > 0:
		return itoa(days) + "d " + itoa(hours) + "h"
	case hours > 0:
		return itoa(hours) + "h " + itoa(minutes) + "m"
	case minutes > 0:
		return itoa(minutes) + "m " + itoa(seconds) + "s"
	}
	return itoa(seconds) + "s"
}
