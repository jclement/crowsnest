package changes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/landing"
	"github.com/jclement/crowsnest/internal/store"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
}

// fakeResolver resolves j_1 to a worktree and project:1 to the main checkout.
type fakeResolver struct {
	mu      sync.Mutex
	job     Target
	project Target
	retired map[string]bool
}

func (f *fakeResolver) Job(_ context.Context, id string) (Target, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.job.JobID {
		return Target{}, fail("not_found", "no job %s", id)
	}
	return f.job, nil
}
func (f *fakeResolver) Project(context.Context, string) (Target, error) { return f.project, nil }
func (f *fakeResolver) Orchestrator(context.Context) (Target, error) {
	return Target{}, fail("not_found", "no orchestrator")
}
func (f *fakeResolver) ProjectJobs(context.Context, string) ([]Target, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []Target{f.job}, nil
}
func (f *fakeResolver) Retired(_ context.Context, subject string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.retired[subject]
}

type fixture struct {
	t        *testing.T
	main, wt string
	svc      *Service
	res      *fakeResolver
	hub      *hub.Hub
	now      time.Time
	st       *store.Store
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for key, value := range map[string]string{"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1", "GIT_AUTHOR_NAME": "T",
		"GIT_AUTHOR_EMAIL": "t@example.com", "GIT_COMMITTER_NAME": "T", "GIT_COMMITTER_EMAIL": "t@example.com"} {
		t.Setenv(key, value)
	}
	root := t.TempDir()
	f := &fixture{t: t, main: filepath.Join(root, "main"), wt: filepath.Join(root, "wt"), now: time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)}
	if err := os.MkdirAll(f.main, 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	git(t, f.main, "init", "-q", "-b", "main")
	write(t, f.main, "app.go", "package app\n\nfunc A() {}\n")
	write(t, f.main, "README.md", "# App\n")
	git(t, f.main, "add", ".")
	git(t, f.main, "commit", "-q", "-m", "first")
	git(t, f.main, "worktree", "add", "-q", "-b", "job/1", f.wt)
	write(t, f.wt, "app.go", "package app\n\nfunc A() { println(1) }\n")
	git(t, f.wt, "commit", "-q", "-am", "A prints")
	write(t, f.wt, "b.go", "package app\n\nfunc B() {}\n")
	st, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f.st = st
	f.res = &fakeResolver{
		job:     Target{Subject: "j_1", Kind: KindJob, JobID: "j_1", Label: "j_1", Root: f.wt, BaseBranch: "main"},
		project: Target{Subject: "project:1", Kind: KindProject, Label: "app", Root: f.main},
		retired: map[string]bool{},
	}
	f.hub = hub.New()
	f.svc = New(Options{Store: st, Hub: f.hub, Resolver: f.res, Now: func() time.Time { return f.now }})
	return f
}

func (f *fixture) later() { f.now = f.now.Add(MinInterval + time.Second) }

var job1 = Ask{Kind: KindJob, Ref: "j_1"}

func fileOf(summary Summary, path string) (File, bool) {
	for _, file := range summary.Files {
		if file.Path == path {
			return file, true
		}
	}
	return File{}, false
}

func TestSummaryScopes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	all, err := f.svc.Summary(ctx, job1, ScopeAll, false)
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != 2 || all.BaseLabel != "where it left main" || len(all.Commits) != 1 {
		t.Fatalf("all = %+v", all)
	}
	committed, _ := f.svc.Summary(ctx, job1, ScopeCommitted, false)
	uncommitted, _ := f.svc.Summary(ctx, job1, ScopeUncommitted, false)
	if _, ok := fileOf(committed, "app.go"); !ok || committed.Total != 1 {
		t.Errorf("committed = %+v", committed.Files)
	}
	if _, ok := fileOf(uncommitted, "b.go"); !ok || uncommitted.Total != 1 {
		t.Errorf("uncommitted = %+v", uncommitted.Files)
	}
	if len(all.Scopes) != 4 || all.Scopes[3].Name != ScopeSinceViewed || all.Scopes[3].Files != 2 {
		t.Errorf("scopes = %+v", all.Scopes)
	}
	if _, err := f.svc.Summary(ctx, job1, "bogus", false); err == nil {
		t.Error("a bogus scope was accepted")
	}
}

// TestViewedMarkFollowsContent: a mark holds while the file is unchanged, clears when it changes,
// the file comes back flagged, and since-viewed shows only the new edit.
func TestViewedMarkFollowsContent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	sub := f.hub.Subscribe(64)
	defer sub.Close()
	all, err := f.svc.Summary(ctx, job1, ScopeAll, false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := fileOf(all, "b.go")
	if _, err := f.svc.Mark(ctx, job1, ScopeAll, []MarkAsk{{Path: "b.go", Hash: "wrong"}}, false); err == nil {
		t.Fatal("a mark with a stale hash was accepted")
	} else if e, ok := AsError(err); !ok || e.Kind != "stale" {
		t.Fatalf("err = %v", err)
	}
	marked, err := f.svc.Mark(ctx, job1, ScopeAll, []MarkAsk{{Path: "b.go", Hash: b.Hash}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if file, _ := fileOf(marked, "b.go"); !file.Viewed || marked.Viewed != 1 {
		t.Fatalf("not viewed: %+v", marked)
	}
	since, _ := f.svc.Summary(ctx, job1, ScopeSinceViewed, false)
	if _, ok := fileOf(since, "b.go"); ok || since.Total != 1 {
		t.Errorf("since-viewed still lists b.go: %+v", since.Files)
	}
	// Committing b.go unchanged: the uncommitted scope's mark-free view still counts it viewed
	// in committed, because the hash (both blobs) is the same change.
	write(t, f.wt, "b.go", "package app\n\nfunc B() {}\n\nfunc C() {}\n")
	f.later()
	again, err := f.svc.Summary(ctx, job1, ScopeAll, true)
	if err != nil {
		t.Fatal(err)
	}
	file, _ := fileOf(again, "b.go")
	if file.Viewed || !file.Changed || !file.SinceAvailable {
		t.Fatalf("after a change: %+v", file)
	}
	view, err := f.svc.File(ctx, job1, FileAsk{Path: "b.go", Scope: ScopeAll, Since: true})
	if err != nil {
		t.Fatal(err)
	}
	if !view.Since || !strings.Contains(view.Diff, "+func C() {}") || strings.Contains(view.Diff, "+func B() {}") {
		t.Errorf("since diff = %q", view.Diff)
	}
	if view.OldText == nil || view.NewText == nil || !strings.Contains(*view.NewText, "func C") {
		t.Errorf("texts: %v %v", view.OldText, view.NewText)
	}
	// A changes event went out when the reading changed.
	sawChange := false
	for len(sub.Events()) > 0 {
		if event := <-sub.Events(); event.Type == EventType {
			sawChange = true
		}
	}
	if !sawChange {
		t.Error("no changes event")
	}
}

func TestMarkAllAndClear(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	summary, err := f.svc.Mark(ctx, job1, ScopeAll, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Viewed != summary.Total || summary.Total != 2 {
		t.Fatalf("mark all: %d of %d", summary.Viewed, summary.Total)
	}
	unviewed, total, err := f.svc.Unviewed(ctx, "j_1")
	if err != nil || unviewed != 0 || total != 1 {
		t.Errorf("unviewed for land = %d of %d, %v", unviewed, total, err)
	}
	cleared, err := f.svc.Unmark(ctx, job1, ScopeAll, nil)
	if err != nil || cleared.Viewed != 0 {
		t.Fatalf("clear: %+v %v", cleared.Viewed, err)
	}
	if unviewed, _, _ := f.svc.Unviewed(ctx, "j_1"); unviewed != 1 {
		t.Errorf("after clearing, unviewed = %d", unviewed)
	}
}

func TestFileConfinedToTheDiff(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, path := range []string{"README.md", "../main/app.go", "/etc/passwd", ""} {
		if _, err := f.svc.File(ctx, job1, FileAsk{Path: path}); err == nil {
			t.Errorf("%q was served", path)
		}
	}
	view, err := f.svc.File(ctx, job1, FileAsk{Path: "app.go", Scope: ScopeCommitted})
	if err != nil || !strings.Contains(view.Diff, "+func A() { println(1) }") {
		t.Fatalf("app.go = %+v %v", view, err)
	}
	if _, err := f.svc.Summary(ctx, Ask{Kind: KindJob, Ref: "j_9"}, ScopeAll, false); err == nil {
		t.Error("an unknown job was read")
	}
	if _, err := f.svc.Summary(ctx, Ask{Kind: KindProject, Ref: "app", Base: "--upload-pack=x"}, ScopeAll, false); err == nil {
		t.Error("an option was accepted as a base")
	}
}

func TestThrottle(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first, _ := f.svc.Summary(ctx, job1, ScopeAll, false)
	write(t, f.wt, "c.go", "package app\n")
	cached, _ := f.svc.Summary(ctx, job1, ScopeAll, true)
	if cached.Total != first.Total || !cached.ComputedAt.Equal(first.ComputedAt) {
		t.Fatalf("read again inside the interval: %d files", cached.Total)
	}
	f.later()
	fresh, _ := f.svc.Summary(ctx, job1, ScopeAll, false)
	if fresh.Total != first.Total+1 {
		t.Fatalf("not read again after the interval: %d", fresh.Total)
	}
}

func TestMainCheckoutUncommitted(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	write(t, f.main, "README.md", "# App\n\nEdited by the lead.\n")
	summary, err := f.svc.Summary(ctx, Ask{Kind: KindProject, Ref: "app"}, ScopeUncommitted, false)
	if err != nil {
		t.Fatal(err)
	}
	if file, ok := fileOf(summary, "README.md"); !ok || file.Status != "modified" || summary.Total != 1 {
		t.Fatalf("lead's uncommitted = %+v", summary.Files)
	}
	if len(summary.BaseChoices) == 0 {
		t.Error("no base choices")
	}
	rows, err := f.svc.ProjectChanges(ctx, "app")
	if err != nil || len(rows) != 2 || rows[1].Files != 2 {
		t.Fatalf("project rows = %+v %v", rows, err)
	}
}

func TestRetiredJobLosesMarks(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.svc.Mark(ctx, job1, ScopeAll, nil, true); err != nil {
		t.Fatal(err)
	}
	f.res.mu.Lock()
	f.res.retired["j_1"] = true
	f.res.mu.Unlock()
	f.svc.onEvent(ctx, hub.Event{Type: "job", Data: map[string]string{"id": "j_1"}})
	marks, _ := f.st.ListDiffMarks(ctx, "j_1")
	if len(marks) != 0 {
		t.Fatalf("marks kept after land: %+v", marks)
	}
}

func TestWatchedSubjectReadAfterActivity(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.svc.opts.Now = time.Now
	f.svc.opts.MinInterval = 10 * time.Millisecond
	if _, err := f.svc.Summary(ctx, job1, ScopeAll, false); err != nil {
		t.Fatal(err)
	}
	sub := f.hub.Subscribe(64)
	defer sub.Close()
	time.Sleep(20 * time.Millisecond)
	write(t, f.wt, "d.go", "package app\n")
	f.svc.onEvent(ctx, hub.Event{Type: "job", Data: map[string]string{"id": "j_1"}})
	deadline := time.After(10 * time.Second)
	for {
		select {
		case event := <-sub.Events():
			if event.Type == EventType {
				return
			}
		case <-deadline:
			t.Fatal("no changes event after the job's activity")
		}
	}
}

// TestLandViewedFailsClosed is R7-C07 and R7-S9: with every listed file viewed, the gate still
// refuses when the commit being landed is not the one it read (the agent committed a new file
// just as the human tapped Land), when the branch changes more files than a reading lists, and
// when the checkout cannot be read.
func TestLandViewedFailsClosed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	summary, err := f.svc.Summary(ctx, job1, ScopeCommitted, false)
	if err != nil {
		t.Fatal(err)
	}
	reviewed := summary.Head
	if _, err := f.svc.Mark(ctx, job1, ScopeAll, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.LandViewed(ctx, "j_1", reviewed); err != nil {
		t.Fatalf("everything viewed: %v", err)
	}
	var refusal *LandRefusal

	// A new commit lands on the branch after the human viewed everything.
	write(t, f.wt, "late.go", "package app\n\nfunc Late() {}\n")
	git(t, f.wt, "add", "late.go")
	git(t, f.wt, "commit", "-q", "-m", "late")
	if err := f.svc.LandViewed(ctx, "j_1", reviewed); !errors.As(err, &refusal) || !refusal.Unknown {
		t.Fatalf("landing the reviewed head after a new commit: %v", err)
	}
	if err := f.svc.LandViewed(ctx, "j_1", git(t, f.wt, "rev-parse", "HEAD")); !errors.As(err, &refusal) || refusal.Unknown || refusal.Unviewed != 1 {
		t.Fatalf("the new head with its new file unviewed: %v", err)
	}

	// More files than a reading lists: every listed one viewed is not every file.
	for i := 0; i <= landing.MaxChangedFiles; i++ {
		write(t, f.wt, fmt.Sprintf("gen/f%04d.txt", i), "x\n")
	}
	git(t, f.wt, "add", "gen")
	git(t, f.wt, "commit", "-q", "-m", "many")
	head := git(t, f.wt, "rev-parse", "HEAD")
	if _, err := f.svc.Mark(ctx, job1, ScopeAll, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.LandViewed(ctx, "j_1", head); !errors.As(err, &refusal) || !refusal.Unknown || !strings.Contains(refusal.Message, "more than") {
		t.Fatalf("a truncated listing: %v", err)
	}

	// The checkout cannot be read.
	f.res.mu.Lock()
	f.res.job.Root = filepath.Join(t.TempDir(), "gone")
	f.res.mu.Unlock()
	if err := f.svc.LandViewed(ctx, "j_1", head); !errors.As(err, &refusal) || !refusal.Unknown {
		t.Fatalf("an unreadable checkout: %v", err)
	}
}

// TestFileServesOnlyTheHashAsked is R7-C12: the browser asks for a file's body by the hash the
// list showed; after an edit the server's reading has another hash, and it answers stale rather
// than a body the browser would cache under the old version.
func TestFileServesOnlyTheHashAsked(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	summary, err := f.svc.Summary(ctx, job1, ScopeAll, false)
	if err != nil {
		t.Fatal(err)
	}
	shown, _ := fileOf(summary, "b.go")
	if view, err := f.svc.File(ctx, job1, FileAsk{Path: "b.go", Scope: ScopeAll, Hash: shown.Hash}); err != nil || view.Hash != shown.Hash {
		t.Fatalf("the version shown: %+v %v", view.Hash, err)
	}
	write(t, f.wt, "b.go", "package app\n\nfunc B() { println(2) }\n")
	f.later()
	_, err = f.svc.File(ctx, job1, FileAsk{Path: "b.go", Scope: ScopeAll, Hash: shown.Hash})
	if e, ok := AsError(err); !ok || e.Kind != "stale" {
		t.Fatalf("an edited file asked by its old hash: %v", err)
	}
}

// TestRepositoryTopMustBeTheSubjects is R7-S8: a worktree job whose .git file is gone is not read
// as the repository around it, and no subject is read through a repository whose top holds home.
func TestRepositoryTopMustBeTheSubjects(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.res.mu.Lock()
	f.res.job.Worktree = true
	f.res.mu.Unlock()
	if _, err := f.svc.Summary(ctx, job1, ScopeAll, false); err != nil {
		t.Fatalf("a worktree read as itself: %v", err)
	}
	// A folder inside the main checkout, given as a worktree job's root: git finds main around it.
	inner := filepath.Join(f.main, "inner")
	write(t, inner, "x.txt", "x\n")
	f.res.mu.Lock()
	f.res.job.Root = inner
	f.res.mu.Unlock()
	f.later()
	if _, err := f.svc.Summary(ctx, job1, ScopeAll, true); err == nil {
		t.Fatal("a worktree job was read through the repository around it")
	}
	// Home inside a repository (a dotfiles repository): a project in it is not read.
	home := filepath.Join(f.main, "home")
	write(t, home, "notes.txt", "n\n")
	svc := New(Options{Store: f.st, Resolver: f.res, Now: func() time.Time { return f.now }, Home: home})
	if _, err := svc.Summary(ctx, Ask{Kind: KindProject, Ref: "1"}, ScopeAll, false); err == nil || !strings.Contains(err.Error(), "home") {
		t.Fatalf("the repository holding home was read: %v", err)
	}
}

// TestConcurrentReadsAreRaceFree is R7-C08: a reading being replaced while the progress route and
// the watch tick look at it. Run with -race (mise run test does): before the fix, the entry's
// reading and time were written under the entry's lock and read under the service's.
func TestConcurrentReadsAreRaceFree(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	svc := New(Options{Store: f.st, Hub: f.hub, Resolver: f.res, Now: time.Now, MinInterval: time.Nanosecond})
	if _, err := svc.Summary(ctx, job1, ScopeAll, false); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(3)
		go func() {
			defer wg.Done()
			for range 5 {
				_, _ = svc.Summary(ctx, job1, ScopeAll, true)
			}
		}()
		go func() {
			defer wg.Done()
			for range 50 {
				_, _ = svc.ProgressAll(ctx)
			}
		}()
		go func() {
			defer wg.Done()
			for range 5 {
				svc.refreshWatched(ctx, 0)
			}
		}()
	}
	wg.Wait()
	if progress, err := svc.ProgressAll(ctx); err != nil || progress["j_1"].Total == 0 {
		t.Fatalf("progress after the reads: %+v %v", progress, err)
	}
}
