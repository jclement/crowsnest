// The send path (DESIGN.md 7.3): prompting an agent from the web app. It works for any agent,
// the orchestrator included.
//
// Before typing anything it looks at the agent: a dialog on screen (herdr's blocked state, or
// one detected from the screen) refuses the send, because the text would land in the dialog; a
// draft typed at the terminal refuses it unless the caller says to append anyway, because
// agent.prompt appends to whatever is in the input box. After typing it waits for the turn to
// start, since a submission alone does not prove Claude read it. Sending while the agent works is
// allowed: Claude queues the input, and the result says so.

package orch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
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
	Started bool   `json:"started"`
	Note    string `json:"note,omitempty"`
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

// ErrEmptyPrompt and ErrPromptTooLong are validation failures.
var (
	ErrEmptyPrompt   = errors.New("the prompt is empty")
	ErrPromptTooLong = fmt.Errorf("the prompt is longer than %d bytes", MaxPromptBytes)
)

// Prompt sends text to the agent target (a name or a pane id). It returns *BlockedError or
// *DraftError when it refused to type; any herdr error (not found included) is returned wrapped.
func Prompt(ctx context.Context, client herdr.Client, target string, request PromptRequest) (PromptResult, error) {
	if err := validatePrompt(request.Text); err != nil {
		return PromptResult{}, err
	}
	agent, err := callAgentGet(ctx, client, target)
	if err != nil {
		return PromptResult{}, fmt.Errorf("finding agent %s: %w", target, err)
	}
	if err := checkInput(ctx, client, agent, request.ForceOverDraft); err != nil {
		return PromptResult{}, err
	}
	sendCtx, cancel := context.WithTimeout(ctx, promptWaitMS*time.Millisecond+5*time.Second)
	defer cancel()
	wait := &herdr.PromptWait{Until: []herdr.AgentStatus{herdr.StatusWorking, herdr.StatusBlocked}, TimeoutMS: promptWaitMS}
	after, err := client.AgentPrompt(sendCtx, agent.PaneID, request.Text, wait)
	return promptOutcome(ctx, client, agent, after, err)
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
		return fmt.Errorf("reading the agent's screen: %w", err)
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
