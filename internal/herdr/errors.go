// Typed herdr errors. herdr answers a failed call with {"error":{"code","message"}}; the code is
// stable and is what callers branch on, so every such answer becomes an *Error and the codes
// crowsnest acts on have sentinels that match with errors.Is.

package herdr

import (
	"errors"
	"fmt"
)

// Error codes herdr returns that crowsnest handles specifically. The list is not exhaustive;
// any other code still arrives as an *Error with that code.
const (
	CodeAgentBlocked       = "agent_blocked"        // a dialog is up; nothing was sent
	CodeAgentNotReady      = "agent_not_ready"      // start hit a trust/login prompt or timed out
	CodeAgentPromptStalled = "agent_prompt_stalled" // submitted, but no turn started within 5 s
	CodeAgentNotFound      = "agent_not_found"
	CodePaneNotFound       = "pane_not_found"
	CodeTabNotFound        = "tab_not_found"
	CodeWorkspaceNotFound  = "workspace_not_found"
	CodeTimeout            = "timeout"
	CodeEventsLost         = "events_lost" // subscriber fell behind; resync from a snapshot
	CodeInvalidRequest     = "invalid_request"
)

// Error is an error answer from herdr.
type Error struct {
	Code    string
	Message string
	Method  string // the method that failed, when known
}

func (e *Error) Error() string {
	if e.Method != "" {
		return fmt.Sprintf("herdr %s: %s (%s)", e.Method, e.Message, e.Code)
	}
	return fmt.Sprintf("herdr: %s (%s)", e.Message, e.Code)
}

// Is matches another *Error by code, so errors.Is(err, herdr.ErrAgentBlocked) works whatever
// the message or method.
func (e *Error) Is(target error) bool {
	var other *Error
	if !errors.As(target, &other) {
		return false
	}
	return other.Code == e.Code
}

// Sentinels for errors.Is.
var (
	ErrAgentBlocked       = &Error{Code: CodeAgentBlocked, Message: "agent is blocked on a dialog"}
	ErrAgentNotReady      = &Error{Code: CodeAgentNotReady, Message: "agent is not ready"}
	ErrAgentPromptStalled = &Error{Code: CodeAgentPromptStalled, Message: "prompt submitted but no turn started"}
	ErrAgentNotFound      = &Error{Code: CodeAgentNotFound, Message: "agent not found"}
	ErrPaneNotFound       = &Error{Code: CodePaneNotFound, Message: "pane not found"}
	ErrTimeout            = &Error{Code: CodeTimeout, Message: "timed out"}
	ErrEventsLost         = &Error{Code: CodeEventsLost, Message: "event subscription fell behind; resync with session.snapshot"}
	ErrInvalidRequest     = &Error{Code: CodeInvalidRequest, Message: "invalid request"}
)

// IsNotFound reports whether err says the addressed workspace, tab, pane or agent does not exist.
func IsNotFound(err error) bool {
	var herdrErr *Error
	if !errors.As(err, &herdrErr) {
		return false
	}
	switch herdrErr.Code {
	case CodeAgentNotFound, CodePaneNotFound, CodeTabNotFound, CodeWorkspaceNotFound:
		return true
	}
	return false
}
