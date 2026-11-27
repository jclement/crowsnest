// job_adopt on the agent socket (DESIGN.md 7.10): POST /api/v1/jobs/adopt records work a standing
// agent was given in conversation as a job it holds itself (jobs.AdoptForStanding). Like
// job_create it needs an identified caller and shares job_create's per-caller rate limit; the
// jobs service decides who may (the orchestrator and leads, never a job agent).

package agentapi

import (
	"context"
	"net/http"

	"github.com/jclement/crowsnest/internal/jobs"
)

func (s *Server) handleJobAdopt(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	var input jobs.AdoptInput
	if !decodeBody(w, r, maxJobBody, &input) {
		return
	}
	caller := s.callerOf(r)
	if caller.PaneID == "" {
		writeError(w, http.StatusForbidden, codeForbidden, "Only an agent crowsnest started can record a job (no valid CROWSNEST_TOKEN came with this request).")
		return
	}
	if ok, retry := s.creates.Allow(quotaKey(caller)); !ok {
		writeQuota(w, retry, "Too many jobs recorded in a short time.")
		return
	}
	job, err := service.AdoptForStanding(r.Context(), input, jobs.AgentCaller{PaneID: caller.PaneID, JobID: caller.JobID})
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeData(w, http.StatusCreated, job)
}

// AdoptJob records work given in conversation as a job the caller holds (job_adopt).
func (c *Client) AdoptJob(ctx context.Context, input jobs.AdoptInput) (jobs.Job, error) {
	var out jobs.Job
	err := c.do(ctx, http.MethodPost, "/api/v1/jobs/adopt", input, &out)
	return out, err
}
