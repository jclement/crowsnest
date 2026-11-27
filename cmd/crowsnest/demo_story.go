// The --demo story's second half (DESIGN.md 21.5): once the demo jobs are on the board, the
// seeded approvals and questions are raised from those jobs' panes and name them, and the fake
// fleet is told which permission dialogs to show and when they are decided. This runs after
// startJobs rather than where the approvals and questions services start, because an approval or
// a question can only name a job that exists; it also keeps the question ids (q_1 to q_8) and
// approval ids (ap_1 to ap_3) the tests use, since nothing else asks first.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/demostory"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
)

// demoInbox is what the second half of the demo seeding needs.
type demoInbox struct {
	demo      *fake.Demo
	jobs      *jobs.Service
	approvals *approvals.Service
	questions *questions.Service
	files     *attachWiring // nil: no attachments
	stateDir  string
}

// envDemoParallel adds two parallel low-risk calls from one demo job (the batch allow's e2e).
const envDemoParallel = "CROWSNEST_DEMO_PARALLEL"

// startDemoInbox seeds the demo approvals and questions (each only into an empty store), attaches
// the demo files, and starts their rotations.
func startDemoInbox(ctx context.Context, inbox demoInbox, logger *slog.Logger) error {
	askers := demoAskers(ctx, inbox.jobs)
	approvalPanes := approvals.DemoPanes{Askers: askers, Asked: inbox.showApproval, Settled: inbox.settleApproval}
	if err := approvals.SeedDemo(ctx, inbox.approvals, approvalPanes); err != nil {
		return fmt.Errorf("seeding the demo approvals: %w", err)
	}
	if os.Getenv(envDemoParallel) != "" {
		if err := approvals.SeedDemoParallel(ctx, inbox.approvals, approvalPanes); err != nil {
			return fmt.Errorf("seeding the demo's parallel approvals: %w", err)
		}
	}
	go approvals.RunDemo(ctx, inbox.approvals, approvalPanes, 0, logger)
	questionPanes := questions.DemoPanes{Askers: askers}
	if err := questions.SeedDemo(ctx, inbox.questions, questionPanes); err != nil {
		return fmt.Errorf("seeding the demo questions: %w", err)
	}
	go questions.RunDemo(ctx, inbox.questions, questionPanes, 0, logger)
	if inbox.files != nil {
		if err := seedDemoAttachments(ctx, inbox.files, inbox.questions, inbox.stateDir); err != nil {
			logger.Warn("seeding the demo attachments", "err", err)
		}
	}
	return nil
}

// demoAskers resolves a demo agent to the live job it holds, read from the board each time (a
// question asked later finds the job as it is then). An agent holding several jobs (the
// orchestrator, once it takes the queued one) asks for its oldest.
func demoAskers(ctx context.Context, service *jobs.Service) demostory.Askers {
	return func(agent string) demostory.Asker {
		list, err := service.List(ctx, []jobs.State{jobs.StateRunning, jobs.StateNeedsYou, jobs.StateBlocked})
		if err != nil {
			return demostory.Asker{}
		}
		var found *jobs.Job
		for i := range list {
			job := &list[i]
			if job.AgentName == agent && job.PaneID != "" && (found == nil || jobNumber(job.ID) < jobNumber(found.ID)) {
				found = job
			}
		}
		if found == nil {
			return demostory.Asker{}
		}
		return demostory.Asker{AgentName: agent, PaneID: found.PaneID, JobID: found.ID, Dir: firstNonEmpty(found.WorkDir, found.Dir)}
	}
}

func jobNumber(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "j_"))
	return n
}

// showApproval draws a pending approval's permission dialog on its pane.
func (inbox demoInbox) showApproval(approval approvals.Approval) {
	if approval.PaneID == "" || approval.Status != approvals.StatusPending {
		return
	}
	var input struct {
		Command     string `json:"command"`
		Description string `json:"description"`
		FilePath    string `json:"file_path"`
	}
	_ = json.Unmarshal(approval.ToolInput, &input)
	switch {
	case approval.ToolName == "Bash" && input.Command != "":
		inbox.demo.AwaitApproval(approval.PaneID, "Bash", input.Command, input.Description)
	case input.FilePath != "":
		inbox.demo.AwaitApproval(approval.PaneID, approval.ToolName, input.FilePath, "")
	default:
		inbox.demo.AwaitApproval(approval.PaneID, approval.ToolName, approval.Summary, "")
	}
}

// settleApproval is the agent's side of a decided approval.
func (inbox demoInbox) settleApproval(approval approvals.Approval) {
	decided := approval.Status == approvals.StatusAllowed || approval.Status == approvals.StatusDenied
	if approval.PaneID != "" && decided { // an expired one leaves the dialog where it is
		inbox.demo.ApprovalSettled(approval.PaneID, approval.Status == approvals.StatusAllowed)
	}
}
