// Keeping the orchestrator's context small (DESIGN.md 7.9): the runtime half. The Tender runs at
// the start of every poke check (it shares the orchestrator's input box with the pokes, so they
// must not interleave): it reads the context size the transcript tailer already parsed, decides
// with compact.go's pure rules, and types `/compact <focus>` or the hand-over prompt, or starts
// the new conversation. Its bookkeeping (the last attempt, the day of the last fresh start, the
// hand-over time) survives restarts in kv.

package orch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/transcript"
)

// kvUpkeep is the kv key of the tender's bookkeeping.
const kvUpkeep = "upkeep.state"

// upkeepState is that bookkeeping.
type upkeepState struct {
	Compact   CompactAttempt `json:"compact"`
	FreshDay  string         `json:"fresh_day,omitempty"`
	HandoffAt time.Time      `json:"handoff_at,omitzero"`
}

// TenderOptions configure a Tender. Client, KV and Usage are required.
type TenderOptions struct {
	Client  herdr.Client
	KV      KV
	Journal *journal.Journal
	Logger  *slog.Logger
	// Usage is the orchestrator's context as the transcript tailer last read it.
	Usage func(ctx context.Context) (transcript.Usage, error)
	// CompactAt is the threshold in tokens (--compact-at); 0 disables compaction.
	CompactAt int
	// FreshAt is --fresh-at as minutes after local midnight; negative disables it.
	FreshAt  int
	Location *time.Location // nil: time.Local
	// HandoffFile is where the orchestrator writes its hand-over (<state>/handoff.md).
	HandoffFile string
	// HeldJobBusy reports a step in flight on a job the orchestrator holds. Nil: never.
	HeldJobBusy func(ctx context.Context) bool
	// NewConversation starts the orchestrator on a fresh session (Orchestrator.NewConversation).
	NewConversation func(ctx context.Context) (Status, error)
	Now             func() time.Time
}

// Tender keeps the orchestrator compact. Build it with NewTender.
type Tender struct {
	opts TenderOptions

	mu     sync.Mutex
	loaded bool
	state  upkeepState
}

// NewTender returns a tender; nothing runs until Tend.
func NewTender(opts TenderOptions) *Tender {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Location == nil {
		opts.Location = time.Local
	}
	return &Tender{opts: opts}
}

// Tend runs one check and reports whether it typed into the orchestrator (or restarted it), in
// which case the poke waits for the next check.
func (t *Tender) Tend(ctx context.Context, now time.Time, status Status) bool {
	if status.State != StateIdle || status.PaneID == "" {
		return false
	}
	usage, err := t.opts.Usage(ctx)
	if err != nil {
		return false
	}
	state := t.load(ctx)
	freshPossible := t.opts.FreshAt >= 0 && state.FreshDay != now.In(t.opts.Location).Format(time.DateOnly)
	compactPossible := t.opts.CompactAt > 0 && usage.Known && usage.Tokens >= t.opts.CompactAt
	if !freshPossible && !compactPossible {
		return false // the common case: no screen read at all
	}
	readiness := t.readiness(ctx, status)
	if freshPossible {
		step := DecideFresh(FreshInput{
			Now: now, Location: t.opts.Location, AtMinutes: t.opts.FreshAt, DoneDay: state.FreshDay,
			HandoffAt: state.HandoffAt, HasTranscript: usage.Known || !usage.At.IsZero(), Usage: usage, Readiness: readiness,
		})
		if step != FreshNone {
			return t.fresh(ctx, now, status, step)
		}
	}
	decision := DecideCompact(CompactInput{
		Threshold: t.opts.CompactAt, Usage: usage, Last: state.Compact, Readiness: readiness, HeldJobBusy: t.heldJobBusy(ctx),
	})
	if !decision.Compact {
		return false
	}
	return t.compact(ctx, now, status, usage, "automatic")
}

func (t *Tender) heldJobBusy(ctx context.Context) bool {
	return t.opts.HeldJobBusy != nil && t.opts.HeldJobBusy(ctx)
}

// readiness reads the orchestrator's screen for a dialog or a draft.
func (t *Tender) readiness(ctx context.Context, status Status) Readiness {
	screen, err := readScreen(ctx, t.opts.Client, status.PaneID)
	return Readiness{Idle: status.State == StateIdle, ScreenRead: err == nil, Dialog: screen.dialog.Present, Draft: screen.draft.Present}
}

// ErrNotReady is CompactNow's answer when the orchestrator cannot take a command now.
var ErrNotReady = errors.New("the orchestrator is not idle with an empty input box")

// CompactNow is the menu's "Compact now": /compact at any size, still only into a ready
// orchestrator.
func (t *Tender) CompactNow(ctx context.Context, status Status) error {
	if status.State != StateIdle || !t.readiness(ctx, status).Ready() {
		return ErrNotReady
	}
	usage, _ := t.opts.Usage(ctx)
	if !t.compact(ctx, t.opts.Now(), status, usage, "manual") {
		return ErrNotReady
	}
	return nil
}

// compact types /compact with the focus and records the attempt.
func (t *Tender) compact(ctx context.Context, now time.Time, status Status, usage transcript.Usage, how string) bool {
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pokeSendTimeout)
	defer cancel()
	result, err := Prompt(sendCtx, t.opts.Client, status.PaneID, PromptRequest{Text: "/compact " + CompactFocus, ExpectAgent: Name})
	if err != nil && provablyNotTyped(err) {
		return false
	}
	t.update(ctx, func(s *upkeepState) { s.Compact = CompactAttempt{At: now, Tokens: usage.Tokens} })
	summary := fmt.Sprintf("Compacted the orchestrator (%s) at %s tokens", how, formatTokens(usage))
	if err != nil {
		summary = fmt.Sprintf("Tried to compact the orchestrator (%s) at %s tokens; herdr did not confirm it", how, formatTokens(usage))
		t.opts.Logger.Warn("typing /compact", "err", err)
	} else if !result.Started {
		summary += "; it did not visibly start"
	}
	t.opts.Journal.Add(ctx, journal.KindUpkeep, summary, map[string]any{"tokens": usage.Tokens, "threshold": t.opts.CompactAt, "how": how, "started": result.Started})
	return true
}

func formatTokens(usage transcript.Usage) string {
	if !usage.Known {
		return "an unknown number of"
	}
	return fmt.Sprintf("%d", usage.Tokens)
}

// fresh carries out one step of the daily new conversation.
func (t *Tender) fresh(ctx context.Context, now time.Time, status Status, step FreshStep) bool {
	today := now.In(t.opts.Location).Format(time.DateOnly)
	switch step {
	case FreshMarkToday:
		t.update(ctx, func(s *upkeepState) { s.FreshDay = today })
		return false
	case FreshHandoff:
		sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pokeSendTimeout)
		defer cancel()
		_, err := Prompt(sendCtx, t.opts.Client, status.PaneID, PromptRequest{Text: HandoffPrompt(t.opts.HandoffFile), ExpectAgent: Name})
		if err != nil && provablyNotTyped(err) {
			return false
		}
		t.update(ctx, func(s *upkeepState) { s.HandoffAt = now })
		t.opts.Journal.Add(ctx, journal.KindUpkeep, "Asked the orchestrator to write its hand-over before the daily new conversation", map[string]any{"file": t.opts.HandoffFile})
		return true
	case FreshRestart:
		if t.opts.NewConversation == nil {
			return false
		}
		t.update(ctx, func(s *upkeepState) { s.FreshDay = today }) // before: a failed restart is not retried all day
		newStatus, err := t.opts.NewConversation(ctx)
		summary := "Started the orchestrator on its daily new conversation"
		if t.handoffWritten() {
			summary += "; the hand-over is in its first context"
		}
		if err != nil {
			summary = "The daily new conversation did not start: " + newStatus.Detail
		}
		t.opts.Journal.Add(ctx, journal.KindUpkeep, summary, map[string]any{"session_id": newStatus.SessionID})
		return true
	}
	return false
}

// handoffWritten reports whether the hand-over file was written since the prompt asked for it.
func (t *Tender) handoffWritten() bool {
	info, err := os.Stat(t.opts.HandoffFile)
	if err != nil {
		return false
	}
	state := t.load(context.Background())
	return !state.HandoffAt.IsZero() && !info.ModTime().Before(state.HandoffAt.Add(-time.Second))
}

// provablyNotTyped is a send that typed nothing: a dialog or draft appeared, or the agent moved.
func provablyNotTyped(err error) bool {
	var blocked *BlockedError
	var draft *DraftError
	return errors.Is(err, ErrNotTyped) || errors.As(err, &blocked) || errors.As(err, &draft)
}

// ---- The menu's view -------------------------------------------------------------------------

// ContextInfo is GET /orchestrator/context: the size of the orchestrator's context and the
// upkeep around it.
type ContextInfo struct {
	transcript.Usage
	Threshold     int       `json:"threshold"` // --compact-at; 0: off
	LastCompactAt time.Time `json:"last_compact_at,omitzero"`
	FreshAt       string    `json:"fresh_at,omitempty"` // HH:MM, empty when off
	FreshDay      string    `json:"fresh_day,omitempty"`
}

// Info is the context size and upkeep state for the Chat menu.
func (t *Tender) Info(ctx context.Context) ContextInfo {
	usage, _ := t.opts.Usage(ctx)
	state := t.load(ctx)
	info := ContextInfo{Usage: usage, Threshold: t.opts.CompactAt, LastCompactAt: state.Compact.At, FreshDay: state.FreshDay}
	if t.opts.FreshAt >= 0 {
		info.FreshAt = fmt.Sprintf("%02d:%02d", t.opts.FreshAt/60, t.opts.FreshAt%60)
	}
	return info
}

// ---- Bookkeeping -----------------------------------------------------------------------------

func (t *Tender) load(ctx context.Context) upkeepState {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.loaded {
		t.loaded = true
		if raw, err := t.opts.KV.GetKV(ctx, kvUpkeep); err == nil {
			if err := json.Unmarshal([]byte(raw), &t.state); err != nil {
				t.opts.Logger.Warn("ignoring unreadable upkeep bookkeeping", "err", err)
			}
		}
	}
	return t.state
}

func (t *Tender) update(ctx context.Context, change func(*upkeepState)) {
	t.load(ctx)
	t.mu.Lock()
	defer t.mu.Unlock()
	change(&t.state)
	body, err := json.Marshal(t.state)
	if err != nil {
		return
	}
	if err := t.opts.KV.SetKV(context.WithoutCancel(ctx), kvUpkeep, string(body)); err != nil {
		t.opts.Logger.Warn("storing upkeep bookkeeping", "err", err)
	}
}
