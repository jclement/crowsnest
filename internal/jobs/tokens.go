// A job's agent tokens end with the job (R2-L1). Without this a finished job's token still
// identified its pane and job: its holder could create jobs and ask questions as that job, and
// answers would be typed into whatever pane herdr later gave the same id. mutate calls
// revokeTokensLocked when a job reaches done, failed or cancelled; the pane-close half is
// serve's token sweeper (agentid.RevokeGonePanes).

package jobs

import (
	"context"

	"github.com/jclement/crowsnest/internal/store"
)

// revokeTokensLocked removes every token issued for the job (s.mu held). A failure is logged:
// the job's state change has already been saved and must stand.
func (s *Service) revokeTokensLocked(ctx context.Context, record store.JobRecord) {
	if s.opts.Launch.Identity == nil {
		return
	}
	if err := s.opts.Launch.Identity.RevokeJob(context.WithoutCancel(ctx), FormatID(record.ID)); err != nil {
		s.opts.Logger.Warn("revoking a finished job's agent tokens", "job", FormatID(record.ID), "err", err)
	}
}
