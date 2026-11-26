// Pokes (DESIGN.md 7.4): the pure half. A poke is a message typed into the orchestrator telling
// it what changed while it was not looking: another agent settled into done or blocked, a
// question it did not ask was settled, a job finished or got stuck, the standing picture changed
// since the last timer poke, or the human pressed "Poke now". The precheck turns what the poker
// observed into the list of reasons that are due; nothing due means no poke and no tokens spent.
// The gate decides whether the orchestrator can take one right now. Both are pure functions of
// their input (time included), so every trigger, hold and refusal is a table row in poke_test.go;
// poker.go does the observing, the bookkeeping and the typing.

package orch

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/termtext"
)

const (
	// DoneHold is how long another agent must stay done before it is reported: an agent that
	// finishes a turn and is given the next one within seconds is not news.
	DoneHold = 45 * time.Second
	// BlockedHold is the same for a dialog. Shorter, because a dialog is a stopped agent.
	BlockedHold = 10 * time.Second
	// AgentPushInterval is the least time between two pokes carrying agent-settled reasons, so a
	// fleet finishing one by one does not cost one orchestrator turn per agent.
	AgentPushInterval = 120 * time.Second
	// DraftAlertAfter is how long a draft in the orchestrator's input box may hold a due poke
	// back before the human is told (someone started typing at the terminal and walked away).
	DraftAlertAfter = 2 * time.Hour
)

// ReasonSource says where a reason came from; it is journalled.
type ReasonSource string

// Reason sources.
const (
	SourceAgent    ReasonSource = "agent"    // another agent settled into done or blocked
	SourceQuestion ReasonSource = "question" // a question the orchestrator did not ask was settled
	SourceJob      ReasonSource = "job"      // a job reached a terminal or stuck state
	SourceTimer    ReasonSource = "timer"    // the standing picture changed since the last timer poke
	SourceManual   ReasonSource = "manual"   // added by hand (tests, the opt-in Claude smoke)
)

// Reason is one line of a poke. Key deduplicates: the same key pending twice is one line.
type Reason struct {
	Key    string       `json:"key"`
	Source ReasonSource `json:"source"`
	Text   string       `json:"text"` // one line, without the leading "- "
	At     time.Time    `json:"at"`
}

// AgentSeen is one other agent as the poker last observed it.
type AgentSeen struct {
	PaneID string
	Name   string
	Status herdr.AgentStatus
	Seq    uint64    // herdr's state_change_seq: one value per settled state
	Since  time.Time // when the poker first saw it in this state at this seq
}

// PrecheckInput is everything the precheck reads.
type PrecheckInput struct {
	Now time.Time
	// OrchestratorPane is excluded from the agent reasons: the orchestrator settling is not news
	// to itself.
	OrchestratorPane string
	Agents           []AgentSeen
	// Pushed maps a pane to the "name:seq" last reported for it (kept in kv across restarts), so
	// each settled state is pushed once.
	Pushed map[string]string
	// LastAgentPush is when a poke last carried agent reasons (zero: never).
	LastAgentPush time.Time
	// Pending are reasons from events (questions, jobs, the timer, manual) not yet delivered.
	Pending []Reason
	// Manual is a "Poke now": it skips the agent push interval (the holds still apply).
	Manual bool
}

// PrecheckResult is the reasons due now and the agent states they report, to be marked pushed
// once the poke is typed.
type PrecheckResult struct {
	Reasons []Reason
	Agents  map[string]string // pane → "name:seq" covered by Reasons
	// AgentsHeld counts agents whose state is due but held back by AgentPushInterval.
	AgentsHeld int
}

// Precheck builds the reason list (pure). Pending event reasons are always due; another agent's
// settled state is due once it has held long enough, unless that state was already pushed or an
// agent push happened less than AgentPushInterval ago (a manual poke skips that last rule).
func Precheck(input PrecheckInput) PrecheckResult {
	result := PrecheckResult{Reasons: dedupe(input.Pending), Agents: map[string]string{}}
	var settled []Reason
	for _, agent := range input.Agents {
		reason, mark, ok := agentReason(input, agent)
		if !ok {
			continue
		}
		settled = append(settled, reason)
		result.Agents[agent.PaneID] = mark
	}
	if len(settled) == 0 {
		return result
	}
	if !input.Manual && !input.LastAgentPush.IsZero() && input.Now.Sub(input.LastAgentPush) < AgentPushInterval {
		result.AgentsHeld = len(settled)
		result.Agents = map[string]string{}
		return result
	}
	sort.Slice(settled, func(i, j int) bool { return settled[i].Key < settled[j].Key })
	result.Reasons = append(result.Reasons, settled...)
	return result
}

// agentReason is the reason for one agent, if its state is due.
func agentReason(input PrecheckInput, agent AgentSeen) (Reason, string, bool) {
	if agent.PaneID == "" || agent.PaneID == input.OrchestratorPane || agent.Since.IsZero() {
		return Reason{}, "", false
	}
	var hold time.Duration
	var what string
	switch agent.Status {
	case herdr.StatusDone:
		hold, what = DoneHold, "finished its turn (done)"
	case herdr.StatusBlocked:
		hold, what = BlockedHold, "is blocked at a dialog"
	default:
		return Reason{}, "", false
	}
	mark := fmt.Sprintf("%s:%d", agent.Name, agent.Seq)
	held := input.Now.Sub(agent.Since)
	if held < hold || input.Pushed[agent.PaneID] == mark {
		return Reason{}, "", false
	}
	name := "an unnamed agent"
	if agent.Name != "" {
		name = termtext.Quote(agent.Name) // herdr names can be set from inside a pane
	}
	text := fmt.Sprintf("%s (pane %s) %s, for %s", name, agent.PaneID, what, roundHeld(held))
	return Reason{Key: "agent:" + agent.PaneID + ":" + mark, Source: SourceAgent, Text: text, At: agent.Since}, mark, true
}

// roundHeld says how long in words a phone reader takes in at a glance.
func roundHeld(held time.Duration) string {
	if held < time.Minute {
		return fmt.Sprintf("%ds", int(held.Seconds()))
	}
	return fmt.Sprintf("%d min", int(held.Minutes()))
}

// dedupe keeps the first reason of each key, in order.
func dedupe(reasons []Reason) []Reason {
	out := make([]Reason, 0, len(reasons))
	seen := map[string]bool{}
	for _, reason := range reasons {
		if seen[reason.Key] {
			continue
		}
		seen[reason.Key] = true
		out = append(out, reason)
	}
	return out
}

// ---- Gate ------------------------------------------------------------------------------

// GateInput is what the gate reads: the orchestrator's state and its input box.
type GateInput struct {
	Now   time.Time
	State State
	// Dialog is a dialog detected on the screen (herdr sometimes reports one as idle or done).
	Dialog bool
	// Draft is text typed at the terminal in the input box; DraftSince is when the poker first
	// saw it (zero when there is none).
	Draft      bool
	DraftSince time.Time
	// ScreenRead is false when the screen could not be read; the gate then refuses, because a
	// poke typed over an unseen draft or into an unseen dialog cannot be taken back.
	ScreenRead bool
}

// GateVerdict is the gate's answer.
type GateVerdict string

// Gate verdicts.
const (
	GateSend     GateVerdict = "send"      // idle or done with an empty input box
	GateWait     GateVerdict = "wait"      // keep the reasons and try again later
	GateNeedsYou GateVerdict = "needs_you" // a dialog is up: the Inbox shows it; no poke
)

// GateDecision is the verdict with the reason in words for the journal and the toast.
type GateDecision struct {
	Verdict GateVerdict
	Reason  string
	// Alert is set when a draft has held a due poke back for longer than DraftAlertAfter.
	Alert bool
}

// Gate decides whether a poke can be typed now (pure): only into an idle (or done) orchestrator
// whose input box is empty and that shows no dialog.
func Gate(input GateInput) GateDecision {
	switch input.State {
	case StateWorking:
		return GateDecision{Verdict: GateWait, Reason: "the orchestrator is working; the poke waits for its turn to end"}
	case StateBlocked, StateNeedsTerminal:
		return GateDecision{Verdict: GateNeedsYou, Reason: "the orchestrator is waiting at a dialog; answer it in the Inbox or its terminal"}
	case StateIdle:
	default:
		return GateDecision{Verdict: GateWait, Reason: "the orchestrator is " + strings.ReplaceAll(string(input.State), "_", " ")}
	}
	switch {
	case !input.ScreenRead:
		return GateDecision{Verdict: GateWait, Reason: "the orchestrator's screen could not be read"}
	case input.Dialog:
		return GateDecision{Verdict: GateNeedsYou, Reason: "a dialog is on the orchestrator's screen; answer it in the Inbox or its terminal"}
	case input.Draft:
		decision := GateDecision{Verdict: GateWait, Reason: "someone has typed a draft into the orchestrator's input box at the terminal"}
		decision.Alert = !input.DraftSince.IsZero() && input.Now.Sub(input.DraftSince) >= DraftAlertAfter
		return decision
	}
	return GateDecision{Verdict: GateSend}
}

// ---- Text ------------------------------------------------------------------------------

// PokeHeader is the first line of a poke: `[crowsnest poke <RFC3339>]`. It carried a `talk=on|off`
// flag until the orchestrator was told to write a SAY line on every reply (DESIGN.md 13.2), which
// left nothing for the flag to switch.
func PokeHeader(at time.Time) string {
	return fmt.Sprintf("[crowsnest poke %s]", at.UTC().Format(time.RFC3339))
}

// PokeText is the whole poke: the header, then one line per reason.
func PokeText(at time.Time, reasons []Reason) string {
	lines := []string{PokeHeader(at)}
	for _, reason := range reasons {
		lines = append(lines, "- "+oneLine(reason.Text))
	}
	return strings.Join(lines, "\n")
}

// ReasonTexts are the reasons' lines, for results and the journal.
func ReasonTexts(reasons []Reason) []string {
	out := make([]string, len(reasons))
	for i, reason := range reasons {
		out[i] = reason.Text
	}
	return out
}

// maxReasonRunes keeps one reason to a line a phone shows whole.
const maxReasonRunes = 240

// oneLine makes a reason safe to type (internal/termtext: one line, no escape sequences,
// control, format or private-use characters, so a title with a newline cannot forge a second
// reason line) and caps its length.
func oneLine(text string) string {
	return termtext.Cap(termtext.Clean(text), maxReasonRunes)
}

// keysOf lists reason keys, for removing delivered reasons from the pending set.
func keysOf(reasons []Reason) []string {
	keys := make([]string, len(reasons))
	for i, reason := range reasons {
		keys[i] = reason.Key
	}
	slices.Sort(keys)
	return keys
}
