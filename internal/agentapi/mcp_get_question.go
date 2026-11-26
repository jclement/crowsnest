// The `get_question` MCP tool: read a question you asked, with its whole answer. A typed answer
// line that would be too long for one line says to use it (R1b-C01), and an agent back from a
// compaction can use it to re-read an answer.

package agentapi

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const getQuestionDescription = `Read a question you asked the human (by its id, q_<n>) with the whole answer: the option(s) they chose and their note, which outranks the choice, never shortened. Use it when a "[crowsnest answer q_<n>]" message says the answer is too long for one line, or to re-read an answer after a compaction. It does not wait: an open question comes back as still open.`

// GetQuestionArgs are the get_question tool's arguments.
type GetQuestionArgs struct {
	ID string `json:"id" jsonschema:"the question id, q_<n>"`
}

func init() {
	registerTool("get_question", func(server *mcp.Server, client *Client) {
		mcp.AddTool(server, &mcp.Tool{
			Name:        "get_question",
			Title:       "Read a question and its answer",
			Description: getQuestionDescription,
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)},
		}, func(ctx context.Context, _ *mcp.CallToolRequest, args GetQuestionArgs) (*mcp.CallToolResult, any, error) {
			question, err := client.Get(ctx, args.ID, 0)
			if err != nil {
				return errorResult(toolError(fmt.Sprintf("Reading %s failed", args.ID), err)), nil, nil
			}
			return answerResult(question), nil, nil
		})
	})
}
