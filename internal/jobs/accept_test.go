package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/store"
)

// newAcceptRig is a landing rig with acceptance on and checkCommand as the server's default
// check (tests have no mise): the check runs synchronously, in the job's real git worktree.
func newAcceptRig(t *testing.T, checkCommand string, landingOpts LandingOptions) *rig {
	t.Helper()
	return newLandRig(t, landingOpts, func(o *Options) {
		o.Accept = AcceptOptions{Enabled: true, DefaultCommand: checkCommand, MaxRounds: 2}
	})
}

// commitIn writes a file in the job's worktree and commits it.
func commitIn(t *testing.T, dir, name, body string) {
	t.Helper()
	writeIn(t, dir, name, body)
	gitIn(t, dir, "add", name)
	gitIn(t, dir, "commit", "-q", "-m", "add "+name)
}

// TestAcceptRefusesWhatIsVisiblyUnfinished: a dirty tree and a branch with no commits are
// refused at done, with what to do; a reason for no commits lets it through.
func TestAcceptRefusesWhatIsVisiblyUnfinished(t *testing.T) {
	r := newAcceptRig(t, "true", LandingOptions{})
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Add a widget", Dir: landRepo(t)})
	if _, err := r.svc.Done(ctx, job.ID, done); err == nil || !strings.Contains(err.Error(), "no commits ahead of main") {
		t.Fatalf("no commits: %v", err)
	}
	writeIn(t, job.WorkDir, "loose.txt", "uncommitted\n")
	if _, err := r.svc.Done(ctx, job.ID, done); err == nil || !strings.Contains(err.Error(), "uncommitted changes") || !strings.Contains(err.Error(), "loose.txt") {
		t.Fatalf("dirty tree: %v", err)
	}
	if d := r.get(job.ID); d.State != StateRunning {
		t.Fatalf("a refused done changed the job: %s", d.State)
	}
	if err := os.Remove(filepath.Join(job.WorkDir, "loose.txt")); err != nil {
		t.Fatal(err)
	}
	withReason := done
	withReason.NoCommitsReason = "the widget already existed"
	if _, err := r.svc.Done(ctx, job.ID, withReason); err != nil {
		t.Fatal(err)
	}
	if d := r.get(job.ID); d.State != StateDone || d.AcceptState != AcceptAccepted || d.NoCommitsReason == "" {
		t.Fatalf("after done with a reason: %s %s", d.State, d.AcceptState)
	}
}

// TestAcceptCheckPassFailStale: the table of what crowsnest's check decides.
func TestAcceptCheckPassFailStale(t *testing.T) {
	cases := []struct {
		name       string
		check      string
		wantState  State
		wantAccept string
		wantCause  Cause
		typed      bool // a [crowsnest check] line reached the agent
	}{
		{name: "pass", check: "test -f widget.go", wantState: StateDone, wantAccept: AcceptAccepted},
		{name: "fail goes back to the agent", check: "echo 'widget_test.go:3: expected 2, got 3' && exit 1", wantState: StateRunning, wantAccept: AcceptFailed, typed: true},
		{name: "no check: finished, unverified", check: "", wantState: StateDone, wantAccept: AcceptUnverified},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newAcceptRig(t, tc.check, LandingOptions{})
			ctx := context.Background()
			job := r.create(CreateInput{Brief: "Add a widget", Dir: landRepo(t)})
			commitIn(t, job.WorkDir, "widget.go", "package widget\n")
			if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
				t.Fatal(err)
			}
			d := r.get(job.ID)
			if d.State != tc.wantState || d.AcceptState != tc.wantAccept || d.Cause != tc.wantCause {
				t.Fatalf("got %s/%s/%s, want %s/%s/%s (%s)", d.State, d.AcceptState, d.Cause, tc.wantState, tc.wantAccept, tc.wantCause, d.AcceptDetail)
			}
			if tc.check != "" {
				head := gitIn(t, job.WorkDir, "rev-parse", "HEAD")
				if d.Evidence == nil || d.Evidence.Commit != head || d.Evidence.Command != tc.check || d.Evidence.Stale {
					t.Fatalf("evidence %+v (head %s)", d.Evidence, head)
				}
			}
			typed := false
			for _, prompt := range r.fake.Prompts() {
				if strings.HasPrefix(prompt.Text, "[crowsnest check]") {
					typed = true
					if !strings.Contains(prompt.Text, "expected 2, got 3") || !strings.Contains(prompt.Text, "round 1 of 2") {
						t.Fatalf("the failure line %q", prompt.Text)
					}
				}
			}
			if typed != tc.typed {
				t.Fatalf("typed a check failure: %v, want %v", typed, tc.typed)
			}
		})
	}
}

// TestAcceptRoundsThenHuman: after MaxRounds failed checks the job needs the human, cause check.
func TestAcceptRoundsThenHuman(t *testing.T) {
	r := newAcceptRig(t, "exit 3", LandingOptions{})
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Add a widget", Dir: landRepo(t)})
	commitIn(t, job.WorkDir, "widget.go", "package widget\n")
	for round := 1; round <= 2; round++ {
		if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
	d := r.get(job.ID)
	if d.State != StateNeedsYou || d.Cause != CauseCheck || d.CheckRounds != 2 || d.Evidence == nil || d.Evidence.ExitCode != 3 {
		t.Fatalf("after two failed rounds: %s %s rounds %d evidence %+v", d.State, d.Cause, d.CheckRounds, d.Evidence)
	}
	if len(r.Notices()) == 0 {
		t.Fatal("the human was not told")
	}
	if _, err := r.svc.Update(ctx, job.ID, UpdateInput{Progress: "fine", State: "running"}); err != nil {
		t.Fatal(err)
	}
	if d = r.get(job.ID); d.State != StateNeedsYou {
		t.Fatal("the agent lifted a failed check by declaring running")
	}
}

// TestAcceptStaleAndMovedBaseRefuseLand: a commit after the evidence makes it stale, and a base
// branch that moved needs a rebase; Land refuses both without the human's override.
func TestAcceptStaleAndMovedBaseRefuseLand(t *testing.T) {
	t.Run("stale", func(t *testing.T) {
		r := newAcceptRig(t, "true", LandingOptions{MergeMode: "merge"})
		ctx := context.Background()
		repo := landRepo(t)
		job := r.create(CreateInput{Brief: "Add a widget", Dir: repo})
		commitIn(t, job.WorkDir, "widget.go", "package widget\n")
		if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
			t.Fatal(err)
		}
		commitIn(t, job.WorkDir, "later.go", "package widget\n")
		if d := r.get(job.ID); d.AcceptState != AcceptStale || d.Evidence == nil || !d.Evidence.Stale {
			t.Fatalf("after a later commit: %s %+v", d.AcceptState, d.Evidence)
		}
		_, err := r.svc.Land(ctx, job.ID, LandMerge, "you")
		var evidence *EvidenceError
		if !errors.As(err, &evidence) || evidence.Code != AcceptStale {
			t.Fatalf("land of stale evidence: %v", err)
		}
		if _, err := os.Stat(filepath.Join(repo, "widget.go")); !os.IsNotExist(err) {
			t.Fatal("a refused land merged anyway")
		}
		// Running the check again makes it accepted, and Land goes through.
		if _, err := r.svc.Recheck(ctx, job.ID); err != nil {
			t.Fatal(err)
		}
		if d := r.get(job.ID); d.AcceptState != AcceptAccepted {
			t.Fatalf("after the re-check: %s", d.AcceptState)
		}
		if _, err := r.svc.Land(ctx, job.ID, LandMerge, "you"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("moved base", func(t *testing.T) {
		r := newAcceptRig(t, "true", LandingOptions{MergeMode: "merge"})
		ctx := context.Background()
		repo := landRepo(t)
		job := r.create(CreateInput{Brief: "Add a widget", Dir: repo})
		commitIn(t, job.WorkDir, "widget.go", "package widget\n")
		if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
			t.Fatal(err)
		}
		commitIn(t, repo, "other.md", "someone else's work\n")
		_, err := r.svc.Land(ctx, job.ID, LandMerge, "you")
		var evidence *EvidenceError
		if !errors.As(err, &evidence) || evidence.Code != AcceptNeedsRebase {
			t.Fatalf("land over a moved base: %v", err)
		}
		if d := r.get(job.ID); d.AcceptState != AcceptNeedsRebase {
			t.Fatalf("accept state %s", d.AcceptState)
		}
		view, _ := r.svc.ReviewOf(ctx, job.ID)
		if view.Land.Evidence != AcceptNeedsRebase {
			t.Fatalf("the Review tab does not say it needs a rebase: %+v", view.Land)
		}
		// Ask an agent to rebase: a fix job in the worktree; when it is done the job is re-checked.
		fix, err := r.svc.AskRebase(ctx, job.ID)
		if err != nil || fix.FixOf != job.ID {
			t.Fatalf("rebase job %+v %v", fix, err)
		}
		gitIn(t, job.WorkDir, "merge", "-q", "--no-edit", "main")
		if _, err := r.svc.Done(ctx, fix.ID, DoneInput{ResultMD: "merged main, no conflicts", Verified: []string{}, NotVerified: []string{}}); err != nil {
			t.Fatal(err)
		}
		if d := r.get(job.ID); d.AcceptState != AcceptAccepted {
			t.Fatalf("after the rebase: %s (%s)", d.AcceptState, d.AcceptDetail)
		}
		// The human may override explicitly too.
		if _, err := r.svc.LandWith(ctx, job.ID, LandMerge, "you", true); err != nil {
			t.Fatal(err)
		}
	})
}

// TestAcceptThenReviewTheRevisionThatPassed: with a review gate, the reviewer starts only after
// the check passed, pinned to that commit; an approval makes it done.
func TestAcceptThenReviewTheRevisionThatPassed(t *testing.T) {
	r := newAcceptRig(t, "true", LandingOptions{ReviewDefault: ReviewDefaultSame})
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Add a widget", Dir: landRepo(t)})
	commitIn(t, job.WorkDir, "widget.go", "package widget\n")
	if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
		t.Fatal(err)
	}
	d := r.get(job.ID)
	if d.State != StateInReview || d.AcceptState != AcceptAccepted || d.ReviewRound != 1 {
		t.Fatalf("after the check: %s %s round %d", d.State, d.AcceptState, d.ReviewRound)
	}
	record, _ := r.db.GetJob(ctx, mustParse(job.ID))
	if record.ReviewCommit != d.Evidence.Commit {
		t.Fatalf("the review is not pinned to the checked commit: %s vs %s", record.ReviewCommit, d.Evidence.Commit)
	}
	reviewers, _ := r.db.ListReviewJobs(ctx, mustParse(job.ID))
	if len(reviewers) != 1 {
		t.Fatalf("reviewers %d", len(reviewers))
	}
	if _, err := r.svc.Done(ctx, FormatID(reviewers[0].ID), DoneInput{ResultMD: "VERDICT: approve", Verified: []string{}, NotVerified: []string{}}); err != nil {
		t.Fatal(err)
	}
	if d = r.get(job.ID); d.State != StateDone || d.AcceptState != AcceptAccepted {
		t.Fatalf("after approve: %s %s", d.State, d.AcceptState)
	}
}

// TestAcceptCheckSurvivesARestart: a job waiting on a check nobody runs (crowsnest restarted
// mid-check) is checked again by the tick.
func TestAcceptCheckSurvivesARestart(t *testing.T) {
	r := newAcceptRig(t, "true", LandingOptions{})
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Add a widget", Dir: landRepo(t)})
	commitIn(t, job.WorkDir, "widget.go", "package widget\n")
	r.edit(job.ID, func(rec *store.JobRecord) {
		rec.State, rec.AcceptState, rec.ResultMD = string(StateInReview), AcceptChecking, "done"
	})
	r.svc.Tick(ctx)
	if d := r.get(job.ID); d.State != StateDone || d.AcceptState != AcceptAccepted {
		t.Fatalf("after the tick: %s %s", d.State, d.AcceptState)
	}
}

// TestAcceptOffKeepsThePlainDone: no directory, or acceptance off, is the agent's word as before.
func TestAcceptOffKeepsThePlainDone(t *testing.T) {
	r := newAcceptRig(t, "false", LandingOptions{})
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Write the release notes"})
	r.edit(job.ID, func(rec *store.JobRecord) { rec.State, rec.PaneID, rec.AgentName = string(StateRunning), "w9:p9", "job-1" })
	if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
		t.Fatal(err)
	}
	if d := r.get(job.ID); d.State != StateDone || d.AcceptState != AcceptNone {
		t.Fatalf("a job with no directory: %s %q", d.State, d.AcceptState)
	}
}

func TestAutoCheckCommand(t *testing.T) {
	cases := map[string]string{
		"[tasks.check]\nrun = \"go test\"\n":           "mise run check",
		"[tasks.\"check\"]\nrun = \"x\"\n":             "mise run check",
		"[tasks]\nlint = \"x\"\ncheck = \"go test\"\n": "mise run check",
		"[tasks.checkout]\nrun = \"x\"\n":              "",
		"[tasks.test]\nrun = \"x\"\n":                  "",
	}
	for body, want := range cases {
		dir := t.TempDir()
		writeIn(t, dir, "mise.toml", body)
		if got := AutoCheckCommand(dir); got != want {
			t.Errorf("%q: got %q, want %q", body, got, want)
		}
	}
	if got := AutoCheckCommand(t.TempDir()); got != "" {
		t.Errorf("no mise.toml: %q", got)
	}
}
