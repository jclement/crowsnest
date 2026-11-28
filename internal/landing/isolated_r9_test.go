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
	root := t.TempDir()
	parent := filepath.Join(root, "copy")
	copied, err := IsolatedCheckout(ctx, r.worktree, commit, root, parent)
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
	root := t.TempDir()
	parent := filepath.Join(root, "left")
	copied, err := IsolatedCheckout(ctx, r.worktree, commit, root, parent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(run(t, r.main, "git", "worktree", "list"), copied.Dir) {
		t.Fatal("setup: the copy is not listed")
	}
	// The process that made it is gone: only the directory is known.
	if err := RemoveIsolatedDir(ctx, root, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(parent); err == nil {
		t.Fatal("the directory is still there")
	}
	if strings.Contains(run(t, r.main, "git", "worktree", "list"), copied.Dir) {
		t.Fatal("git still lists the copy")
	}
}

// TestOnlyACheckCopyIsEverRemoved (R10 new-H1): a recorded path is data (a restored backup's row
// can name any folder), so RemoveIsolatedDir removes only a real directory directly inside the
// checks directory, links resolved, that carries crowsnest's marker. Everything else is left whole
// and ErrNotACheckCopy.
func TestOnlyACheckCopyIsEverRemoved(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	root := filepath.Join(base, "checks")
	precious := filepath.Join(base, "home")
	for _, dir := range []string{root, precious, filepath.Join(root, "nested", "deeper")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// A folder outside the checks directory, even one carrying the marker.
	write(t, precious, "keep.txt", "mine\n")
	write(t, precious, isolatedMarker, precious+"\n")
	// A link inside the checks directory to that folder.
	linked := filepath.Join(root, "j_1-1-link")
	if err := os.Symlink(precious, linked); err != nil {
		t.Fatal(err)
	}
	// A directory inside the checks directory without the marker, and one whose marker is a link.
	unmarked := filepath.Join(root, "j_1-2-unmarked")
	linkedMarker := filepath.Join(root, "j_1-3-linked-marker")
	for _, dir := range []string{unmarked, linkedMarker} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		write(t, dir, "keep.txt", "mine\n")
	}
	if err := os.Symlink(filepath.Join(precious, isolatedMarker), filepath.Join(linkedMarker, isolatedMarker)); err != nil {
		t.Fatal(err)
	}
	// Not a direct child: a marked directory one level down, and the same named through "..".
	// Every case is under the test's own temporary directory: a regression must never be able to
	// remove anything else.
	deeper := filepath.Join(root, "nested", "deeper")
	write(t, deeper, isolatedMarker, deeper+"\n")
	cases := map[string]string{
		"outside the checks directory":       precious,
		"through ..":                         filepath.Join(root, "..", "home"),
		"a link to a folder":                 linked,
		"no marker":                          unmarked,
		"a linked marker":                    linkedMarker,
		"not a direct child":                 deeper,
		"the checks directory itself":        root,
		"a relative path":                    "home",
		"a path through a linked parent":     filepath.Join(linked, "keep.txt"),
		"a checks directory that is missing": filepath.Join(base, "none", "x"),
	}
	for name, path := range cases {
		err := RemoveIsolatedDir(ctx, root, path)
		if !errors.Is(err, ErrNotACheckCopy) {
			t.Errorf("%s (%s): err = %v, want ErrNotACheckCopy", name, path, err)
		}
	}
	for _, file := range []string{filepath.Join(precious, "keep.txt"), filepath.Join(unmarked, "keep.txt"),
		filepath.Join(linkedMarker, "keep.txt"), filepath.Join(deeper, isolatedMarker)} {
		if _, err := os.Stat(file); err != nil {
			t.Errorf("%s was removed: %v", file, err)
		}
	}
	if _, err := os.Lstat(linked); err != nil {
		t.Errorf("the link was removed: %v", err)
	}
	// The checks directory reached through a link of its own is still the checks directory: a
	// marked copy in it is removed.
	aliasRoot := filepath.Join(base, "alias")
	if err := os.Symlink(root, aliasRoot); err != nil {
		t.Fatal(err)
	}
	ours := filepath.Join(root, "j_1-4-ours")
	if err := os.Mkdir(ours, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, ours, isolatedMarker, "\n")
	write(t, ours, "left.txt", "x\n")
	if err := RemoveIsolatedDir(ctx, aliasRoot, filepath.Join(aliasRoot, "j_1-4-ours")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(ours); err == nil {
		t.Fatal("a marked copy directly inside the checks directory was not removed")
	}
}

// TestACopyIsMadeOnlyDirectlyInsideTheChecksDirectory (R10 new-H1): the in-process path is held to
// the same rule, and an existing directory is never adopted as a copy.
func TestACopyIsMadeOnlyDirectlyInsideTheChecksDirectory(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	commit := run(t, r.worktree, "git", "rev-parse", "HEAD")
	root := t.TempDir()
	if _, err := IsolatedCheckout(ctx, r.worktree, commit, root, filepath.Join(root, "a", "b")); err == nil {
		t.Fatal("a copy was made below a direct child")
	}
	existing := filepath.Join(root, "existing")
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, existing, "keep.txt", "mine\n")
	if _, err := IsolatedCheckout(ctx, r.worktree, commit, root, existing); err == nil {
		t.Fatal("an existing directory was adopted as a copy")
	}
	if _, err := os.Stat(filepath.Join(existing, "keep.txt")); err != nil {
		t.Fatalf("the existing directory was touched: %v", err)
	}
}
