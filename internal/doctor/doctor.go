// Package doctor answers "can crowsnest run here, and if not, what do I do?" (DESIGN.md 3.4). It
// is one list of fast, read-only checks: herdr, the agents (Claude Code, Codex) and what
// crowsnest needs from them, the machine, optional features and the effective configuration.
// Each check has a status and, when it is not fine, one line saying how to fix it.
//
// The same Report backs three things: `crowsnest doctor` (text or --json), the System page in
// the app, and the guided first run's checklist. So the checks never write anything: no
// directory is created, no herdr server started, the database opened read-only, and no model
// is called unless Deep is set (which costs a few tokens and says so).
//
// Everything the checks touch goes through Env, so every check is tested with fakes.
package doctor

import (
	"context"
	"slices"
	"time"

	"github.com/jclement/crowsnest/internal/config"
)

// Status is one check's outcome.
type Status string

const (
	StatusOK   Status = "ok"   // fine
	StatusInfo Status = "info" // worth knowing, nothing to do
	StatusWarn Status = "warn" // works, but something is off or missing
	StatusFail Status = "fail" // crowsnest cannot do its job until this is fixed
	StatusSkip Status = "skip" // not checked (not applicable, or needs --deep)
)

// Groups, in report order.
const (
	GroupHerdr    = "herdr"
	GroupAgents   = "agents"
	GroupClaude   = "claude"
	GroupMachine  = "machine"
	GroupOptional = "optional"
	GroupConfig   = "config"
	GroupLive     = "live"
)

// Check is one line of the report.
type Check struct {
	ID     string `json:"id"`    // stable, e.g. "herdr.version"; the UI and tests key on it
	Group  string `json:"group"` // one of the Group constants
	Title  string `json:"title"` // short: "herdr installed"
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"` // what was found: "0.9.5 at /opt/homebrew/bin/herdr"
	Fix    string `json:"fix,omitempty"`    // what to do, when Status is warn or fail
	// Command is a shell command that fixes it, when there is one (the checklist offers to run
	// it in a terminal: `claude` to sign in, the herdr install line).
	Command string `json:"command,omitempty"`
	// Cost is set on a check that spent something (a --deep model call).
	Cost string `json:"cost,omitempty"`
}

// Agent is one coding agent crowsnest can drive.
type Agent struct {
	Name      string `json:"name"`              // "claude" or "codex"
	Installed bool   `json:"installed"`         // on PATH
	Path      string `json:"path,omitempty"`    // where
	Version   string `json:"version,omitempty"` // as the binary reports it, e.g. "2.1.332"
	// SignedIn is whether it is logged in: nil when it could not be told.
	SignedIn *bool `json:"signed_in,omitempty"`
	// AuthMethod is how it is signed in ("claude.ai", "api key", "ChatGPT"); never an account
	// name or email.
	AuthMethod string `json:"auth_method,omitempty"`
	Usable     bool   `json:"usable"` // installed and not known to be signed out
}

// Selection is what crowsnest would run, given the agents and --engine.
type Selection struct {
	Setting      string `json:"setting"`      // --engine: auto, claude or codex
	Orchestrator string `json:"orchestrator"` // claude, codex or "" (none usable)
	Jobs         string `json:"jobs"`         // the default engine of a job that names none
	Reason       string `json:"reason"`       // a sentence: why this choice
}

// Versions are the versions the System page shows at the top.
type Versions struct {
	Crowsnest string `json:"crowsnest"`
	Herdr     string `json:"herdr,omitempty"`
	Claude    string `json:"claude,omitempty"`
	Codex     string `json:"codex,omitempty"`
}

// SettingSource is where one effective setting came from.
type SettingSource struct {
	Name   string `json:"name"`   // option name
	Value  string `json:"value"`  // redacted for secrets
	Source string `json:"source"` // flag, env, database, file, default
	Where  string `json:"where"`  // "--port", "CROWSNEST_PORT", "config.yaml", …
}

// Report is the whole result.
type Report struct {
	CheckedAt time.Time       `json:"checked_at"`
	Verdict   Status          `json:"verdict"` // the worst status of any check (skip and info count as ok)
	Summary   string          `json:"summary"` // one line: "herdr 0.9.5 ✓  claude 2.1.332 ✓ signed in  codex not found"
	Versions  Versions        `json:"versions"`
	Agents    []Agent         `json:"agents"`
	Selection Selection       `json:"selection"`
	Paths     Paths           `json:"paths"`
	Settings  []SettingSource `json:"settings,omitempty"`
	Checks    []Check         `json:"checks"`
	Deep      bool            `json:"deep"`
	Live      *Live           `json:"live,omitempty"` // a running instance's own view, when one answered
}

// Paths are the directories and files crowsnest uses, for the System page.
type Paths struct {
	Data         string `json:"data"`
	State        string `json:"state"`
	Config       string `json:"config"`
	Database     string `json:"database"`
	Orchestrator string `json:"orchestrator"`
	AgentSocket  string `json:"agent_socket"`
	Herdr        string `json:"herdr,omitempty"`
	Claude       string `json:"claude,omitempty"`
	Codex        string `json:"codex,omitempty"`
}

// Live is what a running crowsnest says about itself (the agent socket's /doctor, or the
// server's own state on the System page).
type Live struct {
	Running        bool              `json:"running"`
	SetupRequired  bool              `json:"setup_required,omitempty"`
	Version        string            `json:"version,omitempty"`
	Uptime         string            `json:"uptime,omitempty"`
	StartedAt      time.Time         `json:"started_at,omitzero"`
	Orchestrator   string            `json:"orchestrator,omitempty"` // its state: idle, working, waiting, …
	OrchestratorOK bool              `json:"orchestrator_ok,omitempty"`
	Detail         string            `json:"detail,omitempty"`
	HerdrConnected bool              `json:"herdr_connected,omitempty"`
	Hooks          map[string]string `json:"hooks,omitempty"` // hook event → when it last fired (RFC 3339)
	LastErrors     []LogLine         `json:"last_errors,omitempty"`
	UpdateLatest   string            `json:"update_latest,omitempty"`
}

// LogLine is one recent warning or error from the server log, already redacted.
type LogLine struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

// Inputs are what Run checks.
type Inputs struct {
	Config      config.Config // the resolved configuration (zero when LoadErr is set)
	LoadErr     error         // the config did not load: reported, and checks that need it skip
	Version     string        // crowsnest's own version
	AgentSocket string        // the agent socket path serve would use
	Deep        bool          // also make one tiny real call per agent (and ElevenLabs)
	// Running is true when the caller is the server itself (the System page): the port is ours
	// and the database is open, so those checks say so instead of probing.
	Running bool
	Env     Env
}

// Run performs every check and assembles the report.
func Run(ctx context.Context, in Inputs) Report {
	in.Env = in.Env.withDefaults()
	r := &runner{in: in, env: in.Env, report: Report{CheckedAt: in.Env.Now(), Deep: in.Deep, Versions: Versions{Crowsnest: in.Version}}}
	r.configChecks()
	r.herdrChecks(ctx)
	r.agentChecks(ctx)
	r.claudeChecks(ctx)
	r.machineChecks(ctx)
	r.optionalChecks(ctx)
	r.report.Verdict = Verdict(r.report.Checks)
	r.report.Summary = Summary(r.report)
	return r.report
}

// runner carries one Run's state.
type runner struct {
	in     Inputs
	env    Env
	report Report
	herdr  herdrFacts
}

func (r *runner) add(check Check) { r.report.Checks = append(r.report.Checks, check) }

// configOK reports whether the config loaded, so config-dependent checks can run.
func (r *runner) configOK() bool { return r.in.LoadErr == nil }

// Verdict is the worst status among checks: fail, then warn, else ok.
func Verdict(checks []Check) Status {
	switch {
	case slices.ContainsFunc(checks, func(c Check) bool { return c.Status == StatusFail }):
		return StatusFail
	case slices.ContainsFunc(checks, func(c Check) bool { return c.Status == StatusWarn }):
		return StatusWarn
	}
	return StatusOK
}

// Find returns the check with id, and whether there is one.
func (r Report) Find(id string) (Check, bool) {
	index := slices.IndexFunc(r.Checks, func(c Check) bool { return c.ID == id })
	if index < 0 {
		return Check{}, false
	}
	return r.Checks[index], true
}

// Agent returns the named agent's facts.
func (r Report) Agent(name string) Agent {
	index := slices.IndexFunc(r.Agents, func(a Agent) bool { return a.Name == name })
	if index < 0 {
		return Agent{Name: name}
	}
	return r.Agents[index]
}
