package landing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// changesFixture is a worktree with one commit on its branch (feature.go added, README.md
// changed) and then uncommitted work: server.go edited, notes.txt untracked.
func changesFixture(t *testing.T) (*repo, string) {
	t.Helper()
	r := newRepo(t)
	base := run(t, r.main, "git", "rev-parse", "HEAD")
	write(t, r.worktree, "README.md", "hello\nworld\n")
	r.commitInWorktree("feature.go", "package main\n\nfunc feature() {}\n", "feature")
	write(t, r.worktree, "server.go", "package main\n\nfunc serve() {}\n")
	write(t, r.worktree, "notes.txt", "a note\n")
	return r, base
}

func byPath(files []DiffFile) map[string]DiffFile {
	out := map[string]DiffFile{}
	for _, file := range files {
		out[file.Path] = file
	}
	return out
}

func TestCompareScopes(t *testing.T) {
	r, base := changesFixture(t)
	ctx := context.Background()
	work, skipped, err := WorkTree(ctx, r.worktree)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped %v", skipped)
	}
	head := HeadTree(ctx, r.worktree)

	committed, _, err := Compare(ctx, r.worktree, base, head)
	if err != nil {
		t.Fatal(err)
	}
	got := byPath(committed)
	if len(got) != 2 || got["feature.go"].Status != "added" || got["README.md"].Status != "modified" {
		t.Fatalf("committed = %+v", committed)
	}
	if got["README.md"].Added != 1 || got["README.md"].Deleted != 0 {
		t.Errorf("README counts = %+v", got["README.md"])
	}

	uncommitted, _, err := Compare(ctx, r.worktree, head, work)
	if err != nil {
		t.Fatal(err)
	}
	got = byPath(uncommitted)
	if len(got) != 2 || got["server.go"].Status != "modified" || got["notes.txt"].Status != "added" {
		t.Fatalf("uncommitted = %+v", uncommitted)
	}

	all, _, err := Compare(ctx, r.worktree, base, work)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("all = %+v", all)
	}
	// The checkout's own index is untouched: notes.txt is still untracked, server.go unstaged.
	status := run(t, r.worktree, "git", "status", "--porcelain")
	staged := run(t, r.worktree, "git", "diff", "--cached", "--name-only")
	if !strings.Contains(status, "?? notes.txt") || staged != "" {
		t.Errorf("the real index changed:\n%s\nstaged: %q", status, staged)
	}
}

func TestCompareRenameBinaryDelete(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	write(t, r.main, "big.txt", strings.Repeat("line of text that stays the same\n", 40))
	write(t, r.main, "gone.go", "package main\n")
	run(t, r.main, "git", "add", ".")
	run(t, r.main, "git", "commit", "-q", "-m", "more")
	base := run(t, r.main, "git", "rev-parse", "HEAD")
	run(t, r.worktree, "git", "merge", "-q", "--ff-only", "main")
	run(t, r.worktree, "git", "mv", "big.txt", "moved.txt")
	run(t, r.worktree, "git", "rm", "-q", "gone.go")
	if err := os.WriteFile(filepath.Join(r.worktree, "logo.png"), []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 0, 0}, 0o600); err != nil {
		t.Fatal(err)
	}
	work, _, err := WorkTree(ctx, r.worktree)
	if err != nil {
		t.Fatal(err)
	}
	files, _, err := Compare(ctx, r.worktree, base, work)
	if err != nil {
		t.Fatal(err)
	}
	got := byPath(files)
	if got["moved.txt"].Status != "renamed" || got["moved.txt"].OldPath != "big.txt" {
		t.Errorf("rename = %+v", got["moved.txt"])
	}
	if got["gone.go"].Status != "deleted" || got["gone.go"].NewBlob != NullBlob {
		t.Errorf("delete = %+v", got["gone.go"])
	}
	if !got["logo.png"].Binary || got["logo.png"].Status != "added" {
		t.Errorf("binary = %+v", got["logo.png"])
	}
	diff, binary, _, err := Patch(ctx, r.worktree, base, work, "logo.png", "", PatchOptions{})
	if err != nil || !binary || diff != "" {
		t.Errorf("binary patch = %q %v %v", diff, binary, err)
	}
	diff, _, _, err = Patch(ctx, r.worktree, base, work, "moved.txt", "big.txt", PatchOptions{})
	if err != nil || !strings.Contains(diff, "rename from big.txt") {
		t.Errorf("rename patch = %q %v", diff, err)
	}
}

func TestWorkTreeBounds(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	big := make([]byte, MaxUntrackedBytes+10)
	if err := os.WriteFile(filepath.Join(r.worktree, "dump.bin"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	write(t, r.worktree, ".gitignore", "ignored/\n")
	write(t, r.worktree, "ignored/secret.txt", "never read\n")
	work, skipped, err := WorkTree(ctx, r.worktree)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 1 || skipped[0].Path != "dump.bin" || !strings.Contains(skipped[0].Why, "too large") {
		t.Fatalf("skipped = %+v", skipped)
	}
	files, _, err := Compare(ctx, r.worktree, HeadTree(ctx, r.worktree), work)
	if err != nil {
		t.Fatal(err)
	}
	got := byPath(files)
	if _, ok := got["ignored/secret.txt"]; ok {
		t.Error("an ignored file was read")
	}
	if _, ok := got["dump.bin"]; ok {
		t.Error("the large file was added")
	}
	rows := SkippedFiles(skipped, files)
	if len(rows) != 1 || !rows[0].TooLarge || rows[0].Hash == "" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestHashStableAndChanges(t *testing.T) {
	r, base := changesFixture(t)
	ctx := context.Background()
	hashOf := func(path string) DiffFile {
		work, _, err := WorkTree(ctx, r.worktree)
		if err != nil {
			t.Fatal(err)
		}
		files, _, err := Compare(ctx, r.worktree, base, work)
		if err != nil {
			t.Fatal(err)
		}
		return byPath(files)[path]
	}
	first := hashOf("server.go")
	again := hashOf("server.go")
	if first.Hash == "" || first.Hash != again.Hash {
		t.Fatalf("hash not stable: %q %q", first.Hash, again.Hash)
	}
	// Committing the same content keeps base → work tree's hash: the same change.
	run(t, r.worktree, "git", "add", "server.go")
	run(t, r.worktree, "git", "commit", "-q", "-m", "serve")
	if committed := hashOf("server.go"); committed.Hash != first.Hash {
		t.Errorf("hash changed on commit: %q → %q", first.Hash, committed.Hash)
	}
	write(t, r.worktree, "server.go", "package main\n\nfunc serve() { println(1) }\n")
	changed := hashOf("server.go")
	if changed.Hash == first.Hash {
		t.Fatal("hash did not change with the content")
	}
	// What changed since the first version was viewed: only the new edit.
	diff, _, _, err := BlobPatch(ctx, r.worktree, first.NewBlob, changed.NewBlob, "server.go", PatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "+func serve() { println(1) }") || !strings.Contains(diff, "-func serve() {}") || strings.Contains(diff, "package main\n+") {
		t.Errorf("since-viewed diff = %q", diff)
	}
	if !strings.Contains(diff, "a/server.go") {
		t.Errorf("since-viewed diff is not labelled with the path: %q", diff)
	}
	if !HasBlob(ctx, r.worktree, first.NewBlob) || HasBlob(ctx, r.worktree, NullBlob) {
		t.Error("HasBlob")
	}
	text, err := Blob(ctx, r.worktree, changed.NewBlob, MaxBlobBytes)
	if err != nil || !strings.Contains(text, "println(1)") {
		t.Errorf("blob = %q %v", text, err)
	}
	if _, err := Blob(ctx, r.worktree, changed.NewBlob, 4); !errors.Is(err, ErrBlobTooLarge) {
		t.Errorf("blob over the limit: %v", err)
	}
}

// TestLiveDiffRunsNoRepositoryCommand: a clean filter, an fsmonitor, an external diff and a
// textconv the repository configures do not run when the work tree is snapshotted and diffed.
func TestLiveDiffRunsNoRepositoryCommand(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	marker := filepath.Join(t.TempDir(), "ran")
	tool := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\necho \"$0 $* $(ps -o args= -p $PPID)\" >> "+marker+"\ncat\n"), 0o755); err != nil { //nolint:gosec // an executable test tool
		t.Fatal(err)
	}
	base := run(t, r.main, "git", "rev-parse", "HEAD")
	run(t, r.main, "git", "config", "filter.evil.clean", tool)
	run(t, r.main, "git", "config", "filter.evil.smudge", tool)
	run(t, r.main, "git", "config", "filter.evil.required", "true")
	run(t, r.main, "git", "config", "core.fsmonitor", tool)
	run(t, r.main, "git", "config", "diff.external", tool)
	run(t, r.main, "git", "config", "diff.evil.textconv", tool)
	write(t, r.worktree, ".gitattributes", "*.txt filter=evil diff=evil\n")
	write(t, r.worktree, "server.go", "package main // changed\n")
	write(t, r.worktree, "new.txt", "untracked text\n")
	_ = os.Remove(marker)
	work, _, err := WorkTree(ctx, r.worktree)
	if err != nil {
		t.Fatal(err)
	}
	files, _, err := Compare(ctx, r.worktree, base, work)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := byPath(files)["new.txt"]; !ok {
		t.Fatalf("new.txt missing: %+v", files)
	}
	if _, _, _, err := Patch(ctx, r.worktree, base, work, "new.txt", "", PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(marker); err == nil {
		t.Fatalf("a repository-chosen command ran: %s", strings.TrimSpace(string(body)))
	}
}

func TestPatchOptions(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	lines := []string{}
	for i := range 30 {
		lines = append(lines, "line "+string(rune('a'+i%26)))
	}
	write(t, r.main, "long.txt", strings.Join(lines, "\n")+"\n")
	run(t, r.main, "git", "add", ".")
	run(t, r.main, "git", "commit", "-q", "-m", "long")
	base := run(t, r.main, "git", "rev-parse", "HEAD")
	run(t, r.worktree, "git", "merge", "-q", "--ff-only", "main")
	lines[15] = "line CHANGED"
	lines[3] = "  " + lines[3] // whitespace only
	write(t, r.worktree, "long.txt", strings.Join(lines, "\n")+"\n")
	work, _, err := WorkTree(ctx, r.worktree)
	if err != nil {
		t.Fatal(err)
	}
	narrow, _, _, _ := Patch(ctx, r.worktree, base, work, "long.txt", "", PatchOptions{})
	whole, _, _, _ := Patch(ctx, r.worktree, base, work, "long.txt", "", PatchOptions{Context: 100000})
	if strings.Count(whole, "\n") <= strings.Count(narrow, "\n") || !strings.Contains(whole, " line a\n") {
		t.Errorf("whole-file context:\n%s", whole)
	}
	noWS, _, _, _ := Patch(ctx, r.worktree, base, work, "long.txt", "", PatchOptions{IgnoreWhitespace: true})
	if strings.Contains(noWS, "+  line d") || !strings.Contains(noWS, "+line CHANGED") {
		t.Errorf("ignoring whitespace:\n%s", noWS)
	}
}

func TestRefHelpers(t *testing.T) {
	r, base := changesFixture(t)
	ctx := context.Background()
	if got := MergeBase(ctx, r.worktree, "main"); got != base {
		t.Errorf("merge base = %q, want %q", got, base)
	}
	if ResolveCommit(ctx, r.worktree, "no-such-branch") != "" {
		t.Error("an unknown ref resolved")
	}
	for ref, want := range map[string]bool{"main": true, "origin/main": true, "HEAD~2": true, "-x": false, "a..b": false, "a b": false, "": false, "x.lock": false} {
		if ValidRef(ref) != want {
			t.Errorf("ValidRef(%q) = %v", ref, !want)
		}
	}
	root, err := Root(ctx, filepath.Join(r.worktree))
	if err != nil {
		t.Fatal(err)
	}
	if resolved, _ := filepath.EvalSymlinks(r.worktree); root != resolved && root != r.worktree {
		t.Errorf("root = %q", root)
	}
}

func TestFirstCommitAgainstEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	dir := t.TempDir()
	newRepo(t) // sets the git environment
	run(t, dir, "git", "init", "-q", "-b", "main")
	write(t, dir, "a.txt", "one\n")
	ctx := context.Background()
	work, _, err := WorkTree(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	files, _, err := Compare(ctx, dir, "", work)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Status != "added" {
		t.Fatalf("files = %+v", files)
	}
}
