// The agents: which of Claude Code and Codex are installed, their versions, whether each is
// signed in (from the CLI's own status command, never a model call), and which one crowsnest
// would run. With Deep, one tiny real round trip each.

package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/config"
)

// deepTimeout bounds one --deep round trip: a cold Claude start plus one short haiku turn.
const deepTimeout = 120 * time.Second

// DeepCost is the label on a --deep check.
const DeepCost = "a few tokens (one short haiku or default-model turn)"

// DetectAgents finds Claude Code and Codex, their versions and sign-in state. The server uses it
// to resolve --engine auto and for the first-run checklist.
func DetectAgents(ctx context.Context, env Env) []Agent {
	env = env.withDefaults()
	return []Agent{detectClaude(ctx, env), detectCodex(ctx, env)}
}

func detectClaude(ctx context.Context, env Env) Agent {
	agent := Agent{Name: config.EngineClaude}
	path, err := env.LookPath("claude")
	if err != nil {
		return agent
	}
	agent.Installed, agent.Path = true, path
	if output, err := env.Run(ctx, commandTimeout, path, "--version"); err == nil {
		agent.Version = versionPattern.FindString(output)
	}
	agent.SignedIn, agent.AuthMethod = claudeAuth(ctx, env, path)
	agent.Usable = agent.SignedIn == nil || *agent.SignedIn
	return agent
}

// claudeAuthStatus is the part of `claude auth status --json` crowsnest reads. The output also
// carries the account's email and organisation; those are never kept or shown.
type claudeAuthStatus struct {
	LoggedIn   bool   `json:"loggedIn"`
	AuthMethod string `json:"authMethod"`
}

// claudeAuth asks `claude auth status --json` (Claude Code 2.1.x): local, free, no model call.
// When that is unavailable, an ANTHROPIC_API_KEY in the environment counts as signed in; else
// the state is unknown.
func claudeAuth(ctx context.Context, env Env, path string) (*bool, string) {
	output, _ := env.Run(ctx, commandTimeout, path, "auth", "status", "--json")
	start := strings.Index(output, "{")
	var status claudeAuthStatus
	if start >= 0 && json.Unmarshal([]byte(output[start:]), &status) == nil {
		return &status.LoggedIn, status.AuthMethod
	}
	if value, ok := env.Getenv("ANTHROPIC_API_KEY"); ok && value != "" {
		signedIn := true
		return &signedIn, "api key"
	}
	return nil, ""
}

func detectCodex(ctx context.Context, env Env) Agent {
	agent := Agent{Name: config.EngineCodex}
	path, err := env.LookPath("codex")
	if err != nil {
		return agent
	}
	agent.Installed, agent.Path = true, path
	if output, err := env.Run(ctx, commandTimeout, path, "--version"); err == nil {
		agent.Version = versionPattern.FindString(output)
	}
	output, err := env.Run(ctx, commandTimeout, path, "login", "status")
	lower := strings.ToLower(output)
	switch {
	case err == nil && strings.Contains(lower, "logged in"):
		signedIn := true
		agent.SignedIn = &signedIn
		if _, method, ok := strings.Cut(strings.TrimSpace(output), "using "); ok {
			agent.AuthMethod = strings.TrimSpace(method)
		}
	case strings.Contains(lower, "not logged in"):
		signedIn := false
		agent.SignedIn = &signedIn
	}
	agent.Usable = agent.SignedIn == nil || *agent.SignedIn
	return agent
}

// Select decides what crowsnest runs given --engine and the agents found. auto prefers a
// usable Claude (the orchestrator's full feature set needs it), then Codex.
func Select(setting string, agents []Agent) Selection {
	find := func(name string) Agent {
		for _, agent := range agents {
			if agent.Name == name {
				return agent
			}
		}
		return Agent{Name: name}
	}
	claude, codex := find(config.EngineClaude), find(config.EngineCodex)
	selection := Selection{Setting: setting}
	switch setting {
	case config.EngineClaude, config.EngineCodex:
		chosen := find(setting)
		if chosen.Installed {
			selection.Orchestrator, selection.Jobs = setting, setting
			selection.Reason = fmt.Sprintf("--engine %s", setting)
		} else {
			selection.Reason = fmt.Sprintf("--engine %s, but %s is not installed", setting, setting)
		}
	default:
		switch {
		case claude.Usable:
			selection.Orchestrator, selection.Jobs, selection.Reason = config.EngineClaude, config.EngineClaude, "Claude Code is installed and signed in"
		case codex.Usable:
			selection.Orchestrator, selection.Jobs, selection.Reason = config.EngineCodex, config.EngineCodex, "Codex is the only usable agent"
		case claude.Installed:
			selection.Orchestrator, selection.Jobs, selection.Reason = config.EngineClaude, config.EngineClaude, "Claude Code is installed but not signed in"
		default:
			selection.Reason = "no agent is installed"
		}
	}
	return selection
}

// ClaudeInstall is the install advice for Claude Code.
func ClaudeInstall() (fix, command string) {
	return "Install Claude Code: `curl -fsSL https://claude.ai/install.sh | bash` (or `brew install --cask claude-code`), then run `claude` once to sign in", "curl -fsSL https://claude.ai/install.sh | bash"
}

func (r *runner) agentChecks(ctx context.Context) {
	agents := DetectAgents(ctx, r.env)
	r.report.Agents = agents
	claude, codex := agents[0], agents[1]
	r.report.Versions.Claude, r.report.Versions.Codex = claude.Version, codex.Version
	r.report.Paths.Claude, r.report.Paths.Codex = claude.Path, codex.Path
	setting := config.EngineAuto
	if r.configOK() && r.in.Config.Engine != "" {
		setting = r.in.Config.Engine
	}
	selection := Select(setting, agents)
	r.report.Selection = selection
	r.claudeAgentCheck(claude, selection)
	r.codexAgentCheck(codex, selection)
	r.selectionCheck(selection)
	if r.in.Deep {
		r.deepChecks(ctx, claude, codex)
	}
}

func (r *runner) claudeAgentCheck(claude Agent, selection Selection) {
	check := Check{ID: "agent.claude", Group: GroupAgents, Title: "Claude Code"}
	chosen := selection.Orchestrator == config.EngineClaude
	switch {
	case !claude.Installed:
		check.Status, check.Detail = StatusInfo, "not installed"
		if selection.Orchestrator == "" {
			check.Status = StatusFail
		}
		check.Fix, check.Command = ClaudeInstall()
		if selection.Orchestrator == config.EngineCodex {
			check.Status, check.Fix = StatusWarn, "Codex will run the orchestrator, without the stop-hook guarantee, structured approvals or voice replies; install Claude Code for those. "+check.Fix
		}
	case claude.SignedIn != nil && !*claude.SignedIn:
		check.Status, check.Detail = StatusWarn, fmt.Sprintf("%s at %s, not signed in", claude.Version, claude.Path)
		if chosen {
			check.Status = StatusFail
		}
		check.Fix, check.Command = "Sign in once in a terminal: run `claude` and follow the login (the app's Terminal can do it)", "claude"
	case claude.SignedIn == nil:
		check.Status, check.Detail = StatusWarn, fmt.Sprintf("%s at %s; could not tell whether it is signed in", claude.Version, claude.Path)
		check.Fix, check.Command = "Run `claude` once in a terminal to check it is signed in", "claude"
	default:
		check.Status, check.Detail = StatusOK, fmt.Sprintf("%s at %s, signed in (%s)", claude.Version, claude.Path, orDash(claude.AuthMethod))
	}
	r.add(check)
}

func (r *runner) codexAgentCheck(codex Agent, selection Selection) {
	check := Check{ID: "agent.codex", Group: GroupAgents, Title: "Codex"}
	switch {
	case !codex.Installed:
		check.Status, check.Detail = StatusInfo, "not installed (optional: a second engine for jobs and reviews)"
	case codex.SignedIn != nil && !*codex.SignedIn:
		check.Status, check.Detail = StatusWarn, fmt.Sprintf("%s at %s, not signed in", codex.Version, codex.Path)
		if selection.Orchestrator == config.EngineCodex {
			check.Status = StatusFail
		}
		check.Fix, check.Command = "Sign in once in a terminal: `codex login`", "codex login"
	default:
		state := "signed in"
		if codex.SignedIn == nil {
			state = "sign-in state unknown"
		}
		check.Status, check.Detail = StatusOK, fmt.Sprintf("%s at %s, %s", codex.Version, codex.Path, state)
	}
	r.add(check)
}

func (r *runner) selectionCheck(selection Selection) {
	check := Check{ID: "agent.selection", Group: GroupAgents, Title: "Orchestrator and jobs run on"}
	switch selection.Orchestrator {
	case "":
		check.Status, check.Detail = StatusFail, selection.Reason
		check.Fix, check.Command = ClaudeInstall()
	case config.EngineCodex:
		check.Status = StatusWarn
		check.Detail = "codex (" + selection.Reason + ")"
		check.Fix = "Codex mode has no stop-hook guarantee, no structured approvals and no SAY/voice parsing; install and sign in to Claude Code for the full set"
	default:
		check.Status, check.Detail = StatusOK, selection.Orchestrator+" ("+selection.Reason+")"
	}
	r.add(check)
}

// deepChecks makes one tiny real call per installed agent, in a temporary directory so neither
// the current directory's instructions nor a trust prompt get involved.
func (r *runner) deepChecks(ctx context.Context, claude, codex Agent) {
	dir, err := os.MkdirTemp("", "crowsnest-doctor-")
	if err == nil {
		defer func() { _ = os.RemoveAll(dir) }()
	}
	if claude.Installed {
		output, err := r.env.Run(ctx, deepTimeout, claude.Path, "-p", "Reply with exactly the word OK and nothing else.",
			"--model", "haiku", "--no-session-persistence", "--tools", "", "--setting-sources", "")
		r.add(deepCheck("deep.claude", "Claude Code answers (--deep)", output, err))
	}
	if codex.Installed {
		args := []string{"exec", "--skip-git-repo-check", "--ephemeral", "--sandbox", "read-only"}
		if dir != "" {
			args = append(args, "--cd", dir)
		}
		output, err := r.env.Run(ctx, deepTimeout, codex.Path, append(args, "Reply with exactly the word OK and nothing else.")...)
		r.add(deepCheck("deep.codex", "Codex answers (--deep)", output, err))
	}
}

func deepCheck(id, title, output string, err error) Check {
	check := Check{ID: id, Group: GroupAgents, Title: title, Cost: DeepCost}
	if err == nil && strings.Contains(strings.ToUpper(output), "OK") {
		check.Status, check.Detail = StatusOK, "replied OK"
		return check
	}
	check.Status = StatusFail
	check.Detail = "no OK reply: " + firstLine(output, err)
	check.Fix = "Run the agent by hand in a terminal to see why (signed in? network? usage limit?)"
	return check
}

func firstLine(output string, err error) string {
	line, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	if line == "" && err != nil {
		line = err.Error()
	}
	if len(line) > 160 {
		line = line[:160] + "…"
	}
	return line
}

func orDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
