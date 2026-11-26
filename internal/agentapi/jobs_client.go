// Client methods for the job protocol and the hooks (jobs.go). The job id is passed explicitly;
// JobID resolves the default (the caller's CROWSNEST_JOB).

package agentapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/jclement/crowsnest/internal/jobs"
)

// ErrNoJob means no job id was given and CROWSNEST_JOB is not set.
var ErrNoJob = errors.New("no job: pass the job id (job_id, or --job j_<n>); inside a job's pane CROWSNEST_JOB names it")

// JobID returns explicit when set, else the caller's own job.
func (c *Client) JobID(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if c.job != "" {
		return c.job, nil
	}
	return "", ErrNoJob
}

// WithJob returns a copy of the client whose default job is jobID (a hook baked with --job, or an
// MCP call naming job_id). It changes which job the paths name, not what the server lets the
// caller touch: that comes from the caller's token.
func (c *Client) WithJob(jobID string) *Client {
	copied := *c
	copied.job = jobID
	return &copied
}

func jobPath(id, suffix string) string {
	return "/api/v1/jobs/" + url.PathEscape(id) + suffix
}

// CreateJob delegates a child job.
func (c *Client) CreateJob(ctx context.Context, input jobs.CreateInput) (jobs.Job, error) {
	var out jobs.Job
	err := c.do(ctx, http.MethodPost, "/api/v1/jobs", input, &out)
	return out, err
}

// ListJobs lists jobs (state may be a comma-separated filter).
func (c *Client) ListJobs(ctx context.Context, state string) ([]jobs.Job, error) {
	var out []jobs.Job
	path := "/api/v1/jobs"
	if state != "" {
		path += "?state=" + url.QueryEscape(state)
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// GetJob reads a job with its timeline and questions.
func (c *Client) GetJob(ctx context.Context, id string) (jobs.Detail, error) {
	var out jobs.Detail
	err := c.do(ctx, http.MethodGet, jobPath(id, ""), nil, &out)
	return out, err
}

// JobContext is job_get: the job as text for an agent.
func (c *Client) JobContext(ctx context.Context, id string) (string, error) {
	var out ContextResponse
	err := c.do(ctx, http.MethodGet, jobPath(id, "/context"), nil, &out)
	return out.Text, err
}

// UpdateJob is job_update.
func (c *Client) UpdateJob(ctx context.Context, id string, input jobs.UpdateInput) (jobs.Job, error) {
	var out jobs.Job
	err := c.do(ctx, http.MethodPost, jobPath(id, "/update"), input, &out)
	return out, err
}

// DoneJob is done.
func (c *Client) DoneJob(ctx context.Context, id string, input jobs.DoneInput) (jobs.Job, error) {
	input.Attachments = AbsPaths(input.Attachments)
	var out jobs.Job
	err := c.do(ctx, http.MethodPost, jobPath(id, "/done"), input, &out)
	return out, err
}

// FailJob is fail.
func (c *Client) FailJob(ctx context.Context, id, reason string) (jobs.Job, error) {
	var out jobs.Job
	err := c.do(ctx, http.MethodPost, jobPath(id, "/fail"), FailRequest{Reason: reason}, &out)
	return out, err
}

// Hook forwards a hook's raw stdin and returns what the hook should print.
func (c *Client) Hook(ctx context.Context, event string, input json.RawMessage) (string, error) {
	var out jobs.HookReply
	err := c.do(ctx, http.MethodPost, "/api/v1/hooks/"+url.PathEscape(event), input, &out)
	return out.Stdout, err
}
