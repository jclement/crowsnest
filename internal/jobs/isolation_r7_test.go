// Tests for where crowsnest's check runs (R7-C01): a project's isolated check runs in a temporary
// copy of exactly the commit (after its prepare command), so what the agent left in its worktree
// does not reach it, the copy is removed, and the evidence says so; a copy or prepare that fails is
// an error, never a fallback to the worktree; and a worktree check holds the agent's tools while it
// runs.

package jobs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/guard"
	"github.com/jclement/crowsnest/internal/store"
)

// isolationJob is a job in a project over repo, whose committed .gitignore ignores agent-left.txt,
// with widget.go committed and agent-left.txt left (ignored, so the tree is clean) in its worktree.
func isolationJob(t *testing.T, r *rig, accept store.ProjectAccept) Detail {
	t.Helper()
	ctx := context.Background()
	repo := landRepo(t)
	now := time.Now()
	project, err := r.db.InsertProject(ctx, store.ProjectRecord{Slug: "shop", Name: "Shop", Dir: repo, RootDir: repo, LeadStart: "manual", DefaultIsolate: -1, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.db.SetProjectAccept(ctx, project, accept); err != nil {
		t.Fatal(err)
	}
	job := r.create(CreateInput{Brief: "Add a widget", Dir: repo})
	r.edit(job.ID, func(rec *store.JobRecord) { rec.ProjectID = project }) // the rig has no projects service
	writeIn(t, job.WorkDir, ".gitignore", "agent-left.txt\nprepared.txt\n")
	writeIn(t, job.WorkDir, "widget.go", "package widget\n")
	gitIn(t, job.WorkDir, "add", ".")
	gitIn(t, job.WorkDir, "commit", "-q", "-m", "widget")
	writeIn(t, job.WorkDir, "agent-left.txt", "something the agent left behind\n")
	return r.get(job.ID)
}

// The check passes only where agent-left.txt is absent and the prepare command ran.
const isolationCheck = "test -f prepared.txt && test -f widget.go && ! test -e agent-left.txt"

func TestIsolatedCheckTestsOnlyTheCommit(t *testing.T) {
	r := newAcceptRig(t, "true", LandingOptions{})
	ctx := context.Background()
	job := isolationJob(t, r, store.ProjectAccept{CheckCommand: isolationCheck, CheckIsolated: true, CheckPrepare: "touch prepared.txt"})
	worktreesBefore := gitIn(t, job.WorkDir, "worktree", "list")
	if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
		t.Fatal(err)
	}
	d := r.get(job.ID)
	if d.AcceptState != AcceptAccepted {
		t.Fatalf("the isolated check did not pass: %s %q (%+v)", d.AcceptState, d.AcceptDetail, d.Evidence)
	}
	if d.Evidence == nil || d.Evidence.Mode != CheckModeIsolated {
		t.Fatalf("the evidence does not say it was isolated: %+v", d.Evidence)
	}
	if after := gitIn(t, job.WorkDir, "worktree", "list"); after != worktreesBefore {
		t.Fatalf("the isolated copy was left behind:\n%s\nwas\n%s", after, worktreesBefore)
	}
	if _, err := os.Stat(filepath.Join(job.WorkDir, "prepared.txt")); err == nil {
		t.Fatal("the prepare command ran in the job's worktree")
	}
}

func TestWorktreeCheckSeesWhatTheAgentLeft(t *testing.T) {
	r := newAcceptRig(t, "true", LandingOptions{})
	ctx := context.Background()
	job := isolationJob(t, r, store.ProjectAccept{CheckCommand: "touch prepared.txt && " + isolationCheck})
	if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
		t.Fatal(err)
	}
	d := r.get(job.ID)
	if d.AcceptState == AcceptAccepted {
		t.Fatal("the worktree check passed although the agent's file was there: the two modes are not different")
	}
	if d.Evidence == nil || d.Evidence.Mode != CheckModeWorktree {
		t.Fatalf("evidence mode: %+v", d.Evidence)
	}
	checks, err := r.db.ListChecks(ctx, mustParse(job.ID), 1)
	if err != nil || len(checks) != 1 || checks[0].TreeBefore == "" || checks[0].TreeBefore != checks[0].TreeAfter {
		t.Fatalf("the worktree check's fingerprints: %+v %v", checks, err)
	}
}

func TestIsolatedPrepareFailureIsAnError(t *testing.T) {
	r := newAcceptRig(t, "true", LandingOptions{})
	ctx := context.Background()
	job := isolationJob(t, r, store.ProjectAccept{CheckCommand: "true", CheckIsolated: true, CheckPrepare: "false"})
	if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
		t.Fatal(err)
	}
	d := r.get(job.ID)
	if d.AcceptState == AcceptAccepted || !strings.Contains(d.AcceptDetail+d.Evidence.OutputTail, "prepare command") {
		t.Fatalf("a failed prepare: %s %q %q", d.AcceptState, d.AcceptDetail, d.Evidence.OutputTail)
	}
}

func TestWorktreeCheckHoldsTheAgentsTools(t *testing.T) {
	r := newAcceptRig(t, "true", LandingOptions{})
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Add a widget", Dir: landRepo(t)})
	marker := r.svc.checkingMarker(job.ID)
	if marker == "" {
		t.Fatal("no marker path")
	}
	rules := filepath.Join(filepath.Dir(marker), "guard.json")
	result := r.svc.execWorktreeCheck(ctx, job.ID, job.WorkDir, "test -f '"+marker+"'")
	if result.status != CheckPassed {
		t.Fatalf("the marker was not there while the check ran: %+v %s", result, result.output)
	}
	if guard.HeldByCheck(rules, time.Now()) {
		t.Fatal("the agent's tools are still held after the check")
	}
	if err := os.WriteFile(marker, []byte(time.Now().Add(time.Minute).UTC().Format(time.RFC3339)), 0o600); err != nil {
		t.Fatal(err)
	}
	if !guard.HeldByCheck(rules, time.Now()) || guard.HeldByCheck(rules, time.Now().Add(2*time.Minute)) {
		t.Fatal("the hold does not follow its deadline")
	}
}
