// The `ask` MCP tool: post a question to the human's inbox (DESIGN.md 6.2 and 8.1). Non-blocking
// by default; wait=true long-polls the socket until the question settles.

package agentapi

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jclement/crowsnest/internal/questions"
)

const askDescription = `Ask the human who runs this crowsnest session a question. It appears in their inbox, often on a phone, rendered as Markdown (fenced code and mermaid diagrams work), with your options as buttons.

NON-BLOCKING BY DEFAULT. The call returns a question id (q_<n>) at once. Do not stop and wait: carry on with everything the answer does not block. The answer arrives later as a message that starts "[crowsnest answer q_<n>]"; it wakes you if you are idle. If nothing is left that the answer does not block, say so and end your turn.

Set wait=true only to approve an irreversible or expensive step (deleting data, pushing, deploying, spending money). The call then blocks until the human answers, the question expires, or it is cancelled.

An expired, cancelled or unanswered question is NEVER consent. Do not take the step it asked about.

Kinds:
- confirm: yes/no (options default to yes and no). recommend is required.
- choice: pick one of 2-12 options, or several with multi=true. recommend is required (an option key).
- ack: the human acknowledges something (one button). No options.
- manual: the human writes a free-text answer. No options.
- notice: information only; nothing comes back. No options.

Write a one-line title that states the decision; put the reasoning, trade-offs and evidence in body_md. Use files to show a proposed change as a diff (filename, original, final; up to 20 files, 256 KB each side). urgency=blocking also notifies the human's phone; use it only when work is truly stopped.`

// AskArgs are the ask tool's arguments: the question, plus wait.
type AskArgs struct {
	Title          string             `json:"title" jsonschema:"one line stating what needs deciding (at most 200 characters)"`
	BodyMD         string             `json:"body_md,omitempty" jsonschema:"Markdown detail: context, trade-offs, evidence. Fenced code and mermaid diagrams render"`
	Kind           string             `json:"kind,omitempty" jsonschema:"confirm, choice, ack, manual or notice. Defaults to choice when options are given, else ack"`
	Multi          bool               `json:"multi,omitempty" jsonschema:"for kind choice: allow several options to be picked"`
	Options        []questions.Option `json:"options,omitempty" jsonschema:"for confirm and choice: the choices, each with a unique key and a label (detail optional)"`
	Recommend      string             `json:"recommend,omitempty" jsonschema:"required for confirm and choice: the key of the option you would pick"`
	Context        map[string]string  `json:"context,omitempty" jsonschema:"short labels shown as chips, such as repo, branch or worker"`
	Urgency        string             `json:"urgency,omitempty" jsonschema:"low, normal (default) or blocking. blocking also notifies the human's phone"`
	ExpiresInHours float64            `json:"expires_in_hours,omitempty" jsonschema:"hours until the question expires unanswered (default 24, at most 168)"`
	Files          []questions.File   `json:"files,omitempty" jsonschema:"files to show as a diff: filename, original content and final content"`
	Wait           bool               `json:"wait,omitempty" jsonschema:"block until the question is answered, expired or cancelled. Default false: return the id at once and keep working"`
	JobID          string             `json:"job_id,omitempty" jsonschema:"the job this question is about (j_<n>); empty in a job's own session. The orchestrator passes the id of the job it holds"`
}

func init() {
	registerTool("ask", func(server *mcp.Server, client *Client) {
		mcp.AddTool(server, &mcp.Tool{
			Name:        "ask",
			Title:       "Ask the human",
			Description: askDescription,
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, OpenWorldHint: boolPtr(false)},
		}, func(ctx context.Context, _ *mcp.CallToolRequest, args AskArgs) (*mcp.CallToolResult, any, error) {
			return askTool(ctx, client, args), nil, nil
		})
	})
}

func askTool(ctx context.Context, client *Client, args AskArgs) *mcp.CallToolResult {
	if args.JobID != "" {
		client = client.WithJob(args.JobID)
	}
	question, err := client.Ask(ctx, questions.AskInput{
		Title: args.Title, BodyMD: args.BodyMD, Kind: questions.Kind(args.Kind), Multi: args.Multi, Options: args.Options,
		Recommend: args.Recommend, Context: args.Context, Urgency: questions.Urgency(args.Urgency),
		ExpiresInHours: args.ExpiresInHours, Files: args.Files, Wait: args.Wait,
	})
	if err != nil {
		return errorResult(toolError("The question was not posted", err))
	}
	if question.Kind == questions.KindNotice {
		return textResult(fmt.Sprintf("Notice %s posted. Nothing comes back from a notice; carry on.", question.ID))
	}
	if !args.Wait {
		text := fmt.Sprintf("Recorded as %s. Keep working on whatever this does not block; the answer will arrive as a message starting \"[crowsnest answer %s]\".",
			question.ID, question.ID)
		if question.JobID != "" {
			text += " If truly nothing is left that it does not block, call job_update with waiting=true and end your turn; the answer will wake you."
		} else {
			text += " If nothing else is left, say so and end your turn; the answer will wake you."
		}
		return textResult(text)
	}
	settled, err := client.WaitSettled(ctx, question.ID)
	if err != nil {
		return errorResult(toolError(fmt.Sprintf("Waiting for %s failed; it may still be answered later, as a message", question.ID), err))
	}
	return textResult(SettledText(settled))
}

// SettledText describes a settled question for an agent: the answer, or that there is none.
func SettledText(q questions.Question) string {
	switch q.Status {
	case questions.StatusExpired:
		return fmt.Sprintf("%s expired with no answer. That is not consent: do not take the step it asked about.", q.ID)
	case questions.StatusCancelled:
		return fmt.Sprintf("%s was cancelled. That is not consent: do not take the step it asked about.", q.ID)
	case questions.StatusOpen:
		return fmt.Sprintf("%s is still open.", q.ID)
	}
	line := strings.TrimPrefix(questions.AnswerLine(q), "[crowsnest answer "+q.ID+"] ")
	return fmt.Sprintf("%s answered: %s", q.ID, line)
}

// toolError words a client error for the model.
func toolError(doing string, err error) string {
	var apiErr *APIError
	var down *ServerDownError
	switch {
	case errors.As(err, &apiErr):
		return doing + ": " + apiErr.Message
	case errors.As(err, &down):
		return doing + ": crowsnest is not running or not reachable. Carry on without it, and mention the question in your reply instead."
	default:
		return doing + ": " + err.Error()
	}
}

func boolPtr(value bool) *bool { return &value }
