// The Client interface: everything crowsnest asks of herdr. Two implementations satisfy it,
// SocketClient (real) and fake.Fake (in memory). Keep this the only way the rest of crowsnest
// reaches herdr, so --demo and every unit test exercise the same code paths as production.

package herdr

import "context"

// Client is a connection to one herdr session.
//
// Targets: agent methods take an agent target, which herdr resolves as a live agent name or the
// pane id hosting that agent. Pane methods take a pane id ("w1:p2"). Ids are opaque.
type Client interface {
	// Ping checks the server is alive and reports its version and protocol.
	Ping(ctx context.Context) (Pong, error)
	// Snapshot returns the whole session: workspaces, tabs, panes and agents.
	Snapshot(ctx context.Context) (Snapshot, error)

	WorkspaceCreate(ctx context.Context, params WorkspaceCreateParams) (WorkspaceCreated, error)
	WorkspaceList(ctx context.Context) ([]Workspace, error)
	// TabCreate opens a tab; TabList lists the tabs of one workspace, or all when id is empty.
	TabCreate(ctx context.Context, params TabCreateParams) (TabCreated, error)
	TabList(ctx context.Context, workspaceID string) ([]Tab, error)
	// WorktreeCreate makes a git worktree and opens a workspace on it (M7, the job launcher).
	WorktreeCreate(ctx context.Context, params WorktreeCreateParams) (WorktreeCreated, error)

	// PaneList lists the panes of one workspace, or all when id is empty.
	PaneList(ctx context.Context, workspaceID string) ([]Pane, error)
	PaneGet(ctx context.Context, paneID string) (Pane, error)
	PaneRead(ctx context.Context, paneID string, opts ReadOptions) (ReadResult, error)
	PaneProcessInfo(ctx context.Context, paneID string) (ProcessInfo, error)
	// PaneRun types command and presses Enter, like `herdr pane run`.
	PaneRun(ctx context.Context, paneID, command string) error
	// PaneSendKeys sends key presses ("esc", "ctrl+c", "1", "Enter"); herdr validates every key
	// before writing any.
	PaneSendKeys(ctx context.Context, paneID string, keys []string) error

	// AgentStart starts an agent in a pane at a bare shell prompt. ErrAgentNotReady means it
	// stopped at a start-up dialog; the name stays usable for reads and keys.
	AgentStart(ctx context.Context, params AgentStartParams) (Agent, error)
	AgentGet(ctx context.Context, target string) (Agent, error)
	AgentList(ctx context.Context) ([]Agent, error)
	// AgentPrompt submits text and Enter. ErrAgentBlocked: a dialog is up and nothing was sent.
	// With wait, ErrAgentPromptStalled: no turn started; ErrTimeout: wait expired.
	AgentPrompt(ctx context.Context, target, text string, wait *PromptWait) (Agent, error)
	AgentRead(ctx context.Context, target string, opts ReadOptions) (ReadResult, error)
	AgentSendKeys(ctx context.Context, target string, keys []string) error
	// AgentWait blocks until the agent reaches one of until (idle, done or blocked when empty).
	// timeoutMS 0 waits as long as ctx allows.
	AgentWait(ctx context.Context, target string, until []AgentStatus, timeoutMS int) (Agent, error)
	// AgentRename names the agent; an empty name clears it.
	AgentRename(ctx context.Context, target, name string) (Agent, error)

	// Subscribe opens an event stream. The stream lives until Close, ctx ends, the connection
	// drops, or herdr reports events_lost (then Err returns ErrEventsLost).
	Subscribe(ctx context.Context, subscriptions []Subscription) (EventStream, error)
}
