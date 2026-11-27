// The job_adopt MCP tool (DESIGN.md 7.10): a standing agent records work the human gave it in
// conversation (the Claude app through Remote Control, or Chat) as a crowsnest job it holds, so
// the work is supervised and on the board. Proxied to POST /api/v1/jobs/adopt (adopt.go).

package agentapi

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jclement/crowsnest/internal/jobs"
)

// JobAdoptArgs are job_adopt's arguments.
type JobAdoptArgs struct {
	Title string `json:"title" jsonschema:"one line naming the work, as the board will show it"`
	Brief string `json:"brief" jsonschema:"what the human asked for, in a few lines: the request, constraints, and how you will know it is done"`
}

const jobAdoptDescription = "Record work the human gave you in conversation (the Claude app, Remote Control, or Chat) as a crowsnest job that you hold yourself: no new agent is started. " +
	"Orchestrator and project leads only (a lead's job lands in its project). Call it before starting the work and say the job id in your reply. " +
	"Then report with job_update(job_id) and finish with done(job_id). You hold one live job at a time; to hand work to someone else use job_create instead."

func init() {
	registerTool("job_adopt", func(server *mcp.Server, client *Client) {
		mcp.AddTool(server, &mcp.Tool{Name: "job_adopt", Title: "Record this as a job", Description: jobAdoptDescription},
			func(ctx context.Context, _ *mcp.CallToolRequest, args JobAdoptArgs) (*mcp.CallToolResult, any, error) {
				job, err := client.AdoptJob(ctx, jobs.AdoptInput{Title: args.Title, Brief: args.Brief})
				if err != nil {
					return errorResult(toolError("The job was not recorded", err)), nil, nil
				}
				return textResult(fmt.Sprintf("Recorded %s (%s); you hold it and it is running. Say %s in your reply, report with job_update(job_id=%q), and finish with done(job_id=%q).",
					job.ID, job.Title, job.ID, job.ID, job.ID)), nil, nil
			})
	})
}
