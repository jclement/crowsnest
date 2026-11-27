// `crowsnest heavy <command…>` (DESIGN.md 6.12): run a build, a test suite or a typecheck through
// the instance's heavy-command gate (internal/throttle/heavy.go), so agents running suites at the
// same time cannot take every core. Agents are told to use it in their role prompts.
//
// Where an outside `heavy` exists on PATH (Jeff's machines have ~/.local/bin/heavy), crowsnest
// hands the command to it instead of wrapping twice, so a machine has one set of slots shared by
// every tool. CROWSNEST_HEAVY_DEFER=0 turns that off. `--status` shows the slots either way.
//
// The exit status is the command's; 75 when no slot came free within --timeout (nothing ran).

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/throttle"
)

// exitHeavyTimeout is the exit code when no slot came free in time (EX_TEMPFAIL, as heavy).
const exitHeavyTimeout = 75

func newHeavyCommand() *cobra.Command {
	var status bool
	var timeout time.Duration
	command := &cobra.Command{
		Use:   "heavy [--status] [--timeout 1h] -- <command> [args…]",
		Short: "Run a build or test suite through the heavy-command gate",
		Long: "Runs the command when one of the instance's heavy slots is free (two by default, --heavy-slots),\n" +
			"at low priority (nice 10) with tools' parallelism capped to a third of the cores (MAKEFLAGS,\n" +
			"GOFLAGS, CARGO_BUILD_JOBS, VITEST_MAX_*). While every slot is busy it says whom it waits for.\n" +
			"A `heavy` already on PATH that is not crowsnest is used instead (CROWSNEST_HEAVY_DEFER=0 turns\n" +
			"that off). Never wrap a dev server or watcher: it would hold a slot for as long as it runs.\n\n" +
			"--status lists who holds and who waits for the slots.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := heavyDir(cmd)
			if status {
				printHeavyStatus(cmd, dir)
				return nil
			}
			if len(args) == 0 {
				return usageError{errors.New("give the command to run after --, for example: crowsnest heavy -- go test ./…")}
			}
			return runHeavy(cmd.Context(), dir, timeout, args)
		},
	}
	command.Flags().BoolVar(&status, "status", false, "list who holds and who waits for the slots")
	command.Flags().DurationVar(&timeout, "timeout", time.Hour, "how long to wait for a slot before giving up (exit 75)")
	command.Flags().SetInterspersed(false) // everything after the command is the command's
	return command
}

// heavyDir is the instance's heavy directory: CROWSNEST_HEAVY_DIR in an agent's pane, else
// <state dir>/heavy for the --session in effect.
func heavyDir(cmd *cobra.Command) string {
	if dir := os.Getenv(throttle.DirEnv); dir != "" {
		return dir
	}
	cfg, err := config.Load(cmd.Flags(), config.Options{})
	if err == nil && cfg.StateDir != "" {
		return filepath.Join(cfg.StateDir, "heavy")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "crowsnest", "crowsnest", "heavy")
}

func printHeavyStatus(cmd *cobra.Command, dir string) {
	out := cmd.OutOrStdout()
	self, _ := os.Executable()
	self, _ = filepath.EvalSymlinks(self)
	if outside := throttle.OutsideHeavy(exec.LookPath, self, os.Getenv("CROWSNEST_HEAVY_DEFER")); outside != "" {
		_, _ = fmt.Fprintf(out, "commands go to %s (its own slots: `heavy --status`)\n", outside)
	}
	status := throttle.ReadHeavyStatus(dir, 0)
	_, _ = fmt.Fprintf(out, "%d slots in %s\n", status.Slots, dir)
	held := map[int]throttle.HeavyOwner{}
	for _, owner := range status.Running {
		held[owner.Slot] = owner
	}
	for i := 1; i <= status.Slots; i++ {
		owner, ok := held[i]
		if !ok {
			_, _ = fmt.Fprintf(out, "slot %d: free\n", i)
			continue
		}
		_, _ = fmt.Fprintf(out, "slot %d: %s%s (pid %d, since %s, in %s)\n", i, owner.Command, jobSuffix(owner.Job),
			owner.PID, owner.Since.Local().Format("15:04"), owner.Dir)
	}
	for _, owner := range status.Waiting {
		_, _ = fmt.Fprintf(out, "waiting: %s%s (pid %d, since %s)\n", owner.Command, jobSuffix(owner.Job), owner.PID, owner.Since.Local().Format("15:04"))
	}
}

func jobSuffix(job string) string {
	if job == "" {
		return ""
	}
	return " [job " + job + "]"
}

// runHeavy takes a slot (or defers to an outside heavy) and runs args under it, returning the
// command's exit status as an exitCodeError.
func runHeavy(ctx context.Context, dir string, timeout time.Duration, args []string) error {
	self, _ := os.Executable()
	self, _ = filepath.EvalSymlinks(self)
	if os.Getenv(throttle.HolderEnv) == "" {
		if outside := throttle.OutsideHeavy(exec.LookPath, self, os.Getenv("CROWSNEST_HEAVY_DEFER")); outside != "" {
			return execCommand(ctx, append([]string{outside}, args...), os.Environ())
		}
	}
	env := throttle.CapEnv(os.Environ())
	if os.Getenv(throttle.HolderEnv) != "" {
		// Already under a slot (a script that calls heavy again): run at once.
		return execCommand(ctx, throttle.NiceArgs(args, exec.LookPath), env)
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	slot, err := throttle.AcquireHeavy(ctx, throttle.HeavyOptions{Dir: dir, Timeout: timeout, Job: os.Getenv("CROWSNEST_JOB")}, strings.Join(args, " "))
	if errors.Is(err, throttle.ErrHeavyTimeout) {
		return exitCodeError{code: exitHeavyTimeout, message: fmt.Sprintf("crowsnest heavy: no slot came free within %s; nothing was run", timeout)}
	}
	if err != nil {
		return err
	}
	defer slot.Release()
	env = append(env, fmt.Sprintf("%s=%d.%d", throttle.HolderEnv, os.Getpid(), slot.Index()))
	return execCommand(context.WithoutCancel(ctx), throttle.NiceArgs(args, exec.LookPath), env)
}

// execCommand runs argv with the terminal's stdio and returns its exit status. Interrupts reach
// the child through the shared process group; this process waits for it rather than leaving it.
func execCommand(ctx context.Context, argv []string, env []string) error {
	child := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // the human's or agent's own command, by design
	child.Stdin, child.Stdout, child.Stderr, child.Env = os.Stdin, os.Stdout, os.Stderr, env
	err := child.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitCodeError{code: exitErr.ExitCode()}
	}
	if err != nil {
		return fmt.Errorf("running %s: %w", argv[0], err)
	}
	return nil
}
