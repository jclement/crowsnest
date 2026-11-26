// The `say` MCP tool: a one-sentence progress line spoken aloud in talk mode (M11 speaks it; the
// plumbing exists now). It never blocks and never fails the agent's turn.

package agentapi

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const sayDescription = `Speak one short sentence aloud to the human, when they have talk mode on in the crowsnest web page; otherwise it does nothing. Use it for a progress line worth hearing while they are away from the screen ("Tests are green; opening the pull request now"). Never use it for questions (use ask) or for long text. At most one sentence, 300 characters.`

// SayArgs are the say tool's arguments.
type SayArgs struct {
	Text string `json:"text" jsonschema:"one sentence to speak (at most 300 characters)"`
}

func init() {
	registerTool("say", func(server *mcp.Server, client *Client) {
		mcp.AddTool(server, &mcp.Tool{
			Name:        "say",
			Title:       "Say a sentence aloud",
			Description: sayDescription,
			Annotations: &mcp.ToolAnnotations{OpenWorldHint: boolPtr(false)},
		}, func(ctx context.Context, _ *mcp.CallToolRequest, args SayArgs) (*mcp.CallToolResult, any, error) {
			if err := client.Say(ctx, args.Text); err != nil {
				return errorResult(toolError("Nothing was said", err)), nil, nil
			}
			return textResult("Said."), nil, nil
		})
	})
}
