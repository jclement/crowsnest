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
	"golang.org/x/sys/unix"
)

// tuiTerm is the terminal type the PTY advertises; xterm.js speaks it.
const tuiTerm = "TERM=xterm-256color"

// tuiBridge implements Bridge for the herdr TUI.
type tuiBridge struct {
	*stream
	binary, session string

	// mu guards the pty field only, and is never held across I/O: Write and Resize copy the
	// pointer and let go. The PTY master is non-blocking and owned by Go's poller (pollable),
	// so closing it while a write is blocked on it wakes that write with an error, and an
	// *os.File never hands a closed descriptor to a syscall. That is what lets readOutput close
	// the PTY behind a write stuck on a client that stopped reading (R1-C01 on Linux, where
	// such a write is not woken by the client dying, only by the close).
	mu  sync.Mutex
	pty *os.File
	// procMu guards cmd and the start transition, and is never held across I/O, so Close can
	// always signal the client. started is atomic so Write can report "not started" without
	// taking procMu.
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
	started, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}) //nolint:gosec // ValidSize bounds both
	if err != nil {
		return fmt.Errorf("starting the herdr client in a terminal: %w", err)
	}
	ptmx, err := pollable(started)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("starting the herdr client in a terminal: %w", err)
	}
	b.cmd = cmd
	b.mu.Lock()
	b.pty = ptmx
	b.mu.Unlock()
	go b.readOutput(cmd, ptmx)
	return nil
}

// pollable returns the PTY master as a non-blocking file in Go's poller, and closes the one it
// was given. creack/pty hands back a blocking descriptor (it calls Fd, which switches a file to
// blocking mode), and a blocking write to a client that stopped reading cannot be woken by
// anything on Linux: not the client's death, not closing the file. A poller-owned file wakes
// its blocked calls when it is closed. The descriptor is duplicated (close-on-exec) because
// the original *os.File would close it again when collected.
func pollable(ptmx *os.File) (*os.File, error) {
	defer func() { _ = ptmx.Close() }()
	raw, err := ptmx.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("reaching the terminal's descriptor: %w", err)
	}
	dup := -1
	var dupErr error
	if err := raw.Control(func(fd uintptr) {
		dup, dupErr = unix.FcntlInt(fd, unix.F_DUPFD_CLOEXEC, 0)
	}); err != nil {
		return nil, fmt.Errorf("reaching the terminal's descriptor: %w", err)
	}
	if dupErr != nil {
		return nil, fmt.Errorf("duplicating the terminal's descriptor: %w", dupErr)
	}
	if err := unix.SetNonblock(dup, true); err != nil {
		_ = unix.Close(dup)
		return nil, fmt.Errorf("making the terminal non-blocking: %w", err)
	}
	return os.NewFile(uintptr(dup), ptmx.Name()), nil // non-blocking, so NewFile puts it in the poller
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

// file is the PTY, or nil before Start and after the client exited.
func (b *tuiBridge) file() *os.File {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pty
}

// Write sends keystrokes to the client. A write blocked on a client that stopped reading ends
// with an error once readOutput closes the PTY (see pollable).
func (b *tuiBridge) Write(p []byte) (int, error) {
	ptmx := b.file()
	if ptmx == nil {
		return 0, b.notRunning()
	}
	n, err := ptmx.Write(p)
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
	ptmx := b.file()
	if ptmx == nil {
		return b.notRunning()
	}
	// Through SyscallConn rather than pty.Setsize: that calls Fd, which would put the PTY back
	// in blocking mode.
	raw, err := ptmx.SyscallConn()
	if err != nil {
		return fmt.Errorf("resizing the terminal: %w", err)
	}
	size := &unix.Winsize{Col: uint16(cols), Row: uint16(rows)} //nolint:gosec // ValidSize bounds both
	var setErr error
	if err := raw.Control(func(fd uintptr) { setErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, size) }); err != nil { //nolint:gosec // a descriptor fits in an int
		return fmt.Errorf("resizing the terminal: %w", err)
	}
	if setErr != nil {
		return fmt.Errorf("resizing the terminal: %w", setErr)
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
