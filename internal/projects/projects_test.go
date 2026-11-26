// Tests for projects: validation, the directory rules (too wide, overlapping, links), job
// directory resolution, archiving, and the lead manager (start, adopt on resume, stop) against
// the fake herdr.

package projects

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
)

func newService(t *testing.T) (*Service, *store.Store, string) {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	home := t.TempDir()
	return New(Options{Store: db, Home: home, Roles: func(r string) bool { return r == "builder" || r == "lead" }}), db, home
}

func ptr[T any](v T) *T { return &v }

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCreateValidates(t *testing.T) {
	svc, _, home := newService(t)
	ctx := context.Background()
	repo := mkdir(t, filepath.Join(home, "Work", "crowsnest"))
	cases := []struct {
		name  string
		input Input
		want  string // "" ok, else a substring of the refusal
	}{
		{"no name", Input{Dir: ptr(repo)}, "needs a name"},
		{"no dir", Input{Name: ptr("x")}, "needs a directory"},
		{"relative", Input{Name: ptr("x"), Dir: ptr("Work/x")}, "absolute"},
		{"missing", Input{Name: ptr("x"), Dir: ptr(filepath.Join(home, "nope"))}, "not a directory"},
		{"home", Input{Name: ptr("x"), Dir: ptr(home)}, "too wide"},
		{"root", Input{Name: ptr("x"), Dir: ptr("/")}, "too wide"},
		{"home's parent", Input{Name: ptr("x"), Dir: ptr(filepath.Dir(home))}, "too wide"},
		{"bad role", Input{Name: ptr("x"), Dir: ptr(repo), DefaultRole: ptr("wizard")}, "unknown role"},
		{"bad engine", Input{Name: ptr("x"), Dir: ptr(repo), DefaultEngine: ptr("gpt")}, "claude or codex"},
		{"bad lead start", Input{Name: ptr("x"), Dir: ptr(repo), LeadStart: ptr("sometimes")}, "lead_start"},
		{"bad slug", Input{Name: ptr("x"), Dir: ptr(repo), Slug: ptr("Not OK")}, "short name"},
		{"ok", Input{Name: ptr("Crow's Nest"), Dir: ptr("~/Work/crowsnest"), DefaultRole: ptr("builder"), DefaultIsolate: ptr(1), LeadStart: ptr("job")}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project, err := svc.Create(ctx, tc.input)
			var verr *ValidationError
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.want == "":
				if project.Slug != "crow-s-nest" || project.Dir != repo || project.DefaultIsolate == nil || !*project.DefaultIsolate {
					t.Fatalf("project %+v", project)
				}
			case !errors.As(err, &verr) || !contains(err.Error(), tc.want):
				t.Fatalf("err %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDirectoriesNeverOverlapAndJobsResolve(t *testing.T) {
	svc, _, home := newService(t)
	ctx := context.Background()
	repo := mkdir(t, filepath.Join(home, "Work", "alpha"))
	inner := mkdir(t, filepath.Join(repo, "sub", "deep"))
	other := mkdir(t, filepath.Join(home, "Work", "beta"))
	link := filepath.Join(home, "alpha-link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	alpha, err := svc.Create(ctx, Input{Name: ptr("Alpha"), Dir: ptr(repo)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, Input{Name: ptr("Inner"), Dir: ptr(inner)}); err == nil {
		t.Fatal("a project inside another was allowed")
	}
	if _, err := svc.Create(ctx, Input{Name: ptr("Outer"), Dir: ptr(filepath.Join(home, "Work"))}); err == nil {
		t.Fatal("a project holding another was allowed")
	}
	if _, err := svc.Create(ctx, Input{Name: ptr("Linked"), Dir: ptr(link)}); err == nil {
		t.Fatal("the same repository through a link was allowed")
	}
	for _, dir := range []string{repo, inner, filepath.Join(link, "sub")} {
		if got, ok := svc.ForDir(ctx, dir); !ok || got.ID != alpha.ID {
			t.Fatalf("ForDir(%s) = %+v %v", dir, got, ok)
		}
	}
	if _, ok := svc.ForDir(ctx, other); ok {
		t.Fatal("an unrelated directory joined a project")
	}
	if _, ok := svc.ForDir(ctx, repo+"-not"); ok {
		t.Fatal("a sibling with the same prefix joined a project")
	}
	// Archived: it claims nothing, and its directory is free.
	if _, err := svc.Update(ctx, alpha.ID, Input{Archived: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.ForDir(ctx, inner); ok {
		t.Fatal("an archived project still claims jobs")
	}
	if _, err := svc.Create(ctx, Input{Name: ptr("Inner"), Dir: ptr(inner)}); err != nil {
		t.Fatalf("directory not freed by archiving: %v", err)
	}
	if _, err := svc.Update(ctx, alpha.ID, Input{Archived: ptr(false)}); err == nil {
		t.Fatal("unarchiving into an overlap was allowed")
	}
	if _, err := svc.Update(ctx, alpha.ID, Input{Slug: ptr("renamed")}); err == nil {
		t.Fatal("the slug changed")
	}
	if got, err := svc.Get(ctx, "alpha"); err != nil || got.ID != alpha.ID {
		t.Fatalf("by slug: %+v %v", got, err)
	}
	if _, err := svc.Get(ctx, "p_999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{"Crow's Nest": "crow-s-nest", "  ": "project", "ÉTÉ 2026": "t-2026", "a--b": "a-b"} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLeadsStartStopResume(t *testing.T) {
	svc, db, home := newService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo := mkdir(t, filepath.Join(home, "Work", "alpha"))
	project, err := svc.Create(ctx, Input{Name: ptr("Alpha"), Dir: ptr(repo), LeadStart: ptr(LeadOnJob)})
	if err != nil {
		t.Fatal(err)
	}
	f := fake.New()
	state := t.TempDir()
	factory := func(p Project) (*orch.Orchestrator, *orch.Poker) {
		return orch.New(orch.Options{
			Client: f, KV: db, Session: "crowsnest-test", Dir: p.Dir, StateDir: filepath.Join(state, p.Slug),
			AgentName: AgentName(p.Slug), Workspace: "lead " + p.Slug, KVPrefix: "lead." + p.Slug,
			ShellWait: 200 * time.Millisecond, WatchInterval: 50 * time.Millisecond, Logger: slog.New(slog.DiscardHandler),
		}), nil
	}
	var changes []LeadStatus
	leads := NewLeads(ctx, svc, db, factory, slog.New(slog.DiscardHandler), func(s LeadStatus) { changes = append(changes, s) })
	if status, _ := leads.Status(ctx, project.ID); status.State != StateOff || status.Running {
		t.Fatalf("before start: %+v", status)
	}
	if !leads.GivenJob(ctx, project.ID) {
		t.Fatal("lead_start=job did not start the lead for a job")
	}
	waitFor(t, func() bool { return leads.Get(project.ID).Agent.Status().State == orch.StateIdle })
	lead := leads.Get(project.ID)
	if leads.ByPane(lead.Agent.Status().PaneID) != lead || leads.ByAgentName("lead-alpha") != lead {
		t.Fatal("lookup by pane or name failed")
	}
	if flag, _ := db.GetKV(ctx, "lead.alpha.running"); flag != "1" {
		t.Fatalf("running flag %q", flag)
	}
	// A new manager (a crowsnest restart) resumes it and adopts the live agent.
	resumed := NewLeads(ctx, svc, db, factory, slog.New(slog.DiscardHandler), nil)
	resumed.Resume(ctx)
	waitFor(t, func() bool { l := resumed.Get(project.ID); return l != nil && l.Agent.Status().State == orch.StateIdle })
	if len(f.Starts()) != 1 {
		t.Fatalf("resume started a second Claude instead of adopting: %d", len(f.Starts()))
	}
	leads = resumed
	status, err := leads.Stop(ctx, project.ID)
	if err != nil || status.State != StateOff {
		t.Fatalf("stop: %+v %v", status, err)
	}
	if _, err := db.GetKV(ctx, "lead.alpha.running"); err == nil {
		t.Fatal("running flag kept after stop")
	}
	// Archived projects' leads do not start.
	_, _ = svc.Update(ctx, project.ID, Input{Archived: ptr(true)})
	if _, err := leads.Start(ctx, project.ID); !errors.Is(err, ErrArchived) {
		t.Fatalf("archived start: %v", err)
	}
	if len(changes) == 0 {
		t.Fatal("no change callbacks")
	}
}

func TestManualLeadIsNotStartedByAJob(t *testing.T) {
	svc, db, home := newService(t)
	ctx := context.Background()
	repo := mkdir(t, filepath.Join(home, "r"))
	project, _ := svc.Create(ctx, Input{Name: ptr("R"), Dir: ptr(repo)})
	leads := NewLeads(ctx, svc, db, func(Project) (*orch.Orchestrator, *orch.Poker) { t.Fatal("started"); return nil, nil }, nil, nil)
	if leads.GivenJob(ctx, project.ID) {
		t.Fatal("a manual lead was started by a job")
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func contains(s, sub string) bool { return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0) }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
