package jobs

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// TestRecheckRefusesADirtyTree (R6-C02): the human's Recheck has done's prerequisite, so an
// uncommitted fix cannot be checked and then credited to the commit without it.
func TestRecheckRefusesADirtyTree(t *testing.T) {
	r := newAcceptRig(t, "test -f widget.go", LandingOptions{})
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Add a widget", Dir: landRepo(t)})
	commitIn(t, job.WorkDir, "widget.go", "package widget\n")
	if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
		t.Fatal(err)
	}
	writeIn(t, job.WorkDir, "fix.go", "package widget // uncommitted\n")
	if _, err := r.svc.Recheck(ctx, job.ID); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("a recheck over a dirty tree: %v", err)
	}
}

// TestDirtyTreeAtCheckStartIsNotChecked (R6-C02): a tree that became dirty between done and the
// check run is not checked; the human is told why.
func TestDirtyTreeAtCheckStartIsNotChecked(t *testing.T) {
	r := newAcceptRig(t, "true", LandingOptions{})
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Add a widget", Dir: landRepo(t)})
	commitIn(t, job.WorkDir, "widget.go", "package widget\n")
	r.edit(job.ID, func(rec *store.JobRecord) {
		rec.State, rec.AcceptState = string(StateInReview), AcceptChecking
		setHolds(rec, []Hold{{Kind: HoldChecking, Lift: LiftCrowsnest, Why: "checking"}})
	})
	writeIn(t, job.WorkDir, "late.go", "package widget\n")
	r.svc.runCheck(ctx, job.ID)
	d := r.get(job.ID)
	if d.AcceptState == AcceptAccepted || d.State != StateNeedsYou || d.Cause != CauseCheck || !strings.Contains(d.CauseDetail, "uncommitted") {
		t.Fatalf("a dirty tree was checked: %s/%s/%s %q", d.State, d.Cause, d.AcceptState, d.CauseDetail)
	}
}

// TestCheckThatChangesTheTreeIsNotEvidence (R6-C02, R6-S4): a check (or a process beside it)
// that leaves tracked files changed did not test the commit; it is not accepted.
func TestCheckThatChangesTheTreeIsNotEvidence(t *testing.T) {
	r := newAcceptRig(t, "echo 'package widget // rewritten' > widget.go", LandingOptions{})
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Add a widget", Dir: landRepo(t)})
	commitIn(t, job.WorkDir, "widget.go", "package widget\n")
	if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
		t.Fatal(err)
	}
	d := r.get(job.ID)
	if d.AcceptState == AcceptAccepted || d.State == StateDone {
		t.Fatalf("a check that rewrote the tree was accepted: %s/%s", d.State, d.AcceptState)
	}
	if !strings.Contains(d.AcceptDetail, "changed while the check ran") {
		t.Fatalf("not said why: %q", d.AcceptDetail)
	}
}

// TestPassNotRecordedIsNotAccepted (R6-C13): a passing result that is not the job's durable
// latest check (its FinishCheck write failed) does not accept the job.
func TestPassNotRecordedIsNotAccepted(t *testing.T) {
	r := newAcceptRig(t, "true", LandingOptions{})
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Add a widget", Dir: landRepo(t)})
	commitIn(t, job.WorkDir, "widget.go", "package widget\n")
	head := gitIn(t, job.WorkDir, "rev-parse", "HEAD")
	r.edit(job.ID, func(rec *store.JobRecord) {
		rec.State, rec.AcceptState = string(StateInReview), AcceptChecking
		setHolds(rec, []Hold{{Kind: HoldChecking, Lift: LiftCrowsnest, Why: "checking"}})
	})
	id, err := r.db.InsertCheck(ctx, store.CheckRecord{JobID: mustParse(job.ID), Commit: head, Command: "true", Status: CheckRunning, ExitCode: -1, StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	// The run passed, but its result never reached the database.
	r.svc.checkFinished(ctx, job.ID, store.CheckRecord{ID: id, JobID: mustParse(job.ID), Commit: head, Command: "true", Status: CheckPassed, ExitCode: 0})
	if d := r.get(job.ID); d.AcceptState == AcceptAccepted || d.State == StateDone {
		t.Fatalf("accepted on a result only in memory: %s/%s", d.State, d.AcceptState)
	}
}

// TestCheckDefinitionChangeIsShown (R6-S4): a branch that changes its own check definition gets
// evidence that says so.
func TestCheckDefinitionChangeIsShown(t *testing.T) {
	r := newAcceptRig(t, "true", LandingOptions{})
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Add a widget", Dir: landRepo(t)})
	commitIn(t, job.WorkDir, "Makefile", "check:\n\ttrue\n")
	if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
		t.Fatal(err)
	}
	d := r.get(job.ID)
	if d.AcceptState != AcceptAccepted || d.Evidence == nil || !slices.Contains(d.Evidence.DefinitionChanged, "Makefile") {
		t.Fatalf("evidence %+v (%s)", d.Evidence, d.AcceptState)
	}
	if !strings.Contains(d.AcceptDetail, "changed its own check definition") {
		t.Fatalf("detail %q", d.AcceptDetail)
	}
}

// TestMovingHeadReRunsAreBounded (R6-S8): a branch that keeps moving while its check runs is
// re-run a bounded number of times, then put in front of the human.
func TestMovingHeadReRunsAreBounded(t *testing.T) {
	r := newAcceptRig(t, "git commit -q --allow-empty -m tick", LandingOptions{})
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Add a widget", Dir: landRepo(t)})
	commitIn(t, job.WorkDir, "widget.go", "package widget\n")
	if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if d := r.get(job.ID); d.State == StateNeedsYou {
			if d.Cause != CauseCheck || !strings.Contains(d.CauseDetail, "HEAD moved") {
				t.Fatalf("held for %s: %q", d.Cause, d.CauseDetail)
			}
			checks, _ := r.db.ListChecks(ctx, mustParse(job.ID), 20)
			if len(checks) != maxCheckReruns+1 {
				t.Fatalf("%d runs, want %d", len(checks), maxCheckReruns+1)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("still re-running: %s", r.get(job.ID).AcceptDetail)
}

// TestCheckEnvDropsCredentials (R6-S12): the check does not inherit crowsnest's socket, session
// or an operator's tokens.
func TestCheckEnvDropsCredentials(t *testing.T) {
	t.Setenv("CROWSNEST_SOCKET", "/run/x.sock")
	t.Setenv("CROWSNEST_SESSION", "s")
	t.Setenv("GH_TOKEN", "secret")
	t.Setenv("MY_API_KEY", "secret")
	t.Setenv("KEEP_ME", "yes")
	env := strings.Join(checkEnv(map[string]string{"CROWSNEST_HEAVY_DIR": "/h"}), "\n")
	for _, gone := range []string{"CROWSNEST_SOCKET=", "CROWSNEST_SESSION=", "GH_TOKEN=", "MY_API_KEY="} {
		if strings.Contains(env, gone) {
			t.Fatalf("%s reached the check", gone)
		}
	}
	for _, kept := range []string{"KEEP_ME=yes", "CROWSNEST_HEAVY_DIR=/h", "CI=1"} {
		if !strings.Contains(env, kept) {
			t.Fatalf("%s missing", kept)
		}
	}
}

// TestAutoCheckIgnoresALinkedMiseFile (R6-S4): a committed mise.toml that is a link is not read.
func TestAutoCheckIgnoresALinkedMiseFile(t *testing.T) {
	dir := t.TempDir()
	target := dir + "/real.toml"
	if err := os.WriteFile(target, []byte("[tasks.check]\nrun = \"true\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, dir+"/mise.toml"); err != nil {
		t.Fatal(err)
	}
	if got := AutoCheckCommand(dir); got != "" {
		t.Fatalf("followed a link: %q", got)
	}
}
