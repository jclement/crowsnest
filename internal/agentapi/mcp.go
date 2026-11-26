// `crowsnest mcp`: a stdio MCP server that Claude Code starts (named in the --mcp-config file
// crowsnest writes) and that proxies each tool call to the agent socket. It holds no state of
// its own: every rule lives behind the socket.
//
// Tools live one per file (mcp_ask.go, mcp_say.go, …) and register themselves in init through
// registerTool, so adding one (M7's job_update, done, fail, job_create, job_get) is a new file and
// nothing else. A tool's description is the agent's instruction for it: write it for the model.

package agentapi

import (
	"context"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPServerName is the server's name in the MCP config, so its tools are mcp__crowsnest__<tool>.
const MCPServerName = "crowsnest"

// mcpInstructions is sent to the client at initialisation; Claude Code shows it to the model.
const mcpInstructions = "crowsnest connects you to the human who runs this session, through a web page they " +
	"often read on a phone. Ask them things with `ask` (it does not block unless you pass wait=true) and " +
	"keep working on whatever the answer does not block; answers arrive later as messages starting " +
	"\"[crowsnest answer q_<n>]\". An expired or unanswered question is never consent."

// toolRegistration adds one tool to server; client reaches the socket.
type toolRegistration struct {
	name     string
	register func(server *mcp.Server, client *Client)
}

var toolRegistry []toolRegistration

// registerTool is called from each tool file's init.
func registerTool(name string, register func(server *mcp.Server, client *Client)) {
	toolRegistry = append(toolRegistry, toolRegistration{name: name, register: register})
}

// NewMCPServer builds the MCP server with every registered tool, proxying to client.
func NewMCPServer(client *Client, version string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: MCPServerName, Title: "crowsnest", Version: version},
		&mcp.ServerOptions{Instructions: mcpInstructions})
	registrations := append([]toolRegistration(nil), toolRegistry...)
	sort.Slice(registrations, func(i, j int) bool { return registrations[i].name < registrations[j].name })
	for _, registration := range registrations {
		registration.register(server, client)
	}
	return server
}

// RunMCP serves MCP on stdin and stdout until the client disconnects or ctx ends.
func RunMCP(ctx context.Context, client *Client, version string) error {
	return NewMCPServer(client, version).Run(ctx, &mcp.StdioTransport{})
}

// textResult is a tool result with one text block.
func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// errorResult is a tool result the model sees as a failed call, with a message it can act on.
func errorResult(text string) *mcp.CallToolResult {
	result := textResult(text)
	result.IsError = true
	return result
}
