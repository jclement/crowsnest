// --demo for landing (DESIGN.md 6.11, 6.7, 6.12): a real, throwaway git repository in the demo's
// temporary directory with worktrees made through the fake herdr (in its real-git mode), so the
// Review tab shows a real diff and Land really merges, pushes nowhere, or discards. Seeded:
//
//   - a job done, reviewed and approved, ready to land (merge, or discard);
//   - a job in review, round 2, with round 1's findings and a reviewer running;
//   - a job queued, waiting for a slot.
//
// Nothing here touches a real repository: the directory is under the demo's state directory.

package jobs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/store"
)

// demoGit runs git in dir for the demo repository, with no global configuration (no signing,
// no hooks of the user's).
func demoGit(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) //nolint:gosec // fixed demo commands
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Demo",
		"GIT_AUTHOR_EMAIL=demo@example.invalid", "GIT_COMMITTER_NAME=Demo", "GIT_COMMITTER_EMAIL=demo@example.invalid")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

func demoWrite(dir, name, body string) error {
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o600)
}

// SeedDemoLanding adds the landing demo under root (the demo's state directory). worktrees is
// the fake herdr's switch to real git (fake.RealGitWorktrees), called before anything is made.
func SeedDemoLanding(ctx context.Context, s *Service, root string, worktrees func()) error {
	if _, err := exec.LookPath("git"); err != nil {
		return nil // no git: the rest of the demo still runs
	}
	repo := filepath.Join(root, "demo-src", "widgets")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		return err
	}
	if err := demoGit(repo, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	for name, body := range map[string]string{
		"README.md":      "# Widgets\n\nA small service that renders widgets.\n",
		"retry.go":       "package widgets\n\n// Fetch gets a widget once.\nfunc Fetch(id string) (string, error) {\n\treturn get(id)\n}\n",
		"retry_test.go":  "package widgets\n\nimport \"testing\"\n\nfunc TestFetch(t *testing.T) {}\n",
		"store/store.go": "package store\n\n// Widget is one row.\ntype Widget struct{ ID string }\n",
	} {
		if err := demoWrite(repo, name, body); err != nil {
			return err
		}
	}
	if err := demoGit(repo, "add", "."); err != nil {
		return err
	}
	if err := demoGit(repo, "commit", "-q", "-m", "Widgets: first version"); err != nil {
		return err
	}
	worktrees()
	ready, err := seedDemoWorktreeJob(ctx, s, repo, demoReady())
	if err != nil {
		return err
	}
	review, err := seedDemoWorktreeJob(ctx, s, repo, demoInReview())
	if err != nil {
		return err
	}
	if err := seedDemoReviewer(ctx, s, review, repo); err != nil {
		return err
	}
	_ = ready
	return seedDemoHeld(ctx, s, repo)
}

// demoWork is one seeded worktree job: what it was asked, the files it changed, and its record.
type demoWork struct {
	branch, title, brief string
	files                map[string]string
	commit               string
	record               func(r *store.JobRecord)
}

func demoReady() demoWork {
	return demoWork{
		branch: "job/91-retry-with-back-off", title: "Retry widget fetches with back-off",
		brief: "Retry widget fetches with back-off\n\nFetch fails on a flaky network. Retry three times with exponential back-off, and test it.",
		files: map[string]string{
			"retry.go": "package widgets\n\nimport \"time\"\n\n// Fetch gets a widget, retrying a failed call three times with exponential back-off\n// (100 ms, 200 ms, 400 ms): the widget service drops about one call in fifty.\nfunc Fetch(id string) (string, error) {\n\tvar err error\n\tfor attempt := 0; attempt < 4; attempt++ {\n\t\tvar body string\n\t\tif body, err = get(id); err == nil {\n\t\t\treturn body, nil\n\t\t}\n\t\ttime.Sleep(time.Duration(100<<attempt) * time.Millisecond)\n\t}\n\treturn \"\", err\n}\n",
			"retry_test.go": "package widgets\n\nimport \"testing\"\n\nfunc TestFetchRetriesThenSucceeds(t *testing.T) {\n\tfails = 2\n\tif _, err := Fetch(\"w1\"); err != nil {\n\t\tt.Fatal(err)\n\t}\n}\n\nfunc TestFetchGivesUpAfterFourTries(t *testing.T) {\n\tfails = 9\n\tif _, err := Fetch(\"w1\"); err == nil {\n\t\tt.Fatal(\"want an error\")\n\t}\n}\n",
			"docs/retry.md": "# Retries\n\nFetch retries three times: 100, 200 and 400 ms apart.\n",
		},
		commit: "Fetch: retry with exponential back-off",
		record: func(r *store.JobRecord) {
			r.State, r.ResultMD = string(StateDone), "Fetch now retries a failed call three times with exponential back-off (100, 200, 400 ms). Two tests cover the retry and the give-up."
			r.VerifiedJSON, r.NotVerifiedJSON = `["go test ./... (2 new tests pass)"]`, `["behaviour against the real widget service"]`
			r.WalkthroughMD = "## retry.go\n\nThe loop in `Fetch` is the change: four tries, the sleep doubling from 100 ms. It returns the last error, so callers see why it gave up.\n\n" +
				"## retry_test.go\n\nOne test makes two calls fail and expects success; one makes every call fail and expects the error.\n\n" +
				"## docs/retry.md\n\nThe numbers, for whoever tunes them.\n\n**Tested:** both paths. **Not tested:** the real service.\n\n**Risk:** a slow service now takes up to 700 ms longer to fail; safe because the back-off is bounded and logged."
			r.ReviewRound, r.ReviewVerdict = 1, VerdictApprove
			r.ReviewMD = "VERDICT: approve\n\nThe retry is bounded and the tests cover both paths. Minor: the sleep could honour a context; not a blocker."
		},
	}
}

func demoInReview() demoWork {
	return demoWork{
		branch: "job/92-widget-cache", title: "Cache widgets for a minute",
		brief: "Cache widgets for a minute\n\nAdd an in-memory cache in front of the store, one minute per widget.",
		files: map[string]string{
			"store/cache.go": "package store\n\nimport \"time\"\n\n// Cache keeps widgets for a minute.\ntype Cache struct {\n\titems map[string]entry\n}\n\ntype entry struct {\n\tw  Widget\n\tat time.Time\n}\n\n// Get returns a cached widget younger than a minute.\nfunc (c *Cache) Get(id string) (Widget, bool) {\n\te, ok := c.items[id]\n\treturn e.w, ok && time.Since(e.at) < time.Minute\n}\n",
		},
		commit: "store: a one-minute widget cache",
		record: func(r *store.JobRecord) {
			r.State, r.ResultMD = string(StateInReview), "Added store.Cache: a one-minute in-memory cache. Fixed the race the reviewer found with a mutex."
			r.VerifiedJSON, r.NotVerifiedJSON = `["go test -race ./store"]`, `["memory use under load"]`
			r.WalkthroughMD = "## store/cache.go\n\nA map of entries with their time; `Get` treats anything older than a minute as missing."
			r.ReviewRound, r.ReviewVerdict = 2, VerdictChanges
			r.ReviewMD = "VERDICT: changes\n\n1. store/cache.go:14 the map is read and written from several goroutines without a lock: a data race under load.\n2. Nothing evicts old entries: the map grows without bound."
		},
	}
}

// seedDemoWorktreeJob makes the worktree through herdr (real git in the demo fake), commits the
// work in it, and stores the job.
func seedDemoWorktreeJob(ctx context.Context, s *Service, repo string, work demoWork) (int64, error) {
	path := filepath.Join(filepath.Dir(repo), ".worktrees", strings.ReplaceAll(work.branch, "/", "-"))
	created, err := s.opts.Herdr.WorktreeCreate(ctx, herdr.WorktreeCreateParams{Cwd: repo, Branch: work.branch, Path: path, Label: work.branch})
	if err != nil {
		return 0, fmt.Errorf("making a demo worktree: %w", err)
	}
	for name, body := range work.files {
		if err := demoWrite(path, name, body); err != nil {
			return 0, err
		}
	}
	if err := demoGit(path, "add", "."); err != nil {
		return 0, err
	}
	if err := demoGit(path, "commit", "-q", "-m", work.commit); err != nil {
		return 0, err
	}
	now := s.opts.Now()
	record := store.JobRecord{
		Title: work.title, BriefMD: work.brief, Role: "builder", Dir: repo, WorktreeBranch: work.branch, WorkDir: path, Isolate: true,
		Engine: EngineClaude, State: string(StateRunning), PaneID: created.RootPane.ID, AgentName: "job-demo", BriefSent: true,
		CreatedAt: now.Add(-3 * 60 * 60e9), StartedAt: now.Add(-3 * 60 * 60e9), LastActivityAt: now.Add(-5 * 60e9),
		BaseBranch: "main", WorkspaceID: created.Workspace.ID, VerifiedJSON: "[]", NotVerifiedJSON: "[]", Reviewer: EngineClaude,
	}
	work.record(&record)
	if State(record.State).Terminal() {
		record.SettledAt = now.Add(-20 * 60e9)
	}
	id, err := s.opts.Store.InsertJob(ctx, record)
	if err != nil {
		return 0, err
	}
	for _, event := range []store.JobEventRecord{
		{Kind: EventLaunch, Summary: "Opened a worktree on " + work.branch, At: now.Add(-3 * 60 * 60e9)},
		{Kind: EventReview, Summary: fmt.Sprintf("Review round %d: %s", record.ReviewRound, record.ReviewVerdict), At: now.Add(-25 * 60e9)},
	} {
		event.JobID = id
		_, _ = s.opts.Store.InsertJobEvent(ctx, event)
	}
	return id, nil
}

// seedDemoReviewer adds the in-review job's two reviewers: round 1 done with its findings, round
// 2 running.
func seedDemoReviewer(ctx context.Context, s *Service, parent int64, repo string) error {
	parentRecord, err := s.opts.Store.GetJob(ctx, parent)
	if err != nil {
		return err
	}
	now := s.opts.Now()
	rounds := []store.JobRecord{
		{Title: "Review job j_" + fmt.Sprint(parent) + " (round 1)", State: string(StateDone), ResultMD: parentRecord.ReviewMD, SettledAt: now.Add(-40 * 60e9)},
		{Title: "Review job j_" + fmt.Sprint(parent) + " (round 2)", State: string(StateRunning), Progress: "Reading store/cache.go against round 1's findings"},
	}
	for _, round := range rounds {
		round.BriefMD, round.Role, round.Dir, round.WorkDir, round.Engine = round.Title, "reviewer", parentRecord.WorkDir, parentRecord.WorkDir, EngineCodex
		round.ParentID, round.ReviewOf, round.Reviewer, round.CreatedAt = parent, parent, ReviewerOff, now.Add(-45*60e9)
		round.VerifiedJSON, round.NotVerifiedJSON = "[]", "[]"
		if _, err := s.opts.Store.InsertJob(ctx, round); err != nil {
			return err
		}
	}
	return nil
}

// seedDemoHeld adds a job waiting for a slot.
func seedDemoHeld(ctx context.Context, s *Service, repo string) error {
	now := s.opts.Now()
	_, err := s.opts.Store.InsertJob(ctx, store.JobRecord{
		Title: "Add pagination to the widget list", BriefMD: "Add pagination to the widget list\n\nTwenty per page, with next and previous links.",
		Role: "builder", Dir: repo, Isolate: true, Engine: EngineClaude, State: string(StateQueued), CreatedAt: now.Add(-4 * 60e9),
		Cause: string(CauseHeld), CauseDetail: "waiting for a slot: 4 of 4 running", VerifiedJSON: "[]", NotVerifiedJSON: "[]",
	})
	return err
}
