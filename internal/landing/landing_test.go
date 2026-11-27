package landing

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repo is a real git repository in a temp dir with a worktree on a job branch, as herdr's
// worktree.create leaves one.
type repo struct {
	t        *testing.T
	main     string
	worktree string
	branch   string
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...) //nolint:gosec // test fixture commands
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// No global config (no signing, no hooks) for the commands under test either.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	root := t.TempDir()
	r := &repo{t: t, main: filepath.Join(root, "main"), worktree: filepath.Join(root, "wt"), branch: "job/1-widget"}
	if err := os.MkdirAll(r.main, 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	run(t, r.main, "git", "init", "-q", "-b", "main")
	write(t, r.main, "README.md", "hello\n")
	write(t, r.main, "server.go", "package main\n")
	run(t, r.main, "git", "add", ".")
	run(t, r.main, "git", "commit", "-q", "-m", "first")
	run(t, r.main, "git", "worktree", "add", "-q", "-b", r.branch, r.worktree)
	return r
}

func (r *repo) commitInWorktree(name, body, message string) {
	r.t.Helper()
	write(r.t, r.worktree, name, body)
	run(r.t, r.worktree, "git", "add", ".")
	run(r.t, r.worktree, "git", "commit", "-q", "-m", message)
}

func TestInspect(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	base := run(t, r.main, "git", "rev-parse", "HEAD")
	r.commitInWorktree("server.go", "package main\n\nfunc main() {}\n", "add main")
	write(t, r.worktree, "logo.png", "\x89PNG\r\n\x1a\n\x00\x00binary")
	run(t, r.worktree, "git", "add", ".")
	run(t, r.worktree, "git", "mv", "README.md", "docs.md")
	run(t, r.worktree, "git", "commit", "-q", "-m", "logo and rename")
	produced, err := Inspect(ctx, r.worktree, "main", base)
	if err != nil {
		t.Fatal(err)
	}
	if produced.Branch != r.branch || produced.Ahead != 2 || produced.Behind != 0 || len(produced.Commits) != 2 || !produced.Clean {
		t.Fatalf("produced %+v", produced)
	}
	byPath := map[string]FileChange{}
	for _, file := range produced.Files {
		byPath[file.Path] = file
	}
	if f := byPath["server.go"]; f.Status != "modified" || f.Added != 2 || f.Group != "server" {
		t.Fatalf("server.go %+v", f)
	}
	if f := byPath["logo.png"]; !f.Binary || f.Status != "added" || !f.Churn {
		t.Fatalf("logo.png %+v", f)
	}
	if f := byPath["docs.md"]; f.Status != "renamed" || f.OldPath != "README.md" {
		t.Fatalf("rename %+v", f)
	}
	diff, binary, _, err := FileDiff(ctx, r.worktree, produced.DiffBase, "server.go", "")
	if err != nil || binary || !strings.Contains(diff, "+func main() {}") {
		t.Fatalf("diff %q %v %v", diff, binary, err)
	}
	if _, binary, _, _ := FileDiff(ctx, r.worktree, produced.DiffBase, "logo.png", ""); !binary {
		t.Fatal("a binary file must be named binary, not shown")
	}
	write(t, r.worktree, "scratch.txt", "left over")
	if produced, _ := Inspect(ctx, r.worktree, "main", base); produced.Clean || len(produced.Dirty) != 1 {
		t.Fatalf("dirty tree: %+v", produced)
	}
}

func TestMergeFastForwardAndMergeCommit(t *testing.T) {
	for _, mode := range []string{ModeFF, ModeMerge} {
		t.Run(mode, func(t *testing.T) {
			r := newRepo(t)
			r.commitInWorktree("feature.go", "package main\n", "feature")
			result, err := Merge(context.Background(), MergeInput{MainDir: r.main, WorkDir: r.worktree, Branch: r.branch, Base: "main", Mode: mode, Message: "Merge job j_1"})
			if err != nil {
				t.Fatal(err)
			}
			if result.FastForward != (mode == ModeFF) {
				t.Fatalf("fast-forward %v for %s", result.FastForward, mode)
			}
			if _, err := os.Stat(filepath.Join(r.main, "feature.go")); err != nil {
				t.Fatal("the merged file is not in the main checkout")
			}
			if mode == ModeMerge && !strings.Contains(run(t, r.main, "git", "log", "-1", "--format=%s"), "Merge job j_1") {
				t.Fatal("no merge commit with the message")
			}
		})
	}
}

func TestMergeFFRefusesWhenBaseMovedOn(t *testing.T) {
	r := newRepo(t)
	r.commitInWorktree("feature.go", "package main\n", "feature")
	write(t, r.main, "other.go", "package main\n")
	run(t, r.main, "git", "add", ".")
	run(t, r.main, "git", "commit", "-q", "-m", "base moved")
	before := run(t, r.main, "git", "rev-parse", "HEAD")
	_, err := Merge(context.Background(), MergeInput{MainDir: r.main, WorkDir: r.worktree, Branch: r.branch, Base: "main", Mode: ModeFF})
	if !IsRefusal(err) || !strings.Contains(err.Error(), "not a fast-forward") {
		t.Fatalf("err %v", err)
	}
	if run(t, r.main, "git", "rev-parse", "HEAD") != before {
		t.Fatal("the base moved")
	}
	// A merge commit works where a fast-forward cannot.
	if _, err := Merge(context.Background(), MergeInput{MainDir: r.main, WorkDir: r.worktree, Branch: r.branch, Base: "main", Mode: ModeMerge}); err != nil {
		t.Fatal(err)
	}
}

func TestMergeRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(r *repo)
		input func(r *repo) MergeInput
		want  string
	}{
		{"conflict is aborted", func(r *repo) {
			r.commitInWorktree("server.go", "package main // job\n", "job change")
			write(r.t, r.main, "server.go", "package main // main\n")
			run(r.t, r.main, "git", "commit", "-q", "-am", "main change")
		}, nil, "conflicts in server.go"},
		{"dirty main checkout", func(r *repo) {
			r.commitInWorktree("feature.go", "x", "f")
			write(r.t, r.main, "server.go", "edited but not committed\n")
		}, nil, "uncommitted changes (server.go)"},
		{"dirty job worktree", func(r *repo) {
			r.commitInWorktree("feature.go", "x", "f")
			write(r.t, r.worktree, "half.go", "unfinished")
		}, nil, "uncommitted work"},
		{"protected base", func(r *repo) { r.commitInWorktree("feature.go", "x", "f") },
			func(r *repo) MergeInput {
				return MergeInput{MainDir: r.main, WorkDir: r.worktree, Branch: r.branch, Base: "main", Protected: []string{"main"}}
			}, "protected branch"},
		{"main checkout on another branch", func(r *repo) {
			r.commitInWorktree("feature.go", "x", "f")
			run(r.t, r.main, "git", "switch", "-q", "-c", "elsewhere")
		}, nil, "is on elsewhere, not main"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t)
			tc.setup(r)
			input := MergeInput{MainDir: r.main, WorkDir: r.worktree, Branch: r.branch, Base: "main", Mode: ModeMerge}
			if tc.input != nil {
				input = tc.input(r)
			}
			before := run(t, r.main, "git", "rev-parse", "HEAD")
			_, err := Merge(context.Background(), input)
			if !IsRefusal(err) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want a refusal with %q", err, tc.want)
			}
			if run(t, r.main, "git", "rev-parse", "HEAD") != before {
				t.Fatal("a refused merge moved the base")
			}
			if _, err := os.Stat(filepath.Join(r.main, ".git", "MERGE_HEAD")); err == nil {
				t.Fatal("a merge was left in progress")
			}
		})
	}
}

// fakeGH writes a gh stand-in that is signed in and records its arguments.
func fakeGH(t *testing.T, signedIn bool) (path, argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	status := "0"
	if !signedIn {
		status = "1"
	}
	script := "#!/bin/sh\nif [ \"$1\" = auth ]; then exit " + status + "; fi\n" +
		"printf '%s\\n' \"$@\" > " + argsFile + "\necho 'Creating pull request'\necho https://github.com/owner/repo/pull/7\n"
	path = filepath.Join(dir, "gh")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // a test script
		t.Fatal(err)
	}
	return path, argsFile
}

func TestPushAndPullRequest(t *testing.T) {
	r := newRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	run(t, r.main, "git", "init", "-q", "--bare", remote)
	// A GitHub-looking remote name that is really the local bare repository.
	run(t, r.main, "git", "remote", "add", "origin", remote)
	run(t, r.main, "git", "config", "url."+remote+".insteadOf", "https://github.com/owner/repo.git")
	run(t, r.main, "git", "remote", "set-url", "origin", "https://github.com/owner/repo.git")
	r.commitInWorktree("feature.go", "x", "feature")
	gh, argsFile := fakeGH(t, true)
	result, err := Push(context.Background(), PushInput{WorkDir: r.worktree, Branch: r.branch, Base: "main", Title: "Add a widget", Body: "## Walkthrough\n", GH: gh})
	if err != nil {
		t.Fatal(err)
	}
	if result.PRURL != "https://github.com/owner/repo/pull/7" || result.Remote != "origin" {
		t.Fatalf("result %+v", result)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "--base\nmain") || !strings.Contains(string(args), "--title\nAdd a widget") || strings.Contains(string(args), "reviewer") {
		t.Fatalf("gh args %q", args)
	}
	if out := run(t, remote, "git", "branch", "--list", r.branch); !strings.Contains(out, r.branch) {
		t.Fatal("the branch did not reach the remote")
	}
	if produced, _ := Inspect(context.Background(), r.worktree, "main", ""); !produced.Pushed {
		t.Fatalf("not reported pushed: %+v", produced)
	}
}

func TestPushWithoutGitHubOrSignedOutGH(t *testing.T) {
	r := newRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	run(t, r.main, "git", "init", "-q", "--bare", remote)
	run(t, r.main, "git", "remote", "add", "origin", remote)
	r.commitInWorktree("feature.go", "x", "feature")
	result, err := Push(context.Background(), PushInput{WorkDir: r.worktree, Branch: r.branch, Base: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if result.PRURL != "" || !strings.Contains(result.Note, "not GitHub") {
		t.Fatalf("a non-GitHub remote: %+v", result)
	}
	if _, err := Push(context.Background(), PushInput{WorkDir: r.worktree, Branch: "main", Protected: []string{"main"}}); !IsRefusal(err) {
		t.Fatalf("pushing a protected branch: %v", err)
	}
	gh, _ := fakeGH(t, false)
	if path, why := findGH(context.Background(), gh); path != "" || !strings.Contains(why, "not signed in") {
		t.Fatalf("signed-out gh: %q %q", path, why)
	}
}

func TestNoRemoteRefuses(t *testing.T) {
	r := newRepo(t)
	if _, err := Push(context.Background(), PushInput{WorkDir: r.worktree, Branch: r.branch}); !IsRefusal(err) || !strings.Contains(err.Error(), "no remote") {
		t.Fatalf("err %v", err)
	}
}

func TestDeleteBranchAndMainCheckout(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	main, err := MainCheckout(ctx, r.worktree)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(r.main)
	if got, _ := filepath.EvalSymlinks(main); got != resolved {
		t.Fatalf("main checkout %q, want %q", main, r.main)
	}
	r.commitInWorktree("feature.go", "x", "f")
	run(t, r.main, "git", "worktree", "remove", r.worktree)
	if err := DeleteBranch(ctx, r.main, r.branch, false); err == nil {
		t.Fatal("an unmerged branch must not be deleted without force")
	}
	if err := DeleteBranch(ctx, r.main, r.branch, true); err != nil {
		t.Fatal(err)
	}
}

func TestOrder(t *testing.T) {
	files := []FileChange{
		{Path: "web/src/Review.tsx"}, {Path: "internal/jobs/land_test.go"}, {Path: "internal/store/migrations/0014.sql"},
		{Path: "README.md"}, {Path: "internal/jobs/land.go"}, {Path: "web/package-lock.json"}, {Path: "internal/httpd/land.go"},
	}
	for i := range files {
		files[i].Group, files[i].Churn = Classify(files[i].Path)
	}
	got := []string{}
	for _, file := range Order(files, "") {
		got = append(got, file.Path)
	}
	want := []string{"internal/store/migrations/0014.sql", "internal/jobs/land.go", "internal/httpd/land.go", "web/src/Review.tsx",
		"internal/jobs/land_test.go", "README.md", "web/package-lock.json"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("order\n got %v\nwant %v", got, want)
	}
	walk := "First `internal/httpd/land.go` takes the request, then `internal/jobs/land.go` does it."
	if first := Order(files, walk); first[0].Path != "internal/httpd/land.go" || first[1].Path != "internal/jobs/land.go" {
		t.Fatalf("the walkthrough's order did not win: %v", first[:2])
	}
}

func TestStripCredentialsAndHost(t *testing.T) {
	if got := StripCredentials("https://user:tok@github.com/o/r.git"); got != "https://github.com/o/r.git" {
		t.Fatal(got)
	}
	for url, want := range map[string]bool{"git@github.com:o/r.git": true, "https://github.com/o/r": true, "ssh://git@gitlab.com/o/r": false, "/srv/r.git": false} {
		if IsGitHub(url) != want {
			t.Errorf("IsGitHub(%q) != %v", url, want)
		}
	}
}
