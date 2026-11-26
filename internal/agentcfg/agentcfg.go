// Package agentcfg builds the files every Claude agent crowsnest starts is given (DESIGN.md 6.2
// and 6.3): the --settings file (the supervisor's hooks and the allow rule for crowsnest's MCP
// tools) and the --mcp-config file (one stdio server, `crowsnest mcp`). The orchestrator
// (internal/orch) and job agents (internal/jobs) both use it, so the two cannot drift apart.
//
// Nothing here is written into a repository or the user's own Claude settings.
package agentcfg

import (
	"strings"
)

// MCPAllowRule pre-approves every tool of crowsnest's MCP server (verified in M6: the wildcard
// rule through --settings stops `ask` at no permission prompt, under auto and default).
const MCPAllowRule = "mcp__crowsnest__*"

// Hook names Claude Code fires, and the `crowsnest hook <event>` argument each runs.
var Hooks = []struct {
	Event   string // Claude Code's event name
	Command string // crowsnest hook <command>
	Timeout int    // seconds; the hook's own client gives up after 5 s and fails open
}{
	{"Stop", "stop", 30},
	{"SessionStart", "session-start", 30},
	{"Notification", "notification", 30},
	{"StopFailure", "stop-failure", 30},
	{"PermissionDenied", "permission-denied", 30},
	{"SessionEnd", "session-end", 10},
	// PostToolUse is not in DESIGN 6.3's first list: it is what tells the nudge budget that the
	// agent made progress (a tool call), and gives "doing now" its fallback.
	{"PostToolUse", "post-tool-use", 30},
}

// ClaudeSettings is the --settings file. With executable and socket set it carries the
// supervisor's hooks; with withMCP the allow rule for crowsnest's tools. jobID is baked into
// each hook command for a job agent; the orchestrator's hooks carry none, and the server finds
// its current job from the calling pane.
func ClaudeSettings(executable, socket, jobID string, withMCP bool) map[string]any {
	settings := map[string]any{}
	if withMCP {
		settings["permissions"] = map[string]any{"allow": []string{MCPAllowRule}}
	}
	if executable == "" || socket == "" {
		return settings
	}
	hooks := map[string]any{}
	for _, hook := range Hooks {
		hooks[hook.Event] = []map[string]any{{
			"hooks": []map[string]any{{
				"type": "command", "command": HookCommand(executable, socket, hook.Command, jobID), "timeout": hook.Timeout,
			}},
		}}
	}
	settings["hooks"] = hooks
	return settings
}

// HookCommand is the shell command line for one hook. Claude Code runs it through a shell, so
// every argument is single-quoted.
func HookCommand(executable, socket, event, jobID string) string {
	parts := []string{quote(executable), "hook", event, "--socket", quote(socket)}
	if jobID != "" {
		parts = append(parts, "--job", quote(jobID))
	}
	return strings.Join(parts, " ")
}

// MCPConfig is the --mcp-config file: one stdio server, `<executable> mcp`, told where the agent
// socket is and (for a job agent) which job it holds. The pane's own environment (HERDR_PANE_ID)
// reaches it too, which is how the server knows which pane is asking.
func MCPConfig(executable, socket, jobID string) map[string]any {
	env := map[string]string{"CROWSNEST_SOCKET": socket}
	if jobID != "" {
		env["CROWSNEST_JOB"] = jobID
	}
	return map[string]any{"mcpServers": map[string]any{
		"crowsnest": map[string]any{"type": "stdio", "command": executable, "args": []string{"mcp"}, "env": env},
	}}
}

// quote single-quotes text for a POSIX shell.
func quote(text string) string {
	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}
