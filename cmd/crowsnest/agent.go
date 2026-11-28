// The agent-side subcommands (DESIGN.md section 9): `ask`, `question`, `notify`, `say` and `mcp`.
// They run inside agent panes, talk to the unix socket named by CROWSNEST_SOCKET, and identify the
// caller with CROWSNEST_TOKEN, which crowsnest gave the agent (the server maps it to the pane and
// job). `job` and `hook` join them in M7.
//
// Exit codes: 0 success (for `ask --wait`: answered), 3 for `ask --wait` when the question expired
// or was cancelled, 1 for any error, 2 for a usage error.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jclement/crowsnest/internal/agentapi"
	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/questions"
)

// exitUnanswered is `ask --wait`'s exit code for an expired or cancelled question.
const exitUnanswered = 3

// exitCodeError carries a specific exit code with an optional message.
type exitCodeError struct {
	code    int
	message string
}

func (e exitCodeError) Error() string { return e.message }

// agentClient builds the socket client from the environment.
func agentClient() (*agentapi.Client, error) {
	return agentapi.NewClientFromEnv(os.Getenv)
}

// ---- ask -----------------------------------------------------------------------------------

func newAskCommand() *cobra.Command {
	var file string
	var wait bool
	var timeout time.Duration
	var attachments []string
	command := &cobra.Command{
		Use:   "ask --file q.json|- [--wait] [--timeout 12h] [--attach file]",
		Short: "Ask the human a question (agent side)",
		Long: "Posts a question from a JSON file (or - for stdin) to the inbox. The shape is Meat Prompt's\n" +
			"plus multi: title, body_md, kind (confirm|choice|ack|manual|notice), multi, options\n" +
			"[{key,label,detail}], recommend, context, urgency (low|normal|blocking), expires_in_hours, files,\n" +
			"attachments (paths of files to show as they are: inside your working or job directory).\n\n" +
			"Without --wait it prints the question id and returns; the answer is typed into this pane later\n" +
			"as a line starting [crowsnest answer q_<n>]. With --wait it blocks until the question settles\n" +
			"and prints {status, answer_value, answer_note}: exit 0 answered, 3 expired or cancelled, 1 error.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" {
				return usageError{errors.New("--file is required (a JSON file, or - for stdin)")}
			}
			input, err := readAskInput(cmd.InOrStdin(), file)
			if err != nil {
				return err
			}
			input.Attachments = append(input.Attachments, attachments...)
			return runAsk(cmd.Context(), cmd.OutOrStdout(), input, wait, timeout)
		},
	}
	command.Flags().StringVarP(&file, "file", "f", "", "question JSON file, or - for stdin")
	command.Flags().BoolVar(&wait, "wait", false, "block until the question is answered, expired or cancelled")
	command.Flags().DurationVar(&timeout, "timeout", 0, "with --wait: give up after this long (also sets the question's expiry when the file sets none)")
	command.Flags().StringArrayVar(&attachments, "attach", nil, "a file to show with the question, inside your working or job directory (repeatable)")
	return command
}

func readAskInput(stdin io.Reader, file string) (questions.AskInput, error) {
	reader := stdin
	if file != "-" {
		opened, err := os.Open(file) //nolint:gosec // the agent names its own question file
		if err != nil {
			return questions.AskInput{}, fmt.Errorf("reading the question file: %w", err)
		}
		defer func() { _ = opened.Close() }()
		reader = opened
	}
	var input questions.AskInput
	decoder := json.NewDecoder(io.LimitReader(reader, questions.MaxRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return questions.AskInput{}, usageError{fmt.Errorf("the question is not valid JSON in the expected shape: %w", err)}
	}
	return input, nil
}

// AskResult is what `ask --wait` prints.
type AskResult struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	AnswerValue any    `json:"answer_value"`
	AnswerNote  string `json:"answer_note"`
}

func runAsk(ctx context.Context, out io.Writer, input questions.AskInput, wait bool, timeout time.Duration) error {
	client, err := agentClient()
	if err != nil {
		return err
	}
	input.Wait = wait
	if wait && timeout > 0 && input.ExpiresInHours == 0 {
		// The server refuses an expiry under five minutes; a shorter --timeout still gives up
		// locally at its own time.
		input.ExpiresInHours = max(timeout.Hours(), questions.MinExpiryHours)
	}
	question, err := client.Ask(ctx, input)
	if err != nil {
		return err
	}
	if !wait {
		_, err := fmt.Fprintln(out, question.ID)
		return err
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		// A little past the expiry, so the server's own expiry is what normally ends the wait.
		ctx, cancel = context.WithTimeout(ctx, timeout+time.Minute)
		defer cancel()
	}
	settled, err := client.WaitSettled(ctx, question.ID)
	if errors.Is(err, context.DeadlineExceeded) {
		return exitCodeError{code: exitUnanswered, message: fmt.Sprintf("%s was not answered within %s", question.ID, timeout)}
	}
	if err != nil {
		return err
	}
	if err := json.NewEncoder(out).Encode(AskResult{ID: settled.ID, Status: string(settled.Status), AnswerValue: settled.AnswerValue, AnswerNote: settled.AnswerNote}); err != nil {
		return err
	}
	if settled.Status != questions.StatusAnswered {
		return exitCodeError{code: exitUnanswered}
	}
	return nil
}

// ---- question ------------------------------------------------------------------------------

func newQuestionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "question <id>",
		Short: "Print a question you asked, with its whole answer (agent side)",
		Long: "Prints {id, status, title, answer_value, answer_labels, answer_note} as JSON. Use it when a\n" +
			"[crowsnest answer q_<n>] line says the answer is too long for one line: the note, which\n" +
			"outranks the choice, is never shortened here. It does not wait; see ask --wait for that.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := agentClient()
			if err != nil {
				return err
			}
			question, err := client.Get(cmd.Context(), args[0], 0)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(agentapi.AnswerResult{
				ID: question.ID, Status: string(question.Status), Title: question.Title, AnswerValue: question.AnswerValue,
				AnswerLabels: questions.AnswerLabelList(question), AnswerNote: question.AnswerNote,
			})
		},
	}
}

// ---- notify --------------------------------------------------------------------------------

func newNotifyCommand() *cobra.Command {
	var urgency string
	command := &cobra.Command{
		Use:   `notify "text" [--urgency low|normal|blocking]`,
		Short: "Tell the human something; no answer needed (agent side)",
		Long:  "Posts a notice to the inbox: the first line is its title, any further lines its body. Prints the id.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := agentClient()
			if err != nil {
				return err
			}
			title, body, _ := strings.Cut(strings.TrimSpace(args[0]), "\n")
			question, err := client.Ask(cmd.Context(), questions.AskInput{
				Title: title, BodyMD: strings.TrimSpace(body), Kind: questions.KindNotice, Urgency: questions.Urgency(urgency),
			})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), question.ID)
			return err
		},
	}
	command.Flags().StringVar(&urgency, "urgency", "normal", "low, normal or blocking (blocking also notifies --notify-url)")
	return command
}

// ---- say -----------------------------------------------------------------------------------

func newSayCommand() *cobra.Command {
	return &cobra.Command{
		Use:   `say "sentence"`,
		Short: "Speak a sentence in talk mode; a no-op when it is off (agent side)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := agentClient()
			if err != nil {
				return err
			}
			return client.Say(cmd.Context(), args[0])
		},
	}
}

// ---- mcp -----------------------------------------------------------------------------------

func newMCPCommand() *cobra.Command {
	var flags mcpFlags
	command := &cobra.Command{
		Use:   "mcp [--socket path] [--job id] [--token-file path]",
		Short: "Serve crowsnest's agent tools over MCP on stdio (Claude Code and Codex start this)",
		Long: "A stdio MCP server exposing ask, get_question, say and the job tools, proxied to the agent socket\n" +
			"named by --socket or CROWSNEST_SOCKET. Claude Code starts it from the --mcp-config file crowsnest\n" +
			"writes; a Codex job starts it as its MCP server `crowsnest`, outside Codex's sandbox, which is how a\n" +
			"Codex job reaches crowsnest with the sandbox's network off (DESIGN.md 20.1). --token-file names the\n" +
			"file holding the agent's token, so the token never appears on a command line.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := flags.client(os.Getenv)
			if err != nil {
				return err
			}
			return agentapi.RunMCP(cmd.Context(), client, version)
		},
	}
	command.Flags().StringVar(&flags.socket, "socket", "", "the agent socket (default: CROWSNEST_SOCKET)")
	command.Flags().StringVar(&flags.job, "job", "", "the job this agent holds (default: CROWSNEST_JOB)")
	command.Flags().StringVar(&flags.tokenFile, "token-file", "", "the file holding this agent's token (default: CROWSNEST_TOKEN)")
	return command
}

// mcpFlags are `crowsnest mcp`'s flags; each falls back to the agent's environment.
type mcpFlags struct {
	socket, job, tokenFile string
}

// client is the agent socket client the flags name. A token file that cannot be read is an
// error, not a silent fall back to the environment: the server would ignore every call.
func (f mcpFlags) client(getenv func(string) string) (*agentapi.Client, error) {
	socket := f.socket
	if socket == "" {
		socket = getenv(agentapi.EnvSocket)
	}
	if socket == "" {
		return nil, agentapi.ErrNoSocket
	}
	token := getenv(agentapi.EnvToken)
	if f.tokenFile != "" {
		fromFile, err := agentid.ReadTokenFile(f.tokenFile)
		if err != nil {
			return nil, fmt.Errorf("reading the agent's token file: %w", err)
		}
		token = fromFile
	}
	job := f.job
	if job == "" {
		job = getenv(agentapi.EnvJob)
	}
	return agentapi.NewClient(socket, token).WithJob(job), nil
}
