// Lazy is a Client that may not be connected yet (DESIGN.md 3.4, "starts without herdr").
//
// serve used to refuse to start when herdr was missing. Now it hands every service a Lazy: until
// a background connector reaches the session (installing herdr later is enough, no restart),
// every call answers ErrUnavailable, which the services already treat like an outage (the fleet
// shows "herdr is not answering", the orchestrator waits at its gate). Once connected, calls go
// straight to the socket client.

package herdr

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// CodeUnavailable is the code of ErrUnavailable.
const CodeUnavailable = "herdr_unavailable"

// ErrUnavailable is every Lazy call's answer before herdr is connected.
var ErrUnavailable = &Error{Code: CodeUnavailable, Message: "herdr is not connected yet"}

// connectRetry is how often the connector tries again while herdr is missing or will not start.
const connectRetry = 5 * time.Second

// Lazy forwards to a Client once one is connected.
type Lazy struct {
	client atomic.Pointer[clientBox]

	mu      sync.Mutex
	session *Session
	lastErr string
	waiters []chan struct{}
}

// clientBox lets an interface value sit in an atomic.Pointer.
type clientBox struct{ Client }

// NewLazy returns an unconnected Lazy.
func NewLazy() *Lazy { return &Lazy{} }

// Set connects l to client (tests, and --demo's fake).
func (l *Lazy) Set(client Client) {
	l.client.Store(&clientBox{client})
	l.mu.Lock()
	waiters := l.waiters
	l.waiters = nil
	l.mu.Unlock()
	for _, waiter := range waiters {
		close(waiter)
	}
}

// Connected reports whether a client is set.
func (l *Lazy) Connected() bool { return l.client.Load() != nil }

// Session is the connected session (nil before, and for a client given with Set).
func (l *Lazy) Session() *Session {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.session
}

// LastError is why the last connection attempt failed ("" once connected or before trying).
func (l *Lazy) LastError() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastErr
}

// Ready returns a channel closed once connected.
func (l *Lazy) Ready() <-chan struct{} {
	ready := make(chan struct{})
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.client.Load() != nil {
		close(ready)
		return ready
	}
	l.waiters = append(l.waiters, ready)
	return ready
}

// Connect tries EnsureSession until it succeeds or ctx ends: at once, then every few seconds.
// While herdr is not on PATH it only looks for the binary (cheap); each failure is logged once
// per distinct reason. onConnect runs once, after the client is set.
func (l *Lazy) Connect(ctx context.Context, opts SessionOptions, onConnect func(*Session)) {
	opts = opts.withDefaults()
	ticker := time.NewTicker(connectRetry)
	defer ticker.Stop()
	for {
		if _, err := exec.LookPath(opts.Binary); err != nil {
			l.failed(opts.Logger, errors.New("herdr is not installed (no herdr on PATH); crowsnest will connect when it appears"))
		} else if session, err := EnsureSession(ctx, opts); err != nil {
			l.failed(opts.Logger, err)
		} else {
			l.mu.Lock()
			l.session, l.lastErr = session, ""
			l.mu.Unlock()
			l.Set(session.Client)
			if onConnect != nil {
				onConnect(session)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (l *Lazy) failed(logger *slog.Logger, err error) {
	l.mu.Lock()
	changed := l.lastErr != err.Error()
	l.lastErr = err.Error()
	l.mu.Unlock()
	if changed {
		logger.Warn("herdr is not available yet; retrying", "err", err)
	}
}

func (l *Lazy) get() (Client, error) {
	box := l.client.Load()
	if box == nil {
		return nil, ErrUnavailable
	}
	return box.Client, nil
}

// ---- Client --------------------------------------------------------------------

func (l *Lazy) Ping(ctx context.Context) (Pong, error) {
	c, err := l.get()
	if err != nil {
		return Pong{}, err
	}
	return c.Ping(ctx)
}

func (l *Lazy) Snapshot(ctx context.Context) (Snapshot, error) {
	c, err := l.get()
	if err != nil {
		return Snapshot{}, err
	}
	return c.Snapshot(ctx)
}

func (l *Lazy) WorkspaceCreate(ctx context.Context, params WorkspaceCreateParams) (WorkspaceCreated, error) {
	c, err := l.get()
	if err != nil {
		return WorkspaceCreated{}, err
	}
	return c.WorkspaceCreate(ctx, params)
}

func (l *Lazy) WorkspaceList(ctx context.Context) ([]Workspace, error) {
	c, err := l.get()
	if err != nil {
		return nil, err
	}
	return c.WorkspaceList(ctx)
}

func (l *Lazy) TabCreate(ctx context.Context, params TabCreateParams) (TabCreated, error) {
	c, err := l.get()
	if err != nil {
		return TabCreated{}, err
	}
	return c.TabCreate(ctx, params)
}

func (l *Lazy) TabList(ctx context.Context, workspaceID string) ([]Tab, error) {
	c, err := l.get()
	if err != nil {
		return nil, err
	}
	return c.TabList(ctx, workspaceID)
}

func (l *Lazy) WorktreeCreate(ctx context.Context, params WorktreeCreateParams) (WorktreeCreated, error) {
	c, err := l.get()
	if err != nil {
		return WorktreeCreated{}, err
	}
	return c.WorktreeCreate(ctx, params)
}

func (l *Lazy) PaneList(ctx context.Context, workspaceID string) ([]Pane, error) {
	c, err := l.get()
	if err != nil {
		return nil, err
	}
	return c.PaneList(ctx, workspaceID)
}

func (l *Lazy) PaneGet(ctx context.Context, paneID string) (Pane, error) {
	c, err := l.get()
	if err != nil {
		return Pane{}, err
	}
	return c.PaneGet(ctx, paneID)
}

func (l *Lazy) PaneRead(ctx context.Context, paneID string, opts ReadOptions) (ReadResult, error) {
	c, err := l.get()
	if err != nil {
		return ReadResult{}, err
	}
	return c.PaneRead(ctx, paneID, opts)
}

func (l *Lazy) PaneProcessInfo(ctx context.Context, paneID string) (ProcessInfo, error) {
	c, err := l.get()
	if err != nil {
		return ProcessInfo{}, err
	}
	return c.PaneProcessInfo(ctx, paneID)
}

func (l *Lazy) PaneRun(ctx context.Context, paneID, command string) error {
	c, err := l.get()
	if err != nil {
		return err
	}
	return c.PaneRun(ctx, paneID, command)
}

func (l *Lazy) PaneSendKeys(ctx context.Context, paneID string, keys []string) error {
	c, err := l.get()
	if err != nil {
		return err
	}
	return c.PaneSendKeys(ctx, paneID, keys)
}

func (l *Lazy) AgentStart(ctx context.Context, params AgentStartParams) (Agent, error) {
	c, err := l.get()
	if err != nil {
		return Agent{}, err
	}
	return c.AgentStart(ctx, params)
}

func (l *Lazy) AgentGet(ctx context.Context, target string) (Agent, error) {
	c, err := l.get()
	if err != nil {
		return Agent{}, err
	}
	return c.AgentGet(ctx, target)
}

func (l *Lazy) AgentList(ctx context.Context) ([]Agent, error) {
	c, err := l.get()
	if err != nil {
		return nil, err
	}
	return c.AgentList(ctx)
}

func (l *Lazy) AgentPrompt(ctx context.Context, target, text string, wait *PromptWait) (Agent, error) {
	c, err := l.get()
	if err != nil {
		return Agent{}, err
	}
	return c.AgentPrompt(ctx, target, text, wait)
}

func (l *Lazy) AgentRead(ctx context.Context, target string, opts ReadOptions) (ReadResult, error) {
	c, err := l.get()
	if err != nil {
		return ReadResult{}, err
	}
	return c.AgentRead(ctx, target, opts)
}

func (l *Lazy) AgentSendKeys(ctx context.Context, target string, keys []string) error {
	c, err := l.get()
	if err != nil {
		return err
	}
	return c.AgentSendKeys(ctx, target, keys)
}

func (l *Lazy) AgentWait(ctx context.Context, target string, until []AgentStatus, timeoutMS int) (Agent, error) {
	c, err := l.get()
	if err != nil {
		return Agent{}, err
	}
	return c.AgentWait(ctx, target, until, timeoutMS)
}

func (l *Lazy) AgentRename(ctx context.Context, target, name string) (Agent, error) {
	c, err := l.get()
	if err != nil {
		return Agent{}, err
	}
	return c.AgentRename(ctx, target, name)
}

func (l *Lazy) Subscribe(ctx context.Context, subscriptions []Subscription) (EventStream, error) {
	c, err := l.get()
	if err != nil {
		return nil, err
	}
	return c.Subscribe(ctx, subscriptions)
}
