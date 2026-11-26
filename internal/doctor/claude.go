// What crowsnest needs from Claude Code in particular: the command-line flags it starts every
// agent with, a version whose hooks it relies on, the orchestrator directory's folder trust,
// and a readable transcript directory.

package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// MinClaudeVersion is the oldest Claude Code whose hooks crowsnest relies on: Stop with
// stop_hook_active, SessionStart sources, StopFailure, PermissionRequest and PreToolUse
// decisions all arrived in the 2.1 line (checked against 2.1.332).
const MinClaudeVersion = "2.1.0"

// ClaudeFlags are the flags crowsnest passes to every Claude it starts (DESIGN.md 6.2, 7.10).
var ClaudeFlags = []string{"--settings", "--mcp-config", "--append-system-prompt-file", "--session-id", "--remote-control", "--permission-mode"}

func (r *runner) claudeChecks(ctx context.Context) {
	claude := r.report.Agent("claude")
	if !claude.Installed {
		return
	}
	r.claudeFlagsCheck(ctx, claude)
	r.claudeHooksCheck(claude)
	if r.configOK() {
		r.claudeTrustCheck()
	}
	r.claudeTranscriptCheck()
}

func (r *runner) claudeFlagsCheck(ctx context.Context, claude Agent) {
	output, err := r.env.Run(ctx, commandTimeout, claude.Path, "--help")
	check := Check{ID: "claude.flags", Group: GroupClaude, Title: "Claude Code flags crowsnest uses"}
	if err != nil && output == "" {
		check.Status, check.Detail, check.Fix = StatusWarn, "`claude --help` failed", "Check `claude --help` runs"
		r.add(check)
		return
	}
	var missing []string
	for _, flag := range ClaudeFlags {
		if !HelpHasFlag(output, flag) {
			missing = append(missing, flag)
		}
	}
	if len(missing) > 0 {
		check.Status = StatusFail
		check.Detail = "this Claude Code lacks " + strings.Join(missing, ", ")
		check.Fix, check.Command = "Update Claude Code: `claude update`", "claude update"
	} else {
		check.Status, check.Detail = StatusOK, strings.Join(ClaudeFlags, " ")
	}
	r.add(check)
}

// HelpHasFlag reports whether `claude --help` text names flag. Claude lists some flags only in
// a family's bracket form: --append-system-prompt-file appears as "--append-system-prompt[-file]".
func HelpHasFlag(help, flag string) bool {
	for index := strings.Index(help, flag); index >= 0; {
		next := index + len(flag)
		if next >= len(help) || !isFlagChar(help[next]) {
			return true
		}
		found := strings.Index(help[next:], flag)
		if found < 0 {
			break
		}
		index = next + found
	}
	if base, ok := strings.CutSuffix(flag, "-file"); ok {
		return strings.Contains(help, base+"[-file]")
	}
	return false
}

func isFlagChar(c byte) bool { return c == '-' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') }

func (r *runner) claudeHooksCheck(claude Agent) {
	check := Check{ID: "claude.hooks", Group: GroupClaude, Title: "Claude Code hooks"}
	switch {
	case claude.Version == "":
		check.Status, check.Detail = StatusWarn, "version unknown; hooks need "+MinClaudeVersion+" or newer"
	case CompareVersions(claude.Version, MinClaudeVersion) < 0:
		check.Status = StatusFail
		check.Detail = fmt.Sprintf("%s is older than %s: the Stop, StopFailure, PermissionRequest and PreToolUse hooks crowsnest supervises with are missing", claude.Version, MinClaudeVersion)
		check.Fix, check.Command = "Update Claude Code: `claude update`", "claude update"
	default:
		check.Status, check.Detail = StatusOK, "Stop, SessionStart, Notification, StopFailure, PermissionRequest, PreToolUse, PostToolUse, SessionEnd"
	}
	r.add(check)
}

// claudeConfigDir is CLAUDE_CONFIG_DIR, else ~/.claude.
func (r *runner) claudeConfigDir() string {
	if value, ok := r.env.Getenv("CLAUDE_CONFIG_DIR"); ok && value != "" {
		return value
	}
	return filepath.Join(r.env.Home, ".claude")
}

// claudeGlobalConfig is the file holding per-project trust: $CLAUDE_CONFIG_DIR/.claude.json, else
// ~/.claude.json.
func (r *runner) claudeGlobalConfig() string {
	if value, ok := r.env.Getenv("CLAUDE_CONFIG_DIR"); ok && value != "" {
		return filepath.Join(value, ".claude.json")
	}
	return filepath.Join(r.env.Home, ".claude.json")
}

// claudeProjects is the shape of Claude's global config that holds folder trust.
type claudeProjects struct {
	Projects map[string]struct {
		HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted"`
	} `json:"projects"`
}

// DirTrusted reports whether Claude's global config (its JSON text) records dir, or a parent of
// it, as trusted.
func DirTrusted(configJSON []byte, dir string) bool {
	var parsed claudeProjects
	if json.Unmarshal(configJSON, &parsed) != nil {
		return false
	}
	for path := filepath.Clean(dir); ; path = filepath.Dir(path) {
		if project, ok := parsed.Projects[path]; ok && project.HasTrustDialogAccepted {
			return true
		}
		if filepath.Dir(path) == path {
			return false
		}
	}
}

func (r *runner) claudeTrustCheck() {
	dir := r.in.Config.Dir
	check := Check{ID: "claude.trust", Group: GroupClaude, Title: "Orchestrator folder trusted"}
	data, err := r.env.ReadFile(r.claudeGlobalConfig())
	if err == nil && DirTrusted(data, dir) {
		check.Status, check.Detail = StatusOK, dir+" is trusted"
	} else {
		check.Status = StatusInfo
		check.Detail = "Claude will ask once whether to trust " + dir
		check.Fix = "Nothing to do: crowsnest shows that question as a card with its buttons when the orchestrator starts"
	}
	r.add(check)
}

func (r *runner) claudeTranscriptCheck() {
	dir := filepath.Join(r.claudeConfigDir(), "projects")
	check := Check{ID: "claude.transcripts", Group: GroupClaude, Title: "Claude transcripts readable"}
	info, err := r.env.Stat(dir)
	switch {
	case err != nil:
		check.Status, check.Detail = StatusOK, dir+" does not exist yet; Claude creates it on the first turn"
	case !info.IsDir():
		check.Status, check.Detail, check.Fix = StatusFail, dir+" is not a directory", "Move it aside; Claude keeps conversations there"
	default:
		if !r.env.Readable(dir) {
			check.Status, check.Detail, check.Fix = StatusWarn, dir+" is not readable", "Check its permissions: the Chat view reads conversations from it"
		} else {
			check.Status, check.Detail = StatusOK, dir
		}
	}
	r.add(check)
}
