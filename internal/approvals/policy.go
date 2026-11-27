// Approval policy (DESIGN.md 8.7, "Policy"): rules the human keeps so a low-risk call is allowed
// by the hook at once, journalled, instead of waiting for a tap. This is the answer to approval
// fatigue that keeps "no permanent grants from a phone": a rule lives in crowsnest, not in the
// agent's Claude settings, is scoped to one project or one live job, and can be removed.
//
// Precedence is fixed: a dangerous call is never allowed by a rule (the risk table wins, always);
// then the job's remembered rules; then the project's rules. A rule applies only when the call's
// risk is at or below its max_risk (low unless the human ticked "also when elevated", or it was
// remembered from an elevated approval). No match: a human is asked, as before.
//
// The rule kinds:
//
//   - bash: a glob over a whole simple command, `*` matching any run of characters, anchored at
//     both ends (`go test *` matches `go test ./...`). A compound command matches only when every
//     simple command in it matches some rule. Command substitution, process substitution, eval
//     or a redirect to a file (other than /dev/null or an fd duplication such as 2>&1) never
//     matches: what would really run cannot be read from the line.
//   - edit: Edit, Write, MultiEdit and NotebookEdit on an absolute path inside <root>/<pattern>,
//     where root is the job's working root (its worktree when isolated) or, for a lead's own
//     calls, the project's directory.
//   - tool: any other tool, by exact name or a trailing-`*` prefix (`mcp__github__*`). A tool
//     rule never covers Bash or the edit tools: those have kinds of their own that look inside
//     the call.

package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/guard"
	"github.com/jclement/crowsnest/internal/risk"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/termtext"
)

// Rule kinds, scopes and the decided_via of an approval a rule allowed.
const (
	KindBash = "bash"
	KindEdit = "edit"
	KindTool = "tool"

	ScopeProject = "project"
	ScopeJob     = "job"

	ViaPolicy = "policy"
	ViaDemo   = "demo"
)

// maxPattern caps a rule's pattern; maxRuleNote its note.
const (
	maxPattern  = 300
	maxRuleNote = 500
)

// ErrRuleNotFound: no such rule, or it was already removed.
var ErrRuleNotFound = errors.New("no such rule (it may have been removed already)")

// RuleError is a rule the human cannot add, with a sentence saying what is wrong.
type RuleError struct{ Message string }

func (e *RuleError) Error() string { return e.Message }

func ruleErrorf(format string, args ...any) error {
	return &RuleError{Message: fmt.Sprintf(format, args...)}
}

// Rule is one policy rule, as the API shows it.
type Rule struct {
	ID         string     `json:"id"` // r_N
	Scope      string     `json:"scope"`
	ProjectID  int64      `json:"project_id,omitempty"`
	JobID      string     `json:"job_id,omitempty"`
	Kind       string     `json:"kind"`
	Pattern    string     `json:"pattern"`
	MaxRisk    risk.Level `json:"max_risk"`
	Note       string     `json:"note,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	CreatedVia string     `json:"created_via"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	// Sentence is the rule in words: "Allow Bash commands matching `go test *`".
	Sentence string `json:"sentence"`
}

// RuleInput is a new rule.
type RuleInput struct {
	Kind    string     `json:"kind"`
	Pattern string     `json:"pattern"`
	MaxRisk risk.Level `json:"max_risk"`
	Note    string     `json:"note"`
	// Via is browser (default) or demo.
	Via string `json:"-"`
}

// Scope is what the policy needs to know about where a call comes from: the project whose rules
// apply (0: none), whether the job is still live (a job rule applies only then), and the working
// root edit rules are relative to ("" when unknown: no edit rule applies).
type Scope struct {
	ProjectID   int64
	ProjectName string
	JobLive     bool
	Root        string
}

// Suggestion is what "Allow and remember for this job" proposes: the rule kind the call's tool
// takes and a pattern the human may edit.
type Suggestion struct {
	Kind    string `json:"kind"`
	Pattern string `json:"pattern"`
	// Exact is true for an elevated call (R6-S1): the rule is the call itself, word for word, and
	// the card shows it to confirm, not to widen.
	Exact bool `json:"exact,omitempty"`
}

// FormatRuleID renders a rule id.
func FormatRuleID(id int64) string { return "r_" + strconv.FormatInt(id, 10) }

// ParseRuleID parses r_N.
func ParseRuleID(id string) (int64, error) {
	number, ok := strings.CutPrefix(id, "r_")
	if !ok {
		return 0, ErrRuleNotFound
	}
	value, err := strconv.ParseInt(number, 10, 64)
	if err != nil || value <= 0 {
		return 0, ErrRuleNotFound
	}
	return value, nil
}

// isEditTool reports whether a tool is one the edit kind covers.
func isEditTool(tool string) bool {
	return tool == "Edit" || tool == "Write" || tool == "MultiEdit" || tool == "NotebookEdit"
}

// KindFor is the rule kind a tool's calls are matched by.
func KindFor(tool string) string {
	switch {
	case tool == "Bash":
		return KindBash
	case isEditTool(tool):
		return KindEdit
	}
	return KindTool
}

var toolPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+\*?$`)

// NormalizeRule checks a rule and returns its pattern in canonical form, or a RuleError saying in
// a sentence what is wrong.
func NormalizeRule(kind, pattern string, maxRisk risk.Level) (string, error) {
	pattern = strings.TrimSpace(pattern)
	if maxRisk != risk.Low && maxRisk != risk.Elevated {
		return "", ruleErrorf("max_risk must be low or elevated; a rule never allows a dangerous call.")
	}
	if pattern == "" {
		return "", ruleErrorf("A pattern is required.")
	}
	if len(pattern) > maxPattern {
		return "", ruleErrorf("The pattern is too long (at most %d characters).", maxPattern)
	}
	switch kind {
	case KindBash:
		if strings.ContainsAny(pattern, "\n\r") {
			return "", ruleErrorf("A command pattern is one line.")
		}
		if strings.Trim(pattern, "* \t") == "" {
			return "", ruleErrorf("A command pattern must name a command (`go test *`); `*` alone would allow every command.")
		}
		return pattern, nil
	case KindEdit:
		if filepath.IsAbs(pattern) || strings.HasPrefix(pattern, "~") {
			return "", ruleErrorf("An edit pattern is a directory relative to the job's working root (`internal/`, or `.` for all of it), not an absolute path.")
		}
		cleaned := filepath.Clean(pattern)
		if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			return "", ruleErrorf("An edit pattern cannot leave the working root (no `..`).")
		}
		if strings.Contains(cleaned, "*") {
			return "", ruleErrorf("An edit pattern is a directory (`internal/`), not a glob.")
		}
		return cleaned, nil
	case KindTool:
		if !toolPattern.MatchString(pattern) || pattern == "*" {
			return "", ruleErrorf("A tool pattern is a tool's name (`WebSearch`) or a prefix ending in `*` (`mcp__github__*`).")
		}
		if pattern == "Bash" || isEditTool(pattern) {
			return "", ruleErrorf("Use a bash or edit rule for %s: those look at the command or the file, a tool rule would allow every call.", pattern)
		}
		return pattern, nil
	}
	return "", ruleErrorf("The kind must be bash, edit or tool.")
}

// describe is a rule without its verb: "Bash commands matching `go test *`".
func describe(kind, pattern string) string {
	switch kind {
	case KindBash:
		return "Bash commands matching `" + pattern + "`"
	case KindEdit:
		if pattern == "." {
			return "edits anywhere in the working directory"
		}
		return "edits under `" + strings.TrimSuffix(pattern, "/") + "/`"
	default:
		if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
			return "tools starting with `" + prefix + "`"
		}
		return "the tool `" + pattern + "`"
	}
}

func toRule(r store.ApprovalRuleRecord) Rule {
	rule := Rule{
		ID: FormatRuleID(r.ID), Scope: r.Scope, ProjectID: r.ProjectID, JobID: r.JobID, Kind: r.Kind, Pattern: r.Pattern,
		MaxRisk: risk.Level(r.MaxRisk), Note: r.Note, CreatedAt: r.CreatedAt, CreatedVia: r.CreatedVia,
		Sentence: "Allow " + describe(r.Kind, r.Pattern),
	}
	if !r.RevokedAt.IsZero() {
		revoked := r.RevokedAt
		rule.RevokedAt = &revoked
	}
	return rule
}

// ---- Matching --------------------------------------------------------------------------------

// call is what a rule is matched against.
type call struct {
	tool  string
	input json.RawMessage
	level risk.Level
	root  string
}

// evaluate returns the rules that allow a call, in the order they were used, or nil when none
// does. rules come in precedence order (the job's, then the project's) and are all live.
func evaluate(rules []Rule, c call) []Rule {
	if c.level == risk.Dangerous {
		return nil
	}
	var usable []Rule
	for _, rule := range rules {
		if rule.Kind == KindFor(c.tool) && c.level.AtMost(rule.MaxRisk) && rule.MaxRisk != risk.Dangerous {
			usable = append(usable, rule)
		}
	}
	if len(usable) == 0 {
		return nil
	}
	var fields map[string]any
	_ = json.Unmarshal(c.input, &fields)
	switch KindFor(c.tool) {
	case KindBash:
		command, _ := fields["command"].(string)
		return matchBash(usable, command)
	case KindEdit:
		path, _ := fields["file_path"].(string)
		if path == "" {
			path, _ = fields["notebook_path"].(string)
		}
		for _, rule := range usable {
			if insideRule(c.root, rule.Pattern, path) {
				return []Rule{rule}
			}
		}
	default:
		for _, rule := range usable {
			if toolMatches(rule.Pattern, c.tool) {
				return []Rule{rule}
			}
		}
	}
	return nil
}

// matchBash: every simple command must match some rule; the rules used, deduplicated.
func matchBash(rules []Rule, command string) []Rule {
	commands, ok := SimpleCommands(command)
	if !ok {
		return nil
	}
	var used []Rule
	seen := map[string]bool{}
	for _, simple := range commands {
		matched := false
		for _, rule := range rules {
			if Glob(rule.Pattern, simple) {
				if !seen[rule.ID] {
					seen[rule.ID] = true
					used = append(used, rule)
				}
				matched = true
				break
			}
		}
		if !matched {
			return nil
		}
	}
	return used
}

// SimpleCommands splits a command line into its simple commands, each its words joined by one
// space, for matching against bash rules. ok is false when the line cannot be matched at all:
// empty, bidi controls, command or process substitution, eval, or a redirect to a file.
func SimpleCommands(command string) ([]string, bool) {
	if termtext.HasBidiControl(command) {
		return nil, false // what is shown is not what runs
	}
	for _, unreadable := range []string{"$(", "`", "<(", ">("} {
		if strings.Contains(command, unreadable) {
			return nil, false
		}
	}
	cleaned, ok := stripHarmlessRedirects(command)
	if !ok {
		return nil, false
	}
	var out []string
	for _, words := range guard.Segments(cleaned) {
		for _, word := range words {
			if word == "eval" {
				return nil, false
			}
		}
		out = append(out, strings.Join(words, " "))
	}
	return out, len(out) > 0
}

// stripHarmlessRedirects blanks the redirects that write no file (an fd duplication such as 2>&1
// or >&2, and anything sent to /dev/null) and reports false at any other `>` outside quotes. The
// splitter would otherwise cut 2>&1 at its `&`.
func stripHarmlessRedirects(command string) (string, bool) {
	runes := []rune(command)
	var quote rune
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else if r == '\\' && quote == '"' {
				i++
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '\\':
			i++
		case r == '>':
			end, ok := harmlessRedirectEnd(runes, i)
			if !ok {
				return "", false
			}
			start := i
			if i > 0 && (runes[i-1] == '&' || isDigit(runes[i-1])) && (i == 1 || isSpace(runes[i-2])) {
				start = i - 1
			}
			for j := start; j < end; j++ {
				runes[j] = ' '
			}
			i = end - 1
		}
	}
	return string(runes), true
}

// harmlessRedirectEnd reads the redirect whose `>` is at i and returns where it ends, when it is
// an fd duplication or goes to /dev/null.
func harmlessRedirectEnd(runes []rune, i int) (int, bool) {
	k := i + 1
	if k < len(runes) && runes[k] == '>' {
		k++
	}
	if k < len(runes) && runes[k] == '&' {
		k++
		digits := k
		for k < len(runes) && (isDigit(runes[k]) || runes[k] == '-') {
			k++
		}
		return k, k > digits && (k == len(runes) || boundary(runes[k]))
	}
	for k < len(runes) && isSpace(runes[k]) {
		k++
	}
	const null = "/dev/null"
	if !strings.HasPrefix(string(runes[k:]), null) {
		return 0, false
	}
	k += len(null)
	return k, k == len(runes) || boundary(runes[k])
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }
func isSpace(r rune) bool { return r == ' ' || r == '\t' }
func boundary(r rune) bool {
	return isSpace(r) || strings.ContainsRune(";&|)\n", r)
}

// Glob matches text against a pattern where `*` matches any run of characters (spaces included)
// and everything else matches itself; it is anchored at both ends.
func Glob(pattern, text string) bool {
	p, t := []rune(pattern), []rune(text)
	i, j, star, mark := 0, 0, -1, 0
	for j < len(t) {
		switch {
		case i < len(p) && p[i] == '*':
			star, mark = i, j
			i++
		case i < len(p) && p[i] == t[j]:
			i++
			j++
		case star >= 0:
			// Let the last star swallow one more character and try again from there.
			mark++
			i, j = star+1, mark
		default:
			return false
		}
	}
	for i < len(p) && p[i] == '*' {
		i++
	}
	return i == len(p)
}

// insideRule reports whether path, absolute, lies inside root/pattern after cleaning.
func insideRule(root, pattern, path string) bool {
	if root == "" || !filepath.IsAbs(root) || path == "" || !filepath.IsAbs(path) {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return false // the OS walks a link before the .., so cleaning it away would lie (R6-C03)
		}
	}
	base := filepath.Join(root, pattern)
	if !within(base, filepath.Clean(path)) {
		return false
	}
	// The same comparison on real paths (R6-C03, R6-I4): a link inside the allowed directory that
	// points elsewhere does not carry the rule with it. A base that cannot be resolved, or a path
	// whose existing part cannot, matches nothing.
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return false
	}
	realPath, ok := resolveExisting(filepath.Clean(path))
	return ok && within(realBase, realPath)
}

// within reports whether path is base or below it (both clean and absolute).
func within(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel)
}

// resolveExisting resolves the links of path's longest existing prefix and joins the rest, which
// must not climb (..). ok is false when nothing of it resolves.
func resolveExisting(path string) (string, bool) {
	rest := ""
	for current := path; ; current = filepath.Dir(current) {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			if strings.Contains(rest, "..") {
				return "", false
			}
			return filepath.Join(resolved, rest), true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		rest = filepath.Join(filepath.Base(current), rest)
	}
}

// toolMatches: exact, or a trailing-* prefix; never Bash or an edit tool.
func toolMatches(pattern, tool string) bool {
	if tool == "Bash" || isEditTool(tool) {
		return false
	}
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(tool, prefix)
	}
	return pattern == tool
}

// Suggest is the rule "Allow and remember for this job" proposes for a call: for Bash the first
// two words of its first command plus ` *` (the one word alone when there is only one); for an
// edit the file's directory relative to root; otherwise the tool's name.
func Suggest(tool string, input json.RawMessage, root string) Suggestion {
	var fields map[string]any
	_ = json.Unmarshal(input, &fields)
	kind := KindFor(tool)
	switch kind {
	case KindBash:
		command, _ := fields["command"].(string)
		segments := guard.Segments(command)
		if len(segments) == 0 {
			return Suggestion{Kind: kind}
		}
		words := segments[0]
		if len(words) == 1 {
			return Suggestion{Kind: kind, Pattern: words[0]}
		}
		return Suggestion{Kind: kind, Pattern: words[0] + " " + words[1] + " *"}
	case KindEdit:
		path, _ := fields["file_path"].(string)
		if path == "" {
			path, _ = fields["notebook_path"].(string)
		}
		if root != "" && filepath.IsAbs(path) {
			if rel, err := filepath.Rel(root, filepath.Dir(filepath.Clean(path))); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
				if rel == "." {
					return Suggestion{Kind: kind, Pattern: "."}
				}
				return Suggestion{Kind: kind, Pattern: rel + "/"}
			}
		}
		return Suggestion{Kind: kind}
	}
	return Suggestion{Kind: kind, Pattern: tool}
}

// ---- The service's side ----------------------------------------------------------------------

// scopeOf asks the wiring where a call comes from (nothing known without a Scope option).
func (s *Service) scopeOf(ctx context.Context, jobID, paneID string) Scope {
	if s.opts.Scope == nil {
		return Scope{}
	}
	return s.opts.Scope(ctx, jobID, paneID)
}

// liveRules are the rules that may apply to a call, in precedence order: the job's own (only
// while it is live), then its project's.
func (s *Service) liveRules(ctx context.Context, jobID string, scope Scope) []Rule {
	var out []Rule
	if jobID != "" && scope.JobLive {
		records, err := s.opts.Store.ListApprovalRules(ctx, store.ApprovalRuleFilter{Scope: ScopeJob, JobID: jobID, Live: true})
		if err != nil {
			s.opts.Logger.Warn("reading a job's approval rules", "job", jobID, "err", err)
		}
		for _, record := range records {
			out = append(out, toRule(record))
		}
	}
	if scope.ProjectID != 0 {
		records, err := s.opts.Store.ListApprovalRules(ctx, store.ApprovalRuleFilter{Scope: ScopeProject, ProjectID: scope.ProjectID, Live: true})
		if err != nil {
			s.opts.Logger.Warn("reading a project's approval rules", "project", scope.ProjectID, "err", err)
		}
		for _, record := range records {
			out = append(out, toRule(record))
		}
	}
	return out
}

// policyVerdict is a policy allow: the reason sentence and the rule ids, as stored.
type policyVerdict struct {
	reason  string
	ruleIDs string
}

// checkPolicy evaluates a call against the rules that apply to it; ok is false when a human must
// be asked.
func (s *Service) checkPolicy(ctx context.Context, jobID, paneID, tool string, input json.RawMessage, level risk.Level) (policyVerdict, bool) {
	if level == risk.Dangerous {
		return policyVerdict{}, false
	}
	scope := s.scopeOf(ctx, jobID, paneID)
	used := evaluate(s.liveRules(ctx, jobID, scope), call{tool: tool, input: input, level: level, root: scope.Root})
	if len(used) == 0 {
		return policyVerdict{}, false
	}
	parts := make([]string, 0, len(used))
	ids := make([]string, 0, len(used))
	for _, rule := range used {
		where := "job " + rule.JobID
		if rule.Scope == ScopeProject {
			where = "project " + firstNonBlank(scope.ProjectName, strconv.FormatInt(rule.ProjectID, 10))
		}
		parts = append(parts, fmt.Sprintf("rule %s (%s): %s", rule.ID, where, describe(rule.Kind, rule.Pattern)))
		ids = append(ids, strings.TrimPrefix(rule.ID, "r_"))
	}
	return policyVerdict{reason: "Allowed by " + strings.Join(parts, "; "), ruleIDs: strings.Join(ids, ",")}, true
}

// AddProjectRule stores a project rule the human added.
func (s *Service) AddProjectRule(ctx context.Context, projectID int64, in RuleInput) (Rule, error) {
	if projectID <= 0 {
		return Rule{}, ruleErrorf("A project rule needs a project.")
	}
	return s.addRule(ctx, store.ApprovalRuleRecord{Scope: ScopeProject, ProjectID: projectID}, in)
}

func (s *Service) addRule(ctx context.Context, record store.ApprovalRuleRecord, in RuleInput) (Rule, error) {
	if in.MaxRisk == "" {
		in.MaxRisk = risk.Low
	}
	pattern, err := NormalizeRule(in.Kind, in.Pattern, in.MaxRisk)
	if err != nil {
		return Rule{}, err
	}
	note := strings.TrimSpace(in.Note)
	if len(note) > maxRuleNote {
		return Rule{}, ruleErrorf("The note is too long (at most %d characters).", maxRuleNote)
	}
	via := in.Via
	if via == "" {
		via = ViaBrowser
	}
	record.Kind, record.Pattern, record.MaxRisk, record.Note, record.CreatedVia, record.CreatedAt = in.Kind, pattern, string(in.MaxRisk), note, via, s.opts.Now()
	id, err := s.opts.Store.InsertApprovalRule(ctx, record)
	if err != nil {
		return Rule{}, err
	}
	stored, err := s.opts.Store.GetApprovalRule(ctx, id)
	if err != nil {
		return Rule{}, err
	}
	return toRule(stored), nil
}

// RuleFilter selects rules to list.
type RuleFilter struct {
	ProjectID int64
	JobID     string
	// All includes revoked rules.
	All bool
}

// ListRules lists rules, oldest first.
func (s *Service) ListRules(ctx context.Context, filter RuleFilter) ([]Rule, error) {
	query := store.ApprovalRuleFilter{ProjectID: filter.ProjectID, JobID: filter.JobID, Live: !filter.All}
	switch {
	case filter.JobID != "":
		query.Scope = ScopeJob
	case filter.ProjectID != 0:
		query.Scope = ScopeProject
	}
	records, err := s.opts.Store.ListApprovalRules(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]Rule, 0, len(records))
	for _, record := range records {
		out = append(out, toRule(record))
	}
	return out, nil
}

// GetRule returns one rule.
func (s *Service) GetRule(ctx context.Context, id string) (Rule, error) {
	numeric, err := ParseRuleID(id)
	if err != nil {
		return Rule{}, err
	}
	record, err := s.opts.Store.GetApprovalRule(ctx, numeric)
	if errors.Is(err, store.ErrNotFound) {
		return Rule{}, ErrRuleNotFound
	}
	if err != nil {
		return Rule{}, err
	}
	return toRule(record), nil
}

// RevokeRule removes a rule from force (it stays listed with ?all, so the approvals it allowed
// still name it).
func (s *Service) RevokeRule(ctx context.Context, id string) (Rule, error) {
	numeric, err := ParseRuleID(id)
	if err != nil {
		return Rule{}, err
	}
	if err := s.opts.Store.RevokeApprovalRule(ctx, numeric, s.opts.Now()); err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrNotPending) {
			return Rule{}, ErrRuleNotFound
		}
		return Rule{}, err
	}
	return s.GetRule(ctx, id)
}

// settleByPolicy allows every pending approval of a job the rules now allow (after a rule was
// remembered for it), as the policy would have at request time; it returns how many.
func (s *Service) settleByPolicy(ctx context.Context, jobID string) int {
	count := 0
	for _, approval := range s.pending(ctx) {
		if approval.JobID != jobID {
			continue
		}
		verdict, ok := s.checkPolicy(ctx, approval.JobID, approval.PaneID, approval.ToolName, approval.ToolInput, approval.Risk)
		if !ok {
			continue
		}
		if _, err := s.settle(ctx, mustParse(approval.ID), StatusAllowed, ViaPolicy, "", verdict.reason, verdict.ruleIDs); err == nil {
			count++
		}
	}
	return count
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// ---- Preview: what a rule would have matched -------------------------------------------------

// Preview window: the project's approvals of the last week, at most this many, newest first.
const (
	previewWindow = 7 * 24 * time.Hour
	previewLimit  = 50
)

// PreviewMatch is one recent approval a rule's pattern matches.
type PreviewMatch struct {
	ApprovalID string     `json:"approval_id"`
	Summary    string     `json:"summary"`
	Risk       risk.Level `json:"risk"`
	Status     Status     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Preview is what a rule would have done to the project's recent approvals (R5: a rule is shown
// with its effect before it is saved, and beside it once listed). Allowed would have been
// allowed; AboveMaxRisk match the pattern but are riskier than the rule's max_risk (a human is
// still asked); Dangerous match the pattern and are never auto-allowed.
type Preview struct {
	Considered   int            `json:"considered"`
	Allowed      []PreviewMatch `json:"allowed"`
	AboveMaxRisk []PreviewMatch `json:"above_max_risk"`
	Dangerous    []PreviewMatch `json:"dangerous"`
}

// PreviewRule matches a rule (not yet saved, or listed) against the project's recent approvals,
// settled or pending. Each call is matched on its full tool input, never on its summary; a
// compound command counts only when this one rule matches every part of it.
func (s *Service) PreviewRule(ctx context.Context, projectID int64, kind, pattern string, maxRisk risk.Level) (Preview, error) {
	if maxRisk == "" {
		maxRisk = risk.Low
	}
	pattern, err := NormalizeRule(kind, pattern, maxRisk)
	if err != nil {
		return Preview{}, err
	}
	// Matched as if every call were low, so the pattern alone decides; risk is sorted after.
	probe := []Rule{{ID: "preview", Kind: kind, Pattern: pattern, MaxRisk: risk.Elevated}}
	preview := Preview{Allowed: []PreviewMatch{}, AboveMaxRisk: []PreviewMatch{}, Dangerous: []PreviewMatch{}}
	for _, approval := range s.recentInProject(ctx, projectID) {
		preview.Considered++
		if evaluate(probe, call{tool: approval.ToolName, input: approval.ToolInput, level: risk.Low, root: approval.root}) == nil {
			continue
		}
		match := PreviewMatch{ApprovalID: approval.ID, Summary: approval.Summary, Risk: approval.Risk, Status: approval.Status, CreatedAt: approval.CreatedAt}
		switch {
		case approval.Risk == risk.Dangerous:
			preview.Dangerous = append(preview.Dangerous, match)
		case !approval.Risk.AtMost(maxRisk):
			preview.AboveMaxRisk = append(preview.AboveMaxRisk, match)
		default:
			preview.Allowed = append(preview.Allowed, match)
		}
	}
	return preview, nil
}

// rootedApproval is an approval with the working root its call was made in.
type rootedApproval struct {
	Approval
	root string
}

// recentInProject is the project's approvals of the preview window, newest first, at most
// previewLimit, each with its root (the scope is asked once per job or pane).
func (s *Service) recentInProject(ctx context.Context, projectID int64) []rootedApproval {
	if projectID == 0 {
		return nil
	}
	records, err := s.opts.Store.ListApprovals(ctx, nil, 500)
	if err != nil {
		s.opts.Logger.Warn("listing approvals for a rule preview", "err", err)
		return nil
	}
	since := s.opts.Now().Add(-previewWindow)
	scopes := map[string]Scope{}
	var out []rootedApproval
	for _, record := range records {
		if record.CreatedAt.Before(since) || len(out) >= previewLimit {
			break
		}
		key := record.JobID + "|" + record.PaneID
		if record.JobID != "" {
			key = record.JobID
		}
		scope, ok := scopes[key]
		if !ok {
			scope = s.scopeOf(ctx, record.JobID, record.PaneID)
			scopes[key] = scope
		}
		if scope.ProjectID == projectID {
			out = append(out, rootedApproval{Approval: toApproval(record), root: scope.Root})
		}
	}
	return out
}
