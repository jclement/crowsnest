// Package agentcfg builds the files every Claude agent crowsnest starts is given (DESIGN.md 6.2
// and 6.3): the --settings file (the supervisor's hooks and the allow rule for crowsnest's MCP
// tools) and the --mcp-config file (one stdio server, `crowsnest mcp`). The orchestrator
// (internal/orch) and job agents (internal/jobs) both use it, so the two cannot drift apart.
//
// Nothing here is written into a repository or the user's own Claude settings.
package agentcfg

import (
	"path/filepath"
	"strings"
)

// MCPAllowRule pre-approves every tool of crowsnest's MCP server (verified in M6: the wildcard
// rule through --settings stops `ask` at no permission prompt, under auto and default).
const MCPAllowRule = "mcp__crowsnest__*"

// PermissionRequestTimeout is the PermissionRequest hook's timeout in seconds. The hook holds
// while a human decides in the browser (DESIGN.md 8.7). Claude Code's hooks reference documents
// no ceiling for a command hook's timeout (default 600 s); a hook held 700 s with this value was
// verified to allow its call on 2.1.332. The hook's own client gives up shortly after the
// server's approval expiry (approvals.DefaultTTL, 12 h), so this is only the outer fence: a hook
// killed by it leaves Claude's terminal dialog, which was on screen all along.
const PermissionRequestTimeout = 86400

// Hook names Claude Code fires, and the `crowsnest hook <event>` argument each runs.
var Hooks = []struct {
	Event   string // Claude Code's event name
	Command string // crowsnest hook <command>
	Timeout int    // seconds; the hook's own client gives up after 5 s and fails open
	Matcher string // tool-name matcher; "" for every tool or event
}{
	{"Stop", "stop", 30, ""},
	{"SessionStart", "session-start", 30, ""},
	{"Notification", "notification", 30, ""},
	{"StopFailure", "stop-failure", 30, ""},
	{"PermissionDenied", "permission-denied", 30, ""},
	{"SessionEnd", "session-end", 10, ""},
	// PostToolUse is not in DESIGN 6.3's first list: it is what tells the nudge budget that the
	// agent made progress (a tool call), and gives "doing now" its fallback.
	{"PostToolUse", "post-tool-use", 30, ""},
	// PermissionRequest holds until a human decides in the browser (8.7); it fails open to
	// Claude's own dialog.
	{"PermissionRequest", "permission-request", PermissionRequestTimeout, ""},
	// PreToolUse is the guard (8.7): it decides locally from GuardRules, so it is fast, and only
	// Bash is guarded.
	{"PreToolUse", "pre-tool-use", 10, "Bash"},
}

// ClaudeSettings is the --settings file. With executable and socket set it carries the
// supervisor's hooks; with withMCP the allow rule for crowsnest's tools. Each hook command names
// the agent's token file, which is how the socket knows whose hook it is (R1b N2): the server
// maps the token to the job (a job agent's) or to the pane, whose current job it then looks up
// (the orchestrator's). jobID is still baked in for a job agent, as the path a client uses; the
// server checks it against the token. guardRules is the guard's rules file (GuardRules), named on
// the PreToolUse hook's command line; "" leaves the guard out.
func ClaudeSettings(executable, socket, jobID, tokenFile, guardRules string, withMCP bool) map[string]any {
	settings := map[string]any{}
	if withMCP {
		settings["permissions"] = map[string]any{"allow": []string{MCPAllowRule}}
	}
	if executable == "" || socket == "" {
		return settings
	}
	hooks := map[string]any{}
	for _, hook := range Hooks {
		command := HookCommand(executable, socket, hook.Command, jobID, tokenFile)
		if hook.Command == "pre-tool-use" {
			if guardRules == "" {
				continue
			}
			command += " --rules " + quote(guardRules)
		}
		entry := map[string]any{"hooks": []map[string]any{{"type": "command", "command": command, "timeout": hook.Timeout}}}
		if hook.Matcher != "" {
			entry["matcher"] = hook.Matcher
		}
		hooks[hook.Event] = []map[string]any{entry}
	}
	settings["hooks"] = hooks
	return settings
}

// HookCommand is the shell command line for one hook. Claude Code runs it through a shell, so
// every argument is single-quoted.
func HookCommand(executable, socket, event, jobID, tokenFile string) string {
	parts := []string{quote(executable), "hook", event, "--socket", quote(socket)}
	if jobID != "" {
		parts = append(parts, "--job", quote(jobID))
	}
	if tokenFile != "" {
		parts = append(parts, "--token-file", quote(tokenFile))
	}
	return strings.Join(parts, " ")
}

// MCPConfig is the --mcp-config file: one stdio server, `<executable> mcp`, told where the agent
// socket is, which job it holds (a job agent's: the default job id for the tools' paths) and the
// agent's token, which is how the socket knows who is calling (R1b N2). The file is 0600.
func MCPConfig(executable, socket, jobID, token string) map[string]any {
	env := map[string]string{"CROWSNEST_SOCKET": socket}
	if jobID != "" {
		env["CROWSNEST_JOB"] = jobID
	}
	if token != "" {
		env["CROWSNEST_TOKEN"] = token
	}
	return map[string]any{"mcpServers": map[string]any{
		"crowsnest": map[string]any{"type": "stdio", "command": executable, "args": []string{"mcp"}, "env": env},
	}}
}

// quote single-quotes text for a POSIX shell.
func quote(text string) string {
	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}

// AllowRead adds a Read allow rule for each directory (and everything under it) to a --settings
// map, so an agent reads the attachments crowsnest copied into its inbox without a permission
// prompt under the default permission mode (DESIGN.md 8.6). Claude Code matches a rule against
// the path as the agent writes it, not resolved (probed on 2.1.332: `Read(//tmp/x/**)` allowed
// /tmp/x/a.png, `Read(//private/tmp/x/**)` did not), so both the path crowsnest types and its
// resolved form get a rule when they differ. `//` starts an absolute path in a rule.
func AllowRead(settings map[string]any, dirs ...string) map[string]any {
	var rules []string
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		rules = append(rules, readRule(dir))
		if resolved := resolveExisting(dir); resolved != filepath.Clean(dir) {
			rules = append(rules, readRule(resolved))
		}
	}
	if len(rules) == 0 {
		return settings
	}
	permissions, _ := settings["permissions"].(map[string]any)
	if permissions == nil {
		permissions = map[string]any{}
	}
	allow, _ := permissions["allow"].([]string)
	permissions["allow"] = append(allow, rules...)
	settings["permissions"] = permissions
	return settings
}

// resolveExisting resolves the links in dir's nearest existing ancestor and joins the rest back
// on: an inbox is often made after the agent starts.
func resolveExisting(dir string) string {
	dir = filepath.Clean(dir)
	rest := ""
	for path := dir; ; path = filepath.Dir(path) {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return filepath.Join(resolved, rest)
		}
		if parent := filepath.Dir(path); parent == path {
			return dir
		}
		rest = filepath.Join(filepath.Base(path), rest)
	}
}

// readRule is the allow rule for a directory: Read(//abs/path/**).
func readRule(dir string) string {
	return "Read(//" + strings.TrimPrefix(filepath.ToSlash(filepath.Clean(dir)), "/") + "/**)"
}
