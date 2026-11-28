// Tests for the isolated copy's verification (R9-C6) and the removal of a copy a crash left
// (R9-C7): the copy is the commit's exact bytes even when the repository's attributes would
// convert them, any change to a tracked path is found, and RemoveIsolatedDir leaves nothing.

package landing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheCopyIsExactlyTheCommit(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	// .gitattributes asks for CRLF on checkout and an ident expansion: a copy that honoured them
	// would test bytes the commit does not hold.
	write(t, r.worktree, ".gitattributes", "*.txt text eol=crlf\n*.go ident\n")
	write(t, r.worktree, "notes.txt", "one\ntwo\n")
	write(t, r.worktree, "id.go", "// $Id$\npackage main\n")
	write(t, r.worktree, "run.sh", "#!/bin/sh\necho hi\n")
	if err := os.Chmod(filepath.Join(r.worktree, "run.sh"), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	if err := os.Symlink("server.go", filepath.Join(r.worktree, "link.go")); err != nil {
		t.Fatal(err)
	}
	run(t, r.worktree, "git", "add", ".")
	run(t, r.worktree, "git", "commit", "-q", "-m", "attributes")
	commit := run(t, r.worktree, "git", "rev-parse", "HEAD")
	parent := filepath.Join(t.TempDir(), "copy")
	copied, err := IsolatedCheckout(ctx, r.worktree, commit, parent)
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Remove(ctx)
	tree, err := VerifyCheckout(ctx, copied.Dir, commit)
	if err != nil {
		t.Fatalf("a fresh copy did not verify: %v", err)
	}
	if want := run(t, r.worktree, "git", "rev-parse", commit+"^{tree}"); tree != want {
		t.Fatalf("tree = %s, want %s", tree, want)
	}
	if body, _ := os.ReadFile(filepath.Join(copied.Dir, "notes.txt")); string(body) != "one\ntwo\n" { //nolint:gosec // test fixture
		t.Fatalf("the repository's attributes converted the copy: %q", body)
	}

	// Each kind of change is found and named.
	for name, change := range map[string]func() error{
		"other content": func() error {
			return os.WriteFile(filepath.Join(copied.Dir, "server.go"), []byte("package main // repaired\n"), 0o644)
		}, //nolint:gosec // test fixture
		"missing": func() error { return os.Remove(filepath.Join(copied.Dir, "README.md")) },
		"executable bit": func() error {
			return os.Chmod(filepath.Join(copied.Dir, "run.sh"), 0o644) //nolint:gosec // test fixture
		},
		"symbolic link": func() error {
			_ = os.Remove(filepath.Join(copied.Dir, "link.go"))
			return os.WriteFile(filepath.Join(copied.Dir, "link.go"), []byte("server.go"), 0o644) //nolint:gosec // test fixture
		},
	} {
		if err := change(); err != nil {
			t.Fatal(err)
		}
		_, err := VerifyCheckout(ctx, copied.Dir, commit)
		if !errors.Is(err, ErrNotTheCommit) || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: err = %v", name, err)
		}
		run(t, copied.Dir, "git", "checkout", "-q", "--", ".") // put it back for the next case
		if name == "executable bit" {
			_ = os.Chmod(filepath.Join(copied.Dir, "run.sh"), 0o755) //nolint:gosec // test fixture
		}
	}
	// An untracked file (a dependency, build output) is allowed.
	write(t, copied.Dir, "node_modules/x.js", "x\n")
	if _, err := VerifyCheckout(ctx, copied.Dir, commit); err != nil {
		t.Fatalf("an untracked file failed the verification: %v", err)
	}
	// Another commit checked out in the copy is found by its HEAD.
	run(t, copied.Dir, "git", "checkout", "-q", "--detach", "HEAD~1")
	if _, err := VerifyCheckout(ctx, copied.Dir, commit); !errors.Is(err, ErrNotTheCommit) || !strings.Contains(err.Error(), "HEAD is") {
		t.Fatalf("another commit checked out: %v", err)
	}
}

func TestACopyLeftBehindIsRemovedWithItsWorktree(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	r.commitInWorktree("widget.go", "package widget\n", "widget")
	commit := run(t, r.worktree, "git", "rev-parse", "HEAD")
	parent := filepath.Join(t.TempDir(), "left")
	copied, err := IsolatedCheckout(ctx, r.worktree, commit, parent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(run(t, r.main, "git", "worktree", "list"), copied.Dir) {
		t.Fatal("setup: the copy is not listed")
	}
	// The process that made it is gone: only the directory is known.
	if err := RemoveIsolatedDir(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(parent); err == nil {
		t.Fatal("the directory is still there")
	}
	if strings.Contains(run(t, r.main, "git", "worktree", "list"), copied.Dir) {
		t.Fatal("git still lists the copy")
	}
}
