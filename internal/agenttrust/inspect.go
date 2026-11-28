// What a folder carries that an agent runs or reads the moment it starts there (R9-S4, R9-S5,
// R10 new-M1). Trusting a folder lets these act with no prompt: a Claude settings file (any key
// may run or grant: hooks, statusLine, apiKeyHelper and the other helper commands, env, permission
// allow rules, enabled plugins and extra marketplaces, project MCP switches), a project MCP server
// list, Claude's project skills, commands and agents (frontmatter hooks, allowed-tools, shell
// injection, a skills-directory plugin) and workflows (scripts the runtime executes), and a Codex
// configuration (its hooks and MCP servers run outside Codex's sandbox). The instruction files
// are read, not run, but they steer the agent from its first turn. The project form leaves
// "Agents may work in this folder without asking" unticked when any is found and lists them; a
// Codex job is not started on a .codex/ the human has not seen.
//
// The list follows Claude Code's documentation as read on 2026-11-28 (code.claude.com/docs/en:
// permissions "What runs before you trust a folder", settings, settings-reference, skills,
// sub-agents, plugins/loading, claude-directory, memory). A settings file counts whatever it
// holds: crowsnest does not try to decide which keys are harmless, since new ones keep arriving.
//
// Every read here is of a file someone else may have written (a cloned repository), so nothing is
// parsed and nothing is followed: presence (Lstat) is enough.

package agenttrust

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

// Finding kinds.
const (
	KindSettings     = "settings"     // a Claude settings file: whatever it holds may run or grant
	KindHooks        = "hooks"        // kept for older clients; Inspect reports KindSettings
	KindClaude       = "claude"       // .claude/skills, commands, agents, workflows
	KindMCP          = "mcp"          // .mcp.json: MCP servers Claude may start
	KindCodex        = "codex"        // .codex/: Codex's project config, hooks and MCP servers
	KindInstructions = "instructions" // CLAUDE.md, AGENTS.md: read, not run
)

// Finding is one thing in a folder an agent runs or reads at its start.
type Finding struct {
	// Path is relative to the folder (".claude/settings.json", ".codex/hooks.json").
	Path string `json:"path"`
	Kind string `json:"kind"`
	// Runs is true for what executes or grants when an agent starts (settings, MCP servers,
	// skills and the like, Codex config);
	// false for instructions, which are only read.
	Runs bool `json:"runs"`
}

// claudeRunning are the project paths under .claude/ that Claude Code acts on once the folder is
// trusted, beyond the settings files: skills and commands (frontmatter hooks and allowed-tools,
// !`command` shell injection, a .claude-plugin/ skills-directory plugin), agents (frontmatter
// hooks and MCP servers) and workflows (scripts the runtime executes).
var claudeRunning = []string{".claude/skills", ".claude/commands", ".claude/agents", ".claude/workflows"}

// claudeInstructions are what Claude Code reads as instructions at start (or a subagent's start).
var claudeInstructions = []string{"CLAUDE.md", ".claude/CLAUDE.md", "CLAUDE.local.md", "AGENTS.md", ".claude/AGENTS.md",
	".claude/rules", ".claude/output-styles", ".claude/agent-memory"}

// Inspect lists what dir carries that runs or is read when an agent starts there, in a fixed
// order. A file that cannot be read (a link, a FIFO, too big, unreadable) is still listed when it
// names something that runs: not knowing what it holds is no reason to call the folder clean.
func Inspect(dir string) []Finding {
	var found []Finding
	for _, name := range []string{".claude/settings.json", ".claude/settings.local.json"} {
		if exists(filepath.Join(dir, name)) {
			found = append(found, Finding{Path: name, Kind: KindSettings, Runs: true})
		}
	}
	if exists(filepath.Join(dir, ".mcp.json")) {
		found = append(found, Finding{Path: ".mcp.json", Kind: KindMCP, Runs: true})
	}
	for _, name := range claudeRunning {
		if exists(filepath.Join(dir, name)) {
			found = append(found, Finding{Path: name + "/", Kind: KindClaude, Runs: true})
		}
	}
	if files, _ := codexFiles(dir); len(files) > 0 {
		for _, file := range files {
			found = append(found, Finding{Path: file, Kind: KindCodex, Runs: true})
		}
	} else if exists(filepath.Join(dir, ".codex")) {
		found = append(found, Finding{Path: ".codex/", Kind: KindCodex, Runs: true})
	}
	for _, name := range claudeInstructions {
		if exists(filepath.Join(dir, name)) {
			found = append(found, Finding{Path: name, Kind: KindInstructions})
		}
	}
	return found
}

// AnyRuns reports whether a finding runs code at agent start.
func AnyRuns(found []Finding) bool {
	for _, finding := range found {
		if finding.Runs {
			return true
		}
	}
	return false
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// ---- Codex's project configuration --------------------------------------------------------

// Bounds on what is fingerprinted under .codex/: a project's Codex config is a couple of small
// files. Past these the fingerprint fails, and a Codex job there is refused with the reason.
const (
	codexMaxFiles    = 64
	codexMaxFileSize = 1 << 20
)

// CodexConfig is a folder's .codex/ as a Codex job would load it.
type CodexConfig struct {
	// Files are the regular files under .codex/, relative to the folder, sorted.
	Files []string `json:"files"`
	// Fingerprint is a hash of every file's name and content ("" when there is no .codex/).
	Fingerprint string `json:"fingerprint"`
}

// CodexConfigOf fingerprints dir's .codex/. With --dangerously-bypass-hook-trust (how a Codex job
// gets crowsnest's hooks) Codex runs a repository's own .codex hooks without asking, and its MCP
// servers start outside the sandbox; the fingerprint is what the human saw, so a later change
// (a pull, or anything written into the folder) is caught before the next Codex job starts.
// No .codex/ gives a zero CodexConfig and no error. Anything that cannot be read safely (a link,
// a FIFO, too many or too big files) is an error.
func CodexConfigOf(dir string) (CodexConfig, error) {
	files, err := codexFiles(dir)
	if err != nil || len(files) == 0 {
		return CodexConfig{}, err
	}
	hash := sha256.New()
	for _, name := range files {
		data, err := readSmall(filepath.Join(dir, name), codexMaxFileSize)
		if err != nil {
			return CodexConfig{Files: files}, fmt.Errorf("reading %s: %w", name, err)
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00", name, len(data))
		_, _ = hash.Write(data)
	}
	return CodexConfig{Files: files, Fingerprint: hex.EncodeToString(hash.Sum(nil))}, nil
}

// codexFiles lists the files under dir/.codex, relative to dir. A .codex that is a link, or a
// link or special file inside it, is an error: crowsnest will not vouch for what it points at.
func codexFiles(dir string) ([]string, error) {
	root := filepath.Join(dir, ".codex")
	info, err := os.Lstat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a plain directory", root)
	}
	var files []string
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		if len(files) == codexMaxFiles {
			return fmt.Errorf("%s holds more than %d files", root, codexMaxFiles)
		}
		rel, _ := filepath.Rel(dir, path)
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(files)
	return files, walkErr
}

// readSmall reads a regular file of at most limit bytes without following a link at its last
// element and without blocking on a FIFO (R9-S8: a planted FIFO must not hang the server).
func readSmall(path string, limit int64) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) //nolint:gosec // a file crowsnest inspects, never serves
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, limit)
	}
	return io.ReadAll(io.LimitReader(file, limit+1))
}
