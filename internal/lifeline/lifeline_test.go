// Tests for the lifeline (R9-C7): the watchdog runs as its own process group leader (as crowsnest
// starts it), so the test re-executes its own binary as the watchdog; ending a group from inside
// the test process would end the test runner's group.

package lifeline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const childEnv = "CROWSNEST_LIFELINE_TEST_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		os.Exit(RunChild(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// startWatchdog starts this binary as the watchdog over args, its stdin the lifeline's read end.
func startWatchdog(t *testing.T, args ...string) (*exec.Cmd, *os.File) {
	t.Helper()
	read, write, err := Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], args...) //nolint:gosec // the test binary itself
	cmd.Env = append(os.Environ(), childEnv+"=1")
	cmd.Stdin = read
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = read.Close()
	t.Cleanup(func() {
		_ = write.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	})
	return cmd, write
}

func TestTheGroupEndsWhenCrowsnestGoes(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	// The check starts a background process of its own, as a test runner or a server would.
	cmd, lifeline := startWatchdog(t, "sh", "-c", "sleep 60 & echo $! > "+pidFile+"; wait")
	var pid int
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if body, err := os.ReadFile(pidFile); err == nil && strings.TrimSpace(string(body)) != "" { //nolint:gosec // test fixture
			pid, _ = strconv.Atoi(strings.TrimSpace(string(body)))
			break
		}
	}
	if pid == 0 {
		t.Fatal("the check never started its background process")
	}
	// crowsnest dies: the kernel closes its end of the pipe.
	_ = lifeline.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case waitErr := <-done:
		_ = waitErr // the watchdog's status after a stop is not what is tested
	case <-time.After(15 * time.Second):
		t.Fatal("the watchdog did not end after the lifeline closed")
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return // gone
		}
	}
	t.Fatalf("the check's background process %d outlived crowsnest", pid)
}

func TestTheChildsExitCodeIsKept(t *testing.T) {
	cmd, _ := startWatchdog(t, "sh", "-c", "exit 7")
	err := cmd.Wait()
	exit, ok := err.(*exec.ExitError) //nolint:errorlint // Wait returns it unwrapped
	if !ok || exit.ExitCode() != 7 {
		t.Fatalf("exit = %v, want 7", err)
	}
}

func TestCommandPrefix(t *testing.T) {
	if got := Command(nil, []string{"sh", "-c", "x"}); strings.Join(got, " ") != "sh -c x" {
		t.Fatalf("no prefix: %v", got)
	}
	if got := Command(Prefix("/bin/crowsnest"), []string{"sh", "-c", "x"}); strings.Join(got, " ") != "/bin/crowsnest check-child -- sh -c x" {
		t.Fatalf("prefix: %v", got)
	}
}

// TestTheWatchdogRefusesAGroupItDoesNotLead (R10 low): started without a process group of its own,
// the watchdog's group is its parent's, so it runs nothing (ending that group would end others).
// The command here would exit by itself, so no build of the watchdog ever reaches the group kill.
func TestTheWatchdogRefusesAGroupItDoesNotLead(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	read, write, err := Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = write.Close() }()
	cmd := exec.Command(os.Args[0], "sh", "-c", "touch "+marker) //nolint:gosec // the test binary itself
	cmd.Env = append(os.Environ(), childEnv+"=1")
	cmd.Stdin = read
	err = cmd.Run()
	_ = read.Close()
	if code := cmd.ProcessState.ExitCode(); code != 2 {
		t.Fatalf("exit %d (%v), want 2", code, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command ran under a group the watchdog does not lead")
	}
}
