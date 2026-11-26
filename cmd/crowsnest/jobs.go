// `crowsnest job …` and `crowsnest hook <event>` (DESIGN.md sections 6.2, 6.3 and 9): the job
// protocol for agents without MCP (Codex) and for scripts, and the Claude Code hook entry point.
//
// The job is --job, else CROWSNEST_JOB. `hook` fails open: whatever goes wrong (no socket,
// crowsnest down, a slow answer, bad input), it prints nothing and exits 0, because crowsnest
// being down must never wedge an agent.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jclement/crowsnest/internal/agentapi"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
)

// hookTimeout bounds one hook round trip (DESIGN.md 6.3).
var hookTimeout = 5 * time.Second

// maxHookInput caps what a hook reads from stdin.
const maxHookInput = 4 << 20

// ---- job -----------------------------------------------------------------------------------

func newJobCommand() *cobra.Command {
	var jobID string
	command := &cobra.Command{
		Use:   "job",
		Short: "The job protocol: update, done, fail, get, create, list (agent side)",
		Long: "Report on the job this pane holds (CROWSNEST_JOB, or --job j_<n>): what you are doing now,\n" +
			"that it is done (with evidence) or failed, or read it back. Leads delegate with create.",
	}
	command.PersistentFlags().StringVar(&jobID, "job", "", "the job id (default: CROWSNEST_JOB)")
	command.AddCommand(newJobUpdateCommand(&jobID), newJobDoneCommand(&jobID), newJobFailCommand(&jobID),
		newJobGetCommand(&jobID), newJobCreateCommand(), newJobListCommand())
	return command
}

// jobClient returns the socket client and the resolved job id.
func jobClient(explicit string) (*agentapi.Client, string, error) {
	client, err := agentClient()
	if err != nil {
		return nil, "", err
	}
	id, err := client.JobID(explicit)
	if err != nil {
		return nil, "", usageError{err}
	}
	return client, id, nil
}

func printJob(out io.Writer, job jobs.Job) error {
	_, err := fmt.Fprintf(out, "%s %s\n", job.ID, job.State)
	return err
}

func newJobUpdateCommand(jobID *string) *cobra.Command {
	var state, reason string
	var waiting bool
	command := &cobra.Command{
		Use:   `update "what you are doing now" [--state blocked --reason "…"] [--waiting]`,
		Short: "Heartbeat with a one-line progress; optionally declare blocked, running or waiting",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, id, err := jobClient(*jobID)
			if err != nil {
				return err
			}
			input := jobs.UpdateInput{State: state, Reason: reason}
			if len(args) == 1 {
				input.Progress = args[0]
			}
			if cmd.Flags().Changed("waiting") {
				input.Waiting = &waiting
			}
			job, err := client.UpdateJob(cmd.Context(), id, input)
			if err != nil {
				return err
			}
			return printJob(cmd.OutOrStdout(), job)
		},
	}
	command.Flags().StringVar(&state, "state", "", "blocked or running")
	command.Flags().StringVar(&reason, "reason", "", "with --state blocked: what you are waiting on")
	command.Flags().BoolVar(&waiting, "waiting", false, "nothing is left that your open questions do not block")
	return command
}

func newJobDoneCommand(jobID *string) *cobra.Command {
	var result, resultFile string
	var verified, notVerified []string
	command := &cobra.Command{
		Use:   `done --result "…" --verified "…" --not-verified "…"`,
		Short: "Declare the job finished, with evidence (repeat the list flags; 'none' for an empty list)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, id, err := jobClient(*jobID)
			if err != nil {
				return err
			}
			if resultFile != "" {
				body, err := readInput(cmd.InOrStdin(), resultFile)
				if err != nil {
					return err
				}
				result = string(body)
			}
			if !cmd.Flags().Changed("verified") || !cmd.Flags().Changed("not-verified") {
				return usageError{fmt.Errorf("--verified and --not-verified are both required (pass 'none' for an empty list)")}
			}
			job, err := client.DoneJob(cmd.Context(), id, jobs.DoneInput{ResultMD: result, Verified: listFlag(verified), NotVerified: listFlag(notVerified)})
			if err != nil {
				return err
			}
			return printJob(cmd.OutOrStdout(), job)
		},
	}
	command.Flags().StringVar(&result, "result", "", "what was done, in Markdown")
	command.Flags().StringVar(&resultFile, "result-file", "", "read the result from a file (- for stdin)")
	command.Flags().StringArrayVar(&verified, "verified", nil, "something you checked and saw (repeatable)")
	command.Flags().StringArrayVar(&notVerified, "not-verified", nil, "something you did not check (repeatable)")
	return command
}

// listFlag turns a repeated flag into a list; "none" alone means empty.
func listFlag(values []string) []string {
	out := []string{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && !strings.EqualFold(value, "none") {
			out = append(out, value)
		}
	}
	return out
}

func newJobFailCommand(jobID *string) *cobra.Command {
	return &cobra.Command{
		Use:   `fail "reason"`,
		Short: "Declare that the job cannot be finished",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, id, err := jobClient(*jobID)
			if err != nil {
				return err
			}
			job, err := client.FailJob(cmd.Context(), id, args[0])
			if err != nil {
				return err
			}
			return printJob(cmd.OutOrStdout(), job)
		},
	}
}

func newJobGetCommand(jobID *string) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "get [j_<n>]",
		Short: "Print the job: brief, state, progress, questions and answers",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			explicit := *jobID
			if len(args) == 1 {
				explicit = args[0]
			}
			client, id, err := jobClient(explicit)
			if err != nil {
				return err
			}
			if asJSON {
				detail, err := client.GetJob(cmd.Context(), id)
				if err != nil {
					return err
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(detail)
			}
			text, err := client.JobContext(cmd.Context(), id)
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), text)
			return err
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "print the full record with its timeline as JSON")
	return command
}

func newJobCreateCommand() *cobra.Command {
	var input jobs.CreateInput
	var briefFile string
	var isolate bool
	command := &cobra.Command{
		Use:   `create "brief" [--dir PATH] [--role builder] [--done-when "…"] [--engine claude|codex]`,
		Short: "Delegate a child job (the caller's job becomes its parent)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := agentClient()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				input.Brief = args[0]
			}
			if briefFile != "" {
				body, err := readInput(cmd.InOrStdin(), briefFile)
				if err != nil {
					return err
				}
				input.Brief = string(body)
			}
			if cmd.Flags().Changed("isolate") {
				input.Isolate = &isolate
			}
			job, err := client.CreateJob(cmd.Context(), input)
			if err != nil {
				return err
			}
			return printJob(cmd.OutOrStdout(), job)
		},
	}
	command.Flags().StringVar(&briefFile, "brief-file", "", "read the brief from a file (- for stdin)")
	command.Flags().StringVar(&input.Dir, "dir", "", "absolute directory or git repo to work in")
	command.Flags().StringVar(&input.Role, "role", "", "builder, reviewer, tester, lead, or a custom role")
	command.Flags().StringVar(&input.DoneWhen, "done-when", "", "the definition of done")
	command.Flags().StringVar(&input.Engine, "engine", "", "claude (default) or codex")
	command.Flags().StringVar(&input.Model, "model", "", "model name")
	command.Flags().BoolVar(&isolate, "isolate", false, "force a git worktree on or off")
	return command
}

func newJobListCommand() *cobra.Command {
	var state string
	command := &cobra.Command{
		Use:   "list [--state running,needs_you]",
		Short: "List jobs, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := agentClient()
			if err != nil {
				return err
			}
			list, err := client.ListJobs(cmd.Context(), state)
			if err != nil {
				return err
			}
			for _, job := range list {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", job.ID, job.State, job.Role, job.Title); err != nil {
					return err
				}
			}
			return nil
		},
	}
	command.Flags().StringVar(&state, "state", "", "comma-separated states to show")
	return command
}

func readInput(stdin io.Reader, file string) ([]byte, error) {
	if file == "-" {
		return io.ReadAll(io.LimitReader(stdin, questions.MaxRequestBytes))
	}
	body, err := os.ReadFile(file) //nolint:gosec // the agent names its own file
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}
	return body, nil
}

// ---- hook ----------------------------------------------------------------------------------

func newHookCommand() *cobra.Command {
	var jobID, socket string
	command := &cobra.Command{
		Use:   "hook <event>",
		Short: "Claude Code hook entry point (crowsnest writes it into the settings it starts agents with)",
		Long: "Reads the hook's JSON on stdin, sends it to crowsnest over the agent socket with a 5 s timeout,\n" +
			"and prints crowsnest's reply. Events: stop, session-start, notification, stop-failure,\n" +
			"permission-denied, session-end, post-tool-use. If crowsnest cannot be reached, or anything\n" +
			"else goes wrong, it prints nothing and exits 0: crowsnest being down never wedges an agent.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return nil // fail open even on a malformed command line
			}
			runHook(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), os.Getenv, args[0], jobID, socket)
			return nil
		},
	}
	command.Flags().StringVar(&jobID, "job", "", "the job this agent holds (baked into the job's settings.json)")
	command.Flags().StringVar(&socket, "socket", "", "the agent socket (default: CROWSNEST_SOCKET)")
	command.FParseErrWhitelist.UnknownFlags = true // a newer crowsnest's settings must not break an older binary
	return command
}

// runHook does the hook's work and swallows every failure (fail open).
func runHook(ctx context.Context, stdin io.Reader, stdout io.Writer, getenv func(string) string, event, jobID, socket string) {
	input, err := io.ReadAll(io.LimitReader(stdin, maxHookInput))
	if err != nil || !json.Valid(input) {
		return
	}
	if socket == "" {
		socket = getenv(agentapi.EnvSocket)
	}
	if socket == "" {
		return
	}
	if jobID == "" {
		jobID = getenv(agentapi.EnvJob)
	}
	client := agentapi.NewClient(socket, questions.Caller{PaneID: getenv(agentapi.EnvPane), JobID: jobID})
	ctx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()
	reply, err := client.Hook(ctx, event, input)
	if err != nil || reply == "" {
		return
	}
	_, _ = io.WriteString(stdout, reply)
}
