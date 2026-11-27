// The watchdog between crowsnest and `tailscale serve`: the hidden `crowsnest
// tailscale-serve-child -- <tailscale> serve --https=N <target>` runs this.
//
// Why a watchdog at all: the published address must go away when crowsnest goes away, however
// it goes. A clean stop is easy (crowsnest signals the child). A crash or SIGKILL is not: the
// child is reparented and keeps serving. Linux's parent-death signal does not help in Go (it
// fires when the forking OS thread exits, not the process) and macOS has none. A pipe does: the
// watchdog's stdin is the read end of a pipe whose only write end crowsnest holds (close-on-exec,
// so no other child inherits it). However crowsnest ends, the kernel closes that end, the
// watchdog reads EOF, and it interrupts tailscale. tailscaled then drops the foreground serve
// session (it is tied to the CLI's connection to tailscaled), so nothing persistent is left.
//
// The watchdog and tailscale share a process group of their own, so crowsnest can also end both
// at once (kill -pgid), and a terminal's Ctrl+C aimed at crowsnest does not reach them first.

package tsserve

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
)

// ChildCommand is the hidden subcommand that runs the watchdog.
const ChildCommand = "tailscale-serve-child"

// childGrace is how long the watchdog waits after each signal before the next, harder one:
// interrupt (what Ctrl+C sends, tailscale serve's documented way out), then terminate, then kill.
const childGrace = 2 * time.Second

// RunChild runs args ([tailscale binary, serve, --https=N, target]) and stops it when stdin
// reaches EOF (crowsnest is gone) or this process is signalled. It returns the exit code:
// tailscale's own when it ended by itself, 0 after a stop, 2 for an argv outside the allowlist.
func RunChild(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) < 2 || args[1] != "serve" {
		fmt.Fprintln(stderr, "crowsnest tailscale-serve-child: expected -- <tailscale> serve --https=<port> <target>")
		return 2
	}
	if err := CheckArgs(args[1:]); err != nil {
		fmt.Fprintln(stderr, "crowsnest tailscale-serve-child:", err)
		return 2
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(signals)

	cmd := exec.Command(args[0], args[1:]...) //nolint:gosec // argv checked against the allowlist above
	cmd.Env = herdr.ScrubEnv(os.Environ())
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(stderr, "crowsnest tailscale-serve-child: starting tailscale:", err)
		return 1
	}
	exited := make(chan int, 1)
	go func() { exited <- exitCode(cmd.Wait()) }()
	parentGone := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, stdin)
		close(parentGone)
	}()

	select {
	case code := <-exited:
		return code
	case <-parentGone:
	case <-signals:
	}
	stopProcess(cmd.Process, exited)
	return 0
}

// stopProcess interrupts, then terminates, then kills process, waiting childGrace between
// steps for it to exit.
func stopProcess(process *os.Process, exited <-chan int) {
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM, os.Kill} {
		_ = process.Signal(sig)
		select {
		case <-exited:
			return
		case <-time.After(childGrace):
		}
	}
}

// exitCode turns cmd.Wait's error into an exit code (-1 when killed by a signal).
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if exit, ok := err.(*exec.ExitError); ok { //nolint:errorlint // Wait returns it unwrapped
		return exit.ExitCode()
	}
	return 1
}
