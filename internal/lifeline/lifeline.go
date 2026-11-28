// Package lifeline keeps a child process alive exactly as long as crowsnest (R9-C7).
//
// crowsnest runs its acceptance check (and an isolated check's prepare command) as a process group
// of its own, killed at the timeout. A crash or SIGKILL of crowsnest skipped that: the group was
// reparented and ran on, unnoticed, while the next crowsnest started the same check again beside
// it. macOS has no parent-death signal and Linux's does not suit Go, so the lifeline is a pipe, as
// for tailscale serve's watchdog (internal/tsserve/child.go): the hidden `crowsnest check-child --
// <argv>` runs argv in its own process group, with stdin the read end of a pipe whose only write
// end crowsnest holds (close-on-exec). However crowsnest ends, the kernel closes that end, the
// watchdog reads EOF and ends the whole group: terminate, then kill. crowsnest's own timeout still
// kills the group directly (the watchdog leads it).
package lifeline

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// ChildCommand is the hidden subcommand that runs the watchdog.
const ChildCommand = "check-child"

// grace is how long the group has after the terminate signal before it is killed.
const grace = 3 * time.Second

// RunChild runs args in this process's group and ends the group when stdin reaches EOF
// (crowsnest is gone) or this process is asked to stop. It returns the child's exit code when the
// child ended by itself, 1 after a stop, 2 for no command or when it does not lead its group. The watchdog must be its group's leader
// (crowsnest starts it with Setpgid), so ending the group ends nothing else.
func RunChild(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "crowsnest check-child: expected -- <command> [args...]")
		return 2
	}
	// Ending "this process's group" is only safe when the watchdog leads it: started any other way
	// (by hand from a shell, or by a caller that forgot Setpgid) the group is someone else's, and
	// ending it would end them (R10 low, R9-C7).
	if syscall.Getpgrp() != os.Getpid() {
		_, _ = fmt.Fprintln(stderr, "crowsnest check-child: not the leader of its process group, so it cannot end the check safely; crowsnest starts it with its own group")
		return 2
	}
	stops := make(chan os.Signal, 1)
	signal.Notify(stops, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(stops)

	cmd := exec.Command(args[0], args[1:]...) //nolint:gosec // crowsnest's own check command, set by the human
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Start(); err != nil {
		_, _ = fmt.Fprintln(stderr, "crowsnest check-child: starting the command:", err)
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
	case <-stops:
	}
	endGroup(exited)
	return 1
}

// endGroup terminates this process's group (the watchdog itself ignores the signal), waits up to
// grace for the child, then kills the group, the watchdog with it.
func endGroup(exited <-chan int) {
	signal.Ignore(syscall.SIGTERM)
	group := syscall.Getpgrp()
	_ = syscall.Kill(-group, syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(grace):
	}
	_ = syscall.Kill(-group, syscall.SIGKILL)
}

// exitCode turns Wait's error into an exit code (-1 when killed by a signal).
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if exit, ok := err.(*exec.ExitError); ok { //nolint:errorlint // Wait returns it unwrapped
		return exit.ExitCode()
	}
	return 1
}

// Command is how crowsnest runs args under the watchdog: prefix (crowsnest's binary and the
// hidden subcommand, from Prefix) then args. An empty prefix runs args directly.
func Command(prefix, args []string) []string {
	if len(prefix) == 0 {
		return args
	}
	return append(append(append([]string{}, prefix...), "--"), args...)
}

// Prefix is the watchdog's argv prefix for crowsnest's binary at self ("" gives none).
func Prefix(self string) []string {
	if self == "" {
		return nil
	}
	return []string{self, ChildCommand}
}

// Pipe makes the lifeline: the read end goes to the watchdog as its stdin, crowsnest keeps the
// write end open for as long as the child may run and closes it when the child is done.
func Pipe() (read, write *os.File, err error) {
	return os.Pipe() // both ends close-on-exec: no other child of crowsnest inherits the write end
}
