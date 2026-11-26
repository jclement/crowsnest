// worktree.create (DESIGN.md 6.2): herdr makes a git worktree for a repository and opens a
// workspace on it. The job launcher uses it when a builder job is given a git directory. Kept in
// its own file so the socket client's core stays small.

package herdr

import "context"

// WorktreeCreateParams are worktree.create's options. Cwd names the repository (any directory
// inside it); Branch is created from Base (herdr's default when empty); Path is where the
// checkout goes (herdr's own ~/.herdr/worktrees/<repo>/<slug> when empty). herdr takes no pane
// environment here, so callers open a tab with their env in the new workspace.
type WorktreeCreateParams struct {
	Cwd    string `json:"cwd,omitempty"`
	Branch string `json:"branch,omitempty"`
	Base   string `json:"base,omitempty"`
	Path   string `json:"path,omitempty"`
	Label  string `json:"label,omitempty"`
	Focus  bool   `json:"focus"`
}

// WorktreeInfo is the checkout herdr created.
type WorktreeInfo struct {
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
	Label  string `json:"label"`
}

// WorktreeCreated is the result of worktree.create.
type WorktreeCreated struct {
	Workspace Workspace    `json:"workspace"`
	Tab       Tab          `json:"tab"`
	RootPane  Pane         `json:"root_pane"`
	Worktree  WorktreeInfo `json:"worktree"`
}

// WorktreeCreate implements Client.
func (c *SocketClient) WorktreeCreate(ctx context.Context, params WorktreeCreateParams) (WorktreeCreated, error) {
	var result WorktreeCreated
	err := c.call(ctx, "worktree.create", params, &result, 0)
	return result, err
}
