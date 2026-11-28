// Tests for the snapshot's total bounds (R7-C09 residual): the bytes of changed tracked files one
// reading reads are bounded (refused past it), and so are the untracked files' (skipped past it).

package landing

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestTheSnapshotIsBoundedInBytes(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	oldTracked, oldUntracked := maxSnapshotBytes, maxUntrackedTotal
	t.Cleanup(func() { maxSnapshotBytes, maxUntrackedTotal = oldTracked, oldUntracked })
	maxSnapshotBytes, maxUntrackedTotal = 1000, 1000

	// Untracked files past the total are skipped, named, not read.
	write(t, r.worktree, "a.txt", strings.Repeat("a", 600))
	write(t, r.worktree, "b.txt", strings.Repeat("b", 600))
	_, skipped, err := WorkTree(ctx, r.worktree)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0].Why, "past the") {
		t.Fatalf("skipped = %+v", skipped)
	}

	// Changed tracked files past the bound refuse the snapshot before git reads them.
	write(t, r.worktree, "server.go", "package main\n// "+strings.Repeat("x", 2000)+"\n")
	if _, _, err := WorkTree(ctx, r.worktree); !errors.Is(err, ErrSnapshotTooLarge) {
		t.Fatalf("a snapshot over the bound: %v", err)
	}
}
