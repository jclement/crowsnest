// The send path (DESIGN.md 7.3): prompting an agent from the web app. It works for any agent,
// the orchestrator included.
//
// Before typing anything it looks at the agent: a dialog on screen (herdr's blocked state, or
// one detected from the screen) refuses the send, because the text would land in the dialog; a
// draft typed at the terminal refuses it unless the caller says to append anyway, because
// agent.prompt appends to whatever is in the input box. After typing it waits for the turn to
// start, since a submission alone does not prove Claude read it. Sending while the agent works is
// allowed: Claude queues the input, and the result says so.
//
// Every text typed through here goes through termtext.Block first (R2-M1): line breaks survive,
// escape sequences and every other control, format and private-use character do not. This is
// the one door into a terminal (the web app's Chat and job messages, answers, pokes, briefs,
// nudges), so a caller that forgets to clean its text still cannot type an ESC into a pane.

package orch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/termtext"
)

const (
	// promptWaitMS is how long agent.prompt waits for the turn to start (working or blocked).
	promptWaitMS = 30_000
	// MaxPromptBytes caps one prompt. Long briefs belong in a file the agent is pointed at.
	MaxPromptBytes = 32 << 10
)

// PromptRequest is the body of POST /agents/{target}/prompt.
type PromptRequest struct {
	Text           string `json:"text"`
	ForceOverDraft bool   `json:"force_over_draft"` // append even though a draft is in the input box
	// ExpectAgent, when set, is the agent name the target pane must hold: the supervisor types
	// into a job's pane by id, and herdr reuses pane ids, so a pane that now holds someone else is
	// left alone (R2-I5). Never taken from a browser's request body.
	ExpectAgent string `json:"-"`
}

// PromptResult says what happened to a prompt that was typed.
type PromptResult struct {
	PaneID string            `json:"pane_id"`
	Status herdr.AgentStatus `json:"status"` // the agent's state after the send
	// Queued is true when the agent was already working: Claude holds the text until the
	// current turn ends.
	Queued bool `json:"queued"`
	// Started is true when a turn was seen to start (or the text was queued). False means the
	// text was typed but Claude did not visibly pick it up within 30 s; Note says what to do.
	Started bool `json:"started"`
	// Uncertain is true when crowsnest handed the text to herdr and then lost the answer, so it
	// cannot say whether the text was typed (R1b-C05). A caller must neither retry it (that may
	// type it twice) nor report it as delivered.
	Uncertain bool   `json:"uncertain,omitempty"`
	Note      string `json:"note,omitempty"`
}

// DraftError refuses a send because the input box holds text typed at the terminal.
type DraftError struct {
	Draft fleet.Draft
}

func (e *DraftError) Error() string {
	return "the agent's input box holds a draft typed at the terminal"
}

// BlockedError refuses a send because a dialog is up. Nothing was typed.
type BlockedError struct {
	PaneID     string
	ScreenHash string
	Dialog     fleet.Dialog
}

func (e *BlockedError) Error() string { return "the agent is waiting at a dialog" }

// ErrNotTyped marks a send that failed before anything was typed (the agent could not be found,
// its screen could not be read, or a dialog came up). A caller may safely try again later; the
// answer deliverer (internal/questions) relies on it for at-most-once delivery. BlockedError and
// DraftError also mean nothing was typed.
var ErrNotTyped = errors.New("nothing was typed")

// notTyped wraps err so errors.Is(err, ErrNotTyped) holds.
func notTyped(err error) error { return fmt.Errorf("%w: %w", ErrNotTyped, err) }

// ErrWrongAgent refuses a send whose target pane no longer holds the expected agent. Nothing was
// typed; it is also ErrNotTyped.
var ErrWrongAgent = errors.New("the pane holds a different agent now")

// ErrEmptyPrompt and ErrPromptTooLong are validation failures.
var (
	ErrEmptyPrompt   = errors.New("the prompt is empty")
	ErrPromptTooLong = fmt.Errorf("the prompt is longer than %d bytes", MaxPromptBytes)
)

// Prompt sends text to the agent target (a name or a pane id). It returns *BlockedError or
// *DraftError when it refused to type; any herdr error (not found included) is returned wrapped.
func Prompt(ctx context.Context, client herdr.Client, target string, request PromptRequest) (PromptResult, error) {
	request.Text = termtext.Block(request.Text)
	if err := validatePrompt(request.Text); err != nil {
		return PromptResult{}, err
	}
	agent, err := callAgentGet(ctx, client, target)
	if err != nil {
		return PromptResult{}, notTyped(fmt.Errorf("finding agent %s: %w", target, err))
	}
	if request.ExpectAgent != "" && agent.Name != request.ExpectAgent {
		return PromptResult{}, notTyped(fmt.Errorf("%w: pane %s holds %q, not %q", ErrWrongAgent, agent.PaneID, agent.Name, request.ExpectAgent))
	}
	// One typist per pane: the draft check and the typing happen under the pane's lock, so a
	// send never reads text crowsnest itself is still typing (a poke, an answer, a brief) as
	// somebody's draft, and two texts are never typed into one box at once.
	unlock, err := lockPane(ctx, client, agent.PaneID)
	if err != nil {
		return PromptResult{}, notTyped(err)
	}
	defer unlock()
	if err := checkInput(ctx, client, agent, request.ForceOverDraft); err != nil {
		return PromptResult{}, err
	}
	if err := ctx.Err(); err != nil {
		// The caller gave up during the checks (a job cancelled meanwhile): nothing is typed.
		return PromptResult{}, notTyped(err)
	}
	sendCtx, cancel := context.WithTimeout(ctx, promptWaitMS*time.Millisecond+5*time.Second)
	defer cancel()
	wait := &herdr.PromptWait{Until: []herdr.AgentStatus{herdr.StatusWorking, herdr.StatusBlocked}, TimeoutMS: promptWaitMS}
	after, err := client.AgentPrompt(sendCtx, agent.PaneID, request.Text, wait)
	return promptOutcome(ctx, client, agent, after, err)
}

// paneTypists holds one lock per pane (and per herdr client, so tests with their own fakes do
// not share locks): a buffered channel of one, which a waiter can give up on when its context
// ends. Found by `mise run e2e:claude` (M9): a poke being typed made a human's send fail with a
// false "someone typed a draft". Entries are never removed; a session has a few dozen panes.
var (
	paneTypistsMu sync.Mutex
	paneTypists   = map[paneTypistKey]chan struct{}{}
)

type paneTypistKey struct {
	client herdr.Client
	pane   string
}

// lockPane waits for the pane's typing lock and returns its release, or an error if ctx ends.
func lockPane(ctx context.Context, client herdr.Client, paneID string) (func(), error) {
	paneTypistsMu.Lock()
	key := paneTypistKey{client: client, pane: paneID}
	slot, ok := paneTypists[key]
	if !ok {
		slot = make(chan struct{}, 1)
		paneTypists[key] = slot
	}
	paneTypistsMu.Unlock()
	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for another send to pane %s to finish: %w", paneID, ctx.Err())
	}
}

func validatePrompt(text string) error {
	switch {
	case len(text) == 0 || strings.TrimSpace(text) == "":
		return ErrEmptyPrompt
	case len(text) > MaxPromptBytes:
		return ErrPromptTooLong
	}
	return nil
}

// checkInput refuses a send into a dialog or over a draft, from one read of the screen.
func checkInput(ctx context.Context, client herdr.Client, agent herdr.Agent, force bool) error {
	screen, err := readScreen(ctx, client, agent.PaneID)
	if err != nil {
		return notTyped(fmt.Errorf("reading the agent's screen: %w", err))
	}
	if agent.AgentStatus == herdr.StatusBlocked || screen.dialog.Present {
		return &BlockedError{PaneID: agent.PaneID, ScreenHash: screen.hash, Dialog: screen.dialog}
	}
	if screen.draft.Present && !force {
		return &DraftError{Draft: screen.draft}
	}
	return nil
}

// promptOutcome turns agent.prompt's answer into a result.
func promptOutcome(ctx context.Context, client herdr.Client, before, after herdr.Agent, err error) (PromptResult, error) {
	result := PromptResult{PaneID: before.PaneID, Status: after.AgentStatus, Queued: before.AgentStatus == herdr.StatusWorking}
	switch {
	case err == nil:
		result.Started = result.Queued || after.AgentStatus == herdr.StatusWorking
		if after.AgentStatus == herdr.StatusBlocked {
			result.Note = "Claude started, and is now waiting at a dialog."
		}
		return result, nil
	case errors.Is(err, herdr.ErrAgentBlocked):
		// A dialog came up between the check and the send; herdr typed nothing.
		screen, readErr := readScreen(ctx, client, before.PaneID)
		if readErr != nil {
			return PromptResult{}, &BlockedError{PaneID: before.PaneID}
		}
		return PromptResult{}, &BlockedError{PaneID: before.PaneID, ScreenHash: screen.hash, Dialog: screen.dialog}
	case errors.Is(err, herdr.ErrAgentPromptStalled), errors.Is(err, herdr.ErrTimeout):
		result.Status, result.Started = before.AgentStatus, result.Queued
		result.Note = "The text was typed, but Claude has not started a turn. Check its terminal before sending again."
		return result, nil
	case errors.Is(err, herdr.ErrNoResponse):
		// The request reached herdr and then the answer was lost (our own deadline, a dropped
		// connection). herdr may well have typed it, so this is uncertain delivery, not a
		// failure: an error here would invite a retry that sends the instruction twice (R1-C09).
		result.Status, result.Started, result.Uncertain = before.AgentStatus, false, true
		result.Note = "crowsnest lost track of this send after handing it to herdr, so it may or may not have been typed. Check the agent's terminal before sending again."
		return result, nil
	default:
		return PromptResult{}, fmt.Errorf("sending the prompt: %w", err)
	}
}

// screenRead is the part of a screen the send path needs.
type screenRead struct {
	hash   string
	dialog fleet.Dialog
	draft  fleet.Draft
}

func readScreen(ctx context.Context, client herdr.Client, paneID string) (screenRead, error) {
	ctx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
	defer cancel()
	read, err := client.PaneRead(ctx, paneID, herdr.ReadOptions{Source: herdr.SourceVisible, ANSI: true})
	if err != nil {
		return screenRead{}, err
	}
	text := fleet.StripANSI(read.Text)
	return screenRead{hash: fleet.ScreenHash(text), dialog: fleet.DetectDialog(text), draft: fleet.DetectDraft(read.Text)}, nil
}

func callAgentGet(ctx context.Context, client herdr.Client, target string) (herdr.Agent, error) {
	ctx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
	defer cancel()
	return client.AgentGet(ctx, target)
}
