// Package term bridges a browser terminal to a process: the herdr TUI in a PTY, one pane's
// control stream, or (in --demo) a built-in toy that never starts a process at all. The
// WebSocket handler in internal/httpd speaks the browser side; everything here is about the
// process side, behind one small interface so the handler does not care which bridge it holds.
//
// Life cycle of a Bridge, which matches the WebSocket protocol in DESIGN.md section 10: the
// handler builds one per connection, calls Start with the browser's first resize, copies
// Output to the socket and socket bytes to Write, forwards later resizes, and calls Close when
// either side goes away. Output is closed when the process side ends; Reason then says why
// ("detached", "terminal attach taken over", "exited with status 1", …) and is what the browser
// is told in its `exit` control message.
package term

import (
	"errors"
	"fmt"
	"time"
)

// Bridge is one browser terminal's connection to a process.
type Bridge interface {
	// Start begins the process at the given size. It is called once, with the first resize.
	Start(cols, rows int) error
	// Write sends terminal input (keystrokes, pastes) to the process.
	Write(p []byte) (int, error)
	// Resize tells the process the terminal's new size.
	Resize(cols, rows int) error
	// Output delivers terminal output in order. It is closed when the process side ends, or
	// after Close.
	Output() <-chan []byte
	// Reason says why Output ended. It is meaningful only once Output is closed.
	Reason() string
	// Close ends the bridge and releases the process. It is safe to call more than once and
	// from any goroutine; it does not wait for Output to be drained.
	Close() error
}

// Launcher builds bridges for the two terminal routes. Building a bridge starts nothing; the
// process begins at Start.
type Launcher interface {
	// TUI is the whole herdr session (GET /term/tui).
	TUI() Bridge
	// Pane is one pane's control stream (GET /term/pane/{pane_id}). The caller has already
	// checked that paneID names a pane in the session.
	Pane(paneID string) Bridge
}

// Size limits. A browser never asks for more; anything outside is a bug or an attack, and is
// refused rather than passed to a process.
const (
	MaxCols = 1000
	MaxRows = 500
)

// Timing and buffer sizes shared by the bridges.
const (
	// closeGrace is how long a process gets to leave after being asked before it is killed.
	closeGrace = 2 * time.Second
	// readChunk is the most terminal output read from a process in one go.
	readChunk = 32 << 10
	// outputBuffer is how many chunks of output may wait between a bridge and its reader. The
	// WebSocket handler has its own byte-bounded queue; this one only smooths bursts.
	outputBuffer = 16
)

// Reasons a bridge reports when the process side ends without saying why itself.
const (
	ReasonClosed = "closed" // Close was called
	ReasonExited = "exited" // the process ended normally
)

// ErrStarted is returned by a second Start.
var ErrStarted = errors.New("terminal already started")

// ErrClosed is returned by Start after Close.
var ErrClosed = errors.New("terminal closed")

// ErrNotStarted is returned by Write and Resize before Start.
var ErrNotStarted = errors.New("terminal not started")

// ValidSize reports an error unless cols and rows are within 1..MaxCols and 1..MaxRows.
func ValidSize(cols, rows int) error {
	if cols < 1 || cols > MaxCols || rows < 1 || rows > MaxRows {
		return fmt.Errorf("terminal size %dx%d is outside 1x1..%dx%d", cols, rows, MaxCols, MaxRows)
	}
	return nil
}
