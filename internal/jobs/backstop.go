// What the herdr-side backstop (internal/orch/backstop.go, DESIGN.md 6.3) reads from the jobs
// service: the jobs it should watch, with when their agent's Stop hook last ran. NudgeIfDue
// (actions.go) is the other half.

package jobs

import (
	"context"

	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
)

// BackstopJobs lists running jobs that hold an agent and are not waiting on questions: the jobs
// whose agent must not sit idle with nothing declared. Every other state either lets the agent
// rest (blocked, needs_you, in_review, queued, terminal) or has no agent yet. A job whose own
// recovery owns it (a limit or API error on its retry schedule, an agent being restarted) is left
// to that recovery (R2-C09), and one crowsnest is acting on right now is skipped. Every running
// job is considered, not a page of the board (R2-C16).
func (s *Service) BackstopJobs(ctx context.Context) ([]orch.BackstopJob, error) {
	var out []orch.BackstopJob
	err := s.eachLive(ctx, []State{StateRunning}, func(r store.JobRecord) {
		recovering := r.Stuck && (Cause(r.Cause) == CauseLimit || Cause(r.Cause) == CauseServerError || Cause(r.Cause) == CauseExited)
		if r.PaneID == "" || r.Waiting || !r.BriefSent || recovering || hasFeedback(r) || s.claimLive(r) {
			return
		}
		out = append(out, orch.BackstopJob{ID: FormatID(r.ID), PaneID: r.PaneID, LastStopAt: r.LastStopAt})
	})
	return out, err
}
