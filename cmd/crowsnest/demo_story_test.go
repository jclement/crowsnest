// TestDemoStory boots the --demo seeding as serve runs it (the projects, the jobs, then the
// approvals and questions) against the fake fleet and an in-memory store, and checks that the
// pieces tell one story (DESIGN.md 21.5): every job's pane is in its project's workspace and holds
// the agent the job names, a dialog job's cause is the dialog on its screen, every approval and
// question names the job whose pane raised it and whose project its repo is, and the busy-day
// obligations are there.

package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/demostory"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/risk"
	"github.com/jclement/crowsnest/internal/store"
)

// demoWorld is the seeded demo, read back.
type demoWorld struct {
	demo      *fake.Demo
	jobs      map[string]jobs.Job // by id
	slugOf    map[int64]string    // project id → slug
	workspace map[string]string   // pane id → workspace label
	agentIn   map[string]string   // pane id → agent name
	approvals []approvals.Approval
	questions []questions.Question
	landing   int // the landing demo's jobs, counted and left out of the story's invariants
}

func seedDemoWorld(t *testing.T) demoWorld {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	demo := fake.NewDemo()
	jobService := jobs.New(jobs.Options{Store: db, Herdr: demo, Logger: logger, ApprovalPending: func(ctx context.Context, jobID string) bool {
		count, err := db.CountPendingApprovals(ctx, jobID)
		return err == nil && count > 0
	}})
	// Wired as newApprovals wires it: the job's directory is the root its calls are judged by, and
	// a change moves the job's state.
	questionService := questions.New(questions.Options{Store: db, Logger: logger})
	wiring := &projectWiring{service: projects.New(projects.Options{Store: db})}
	links := &approvalWiring{projects: wiring.service}
	links.jobs.Store(jobService)
	var approvalService *approvals.Service
	approvalService = approvals.New(approvals.Options{Store: db, Logger: logger, Scope: links.scope,
		RiskContext: func(ctx context.Context, jobID string) risk.Context {
			job, err := jobService.Get(ctx, jobID)
			if err != nil {
				return risk.Context{}
			}
			return risk.Context{JobRoot: firstNonEmpty(job.WorkDir, job.Dir)}
		},
		OnChange: func(ctx context.Context, a approvals.Approval, change string) {
			summary := a.Summary
			if change != "requested" {
				summary = approvalService.WaitingSummary(ctx, a.JobID)
			}
			_ = jobService.SetApprovalWaiting(ctx, a.JobID, change == "requested", summary)
		}})

	projectOf := seedDemoProjects(ctx, config.Config{StateDir: t.TempDir()}, wiring, logger)
	startJobs(ctx, jobService, demo, projectOf, t.TempDir(), logger)
	if err := startDemoInbox(ctx, demoInbox{demo: demo, jobs: jobService, approvals: approvalService, questions: questionService}, logger); err != nil {
		t.Fatal(err)
	}

	world := demoWorld{demo: demo, jobs: map[string]jobs.Job{}, slugOf: map[int64]string{}, workspace: map[string]string{}, agentIn: map[string]string{}}
	list, err := jobService.List(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range list {
		// The landing demo (internal/jobs/demo_landing.go, j_18 to j_22) is a scenario of its
		// own in a real git repository, unfiled and held by "job-demo"; it is not part of the
		// five-project story these invariants describe (REPORT: a known gap).
		if strings.Contains(job.Dir, "demo-src/widgets") || strings.Contains(job.Dir, "demo-src/.worktrees") {
			world.landing++
			continue
		}
		world.jobs[job.ID] = job
	}
	projectList, _ := wiring.service.List(ctx, false)
	for _, project := range projectList {
		world.slugOf[project.ID] = project.Slug
	}
	snapshot, _ := demo.Snapshot(ctx)
	labels := map[string]string{}
	for _, workspace := range snapshot.Workspaces {
		labels[workspace.ID] = workspace.Label
	}
	for _, pane := range snapshot.Panes {
		world.workspace[pane.ID] = labels[pane.WorkspaceID]
	}
	for _, agent := range snapshot.Agents {
		world.agentIn[agent.PaneID] = agent.Name
	}
	if world.approvals, err = approvalService.List(ctx, nil, 100); err != nil {
		t.Fatal(err)
	}
	if world.questions, err = questionService.List(ctx, "all"); err != nil {
		t.Fatal(err)
	}
	return world
}

// slug is the project a job is filed under ("" for Unfiled).
func (w demoWorld) slug(job jobs.Job) string { return w.slugOf[job.ProjectID] }

func TestDemoStory(t *testing.T) {
	w := seedDemoWorld(t)
	// The story's 17 jobs, and the landing demo's five beside them (j_18 to j_22).
	if len(w.slugOf) != 5 || len(w.jobs) != 17 || w.landing != 5 {
		t.Fatalf("%d projects, %d story jobs and %d landing jobs", len(w.slugOf), len(w.jobs), w.landing)
	}
	t.Run("jobs sit in their project's workspace, held by the agent they name", func(t *testing.T) { checkDemoJobs(t, w) })
	t.Run("a dialog job's cause is the dialog on its screen", func(t *testing.T) { checkDemoDialogs(t, w) })
	t.Run("approvals come from their job's pane", func(t *testing.T) { checkDemoApprovals(t, w) })
	t.Run("questions come from their job's pane and project", func(t *testing.T) { checkDemoQuestions(t, w) })
	t.Run("the board has variety and a busy day's obligations", func(t *testing.T) { checkDemoShape(t, w) })
}

func checkDemoJobs(t *testing.T, w demoWorld) {
	for id, job := range w.jobs {
		if job.Dir != "" && w.slug(job) == "" {
			t.Errorf("%s names %s but is Unfiled", id, job.Dir)
		}
		if job.PaneID == "" {
			continue
		}
		if got := w.agentIn[job.PaneID]; got != job.AgentName {
			t.Errorf("%s names agent %q; its pane %s holds %q", id, job.AgentName, job.PaneID, got)
		}
		wantWorkspace := w.slug(job)
		if wantWorkspace == "" {
			wantWorkspace = demostory.Orchestrator
		}
		if got := w.workspace[job.PaneID]; got != wantWorkspace {
			t.Errorf("%s (%s) sits in workspace %q, want %q", id, job.Title, got, wantWorkspace)
		}
		if !jobs.IsStanding(job.AgentName) && job.AgentName != "job-"+strings.TrimPrefix(id, "j_") {
			t.Errorf("%s is held by %q; a job agent is named after its job", id, job.AgentName)
		}
		if slug := demostory.ProjectOf(job.AgentName); !jobs.IsStanding(job.AgentName) && slug != w.slug(job) {
			t.Errorf("%s: demostory files %s under %q, the board under %q", id, job.AgentName, slug, w.slug(job))
		}
	}
}

func checkDemoDialogs(t *testing.T, w demoWorld) {
	dialogs := 0
	for id, job := range w.jobs {
		if job.Cause != jobs.CauseDialog {
			continue
		}
		dialogs++
		command := w.demo.DialogCommand(job.PaneID)
		screen, _ := w.demo.PaneRead(context.Background(), job.PaneID, herdr.ReadOptions{})
		if command == "" || job.CauseDetail != "Claude wants to run: "+command || !strings.Contains(screen.Text, "Bash("+command+")") {
			t.Errorf("%s says %q; its pane shows:\n%s", id, job.CauseDetail, screen.Text)
		}
	}
	if dialogs != 1 {
		t.Errorf("%d jobs at a terminal dialog, want 1", dialogs)
	}
}

func checkDemoApprovals(t *testing.T, w demoWorld) {
	// ap_4 is the call the demo's project rule allowed (DESIGN.md 8.7, Policy): settled at once,
	// by policy, from the Crowsnest reviewer's job; the other three wait for a human.
	var pending []approvals.Approval
	for _, approval := range w.approvals {
		if approval.ID == "ap_4" {
			if approval.Status != approvals.StatusAllowed || approval.DecidedVia != approvals.ViaPolicy || !strings.Contains(approval.StatusReason, "(project Crowsnest)") {
				t.Errorf("the demo's auto-allowed call: %+v", approval)
			}
			continue
		}
		pending = append(pending, approval)
	}
	w.approvals = pending
	if len(w.approvals) != 3 {
		t.Fatalf("%d approvals waiting for a human", len(w.approvals))
	}
	// One of each risk the tray shows differently: two low (one answerable from a notification),
	// and the dangerous one that needs a second confirmation.
	risks := map[string]risk.Level{}
	for _, approval := range w.approvals {
		risks[approval.ID] = approval.Risk
	}
	if risks["ap_1"] != risk.Low || risks["ap_2"] != risk.Low || risks["ap_3"] != risk.Dangerous {
		t.Errorf("risks %v", risks)
	}
	for _, approval := range w.approvals {
		job, ok := w.jobs[approval.JobID]
		switch {
		case !ok:
			t.Errorf("%s names no job (%q)", approval.ID, approval.JobID)
		case job.PaneID != approval.PaneID || job.AgentName != approval.AgentName:
			t.Errorf("%s comes from %s/%s; its job %s is held by %s/%s", approval.ID, approval.PaneID, approval.AgentName, job.ID, job.PaneID, job.AgentName)
		case job.State != jobs.StateNeedsYou || job.Cause != jobs.CauseApprovalTool || job.CauseDetail != approval.Summary:
			t.Errorf("%s: its job %s is %s/%s %q", approval.ID, job.ID, job.State, job.Cause, job.CauseDetail)
		case approval.Cwd != job.Dir:
			t.Errorf("%s runs in %s; its job in %s", approval.ID, approval.Cwd, job.Dir)
		}
		if agent, _ := w.demo.AgentGet(context.Background(), approval.PaneID); agent.AgentStatus != herdr.StatusBlocked {
			t.Errorf("%s: the agent in its pane is %s, not at the permission dialog", approval.ID, agent.AgentStatus)
		}
	}
}

func checkDemoQuestions(t *testing.T, w demoWorld) {
	if len(w.questions) != 8 {
		t.Fatalf("%d questions", len(w.questions))
	}
	open := map[string]int{}
	for _, question := range w.questions {
		job, ok := w.jobs[question.JobID]
		if !ok || job.PaneID != question.SourcePane {
			t.Errorf("%s (%s) comes from pane %s; its job %q sits in %s", question.ID, question.Title, question.SourcePane, question.JobID, job.PaneID)
			continue
		}
		if repo := question.Context["repo"]; repo != w.slug(job) {
			t.Errorf("%s says repo %q; its job %s is in %q", question.ID, repo, job.ID, w.slug(job))
		}
		if question.Status != questions.StatusOpen {
			continue
		}
		open[job.ID]++
		if question.Urgency == questions.UrgencyBlocking && (job.State != jobs.StateNeedsYou || job.Cause != jobs.CauseQuestions) {
			t.Errorf("%s is blocking; its job %s is %s/%s", question.ID, job.ID, job.State, job.Cause)
		}
	}
	for id, job := range w.jobs {
		if job.Cause == jobs.CauseQuestions && (open[id] == 0 || !job.Waiting) {
			t.Errorf("%s waits on questions with %d open (waiting=%v)", id, open[id], job.Waiting)
		}
	}
}

func checkDemoShape(t *testing.T, w demoWorld) {
	live, waiting := map[string]int{}, map[string]int{}
	var child, lead bool
	for _, job := range w.jobs {
		if job.PaneID != "" && !job.State.Terminal() {
			live[w.slug(job)]++
		}
		if job.State == jobs.StateNeedsYou || job.Stuck {
			waiting[w.slug(job)]++
		}
		if parent, ok := w.jobs[job.ParentID]; ok && parent.Origin == jobs.LeadPrefix+demostory.Crowsnest && job.Origin == jobs.OriginAgent {
			child, lead = true, w.slug(parent) == demostory.Crowsnest
		}
	}
	for _, slug := range demostory.Slugs {
		if live[slug] == 0 {
			t.Errorf("project %s has no live job", slug)
		}
	}
	if waiting[demostory.Infra] != 0 || live[""] == 0 || !child || !lead {
		t.Errorf("Infra waiting %d, Unfiled live %d, a lead's job with a child: %v", waiting[demostory.Infra], live[""], child && lead)
	}
	// The busy-day obligations: a dangerous approval in Crowsnest, a blocking question in
	// Website, a terminal dialog elsewhere, and Mobile stuck on the usage limit.
	var dangerous, blocking, dialog, limit bool
	for _, approval := range w.approvals {
		dangerous = dangerous || (approval.Risk == risk.Dangerous && w.slug(w.jobs[approval.JobID]) == demostory.Crowsnest && strings.HasPrefix(w.jobs[approval.JobID].Title, "Review "))
	}
	for _, question := range w.questions {
		blocking = blocking || (question.Status == questions.StatusOpen && question.Urgency == questions.UrgencyBlocking && w.slug(w.jobs[question.JobID]) == demostory.Website)
	}
	for _, job := range w.jobs {
		slug := w.slug(job)
		dialog = dialog || (job.Cause == jobs.CauseDialog && slug != demostory.Crowsnest && slug != demostory.Website)
		limit = limit || (job.Cause == jobs.CauseLimit && job.Stuck && slug == demostory.Mobile)
	}
	if !dangerous || !blocking || !dialog || !limit {
		t.Errorf("obligations: dangerous approval %v, blocking question %v, dialog %v, limit %v", dangerous, blocking, dialog, limit)
	}
}
