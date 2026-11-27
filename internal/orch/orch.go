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
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/guard"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/journal"
)

// Name is the herdr agent name of the orchestrator; the API also accepts it as a target.
const Name = "orchestrator"

// WorkspaceLabel is the label of the herdr workspace crowsnest creates for the orchestrator.
const WorkspaceLabel = "orchestrator"

// KV key suffixes under which a standing agent's identity survives a crowsnest restart
// ("orchestrator.session_id" for the orchestrator, "lead.<slug>.session_id" for a lead).
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
	State     State  `json:"state"`
	Name      string `json:"name"`
	PaneID    string `json:"pane_id"`    // empty until it has a pane
	SessionID string `json:"session_id"` // Claude's session id, when known
	Dir       string `json:"dir"`
	// Model is the model Claude was started with (--model); empty means Claude's own default.
	Model string `json:"model"`
	// Engine is the agent it runs on: claude or codex.
	Engine string `json:"engine,omitempty"`
	Detail string `json:"detail,omitempty"` // a sentence for the human when something is wrong
	// RemoteControl is the Remote Control session name Claude was started with ("" when off,
	// --no-remote-control). Claude turns Remote Control off by itself when the login cannot use
	// it (an API key, a plan without it, an organisation policy) and the session starts anyway;
	// RemoteControlNote says so when crowsnest saw it.
	RemoteControl     string `json:"remote_control,omitempty"`
	RemoteControlNote string `json:"remote_control_note,omitempty"`
	// RemoteControlURL is the session's https://claude.ai/code/session_… link, once Claude has
	// shown it (remote_control.go); "" until then, after every start, and when it is off.
	RemoteControlURL string `json:"remote_control_url,omitempty"`
	// InstructionsStale: what crowsnest would start it with now (prompt file, hooks, settings,
	// MCP config) differs from what its running Claude was started with (freshness.go).
	InstructionsStale bool      `json:"instructions_stale,omitempty"`
	Restarts          int       `json:"restarts"` // automatic restarts in the last hour
	UpdatedAt         time.Time `json:"updated_at"`
}

// KV is the small persistent store the orchestrator keeps its session id in (store.Store).
type KV interface {
	GetKV(ctx context.Context, key string) (string, error)
	SetKV(ctx context.Context, key, value string) error
	DeleteKV(ctx context.Context, key string) error
}

// Options configure an Orchestrator. Client, KV, Dir and StateDir are required.
type Options struct {
	Client herdr.Client
	KV     KV
	Hub    *hub.Hub // nil: no events (tests)
	// Journal records restarts (automatic and manual); nil records nothing.
	Journal  *journal.Journal
	Logger   *slog.Logger
	Session  string // herdr session name, for the pane env and the instructions
	Dir      string // working directory (--dir), created if missing
	StateDir string // where CROWSNEST.md and claude-settings.json are written
	Model    string // --model, when set
	// RemoteControlName, when set, starts Claude with `--remote-control <name>` (DESIGN.md 7.10).
	RemoteControlName string
	PermissionMode    string // --permission-mode
	Version           string // crowsnest version, written into CROWSNEST.md
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
	// Identity mints the orchestrator's agent tokens (CROWSNEST_TOKEN in its pane and in its
	// MCP server's environment), which is how the agent socket knows a question came from the
	// orchestrator's pane (R1b N2). nil: no tokens, so the orchestrator asks unidentified.
	Identity *agentid.Registry
	// ClaudeConfigDir is where transcripts live, to decide between --resume and a new session.
	ClaudeConfigDir string
	// Guard, when set, is written to <state>/guard.json and named on the PreToolUse guard hook
	// (DESIGN.md 8.7). nil: no guard hook.
	Guard         *guard.Rules
	Disabled      bool          // --no-orchestrator: report disabled, start nothing
	WatchInterval time.Duration // DefaultWatchInterval when zero
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
	// Gate, when set, is asked before the orchestrator is started or restarted automatically:
	// "" means go, anything else is the sentence shown while it waits (gate.go).
	Gate func(ctx context.Context) string
	// Launch, when set, gives the launch settings at each start (engine, model, permission mode,
	// Remote Control), so a change in the app's Settings applies on the next start.
	Launch func() LaunchSettings
	// Paused reports whether the human paused crowsnest's automation (DESIGN.md 6.13): an agent
	// that exits meanwhile is not restarted automatically (Restart still works). Nil: never.
	Paused func() bool
	// DemoRemoteControl, under --demo, is a made-up Remote Control name and session URL the
	// status shows after every start, so the UI has a link to draw; Claude is never started with
	// Remote Control for it (DESIGN.md 7.10). Nil: none.
	DemoRemoteControl *DemoRemoteControl
	// ExtraEnv is added to the agent's pane environment (CROWSNEST_HEAVY_DIR, DESIGN.md 6.12).
	ExtraEnv map[string]string

	// ---- A standing agent other than the session's orchestrator (DESIGN.md 21.2) ----
	// The lifecycle is the same for every standing agent: the orchestrator, and each project's
	// lead. These name the agent; zero values mean the orchestrator's.

	// AgentName is the herdr agent name (Name when empty), e.g. "lead-alpha".
	AgentName string
	// Workspace is the label of the herdr workspace it runs in (WorkspaceLabel when empty).
	Workspace string
	// KVPrefix prefixes the kv keys of its session id and pane ("orchestrator" when empty).
	KVPrefix string
	// Render writes the instructions file appended to its system prompt; nil renders the
	// orchestrator's CROWSNEST.md.
	Render func() (string, error)
	// OnStart, when set, is called after the start-up files are written for a start (the
	// instructions service records what the agent was started with). It must not block long.
	OnStart func(ctx context.Context)
	// Guidance renders what Render would write now, without side effects (nothing recorded),
	// for the comparison with what the agent was started with (freshness.go). Nil: Render.
	Guidance func() (string, error)
	// AutoInstructionRestart reports whether an agent whose instructions changed is restarted by
	// itself at its next idle moment (the Setting); nil: yes.
	AutoInstructionRestart func() bool
	// HeldJobBusy reports a step crowsnest is taking on a job the agent holds; such an agent is
	// not restarted for new instructions until it is over. Nil: never.
	HeldJobBusy func(ctx context.Context) bool
	// AdoptAsCurrent counts an adopted agent with no recorded start as started with the current
	// instructions (--demo, whose fleet is made up: nothing there was started with older ones).
	AdoptAsCurrent bool
}

// agentName is the herdr name of the agent this Orchestrator keeps.
func (o *Orchestrator) agentName() string {
	if o.opts.AgentName != "" {
		return o.opts.AgentName
	}
	return Name
}

func (o *Orchestrator) workspaceLabel() string {
	if o.opts.Workspace != "" {
		return o.opts.Workspace
	}
	return WorkspaceLabel
}

// kvKey is the orchestrator's key, or the same key under the standing agent's KVPrefix.
func (o *Orchestrator) kvKey(key string) string {
	if o.opts.KVPrefix == "" {
		return key
	}
	return o.opts.KVPrefix + strings.TrimPrefix(key, "orchestrator")
}

// AgentName is the herdr name of the standing agent this keeps ("orchestrator" or "lead-<slug>").
func (o *Orchestrator) AgentName() string { return o.agentName() }

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
	// rcLook schedules the watch's looks for Remote Control's URL (remote_control.go).
	rcLook rcLook
	// kick wakes a waiting orchestrator to ask its gate again (Kick).
	kick chan struct{}
	// launching is the pane a start is launching Claude in, before the status names it: hooks
	// that fire during the launch (SessionStart, InstructionsLoaded) are matched to it.
	launching string
	// instructionsAt is the last comparison of its instructions, instructionsRestartAt the last
	// restart for new ones (freshness.go).
	instructionsAt, instructionsRestartAt time.Time
}

// Pane is the pane the agent runs in, or the one it is being launched in right now.
func (o *Orchestrator) Pane() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.status.PaneID != "" {
		return o.status.PaneID
	}
	return o.launching
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
	return &Orchestrator{opts: opts, kick: make(chan struct{}, 1), status: Status{State: state, Name: nameOr(opts.AgentName), Dir: opts.Dir, Model: opts.Model, UpdatedAt: opts.Now()}}
}

// Status returns the current status.
func nameOr(name string) string {
	if name == "" {
		return Name
	}
	return name
}

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
	if !o.waitForGate(ctx) {
		return
	}
	// Subscribed before the start, not after: a change herdr reports while (or just after) the
	// agent starts would otherwise fall between the two and wait for the next tick.
	fleetChanged := o.fleetEvents(ctx)
	if _, err := o.Ensure(ctx); err != nil {
		o.opts.Logger.Warn("orchestrator did not start", "err", err)
	}
	ticker := time.NewTicker(o.opts.WatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			o.Watch(ctx)
		case <-fleetChanged:
			o.Watch(ctx) // herdr reported a change; follow it now rather than at the next tick
		case <-o.kick:
			o.Watch(ctx)
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
	// The first subscription is taken here, before returning, so the caller is subscribed from
	// this point on; only renewals happen in the goroutine.
	sub := o.opts.Hub.Subscribe(0)
	go func() {
		for {
			forwardFleet(ctx, sub, changed)
			sub.Close()
			if ctx.Err() != nil {
				return
			}
			time.Sleep(100 * time.Millisecond) // a closed hub must not spin this loop
			sub = o.opts.Hub.Subscribe(0)
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
	// A gate that says not yet (herdr still connecting at start-up) makes this a wait, not a
	// failure: the watch starts the agent once the gate opens (upkeep.go).
	if reason := o.gateReason(ctx); reason != "" {
		o.setWaiting(reason)
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
	status := o.follow(agent)
	o.restoreRemoteControl(ctx, status.SessionID)
	o.checkInstructions(ctx, agent, false) // marked now; the watch restarts it when idle
	return o.Status()
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
		s.Detail = o.needsTerminalDetail()
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

// needsTerminalDetail says what to do about a start-up prompt, naming the agent: a lead's card said
// "Open the orchestrator's terminal" (found by e2e:claude, a lead in a folder Claude had not seen).
func (o *Orchestrator) needsTerminalDetail() string {
	whose := "the orchestrator's"
	if name := o.agentName(); name != Name {
		whose = name + "'s"
	}
	return "Claude stopped at a start-up prompt (folder trust or login). Open " + whose + " terminal, or its card under Needs you, and answer it."
}

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
		if err := o.opts.KV.SetKV(ctx, o.kvKey(kvPaneID), paneID); err != nil {
			o.opts.Logger.Warn("storing the orchestrator pane", "err", err)
		}
	}
	if sessionID != "" {
		if err := o.opts.KV.SetKV(ctx, o.kvKey(kvSessionID), sessionID); err != nil {
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
	return o.opts.Client.AgentGet(ctx, o.agentName())
}

// errNoShell means no pane at a bare shell could be found or made in time.
var errNoShell = errors.New("no pane at a bare shell prompt")
