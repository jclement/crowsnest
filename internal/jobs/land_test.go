package jobs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/store"
)

// landRepo is a real repository with one commit on main, for jobs whose worktrees the fake
// creates and removes with real git (fake.RealGitWorktrees).
func landRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for key, value := range map[string]string{"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1", "GIT_AUTHOR_NAME": "T",
		"GIT_AUTHOR_EMAIL": "t@example.com", "GIT_COMMITTER_NAME": "T", "GIT_COMMITTER_EMAIL": "t@example.com"} {
		t.Setenv(key, value)
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	writeIn(t, dir, "README.md", "hello\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput() //nolint:gosec // test fixture
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeIn(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
}

func newLandRig(t *testing.T, landingOpts LandingOptions, change func(*Options)) *rig {
	t.Helper()
	r := newRig(t, func(o *Options) {
		o.Landing = landingOpts
		o.Launch.WorktreeRoot = t.TempDir()
		if change != nil {
			change(o)
		}
	})
	r.fake.RealGitWorktrees()
	return r
}

var done = DoneInput{ResultMD: "Added the widget.", Verified: []string{"go test"}, NotVerified: []string{}, WalkthroughMD: "## widget.go\nThe widget."}

// TestReviewGateRounds: done → in review with a reviewer job in the same worktree; changes →
// back to the builder with the findings typed; a second done → round 2; approve → done.
func TestReviewGateRounds(t *testing.T) {
	r := newLandRig(t, LandingOptions{ReviewDefault: ReviewDefaultSame}, nil)
	ctx := context.Background()
	repo := landRepo(t)
	job := r.create(CreateInput{Brief: "Add a widget", Dir: repo})
	if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
		t.Fatal(err)
	}
	d := r.get(job.ID)
	if d.State != StateInReview || d.ReviewRound != 1 || d.WalkthroughMD == "" {
		t.Fatalf("after done: %s round %d", d.State, d.ReviewRound)
	}
	reviewers, _ := r.db.ListReviewJobs(ctx, mustParse(job.ID))
	if len(reviewers) != 1 || reviewers[0].Role != "reviewer" || reviewers[0].Dir != d.WorkDir || reviewers[0].Isolate {
		t.Fatalf("reviewer %+v", reviewers)
	}
	reviewer := FormatID(reviewers[0].ID)
	if brief := r.get(reviewer).BriefMD; !strings.Contains(brief, "READ ONLY") || !strings.Contains(brief, "VERDICT: approve") {
		t.Fatalf("reviewer brief %q", brief)
	}
	if _, err := r.svc.Done(ctx, job.ID, done); err == nil {
		t.Fatal("done while in review must be refused")
	}
	if _, err := r.svc.Done(ctx, reviewer, DoneInput{ResultMD: "VERDICT: changes\n- widget.go:3 no error check", Verified: []string{}, NotVerified: []string{}}); err != nil {
		t.Fatal(err)
	}
	d = r.get(job.ID)
	if d.State != StateRunning || d.ReviewVerdict != VerdictChanges || !strings.Contains(d.ReviewMD, "no error check") {
		t.Fatalf("after changes: %s %s", d.State, d.ReviewVerdict)
	}
	prompts := r.fake.Prompts()
	if last := prompts[len(prompts)-1]; last.PaneID != d.PaneID || !strings.HasPrefix(last.Text, "[crowsnest review "+reviewer+"]") {
		t.Fatalf("findings not typed to the builder: %+v", last)
	}
	if text, _ := r.svc.GetForAgent(ctx, job.ID); !strings.Contains(text, "## Review, round 1") {
		t.Fatal("job_get does not show the findings")
	}
	if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
		t.Fatal(err)
	}
	reviewers, _ = r.db.ListReviewJobs(ctx, mustParse(job.ID))
	if d = r.get(job.ID); d.State != StateInReview || d.ReviewRound != 2 || len(reviewers) != 2 {
		t.Fatalf("round 2: %s %d reviewers %d", d.State, d.ReviewRound, len(reviewers))
	}
	if _, err := r.svc.Done(ctx, FormatID(reviewers[1].ID), DoneInput{ResultMD: "VERDICT: approve\nLooks right.", Verified: []string{}, NotVerified: []string{}}); err != nil {
		t.Fatal(err)
	}
	if d = r.get(job.ID); d.State != StateDone || d.ReviewVerdict != VerdictApprove {
		t.Fatalf("after approve: %s %s", d.State, d.ReviewVerdict)
	}
}

func TestReviewStillObjectingNeedsYou(t *testing.T) {
	r := newLandRig(t, LandingOptions{ReviewDefault: ReviewDefaultSame}, nil)
	ctx := context.Background()
	job := r.create(CreateInput{Brief: "Add a widget", Dir: landRepo(t)})
	for round := 1; round <= maxReviewRounds; round++ {
		if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
			t.Fatal(err)
		}
		reviewers, _ := r.db.ListReviewJobs(ctx, mustParse(job.ID))
		if _, err := r.svc.Done(ctx, FormatID(reviewers[round-1].ID), DoneInput{ResultMD: "no verdict line, just complaints", Verified: []string{}, NotVerified: []string{}}); err != nil {
			t.Fatal(err)
		}
	}
	d := r.get(job.ID)
	if d.State != StateNeedsYou || d.Cause != CauseReview || !strings.Contains(d.CauseDetail, "still objects") {
		t.Fatalf("after two objecting rounds: %s %s %q", d.State, d.Cause, d.CauseDetail)
	}
}

func TestReviewerDefaults(t *testing.T) {
	s := &Service{}
	base := func(engine string) storeRecord {
		return storeRecord{WorktreeBranch: "job/1-x", WorkDir: "/w", Role: "builder", Engine: engine}
	}
	cases := []struct {
		def    string
		codex  bool
		record storeRecord
		want   string
	}{
		{ReviewDefaultAuto, true, base(EngineClaude), EngineCodex},
		{ReviewDefaultAuto, false, base(EngineClaude), EngineClaude},
		{ReviewDefaultAuto, false, base(EngineCodex), EngineClaude},
		{ReviewDefaultOff, true, base(EngineClaude), ""},
		{ReviewDefaultSame, true, base(EngineCodex), EngineCodex},
		{ReviewDefaultOff, true, func() storeRecord { b := base(EngineClaude); b.Reviewer = EngineCodex; return b }(), EngineCodex},
		{ReviewDefaultAuto, true, func() storeRecord { b := base(EngineClaude); b.Reviewer = ReviewerOff; return b }(), ""},
		{ReviewDefaultAuto, true, func() storeRecord { b := base(EngineClaude); b.WorktreeBranch = ""; return b }(), ""},
		{ReviewDefaultAuto, true, func() storeRecord { b := base(EngineClaude); b.Role = "tester"; return b }(), ""},
	}
	for i, tc := range cases {
		s.opts.Landing = LandingOptions{ReviewDefault: tc.def, CodexInstalled: tc.codex}
		if got := s.reviewerFor(tc.record); got != tc.want {
			t.Errorf("case %d: %q, want %q", i, got, tc.want)
		}
	}
}

func TestParseVerdict(t *testing.T) {
	for text, want := range map[string]string{"VERDICT: approve\nok": VerdictApprove, "**Verdict:** changes requested": VerdictChanges,
		"verdict: LGTM": VerdictApprove, "no line here": VerdictChanges, "VERDICT: changes": VerdictChanges} {
		if got := ParseVerdict(text); got != want {
			t.Errorf("%q: %s, want %s", text, got, want)
		}
	}
}

// landedJob makes a done job with one commit on its branch.
func landedJob(t *testing.T, r *rig, repo string) Detail {
	t.Helper()
	job := r.create(CreateInput{Brief: "Add a widget", Dir: repo})
	writeIn(t, job.WorkDir, "widget.go", "package widget\n")
	gitIn(t, job.WorkDir, "add", ".")
	gitIn(t, job.WorkDir, "commit", "-q", "-m", "widget")
	if _, err := r.svc.Done(context.Background(), job.ID, done); err != nil {
		t.Fatal(err)
	}
	return r.get(job.ID)
}

func TestLandMergeTearsDown(t *testing.T) {
	r := newLandRig(t, LandingOptions{MergeMode: "merge"}, nil)
	ctx := context.Background()
	repo := landRepo(t)
	job := landedJob(t, r, repo)
	if job.BaseBranch != "main" || job.State != StateDone {
		t.Fatalf("job %+v", job.Job)
	}
	view, err := r.svc.ReviewOf(ctx, job.ID)
	if err != nil || view.Produced == nil || view.Produced.Ahead != 1 || !view.Land.Allowed[LandMerge] || view.Land.Confirm[LandMerge] == "" {
		t.Fatalf("review view %+v %v", view, err)
	}
	if diff, err := r.svc.FileDiff(ctx, job.ID, "widget.go"); err != nil || !strings.Contains(diff.Diff, "+package widget") {
		t.Fatalf("diff %+v %v", diff, err)
	}
	if _, err := r.svc.FileDiff(ctx, job.ID, "../etc/passwd"); err == nil {
		t.Fatal("a file the job did not change must not be served")
	}
	result, err := r.svc.Land(ctx, job.ID, LandMerge, "you")
	if err != nil {
		t.Fatal(err)
	}
	if !result.TornDown || result.Job.LandState != LandedMerged || result.Job.TornDownAt == nil {
		t.Fatalf("result %+v", result)
	}
	if _, err := os.Stat(filepath.Join(repo, "widget.go")); err != nil {
		t.Fatal("the base branch does not have the commit")
	}
	if _, err := os.Stat(job.WorkDir); !os.IsNotExist(err) {
		t.Fatal("the worktree is still there")
	}
	if out := gitIn(t, repo, "branch", "--list", job.WorktreeBranch); out != "" {
		t.Fatalf("the branch was not deleted: %q", out)
	}
	// Teardown order: the agent was stopped (it got /exit) before herdr removed the worktree.
	prompts := r.fake.Prompts()
	if prompts[len(prompts)-1].Text != "/exit" || len(r.fake.Removed()) != 1 {
		t.Fatalf("teardown: prompts %v removed %v", prompts[len(prompts)-1], r.fake.Removed())
	}
	d := r.get(job.ID)
	if !hasEvent(d, EventLand, "merged by you") || !hasEvent(d, EventLand, "Teardown finished") {
		t.Fatal("the land is not on the timeline")
	}
	if _, err := r.svc.Land(ctx, job.ID, LandMerge, "you"); err == nil {
		t.Fatal("landing twice must be refused")
	}
	if sweep, _ := r.svc.Sweep(ctx); len(sweep) != 0 {
		t.Fatalf("a torn-down job in the sweep: %+v", sweep)
	}
}

func TestLandRefusals(t *testing.T) {
	ctx := context.Background()
	t.Run("protected base", func(t *testing.T) {
		r := newLandRig(t, LandingOptions{Protected: []string{"main"}}, nil)
		job := landedJob(t, r, landRepo(t))
		if _, err := r.svc.Land(ctx, job.ID, LandMerge, "you"); err == nil || !strings.Contains(err.Error(), "protected") {
			t.Fatalf("err %v", err)
		}
		if view, _ := r.svc.ReviewOf(ctx, job.ID); view.Land.Allowed[LandMerge] || !view.Land.Allowed[LandPR] {
			t.Fatalf("land options %+v", view.Land)
		}
	})
	t.Run("running job", func(t *testing.T) {
		r := newLandRig(t, LandingOptions{}, nil)
		job := r.create(CreateInput{Brief: "Add a widget", Dir: landRepo(t)})
		if _, err := r.svc.Land(ctx, job.ID, LandMerge, "you"); err == nil {
			t.Fatal("a running job must not be merged")
		}
		if _, err := r.svc.Land(ctx, job.ID, LandDiscard, "you"); err == nil {
			t.Fatal("a running job must not be discarded")
		}
	})
	t.Run("conflict leaves everything", func(t *testing.T) {
		r := newLandRig(t, LandingOptions{}, nil)
		repo := landRepo(t)
		job := r.create(CreateInput{Brief: "Edit the readme", Dir: repo})
		writeIn(t, job.WorkDir, "README.md", "job\n")
		gitIn(t, job.WorkDir, "commit", "-q", "-am", "job")
		writeIn(t, repo, "README.md", "main\n")
		gitIn(t, repo, "commit", "-q", "-am", "main")
		if _, err := r.svc.Done(ctx, job.ID, done); err != nil {
			t.Fatal(err)
		}
		if _, err := r.svc.Land(ctx, job.ID, LandMerge, "you"); err == nil || !strings.Contains(err.Error(), "conflicts") {
			t.Fatalf("err %v", err)
		}
		if d := r.get(job.ID); d.LandState != "" || d.TornDownAt != nil || len(r.fake.Removed()) != 0 {
			t.Fatal("a refused merge changed the job or tore it down")
		}
	})
	t.Run("discard refuses a dirty worktree", func(t *testing.T) {
		r := newLandRig(t, LandingOptions{}, nil)
		job := landedJob(t, r, landRepo(t))
		writeIn(t, job.WorkDir, "half.go", "unfinished")
		if _, err := r.svc.Land(ctx, job.ID, LandDiscard, "you"); err == nil || !strings.Contains(err.Error(), "uncommitted") {
			t.Fatalf("err %v", err)
		}
		if sweep, _ := r.svc.Sweep(ctx); len(sweep) != 1 || !sweep[0].Dirty {
			t.Fatalf("sweep %+v", sweep)
		}
		_ = os.Remove(filepath.Join(job.WorkDir, "half.go"))
		result, err := r.svc.Land(ctx, job.ID, LandDiscard, "you")
		if err != nil || !result.TornDown || result.Job.LandState != LandedDiscarded {
			t.Fatalf("discard: %+v %v", result, err)
		}
	})
}

// storeRecord is store.JobRecord, named here to keep the defaults table short.
type storeRecord = store.JobRecord
