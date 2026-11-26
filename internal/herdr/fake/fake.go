// Package fake is an in-memory herdr: a scriptable implementation of herdr.Client that backs
// --demo and the unit tests of everything above the herdr client (fleet, the API, and later the
// orchestrator and jobs). It models workspaces, tabs, panes and agents with states, canned screen
// text per pane, event emission on every change, and records what was prompted and typed so a
// test can assert on it.
//
// It follows herdr's observable rules where callers depend on them: ids look like herdr's
// ("w1", "w1:t1", "w1:p1"); agent targets resolve by name or pane id; agent.prompt refuses a
// blocked agent with agent_blocked and sends nothing; per-pane subscriptions need a pane id.
// It is not a terminal emulator: a pane's screen is whatever the script last set.
package fake

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/jclement/crowsnest/internal/herdr"
)

// Version and Protocol are what the fake reports from ping and snapshots.
const (
	Version  = "0.9.5-fake"
	Protocol = herdr.ProtocolVersion
)

// Prompt is one recorded agent.prompt.
type Prompt struct {
	Target string // as given by the caller
	PaneID string // resolved pane
	Text   string
}

// Keys is one recorded send_keys (agent or pane).
type Keys struct {
	PaneID string
	Keys   []string
}

// KeysHook runs after keys are recorded and may change state (the demo answers dialogs with it).
// It is called with the fake's lock released.
type KeysHook func(f *Fake, paneID string, keys []string)

// PromptHook runs after a prompt is accepted, with the lock released.
type PromptHook func(f *Fake, paneID, text string)

type workspace struct {
	info herdr.Workspace
	tabs []string // tab ids in order
	next int      // next tab and pane numbers within the workspace
}

type tab struct {
	info  herdr.Tab
	panes []string
}

type pane struct {
	info      herdr.Pane
	screen    string // visible screen, plain text
	ansi      string // visible screen with ANSI; falls back to screen when empty
	recent    string // scrollback; falls back to screen
	processes []herdr.Process
	agent     *herdr.Agent      // nil for a plain shell
	env       map[string]string // env given at workspace/tab creation
}

// Fake is the in-memory herdr. The zero value is not usable; call New.
type Fake struct {
	mu         sync.Mutex
	workspaces map[string]*workspace
	order      []string // workspace ids in sidebar order
	tabs       map[string]*tab
	panes      map[string]*pane
	nextWS     int
	revision   uint64

	prompts []Prompt
	keys    []Keys
	runs    []Keys // PaneRun commands, recorded as a single "key" each

	streams  map[*stream]struct{}
	failures map[string][]error // method → errors to return on the next calls

	onKeys   KeysHook
	onPrompt PromptHook

	starts        []herdr.AgentStartParams // every agent.start accepted, in order
	worktrees     []herdr.WorktreeCreateParams
	startNotReady bool                     // the next agent.start stops at a dialog
}

// New returns an empty fake session.
func New() *Fake {
	return &Fake{
		workspaces: map[string]*workspace{},
		tabs:       map[string]*tab{},
		panes:      map[string]*pane{},
		streams:    map[*stream]struct{}{},
		failures:   map[string][]error{},
	}
}

// ---- Scripting ---------------------------------------------------------------------

// FailNext makes the next call to method (a herdr method name such as "session.snapshot" or
// "agent.prompt") return err instead of doing anything. Calls queue up in order.
func (f *Fake) FailNext(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[method] = append(f.failures[method], err)
}

// OnKeys installs a hook run after every send_keys.
func (f *Fake) OnKeys(hook KeysHook) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onKeys = hook
}

// OnPrompt installs a hook run after every accepted prompt.
func (f *Fake) OnPrompt(hook PromptHook) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onPrompt = hook
}

// AddWorkspace creates a workspace with one tab and one shell pane, and returns their ids.
func (f *Fake) AddWorkspace(label, cwd string) (workspaceID, tabID, paneID string) {
	created, _ := f.WorkspaceCreate(context.Background(), herdr.WorkspaceCreateParams{Label: label, Cwd: cwd})
	return created.Workspace.ID, created.Tab.ID, created.RootPane.ID
}

// AddTab adds a tab with one shell pane to a workspace and returns their ids.
func (f *Fake) AddTab(workspaceID, label, cwd string) (tabID, paneID string) {
	created, _ := f.TabCreate(context.Background(), herdr.TabCreateParams{WorkspaceID: workspaceID, Label: label, Cwd: cwd})
	return created.Tab.ID, created.RootPane.ID
}

// AddAgent puts an agent of kind in a pane, named name, in status, with the given Claude session
// id (empty for none). It is what agent.start does, without the start-up dance.
func (f *Fake) AddAgent(paneID, name, kind string, status herdr.AgentStatus, sessionID string) error {
	f.mu.Lock()
	p, ok := f.panes[paneID]
	if !ok {
		f.mu.Unlock()
		return herdr.ErrPaneNotFound
	}
	f.attachAgentLocked(p, name, kind, status, sessionID)
	f.mu.Unlock()
	f.emit(herdr.Event{Kind: "pane_agent_detected", PaneID: paneID, WorkspaceID: p.info.WorkspaceID})
	return nil
}

// SetStatus changes an agent's status and emits pane.agent_status_changed.
func (f *Fake) SetStatus(target string, status herdr.AgentStatus) error {
	f.mu.Lock()
	p, err := f.resolveLocked(target)
	if err != nil {
		f.mu.Unlock()
		return err
	}
	f.setStatusLocked(p, status)
	f.mu.Unlock()
	f.emitStatus(p.info.ID, p.info.WorkspaceID, status)
	return nil
}

// SetScreen replaces a pane's visible screen (plain text) and emits pane_updated. ansi may be
// empty, in which case ANSI reads return the plain text.
func (f *Fake) SetScreen(paneID, text, ansi string) error {
	f.mu.Lock()
	p, ok := f.panes[paneID]
	if !ok {
		f.mu.Unlock()
		return herdr.ErrPaneNotFound
	}
	p.screen, p.ansi = text, ansi
	f.bumpLocked(p)
	f.mu.Unlock()
	f.emit(herdr.Event{Kind: "pane_updated", PaneID: paneID, WorkspaceID: p.info.WorkspaceID})
	return nil
}

// SetProcesses sets what pane.process_info reports as the pane's foreground processes.
func (f *Fake) SetProcesses(paneID string, processes ...herdr.Process) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.panes[paneID]
	if !ok {
		return herdr.ErrPaneNotFound
	}
	p.processes = processes
	return nil
}

// Prompts returns every prompt accepted so far.
func (f *Fake) Prompts() []Prompt {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Prompt(nil), f.prompts...)
}

// SentKeys returns every send_keys so far, agent and pane alike.
func (f *Fake) SentKeys() []Keys {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Keys(nil), f.keys...)
}

// Runs returns every PaneRun command so far.
func (f *Fake) Runs() []Keys {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Keys(nil), f.runs...)
}

// Streams is the number of open event subscriptions.
func (f *Fake) Streams() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.streams)
}

// DropStreams ends every open subscription with err (herdr.ErrEventsLost to simulate herdr
// falling behind, or nil for a dropped connection).
func (f *Fake) DropStreams(err error) {
	f.mu.Lock()
	streams := make([]*stream, 0, len(f.streams))
	for s := range f.streams {
		streams = append(streams, s)
		delete(f.streams, s)
	}
	f.mu.Unlock()
	for _, s := range streams {
		s.end(err)
	}
}

// ---- Internal state helpers (call with mu held) -----------------------------------

func (f *Fake) takeFailure(method string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	queue := f.failures[method]
	if len(queue) == 0 {
		return nil
	}
	f.failures[method] = queue[1:]
	return queue[0]
}

func (f *Fake) bumpLocked(p *pane) {
	f.revision++
	p.info.Revision = f.revision
	if p.agent != nil {
		p.agent.Revision = f.revision
	}
}

func (f *Fake) attachAgentLocked(p *pane, name, kind string, status herdr.AgentStatus, sessionID string) {
	agent := &herdr.Agent{
		PaneID: p.info.ID, TerminalID: p.info.TerminalID, WorkspaceID: p.info.WorkspaceID, TabID: p.info.TabID,
		Name: name, Agent: kind, AgentStatus: status, Cwd: p.info.Cwd, ForegroundCwd: p.info.Cwd,
		InteractiveReady: status != herdr.StatusBlocked, Title: kind,
	}
	if sessionID != "" {
		agent.AgentSession = &herdr.AgentSession{Agent: kind, Kind: "id", Source: "fake", Value: sessionID}
	}
	p.agent = agent
	p.info.Agent, p.info.AgentSession, p.info.AgentStatus = kind, agent.AgentSession, status
	p.processes = []herdr.Process{{PID: 4000 + len(f.panes), Name: kind, Argv: []string{kind}, Cwd: p.info.Cwd}}
	f.rollUpLocked(p.info.WorkspaceID)
	f.bumpLocked(p)
}

func (f *Fake) setStatusLocked(p *pane, status herdr.AgentStatus) {
	p.info.AgentStatus = status
	if p.agent != nil {
		p.agent.AgentStatus = status
		p.agent.StateChangeSeq++
		p.agent.InteractiveReady = true
	}
	f.rollUpLocked(p.info.WorkspaceID)
	f.bumpLocked(p)
}

// statusRank orders states for herdr's roll-up: the most urgent pane state wins.
var statusRank = map[herdr.AgentStatus]int{
	herdr.StatusBlocked: 5, herdr.StatusWorking: 4, herdr.StatusDone: 3, herdr.StatusIdle: 2, herdr.StatusUnknown: 1,
}

func rollUp(current, next herdr.AgentStatus) herdr.AgentStatus {
	if statusRank[next] > statusRank[current] {
		return next
	}
	return current
}

func (f *Fake) rollUpLocked(workspaceID string) {
	ws, ok := f.workspaces[workspaceID]
	if !ok {
		return
	}
	wsStatus := herdr.StatusUnknown
	for _, tabID := range ws.tabs {
		t := f.tabs[tabID]
		tabStatus := herdr.StatusUnknown
		for _, paneID := range t.panes {
			tabStatus = rollUp(tabStatus, f.panes[paneID].info.AgentStatus)
		}
		t.info.AgentStatus = tabStatus
		wsStatus = rollUp(wsStatus, tabStatus)
	}
	ws.info.AgentStatus = wsStatus
}

// resolveLocked finds the pane hosting an agent target: a live agent name or a pane id.
func (f *Fake) resolveLocked(target string) (*pane, error) {
	if p, ok := f.panes[target]; ok && p.agent != nil {
		return p, nil
	}
	for _, p := range f.panes {
		if p.agent != nil && p.agent.Name != "" && p.agent.Name == target {
			return p, nil
		}
	}
	return nil, &herdr.Error{Code: herdr.CodeAgentNotFound, Message: fmt.Sprintf("agent target %s not found", target)}
}

func (f *Fake) paneLocked(paneID string) (*pane, error) {
	p, ok := f.panes[paneID]
	if !ok {
		return nil, &herdr.Error{Code: herdr.CodePaneNotFound, Message: fmt.Sprintf("pane %s not found", paneID)}
	}
	return p, nil
}

func (f *Fake) newPaneLocked(ws *workspace, t *tab, cwd string) *pane {
	ws.next++
	id := fmt.Sprintf("%s:p%d", ws.info.ID, ws.next)
	p := &pane{
		info: herdr.Pane{
			ID: id, TerminalID: "term_fake_" + strings.ReplaceAll(id, ":", "_"), WorkspaceID: ws.info.ID, TabID: t.info.ID,
			AgentStatus: herdr.StatusUnknown, Cwd: cwd, ForegroundCwd: cwd,
		},
		screen:    shellPrompt(cwd),
		processes: []herdr.Process{{PID: 3000 + len(f.panes), Name: "zsh", Argv: []string{"-zsh"}, Cwd: cwd}},
	}
	f.panes[id] = p
	t.panes = append(t.panes, id)
	t.info.PaneCount = len(t.panes)
	ws.info.PaneCount++
	f.bumpLocked(p)
	return p
}

// shellPrompt is the screen of a fresh shell pane.
func shellPrompt(cwd string) string {
	return "\n" + cwd + "\n❯ \n"
}

func (f *Fake) agentsLocked() []herdr.Agent {
	var agents []herdr.Agent
	for _, id := range f.paneIDsLocked() {
		if p := f.panes[id]; p.agent != nil {
			agents = append(agents, *p.agent)
		}
	}
	return agents
}

// paneIDsLocked lists pane ids in workspace, tab, pane order, so output is deterministic.
func (f *Fake) paneIDsLocked() []string {
	var ids []string
	for _, wsID := range f.order {
		for _, tabID := range f.workspaces[wsID].tabs {
			ids = append(ids, f.tabs[tabID].panes...)
		}
	}
	return ids
}

func (f *Fake) tabIDsLocked(workspaceID string) []string {
	var ids []string
	for _, wsID := range f.order {
		if workspaceID == "" || workspaceID == wsID {
			ids = append(ids, f.workspaces[wsID].tabs...)
		}
	}
	return ids
}
