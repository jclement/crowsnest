// The herdr.Client methods of the fake, and its event streams.

package fake

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/jclement/crowsnest/internal/herdr"
)

// Ping implements herdr.Client.
func (f *Fake) Ping(context.Context) (herdr.Pong, error) {
	if err := f.takeFailure("ping"); err != nil {
		return herdr.Pong{}, err
	}
	return herdr.Pong{Version: Version, Protocol: Protocol}, nil
}

// Snapshot implements herdr.Client.
func (f *Fake) Snapshot(context.Context) (herdr.Snapshot, error) {
	if err := f.takeFailure("session.snapshot"); err != nil {
		return herdr.Snapshot{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	snapshot := herdr.Snapshot{
		Version: Version, Protocol: Protocol,
		Workspaces: []herdr.Workspace{}, Tabs: []herdr.Tab{}, Panes: []herdr.Pane{}, Agents: []herdr.Agent{},
	}
	for _, wsID := range f.order {
		snapshot.Workspaces = append(snapshot.Workspaces, f.workspaces[wsID].info)
	}
	for _, tabID := range f.tabIDsLocked("") {
		snapshot.Tabs = append(snapshot.Tabs, f.tabs[tabID].info)
	}
	for _, paneID := range f.paneIDsLocked() {
		snapshot.Panes = append(snapshot.Panes, f.panes[paneID].info)
	}
	snapshot.Agents = append(snapshot.Agents, f.agentsLocked()...)
	return snapshot, nil
}

// WorkspaceCreate implements herdr.Client.
func (f *Fake) WorkspaceCreate(_ context.Context, params herdr.WorkspaceCreateParams) (herdr.WorkspaceCreated, error) {
	if err := f.takeFailure("workspace.create"); err != nil {
		return herdr.WorkspaceCreated{}, err
	}
	f.mu.Lock()
	f.nextWS++
	id := fmt.Sprintf("w%d", f.nextWS)
	label := params.Label
	if label == "" {
		label = id
	}
	ws := &workspace{info: herdr.Workspace{ID: id, Number: len(f.order) + 1, Label: label, AgentStatus: herdr.StatusUnknown}}
	f.workspaces[id] = ws
	f.order = append(f.order, id)
	t, p := f.newTabLocked(ws, "", params.Cwd)
	p.env = params.Env
	ws.info.ActiveTabID = t.info.ID
	created := herdr.WorkspaceCreated{Workspace: ws.info, Tab: t.info, RootPane: p.info}
	f.mu.Unlock()
	f.emit(herdr.Event{Kind: "workspace_created", WorkspaceID: id})
	f.emit(herdr.Event{Kind: "tab_created", WorkspaceID: id})
	f.emit(herdr.Event{Kind: "pane_created", PaneID: p.info.ID, WorkspaceID: id})
	return created, nil
}

func (f *Fake) newTabLocked(ws *workspace, label, cwd string) (*tab, *pane) {
	number := len(ws.tabs) + 1
	if label == "" {
		label = fmt.Sprint(number)
	}
	id := fmt.Sprintf("%s:t%d", ws.info.ID, number)
	t := &tab{info: herdr.Tab{ID: id, WorkspaceID: ws.info.ID, Number: number, Label: label, AgentStatus: herdr.StatusUnknown}}
	f.tabs[id] = t
	ws.tabs = append(ws.tabs, id)
	ws.info.TabCount = len(ws.tabs)
	p := f.newPaneLocked(ws, t, cwd)
	return t, p
}

// WorkspaceList implements herdr.Client.
func (f *Fake) WorkspaceList(context.Context) ([]herdr.Workspace, error) {
	if err := f.takeFailure("workspace.list"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	list := []herdr.Workspace{}
	for _, wsID := range f.order {
		list = append(list, f.workspaces[wsID].info)
	}
	return list, nil
}

// TabCreate implements herdr.Client.
func (f *Fake) TabCreate(_ context.Context, params herdr.TabCreateParams) (herdr.TabCreated, error) {
	if err := f.takeFailure("tab.create"); err != nil {
		return herdr.TabCreated{}, err
	}
	f.mu.Lock()
	ws, ok := f.workspaces[params.WorkspaceID]
	if !ok {
		f.mu.Unlock()
		return herdr.TabCreated{}, &herdr.Error{Code: herdr.CodeWorkspaceNotFound, Message: "workspace not found"}
	}
	t, p := f.newTabLocked(ws, params.Label, params.Cwd)
	p.env = params.Env
	created := herdr.TabCreated{Tab: t.info, RootPane: p.info}
	f.mu.Unlock()
	f.emit(herdr.Event{Kind: "tab_created", WorkspaceID: ws.info.ID})
	f.emit(herdr.Event{Kind: "pane_created", PaneID: p.info.ID, WorkspaceID: ws.info.ID})
	return created, nil
}

// TabList implements herdr.Client.
func (f *Fake) TabList(_ context.Context, workspaceID string) ([]herdr.Tab, error) {
	if err := f.takeFailure("tab.list"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	list := []herdr.Tab{}
	for _, tabID := range f.tabIDsLocked(workspaceID) {
		list = append(list, f.tabs[tabID].info)
	}
	return list, nil
}

// PaneList implements herdr.Client.
func (f *Fake) PaneList(_ context.Context, workspaceID string) ([]herdr.Pane, error) {
	if err := f.takeFailure("pane.list"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	list := []herdr.Pane{}
	for _, paneID := range f.paneIDsLocked() {
		if p := f.panes[paneID]; workspaceID == "" || p.info.WorkspaceID == workspaceID {
			list = append(list, p.info)
		}
	}
	return list, nil
}

// PaneGet implements herdr.Client.
func (f *Fake) PaneGet(_ context.Context, paneID string) (herdr.Pane, error) {
	if err := f.takeFailure("pane.get"); err != nil {
		return herdr.Pane{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.paneLocked(paneID)
	if err != nil {
		return herdr.Pane{}, err
	}
	return p.info, nil
}

// PaneRead implements herdr.Client.
func (f *Fake) PaneRead(_ context.Context, paneID string, opts herdr.ReadOptions) (herdr.ReadResult, error) {
	if err := f.takeFailure("pane.read"); err != nil {
		return herdr.ReadResult{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.paneLocked(paneID)
	if err != nil {
		return herdr.ReadResult{}, err
	}
	return readPane(p, opts), nil
}

func readPane(p *pane, opts herdr.ReadOptions) herdr.ReadResult {
	source := opts.Source
	if source == "" {
		source = herdr.SourceVisible
	}
	text, format := p.screen, herdr.FormatText
	if source == herdr.SourceRecent || source == herdr.SourceRecentUnwrapped {
		text = p.recent + p.screen
	}
	if opts.ANSI {
		format = herdr.FormatANSI
		if p.ansi != "" && source == herdr.SourceVisible {
			text = p.ansi
		}
	}
	if opts.Lines > 0 {
		lines := strings.Split(text, "\n")
		if len(lines) > opts.Lines {
			text = strings.Join(lines[len(lines)-opts.Lines:], "\n")
		}
	}
	return herdr.ReadResult{
		PaneID: p.info.ID, WorkspaceID: p.info.WorkspaceID, TabID: p.info.TabID,
		Source: source, Format: format, Text: text, Revision: p.info.Revision,
	}
}

// PaneProcessInfo implements herdr.Client.
func (f *Fake) PaneProcessInfo(_ context.Context, paneID string) (herdr.ProcessInfo, error) {
	if err := f.takeFailure("pane.process_info"); err != nil {
		return herdr.ProcessInfo{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.paneLocked(paneID)
	if err != nil {
		return herdr.ProcessInfo{}, err
	}
	info := herdr.ProcessInfo{PaneID: paneID, ForegroundProcesses: append([]herdr.Process(nil), p.processes...)}
	if len(p.processes) > 0 {
		info.ShellPID = p.processes[0].PID
	}
	return info, nil
}

// PaneRun implements herdr.Client: the command is recorded and echoed onto the screen, as a
// shell would show it. Nothing runs.
func (f *Fake) PaneRun(_ context.Context, paneID, command string) error {
	if err := f.takeFailure("pane.run"); err != nil {
		return err
	}
	f.mu.Lock()
	p, err := f.paneLocked(paneID)
	if err != nil {
		f.mu.Unlock()
		return err
	}
	f.runs = append(f.runs, Keys{PaneID: paneID, Keys: []string{command}})
	p.recent += p.screen
	p.screen = "❯ " + command + "\n" + shellPrompt(p.info.Cwd)
	f.bumpLocked(p)
	f.mu.Unlock()
	f.emit(herdr.Event{Kind: "pane_updated", PaneID: paneID, WorkspaceID: p.info.WorkspaceID})
	return nil
}

// PaneSendKeys implements herdr.Client.
func (f *Fake) PaneSendKeys(_ context.Context, paneID string, keys []string) error {
	if err := f.takeFailure("pane.send_keys"); err != nil {
		return err
	}
	f.mu.Lock()
	if _, err := f.paneLocked(paneID); err != nil {
		f.mu.Unlock()
		return err
	}
	return f.recordKeys(paneID, keys)
}

// recordKeys is entered with mu held and releases it before running the hook.
func (f *Fake) recordKeys(paneID string, keys []string) error {
	f.keys = append(f.keys, Keys{PaneID: paneID, Keys: append([]string(nil), keys...)})
	hook := f.onKeys
	f.mu.Unlock()
	if hook != nil {
		hook(f, paneID, keys)
	}
	return nil
}

// AgentStart implements herdr.Client. The pane must hold only a shell, like herdr requires.
func (f *Fake) AgentStart(_ context.Context, params herdr.AgentStartParams) (herdr.Agent, error) {
	if err := f.takeFailure("agent.start"); err != nil {
		return herdr.Agent{}, err
	}
	f.mu.Lock()
	p, err := f.paneLocked(params.PaneID)
	if err != nil {
		f.mu.Unlock()
		return herdr.Agent{}, err
	}
	if !onlyShell(p.processes) {
		f.mu.Unlock()
		return herdr.Agent{}, &herdr.Error{Code: "pane_busy", Message: "pane is not at a shell prompt"}
	}
	for _, other := range f.panes {
		if other.agent != nil && params.Name != "" && other.agent.Name == params.Name {
			f.mu.Unlock()
			return herdr.Agent{}, &herdr.Error{Code: "agent_name_taken", Message: "agent name " + params.Name + " is already used", Method: "agent.start"}
		}
	}
	f.starts = append(f.starts, params)
	session := sessionFromArgs(params.Args, fmt.Sprintf("fake-session-%s", strings.ReplaceAll(p.info.ID, ":", "-")))
	notReady := f.startNotReady
	f.startNotReady = false
	status := herdr.StatusIdle
	if notReady {
		status = herdr.StatusBlocked // parked at a trust dialog, as real Claude would be
	}
	f.attachAgentLocked(p, params.Name, params.Kind, status, session)
	if notReady {
		p.agent.InteractiveReady, p.agent.LaunchPending = false, true
	}
	// Like herdr over the socket, agent.start answers at once with the launch still pending;
	// the caller polls agent.get until the agent is interactive-ready.
	agent := *p.agent
	agent.LaunchPending, agent.InteractiveReady, agent.AgentStatus, agent.AgentSession = true, false, herdr.StatusUnknown, nil
	f.mu.Unlock()
	f.emit(herdr.Event{Kind: "pane_agent_detected", PaneID: agent.PaneID, WorkspaceID: agent.WorkspaceID})
	return agent, nil
}

// AgentGet implements herdr.Client.
func (f *Fake) AgentGet(_ context.Context, target string) (herdr.Agent, error) {
	if err := f.takeFailure("agent.get"); err != nil {
		return herdr.Agent{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.resolveLocked(target)
	if err != nil {
		return herdr.Agent{}, err
	}
	return *p.agent, nil
}

// AgentList implements herdr.Client.
func (f *Fake) AgentList(context.Context) ([]herdr.Agent, error) {
	if err := f.takeFailure("agent.list"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]herdr.Agent{}, f.agentsLocked()...), nil
}

// AgentPrompt implements herdr.Client: refused with agent_blocked when the agent is blocked;
// otherwise recorded, and the agent goes to working (as a real turn would start).
func (f *Fake) AgentPrompt(_ context.Context, target, text string, _ *herdr.PromptWait) (herdr.Agent, error) {
	if err := f.takeFailure("agent.prompt"); err != nil {
		return herdr.Agent{}, err
	}
	f.mu.Lock()
	p, err := f.resolveLocked(target)
	if err != nil {
		f.mu.Unlock()
		return herdr.Agent{}, err
	}
	if p.agent.AgentStatus == herdr.StatusBlocked {
		f.mu.Unlock()
		return herdr.Agent{}, &herdr.Error{Code: herdr.CodeAgentBlocked, Message: "agent is waiting at a dialog", Method: "agent.prompt"}
	}
	f.prompts = append(f.prompts, Prompt{Target: target, PaneID: p.info.ID, Text: text})
	if strings.TrimSpace(text) == "/exit" {
		exiting := *p.agent
		f.mu.Unlock()
		return exiting, f.ExitAgent(exiting.PaneID)
	}
	f.setStatusLocked(p, herdr.StatusWorking)
	agent := *p.agent
	hook := f.onPrompt
	f.mu.Unlock()
	f.emitStatus(agent.PaneID, agent.WorkspaceID, herdr.StatusWorking)
	if hook != nil {
		hook(f, agent.PaneID, text)
	}
	return agent, nil
}

// AgentRead implements herdr.Client.
func (f *Fake) AgentRead(_ context.Context, target string, opts herdr.ReadOptions) (herdr.ReadResult, error) {
	if err := f.takeFailure("agent.read"); err != nil {
		return herdr.ReadResult{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.resolveLocked(target)
	if err != nil {
		return herdr.ReadResult{}, err
	}
	return readPane(p, opts), nil
}

// AgentSendKeys implements herdr.Client.
func (f *Fake) AgentSendKeys(_ context.Context, target string, keys []string) error {
	if err := f.takeFailure("agent.send_keys"); err != nil {
		return err
	}
	f.mu.Lock()
	p, err := f.resolveLocked(target)
	if err != nil {
		f.mu.Unlock()
		return err
	}
	return f.recordKeys(p.info.ID, keys)
}

// AgentWait implements herdr.Client. The fake does not block: it answers with the current state
// if it matches until (idle, done or blocked when empty), and herdr's timeout error otherwise.
func (f *Fake) AgentWait(_ context.Context, target string, until []herdr.AgentStatus, _ int) (herdr.Agent, error) {
	if err := f.takeFailure("agent.wait"); err != nil {
		return herdr.Agent{}, err
	}
	if len(until) == 0 {
		until = []herdr.AgentStatus{herdr.StatusIdle, herdr.StatusDone, herdr.StatusBlocked}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.resolveLocked(target)
	if err != nil {
		return herdr.Agent{}, err
	}
	if !slices.Contains(until, p.agent.AgentStatus) {
		return herdr.Agent{}, &herdr.Error{Code: herdr.CodeTimeout, Message: "timed out waiting for agent status", Method: "agent.wait"}
	}
	return *p.agent, nil
}

// AgentRename implements herdr.Client.
func (f *Fake) AgentRename(_ context.Context, target, name string) (herdr.Agent, error) {
	if err := f.takeFailure("agent.rename"); err != nil {
		return herdr.Agent{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.resolveLocked(target)
	if err != nil {
		return herdr.Agent{}, err
	}
	p.agent.Name = name
	return *p.agent, nil
}

// ---- Events ------------------------------------------------------------------------

// Subscribe implements herdr.Client with herdr's validation rule: per-pane types need a pane id
// and the others must not have one, or the whole subscription is refused.
func (f *Fake) Subscribe(ctx context.Context, subscriptions []herdr.Subscription) (herdr.EventStream, error) {
	if err := f.takeFailure("events.subscribe"); err != nil {
		return nil, err
	}
	s := &stream{owner: f, events: make(chan herdr.Event, 256), lifecycle: map[string]bool{}, panes: map[string]bool{}}
	for _, sub := range subscriptions {
		requiresPane := herdr.RequiresPane(sub.Type)
		if requiresPane != (sub.PaneID != "") {
			return nil, &herdr.Error{Code: herdr.CodeInvalidRequest, Message: "invalid subscription " + sub.Type, Method: "events.subscribe"}
		}
		if requiresPane {
			s.panes[sub.PaneID] = true
		} else {
			s.lifecycle[strings.ReplaceAll(sub.Type, ".", "_")] = true
		}
	}
	f.mu.Lock()
	f.streams[s] = struct{}{}
	f.mu.Unlock()
	go func() {
		<-ctx.Done()
		f.mu.Lock()
		delete(f.streams, s)
		f.mu.Unlock()
		s.end(nil)
	}()
	return s, nil
}

// emit delivers a lifecycle event to every stream subscribed to its kind.
func (f *Fake) emit(event herdr.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for s := range f.streams {
		if s.lifecycle[event.Kind] {
			s.send(event)
		}
	}
}

func (f *Fake) emitStatus(paneID, workspaceID string, status herdr.AgentStatus) {
	event := herdr.Event{Kind: herdr.EventAgentStatusChanged, PaneID: paneID, WorkspaceID: workspaceID, Status: status}
	f.mu.Lock()
	defer f.mu.Unlock()
	for s := range f.streams {
		if s.panes[paneID] {
			s.send(event)
		}
	}
}

// stream is one fake subscription. A stream whose consumer stops reading is ended with
// events_lost, as herdr does.
type stream struct {
	owner     *Fake
	events    chan herdr.Event
	lifecycle map[string]bool // event kinds, underscore form ("pane_created")
	panes     map[string]bool // pane ids for agent status changes
	mu        sync.Mutex
	err       error
	ended     bool
}

func (s *stream) Events() <-chan herdr.Event { return s.events }

func (s *stream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *stream) Close() error {
	s.owner.mu.Lock()
	delete(s.owner.streams, s)
	s.owner.mu.Unlock()
	s.end(nil)
	return nil
}

func (s *stream) send(event herdr.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	select {
	case s.events <- event:
	default:
		s.err, s.ended = herdr.ErrEventsLost, true
		close(s.events)
	}
}

func (s *stream) end(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.err, s.ended = err, true
	close(s.events)
}

var _ herdr.Client = (*Fake)(nil)
