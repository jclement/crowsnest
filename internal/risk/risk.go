// Package risk classifies a tool call waiting for approval as low, elevated or dangerous
// (DESIGN.md 8.7). The level decides where it may be answered: low anywhere (the tray, the Inbox,
// a notification's buttons), elevated from the tray or the Inbox but never from a notification,
// dangerous only with an explicit second confirmation on the card and never from a notification.
//
// The built-in rules are a small table (Builtins) plus three checks in code: a protected host
// named anywhere in the input (dangerous), a file write outside the job's directory (elevated) and
// a secrets-looking path (elevated). The patterns come from the front door's relay lists
// (claude-agent's frontdoor/relay-refuse-patterns). A file in ~/.config/crowsnest/risk.yaml can
// disable built-ins by id and add rules of its own; a matching rule from the file decides the
// level outright (so it can also lower one), otherwise the highest built-in level wins.
package risk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/jclement/crowsnest/internal/guard"
	"github.com/jclement/crowsnest/internal/termtext"
)

// Level is how risky a call is.
type Level string

// The levels, lowest first.
const (
	Low       Level = "low"
	Elevated  Level = "elevated"
	Dangerous Level = "dangerous"
)

func (l Level) rank() int {
	switch l {
	case Dangerous:
		return 2
	case Elevated:
		return 1
	}
	return 0
}

// AtMost reports whether l is no riskier than limit (the approval policy's max_risk check).
func (l Level) AtMost(limit Level) bool { return l.rank() <= limit.rank() }

// Valid reports whether l is one of the three levels.
func (l Level) Valid() bool { return l == Low || l == Elevated || l == Dangerous }

// Rule is one entry of the table: tools it applies to, the input field it reads and a
// case-insensitive regular expression.
type Rule struct {
	ID    string   `yaml:"id" json:"id"`
	Level Level    `yaml:"level" json:"level"`
	Tools []string `yaml:"tools" json:"tools"` // tool names; a trailing * is a prefix ("mcp__*"); empty means every tool
	// Field is the tool-input field matched: "command", "path" (file_path, path, notebook_path),
	// "url", or "any" (every string in the input). Default "any".
	Field   string `yaml:"field" json:"field"`
	Pattern string `yaml:"pattern" json:"pattern"`
	Reason  string `yaml:"reason" json:"reason"`

	re *regexp.Regexp
}

// The ids of the checks done in code, which a file can disable like any rule.
const (
	CheckProtectedHost = "protected-host"
	CheckOutsideDir    = "outside-dir"
	CheckSecrets       = "secrets"
)

// Builtins is the rule table.
var Builtins = []Rule{
	{ID: "force-push", Level: Dangerous, Tools: []string{"Bash"}, Field: "command", Pattern: `git\s+push\b.*(\s--force\b|\s-[a-z]*f\b|--force-with-lease|\s\+\S)`, Reason: "Force push: rewrites the history of a remote branch"},
	{ID: "rm-rf", Level: Dangerous, Tools: []string{"Bash"}, Field: "command", Pattern: `\brm\s+(-[a-z]*r[a-z]*f|-[a-z]*f[a-z]*r|(-\S+\s+)*(-r|-R|--recursive)\s+(-\S+\s+)*(-f|--force)\b|(-\S+\s+)*(-f|--force)\s+(-\S+\s+)*(-r|-R|--recursive)\b)`, Reason: "rm -rf: deletes files and directories without asking"},
	{ID: "reset-hard", Level: Dangerous, Tools: []string{"Bash"}, Field: "command", Pattern: `git\s+reset\b.*--hard`, Reason: "git reset --hard: throws away uncommitted work"},
	{ID: "git-clean", Level: Dangerous, Tools: []string{"Bash"}, Field: "command", Pattern: `git\s+clean\s+(-\S*\s+)*-[a-z]*f`, Reason: "git clean -f: deletes untracked files"},
	{ID: "drop-truncate", Level: Dangerous, Field: "any", Pattern: `\b(drop\s+(table|database|schema)|truncate\s+(table\s+)?[a-z_"])`, Reason: "Drops or truncates database tables"},
	{ID: "sudo", Level: Dangerous, Tools: []string{"Bash"}, Field: "command", Pattern: `(^|[^[:alnum:]_-])sudo([^[:alnum:]_-]|$)`, Reason: "sudo: runs as root"},
	{ID: "disk", Level: Dangerous, Tools: []string{"Bash"}, Field: "command", Pattern: `\b(mkfs(\.\w+)?|diskutil\s+(erase|partition)\w*|dd\s+.*\bof=)`, Reason: "Writes a disk or filesystem directly"},
	{ID: "pipe-to-shell", Level: Dangerous, Tools: []string{"Bash"}, Field: "command", Pattern: `\b(curl|wget)\b[^|]*\|\s*(sudo\s+)?(ba|z|da)?sh\b`, Reason: "Runs a script straight from the network"},
	{ID: "chmod-world", Level: Dangerous, Tools: []string{"Bash"}, Field: "command", Pattern: `chmod\s+(-R\s+)?(0?777|a\+rwx)`, Reason: "Makes files writable by everyone"},
	{ID: "git-push", Level: Elevated, Tools: []string{"Bash"}, Field: "command", Pattern: `git\s+push\b`, Reason: "Pushes to a remote"},
	{ID: "network", Level: Elevated, Tools: []string{"Bash"}, Field: "command", Pattern: `(^|[\s;&|(])(curl|wget|ssh|scp|sftp|rsync|nc|telnet)\s`, Reason: "Talks to another machine"},
	{ID: "install", Level: Elevated, Tools: []string{"Bash"}, Field: "command", Pattern: `\b(npm|pnpm|yarn|bun|pip3?|pipx|brew|gem|cargo|apt(-get)?)\s+(install|add|i|global)\b|\bgo\s+install\b`, Reason: "Installs software"},
	{ID: "kill", Level: Elevated, Tools: []string{"Bash"}, Field: "command", Pattern: `(^|[\s;&|(])(kill|pkill|killall)\s`, Reason: "Stops processes"},
	{ID: "docker-destroy", Level: Elevated, Tools: []string{"Bash"}, Field: "command", Pattern: `\b(docker|podman)\s+((container|image|volume|network)\s+)?(rm|rmi|prune|system\s+prune|volume\s+rm)\b`, Reason: "Removes containers, images or volumes"},
	{ID: "mcp", Level: Elevated, Tools: []string{"mcp__*"}, Field: "any", Pattern: `.`, Reason: "Calls a tool on an MCP server, which may act outside this machine"},
	{ID: "permission-mode", Level: Elevated, Tools: []string{"ExitPlanMode"}, Field: "any", Pattern: `.`, Reason: "Leaves plan mode and starts making changes"},
}

// secretPath matches paths that usually hold credentials.
var secretPath = regexp.MustCompile(`(?i)(^|/)(\.env(\.[\w.-]+)?$|\.envrc$|id_(rsa|dsa|ecdsa|ed25519)(\.pub)?$|\.ssh/|\.aws/|\.gnupg/|\.kube/config|\.docker/config\.json|\.netrc$|\.npmrc$|\.pypirc$|credentials(\.json)?$|[^/]*\.(pem|p12|pfx|key)$|[^/]*secrets?[^/]*$|[^/]*tokens?(\.json|\.txt)?$|agent-token$)`)

// Context is what a classification knows besides the call itself.
type Context struct {
	// JobRoot is the job's working directory (links resolved); "" when unknown (the orchestrator).
	JobRoot string
	// ProtectedHosts are the production hosts (guard's list).
	ProtectedHosts []string
}

// Result is a classification: the level and why, most serious first.
type Result struct {
	Level   Level    `json:"level"`
	Reasons []string `json:"reasons"`
	// Unrecognised is true when the call is elevated only because it is not a shape crowsnest
	// knows (an interpreter, an unknown program, a command it cannot read): no rule of the table
	// says what it does. Such a call is never remembered from a card (R6-S1).
	Unrecognised bool `json:"unrecognised,omitempty"`
}

// Classifier holds the rules in force.
type Classifier struct {
	builtins []Rule
	custom   []Rule
	disabled map[string]bool
}

// Default is the built-in table and checks.
func Default() *Classifier {
	classifier, err := build(nil, nil)
	if err != nil {
		panic(err) // the built-ins are tested to compile
	}
	return classifier
}

// File is the override file's shape.
type File struct {
	Disable []string `yaml:"disable"`
	Rules   []Rule   `yaml:"rules"`
}

// Load returns the built-ins with the override file at path applied; a missing file is the
// built-ins alone. An invalid file is an error, so a typo does not silently loosen anything.
func Load(path string) (*Classifier, error) {
	if path == "" {
		return Default(), nil
	}
	body, err := os.ReadFile(path) //nolint:gosec // a configured path
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var file File
	decoder := yaml.NewDecoder(strings.NewReader(string(body)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil && err.Error() != "EOF" {
		return nil, fmt.Errorf("risk rules %s: %w", path, err)
	}
	classifier, err := build(file.Rules, file.Disable)
	if err != nil {
		return nil, fmt.Errorf("risk rules %s: %w", path, err)
	}
	return classifier, nil
}

func build(custom []Rule, disable []string) (*Classifier, error) {
	classifier := &Classifier{disabled: map[string]bool{}}
	for _, id := range disable {
		classifier.disabled[id] = true
	}
	for _, rule := range Builtins {
		compiled, err := compile(rule)
		if err != nil {
			return nil, err
		}
		classifier.builtins = append(classifier.builtins, compiled)
	}
	for i, rule := range custom {
		if rule.ID == "" {
			rule.ID = fmt.Sprintf("custom-%d", i+1)
		}
		if !rule.Level.Valid() {
			return nil, fmt.Errorf("rule %s: level must be low, elevated or dangerous", rule.ID)
		}
		compiled, err := compile(rule)
		if err != nil {
			return nil, err
		}
		classifier.custom = append(classifier.custom, compiled)
	}
	return classifier, nil
}

func compile(rule Rule) (Rule, error) {
	re, err := regexp.Compile("(?i)" + rule.Pattern)
	if err != nil {
		return Rule{}, fmt.Errorf("rule %s: %w", rule.ID, err)
	}
	if rule.Field == "" {
		rule.Field = "any"
	}
	switch rule.Field {
	case "any", "command", "path", "url":
	default:
		return Rule{}, fmt.Errorf("rule %s: field must be any, command, path or url", rule.ID)
	}
	if rule.Reason == "" {
		rule.Reason = "Matches the rule " + rule.ID
	}
	rule.re = re
	return rule, nil
}

// hit is one rule or check that applies to a call.
type hit struct {
	level  Level
	reason string
	shape  bool // from shapeHits: not a known shape, rather than a rule saying what it does
}

// Classify rates one tool call. A Bash command is read first (guard.Parse): the table's command
// rules are matched against the line as written and against each simple command written plainly
// (quotes, wrappers and git's global options removed), so a respelling cannot slip under a rule.
// The call stays low only if every part of it is a known-benign shape (shape.go).
func (c *Classifier) Classify(tool string, input json.RawMessage, ctx Context) Result {
	var fields map[string]any
	_ = json.Unmarshal(input, &fields)
	var commands []guard.Command
	readable := true
	plain := []string{}
	if command, ok := fields["command"].(string); ok && tool == "Bash" {
		commands, readable = guard.Parse(command)
		for _, cmd := range commands {
			plain = append(plain, cmd.Text())
		}
	}
	for _, rule := range c.custom {
		if rule.matches(tool, fields, plain) {
			return Result{Level: rule.Level, Reasons: []string{rule.Reason}}
		}
	}
	var hits []hit
	for _, rule := range c.builtins {
		if !c.disabled[rule.ID] && rule.matches(tool, fields, plain) {
			hits = append(hits, hit{rule.Level, rule.Reason, false})
		}
	}
	hits = append(hits, shapeHits(tool, fields, commands, readable, ctx.JobRoot)...)
	if !c.disabled[CheckProtectedHost] {
		if host := guard.HostNamed(ctx.ProtectedHosts, strings.Join(stringsIn(fields), "\n")); host != "" {
			hits = append(hits, hit{Dangerous, "Names " + host + ", a protected (production) host", false})
		}
	}
	paths := pathsIn(fields)
	if !c.disabled[CheckOutsideDir] && writes(tool) {
		if ctx.JobRoot == "" {
			// R9-S7: with no job directory (the orchestrator, a call with no job) there is nothing to
			// check the path against, so a write anywhere (~/.zshrc, a LaunchAgent) is not "low".
			hits = append(hits, hit{Elevated, "Writes a file, with no job directory to check it against", false})
		}
		for _, path := range paths {
			if ctx.JobRoot != "" && !inside(ctx.JobRoot, path) {
				hits = append(hits, hit{Elevated, "Writes outside the job's directory (" + ctx.JobRoot + ")", false})
				break
			}
		}
	}
	if !c.disabled[CheckSecrets] {
		candidates := paths
		if command, ok := fields["command"].(string); ok {
			candidates = append(candidates, strings.Fields(command)...)
		}
		for _, path := range candidates {
			if secretPath.MatchString(strings.Trim(path, `"'`)) {
				hits = append(hits, hit{Elevated, "Touches a file that may hold secrets (" + filepath.Base(strings.Trim(path, `"'`)) + ")", false})
				break
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].level.rank() > hits[j].level.rank() })
	result := Result{Level: Low}
	seen := map[string]bool{}
	recognised := false
	for _, h := range hits {
		if h.level != Low && !h.shape {
			recognised = true
		}
		if h.level.rank() > result.Level.rank() {
			result.Level = h.level
		}
		if !seen[h.reason] {
			seen[h.reason] = true
			result.Reasons = append(result.Reasons, h.reason)
		}
	}
	if result.Reasons == nil {
		result.Reasons = []string{}
	}
	result.Unrecognised = result.Level == Elevated && !recognised
	return result
}

// matches reports whether the rule applies. plain holds a Bash command's simple commands written
// plainly; a command rule is matched against them as well as the line.
func (r Rule) matches(tool string, fields map[string]any, plain []string) bool {
	if len(r.Tools) > 0 {
		ok := false
		for _, pattern := range r.Tools {
			if prefix, wildcard := strings.CutSuffix(pattern, "*"); wildcard && strings.HasPrefix(tool, prefix) || pattern == tool {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	var texts []string
	switch r.Field {
	case "command":
		if command, ok := fields["command"].(string); ok {
			texts = append([]string{command}, plain...)
		}
	case "path":
		texts = pathsIn(fields)
	case "url":
		if url, ok := fields["url"].(string); ok {
			texts = []string{url}
		}
	default:
		texts = stringsIn(fields)
		if len(texts) == 0 {
			texts = []string{""} // a rule matching "." still matches a call with no strings
		}
	}
	for _, text := range texts {
		if r.re.MatchString(text) {
			return true
		}
	}
	return false
}

// shapeHits keeps a call at low only when it is a known-benign shape: a listed tool, or a Bash
// line read with confidence whose every command is on the list (shape.go). A line too long to
// show whole on a lock screen, or carrying bidi controls that can reorder what is shown, is not
// low either: low is what a notification may allow.
func shapeHits(tool string, fields map[string]any, commands []guard.Command, readable bool, root string) []hit {
	var hits []hit
	switch {
	case tool == "Bash" && !readable:
		hits = append(hits, hit{Elevated, "crowsnest cannot read this command with confidence (an unterminated quote, a shell reading a script or stdin, or deep nesting)", true})
	case tool == "Bash":
		for _, cmd := range commands {
			if reason := commandShape(cmd, root); reason != "" {
				hits = append(hits, hit{Elevated, reason, true})
				break
			}
		}
	case !lowTools[tool]:
		hits = append(hits, hit{Elevated, unlistedTool(tool), true})
	}
	text := SummaryText(fields)
	switch {
	case termtext.HasBidiControl(strings.Join(stringsIn(fields), "")):
		hits = append(hits, hit{Elevated, "Contains bidirectional control characters, which can reorder what is shown", true})
	case len([]rune(text)) > MaxSummaryRunes:
		hits = append(hits, hit{Elevated, "Too long to show whole on a lock screen", true})
	}
	return hits
}

// MaxSummaryRunes is the longest a call's summary may be: a notification shows it whole, so a
// longer one is never low (anything past the cut could be the payload).
const MaxSummaryRunes = 160

// summaryKeys are the input fields a summary is made from, first present wins.
var summaryKeys = []string{"command", "file_path", "notebook_path", "url", "path", "pattern", "query", "description"}

// SummaryText is the field a call's one-line summary shows, whitespace collapsed ("" for none).
func SummaryText(fields map[string]any) string {
	for _, key := range summaryKeys {
		if value, ok := fields[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.Join(strings.Fields(value), " ")
		}
	}
	return ""
}

// writes reports whether a tool writes the files it names.
func writes(tool string) bool {
	switch tool {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return true
	}
	return false
}

func pathsIn(fields map[string]any) []string {
	var out []string
	for _, key := range []string{"file_path", "path", "notebook_path"} {
		if value, ok := fields[key].(string); ok && value != "" {
			out = append(out, value)
		}
	}
	return out
}

// stringsIn collects every string in the input, nested ones too.
func stringsIn(value any) []string {
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch typed := v.(type) {
		case string:
			out = append(out, typed)
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				walk(typed[key])
			}
		case []any:
			for _, item := range typed {
				walk(item)
			}
		}
	}
	walk(value)
	return out
}

// inside reports whether path is root or below it, after cleaning (and resolving the links of
// the longest existing prefix, so a path written through a link to the root still counts).
func inside(root, path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return false // the OS follows a link before the .., which cleaning would hide (R6-C03)
		}
	}
	if !filepath.IsAbs(path) {
		if root == "" {
			return true // relative to the agent's directory, which is not known
		}
		// Relative to the job's directory: resolved like an absolute path, so a link committed
		// inside the root that points outside it does not count as inside (R9-S9).
		path = filepath.Join(root, path)
	}
	clean := resolve(filepath.Clean(path))
	root = resolve(filepath.Clean(root))
	if clean != root && !strings.HasPrefix(clean, root+string(filepath.Separator)) {
		return false
	}
	// R9-S9: the repository's own .git under the root is not "inside": a hook or the config
	// written there runs on the next harmless-looking git command.
	// Compared without case (R10 low): on a case-insensitive volume (APFS, the macOS default)
	// "<root>/.GIT/hooks" is the same directory. On a case-sensitive one this only fails closed.
	rel := strings.TrimPrefix(strings.TrimPrefix(clean, root), string(filepath.Separator))
	first, _, _ := strings.Cut(rel, string(filepath.Separator))
	return !strings.EqualFold(first, ".git")
}

func resolve(path string) string {
	rest := ""
	for current := path; ; current = filepath.Dir(current) {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		rest = filepath.Join(filepath.Base(current), rest)
	}
}
