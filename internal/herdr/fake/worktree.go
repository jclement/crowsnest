// worktree.create for the fake: a workspace labelled and rooted at the "checkout" path, with the
// worktree recorded so tests can assert on the branch. No git is run; the path is whatever the
// caller asked for, or <cwd>/.worktrees/<branch>.

package fake

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jclement/crowsnest/internal/herdr"
)

// WorktreeCreate implements herdr.Client.
func (f *Fake) WorktreeCreate(ctx context.Context, params herdr.WorktreeCreateParams) (herdr.WorktreeCreated, error) {
	if err := f.takeFailure("worktree.create"); err != nil {
		return herdr.WorktreeCreated{}, err
	}
	path := params.Path
	if path == "" {
		path = filepath.Join(params.Cwd, ".worktrees", strings.ReplaceAll(params.Branch, "/", "-"))
	}
	f.mu.Lock()
	realGit := f.realGit
	f.mu.Unlock()
	if realGit {
		args := []string{"-C", params.Cwd, "worktree", "add", "-q", "-b", params.Branch, path}
		if params.Base != "" {
			args = append(args, params.Base)
		}
		if out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput(); err != nil { //nolint:gosec // test fake
			return herdr.WorktreeCreated{}, &herdr.Error{Code: "worktree_failed", Message: strings.TrimSpace(string(out)), Method: "worktree.create"}
		}
	}
	created, err := f.WorkspaceCreate(ctx, herdr.WorkspaceCreateParams{Label: params.Label, Cwd: path})
	if err != nil {
		return herdr.WorktreeCreated{}, err
	}
	info := herdr.WorktreeInfo{Path: path, Branch: params.Branch, Label: params.Label}
	f.mu.Lock()
	f.workspaces[created.Workspace.ID].info.Worktree = &herdr.Worktree{RepoRoot: params.Cwd, CheckoutPath: path, IsLinked: true, RepoName: filepath.Base(params.Cwd)}
	f.worktrees = append(f.worktrees, params)
	created.Workspace = f.workspaces[created.Workspace.ID].info
	f.mu.Unlock()
	return herdr.WorktreeCreated{Workspace: created.Workspace, Tab: created.Tab, RootPane: created.RootPane, Worktree: info}, nil
}

// Worktrees returns every accepted worktree.create, in order.
func (f *Fake) Worktrees() []herdr.WorktreeCreateParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]herdr.WorktreeCreateParams(nil), f.worktrees...)
}

// RealGitWorktrees makes worktree.create run `git worktree add` and worktree.remove run `git
// worktree remove` (which refuses a dirty tree, as herdr does): landing tests then exercise
// real checkouts while everything else stays in memory.
func (f *Fake) RealGitWorktrees() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.realGit = true
}

// WorktreeRemove implements herdr.Client: the workspace's worktree is removed (for real with
// RealGitWorktrees) and the workspace closed. A dirty tree is refused unless forced.
func (f *Fake) WorktreeRemove(ctx context.Context, params herdr.WorktreeRemoveParams) (herdr.WorktreeRemoved, error) {
	if err := f.takeFailure("worktree.remove"); err != nil {
		return herdr.WorktreeRemoved{}, err
	}
	f.mu.Lock()
	ws, ok := f.workspaces[params.WorkspaceID]
	if !ok || ws.info.Worktree == nil {
		f.mu.Unlock()
		return herdr.WorktreeRemoved{}, &herdr.Error{Code: "workspace_not_found", Message: "no worktree workspace " + params.WorkspaceID, Method: "worktree.remove"}
	}
	tree := *ws.info.Worktree
	realGit := f.realGit
	f.mu.Unlock()
	if realGit {
		args := []string{"-C", tree.RepoRoot, "worktree", "remove", tree.CheckoutPath}
		if params.Force {
			args = append(args, "--force")
		}
		if out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput(); err != nil { //nolint:gosec // test fake
			return herdr.WorktreeRemoved{}, &herdr.Error{Code: "worktree_dirty", Message: strings.TrimSpace(string(out)), Method: "worktree.remove"}
		}
	}
	f.mu.Lock()
	f.removed = append(f.removed, params.WorkspaceID)
	f.closeWorkspaceLocked(params.WorkspaceID)
	f.mu.Unlock()
	f.emit(herdr.Event{Kind: "workspace_closed", WorkspaceID: params.WorkspaceID})
	return herdr.WorktreeRemoved{WorkspaceID: params.WorkspaceID, Worktree: herdr.WorktreeInfo{Path: tree.CheckoutPath}, Forced: params.Force}, nil
}

// WorkspaceClose implements herdr.Client.
func (f *Fake) WorkspaceClose(_ context.Context, workspaceID string) error {
	if err := f.takeFailure("workspace.close"); err != nil {
		return err
	}
	f.mu.Lock()
	if _, ok := f.workspaces[workspaceID]; !ok {
		f.mu.Unlock()
		return &herdr.Error{Code: "workspace_not_found", Message: fmt.Sprintf("no workspace %s", workspaceID), Method: "workspace.close"}
	}
	f.closed = append(f.closed, workspaceID)
	f.closeWorkspaceLocked(workspaceID)
	f.mu.Unlock()
	f.emit(herdr.Event{Kind: "workspace_closed", WorkspaceID: workspaceID})
	return nil
}

func (f *Fake) closeWorkspaceLocked(id string) {
	ws, ok := f.workspaces[id]
	if !ok {
		return
	}
	for _, tabID := range ws.tabs {
		if t, ok := f.tabs[tabID]; ok {
			for _, paneID := range t.panes {
				delete(f.panes, paneID)
			}
		}
		delete(f.tabs, tabID)
	}
	delete(f.workspaces, id)
	f.order = slices.DeleteFunc(f.order, func(w string) bool { return w == id })
	f.revision++
}

// Removed and Closed list the workspaces whose worktree was removed, and those closed.
func (f *Fake) Removed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.removed...)
}

// Closed lists the workspaces closed with workspace.close.
func (f *Fake) Closed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.closed...)
}
