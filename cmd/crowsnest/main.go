// Command crowsnest runs a herdr session and its orchestrator from a browser. This file is the
// Cobra root: `crowsnest` with no subcommand is `crowsnest serve`. Every configuration flag is
// persistent on the root so `serve` and `health` resolve the same config the same way.
//
// Exit codes: 0 success, 1 runtime error, 2 usage error (go-cli house style), and 3 from
// `ask --wait` when the question expired or was cancelled (DESIGN.md section 9).
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jclement/crowsnest/internal/config"
)

// Stamped by -ldflags at build time (mise run build).
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

// usageError marks a mistake in how the command was invoked (exit code 2).
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

func main() {
	root := newRootCommand()
	if err := root.Execute(); err != nil {
		if err.Error() != "" {
			printError(err)
		}
		os.Exit(exitCodeFor(err))
	}
}

// exitCodeFor maps a command's error to the process exit code.
func exitCodeFor(err error) int {
	var exit exitCodeError
	var usage usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.code
	case errors.As(err, &usage):
		return 2
	default:
		return 1
	}
}

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "crowsnest",
		Short:         "Run a herdr session and its orchestrator from a browser",
		Long:          "Crowsnest starts a herdr session and a Claude Code orchestrator in it, and serves a\nmobile-friendly web app for chatting with it, answering questions and driving terminals.\n\nWith no subcommand, crowsnest runs `serve`.",
		Version:       versionLine(),
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE:          runServe,
	}
	root.SetVersionTemplate("crowsnest {{.Version}}\n")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	config.RegisterFlags(root.PersistentFlags())
	root.AddCommand(newServeCommand(), newVersionCommand(), newHealthCommand(),
		newAskCommand(), newQuestionCommand(), newNotifyCommand(), newSayCommand(), newMCPCommand(), newJobCommand(), newProjectCommand(), newHookCommand(), newUpdateCommand(),
		newResetPasswordCommand(), newResetSetupCommand(), newHeavyCommand(), newPauseCommand(), newResumeCommand())
	return root
}

// versionLine is "v1.2.3 (abc1234, 2026-01-15T10:30:00Z)".
func versionLine() string {
	return fmt.Sprintf("%s (%s, %s)", version, commit, buildDate)
}

// printError shows an error on stderr, in red on a terminal.
func printError(err error) {
	if stderrIsTerminal() {
		fmt.Fprintf(os.Stderr, "\x1b[1;31merror:\x1b[0m %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
}
