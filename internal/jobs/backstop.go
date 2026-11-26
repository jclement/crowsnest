// What the herdr-side backstop (internal/orch/backstop.go, DESIGN.md 6.3) reads from the jobs
// service: the jobs it should watch, with when their agent's Stop hook last ran. NudgeIfDue
// (actions.go) is the other half.

package jobs

import (
	"context"

	"github.com/jclement/crowsnest/internal/orch"
)

// BackstopJobs lists running jobs that hold an agent and are not waiting on questions: the jobs
// whose agent must not sit idle with nothing declared. Every other state either lets the agent
// rest (blocked, needs_you, in_review, queued, terminal) or has no agent yet.
func (s *Service) BackstopJobs(ctx context.Context) ([]orch.BackstopJob, error) {
	records, err := s.opts.Store.ListJobs(ctx, []string{string(StateRunning)}, listLimit)
	if err != nil {
		return nil, err
	}
	var out []orch.BackstopJob
	for _, r := range records {
		if r.PaneID == "" || r.Waiting || !r.BriefSent {
			continue
		}
		out = append(out, orch.BackstopJob{ID: FormatID(r.ID), PaneID: r.PaneID, LastStopAt: r.LastStopAt})
	}
	return out, nil
}
