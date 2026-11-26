// Tests for jobs in projects (DESIGN.md 21.1, 21.2): a directory inside a project joins it, the
// project's defaults fill what is unset, a job "to the lead" is held by the project's lead (and
// never by the orchestrator), and a lead's own delegation is confined to its project's root.

package jobs

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jclement/crowsnest/internal/herdr"
)

// fakeProjects is a ProjectSource over fixed projects and leads.
type fakeProjects struct {
	mu       sync.Mutex
	projects []ProjectInfo
	leads    []StandingAgent
	given    []int64
}

func (f *fakeProjects) Project(_ context.Context, id int64) (ProjectInfo, bool) {
	for _, p := range f.projects {
		if p.ID == id {
			return p, true
		}
	}
	return ProjectInfo{}, false
}

func (f *fakeProjects) ForDir(_ context.Context, dir string) (ProjectInfo, bool) {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolved = dir
	}
	for _, p := range f.projects {
		if !p.Archived && (resolved == p.RootDir || strings.HasPrefix(resolved, p.RootDir+"/")) {
			return p, true
		}
	}
	return ProjectInfo{}, false
}

func (f *fakeProjects) Leads() []StandingAgent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]StandingAgent(nil), f.leads...)
}

func (f *fakeProjects) LeadByPane(pane string) (StandingAgent, bool) {
	for _, lead := range f.Leads() {
		if lead.PaneID == pane {
			return lead, true
		}
	}
	return StandingAgent{}, false
}

func (f *fakeProjects) GivenJob(_ context.Context, id int64) {
	f.mu.Lock()
	f.given = append(f.given, id)
	f.mu.Unlock()
}

func projectRig(t *testing.T) (*rig, *fakeProjects, ProjectInfo) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	isolate := false
	alpha := ProjectInfo{ID: 1, Slug: "alpha", Name: "Alpha", Dir: root, RootDir: root, DefaultRole: "tester", DefaultModel: "haiku", DefaultIsolate: &isolate}
	projects := &fakeProjects{projects: []ProjectInfo{alpha, {ID: 2, Slug: "old", Name: "Old", Archived: true}}}
	r := newRig(t, func(o *Options) { o.Projects = projects })
	return r, projects, alpha
}

func TestJobsJoinProjectsAndTakeTheirDefaults(t *testing.T) {
	r, _, alpha := projectRig(t)
	ctx := context.Background()
	inside := mkdir(t, alpha.Dir, "pkg")
	cases := []struct {
		name    string
		input   CreateInput
		project int64
		dir     string
		role    string
		err     string
	}{
		{name: "a directory inside joins", input: CreateInput{Brief: "x", Dir: inside}, project: 1, dir: inside, role: "tester"},
		{name: "given to the project as a job runs in its dir", input: CreateInput{Brief: "x", ProjectID: 1, To: ToJob}, project: 1, dir: alpha.Dir, role: "tester"},
		{name: "given to the project with no dir goes to the lead", input: CreateInput{Brief: "x", ProjectID: 1}, project: 1, dir: "", role: "lead"},
		{name: "an explicit role wins", input: CreateInput{Brief: "x", Dir: inside, Role: "builder"}, project: 1, dir: inside, role: "builder"},
		{name: "outside any project is Unfiled", input: CreateInput{Brief: "x", Dir: t.TempDir()}, project: 0, role: "builder"},
		{name: "a dir outside the named project", input: CreateInput{Brief: "x", ProjectID: 1, Dir: t.TempDir()}, err: "not inside project"},
		{name: "a dir for the lead", input: CreateInput{Brief: "x", ProjectID: 1, To: ToLead, Dir: inside}, err: "takes no directory"},
		{name: "archived", input: CreateInput{Brief: "x", ProjectID: 2}, err: "archived"},
		{name: "unknown project", input: CreateInput{Brief: "x", ProjectID: 9}, err: "no project"},
		{name: "unknown target", input: CreateInput{Brief: "x", ProjectID: 1, To: "boss"}, err: "to is"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			job, err := r.svc.Create(ctx, tc.input, "")
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if job.ProjectID != tc.project || job.Role != tc.role || (tc.dir != "" && job.Dir != tc.dir) {
				t.Fatalf("job %+v", job)
			}
			if tc.project == 1 && tc.dir != "" && (job.Model != "haiku" || job.Isolate) {
				t.Fatalf("defaults not applied: model %q isolate %v", job.Model, job.Isolate)
			}
		})
	}
}

func TestAJobForTheLeadIsHeldByTheLeadNotTheOrchestrator(t *testing.T) {
	r, projects, alpha := projectRig(t)
	ctx := context.Background()
	_, _, orchPane := r.fake.AddWorkspace("orchestrator", "/tmp/orch")
	_ = r.fake.AddAgent(orchPane, OrchestratorName, "claude", herdr.StatusIdle, "s-orch")
	r.orch = OrchestratorInfo{PaneID: orchPane, Ready: true}
	job, err := r.svc.Create(ctx, CreateInput{Brief: "Plan alpha's release", ProjectID: alpha.ID}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.get(job.ID); got.State != StateQueued || got.AgentName != "" {
		t.Fatalf("with no lead running the job waits: %+v", got.Job)
	}
	if len(projects.given) != 1 || projects.given[0] != alpha.ID {
		t.Fatalf("the lead's start policy was not applied: %v", projects.given)
	}
	// An Unfiled job still goes to the orchestrator, not the project's queue.
	unfiled := r.create(CreateInput{Brief: "Unfiled thing"})
	if unfiled.AgentName != OrchestratorName {
		t.Fatalf("unfiled %+v", unfiled.Job)
	}
	_, _, leadPane := r.fake.AddWorkspace("lead alpha", alpha.Dir)
	_ = r.fake.AddAgent(leadPane, "lead-alpha", "claude", herdr.StatusIdle, "s-lead")
	projects.mu.Lock()
	projects.leads = []StandingAgent{{Name: "lead-alpha", PaneID: leadPane, Ready: true, ProjectID: alpha.ID, RootDir: alpha.RootDir}}
	projects.mu.Unlock()
	r.svc.DispatchStanding(ctx)
	got := r.get(job.ID)
	if got.State != StateRunning || got.AgentName != "lead-alpha" || got.PaneID != leadPane {
		t.Fatalf("the lead did not take its job: %+v", got.Job)
	}
	var typed string
	for _, prompt := range r.fake.Prompts() {
		if prompt.PaneID == leadPane {
			typed = prompt.Text
		}
	}
	if !strings.HasPrefix(typed, "[crowsnest job "+job.ID+"]") {
		t.Fatalf("typed to the lead: %q", typed)
	}
	// One job at a time, like the orchestrator.
	second, _ := r.svc.Create(ctx, CreateInput{Brief: "Another for alpha", ProjectID: alpha.ID}, "")
	r.svc.DispatchStanding(ctx)
	if got := r.get(second.ID); got.State != StateQueued {
		t.Fatalf("the lead was given two jobs: %+v", got.Job)
	}
}

func TestALeadDelegatesOnlyInsideItsProject(t *testing.T) {
	d := newDelegateRig(t, DelegationLimits{})
	root, _ := filepath.EvalSymlinks(t.TempDir())
	alpha := ProjectInfo{ID: 1, Slug: "alpha", Name: "Alpha", Dir: root, RootDir: root}
	_, _, leadPane := d.fake.AddWorkspace("lead alpha", root)
	_ = d.fake.AddAgent(leadPane, "lead-alpha", "claude", herdr.StatusIdle, "s-lead")
	projects := &fakeProjects{projects: []ProjectInfo{alpha}, leads: []StandingAgent{{Name: "lead-alpha", PaneID: leadPane, Ready: true, ProjectID: 1, RootDir: root}}}
	d.svc.opts.Projects = projects
	ctx := context.Background()
	caller := AgentCaller{PaneID: leadPane}

	inside, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "Test pkg", Dir: mkdir(t, root, "pkg"), Role: "tester"}, caller)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.get(inside.ID); got.State != StateRunning || got.ProjectID != 1 || got.Origin != "lead-alpha" {
		t.Fatalf("inside the project: %+v", got.Job)
	}
	noDir, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "Review it", Role: "reviewer"}, caller)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.get(noDir.ID); got.State != StateRunning || got.Dir != root {
		t.Fatalf("a child with no dir runs in the project: %+v", got.Job)
	}
	outside, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "Elsewhere", Dir: t.TempDir(), Role: "tester"}, caller)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.get(outside.ID); got.State != StateQueued || got.Cause != CauseApproval {
		t.Fatalf("outside the project waits for the human: %+v", got.Job)
	}
	codex, _ := d.svc.CreateForAgent(ctx, CreateInput{Brief: "Codex", Dir: root, Engine: EngineCodex}, caller)
	if got := d.get(codex.ID); got.Cause != CauseApproval {
		t.Fatalf("another engine waits for the human: %+v", got.Job)
	}
	// A pane that is neither a job agent, a lead nor the orchestrator gets nothing.
	if _, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "x", Dir: root}, AgentCaller{PaneID: "w9:p9"}); err == nil {
		t.Fatal("an unknown pane created a job")
	}
}
