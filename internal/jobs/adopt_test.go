// job_adopt (DESIGN.md 7.10): work given in conversation becomes a job the standing agent holds
// itself: the orchestrator's Unfiled, a lead's in its project, running in the agent's own pane
// with nothing launched; a job agent is refused, and so is a second live job for the same agent.

package jobs

import (
	"context"
	"strings"
	"testing"
)

func TestAdoptForStandingOrchestrator(t *testing.T) {
	r := newRig(t, nil)
	r.orch = OrchestratorInfo{PaneID: "w1:p1", Ready: true}
	ctx := context.Background()
	job, err := r.svc.AdoptForStanding(ctx, AdoptInput{Title: "Tidy the README", Brief: "Jeff asked in the Claude app."}, AgentCaller{PaneID: "w1:p1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(job.ID, "j_") || job.Title != "Tidy the README" || job.State != StateRunning || job.ProjectID != 0 {
		t.Fatalf("adopted %+v", job)
	}
	detail := r.get(job.ID)
	if detail.AgentName != OrchestratorName || detail.PaneID != "w1:p1" || detail.Dir != "" {
		t.Fatalf("held by %q in %q (dir %q)", detail.AgentName, detail.PaneID, detail.Dir)
	}
	if !strings.Contains(detail.BriefMD, "Jeff asked in the Claude app.") {
		t.Fatalf("brief %q", detail.BriefMD)
	}
	found := false
	for _, event := range detail.Timeline {
		found = found || (event.Kind == EventLaunch && strings.Contains(event.Summary, "from conversation"))
	}
	if !found {
		t.Fatalf("no 'from conversation' launch event: %+v", detail.Timeline)
	}
	if starts := r.fake.Starts(); len(starts) != 0 {
		t.Fatalf("adopting started an agent: %v", starts)
	}
	// One live job at a time: a second adoption is refused until the first is done.
	if _, err := r.svc.AdoptForStanding(ctx, AdoptInput{Title: "Another"}, AgentCaller{PaneID: "w1:p1"}); !IsValidation(err) {
		t.Fatalf("second adoption: %v", err)
	}
	if _, err := r.svc.Done(ctx, job.ID, DoneInput{ResultMD: "Tidied.", Verified: []string{}, NotVerified: []string{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.AdoptForStanding(ctx, AdoptInput{Title: "Another"}, AgentCaller{PaneID: "w1:p1"}); err != nil {
		t.Fatalf("adoption after done: %v", err)
	}
}

func TestAdoptForStandingLead(t *testing.T) {
	r, projects, alpha := projectRig(t)
	projects.leads = []StandingAgent{{Name: "lead-alpha", PaneID: "w3:p1", Ready: true, ProjectID: alpha.ID, RootDir: alpha.RootDir}}
	job, err := r.svc.AdoptForStanding(context.Background(), AdoptInput{Title: "Fix the flaky test", Brief: "From the Claude app."}, AgentCaller{PaneID: "w3:p1"})
	if err != nil {
		t.Fatal(err)
	}
	detail := r.get(job.ID)
	if detail.ProjectID != alpha.ID || detail.AgentName != "lead-alpha" || detail.PaneID != "w3:p1" || detail.State != StateRunning {
		t.Fatalf("lead's adopted job %+v", detail.Job)
	}
}

func TestAdoptForStandingRefusals(t *testing.T) {
	r := newRig(t, nil)
	r.orch = OrchestratorInfo{PaneID: "w1:p1", Ready: true}
	ctx := context.Background()
	if _, err := r.svc.AdoptForStanding(ctx, AdoptInput{Title: "x"}, AgentCaller{PaneID: "w2:p1", JobID: "j_4"}); !IsValidation(err) || !strings.Contains(err.Error(), "job agent") {
		t.Fatalf("job agent: %v", err)
	}
	if _, err := r.svc.AdoptForStanding(ctx, AdoptInput{Title: "x"}, AgentCaller{PaneID: "w9:p9"}); !IsValidation(err) {
		t.Fatalf("unknown pane: %v", err)
	}
	if _, err := r.svc.AdoptForStanding(ctx, AdoptInput{Title: "  "}, AgentCaller{PaneID: "w1:p1"}); !IsValidation(err) {
		t.Fatalf("no title: %v", err)
	}
}
