// The pane bridge: one pane of the session through `herdr --session <name> terminal session
// control <pane> --takeover --cols N --rows N`. stdout frames become terminal bytes, input and
// resizes become stdin lines (codec.go). This is the phone-friendly terminal: one pane, any
// width, and no herdr chrome around it.
//
// One controller per pane: --takeover means a new browser wins and the previous controller's
// stream ends with terminal.closed "terminal attach taken over" (verified), which the previous
// browser is shown as its exit reason.

package term

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// maxControlLine bounds one stdout line. A full redraw of a MaxCols x MaxRows screen with
// colour escapes, base64-encoded, stays well under this.
const maxControlLine = 16 << 20

// stderrTail is how much of the control process's stderr is kept to explain an unexpected exit.
const stderrTail = 512

// paneBridge implements Bridge for one pane.
type paneBridge struct {
	*stream
	binary, session, paneID string

	// mu guards cmd, stdin and started. It is never held across a write to the child: a child
	// that stops reading its stdin would block that write for ever, and Close needs mu to find
	// the process it must stop (R1-C01). writeMu orders the writes themselves.
	mu      sync.Mutex
	writeMu sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	exited  chan struct{} // closed after cmd.Wait
	stderr  tailBuffer
	started bool
}

func newPaneBridge(binary, session, paneID string) *paneBridge {
	return &paneBridge{stream: newStream(), binary: binary, session: session, paneID: paneID, exited: make(chan struct{})}
}

// Start runs the control process at cols x rows.
func (b *paneBridge) Start(cols, rows int) error {
	if err := ValidSize(cols, rows); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return ErrStarted
	}
	if b.stopping() {
		return ErrClosed
	}
	b.started = true
	cmd := exec.Command(b.binary, "--session", b.session, "terminal", "session", "control", b.paneID, //nolint:gosec // fixed argv; session and pane id are validated by the caller
		"--takeover", "--cols", strconv.Itoa(cols), "--rows", strconv.Itoa(rows))
	cmd.Env = childEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stderr = &b.stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("opening control stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("opening control stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting herdr terminal control for %s: %w", b.paneID, err)
	}
	b.cmd, b.stdin = cmd, stdin
	go b.readFrames(stdout)
	return nil
}

// readFrames turns stdout lines into Output until terminal.closed, end of stream, or Close.
func (b *paneBridge) readFrames(stdout io.Reader) {
	reason := ""
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), maxControlLine)
	for scanner.Scan() {
		message, err := DecodeMessage(scanner.Bytes())
		if err != nil {
			continue // herdr may add message types; one bad line is not worth the session
		}
		if message.Type == TypeClosed {
			reason = firstNonEmpty(message.Reason, ReasonExited)
			break
		}
		if message.Type == TypeFrame && len(message.Bytes) > 0 && !b.send(message.Bytes) {
			break
		}
	}
	_, _ = io.Copy(io.Discard, stdout) // let the process finish writing so Wait can return
	waitErr := b.cmd.Wait()
	close(b.exited)
	if reason == "" {
		reason = exitReason(waitErr, b.stderr.String())
	}
	b.finish(reason)
}

// Write types p into the pane.
func (b *paneBridge) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := b.writeLine(EncodeInput(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Resize changes the controller's view of the pane.
func (b *paneBridge) Resize(cols, rows int) error {
	if err := ValidSize(cols, rows); err != nil {
		return err
	}
	return b.writeLine(EncodeResize(cols, rows))
}

func (b *paneBridge) writeLine(line []byte) error {
	b.mu.Lock()
	stdin := b.stdin
	b.mu.Unlock()
	if stdin == nil {
		return ErrNotStarted
	}
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	if _, err := stdin.Write(line); err != nil {
		return fmt.Errorf("writing to herdr terminal control: %w", err)
	}
	return nil
}

// releaseWait bounds the polite part of Close: how long the release line may take to reach a
// control process before its stdin is closed anyway. A healthy process takes microseconds; one
// that stopped reading would otherwise hold Close for ever.
const releaseWait = 500 * time.Millisecond

// Close releases the pane (so herdr detaches cleanly) and stops the control process. It does
// not wait: the release, closing stdin and the signals happen on their own goroutine, so a
// child that stopped reading cannot hold up the caller (R1-C01).
func (b *paneBridge) Close() error {
	if !b.stop() {
		return nil
	}
	b.mu.Lock()
	cmd, stdin := b.cmd, b.stdin
	b.mu.Unlock()
	if cmd == nil {
		close(b.out) // never started: nothing will ever finish the stream
		return nil
	}
	go b.shutdown(cmd, stdin)
	return nil
}

// shutdown sends terminal.release if the child takes it within releaseWait, then closes stdin
// (which also fails any write still blocked on the pipe) and stops the process group.
func (b *paneBridge) shutdown(cmd *exec.Cmd, stdin io.WriteCloser) {
	released := make(chan struct{})
	go func() {
		defer close(released)
		b.writeMu.Lock()
		defer b.writeMu.Unlock()
		_, _ = stdin.Write(EncodeRelease())
	}()
	select {
	case <-released:
	case <-time.After(releaseWait):
	}
	_ = stdin.Close()
	terminate(cmd, true, b.exited)
}

// exitReason explains a control process that ended without terminal.closed.
func exitReason(err error, stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if lines := strings.Split(stderr, "\n"); stderr != "" {
		stderr = strings.TrimPrefix(lines[len(lines)-1], "herdr: ")
	}
	switch {
	case err == nil && stderr == "":
		return ReasonExited
	case err == nil:
		return stderr
	case stderr != "":
		return fmt.Sprintf("%s (%v)", stderr, err)
	default:
		return fmt.Sprintf("%s (%v)", ReasonExited, err)
	}
}

// tailBuffer is an io.Writer that keeps only the last stderrTail bytes.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if extra := len(t.buf) - stderrTail; extra > 0 {
		t.buf = bytes.Clone(t.buf[extra:])
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
