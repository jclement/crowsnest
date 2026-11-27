// Client methods for the job protocol and the hooks (jobs.go). The job id is passed explicitly;
// JobID resolves the default (the caller's CROWSNEST_JOB).

package agentapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/pause"
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

// RequestApproval posts a PermissionRequest hook's input (DESIGN.md 8.7).
func (c *Client) RequestApproval(ctx context.Context, input json.RawMessage) (ApprovalReply, error) {
	var out ApprovalReply
	err := c.do(ctx, http.MethodPost, "/api/v1/approvals", input, &out)
	return out, err
}

// WaitApproval long-polls an approval for up to wait.
func (c *Client) WaitApproval(ctx context.Context, id string, wait time.Duration) (ApprovalReply, error) {
	var out ApprovalReply
	err := c.do(ctx, http.MethodGet, "/api/v1/approvals/"+url.PathEscape(id)+"?wait="+strconv.Itoa(int(wait.Seconds())), nil, &out)
	return out, err
}

// GuardDenied tells crowsnest the guard refused a command.
func (c *Client) GuardDenied(ctx context.Context, denial GuardDenial) error {
	return c.do(ctx, http.MethodPost, "/api/v1/guard/denied", denial, nil)
}

// LandResponse is what a land request returns: the question the human answers.
type LandResponse struct {
	QuestionID string `json:"question_id"`
	Status     string `json:"status"`
	Note       string `json:"note"`
}

// RequestLand asks the human to merge, push or discard a finished job (DESIGN.md 6.11).
func (c *Client) RequestLand(ctx context.Context, id, action, reason string) (LandResponse, error) {
	var out LandResponse
	err := c.do(ctx, http.MethodPost, jobPath(id, "/land"), LandRequest{Action: action, Reason: reason}, &out)
	return out, err
}

// Pause pauses crowsnest's automation; until zero means until resumed (DESIGN.md 6.13).
func (c *Client) Pause(ctx context.Context, until time.Time, reason string) (pause.State, error) {
	var out pause.State
	err := c.do(ctx, http.MethodPost, "/api/v1/pause", PauseRequest{Until: until, Reason: reason}, &out)
	return out, err
}

// Resume resumes it (refused to an agent).
func (c *Client) Resume(ctx context.Context) (pause.State, error) {
	var out pause.State
	err := c.do(ctx, http.MethodPost, "/api/v1/resume", nil, &out)
	return out, err
}

// PauseState reads the switch.
func (c *Client) PauseState(ctx context.Context) (pause.State, error) {
	var out pause.State
	err := c.do(ctx, http.MethodGet, "/api/v1/pause", nil, &out)
	return out, err
}
