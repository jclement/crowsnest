//go:build integration

// worktree.create against real herdr (M7's launcher uses it for builder jobs): a scratch git
// repository gets a worktree on a new branch at the path asked for, and herdr opens a workspace on
// it that the launcher can add a tab to. No Claude involved.

package herdr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIntegrationWorktreeCreate(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	session := newTestSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("scratch\n"), 0o644)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"}, {"add", "README.md"},
		{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	path := filepath.Join(root, "worktrees", "job-1")
	created, err := session.Client.WorktreeCreate(ctx, WorktreeCreateParams{Cwd: repo, Branch: "job/1-itest", Path: path, Label: "job-1"})
	if err != nil {
		t.Fatalf("worktree.create: %v", err)
	}
	t.Logf("worktree.create = %+v", created)
	if created.Workspace.ID == "" || created.RootPane.ID == "" {
		t.Fatalf("no workspace or pane: %+v", created)
	}
	resolved, _ := filepath.EvalSymlinks(path)
	got, _ := filepath.EvalSymlinks(created.Worktree.Path)
	if got != resolved {
		t.Errorf("worktree path %q, want %q", created.Worktree.Path, path)
	}
	out, err := exec.Command("git", "-C", path, "branch", "--show-current").Output()
	if err != nil || strings.TrimSpace(string(out)) != "job/1-itest" {
		t.Fatalf("branch in the worktree: %q %v", out, err)
	}
	tab, err := session.Client.TabCreate(ctx, TabCreateParams{WorkspaceID: created.Workspace.ID, Label: "job-1", Cwd: path, Env: map[string]string{"CROWSNEST_JOB": "j_1"}})
	if err != nil || tab.RootPane.ID == "" {
		t.Fatalf("tab in the worktree workspace: %+v %v", tab, err)
	}
}

// TestIntegrationWorktreeRemove is Land's teardown against real herdr (DESIGN.md 6.11): a dirty
// worktree is refused (and stays), a clean one is removed with its workspace, and closing a
// workspace that is already gone is an error crowsnest can ignore.
func TestIntegrationWorktreeRemove(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	session := newTestSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("scratch\n"), 0o644)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"}, {"add", "README.md"},
		{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	path := filepath.Join(root, "worktrees", "job-2")
	created, err := session.Client.WorktreeCreate(ctx, WorktreeCreateParams{Cwd: repo, Branch: "job/2-itest", Path: path, Label: "job-2"})
	if err != nil {
		t.Fatalf("worktree.create: %v", err)
	}
	// Dirty: an untracked file. herdr must refuse without force.
	if err := os.WriteFile(filepath.Join(path, "half.txt"), []byte("unfinished"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Client.WorktreeRemove(ctx, WorktreeRemoveParams{WorkspaceID: created.Workspace.ID}); err == nil {
		t.Fatal("worktree.remove removed a dirty worktree without force")
	} else {
		t.Logf("dirty worktree refused: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the refused worktree is gone: %v", err)
	}
	if err := os.Remove(filepath.Join(path, "half.txt")); err != nil {
		t.Fatal(err)
	}
	removed, err := session.Client.WorktreeRemove(ctx, WorktreeRemoveParams{WorkspaceID: created.Workspace.ID})
	if err != nil {
		t.Fatalf("worktree.remove of a clean worktree: %v", err)
	}
	t.Logf("worktree.remove = %+v", removed)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the worktree is still on disk: %v", err)
	}
	workspaces, err := session.Client.WorkspaceList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, workspace := range workspaces {
		if workspace.ID == created.Workspace.ID {
			t.Fatalf("the workspace is still open after worktree.remove: %+v", workspace)
		}
	}
	if err := session.Client.WorkspaceClose(ctx, created.Workspace.ID); err == nil {
		t.Log("workspace.close of a closed workspace answered without an error")
	} else {
		t.Logf("workspace.close of a closed workspace: %v (ignored by the teardown)", err)
	}
	out, _ := exec.Command("git", "-C", repo, "branch", "--list", "job/2-itest").Output()
	t.Logf("branch after removal: %q (crowsnest deletes it itself)", strings.TrimSpace(string(out)))
}
