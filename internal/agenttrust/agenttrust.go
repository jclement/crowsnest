// Package agenttrust records an agent's own folder-trust answer for a folder the human chose in
// crowsnest (DESIGN.md 8.2.1): the one exception to "nothing is written into the user's Claude
// settings". Neither agent offers a flag or setting that skips the prompt for a session (Claude
// Code 2.1.332 has none; codex-cli 0.167 ignores `-c projects."<dir>".trust_level`, both verified
// in tmux), so the only supported way is the record the prompt itself writes:
//
//   - Claude Code: `projects["<dir>"].hasTrustDialogAccepted = true` in ~/.claude.json (or
//     $CLAUDE_CONFIG_DIR/.claude.json). The same record Jeff's `new-project` writes, and the one
//     Claude's permissions documentation describes setting by hand.
//   - Codex: `[projects."<dir>"] trust_level = "trusted"` in ~/.codex/config.toml (or
//     $CODEX_HOME/config.toml), which is what pressing "Trust and continue" appends.
//
// Both agents resolve a linked git worktree to its main checkout's trust (verified: a worktree of
// a trusted repository starts with no prompt, one of an untrusted repository asks), so trusting a
// project's main checkout covers every job worktree crowsnest makes from it. Nothing here is ever
// called for a worktree, or for a folder an agent named.
//
// The rules: only one key is added or set, everything else in the file is kept byte for byte
// (Claude's JSON values are carried as raw JSON; Codex's TOML is appended to, never rewritten);
// the write is the careful update in file.go (Claude's own lock, a re-read and comparison just
// before the atomic rename, a link kept, the old mode kept); it is read back, and retried a few
// times if a running agent rewrote the file in between; a file that does not parse, or is not a
// JSON object, is left alone and reported; Codex's explicit "untrusted" for the folder is
// respected, not overwritten.
package agenttrust

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

// Result says what happened to one agent's record.
type Result struct {
	Agent  string `json:"agent"`  // claude or codex
	Status string `json:"status"` // trusted (written now), already, skipped, failed
	Detail string `json:"detail,omitempty"`
}

// Statuses.
const (
	StatusTrusted = "trusted"
	StatusAlready = "already"
	StatusSkipped = "skipped"
	StatusFailed  = "failed"
)

// Paths says where each agent keeps its record. Empty fields come from the environment.
type Paths struct {
	ClaudeConfig string // ~/.claude.json, or $CLAUDE_CONFIG_DIR/.claude.json
	CodexConfig  string // ~/.codex/config.toml, or $CODEX_HOME/config.toml
}

// DefaultPaths reads $CLAUDE_CONFIG_DIR, $CODEX_HOME and home.
func DefaultPaths(home string, getenv func(string) string) Paths {
	paths := Paths{ClaudeConfig: filepath.Join(home, ".claude.json"), CodexConfig: filepath.Join(home, ".codex", "config.toml")}
	if dir := getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		paths.ClaudeConfig = filepath.Join(dir, ".claude.json")
	}
	if dir := getenv("CODEX_HOME"); dir != "" {
		paths.CodexConfig = filepath.Join(dir, "config.toml")
	}
	return paths
}

// attempts and settle bound the read-back loop: an agent that rewrites its file while this one
// writes wins the race once or twice, then the record holds (new-project does the same).
const (
	attempts = 5
	settle   = 200 * time.Millisecond
)

// Trust records trust for every folder in dirs (a project's folder as given and resolved, when
// they differ), for Claude always and for Codex when codexInstalled. dirs must be folders the
// human chose; the caller guarantees that.
func Trust(paths Paths, dirs []string, codexInstalled bool) []Result {
	results := []Result{trustClaude(paths.ClaudeConfig, dirs)}
	if codexInstalled {
		results = append(results, trustCodex(paths.CodexConfig, dirs))
	} else {
		results = append(results, Result{Agent: "codex", Status: StatusSkipped, Detail: "Codex is not installed"})
	}
	return results
}

// ---- Claude ---------------------------------------------------------------------------------

func trustClaude(path string, dirs []string) Result {
	return confirmed(Result{Agent: "claude"}, "Claude Code", func() (bool, string, error) {
		wrote, err := updateFile(path, func(original []byte) ([]byte, bool, error) { return claudeWithTrust(original, dirs) })
		return wrote, "", err
	})
}

// confirmed runs one careful update, then again after a pause until an update finds nothing to
// change: that read-back is what says the record held (an agent rewriting its file from memory
// can drop it once or twice, as new-project found). refused ends it with that sentence.
func confirmed(result Result, agent string, update func() (wrote bool, refused string, err error)) Result {
	for attempt := 0; ; attempt++ {
		wrote, refused, err := update()
		switch {
		case err != nil:
			return failed(result, err)
		case refused != "":
			result.Status, result.Detail = StatusSkipped, refused
			return result
		case !wrote:
			result.Status = StatusAlready
			if attempt > 0 {
				result.Status = StatusTrusted
			}
			return result
		case attempt >= attempts:
			return failed(result, errors.New("the trust was not kept: "+agent+" kept rewriting its config"))
		}
		time.Sleep(settle)
	}
}

// claudeWithTrust returns the config with hasTrustDialogAccepted set for each dir, and whether
// anything changed. Every other value is carried as raw JSON, so nothing else is reinterpreted.
func claudeWithTrust(original []byte, dirs []string) ([]byte, bool, error) {
	top := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(original)) > 0 {
		if err := json.Unmarshal(original, &top); err != nil {
			return nil, false, fmt.Errorf("the config is not a JSON object, so it was left alone: %w", err)
		}
		if top == nil { // the JSON value null (R9-C9): not an object either
			return nil, false, errors.New("the config is null, not a JSON object, so it was left alone")
		}
	}
	projects := map[string]json.RawMessage{}
	if raw, ok := top["projects"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &projects); err != nil {
			return nil, false, fmt.Errorf("the config's projects value is not an object, so it was left alone: %w", err)
		}
	}
	changed := false
	for _, dir := range dirs {
		entry := map[string]json.RawMessage{}
		if raw, ok := projects[dir]; ok && string(raw) != "null" {
			if err := json.Unmarshal(raw, &entry); err != nil {
				return nil, false, fmt.Errorf("the config's entry for %s is not an object, so it was left alone: %w", dir, err)
			}
		}
		if string(entry["hasTrustDialogAccepted"]) == "true" {
			continue
		}
		entry["hasTrustDialogAccepted"] = json.RawMessage("true")
		raw, err := marshal(entry, "")
		if err != nil {
			return nil, false, err
		}
		projects[dir] = raw
		changed = true
	}
	if !changed {
		return original, false, nil
	}
	raw, err := marshal(projects, "")
	if err != nil {
		return nil, false, err
	}
	top["projects"] = raw
	out, err := marshal(top, "  ")
	if err != nil {
		return nil, false, err
	}
	return append(out, '\n'), true, nil
}

// marshal encodes without escaping <, > and & (Claude's own values stay as they were written),
// indented when indent is set, with no trailing newline.
func marshal(value any, indent string) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if indent != "" {
		encoder.SetIndent("", indent)
	}
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// ---- Codex ----------------------------------------------------------------------------------

// codexConfig is the part of Codex's config.toml this reads.
type codexConfig struct {
	Projects map[string]struct {
		TrustLevel string `toml:"trust_level"`
	} `toml:"projects"`
}

func trustCodex(path string, dirs []string) Result {
	return confirmed(Result{Agent: "codex"}, "Codex", func() (bool, string, error) {
		refused := ""
		wrote, err := updateFile(path, func(original []byte) ([]byte, bool, error) {
			updated, changed, why, err := codexWithTrust(original, dirs)
			refused = why
			return updated, changed, err
		})
		return wrote, refused, err
	})
}

// codexWithTrust appends a [projects."<dir>"] table for each dir Codex has no answer for. A
// folder Codex was told not to trust is left as it is (refused says so); the result must parse,
// or nothing is written.
func codexWithTrust(original []byte, dirs []string) (updated []byte, changed bool, refused string, err error) {
	var parsed codexConfig
	if err := toml.Unmarshal(original, &parsed); err != nil {
		return nil, false, "", fmt.Errorf("config.toml does not parse, so it was left alone: %w", err)
	}
	updated = append([]byte{}, original...)
	for _, dir := range dirs {
		switch parsed.Projects[dir].TrustLevel {
		case "trusted":
			continue
		case "":
		default:
			return nil, false, "Codex was told not to trust " + dir + " (trust_level = " + strconv.Quote(parsed.Projects[dir].TrustLevel) + "); that answer is kept", nil
		}
		if len(updated) > 0 && !bytes.HasSuffix(updated, []byte("\n")) {
			updated = append(updated, '\n')
		}
		updated = append(updated, []byte("\n[projects."+strconv.Quote(dir)+"]\ntrust_level = \"trusted\"\n")...)
		changed = true
	}
	if !changed {
		return original, false, "", nil
	}
	var check codexConfig
	if err := toml.Unmarshal(updated, &check); err != nil {
		return nil, false, "", fmt.Errorf("adding the trust to config.toml would not parse (an inline projects table?), so it was left alone: %w", err)
	}
	for _, dir := range dirs {
		if check.Projects[dir].TrustLevel != "trusted" {
			return nil, false, "", fmt.Errorf("config.toml would not read back %s as trusted, so it was left alone", dir)
		}
	}
	return updated, true, "", nil
}

// ---- Files ----------------------------------------------------------------------------------

func failed(result Result, err error) Result {
	result.Status, result.Detail = StatusFailed, err.Error()
	return result
}
