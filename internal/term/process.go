// What the bridges share: the output stream (a channel that is closed exactly once, with a
// reason), the scrubbed child environment, and stopping a child politely and then firmly.

package term

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
)

// ---- Output stream -------------------------------------------------------------------

// stream is the output side of a bridge. One producer goroutine calls send and then finish;
// Close (from anywhere) calls stop. send never blocks past stop, so a producer cannot leak
// when nobody reads Output any more.
type stream struct {
	out  chan []byte
	done chan struct{} // closed by stop

	stopOnce sync.Once
	mu       sync.Mutex
	reason   string
}

func newStream() *stream {
	return &stream{out: make(chan []byte, outputBuffer), done: make(chan struct{})}
}

// send delivers a copy of p, or reports false once the bridge is stopping.
func (s *stream) send(p []byte) bool {
	chunk := append([]byte(nil), p...)
	select {
	case s.out <- chunk:
		return true
	case <-s.done:
		return false
	}
}

// finish records why output ended (unless a reason was already recorded) and closes Output.
// Only the producer calls it, once.
func (s *stream) finish(reason string) {
	s.setReason(reason)
	close(s.out)
}

// setReason keeps the first reason given: the process's own account beats a later "closed".
func (s *stream) setReason(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reason == "" {
		s.reason = reason
	}
}

// stop marks the bridge as closing. It returns true only the first time.
func (s *stream) stop() bool {
	first := false
	s.stopOnce.Do(func() {
		first = true
		s.setReason(ReasonClosed)
		close(s.done)
	})
	return first
}

func (s *stream) stopping() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// Output and Reason implement the Bridge methods of the same name.
func (s *stream) Output() <-chan []byte { return s.out }

func (s *stream) Reason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

// ---- Child processes -----------------------------------------------------------------

// childEnv is this process's environment without HERDR_* (DESIGN.md 4.10: a crowsnest started
// inside a herdr pane must not be able to drive that pane's session), plus extra entries.
func childEnv(extra ...string) []string {
	return append(herdr.ScrubEnv(os.Environ()), extra...)
}

// withEnv returns env with key set to value, replacing any earlier value (the last one would
// win in most libcs, but not all; one entry leaves no doubt).
func withEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, key+"=") {
			out = append(out, entry)
		}
	}
	return append(out, key+"="+value)
}

// terminate asks a child to leave and kills it if it has not by closeGrace. exited must be
// closed by whoever calls cmd.Wait. With group set, the signal goes to the child's process
// group (it was started as a group leader); otherwise to the child alone.
func terminate(cmd *exec.Cmd, group bool, exited <-chan struct{}) {
	if cmd.Process == nil {
		return
	}
	signal := func(sig syscall.Signal) {
		if group {
			_ = syscall.Kill(-cmd.Process.Pid, sig)
			return
		}
		_ = cmd.Process.Signal(sig)
	}
	select {
	case <-exited:
		return
	default:
	}
	signal(syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(closeGrace):
		signal(syscall.SIGKILL)
	}
}
