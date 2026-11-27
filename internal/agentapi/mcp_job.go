// The job MCP tools (DESIGN.md 6.2): job_update, done, fail, job_create and job_get, proxied to
// the socket. A job agent's MCP server has CROWSNEST_JOB in its environment (mcp.json); the
// orchestrator's does not, so every tool also takes job_id. The descriptions are the agent's
// instructions: short and imperative.

package agentapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jclement/crowsnest/internal/jobs"
)

// JobUpdateArgs are job_update's arguments.
type JobUpdateArgs struct {
	JobID    string `json:"job_id,omitempty" jsonschema:"your job id (j_<n>). Leave empty in a job's own session; the orchestrator passes the id from the [crowsnest job j_<n>] header"`
	Progress string `json:"progress" jsonschema:"one line: what you are doing now"`
	State    string `json:"state,omitempty" jsonschema:"blocked (waiting on something other than the human; give reason) or running (it cleared). Omit otherwise"`
	Reason   string `json:"reason,omitempty" jsonschema:"with state blocked: what you are waiting on"`
	Waiting  *bool  `json:"waiting,omitempty" jsonschema:"true: nothing is left that your open questions do not block, so you may end your turn until an answer arrives"`
}

// DoneArgs are done's arguments.
type DoneArgs struct {
	JobID       string   `json:"job_id,omitempty" jsonschema:"your job id (j_<n>). Leave empty in a job's own session; the orchestrator passes the id from the [crowsnest job j_<n>] header"`
	ResultMD    string   `json:"result_md" jsonschema:"Markdown: what was done, where (files, branch, commits), and anything the human must know"`
	Verified    []string `json:"verified" jsonschema:"what you checked and saw, each with the command or evidence. [] if nothing"`
	NotVerified []string `json:"not_verified" jsonschema:"what you did not check or could not run. [] if nothing"`
	Attachments []string `json:"attachments,omitempty" jsonschema:"files to attach to the result (a report, a screenshot of what you built): paths inside your working directory or job directory, at most 10"`
	// WalkthroughMD is the change explained chunk by chunk (DESIGN.md 6.11).
	WalkthroughMD string `json:"walkthrough_md,omitempty" jsonschema:"for work in a git worktree: Markdown walking a reviewer through the change chunk by chunk, in the order it is best understood (schema, logic, API, UI, tests), naming each file path, why each part exists, what was tested and what was not, and the risk both ways"`
}

// LandArgs are job_land's arguments.
type LandArgs struct {
	JobID  string `json:"job_id" jsonschema:"the finished job to land (j_<n>): a child of yours, or one in your project"`
	Action string `json:"action" jsonschema:"merge (into its base branch locally), pr (push and open a pull request) or discard"`
	Reason string `json:"reason,omitempty" jsonschema:"one or two sentences for the human: why this action, what you checked"`
}

// FailArgs are fail's arguments.
type FailArgs struct {
	JobID  string `json:"job_id,omitempty" jsonschema:"your job id (j_<n>). Leave empty in a job's own session; the orchestrator passes the id from the [crowsnest job j_<n>] header"`
	Reason string `json:"reason" jsonschema:"why the job cannot be finished, and what was tried"`
}

// JobCreateArgs are job_create's arguments.
type JobCreateArgs struct {
	Brief    string `json:"brief" jsonschema:"what needs doing: the first line is the title; include context, constraints and where to look"`
	Role     string `json:"role,omitempty" jsonschema:"builder (default with a dir), reviewer, tester, lead, or a custom role"`
	Dir      string `json:"dir,omitempty" jsonschema:"absolute path of the directory or git repo to work in. A builder in a git repo gets its own worktree. Required for code work"`
	DoneWhen string `json:"done_when,omitempty" jsonschema:"how the child knows it is finished"`
	Engine   string `json:"engine,omitempty" jsonschema:"claude or codex; default: your own engine (another one needs the human's approval)"`
	Model    string `json:"model,omitempty" jsonschema:"model name, when not your own"`
	Isolate  *bool  `json:"isolate,omitempty" jsonschema:"force a git worktree on or off (default: yours, or the role's)"`
	// Project and To are the orchestrator's: hand the work to a project (DESIGN.md 21.2).
	Project string `json:"project,omitempty" jsonschema:"orchestrator only: a project's short name or id (project_list). With no dir the job goes to that project's lead"`
	To      string `json:"to,omitempty" jsonschema:"with project: lead (the default with no dir) or job (a job agent of its own in the project's directory)"`
}

// JobGetArgs are job_get's arguments.
type JobGetArgs struct {
	JobID string `json:"job_id,omitempty" jsonschema:"the job to read; empty for your own. A lead reads its children by id"`
}

const (
	jobUpdateDescription = "Report what you are doing now, in one line. Call it at the start of each step and at least every few minutes; the human watches this line on their phone.\n\n" +
		"state=blocked with a reason: you are waiting on something other than the human (an outage, another job). state=running: it cleared.\n" +
		"waiting=true: you asked questions and truly nothing is left that they do not block; you may then end your turn and the answer will wake you."
	doneDescription = "Declare your job finished. Call it once, when the work is complete and checked. result_md says what was done. " +
		"verified lists what you checked and saw pass, with commands. not_verified lists what you skipped or could not run. Both lists are required (use [] for none). " +
		"Never call done over failing tests; say so in not_verified or call fail."
	failDescription      = "Declare that your job cannot be finished, with the reason and what you tried. Use it only when no path forward is left; if you need a decision, call ask instead."
	jobCreateDescription = "Delegate a unit of work as a child job (leads). crowsnest starts a new agent for it with the role's instructions; a builder given a git repo works in its own worktree. " +
		"A child inside your own directory, with your engine, starts at once; anything wider (another directory, no directory, another engine, isolate=false when you are isolated) waits for the human's approval. " +
		"Returns the child's id. Follow it with job_get(job_id), and check its evidence when it is done before relying on it."
	jobGetDescription = "Read a job: its brief, state, progress, and your questions with their answers. Read your own after a restart or compaction, or when you lose track. A lead reads a child's by id."
)

func init() {
	registerTool("job_update", func(server *mcp.Server, client *Client) {
		mcp.AddTool(server, &mcp.Tool{Name: "job_update", Title: "Report progress", Description: jobUpdateDescription},
			func(ctx context.Context, _ *mcp.CallToolRequest, args JobUpdateArgs) (*mcp.CallToolResult, any, error) {
				return jobCall(client, args.JobID, func(id string) (string, error) {
					job, err := client.UpdateJob(ctx, id, jobs.UpdateInput{Progress: args.Progress, State: args.State, Reason: args.Reason, Waiting: args.Waiting})
					if err != nil {
						return "", err
					}
					text := fmt.Sprintf("Recorded. Job %s is %s.", job.ID, job.State)
					switch job.State {
					case jobs.StateNeedsYou:
						text += " It waits on the human; you may end your turn, and the answer will arrive as a message."
					case jobs.StateRunning:
						text += " Keep going."
					}
					return text, nil
				}), nil, nil
			})
	})
	registerTool("done", func(server *mcp.Server, client *Client) {
		mcp.AddTool(server, &mcp.Tool{Name: "done", Title: "Finish the job", Description: doneDescription},
			func(ctx context.Context, _ *mcp.CallToolRequest, args DoneArgs) (*mcp.CallToolResult, any, error) {
				return jobCall(client, args.JobID, func(id string) (string, error) {
					job, err := client.DoneJob(ctx, id, jobs.DoneInput{ResultMD: args.ResultMD, Verified: args.Verified, NotVerified: args.NotVerified, Attachments: args.Attachments, WalkthroughMD: args.WalkthroughMD})
					if err != nil {
						return "", err
					}
					if job.State == jobs.StateInReview {
						return fmt.Sprintf("Job %s is in review: a reviewer agent is looking at the change. You may end your turn; if it asks for changes, its findings arrive as a [crowsnest review …] message.", job.ID), nil
					}
					return fmt.Sprintf("Job %s is done. You may end your turn.", job.ID), nil
				}), nil, nil
			})
	})
	registerTool("fail", func(server *mcp.Server, client *Client) {
		mcp.AddTool(server, &mcp.Tool{Name: "fail", Title: "Give up on the job", Description: failDescription},
			func(ctx context.Context, _ *mcp.CallToolRequest, args FailArgs) (*mcp.CallToolResult, any, error) {
				return jobCall(client, args.JobID, func(id string) (string, error) {
					job, err := client.FailJob(ctx, id, args.Reason)
					if err != nil {
						return "", err
					}
					return fmt.Sprintf("Job %s is marked failed; the human has been told. You may end your turn.", job.ID), nil
				}), nil, nil
			})
	})
	registerTool("job_land", func(server *mcp.Server, client *Client) {
		mcp.AddTool(server, &mcp.Tool{Name: "job_land", Title: "Ask the human to land a job",
			Description: "Ask the human to land a finished job that ran in a worktree: merge it into its base, push it with a pull request, or discard it. " +
				"This only asks: the human sees the change in the job's Review tab and decides. Nothing is merged, pushed or deleted until they allow it; the answer arrives as a message."},
			func(ctx context.Context, _ *mcp.CallToolRequest, args LandArgs) (*mcp.CallToolResult, any, error) {
				response, err := client.RequestLand(ctx, strings.TrimSpace(args.JobID), strings.TrimSpace(args.Action), args.Reason)
				if err != nil {
					return errorResult(toolError("The request failed", err)), nil, nil
				}
				return textResult(fmt.Sprintf("Asked the human (%s). %s", response.QuestionID, response.Note)), nil, nil
			})
	})
	registerTool("job_create", func(server *mcp.Server, client *Client) {
		mcp.AddTool(server, &mcp.Tool{Name: "job_create", Title: "Delegate a child job", Description: jobCreateDescription},
			func(ctx context.Context, _ *mcp.CallToolRequest, args JobCreateArgs) (*mcp.CallToolResult, any, error) {
				input := jobs.CreateInput{Brief: args.Brief, Role: args.Role, Dir: args.Dir, DoneWhen: args.DoneWhen, Engine: args.Engine, Model: args.Model, Isolate: args.Isolate, To: args.To}
				if args.Project != "" {
					id, err := client.ProjectID(ctx, args.Project)
					if err != nil {
						return errorResult(toolError("The job was not created", err)), nil, nil
					}
					input.ProjectID = id
				}
				job, err := client.CreateJob(ctx, input)
				if err != nil {
					return errorResult(toolError("The job was not created", err)), nil, nil
				}
				if job.Cause == jobs.CauseApproval {
					return textResult(fmt.Sprintf("Created %s (%s), but it waits for the human's approval (%s): it asks for more than your own directory, engine and isolation. "+
						"It starts only if they allow it as asked; otherwise it is cancelled. An allow that carries a note from the human does not start it either: read the note and ask again if it still makes sense. "+
						"The answer arrives as a message starting [crowsnest answer %s]. Carry on with other work meanwhile.",
						job.ID, job.Title, job.CauseDetail, job.CauseDetail)), nil, nil
				}
				return textResult(fmt.Sprintf("Created %s (%s, role %s). It is starting now. Check on it with job_get(job_id=%q).", job.ID, job.Title, job.Role, job.ID)), nil, nil
			})
	})
	registerTool("job_get", func(server *mcp.Server, client *Client) {
		mcp.AddTool(server, &mcp.Tool{Name: "job_get", Title: "Read a job", Description: jobGetDescription, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
			func(ctx context.Context, _ *mcp.CallToolRequest, args JobGetArgs) (*mcp.CallToolResult, any, error) {
				return jobCall(client, args.JobID, func(id string) (string, error) {
					return client.JobContext(ctx, id)
				}), nil, nil
			})
	})
}

// jobCall resolves the job id and turns errors into results the model can act on.
func jobCall(client *Client, explicit string, call func(id string) (string, error)) *mcp.CallToolResult {
	id, err := client.JobID(strings.TrimSpace(explicit))
	if err != nil {
		return errorResult(err.Error())
	}
	text, err := call(id)
	if err != nil {
		return errorResult(toolError("The call failed", err))
	}
	return textResult(text)
}
