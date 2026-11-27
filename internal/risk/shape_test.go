package risk

import (
	"strings"
	"testing"
)

// TestRespellingsAndUnknownShapes is R5-C03 and R5-S7: respelling a dangerous command does not
// lower it, and a command that is not a known-benign shape is never low (low is what a lock
// screen may allow). The first block is the reviewers' exact spellings.
func TestRespellingsAndUnknownShapes(t *testing.T) {
	cases := []struct {
		command string
		want    Level
	}{
		// R5-C03, as written in the review.
		{`git -C . reset --hard`, Dangerous},
		{`git -C . clean -fd`, Dangerous},
		{`git -C . push --force origin feature/x`, Dangerous},
		// R5-S7, as written in the review.
		{`python3 -c "import shutil;shutil.rmtree('/Users/x/proj')"`, Elevated},
		{`find . -delete`, Elevated},
		{`base64 -d <<<abc | sh`, Elevated},
		{`perl -e 'unlink glob "~/*"'`, Elevated},
		{`git -c alias.p=push p`, Elevated},
		{`ls ` + strings.Repeat("a", 170) + ` && rm -rf ~`, Dangerous},
		{`echo ` + strings.Repeat("x", 170), Elevated}, // benign, but too long to show whole
		// More respellings.
		{`"git" "reset" --hard`, Dangerous},
		{`git --git-dir=.git --work-tree=. reset --hard HEAD`, Dangerous},
		{`git -Cdir reset --hard`, Dangerous},
		{`FOO=1 git -C . push -f`, Dangerous},
		{`command git push --force-with-lease`, Dangerous},
		{`sh -c 'git reset --hard'`, Dangerous},
		{`bash -lc "rm -rf build"`, Dangerous},
		{`eval "git clean -fdx"`, Dangerous},
		{`env -i PATH=/bin rm -rf x`, Dangerous},
		{`git -c core.pager=evil log`, Elevated},
		{`git -C . push origin feature/x`, Elevated},
		{`ls; python3 x.py`, Elevated},
		{`echo "unterminated`, Elevated},
		{`node -e "require('fs').rmSync('x',{recursive:true})"`, Elevated},
		{`awk 'BEGIN{system("id")}'`, Elevated},
		{`sed -i s/a/b/ x.go`, Elevated},
		{`find . -name x -exec rm {} ;`, Elevated},
		{`xargs rm < list`, Elevated},
		{`git checkout -- .`, Elevated},
		{`git branch -D old`, Elevated},
		{`git config core.hooksPath /tmp/h`, Elevated},
		{`npm run deploy`, Elevated},
		{`mise run release`, Elevated},
		{"ls‮ -la", Elevated},
		// Benign shapes stay low.
		{`go test ./...`, Low},
		{`heavy go test ./internal/risk/`, Low},
		{`git -C sub status`, Low},
		{`git diff --stat && git log --oneline -5`, Low},
		{`git add a.go && git commit -m "wip"`, Low},
		{`git switch -c fix/y`, Low},
		{`git checkout -b fix/y`, Low},
		{`ls -la | grep x | wc -l`, Low},
		{`cd sub && go vet ./...`, Low},
		{`npm run test:unit`, Low},
		{`mise run check`, Low},
		{`find . -name '*.go'`, Low},
		{`sh -c 'go test ./...'`, Low},
	}
	for _, c := range cases {
		got := Default().Classify("Bash", input(t, map[string]any{"command": c.command}), Context{})
		if got.Level != c.want {
			t.Errorf("%q: %s %v, want %s", c.command, got.Level, got.Reasons, c.want)
		}
		if got.Level != Low && len(got.Reasons) == 0 {
			t.Errorf("%q: %s with no reason", c.command, got.Level)
		}
	}
}

// TestUnlistedToolsAreNotLow: a tool not on the list is elevated; listed ones stay low.
func TestUnlistedToolsAreNotLow(t *testing.T) {
	for tool, want := range map[string]Level{"Read": Low, "Grep": Low, "Glob": Low, "WebSearch": Low, "Task": Elevated, "SomethingNew": Elevated} {
		if got := Default().Classify(tool, input(t, map[string]any{"pattern": "x"}), Context{}); got.Level != want {
			t.Errorf("%s: %s, want %s", tool, got.Level, want)
		}
	}
}
