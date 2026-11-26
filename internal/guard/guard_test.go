package guard

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCheck(t *testing.T) {
	rules := Rules{Session: "crowsnest", ProtectedHosts: []string{"nyc1", "server-nyc1.barreleye.app"}, ProtectedBranches: []string{"develop", "staging"}}
	onDevelop := func(string) string { return "develop" }
	onFeature := func(string) string { return "feature/x" }
	cases := []struct {
		name    string
		command string
		branch  BranchOf
		rule    string // "" means allowed
	}{
		{"plain command", "go test ./...", onFeature, ""},
		{"ssh to protected host", "ssh nyc1 uptime", onFeature, RuleProtectedHost},
		{"ssh user@host", "ssh deploy@nyc1", onFeature, RuleProtectedHost},
		{"host as a suffix word", "ssh server-nyc1", onFeature, RuleProtectedHost},
		{"host with domain", "curl https://nyc1.internal/health", onFeature, RuleProtectedHost},
		{"full protected name", "psql -h server-nyc1.barreleye.app", onFeature, RuleProtectedHost},
		{"similar host is not protected", "ssh nyc10", onFeature, ""},
		{"mention in a commit message", `git commit -m "document the nyc1 backup"`, onFeature, ""},
		{"echo mention", "echo nyc1 is production", onFeature, ""},
		{"address outside a network program", "scp-wrapper deploy@nyc1:/tmp/x .", onFeature, RuleProtectedHost},
		{"host:port", "some-tool nyc1:5432", onFeature, RuleProtectedHost},
		{"inside sudo", "sudo -u root ssh nyc1", onFeature, RuleProtectedHost},
		{"after &&", "cd /tmp && curl nyc1", onFeature, RuleProtectedHost},
		{"in a substitution", `echo "$(ssh nyc1 hostname)"`, onFeature, RuleProtectedHost},
		{"case insensitive", "SSH NYC1", onFeature, RuleProtectedHost},
		{"commit on a protected branch", "git commit -am wip", onDevelop, RuleProtectedBranch},
		{"commit on a feature branch", "git commit -am wip", onFeature, ""},
		{"merge on a protected branch", "git -C repo merge feature/x", onDevelop, RuleProtectedBranch},
		{"push to a protected branch", "git push origin develop", onFeature, RuleProtectedBranch},
		{"push HEAD to a protected branch", "git push origin HEAD:refs/heads/staging", onFeature, RuleProtectedBranch},
		{"push current protected branch", "git push", onDevelop, RuleProtectedBranch},
		{"push a feature branch", "git push -u origin feature/x", onFeature, ""},
		{"force push to a protected branch", "git push --force origin develop", onFeature, RuleForcePush},
		{"force push with lease", "git push --force-with-lease origin staging", onFeature, RuleForcePush},
		{"plus refspec", "git push origin +main:develop", onFeature, RuleForcePush},
		{"combined short flags", "git push -uf origin develop", onFeature, RuleForcePush},
		{"force push a feature branch", "git push -f origin feature/x", onFeature, ""},
		{"herdr server stop", "herdr server stop", onFeature, RuleHerdrStop},
		{"herdr server stop with session", "herdr --session crowsnest server stop", onFeature, RuleHerdrStop},
		{"herdr session delete", "herdr session delete crowsnest-test-1", onFeature, RuleHerdrStop},
		{"herdr other session", "herdr --session default pane list", onFeature, RuleOtherSession},
		{"herdr other session equals", "herdr pane list --session=default", onFeature, RuleOtherSession},
		{"herdr own session", "herdr --session crowsnest pane list", onFeature, ""},
		{"herdr default target", "herdr pane list", onFeature, ""},
		{"crowsnest other session", "crowsnest job list --session other", onFeature, RuleOtherSession},
		{"crowsnest own", "crowsnest job update --progress x", onFeature, ""},
		{"env-prefixed", "FOO=1 herdr server stop", onFeature, RuleHerdrStop},
		{"absolute path", "/opt/homebrew/bin/herdr server stop", onFeature, RuleHerdrStop},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			verdict := Check(rules, c.command, c.branch)
			if verdict.Rule != c.rule || verdict.Deny != (c.rule != "") {
				t.Fatalf("Check(%q) = %+v, want rule %q", c.command, verdict, c.rule)
			}
			if verdict.Deny && verdict.Reason == "" {
				t.Fatal("a refusal must carry a reason")
			}
		})
	}
}

func TestNoProtectedBranchesByDefault(t *testing.T) {
	verdict := Check(Rules{Session: "s"}, "git push --force origin main", func(string) string { return "main" })
	if verdict.Deny {
		t.Fatalf("with no protected branches nothing git is refused: %+v", verdict)
	}
}

func TestBranchOfOnlyAskedForGit(t *testing.T) {
	asked := 0
	Check(Rules{ProtectedBranches: []string{"main"}}, "ls -la; go test", func(string) string { asked++; return "main" })
	if asked != 0 {
		t.Fatalf("the branch was looked up %d times for a command with no git", asked)
	}
}

func TestSegments(t *testing.T) {
	got := Segments(`a 'b c' "d \"e\"" f\ g; h | i && j $(k l) ` + "`m`")
	want := [][]string{{"a", "b c", `d "e"`, "f g"}, {"h"}, {"i"}, {"j"}, {"k", "l"}, {"m"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Segments = %q, want %q", got, want)
	}
}

func TestHostsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts")
	if err := os.WriteFile(path, []byte("# comment\nnyc1\n\n  db.example  # trailing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hosts, err := ReadHostsFile(path)
	if err != nil || !reflect.DeepEqual(hosts, []string{"nyc1", "db.example"}) {
		t.Fatalf("ReadHostsFile = %v, %v", hosts, err)
	}
	if hosts, err := ReadHostsFile(filepath.Join(dir, "missing")); err != nil || hosts != nil {
		t.Fatalf("a missing file is no hosts: %v, %v", hosts, err)
	}
}
