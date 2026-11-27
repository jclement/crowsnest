package landing

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRepositoryCannotRunCommandsOnLand is R5-S5: hooks, an fsmonitor, an external diff and a
// textconv filter that the repository configures do not run when crowsnest inspects, diffs or
// merges a job. Each writes a marker file if it runs.
func TestRepositoryCannotRunCommandsOnLand(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	marker := filepath.Join(t.TempDir(), "ran")
	script := "#!/bin/sh\necho \"$0\" >> " + marker + "\n"
	hooks := filepath.Join(r.main, ".git", "hooks")
	for _, hook := range []string{"post-merge", "pre-merge-commit", "commit-msg", "reference-transaction", "post-checkout"} {
		path := filepath.Join(hooks, hook)
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // an executable test hook
			t.Fatal(err)
		}
	}
	tool := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(tool, []byte(script), 0o755); err != nil { //nolint:gosec // an executable test tool
		t.Fatal(err)
	}
	run(t, r.main, "git", "config", "core.fsmonitor", tool)
	run(t, r.main, "git", "config", "diff.external", tool)
	run(t, r.main, "git", "config", "diff.evil.textconv", tool)
	write(t, r.worktree, ".gitattributes", "*.go diff=evil\n")
	r.commitInWorktree("feature.go", "package main\n", "feature")

	base := run(t, r.main, "git", "rev-parse", "HEAD")
	_ = os.Remove(marker) // the setup's own commits ran the hooks; only crowsnest's calls count
	produced, err := Inspect(ctx, r.worktree, "main", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := FileDiff(ctx, r.worktree, produced.DiffBase, "feature.go", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(ctx, MergeInput{MainDir: r.main, WorkDir: r.worktree, Branch: r.branch, Base: "main", Mode: ModeMerge, Message: "Merge job j_1"}); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(marker); err == nil {
		t.Fatalf("a repository-chosen command ran during Land: %s", strings.TrimSpace(string(body)))
	}
}

// TestProtectedArgs: the protective options come first, and diff-like subcommands get the flags
// that turn off external diffs and textconv.
func TestProtectedArgs(t *testing.T) {
	args := protectedArgs("/repo", []string{"diff", "HEAD"})
	joined := strings.Join(args, " ")
	for _, want := range []string{"-c core.hooksPath=/dev/null", "-c core.fsmonitor=false", "-c protocol.ext.allow=never", "-C /repo diff --no-ext-diff --no-textconv HEAD"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%q lacks %q", joined, want)
		}
	}
	if joined := strings.Join(protectedArgs("/repo", []string{"status"}), " "); strings.Contains(joined, "--no-textconv") {
		t.Errorf("status got diff flags: %q", joined)
	}
}
