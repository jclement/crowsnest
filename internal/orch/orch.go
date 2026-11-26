// Package orch keeps the session's orchestrator, a Claude Code agent named "orchestrator",
// alive in the herdr session (DESIGN.md section 7): the idempotent start-up sequence (adopt a live
// one, else find or create its workspace and start Claude), the watch that restarts it with
// --resume when its pane is back at a shell, the Restart and New conversation operations, and the
// send path the web app uses to prompt any agent (send.go).
//
// Every change of state is published on the hub as an `orchestrator` event carrying Status, so
// the Chat view's status chip follows it without polling.
package orch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/hub"
)

// Name is the herdr agent name of the orchestrator; the API also accepts it as a target.
const Name = "orchestrator"

// WorkspaceLabel is the label of the herdr workspace crowsnest creates for the orchestrator.
const WorkspaceLabel = "orchestrator"

// KV keys under which the orchestrator's identity survives a crowsnest restart.
const (
	kvSessionID = "orchestrator.session_id" // Claude session id, for --resume
	kvPaneID    = "orchestrator.pane_id"    // the pane it last ran in
)

const (
	// DefaultWatchInterval is how often the watch checks the orchestrator is still there.
	DefaultWatchInterval = 2 * time.Second
	// startTimeoutMS is agent.start's own timeout: long enough for Claude to boot on a slow
	// machine (verified ~3.5 s on quark), within herdr's 3001..300000 range.
	startTimeoutMS = 60_000
	// maxRestartsPerHour caps automatic restarts (DESIGN.md 6.4); past it the orchestrator is
	// marked failed and waits for a manual restart, rather than looping on a crash.
	maxRestartsPerHour = 3
	// herdrCallTimeout bounds each ordinary herdr call.
	herdrCallTimeout = 5 * time.Second
	// DefaultLaunchTimeout is how long a started Claude gets to become interactive-ready.
	DefaultLaunchTimeout = 60 * time.Second
	// launchPollInterval is how often a launching agent is checked.
	launchPollInterval = 250 * time.Millisecond
)

// State is the orchestrator's lifecycle state as the UI shows it.
type State string

// Orchestrator states. Idle, working and blocked follow herdr's agent state; the others are
// crowsnest's own.
const (
	StateDisabled      State = "disabled"       // --no-orchestrator
	StateStarting      State = "starting"       // Ensure is running
	StateIdle          State = "idle"           // ready for input (herdr idle or done)
	StateWorking       State = "working"        // a turn is running
	StateBlocked       State = "blocked"        // a permission or question dialog is up
	StateNeedsTerminal State = "needs_terminal" // stopped at a start-up prompt (trust, login)
	StateRestarting    State = "restarting"     // Restart or New conversation in progress
	StateFailed        State = "failed"         // could not start, or too many restarts
)

// Status is what GET /orchestrator returns and an `orchestrator` event carries.
type Status struct {
	State     State     `json:"state"`
	Name      string    `json:"name"`
	PaneID    string    `json:"pane_id"`    // empty until it has a pane
	SessionID string    `json:"session_id"` // Claude's session id, when known
	Dir       string    `json:"dir"`
	Detail    string    `json:"detail,omitempty"` // a sentence for the human when something is wrong
	Restarts  int       `json:"restarts"`         // automatic restarts in the last hour
	UpdatedAt time.Time `json:"updated_at"`
}

// KV is the small persistent store the orchestrator keeps its session id in (store.Store).
type KV interface {
	GetKV(ctx context.Context, key string) (string, error)
	SetKV(ctx context.Context, key, value string) error
	DeleteKV(ctx context.Context, key string) error
}

// Options configure an Orchestrator. Client, KV, Dir and StateDir are required.
type Options struct {
	Client         herdr.Client
	KV             KV
	Hub            *hub.Hub // nil: no events (tests)
	Logger         *slog.Logger
	Session        string // herdr session name, for the pane env and the instructions
	Dir            string // working directory (--dir), created if missing
	StateDir       string // where CROWSNEST.md and claude-settings.json are written
	Model          string // --model, when set
	PermissionMode string // --permission-mode
	Version        string // crowsnest version, written into CROWSNEST.md
	// BinDir is prepended to PATH in the pane env so `crowsnest ask` resolves; empty skips it.
	BinDir string
	// Executable is the absolute path of the crowsnest binary, named in mcp.json as the command
	// of the `crowsnest mcp` server. Empty: no MCP config is written or passed.
	Executable string
	// AgentSocket is the agent API's unix socket, exported to the pane as CROWSNEST_SOCKET and
	// to the MCP server through mcp.json. Empty: neither is set.
	AgentSocket string
	// BasePath is the PATH that BinDir is prepended to (normally os.Getenv("PATH")).
	BasePath string
	// ClaudeConfigDir is where transcripts live, to decide between --resume and a new session.
	ClaudeConfigDir string
	Disabled        bool          // --no-orchestrator: report disabled, start nothing
	WatchInterval   time.Duration // DefaultWatchInterval when zero
	// ShellWait is how long to wait for a pane to show a bare shell (after creating it, or
	// after /exit). Defaults to 15 s.
	ShellWait time.Duration
	// LaunchTimeout is how long a started Claude gets to become ready before the orchestrator is
	// reported as needing the terminal. DefaultLaunchTimeout when zero.
	LaunchTimeout time.Duration
	// OnStatus, when set, is called after every status change (serve uses it to pin the
	// orchestrator's conversation). It must not block.
	OnStatus func(Status)
	Now      func() time.Time
}

// Orchestrator owns the orchestrator agent's lifecycle.
type Orchestrator struct {
	opts Options

	// lifecycle serialises Ensure, Restart and New; the watch skips a tick while one runs.
	lifecycle sync.Mutex

	mu       sync.Mutex
	status   Status
	restarts []time.Time // automatic restarts, for the hourly budget
	// launch remembers what crowsnest started Claude with, so a stale session id from herdr
	// cannot replace it (R1-C02). knownSession is the last non-empty Status.SessionID, kept
	// across New conversation (which clears the status) so it can be recognised as stale.
	launch       launchBaseline
	knownSession string
}

// launchBaseline is the session id Claude was started with and the ids herdr may go on
// reporting for a while afterwards: the previous conversation's, and whatever herdr reported
// when the launch finished. While herdr reports one of those stale ids, the launched id wins;
// the first time herdr reports anything else (it caught up, or a /clear really moved the
// conversation) the baseline is dropped and herdr is followed again.
type launchBaseline struct {
	session string
	stale   []string
}

func newLaunchBaseline(session string, candidates ...string) launchBaseline {
	baseline := launchBaseline{session: session}
	for _, candidate := range candidates {
		if candidate != "" && candidate != session && !slices.Contains(baseline.stale, candidate) {
			baseline.stale = append(baseline.stale, candidate)
		}
	}
	return baseline
}

// New returns an orchestrator in the starting (or disabled) state. Nothing touches herdr until
// Ensure or Run.
func New(opts Options) *Orchestrator {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.WatchInterval <= 0 {
		opts.WatchInterval = DefaultWatchInterval
	}
	if opts.ShellWait <= 0 {
		opts.ShellWait = 15 * time.Second
	}
	if opts.LaunchTimeout <= 0 {
		opts.LaunchTimeout = DefaultLaunchTimeout
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	state := StateStarting
	if opts.Disabled {
		state = StateDisabled
	}
	return &Orchestrator{opts: opts, status: Status{State: state, Name: Name, Dir: opts.Dir, UpdatedAt: opts.Now()}}
}

// Status returns the current status.
func (o *Orchestrator) Status() Status {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.status
}

// Run ensures the orchestrator at once and then watches it until ctx ends.
func (o *Orchestrator) Run(ctx context.Context) {
	if o.opts.Disabled {
		return
	}
	if _, err := o.Ensure(ctx); err != nil {
		o.opts.Logger.Warn("orchestrator did not start", "err", err)
	}
	ticker := time.NewTicker(o.opts.WatchInterval)
	defer ticker.Stop()
	fleetChanged := o.fleetEvents(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			o.Watch(ctx)
		case <-fleetChanged:
			o.Watch(ctx) // herdr reported a change; follow it now rather than at the next tick
		}
	}
}

// fleetEvents returns a channel that receives whenever the fleet cache publishes a change, so
// the status follows herdr's own state-change events. Nil (never ready) without a hub. A
// subscription the hub closes (it fell behind) is simply renewed.
func (o *Orchestrator) fleetEvents(ctx context.Context) <-chan struct{} {
	if o.opts.Hub == nil {
		return nil
	}
	changed := make(chan struct{}, 1)
	go func() {
		for ctx.Err() == nil {
			sub := o.opts.Hub.Subscribe(0)
			forwardFleet(ctx, sub, changed)
			sub.Close()
			time.Sleep(100 * time.Millisecond) // a closed hub must not spin this loop
		}
	}()
	return changed
}

func forwardFleet(ctx context.Context, sub *hub.Subscription, changed chan<- struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-sub.Events():
			if !ok {
				return
			}
			if event.Type != "fleet" {
				continue
			}
			select {
			case changed <- struct{}{}:
			default: // a check is already pending
			}
		}
	}
}

// ---- Start-up --------------------------------------------------------------------------

// Ensure runs the start-up sequence (DESIGN.md 7): adopt a live agent named orchestrator, or else
// find or create its pane and start Claude there, resuming the stored conversation when its
// transcript exists. It is idempotent: with a live orchestrator it only refreshes the status.
func (o *Orchestrator) Ensure(ctx context.Context) (Status, error) {
	if o.opts.Disabled {
		return o.Status(), nil
	}
	o.lifecycle.Lock()
	defer o.lifecycle.Unlock()
	return o.ensureLocked(ctx, true)
}

// ensureLocked is Ensure with the lifecycle lock held. resume chooses --resume over a fresh
// session when a stored conversation exists.
func (o *Orchestrator) ensureLocked(ctx context.Context, resume bool) (Status, error) {
	agent, alive, err := o.liveAgent(ctx)
	if err != nil {
		return o.fail(fmt.Errorf("looking for the orchestrator agent: %w", err))
	}
	if alive {
		return o.adopt(ctx, agent), nil
	}
	o.update(func(s *Status) { s.State, s.Detail = StateStarting, "" })
	paneID, err := o.findShellPane(ctx)
	if err != nil {
		return o.fail(err)
	}
	return o.start(ctx, paneID, resume)
}

// adopt takes over a live agent: its pane and session id become the orchestrator's.
func (o *Orchestrator) adopt(ctx context.Context, agent herdr.Agent) Status {
	o.remember(ctx, agent.PaneID, sessionOf(agent, ""))
	return o.follow(agent)
}

// follow maps herdr's view of the agent onto the status.
func (o *Orchestrator) follow(agent herdr.Agent) Status {
	return o.update(func(s *Status) { o.applyAgentLocked(s, agent) })
}

// followLaunch is follow for an agent crowsnest has just started with sessionID: that id is
// published and becomes the baseline herdr's possibly stale report is judged against. prior is
// the session id known before the launch.
func (o *Orchestrator) followLaunch(agent herdr.Agent, sessionID, prior string) Status {
	return o.update(func(s *Status) {
		o.launch = newLaunchBaseline(sessionID, prior, sessionOf(agent, ""))
		s.SessionID = sessionID
		o.applyAgentLocked(s, agent)
	})
}

// applyAgentLocked copies herdr's view of the agent into s; called with o.mu held.
func (o *Orchestrator) applyAgentLocked(s *Status, agent herdr.Agent) {
	s.PaneID = agent.PaneID
	o.adoptSessionLocked(s, agent)
	s.State, s.Detail = stateOf(agent, s.State), ""
	if s.State == StateNeedsTerminal {
		s.Detail = needsTerminalDetail
	}
}

// adoptSessionLocked takes herdr's session id for the agent unless it is stale: while Claude is
// launching herdr may still report the previous session, and after a launch it can keep doing
// so for a while (DESIGN.md 7.6). Called with o.mu held.
func (o *Orchestrator) adoptSessionLocked(s *Status, agent herdr.Agent) {
	reported := sessionOf(agent, "")
	if reported == "" || agent.LaunchPending {
		return
	}
	if o.launch.session != "" {
		if slices.Contains(o.launch.stale, reported) {
			return
		}
		o.launch = launchBaseline{}
	}
	s.SessionID = reported
}

const needsTerminalDetail = "Claude stopped at a start-up prompt (folder trust or login). Open the orchestrator's terminal and answer it."

// stateOf maps an agent to a state. An agent that is not interactive-ready and is blocked is
// still at its start-up dialog; while it stays there the state stays needs_terminal.
func stateOf(agent herdr.Agent, previous State) State {
	switch {
	case !agent.InteractiveReady && (agent.AgentStatus == herdr.StatusBlocked || previous == StateNeedsTerminal):
		return StateNeedsTerminal
	case agent.LaunchPending:
		return StateStarting
	case agent.AgentStatus == herdr.StatusWorking:
		return StateWorking
	case agent.AgentStatus == herdr.StatusBlocked:
		return StateBlocked
	case agent.AgentStatus.Ready():
		return StateIdle
	}
	if previous == StateNeedsTerminal {
		return previous
	}
	return StateIdle // unknown: herdr cannot classify the screen; treat as present and ready
}

func sessionOf(agent herdr.Agent, fallback string) string {
	if agent.AgentSession != nil && agent.AgentSession.Value != "" {
		return agent.AgentSession.Value
	}
	return fallback
}

// remember stores the pane and session id so a restarted crowsnest resumes the same
// conversation in the same pane. Failures are logged: losing them costs a fresh session, not
// correctness.
func (o *Orchestrator) remember(ctx context.Context, paneID, sessionID string) {
	if paneID != "" {
		if err := o.opts.KV.SetKV(ctx, kvPaneID, paneID); err != nil {
			o.opts.Logger.Warn("storing the orchestrator pane", "err", err)
		}
	}
	if sessionID != "" {
		if err := o.opts.KV.SetKV(ctx, kvSessionID, sessionID); err != nil {
			o.opts.Logger.Warn("storing the orchestrator session id", "err", err)
		}
	}
}

// ---- Status bookkeeping ----------------------------------------------------------------

// update applies change to the status and publishes it when anything but the timestamp moved.
func (o *Orchestrator) update(change func(*Status)) Status {
	o.mu.Lock()
	before := o.status
	change(&o.status)
	if o.status.SessionID != "" {
		o.knownSession = o.status.SessionID
	}
	o.status.Restarts = o.restartsInLastHourLocked()
	after := o.status
	after.UpdatedAt = before.UpdatedAt
	moved := after != before
	if moved {
		o.status.UpdatedAt = o.opts.Now()
	}
	current := o.status
	o.mu.Unlock()
	if moved {
		o.publish(current)
	}
	return current
}

func (o *Orchestrator) publish(status Status) {
	if o.opts.Hub != nil {
		o.opts.Hub.Publish(hub.Event{Type: "orchestrator", Data: status})
	}
	if o.opts.OnStatus != nil {
		o.opts.OnStatus(status)
	}
}

// fail records a failure and returns it. The status (which GET /orchestrator and the
// `orchestrator` event carry to the browser) gets a sentence chosen by the kind of failure;
// the error itself, which can hold herdr's messages and file paths, goes only to the log
// (R1 I2).
func (o *Orchestrator) fail(err error) (Status, error) {
	o.opts.Logger.Warn("orchestrator failed", "err", err)
	status := o.update(func(s *Status) { s.State, s.Detail = StateFailed, failureDetail(err) })
	return status, err
}

// failureDetail says what went wrong in words for the person, without the error's own text.
func failureDetail(err error) string {
	var herdrErr *herdr.Error
	switch {
	case errors.Is(err, errNoShell):
		return "No pane at a shell prompt could be found or made for Claude. Open the session's terminal to see why, then use Restart."
	case errors.As(err, &herdrErr):
		return fmt.Sprintf("herdr refused to start Claude (%s). Open the session's terminal to see why, then use Restart; the server log has the detail.", herdrErr.Code)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, herdr.ErrNoResponse):
		return "herdr is not answering; check that the session's server is running, then use Restart."
	default:
		return "Claude could not be started. The server log has the detail; use Restart to try again."
	}
}

func (o *Orchestrator) restartsInLastHourLocked() int {
	cutoff := o.opts.Now().Add(-time.Hour)
	kept := o.restarts[:0]
	for _, at := range o.restarts {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	o.restarts = kept
	return len(kept)
}

// liveAgent finds the orchestrator and says whether Claude is really running. herdr can keep
// answering agent.get for a named agent after Claude has exited (seen in M4 right after /exit),
// so an agent whose pane shows only a bare shell, and is not mid-launch, counts as gone.
func (o *Orchestrator) liveAgent(ctx context.Context) (herdr.Agent, bool, error) {
	agent, err := o.getAgent(ctx)
	if herdr.IsNotFound(err) {
		return herdr.Agent{}, false, nil
	}
	if err != nil {
		return herdr.Agent{}, false, err
	}
	if !agent.LaunchPending && o.atShell(ctx, agent.PaneID) {
		return agent, false, nil
	}
	return agent, true, nil
}

// getAgent is AgentGet for the orchestrator with the ordinary call timeout.
func (o *Orchestrator) getAgent(ctx context.Context) (herdr.Agent, error) {
	ctx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
	defer cancel()
	return o.opts.Client.AgentGet(ctx, Name)
}

// errNoShell means no pane at a bare shell could be found or made in time.
var errNoShell = errors.New("no pane at a bare shell prompt")
