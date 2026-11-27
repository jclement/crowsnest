package guard

import (
	"path/filepath"
	"testing"
)

// TestCheckFollowsTheLine is R5-C02: a write is judged on the branch it actually lands on, after
// the cd and branch switches earlier in the same line, and refused when that cannot be told.
func TestCheckFollowsTheLine(t *testing.T) {
	rules := Rules{Session: "crowsnest", ProtectedBranches: []string{"develop", "staging"}}
	// The hook's directory is on feature/x; /repo-on-develop (and ./dev) are on develop.
	branchOf := func(dir string) string {
		switch filepath.Clean(dir) {
		case "/repo-on-develop", "dev", "/work/dev":
			return "develop"
		}
		return "feature/x"
	}
	cases := []struct {
		name    string
		command string
		rule    string
	}{
		// The reviewer's exact spellings.
		{"switch then commit", "git switch develop && git commit --allow-empty -m checkpoint", RuleProtectedBranch},
		{"cd then commit", "cd /repo-on-develop && git commit -m x", RuleProtectedBranch},
		// More of the same shape.
		{"checkout then commit", "git checkout develop; git commit -am x", RuleProtectedBranch},
		{"checkout then push", "git checkout staging && git push", RuleProtectedBranch},
		{"relative cd then commit", "cd dev && git commit -m x", RuleProtectedBranch},
		{"cd then -C", "cd /work && git -C dev commit -m x", RuleProtectedBranch},
		{"joined -C", "git -Cdev commit -m x", RuleProtectedBranch},
		{"cd to a variable", "cd $REPO && git commit -m x", RuleProtectedBranch},
		{"cd in a subshell", "(cd /elsewhere) && git commit -m x", RuleProtectedBranch},
		{"git-dir option", "git --git-dir=/repo-on-develop/.git commit -m x", RuleProtectedBranch},
		{"work-tree option", "git --work-tree /repo-on-develop commit -m x", RuleProtectedBranch},
		{"GIT_DIR in the environment", "GIT_DIR=/repo-on-develop/.git git commit -m x", RuleProtectedBranch},
		{"inline alias", "git -c alias.ci=commit ci -m x", RuleProtectedBranch},
		{"inside sh -c", `sh -c 'cd /repo-on-develop && git commit -m x'`, RuleProtectedBranch},
		{"inside bash -lc", `bash -lc "git switch develop; git commit -m x"`, RuleProtectedBranch},
		{"inside eval", `eval "git switch develop && git commit -m x"`, RuleProtectedBranch},
		{"quoted program", `"git" switch develop && 'git' commit -m x`, RuleProtectedBranch},
		{"through command and env", "command env git switch develop && exec git commit -m x", RuleProtectedBranch},
		{"pull onto a protected branch", "cd /repo-on-develop && git pull", RuleProtectedBranch},
		// Allowed: the line provably lands elsewhere.
		{"new branch then commit", "git switch -c fix/y && git commit -m x", ""},
		{"checkout -b then commit", "git checkout -b fix/y && git commit -m x", ""},
		{"cd to a feature repo", "cd /work/feature && git commit -m x", ""},
		{"checkout paths only", "git checkout -- a.go && git commit -m x", ""},
		{"plain commit", "git commit -m x", ""},
		{"switch away and back is a feature", "git switch feature/z && git commit -m x", ""},
	}
	for _, c := range cases {
		verdict := Check(rules, c.command, branchOf)
		if verdict.Rule != c.rule {
			t.Errorf("%s: %q gave %q (%s), want %q", c.name, c.command, verdict.Rule, verdict.Reason, c.rule)
		}
	}
}

// TestCheckNeedsNoBranchesForUncertainty: with no protected branches, nothing is refused for
// being hard to follow.
func TestCheckNeedsNoBranchesForUncertainty(t *testing.T) {
	if verdict := Check(Rules{Session: "s"}, "cd $X && git commit -m x", nil); verdict.Deny {
		t.Fatalf("refused with no protected branches: %+v", verdict)
	}
}

// TestParse covers the reader both the guard and the risk classifier use.
func TestParse(t *testing.T) {
	cases := []struct {
		command string
		texts   []string
		ok      bool
	}{
		{`git -C . reset --hard`, []string{"git reset --hard"}, true},
		{`"git" -c core.pager=less "push" --force origin x`, []string{"git push --force origin x"}, true},
		{`FOO=1 sudo -u root env -i BAR=2 rm -rf /tmp/x`, []string{"rm -rf /tmp/x"}, true},
		{`sh -c "cd a && git status"`, []string{"cd a", "git status"}, true},
		{`bash -c 'sh -c "rm -rf x"'`, []string{"rm -rf x"}, true},
		{`echo "unterminated`, nil, false},
		{`base64 -d <<<abc | sh`, nil, false},
		{`(cd x; ls)`, []string{"cd x", "ls"}, true},
		{`heavy go test ./...`, []string{"go test ./..."}, true},
		{`go test ./... 2>&1 | tail -20`, []string{"go test ./... 2>&1", "tail -20"}, true},
		{`make &>log; ls >&2`, []string{"make &>log", "ls >&2"}, true},
		{`sleep 1 & ls`, []string{"sleep 1", "ls"}, true},
	}
	for _, c := range cases {
		commands, ok := Parse(c.command)
		if ok != c.ok {
			t.Errorf("%q: ok %v, want %v", c.command, ok, c.ok)
		}
		if c.texts == nil {
			continue
		}
		var texts []string
		for _, cmd := range commands {
			texts = append(texts, cmd.Text())
		}
		if len(texts) != len(c.texts) {
			t.Errorf("%q: %q, want %q", c.command, texts, c.texts)
			continue
		}
		for i := range texts {
			if texts[i] != c.texts[i] {
				t.Errorf("%q: %q, want %q", c.command, texts, c.texts)
				break
			}
		}
	}
	if commands, _ := Parse(`(cd x; ls) && pwd`); !commands[0].Subshell || commands[2].Subshell {
		t.Fatalf("subshell marks: %+v", commands)
	}
}
