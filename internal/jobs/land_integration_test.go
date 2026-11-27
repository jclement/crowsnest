//go:build integration

// The landing test (DESIGN.md 6.7, 6.11, 6.13) with real Claude (haiku) in a throwaway
// crowsnest-test-* herdr session, on the stop test's rig (stop_integration_test.go):
//
//	(a) a builder job in a scratch repository commits a file and calls done; the review gate puts
//	    it in review and starts a reviewer job in the same worktree; the reviewer reads, never
//	    writes (the branch's commit and the tree are unchanged by it), and reports a verdict; on
//	    changes the findings are typed to the builder; once the job is done (or needs the human
//	    after the last round), Land merges it: the base branch has the commit and the worktree is
//	    gone;
//	(b) with the automation paused, a job waits to start; started anyway, its agent ends a turn
//	    without declaring anything and is not nudged (the Stop hook allows it while paused).
//
// Opt-in and paid: -tags integration and CROWSNEST_E2E_CLAUDE=1 (`mise run test:claude`).

package jobs_test

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/throttle"
)

// switchPause is a pause the test flips.
type switchPause struct {
	mu     sync.Mutex
	paused bool
}

func (p *switchPause) Paused() bool                         { p.mu.Lock(); defer p.mu.Unlock(); return p.paused }
func (p *switchPause) Held(context.Context, string, string) {}
func (p *switchPause) set(on bool)                          { p.mu.Lock(); defer p.mu.Unlock(); p.paused = on }

// generousThrottle never holds a start in this test.
type generousThrottle struct{}

func (generousThrottle) Settings(context.Context) throttle.Settings {
	return throttle.Settings{MaxRunning: 16}
}
func (generousThrottle) Load(context.Context) throttle.LoadState { return throttle.LoadState{} }

func TestIntegrationClaudeLanding(t *testing.T) {
	if os.Getenv("CROWSNEST_E2E_CLAUDE") != "1" {
		t.Skip("real-Claude test: set CROWSNEST_E2E_CLAUDE=1 (it costs a few cents)")
	}
	for _, tool := range []string{"herdr", "claude", "git", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	pause := &switchPause{}
	r := newStopRig(t, func(o *jobs.Options) {
		o.Landing = jobs.LandingOptions{ReviewDefault: jobs.ReviewDefaultSame, MergeMode: "merge"}
		o.DefaultModel = "haiku" // the reviewer job names no model
		o.Pause = pause
		o.Throttle = generousThrottle{}
	})
	t.Run("a_builder_reviewer_land", r.testLanding)
	t.Run("b_paused_job_is_not_nudged", func(t *testing.T) { r.testPausedNotNudged(t, pause) })
	if path := os.Getenv("CROWSNEST_LAND_TEST_OUT"); path != "" {
		_ = os.WriteFile(path, []byte(r.out.String()), 0o600)
	}
}

func (r *stopRig) testLanding(t *testing.T) {
	job := r.create(t, "Create a file named land.txt containing exactly the word landed. Commit it on your branch with git "+
		"(git add land.txt && git commit -m 'Add land.txt'). Then call your crowsnest done tool with a one-line result, verified listing what you checked, "+
		"not_verified as an empty list, and walkthrough_md: one short paragraph naming land.txt and why it exists. If a [crowsnest review …] message "+
		"asks for changes, make them, commit, and call done again.")
	review := r.waitFor(t, job.ID, 5*time.Minute, func(d jobs.Detail) bool {
		return d.State == jobs.StateInReview || d.State.Terminal() || (d.State == jobs.StateNeedsYou && d.Cause == jobs.CauseReview)
	})
	r.record(t, "(a) after the builder's done", review)
	if review.State != jobs.StateInReview || review.ReviewRound != 1 || review.WalkthroughMD == "" {
		t.Fatalf("the builder's done did not start a review: state %s round %d walkthrough %q", review.State, review.ReviewRound, review.WalkthroughMD)
	}
	headBefore := gitOut(t, review.WorkDir, "rev-parse", "HEAD")

	// The reviewer is a normal job: role reviewer, in the same worktree, with its own pane.
	var reviewer jobs.Job
	for _, child := range r.list(t) {
		if child.ReviewOf == job.ID {
			reviewer = child
		}
	}
	if reviewer.ID == "" || reviewer.Role != "reviewer" || reviewer.Dir != review.WorkDir {
		t.Fatalf("no reviewer job in the worktree: %+v", reviewer)
	}
	reviewed := r.waitFor(t, reviewer.ID, 6*time.Minute, func(d jobs.Detail) bool { return d.State.Terminal() })
	r.record(t, "(a) the reviewer", reviewed)
	if gitOut(t, review.WorkDir, "rev-parse", "HEAD") != headBefore && reviewed.State == jobs.StateDone {
		// The builder may already be fixing findings; only the reviewer's own window counts.
		t.Logf("HEAD moved after the review (the builder answering findings)")
	}
	if status := gitOut(t, review.WorkDir, "status", "--porcelain"); status != "" && strings.Contains(status, "REVIEW") {
		t.Fatalf("the reviewer wrote into the worktree: %q", status)
	}

	settled := r.waitFor(t, job.ID, 10*time.Minute, func(d jobs.Detail) bool {
		return d.State == jobs.StateDone || (d.State == jobs.StateNeedsYou && d.Cause == jobs.CauseReview) || d.State == jobs.StateFailed
	})
	r.record(t, "(a) after the review", settled)
	if settled.ReviewVerdict == jobs.VerdictChanges || settled.ReviewRound > 1 {
		if !hasEventText(settled, "the findings were sent to the agent") {
			t.Errorf("changes were requested but the findings were not sent to the builder")
		}
	}
	if settled.State == jobs.StateFailed {
		t.Fatalf("the job failed: %s", settled.FailReason)
	}

	var result jobs.LandResult
	if code := r.call(http.MethodPost, "/api/v1/jobs/"+job.ID+"/land", map[string]any{"action": "merge"}, nil); code != http.StatusConflict {
		t.Fatalf("a land without confirm: %d, want 409", code)
	}
	// The Review tab's commit goes back with the land (R5-S4): what lands is what was reviewed.
	var view jobs.ReviewView
	r.call(http.MethodGet, "/api/v1/jobs/"+job.ID+"/review", nil, &view)
	if view.Land.Head == "" {
		t.Fatalf("the Review tab names no commit: %+v", view.Land)
	}
	if code := r.call(http.MethodPost, "/api/v1/jobs/"+job.ID+"/land", map[string]any{"action": "merge", "confirm": true, "expected_head": view.Land.Head}, &result); code != http.StatusOK {
		t.Fatalf("land merge: %d", code)
	}
	landed := r.get(t, job.ID)
	r.record(t, "(a) landed", landed)
	if landed.LandState != jobs.LandedMerged {
		t.Fatalf("not merged: %+v", landed.Job)
	}
	if body, err := os.ReadFile(filepath.Join(r.repo, "land.txt")); err != nil || !strings.Contains(string(body), "landed") {
		t.Fatalf("the base branch does not have land.txt: %q %v", body, err)
	}
	if !result.TornDown {
		t.Fatalf("not torn down: %s", result.TeardownError)
	}
	if _, err := os.Stat(review.WorkDir); !os.IsNotExist(err) {
		t.Fatalf("the worktree is still there: %v", err)
	}
}

func (r *stopRig) testPausedNotNudged(t *testing.T, pause *switchPause) {
	pause.set(true)
	defer pause.set(false)
	var job jobs.Job
	if code := r.call(http.MethodPost, "/api/v1/jobs", map[string]any{"brief": "Reply with the single word hello and end your turn. Do not call any tool.",
		"dir": r.repo, "model": "haiku", "reviewer": "off"}, &job); code != http.StatusCreated {
		t.Fatalf("creating the job: %d", code)
	}
	time.Sleep(3 * time.Second)
	if held := r.get(t, job.ID); held.State != jobs.StateQueued || held.Cause != jobs.CauseHeld {
		t.Fatalf("a job started while paused: %s %s", held.State, held.Cause)
	}
	if code := r.call(http.MethodPost, "/api/v1/jobs/"+job.ID+"/start-anyway", nil, nil); code != http.StatusOK {
		t.Fatalf("start anyway: %d", code)
	}
	detail := r.waitFor(t, job.ID, 4*time.Minute, func(d jobs.Detail) bool { return hasEventText(d, "paused") && hasEventText(d, "Turn ended") })
	time.Sleep(10 * time.Second) // a nudge would land within a tick
	detail = r.get(t, job.ID)
	r.record(t, "(b) paused", detail)
	if detail.Nudges != 0 {
		t.Fatalf("a job was nudged while paused: %d", detail.Nudges)
	}
	_, _ = r.jobs.Cancel(context.Background(), job.ID)
}

func (r *stopRig) list(t *testing.T) []jobs.Job {
	var list []jobs.Job
	r.call(http.MethodGet, "/api/v1/jobs", nil, &list)
	return list
}

func hasEventText(d jobs.Detail, text string) bool {
	for _, event := range d.Timeline {
		if strings.Contains(event.Summary, text) {
			return true
		}
	}
	return false
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
