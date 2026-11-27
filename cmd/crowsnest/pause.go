// `crowsnest pause` and `crowsnest resume` (DESIGN.md 6.13): the automation's pause switch from
// a shell, through the agent socket. Pausing works from anywhere; resuming is refused to an agent
// (a caller with an agent token), so an agent cannot undo the human's pause. With the server down,
// `pause` writes the paused file (~/.config/crowsnest/paused), which the server honours at start.

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jclement/crowsnest/internal/agentapi"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/pause"
)

func newPauseCommand() *cobra.Command {
	var until, reason string
	var status bool
	command := &cobra.Command{
		Use:   "pause [--until HH:MM] [--reason …] | pause --status",
		Short: "Pause crowsnest's automation (no pokes, nudges, scheduled tasks, new starts or restarts)",
		Long: "Pauses what crowsnest does by itself without stopping any agent: no pokes, no nudges or backstop\n" +
			"prompts, no scheduled tasks, no new job starts, no restarts or retries. Hooks still record, questions\n" +
			"and approvals still arrive, and you can still chat and answer. --until resumes by itself at that\n" +
			"local time (today, or tomorrow when it has passed). If the server is not running, the paused file is\n" +
			"written instead.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, clientErr := humanClient(cmd)
			if status {
				if clientErr != nil {
					return clientErr
				}
				state, err := client.PauseState(cmd.Context())
				if err != nil {
					return err
				}
				return printPause(cmd, state)
			}
			at, err := untilTime(until, time.Now())
			if err != nil {
				return usageError{err}
			}
			if clientErr == nil {
				state, err := client.Pause(cmd.Context(), at, reason)
				if err == nil {
					return printPause(cmd, state)
				}
				if !isUnreachable(err) {
					return err
				}
			}
			file := pausedFile()
			if file == "" {
				return errors.New("crowsnest is not reachable and the config directory is unknown; nothing was paused")
			}
			if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(file, []byte(reason+"\n"), 0o600); err != nil {
				return fmt.Errorf("writing the paused file: %w", err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "crowsnest is not reachable; wrote %s, which pauses it while it exists\n", file)
			return err
		},
	}
	command.Flags().StringVar(&until, "until", "", "HH:MM: resume by itself at this local time")
	command.Flags().StringVar(&reason, "reason", "", "why, shown on the banner")
	command.Flags().BoolVar(&status, "status", false, "show whether the automation is paused")
	return command
}

func newResumeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Resume crowsnest's automation (not from an agent)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := humanClient(cmd)
			if err != nil {
				return err
			}
			state, err := client.Resume(cmd.Context())
			if err != nil {
				return err
			}
			return printPause(cmd, state)
		},
	}
}

// humanClient is the socket client from an agent's pane environment, else, in the human's own
// shell, the socket of the --session in effect (with no token: the human is not an agent).
func humanClient(cmd *cobra.Command) (*agentapi.Client, error) {
	if client, err := agentClient(); err == nil {
		return client, nil
	}
	cfg, err := config.Load(cmd.Flags(), config.Options{})
	if err != nil {
		return nil, fmt.Errorf("finding crowsnest's socket: %w", err)
	}
	return agentapi.NewClient(agentSocketPath(cfg), ""), nil
}

func printPause(cmd *cobra.Command, state pause.State) error {
	out := cmd.OutOrStdout()
	if !state.Paused {
		_, err := fmt.Fprintln(out, "running: the automation is not paused")
		return err
	}
	line := "paused by " + state.By
	if state.At != nil {
		line += " at " + state.At.Local().Format("15:04")
	}
	if state.Until != nil {
		line += ", resumes at " + state.Until.Local().Format("15:04")
	}
	if state.Reason != "" {
		line += ": " + state.Reason
	}
	_, err := fmt.Fprintln(out, line)
	return err
}

// untilTime turns HH:MM into the next such local time ("" is zero: until resumed).
func untilTime(raw string, now time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	clock, err := time.ParseInLocation("15:04", raw, now.Location())
	if err != nil {
		return time.Time{}, fmt.Errorf("--until %q is not a time of day; use HH:MM", raw)
	}
	at := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), 0, 0, now.Location())
	if !at.After(now) {
		at = at.AddDate(0, 0, 1)
	}
	return at, nil
}

// pausedFile is ~/.config/crowsnest/paused (XDG), the front door's switch.
func pausedFile() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return pause.DefaultFile(filepath.Join(dir, "crowsnest"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		return pause.DefaultFile(filepath.Join(home, ".config", "crowsnest"))
	}
	return ""
}

// isUnreachable says the socket did not answer (the server is down), as opposed to a refusal.
func isUnreachable(err error) bool {
	text := err.Error()
	return strings.Contains(text, "connect") || strings.Contains(text, "no such file") || strings.Contains(text, "not reachable") || strings.Contains(text, "refused")
}
