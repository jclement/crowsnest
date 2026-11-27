// --demo for acceptance, budgets and wake-ups (DESIGN.md 6.15 to 6.18): the landable job's
// evidence (a passing check pinned to its worktree's HEAD, so Land works as it would for real),
// a job whose check failed three rounds and needs the human, and on request the fleet's usage
// limit for the banner.

package jobs

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// seedDemoEvidence records a check of job id at its worktree's HEAD and marks its accept state.
func seedDemoEvidence(ctx context.Context, s *Service, id int64, status, tail string, exit int) error {
	record, err := s.opts.Store.GetJob(ctx, id)
	if err != nil {
		return err
	}
	head, err := exec.CommandContext(ctx, "git", "-C", record.WorkDir, "rev-parse", "HEAD").Output() //nolint:gosec // the demo's own worktree
	if err != nil {
		return err
	}
	now := s.opts.Now()
	commit := strings.TrimSpace(string(head))
	if _, err := s.opts.Store.InsertCheck(ctx, store.CheckRecord{JobID: id, Commit: commit, Command: "mise run check", Status: status, ExitCode: exit,
		Duration: 47 * time.Second, OutputTail: tail, StartedAt: now.Add(-22 * time.Minute), FinishedAt: now.Add(-21 * time.Minute)}); err != nil {
		return err
	}
	switch status {
	case CheckPassed:
		record.AcceptState, record.AcceptDetail = AcceptAccepted, "Accepted: `mise run check` passed at "+shortHash(commit)+" (exit 0, 47s)"
	default:
		record.AcceptState, record.CheckRounds = AcceptFailed, 3
		record.AcceptDetail = "The check still fails after 3 rounds: `mise run check` failed at " + shortHash(commit) + " (exit 1, 47s)"
	}
	return s.opts.Store.UpdateJob(ctx, record)
}

func demoCheckFailed() demoWork {
	return demoWork{
		branch: "job/93-widget-sizes", title: "Render widgets at three sizes",
		brief: "Render widgets at three sizes\n\nSmall, medium and large, chosen by the caller.",
		files: map[string]string{
			"render.go": "package widgets\n\n// Size is how big a widget renders.\ntype Size int\n\nconst (\n\tSmall Size = iota\n\tMedium\n\tLarge\n)\n",
		},
		commit: "Render: three sizes",
		record: func(r *store.JobRecord) {
			r.State, r.Cause = string(StateNeedsYou), string(CauseCheck)
			r.CauseDetail = "The check still fails after 3 rounds: `mise run check` failed (exit 1, 47s)"
			r.ResultMD = "Added the three sizes and their tests."
			r.VerifiedJSON, r.NotVerifiedJSON = `["go test ./... passes"]`, `[]`
			r.LastMessage = "I've fixed the size test twice; the lint step still complains about render.go. Should I disable that lint rule?"
			r.LastMessageAt = time.Now().Add(-20 * time.Minute)
			r.Reviewer = ReviewerOff
		},
	}
}

const demoFailedTail = "go vet ./...\nok\ngolangci-lint run\nrender.go:4:6: exported type Size should have comment or be unexported (revive)\nrender.go:7:2: exported const Small should have comment (revive)\n2 issues.\nexit status 1"

// SeedDemoLimit records the fleet's usage limit for the demo's banner (CROWSNEST_DEMO_LIMIT).
func SeedDemoLimit(ctx context.Context, s *Service) {
	if s.opts.Limits == nil {
		return
	}
	jobID := ""
	_ = s.eachLive(ctx, []State{StateRunning}, func(r store.JobRecord) {
		if r.Stuck && Cause(r.Cause) == CauseLimit && jobID == "" {
			jobID = FormatID(r.ID)
		}
	})
	s.opts.Limits.Note(ctx, EngineClaude, jobID, "5-hour limit reached: try again in 50 minutes")
	// A second job agrees, so the limit holds the fleet (R6-S3: one job's word holds only itself).
	state := s.opts.Limits.Note(ctx, EngineClaude, "j_demo_second", "5-hour limit reached: try again in 50 minutes")
	if jobID != "" {
		_, _ = s.mutate(ctx, jobID, func(r *store.JobRecord, _ *change) error {
			r.RetryAt, r.CauseDetail = state.ResetAt, limitText(state)
			return nil
		})
	}
}
