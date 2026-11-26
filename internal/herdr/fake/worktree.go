// worktree.create for the fake: a workspace labelled and rooted at the "checkout" path, with the
// worktree recorded so tests can assert on the branch. No git is run; the path is whatever the
// caller asked for, or <cwd>/.worktrees/<branch>.

package fake

import (
	"context"
	"path/filepath"
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
