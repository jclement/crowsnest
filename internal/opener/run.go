// Running the opener, and the real machine's Env.

package opener

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// Detect fills an Env from this process and machine. interactive is whether stderr is a terminal;
// inContainer comes from the caller (doctor.InContainer).
func Detect(interactive, inContainer bool) Env {
	home, _ := os.UserHomeDir()
	return Env{
		GOOS: runtime.GOOS, Getenv: os.Getenv, LookPath: exec.LookPath, Stat: os.Stat, Home: home,
		UID: os.Getuid(), Interactive: interactive, InContainer: inContainer,
	}
}

// Open starts decision's command with url and does not wait for the browser: the opener is
// reaped in the background. env is the child's environment (the caller scrubs it of secrets).
func Open(decision Decision, url string, env []string) error {
	if len(decision.Command) == 0 {
		return fmt.Errorf("no opener: %s", decision.Reason)
	}
	args := append(append([]string{}, decision.Command[1:]...), url)
	cmd := exec.Command(decision.Command[0], args...) //nolint:gosec // the opener chosen by Decide; url is crowsnest's own
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", decision.Command[0], err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
