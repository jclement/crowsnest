// Keeping the orchestrator's context small (DESIGN.md 7.9): the pure half. A long-lived
// orchestrator poked all day grows to hundreds of thousands of tokens, and every turn re-reads
// all of them. Two remedies, both taken from claude-agent's front door (bin/frontdoor-poke):
//
//   - Compaction: once the context passes a threshold (--compact-at), and the orchestrator is
//     idle with an empty input box, type `/compact <focus>`, where the focus says what to keep.
//   - A daily new conversation (--fresh-at HH:MM, off by default): once the time has passed and
//     the human has been quiet a while, first ask the orchestrator to write a hand-over file,
//     then on a later check start it on a fresh conversation whose first context includes that
//     file. Repeated summaries leave sediment; a fresh start clears it.
//
// Both decisions are pure functions of what tender.go observed, so each rule is a table row.

package orch

import (
	"fmt"
	"time"

	"github.com/jclement/crowsnest/internal/transcript"
)

const (
	// DefaultCompactAt is the context size, in tokens, past which the orchestrator is compacted.
	DefaultCompactAt = 150_000
	// FreshQuiet is how long the human must have been silent before the daily new conversation:
	// someone mid-conversation should not see it vanish.
	FreshQuiet = 20 * time.Minute
	// handoffSettle is the least time between the hand-over prompt and the restart, so the
	// hand-over turn has really ended and not merely paused between tool calls.
	handoffSettle = 30 * time.Second
)

// CompactFocus is what /compact is told to keep: the orchestrator's work in flight and the
// contract it answers with.
const CompactFocus = "Keep: every open job with its id and state, questions asked and not yet answered, " +
	"anything the human asked for that is not finished, standing instructions the human gave, " +
	"decisions made today, and the reply contract (SAY: lines when talk is on, noop for a poke with nothing due). " +
	"Drop: finished poke results and work already reported."

// HandoffPrompt asks the orchestrator to write what its next conversation needs. It starts with
// a crowsnest header so it never counts as the human speaking.
func HandoffPrompt(file string) string {
	return "[crowsnest upkeep] This conversation will be closed shortly and you will start on a new one. " +
		"Write what the next conversation needs to " + file + " (replace the file): open jobs and their states, " +
		"questions waiting for an answer, standing instructions from the human, and anything you are tracking only " +
		"in this conversation. It will be given to you when the new conversation starts. Do nothing else, " +
		"do not message the human, and reply noop when done."
}

// Readiness is whether something may be typed into the orchestrator now: idle, its screen read,
// no dialog and an empty input box.
type Readiness struct {
	Idle, ScreenRead, Dialog, Draft bool
}

// Ready reports whether all of it holds.
func (r Readiness) Ready() bool { return r.Idle && r.ScreenRead && !r.Dialog && !r.Draft }

// CompactAttempt is the last /compact crowsnest typed: when, and at what size.
type CompactAttempt struct {
	At     time.Time `json:"at,omitzero"`
	Tokens int       `json:"tokens,omitempty"`
}

// CompactInput is what the compaction decision reads.
type CompactInput struct {
	Threshold int // tokens; 0 disables
	Usage     transcript.Usage
	Last      CompactAttempt
	Readiness Readiness
	// HeldJobBusy is true while crowsnest is in the middle of a step on a job the orchestrator
	// holds (typing its brief, a nudge or a retry): a /compact typed then would interleave.
	HeldJobBusy bool
}

// CompactDecision is whether to compact now, and why not in words.
type CompactDecision struct {
	Compact bool
	// Wait is true when compaction is due but the orchestrator cannot take it yet.
	Wait   bool
	Reason string
}

// DecideCompact applies the rules (pure): over the threshold on a known reading; not again
// without growth since the last attempt (unless a compaction happened since, which settles it);
// only into a ready orchestrator; never while a held job's step is in flight.
func DecideCompact(in CompactInput) CompactDecision {
	switch {
	case in.Threshold <= 0:
		return CompactDecision{Reason: "compaction is off (--compact-at 0)"}
	case !in.Usage.Known:
		return CompactDecision{Reason: "the context size is not known yet"}
	case in.Usage.Tokens < in.Threshold:
		return CompactDecision{Reason: fmt.Sprintf("the context is %d tokens, under %d", in.Usage.Tokens, in.Threshold)}
	case !in.Last.At.IsZero() && !in.Usage.CompactedAt.After(in.Last.At) && in.Usage.Tokens <= in.Last.Tokens:
		return CompactDecision{Reason: "no growth since the last /compact, which has not taken effect"}
	case !in.Readiness.Ready():
		return CompactDecision{Wait: true, Reason: "the orchestrator is busy or its input box is not empty"}
	case in.HeldJobBusy:
		return CompactDecision{Wait: true, Reason: "a step of a job the orchestrator holds is in flight"}
	}
	return CompactDecision{Compact: true}
}

// FreshStep is what the daily new conversation does at a check.
type FreshStep string

// Fresh steps.
const (
	FreshNone      FreshStep = ""
	FreshMarkToday FreshStep = "mark"    // first run on this install: count today as done, do nothing
	FreshHandoff   FreshStep = "handoff" // ask for the hand-over file
	FreshRestart   FreshStep = "restart" // exit and start a new conversation
)

// FreshInput is what the daily decision reads.
type FreshInput struct {
	Now      time.Time
	Location *time.Location
	// AtMinutes is --fresh-at as minutes after local midnight; negative disables.
	AtMinutes int
	// DoneDay is the local date (2006-01-02) of the last fresh start, "" if never.
	DoneDay string
	// HandoffAt is when the hand-over prompt was last typed (zero: never).
	HandoffAt     time.Time
	HasTranscript bool // a conversation with nothing in it needs no hand-over
	Usage         transcript.Usage
	Readiness     Readiness
}

// DecideFresh applies the daily rules (pure).
func DecideFresh(in FreshInput) FreshStep {
	if in.AtMinutes < 0 {
		return FreshNone
	}
	loc := in.Location
	if loc == nil {
		loc = time.Local
	}
	local := in.Now.In(loc)
	today := local.Format(time.DateOnly)
	switch {
	case local.Hour()*60+local.Minute() < in.AtMinutes, in.DoneDay == today:
		return FreshNone
	case in.DoneDay == "":
		return FreshMarkToday // installing or turning it on must not restart the session at once
	case !in.Readiness.Ready():
		return FreshNone
	case !in.Usage.HumanAt.IsZero() && in.Now.Sub(in.Usage.HumanAt) < FreshQuiet:
		return FreshNone
	}
	handedOver := !in.HandoffAt.IsZero() && in.HandoffAt.In(loc).Format(time.DateOnly) == today
	if in.HasTranscript && !handedOver {
		return FreshHandoff
	}
	if handedOver && in.Now.Sub(in.HandoffAt) < handoffSettle {
		return FreshNone
	}
	return FreshRestart
}
