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

// Classify rates one tool call.
func (c *Classifier) Classify(tool string, input json.RawMessage, ctx Context) Result {
	var fields map[string]any
	_ = json.Unmarshal(input, &fields)
	for _, rule := range c.custom {
		if rule.matches(tool, fields) {
			return Result{Level: rule.Level, Reasons: []string{rule.Reason}}
		}
	}
	type hit struct {
		level  Level
		reason string
	}
	var hits []hit
	for _, rule := range c.builtins {
		if !c.disabled[rule.ID] && rule.matches(tool, fields) {
			hits = append(hits, hit{rule.Level, rule.Reason})
		}
	}
	if !c.disabled[CheckProtectedHost] {
		if host := guard.HostNamed(ctx.ProtectedHosts, strings.Join(stringsIn(fields), "\n")); host != "" {
			hits = append(hits, hit{Dangerous, "Names " + host + ", a protected (production) host"})
		}
	}
	paths := pathsIn(fields)
	if !c.disabled[CheckOutsideDir] && ctx.JobRoot != "" && writes(tool) {
		for _, path := range paths {
			if !inside(ctx.JobRoot, path) {
				hits = append(hits, hit{Elevated, "Writes outside the job's directory (" + ctx.JobRoot + ")"})
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
				hits = append(hits, hit{Elevated, "Touches a file that may hold secrets (" + filepath.Base(strings.Trim(path, `"'`)) + ")"})
				break
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].level.rank() > hits[j].level.rank() })
	result := Result{Level: Low}
	seen := map[string]bool{}
	for _, h := range hits {
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
	return result
}

func (r Rule) matches(tool string, fields map[string]any) bool {
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
			texts = []string{command}
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
	if !filepath.IsAbs(path) {
		return true // relative to the agent's directory
	}
	clean := resolve(filepath.Clean(path))
	root = resolve(filepath.Clean(root))
	return clean == root || strings.HasPrefix(clean, root+string(filepath.Separator))
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
