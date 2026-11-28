// Tests for the isolated check as R9 left it: what passes is exactly the commit's tracked tree,
// verified after the checkout, after the prepare command and after the check (R9-C6), and a copy a
// crash left behind is removed at start-up (R9-C7).

package jobs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/landing"
	"github.com/jclement/crowsnest/internal/store"
)

// TestAPrepareThatRepairsATrackedFileIsNotEvidence (R9-C6): a commit whose check fails, with a
// prepare command that rewrites the tracked file the check tests, is not accepted.
func TestAPrepareThatRepairsATrackedFileIsNotEvidence(t *testing.T) {
	r := newAcceptRig(t, "true", LandingOptions{})
	ctx := context.Background()
	job := isolationJob(t, r, store.ProjectAccept{CheckCommand: "grep -q fixed widget.go", CheckIsolated: true,
		CheckPrepare: "printf 'package widget // fixed\\n' > widget.go"})
	if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
		t.Fatal(err)
	}
	d := r.get(job.ID)
	if d.AcceptState == AcceptAccepted {
		t.Fatalf("a commit that fails its check was accepted because the prepare command repaired it: %q", d.AcceptDetail)
	}
	if d.Evidence == nil || !strings.Contains(d.Evidence.OutputTail, "widget.go has other content") {
		t.Fatalf("the evidence does not say the copy changed: %+v", d.Evidence)
	}
}

// TestACheckThatRewritesATrackedFileIsNotEvidence (R9-C6): a check that leaves tracked repairs
// behind in the copy is not evidence for the commit either.
func TestACheckThatRewritesATrackedFileIsNotEvidence(t *testing.T) {
	r := newAcceptRig(t, "true", LandingOptions{})
	ctx := context.Background()
	job := isolationJob(t, r, store.ProjectAccept{CheckCommand: "chmod +x widget.go", CheckIsolated: true})
	if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
		t.Fatal(err)
	}
	d := r.get(job.ID)
	if d.AcceptState == AcceptAccepted || d.Evidence == nil || !strings.Contains(d.Evidence.OutputTail, "executable bit") {
		t.Fatalf("a check that changed a tracked file's mode passed: %s %+v", d.AcceptState, d.Evidence)
	}
}

// TestAnIsolatedPassNamesTheTreeItTested (R9-C6): the record names the commit's tree, verified.
func TestAnIsolatedPassNamesTheTreeItTested(t *testing.T) {
	r := newAcceptRig(t, "true", LandingOptions{})
	ctx := context.Background()
	job := isolationJob(t, r, store.ProjectAccept{CheckCommand: isolationCheck, CheckIsolated: true, CheckPrepare: "touch prepared.txt"})
	if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
		t.Fatal(err)
	}
	if d := r.get(job.ID); d.AcceptState != AcceptAccepted {
		t.Fatalf("setup: %s %q", d.AcceptState, d.AcceptDetail)
	}
	checks, err := r.db.ListChecks(ctx, mustParse(job.ID), 1)
	if err != nil || len(checks) != 1 {
		t.Fatal(checks, err)
	}
	tree := strings.TrimSpace(gitIn(t, job.WorkDir, "rev-parse", checks[0].Commit+"^{tree}"))
	if checks[0].TreeBefore != tree || checks[0].TreeAfter != tree {
		t.Fatalf("the record names %q / %q, the commit's tree is %q", checks[0].TreeBefore, checks[0].TreeAfter, tree)
	}
	if dirs, _ := r.db.CheckWorkspaces(ctx); len(dirs) != 0 {
		t.Fatalf("the copy's directory is still recorded after the run: %v", dirs)
	}
}

// TestACopyACrashLeftIsRemovedAtStart (R9-C7): an isolated copy recorded on a check row by a
// crowsnest that died, and one no row names, are removed on the first tick.
func TestACopyACrashLeftIsRemovedAtStart(t *testing.T) {
	r := newAcceptRig(t, "true", LandingOptions{})
	ctx := context.Background()
	job := isolationJob(t, r, store.ProjectAccept{CheckCommand: "true", CheckIsolated: true})
	commit := strings.TrimSpace(gitIn(t, job.WorkDir, "rev-parse", "HEAD"))
	checkID, err := r.db.InsertCheck(ctx, store.CheckRecord{JobID: mustParse(job.ID), Commit: commit, Command: "true", Status: CheckRunning, Owner: "a-dead-process"})
	if err != nil {
		t.Fatal(err)
	}
	// What the dead process had done: recorded a directory and made the copy in it.
	other := New(r.svc.opts)
	workspace, err := other.newCheckWorkspace(ctx, job.ID, checkID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := isolatedCheckoutForTest(ctx, job.WorkDir, commit, workspace); err != nil {
		t.Fatal(err)
	}
	// One no row names but carrying crowsnest's marker (an older copy): removed. One without the
	// marker: not provably crowsnest's, so left (R10 new-H1).
	stray := filepath.Join(r.svc.checksDir(), job.ID+"-0-stray")
	unmarked := filepath.Join(r.svc.checksDir(), job.ID+"-0-unmarked")
	for _, dir := range []string{stray, unmarked} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(stray, "crowsnest-check-repo"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gitIn(t, job.WorkDir, "worktree", "list"), workspace) {
		t.Fatal("setup: the copy is not a worktree")
	}
	r.svc.Tick(ctx)
	for _, dir := range []string{workspace, stray} {
		if _, err := os.Stat(dir); err == nil {
			t.Fatalf("%s was left behind", dir)
		}
	}
	if _, err := os.Stat(unmarked); err != nil {
		t.Fatalf("a directory without crowsnest's marker was removed: %v", err)
	}
	if strings.Contains(gitIn(t, job.WorkDir, "worktree", "list"), workspace) {
		t.Fatal("git still lists the copy as a worktree")
	}
	if dirs, _ := r.db.CheckWorkspaces(ctx); len(dirs) != 0 {
		t.Fatalf("the row still names the copy: %v", dirs)
	}
	if !strings.Contains(timelineText(r.get(job.ID)), "Removed an isolated check's copy") {
		t.Fatalf("the clean-up is not on the job's timeline: %s", timelineText(r.get(job.ID)))
	}
}

// isolatedCheckoutForTest makes a copy the way a check does.
func isolatedCheckoutForTest(ctx context.Context, dir, commit, parent string) (string, error) {
	copied, err := landing.IsolatedCheckout(ctx, dir, commit, filepath.Dir(parent), parent)
	if err != nil {
		return "", err
	}
	return copied.Dir, nil
}

// TestARecordedFolderOutsideTheChecksDirectoryIsNeverRemoved (R10 new-H1): a job_checks row naming
// any other folder, as a restored backup's can, is left whole at the first tick, journalled, and
// forgotten.
func TestARecordedFolderOutsideTheChecksDirectoryIsNeverRemoved(t *testing.T) {
	r := newAcceptRig(t, "true", LandingOptions{})
	ctx := context.Background()
	job := isolationJob(t, r, store.ProjectAccept{CheckCommand: "true", CheckIsolated: true})
	commit := strings.TrimSpace(gitIn(t, job.WorkDir, "rev-parse", "HEAD"))
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	// Even a marker in it does not make it a copy: it is not inside the checks directory.
	for _, name := range []string{"precious.txt", "crowsnest-check-repo"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte("mine\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(r.svc.checksDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(r.svc.checksDir(), job.ID+"-9-link")
	if err := os.Symlink(home, linked); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{home, linked} {
		checkID, err := r.db.InsertCheck(ctx, store.CheckRecord{JobID: mustParse(job.ID), Commit: commit, Command: "true", Status: CheckRunning, Owner: "a-backup"})
		if err != nil {
			t.Fatal(err)
		}
		if err := r.db.SetCheckWorkspace(ctx, checkID, dir); err != nil {
			t.Fatal(err)
		}
	}
	r.svc.Tick(ctx)
	if _, err := os.Stat(filepath.Join(home, "precious.txt")); err != nil {
		t.Fatalf("the recorded folder was removed: %v", err)
	}
	if _, err := os.Lstat(linked); err != nil {
		t.Fatalf("a link in the checks directory was removed: %v", err)
	}
	if dirs, _ := r.db.CheckWorkspaces(ctx); len(dirs) != 0 {
		t.Fatalf("the rows still name the folders: %v", dirs)
	}
	if !strings.Contains(timelineText(r.get(job.ID)), "Left alone, not removed") {
		t.Fatalf("leaving it is not on the job's timeline: %s", timelineText(r.get(job.ID)))
	}
}
