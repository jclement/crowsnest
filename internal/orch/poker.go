// Pokes (DESIGN.md 7.4): the runtime half. The Poker watches the session's agents (one
// agent.list every couple of seconds), collects reasons from events (a question settled, a job
// finished or stuck, the standing picture changed at the timer, a manual reason), runs the pure
// Precheck and Gate from poke.go, and types the poke through the web app's own send path
// (Prompt), which waits for the turn to start. Every poke and every skip worth knowing about is
// journalled, and a `poke` event carries the outcome.
//
// Reasons that are due but gated (the orchestrator is working, a draft is in its input box)
// stay pending and go out with the next poke the gate allows; a poke is never lost to timing.
// What was pushed survives a crowsnest restart in kv (one JSON blob), so each agent state
// (pane, name, state_change_seq) and each event reason is pushed once.
//
// The same loop runs the herdr-side backstop (backstop.go) for agents without hooks.

package orch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/termtext"
)

const (
	// DefaultPokeCheckInterval is how often the poker looks at the agents and its reasons. It is
	// what the holds are measured with, so it is a few seconds, not the timer interval.
	DefaultPokeCheckInterval = 2 * time.Second
	// kvPokeState is the kv key of the poker's persistent bookkeeping.
	kvPokeState = "poke.state"
	// deliveredKeep is how long a delivered event reason's key is remembered, so a job that
	// stays stuck is not pushed again on its next unrelated change.
	deliveredKeep = 7 * 24 * time.Hour
	// maxDelivered caps the remembered keys.
	maxDelivered = 1000
	// pokeSendTimeout bounds one poke: Prompt waits up to 30 s for the turn to start.
	pokeSendTimeout = 45 * time.Second
)

// Outcomes of a poke attempt.
const (
	OutcomeSent    = "sent"
	OutcomeSkipped = "skipped"
	OutcomeNothing = "nothing" // an automatic check found nothing due (not journalled)
)

// PokeResult is what one check did; POST /orchestrator/poke returns it and a `poke` event
// carries it.
type PokeResult struct {
	Outcome string    `json:"outcome"`          // sent, skipped or nothing
	Reason  string    `json:"reason,omitempty"` // why it was skipped, in words
	Reasons []string  `json:"reasons"`          // the lines that were (or would have been) sent
	Manual  bool      `json:"manual"`
	Started bool      `json:"started,omitempty"` // the orchestrator was seen to start a turn
	Note    string    `json:"note,omitempty"`    // what Prompt said about an uncertain send
	At      time.Time `json:"at"`
}

// QuestionInfo is a settled question, as the poker needs it.
type QuestionInfo struct {
	ID, Title, Status, Kind string
	SourcePane              string // the asker's pane; the orchestrator's own are not news
}

// JobInfo is a job that changed, as the poker needs it.
type JobInfo struct {
	ID, Title, State string
	Stuck            bool
	Cause            string // stuck or needs-you cause, in words
	PaneID           string // the agent holding it; the orchestrator's own are not news
}

// PokerOptions configure a Poker. Client, KV and Status are required.
type PokerOptions struct {
	Client  herdr.Client
	KV      KV
	Journal *journal.Journal // nil: nothing is journalled
	Hub     *hub.Hub         // nil: no `poke` events
	Logger  *slog.Logger
	// Status is the orchestrator's current status (Orchestrator.Status).
	Status func() Status
	// Talk reports whether talk mode is on, for the header's talk= flag. Nil: off.
	Talk func() bool
	// Interval is the timer (--poke-interval); 0 disables timer pokes.
	Interval time.Duration
	// Standing lists what is open right now (stuck jobs, jobs that need the human, open
	// questions), one line each. At each timer tick a poke is due when this list changed since
	// the last timer poke. Nil: the timer never finds anything.
	Standing func(ctx context.Context) []string
	// Backstop, when set, is nudged for agents without hooks (backstop.go).
	Backstop Backstop
	// Alert tells the human something (the --notify-url POST). Nil: journal only.
	Alert func(text string)
	// ManualOnly sends pokes only for "Poke now" (--demo, whose scripted fleet would otherwise
	// poke its scripted orchestrator every two minutes); the automatic checks still observe.
	ManualOnly bool
	// Tasks are the scheduled tasks (7.8). Nil: none.
	Tasks TaskSource
	// Tender keeps the orchestrator's context small (7.9): it runs first at every check and may
	// spend the check on a compaction or a hand-over, holding the poke to the next one. Nil: none.
	Tender Upkeep
	// CheckInterval overrides DefaultPokeCheckInterval.
	CheckInterval time.Duration
	Now           func() time.Time
}

// ScheduledTask is a due task as the poker needs it (internal/schedule adapts to this).
type ScheduledTask struct {
	Name      string
	Text      string // the reason line, naming the task file
	RideAlong bool   // every: poke: only with a poke that has something else due
}

// TaskSource lists due scheduled tasks and is told which ones a delivered poke carried; the
// stamps are its own, set only then, so a task never repeats because the model forgot it.
type TaskSource interface {
	DueTasks(ctx context.Context, now time.Time) []ScheduledTask
	Delivered(ctx context.Context, at time.Time, names []string)
}

// Upkeep is what shares the orchestrator's input box with the pokes (the Tender: compaction, the
// daily new conversation). Tend reports whether it typed something this check.
type Upkeep interface {
	Tend(ctx context.Context, now time.Time, status Status) bool
}

// pokeState is the bookkeeping kept in kv.
type pokeState struct {
	Pushed        map[string]string `json:"pushed"`    // pane → "name:seq" last pushed
	Delivered     map[string]int64  `json:"delivered"` // event reason key → unix time pushed
	LastAgentPush int64             `json:"last_agent_push"`
	Timer         string            `json:"timer"` // fingerprint of the standing list last pushed
	// Pending are the event reasons (a job or question that settled) not yet delivered (R2-C13):
	// a restart while the gate held them used to lose them, because the hub event that made
	// them is gone. Agent and timer reasons are not kept: observation rebuilds them.
	Pending []Reason `json:"pending,omitempty"`
}

// Poker decides and sends pokes. Build it with NewPoker, then Run it.
type Poker struct {
	opts PokerOptions
	kick chan struct{}

	// step serialises checks: the loop's, a manual "Poke now", and a test's.
	step sync.Mutex

	mu         sync.Mutex
	loaded     bool
	state      pokeState
	pending    []Reason
	agents     map[string]AgentSeen // by pane, as last observed
	lastTimer  time.Time
	draftSince time.Time
	alerted    bool   // the draft alert for this draft was sent
	lastSkip   string // the gate refusal last journalled, so a held poke is journalled once
	backstop   map[string]time.Time
}

// NewPoker returns a poker; nothing runs until Run (or Check).
func NewPoker(opts PokerOptions) *Poker {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.CheckInterval <= 0 {
		opts.CheckInterval = DefaultPokeCheckInterval
	}
	return &Poker{
		opts: opts, kick: make(chan struct{}, 1), agents: map[string]AgentSeen{}, backstop: map[string]time.Time{},
		state: pokeState{Pushed: map[string]string{}, Delivered: map[string]int64{}},
	}
}

// Run checks every CheckInterval, and at once when a reason arrives, until ctx ends.
func (p *Poker) Run(ctx context.Context) {
	p.mu.Lock()
	p.lastTimer = p.opts.Now()
	p.mu.Unlock()
	ticker := time.NewTicker(p.opts.CheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-p.kick:
		}
		p.Check(ctx, false)
	}
}

// PokeNow is the "Poke now" button: a check that skips the agent push interval and journals a
// skip even when nothing is due.
func (p *Poker) PokeNow(ctx context.Context) PokeResult { return p.Check(ctx, true) }

// Add queues a reason (it is due at the next check) unless the same key is pending or was
// already delivered.
func (p *Poker) Add(ctx context.Context, reason Reason) {
	p.mu.Lock()
	p.loadLocked(ctx)
	_, delivered := p.state.Delivered[reason.Key]
	pending := slices.ContainsFunc(p.pending, func(r Reason) bool { return r.Key == reason.Key })
	if !delivered && !pending {
		if reason.At.IsZero() {
			reason.At = p.opts.Now()
		}
		p.pending = append(p.pending, reason)
		if durable(reason) {
			p.saveLocked(ctx) // kept before the event is acknowledged (R2-C13)
		}
	}
	p.mu.Unlock()
	select {
	case p.kick <- struct{}{}:
	default:
	}
}

// QuestionSettled queues a reason for a question that was answered, expired or cancelled,
// unless the orchestrator asked it (its answer is typed to it anyway) or it was a notice.
func (p *Poker) QuestionSettled(ctx context.Context, q QuestionInfo) {
	if q.Kind == "notice" || q.SourcePane == "" || q.SourcePane == p.opts.Status().PaneID {
		return
	}
	// The title is the asker's words: quoted, so it cannot pass for a header (internal/termtext).
	text := q.ID + " (asked by pane " + q.SourcePane + ") was " + q.Status + ": " + termtext.Quote(q.Title)
	p.Add(ctx, Reason{Key: "question:" + q.ID, Source: SourceQuestion, Text: text})
}

// JobChanged queues a reason for a job that reached a terminal state or got stuck, unless the
// orchestrator holds it.
func (p *Poker) JobChanged(ctx context.Context, job JobInfo) {
	if job.PaneID != "" && job.PaneID == p.opts.Status().PaneID {
		return
	}
	var key, what string
	switch {
	case job.State == "done" || job.State == "failed" || job.State == "cancelled":
		key, what = "job:"+job.ID+":"+job.State, "is "+job.State
	case job.Stuck:
		key, what = "job:"+job.ID+":stuck:"+job.Cause, "is stuck"
		if job.Cause != "" {
			what += " (" + job.Cause + ")"
		}
	default:
		return
	}
	p.Add(ctx, Reason{Key: key, Source: SourceJob, Text: "job " + job.ID + " " + what + ": " + termtext.Quote(job.Title)})
}

// Check observes the agents, runs the backstop and the timer, and pokes if anything is due and
// the gate allows it. manual marks a "Poke now".
func (p *Poker) Check(ctx context.Context, manual bool) PokeResult {
	p.step.Lock()
	defer p.step.Unlock()
	now := p.opts.Now()
	p.observe(ctx, now)
	p.runBackstop(ctx, now)
	p.timerReason(ctx, now)

	status := p.opts.Status()
	if status.State == StateDisabled {
		return p.disabled(ctx, now, manual)
	}
	if !p.opts.ManualOnly && p.opts.Tender != nil && p.opts.Tender.Tend(ctx, now, status) {
		// The check went on upkeep; due reasons stay and go with the next poke.
		result := PokeResult{Outcome: OutcomeNothing, Reasons: []string{}, Manual: manual, At: now}
		if manual {
			result.Outcome, result.Reason = OutcomeSkipped, "the orchestrator is being compacted or handed over; the poke goes next"
			p.journal(ctx, journal.KindSkip, "Poke now skipped: "+result.Reason, result)
			p.publish(result)
		}
		return result
	}
	input := p.precheckInput(now, manual)
	input.Tasks, input.RideAlong = p.dueTasks(ctx, now)
	due := Precheck(input)
	result := PokeResult{Outcome: OutcomeNothing, Reasons: ReasonTexts(due.Reasons), Manual: manual, At: now}
	if len(due.Reasons) == 0 {
		if manual {
			result.Outcome, result.Reason = OutcomeSkipped, "nothing is due"
			if due.AgentsHeld > 0 {
				result.Reason = "nothing is due yet"
			}
			p.journal(ctx, journal.KindSkip, "Poke now: "+result.Reason, result)
			p.publish(result)
		}
		return result
	}
	if !manual && p.opts.ManualOnly {
		return result
	}
	decision := p.gate(ctx, now, status)
	if decision.Verdict != GateSend {
		return p.skipped(ctx, result, decision)
	}
	return p.send(ctx, result, status, due)
}

// disabled answers a check under --no-orchestrator: nothing to poke, nothing kept pending.
func (p *Poker) disabled(ctx context.Context, now time.Time, manual bool) PokeResult {
	p.mu.Lock()
	p.loadLocked(ctx)
	p.pending = nil
	p.saveLocked(ctx)
	p.mu.Unlock()
	result := PokeResult{Outcome: OutcomeNothing, Reasons: []string{}, Manual: manual, At: now}
	if manual {
		result.Outcome, result.Reason = OutcomeSkipped, "the orchestrator is off (--no-orchestrator)"
		p.journal(ctx, journal.KindSkip, "Poke now skipped: "+result.Reason, result)
	}
	return result
}

// ---- Observing ---------------------------------------------------------------------------

// observe refreshes the agents map from agent.list. Since is kept while an agent's state and
// seq stay the same, so the holds measure how long it has really been settled.
func (p *Poker) observe(ctx context.Context, now time.Time) {
	callCtx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
	agents, err := p.opts.Client.AgentList(callCtx)
	cancel()
	if err != nil {
		p.opts.Logger.Debug("poker could not list agents", "err", err)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := make(map[string]AgentSeen, len(agents))
	for _, agent := range agents {
		next := AgentSeen{PaneID: agent.PaneID, Name: agent.Name, Status: agent.AgentStatus, Seq: agent.StateChangeSeq, Since: now}
		if previous, ok := p.agents[agent.PaneID]; ok && previous.Status == next.Status && previous.Seq == next.Seq && previous.Name == next.Name {
			next.Since = previous.Since
		}
		seen[agent.PaneID] = next
	}
	p.agents = seen
}

// dueTasks asks the task source what is due, as reasons keyed task:<name>.
func (p *Poker) dueTasks(ctx context.Context, now time.Time) (tasks, rideAlong []Reason) {
	if p.opts.Tasks == nil {
		return nil, nil
	}
	for _, task := range p.opts.Tasks.DueTasks(ctx, now) {
		reason := Reason{Key: "task:" + task.Name, Source: SourceTask, Text: task.Text, At: now}
		if task.RideAlong {
			rideAlong = append(rideAlong, reason)
		} else {
			tasks = append(tasks, reason)
		}
	}
	return tasks, rideAlong
}

func (p *Poker) precheckInput(now time.Time, manual bool) PrecheckInput {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadLocked(context.Background())
	agents := make([]AgentSeen, 0, len(p.agents))
	for _, agent := range p.agents {
		agents = append(agents, agent)
	}
	pushed := make(map[string]string, len(p.state.Pushed))
	for pane, mark := range p.state.Pushed {
		pushed[pane] = mark
	}
	return PrecheckInput{
		Now: now, OrchestratorPane: p.opts.Status().PaneID, Agents: agents, Pushed: pushed,
		LastAgentPush: unixTime(p.state.LastAgentPush), Pending: slices.Clone(p.pending), Manual: manual,
	}
}

// timerReason queues the standing picture at each timer tick when it changed since the last
// timer poke (DESIGN.md 7.4: the timer costs nothing when nothing changed).
func (p *Poker) timerReason(ctx context.Context, now time.Time) {
	if p.opts.Interval <= 0 || p.opts.Standing == nil {
		return
	}
	p.mu.Lock()
	due := !p.lastTimer.IsZero() && now.Sub(p.lastTimer) >= p.opts.Interval
	if p.lastTimer.IsZero() || due {
		p.lastTimer = now
	}
	p.mu.Unlock()
	if !due {
		return
	}
	lines := p.opts.Standing(ctx)
	if len(lines) == 0 {
		return
	}
	slices.Sort(lines)
	fingerprint := fingerprintOf(lines)
	p.mu.Lock()
	p.loadLocked(ctx)
	unchanged := p.state.Timer == fingerprint
	p.mu.Unlock()
	if unchanged {
		return
	}
	p.Add(ctx, Reason{Key: "timer:" + fingerprint, Source: SourceTimer, Text: "still open: " + strings.Join(lines, "; ")})
}

func fingerprintOf(lines []string) string {
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:8])
}

// ---- Gate and send -----------------------------------------------------------------------

// gate reads the orchestrator's screen (only when it is idle: otherwise the state decides) and
// applies Gate, tracking how long a draft has been there.
func (p *Poker) gate(ctx context.Context, now time.Time, status Status) GateDecision {
	input := GateInput{Now: now, State: status.State}
	if status.State == StateIdle && status.PaneID != "" {
		screen, err := readScreen(ctx, p.opts.Client, status.PaneID)
		input.ScreenRead = err == nil
		input.Dialog, input.Draft = screen.dialog.Present, screen.draft.Present
	}
	p.mu.Lock()
	if input.Draft {
		if p.draftSince.IsZero() {
			p.draftSince = now
		}
	} else if input.ScreenRead {
		p.draftSince, p.alerted = time.Time{}, false
	}
	input.DraftSince = p.draftSince
	p.mu.Unlock()
	return Gate(input)
}

// skipped journals a gated poke (once per refusal, every time for a manual one) and raises the
// draft alert once per draft.
func (p *Poker) skipped(ctx context.Context, result PokeResult, decision GateDecision) PokeResult {
	result.Outcome, result.Reason = OutcomeSkipped, decision.Reason
	p.mu.Lock()
	repeat := p.lastSkip == decision.Reason
	p.lastSkip = decision.Reason
	alert := decision.Alert && !p.alerted
	if alert {
		p.alerted = true
	}
	p.mu.Unlock()
	if !repeat || result.Manual {
		prefix := "Poke held: "
		if result.Manual {
			prefix = "Poke now skipped: "
		}
		p.journal(ctx, journal.KindSkip, prefix+decision.Reason, result)
		p.publish(result)
	}
	if alert {
		text := "crowsnest: a draft in the orchestrator's input box has held a poke back for over 2 hours; clear or send it at the terminal"
		p.journal(ctx, journal.KindAlert, text, nil)
		if p.opts.Alert != nil {
			p.opts.Alert(text)
		}
	}
	return result
}

// send types the poke. A send that provably typed nothing (a dialog or draft appeared, the
// agent vanished) keeps the reasons pending; anything else marks them delivered, because a
// second copy of a poke costs a turn and says the same thing twice.
func (p *Poker) send(ctx context.Context, result PokeResult, status Status, due PrecheckResult) PokeResult {
	talk := p.opts.Talk != nil && p.opts.Talk()
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pokeSendTimeout)
	defer cancel()
	sent, err := Prompt(sendCtx, p.opts.Client, status.PaneID, PromptRequest{Text: PokeText(result.At, talk, due.Reasons)})
	var blocked *BlockedError
	var draft *DraftError
	if errors.Is(err, ErrNotTyped) || errors.As(err, &blocked) || errors.As(err, &draft) {
		reason := "the orchestrator could not take it just now"
		switch {
		case blocked != nil:
			reason = "a dialog came up on the orchestrator's screen"
		case draft != nil:
			reason = "someone has typed a draft into the orchestrator's input box at the terminal"
		}
		return p.skipped(ctx, result, GateDecision{Verdict: GateWait, Reason: reason})
	}
	if err != nil {
		p.opts.Logger.Warn("poke failed", "err", err)
		result.Outcome, result.Reason = OutcomeSkipped, "herdr refused the poke; it will be tried again"
		p.journal(ctx, journal.KindSkip, "Poke failed: herdr refused it; it will be tried again", result)
		p.publish(result)
		return result
	}
	result.Outcome, result.Started, result.Note = OutcomeSent, sent.Started, sent.Note
	p.delivered(ctx, result.At, due)
	summary := "Poked the orchestrator: " + strings.Join(result.Reasons, "; ")
	if result.Manual {
		summary = "Poke now: " + strings.Join(result.Reasons, "; ")
	}
	p.journal(ctx, journal.KindPoke, summary, result)
	p.publish(result)
	return result
}

// delivered records what a typed poke covered, in memory and in kv.
func (p *Poker) delivered(ctx context.Context, at time.Time, due PrecheckResult) {
	p.mu.Lock()
	defer p.mu.Unlock()
	keys := keysOf(due.Reasons)
	p.pending = slices.DeleteFunc(p.pending, func(r Reason) bool { _, found := slices.BinarySearch(keys, r.Key); return found })
	for _, reason := range due.Reasons {
		switch reason.Source {
		case SourceAgent, SourceTask: // tasks are stamped by their source
		case SourceTimer:
			p.state.Timer = strings.TrimPrefix(reason.Key, "timer:")
		default:
			p.state.Delivered[reason.Key] = at.Unix()
		}
	}
	for pane, mark := range due.Agents {
		p.state.Pushed[pane] = mark
	}
	if len(due.Agents) > 0 {
		p.state.LastAgentPush = at.Unix()
	}
	p.lastSkip, p.draftSince, p.alerted = "", time.Time{}, false
	p.pruneLocked(at)
	p.saveLocked(ctx)
	if names := taskNames(due.Reasons); len(names) > 0 && p.opts.Tasks != nil {
		// Under p.mu: the task source takes only its own lock and never calls back.
		p.opts.Tasks.Delivered(context.WithoutCancel(ctx), at, names)
	}
}

// taskNames are the scheduled tasks among reasons.
func taskNames(reasons []Reason) []string {
	var names []string
	for _, reason := range reasons {
		if reason.Source == SourceTask {
			names = append(names, strings.TrimPrefix(reason.Key, "task:"))
		}
	}
	return names
}

// ---- Bookkeeping -------------------------------------------------------------------------

// loadLocked reads the kv blob once. A missing or unreadable blob starts empty: the cost is at
// most one repeated push per agent.
func (p *Poker) loadLocked(ctx context.Context) {
	if p.loaded {
		return
	}
	p.loaded = true
	raw, err := p.opts.KV.GetKV(ctx, kvPokeState)
	if err != nil {
		return
	}
	var state pokeState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		p.opts.Logger.Warn("ignoring unreadable poke bookkeeping", "err", err)
		return
	}
	if state.Pushed == nil {
		state.Pushed = map[string]string{}
	}
	if state.Delivered == nil {
		state.Delivered = map[string]int64{}
	}
	for _, reason := range state.Pending {
		_, delivered := state.Delivered[reason.Key]
		if !delivered && !slices.ContainsFunc(p.pending, func(r Reason) bool { return r.Key == reason.Key }) {
			p.pending = append(p.pending, reason)
		}
	}
	state.Pending = nil
	p.state = state
}

// durable reports a reason that only an event produced, which nothing would rebuild.
func durable(reason Reason) bool {
	return reason.Source != SourceAgent && reason.Source != SourceTimer && reason.Source != SourceTask
}

func (p *Poker) saveLocked(ctx context.Context) {
	p.state.Pending = slices.DeleteFunc(slices.Clone(p.pending), func(r Reason) bool { return !durable(r) })
	body, err := json.Marshal(p.state)
	if err != nil {
		return
	}
	if err := p.opts.KV.SetKV(context.WithoutCancel(ctx), kvPokeState, string(body)); err != nil {
		p.opts.Logger.Warn("storing poke bookkeeping", "err", err)
	}
}

// pruneLocked forgets delivered keys older than deliveredKeep, and the oldest past maxDelivered.
func (p *Poker) pruneLocked(now time.Time) {
	cutoff := now.Add(-deliveredKeep).Unix()
	for key, at := range p.state.Delivered {
		if at < cutoff {
			delete(p.state.Delivered, key)
		}
	}
	for len(p.state.Delivered) > maxDelivered {
		oldest, oldestAt := "", int64(0)
		for key, at := range p.state.Delivered {
			if oldest == "" || at < oldestAt {
				oldest, oldestAt = key, at
			}
		}
		delete(p.state.Delivered, oldest)
	}
}

func (p *Poker) journal(ctx context.Context, kind journal.Kind, summary string, result any) {
	var detail map[string]any
	if result != nil {
		body, _ := json.Marshal(result)
		_ = json.Unmarshal(body, &detail)
	}
	p.opts.Journal.Add(ctx, kind, summary, detail)
}

func (p *Poker) publish(result PokeResult) {
	if p.opts.Hub != nil {
		p.opts.Hub.Publish(hub.Event{Type: "poke", Data: result})
	}
}

func unixTime(seconds int64) time.Time {
	if seconds == 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0)
}
