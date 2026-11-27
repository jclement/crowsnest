// The watcher: turns what happens in crowsnest into notices and pushes them.
//
// Sources:
//
//   - QuestionAsked, called from the question service's OnAsked hook (the same hook --notify-url
//     hangs off), so a question is pushed the moment it is stored.
//   - the hub's "job" events: a job going stuck (once per cause), failing, needing a fix only Jeff
//     can make (auth, config, launch), or finishing. One notice per job per reason; a job that
//     recovers and gets stuck again is pushed again.
//   - the hub's "fleet" events: an agent at a terminal dialog. A dialog must stay up for
//     dialogHold before it is pushed (a permission prompt answered at once by someone watching
//     the terminal is not worth a buzz), then once until it clears.
//   - the hub's "orchestrator" events: needs_terminal (a start-up prompt) and failed.
//
// Every notice passes the kind switch in Settings, then the rate gate, then goes out in the
// background. The hub closes a subscriber that falls behind; the watcher then subscribes again.

package push

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/questions"
)

const (
	// dialogHold is how long a dialog must stay up before it is pushed; the poker holds a blocked
	// agent 10 s for the same reason (DESIGN.md 7.7).
	dialogHold = 10 * time.Second
	// dialogTick is how often held dialogs are checked against dialogHold.
	dialogTick = 2 * time.Second
	// maxJobMarks bounds the per-job dedupe map; past it the oldest marks are simply forgotten
	// (at worst a finished job's notice could repeat if it emitted another event).
	maxJobMarks = 2000
	// approvalDialogWindow: a dialog that appears this soon after its pane's approval was pushed
	// is that approval's dialog, and is not pushed again.
	approvalDialogWindow = 2 * time.Minute
)

// Sender is the part of the Service the watcher uses; tests pass a fake.
type Sender interface {
	Enabled(ctx context.Context, kind Kind) bool
	Deliver(ctx context.Context, notice Notice, endpoint string) (Result, error)
}

// deviceLister is implemented by a Sender that can say whether any device is subscribed.
type deviceLister interface {
	HasDevices(ctx context.Context) bool
}

// WatcherOptions configure a Watcher. Sender is required.
type WatcherOptions struct {
	Sender Sender
	Hub    *hub.Hub
	Logger *slog.Logger
	Now    func() time.Time
	// Synchronous delivers in the caller's goroutine (tests); otherwise each notice goes out in
	// the background so no event source ever waits on a push service.
	Synchronous bool
	// Tokens mints the single-use action tokens a notification's buttons carry (DESIGN.md 8.7).
	// Nil: no notice carries buttons.
	Tokens Minter
	// Route asks the presence router whether a notice may be pushed now (DESIGN.md 6.14): not
	// while the human is here (it is on screen), not twice for one item, held by do not disturb
	// unless urgent. Nil: every notice that passes the kind switch and the rate gate is pushed.
	Route func(ctx context.Context, item, channel string, blocking bool) (bool, string)
}

// Minter mints an action token for one item (internal/actions).
type Minter interface {
	Mint(ctx context.Context, item string, actions []string) (string, error)
}

// Watcher decides what to push.
type Watcher struct {
	opts WatcherOptions
	gate *gate

	mu sync.Mutex
	// dialogs are panes seen at a dialog: when first seen, and whether already pushed.
	dialogs map[string]*dialogMark
	// jobMarks is the last reason pushed per job id ("" when none applies now).
	jobMarks map[string]string
	// orchState is the orchestrator state last seen.
	orchState orch.State
	// approvalPanes are panes whose tool call was pushed as an approval, and when: their terminal
	// dialog (the same request) is not pushed again.
	approvalPanes map[string]time.Time
}

type dialogMark struct {
	since     time.Time
	name      string
	workspace string
	pushed    bool
}

// NewWatcher builds a Watcher.
func NewWatcher(opts WatcherOptions) *Watcher {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Watcher{opts: opts, gate: newGate(opts.Now), dialogs: map[string]*dialogMark{}, jobMarks: map[string]string{}, approvalPanes: map[string]time.Time{}}
}

// Run reads the hub until ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	ticker := time.NewTicker(dialogTick)
	defer ticker.Stop()
	for ctx.Err() == nil {
		subscription := w.opts.Hub.Subscribe(hub.DefaultBuffer)
		w.drain(ctx, subscription, ticker.C)
		subscription.Close()
	}
}

// drain handles events until the subscription is closed or ctx ends.
func (w *Watcher) drain(ctx context.Context, subscription *hub.Subscription, tick <-chan time.Time) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			w.CheckDialogs(ctx)
		case event, open := <-subscription.Events():
			if !open {
				return
			}
			w.HandleEvent(ctx, event)
		}
	}
}

// HandleEvent reacts to one hub event.
func (w *Watcher) HandleEvent(ctx context.Context, event hub.Event) {
	switch data := event.Data.(type) {
	case jobs.HubEvent:
		w.jobChanged(ctx, data)
	case fleet.Fleet:
		w.fleetChanged(data)
	case orch.Status:
		w.orchestratorChanged(ctx, data)
	}
}

// QuestionAsked pushes a newly asked question (blocking and normal; low is not pushed). A confirm
// or choice question carries its first two options as buttons.
func (w *Watcher) QuestionAsked(question questions.Question) {
	notice, ok := QuestionNotice(question.ID, question.Title, string(question.Kind), string(question.Urgency), question.SourcePane)
	if !ok {
		return
	}
	var options []NoticeAction
	for _, option := range question.Options {
		options = append(options, NoticeAction{Action: option.Key, Title: option.Label})
	}
	if notice.Actions = QuestionActions(string(question.Kind), question.Multi, options); notice.Actions != nil {
		notice.ActionItem = "question:" + question.ID
	}
	w.notify(context.Background(), notice)
}

// ApprovalRequested pushes a tool call waiting for approval (DESIGN.md 8.7); its pane's terminal
// dialog, the same request, is then not pushed as well.
func (w *Watcher) ApprovalRequested(approval approvals.Approval) {
	w.mu.Lock()
	w.approvalPanes[approval.PaneID] = w.opts.Now()
	if mark, ok := w.dialogs[approval.PaneID]; ok {
		mark.pushed = true
	}
	w.mu.Unlock()
	w.notify(context.Background(), ApprovalNotice(approval.ID, string(approval.Risk), approval.AgentName, approval.Summary, approval.PaneID))
}

func (w *Watcher) jobChanged(ctx context.Context, event jobs.HubEvent) {
	notice, reason, ok := JobNotice(event.ID, event.Title, string(event.State), string(event.Cause), event.Stuck)
	w.mu.Lock()
	previous := w.jobMarks[event.ID]
	if len(w.jobMarks) > maxJobMarks {
		w.jobMarks = map[string]string{}
	}
	w.jobMarks[event.ID] = reason
	w.mu.Unlock()
	if ok && reason != previous {
		w.notify(ctx, notice)
	}
}

func (w *Watcher) fleetChanged(current fleet.Fleet) {
	if current.Error != "" {
		return // the last good picture: nothing new is known
	}
	now := w.opts.Now()
	w.mu.Lock()
	defer w.mu.Unlock()
	atDialog := map[string]bool{}
	for _, agent := range current.Agents {
		if !agent.NeedsYou {
			continue
		}
		atDialog[agent.PaneID] = true
		if _, seen := w.dialogs[agent.PaneID]; !seen {
			mark := &dialogMark{since: now, name: agent.Name, workspace: agent.Workspace}
			if at, ok := w.approvalPanes[agent.PaneID]; ok && now.Sub(at) < approvalDialogWindow {
				mark.pushed = true // already pushed as an approval
			}
			w.dialogs[agent.PaneID] = mark
		}
	}
	for pane := range w.dialogs {
		if !atDialog[pane] {
			delete(w.dialogs, pane) // cleared: a later dialog in this pane is pushed again
		}
	}
}

// CheckDialogs pushes every dialog that has been up for dialogHold and not pushed yet.
func (w *Watcher) CheckDialogs(ctx context.Context) {
	now := w.opts.Now()
	var due []Notice
	w.mu.Lock()
	for pane, mark := range w.dialogs {
		if !mark.pushed && now.Sub(mark.since) >= dialogHold {
			mark.pushed = true
			due = append(due, DialogNotice(pane, mark.name, mark.workspace))
		}
	}
	w.mu.Unlock()
	for _, notice := range due {
		w.notify(ctx, notice)
	}
}

func (w *Watcher) orchestratorChanged(ctx context.Context, status orch.Status) {
	w.mu.Lock()
	changed := status.State != w.orchState
	w.orchState = status.State
	w.mu.Unlock()
	if !changed {
		return
	}
	if notice, ok := OrchestratorNotice(string(status.State), status.Detail); ok {
		w.notify(ctx, notice)
	}
}

// notify applies the kind switch and the rate gate, then delivers.
func (w *Watcher) notify(ctx context.Context, notice Notice) {
	if !w.opts.Sender.Enabled(ctx, notice.Kind) {
		return
	}
	if w.opts.Route != nil {
		if ok, why := w.opts.Route(ctx, notice.Tag, "push", notice.Urgency == UrgencyHigh); !ok {
			w.opts.Logger.Info("not pushing a notice: "+why, "kind", notice.Kind, "tag", notice.Tag)
			return
		}
	}
	// A notice no device would receive must not spend the rate budget: a burst at start-up (the
	// demo's seeded morning, or a backlog after a restart) would otherwise hold back the first
	// notice the first device subscribes for.
	if devices, ok := w.opts.Sender.(deviceLister); ok && !devices.HasDevices(ctx) {
		return
	}
	if ok, why := w.gate.allow(notice.Source); !ok {
		w.opts.Logger.Info("not pushing a notice: "+why, "kind", notice.Kind, "tag", notice.Tag)
		return
	}
	deliver := func() {
		notice = w.withActionToken(context.WithoutCancel(ctx), notice)
		result, err := w.opts.Sender.Deliver(context.WithoutCancel(ctx), notice, "")
		if err != nil {
			w.opts.Logger.Warn("pushing a notice failed", "kind", notice.Kind, "err", err)
			return
		}
		if result.Sent+result.Failed+result.Pruned > 0 {
			w.opts.Logger.Info("pushed a notice", "kind", notice.Kind, "tag", notice.Tag, "sent", result.Sent, "failed", result.Failed, "pruned", result.Pruned)
		}
	}
	if w.opts.Synchronous {
		deliver()
		return
	}
	go deliver()
}

// withActionToken mints the single-use token a notice's buttons carry; without one (no minter, no
// item, a failed mint) the notice goes without buttons and a tap opens the app.
func (w *Watcher) withActionToken(ctx context.Context, notice Notice) Notice {
	if len(notice.Actions) == 0 || notice.ActionItem == "" || w.opts.Tokens == nil {
		notice.Actions, notice.ActionToken = nil, ""
		return notice
	}
	names := make([]string, 0, len(notice.Actions))
	for _, action := range notice.Actions {
		names = append(names, action.Action)
	}
	token, err := w.opts.Tokens.Mint(ctx, notice.ActionItem, names)
	if err != nil {
		w.opts.Logger.Warn("minting a notification's action token; sending it without buttons", "err", err)
		notice.Actions, notice.ActionToken = nil, ""
		return notice
	}
	notice.ActionToken = token
	return notice
}
