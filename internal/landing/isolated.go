// An isolated copy of a commit for crowsnest's own check (DESIGN.md 6.15, R7-C01).
//
// The default check runs in the job's own worktree, which has the dependencies and caches a check
// needs, but which the job's agent (or anything it left running) can change while the check runs.
// A project can instead ask for its check to run in a temporary detached worktree of exactly the
// pinned commit: made here, with the protective configuration of git.go, every configured clean,
// smudge and process filter switched off (so no repository-chosen program runs while the files are
// written) and Git LFS's smudge skipped, and removed afterwards. What the copy does not have, the
// check does not get: ignored files (node_modules, a venv, build caches), submodules (not
// initialised) and LFS content (the pointer files are checked out). The project's "prepare"
// command, run in the copy first, is where dependencies come from.

package landing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// isolatedTimeout bounds making or removing the copy: a checkout of a large repository.
const isolatedTimeout = 5 * time.Minute

// Isolated is a temporary checkout of one commit. Remove takes it away again (and is safe to call
// more than once).
type Isolated struct {
	Dir    string
	repo   string
	parent string
}

// IsolatedCheckout makes a detached worktree of commit from the repository dir is in, in a new
// temporary directory, and returns it. Every failure is an error: there is no fallback to the
// job's own checkout.
func IsolatedCheckout(ctx context.Context, dir, commit string) (*Isolated, error) {
	if !isObjectID(commit) {
		return nil, fmt.Errorf("not a commit id: %q", commit)
	}
	repo, err := Root(ctx, dir)
	if err != nil || repo == "" {
		return nil, fmt.Errorf("finding the repository of %s: %v", dir, err)
	}
	parent, err := os.MkdirTemp("", "crowsnest-check-*")
	if err != nil {
		return nil, fmt.Errorf("making a directory for the isolated copy: %w", err)
	}
	copyDir := filepath.Join(parent, filepath.Base(repo))
	call := gitCall{dir: repo, args: []string{"worktree", "add", "--detach", "--force", copyDir, commit},
		config: filtersOff(ctx, repo), env: []string{"GIT_LFS_SKIP_SMUDGE=1"}, timeout: isolatedTimeout}
	if _, err := call.run(ctx); err != nil {
		_ = os.RemoveAll(parent)
		_, _ = gitCall{dir: repo, args: []string{"worktree", "prune"}}.run(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("checking out %s in an isolated copy: %w", shortID(commit), err)
	}
	head, err := gitCall{dir: copyDir, args: []string{"rev-parse", "HEAD"}}.run(ctx)
	if err != nil || strings.TrimSpace(head) != commit {
		iso := &Isolated{Dir: copyDir, repo: repo, parent: parent}
		iso.Remove(ctx)
		return nil, fmt.Errorf("the isolated copy is not at %s (%s, %v)", shortID(commit), strings.TrimSpace(head), err)
	}
	return &Isolated{Dir: copyDir, repo: repo, parent: parent}, nil
}

// Remove takes the copy away: git forgets the worktree and the directory goes, whatever the check
// left in it.
func (i *Isolated) Remove(ctx context.Context) {
	if i == nil || i.parent == "" {
		return
	}
	ctx = context.WithoutCancel(ctx)
	_, _ = gitCall{dir: i.repo, args: []string{"worktree", "remove", "--force", "--force", i.Dir}, timeout: isolatedTimeout}.run(ctx)
	_ = os.RemoveAll(i.parent)
	_, _ = gitCall{dir: i.repo, args: []string{"worktree", "prune"}}.run(ctx)
	i.parent = ""
}

// TreeFingerprint is a content fingerprint of dir's checkout now: its tracked files as they are
// and its untracked files that are not ignored, as one git tree id (WorkTree's snapshot, which
// leaves the checkout's own index alone). Two equal fingerprints mean the same content.
func TreeFingerprint(ctx context.Context, dir string) (string, error) {
	tree, _, err := WorkTree(ctx, dir)
	return tree, err
}

func shortID(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}
