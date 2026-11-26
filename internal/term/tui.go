// The TUI bridge: the whole herdr session (sidebar, tabs, every pane) as `herdr --session
// <name>` in a PTY. It is the honest "everything" view for a desktop browser.
//
// The herdr client is only a viewer: the server and every agent live in the session's server
// process, which crowsnest started separately (detached) or found running. So closing a browser
// tab kills this client and nothing else. The client runs in its own session and process group
// (pty.Start does setsid) with HERDR_* scrubbed from its environment, and Close signals the
// client process alone, never a group, so even a client that had forked a helper could not take
// the server down with it. The integration test checks the session survives.

package term

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/creack/pty"
)

// tuiTerm is the terminal type the PTY advertises; xterm.js speaks it.
const tuiTerm = "TERM=xterm-256color"

// tuiBridge implements Bridge for the herdr TUI.
type tuiBridge struct {
	*stream
	binary, session string

	// mu guards pty. Write and Resize hold it shared while they use the PTY, and the PTY is
	// closed (and set to nil) only under the exclusive lock, so neither can use a closed
	// descriptor.
	mu  sync.RWMutex
	pty *os.File
	// procMu guards cmd and the start transition, and is never held across I/O. Close takes
	// only procMu: a write blocked on a client that stopped reading holds mu shared, and Close
	// must still be able to signal that client (R1-C01). Once it dies the blocked write fails.
	// started is atomic so Write can report "not started" without taking procMu (Start holds
	// procMu while it takes mu; the reverse order would deadlock).
	procMu  sync.Mutex
	cmd     *exec.Cmd
	exited  chan struct{}
	started atomic.Bool
}

func newTUIBridge(binary, session string) *tuiBridge {
	return &tuiBridge{stream: newStream(), binary: binary, session: session, exited: make(chan struct{})}
}

// Start runs the herdr client in a PTY of cols x rows.
func (b *tuiBridge) Start(cols, rows int) error {
	if err := ValidSize(cols, rows); err != nil {
		return err
	}
	b.procMu.Lock()
	defer b.procMu.Unlock()
	if b.started.Load() {
		return ErrStarted
	}
	if b.stopping() {
		return ErrClosed
	}
	b.started.Store(true)
	cmd := exec.Command(b.binary, "--session", b.session) //nolint:gosec // fixed argv; session name is validated at startup
	cmd.Env = childEnv(tuiTerm)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}) //nolint:gosec // ValidSize bounds both
	if err != nil {
		return fmt.Errorf("starting the herdr client in a terminal: %w", err)
	}
	b.cmd = cmd
	b.mu.Lock()
	b.pty = ptmx
	b.mu.Unlock()
	go b.readOutput(cmd, ptmx)
	return nil
}

// readOutput copies PTY output to Output until the client exits. After Close it keeps reading
// and discards what it reads: on macOS a process exiting with unread output on its terminal
// blocks in exit until that output drains (seen as state "E" in ps), so a reader that stopped
// early would leave the killed client unreapable and this goroutine stuck in Wait.
func (b *tuiBridge) readOutput(cmd *exec.Cmd, ptmx *os.File) {
	buffer := make([]byte, readChunk)
	delivering := true
	for {
		n, err := ptmx.Read(buffer)
		if n > 0 && delivering {
			delivering = b.send(buffer[:n])
		}
		if err != nil {
			break // EIO once the client has exited and the slave side is closed
		}
	}
	waitErr := cmd.Wait()
	close(b.exited)
	b.mu.Lock()
	_ = ptmx.Close()
	b.pty = nil
	b.mu.Unlock()
	b.finish(clientExitReason(waitErr))
}

// clientExitReason turns the client's exit into a sentence for the browser.
func clientExitReason(err error) string {
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return "detached" // ctrl+b q, or the client left by itself
	case errors.As(err, &exitErr):
		return fmt.Sprintf("herdr exited (%v)", exitErr)
	default:
		return fmt.Sprintf("herdr exited (%v)", err)
	}
}

// Write sends keystrokes to the client.
func (b *tuiBridge) Write(p []byte) (int, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.pty == nil {
		return 0, b.notRunning()
	}
	n, err := b.pty.Write(p)
	if err != nil {
		return n, fmt.Errorf("writing to the herdr client: %w", err)
	}
	return n, nil
}

// Resize changes the PTY size; the client redraws on SIGWINCH.
func (b *tuiBridge) Resize(cols, rows int) error {
	if err := ValidSize(cols, rows); err != nil {
		return err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.pty == nil {
		return b.notRunning()
	}
	if err := pty.Setsize(b.pty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}); err != nil { //nolint:gosec // ValidSize bounds both
		return fmt.Errorf("resizing the terminal: %w", err)
	}
	return nil
}

// notRunning is the error for a call with no PTY: before Start, or after the client exited.
func (b *tuiBridge) notRunning() error {
	if b.started.Load() {
		return ErrClosed
	}
	return ErrNotStarted
}

// Close kills the herdr client (only the client: see the file comment) and frees the PTY.
func (b *tuiBridge) Close() error {
	if !b.stop() {
		return nil
	}
	b.procMu.Lock()
	cmd := b.cmd
	b.procMu.Unlock()
	if cmd == nil {
		close(b.out)
		return nil
	}
	// SIGHUP is what a terminal closing means; the client may also get EIO on its next write.
	_ = cmd.Process.Signal(syscall.SIGHUP)
	go terminate(cmd, false, b.exited)
	return nil
}

var _ io.Writer = (*tuiBridge)(nil)
