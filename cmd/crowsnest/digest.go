// `crowsnest digest submit` (DESIGN.md 7.13): how the orchestrator delivers the daily digest it
// was asked to write. It runs in the orchestrator's pane and needs its token.

package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newDigestCommand() *cobra.Command {
	command := &cobra.Command{Use: "digest", Short: "The daily digest (agent side)"}
	var day, file string
	submit := &cobra.Command{
		Use:   "submit --file digest.md [--day YYYY-MM-DD]",
		Short: "Deliver the digest crowsnest asked you to write",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" {
				return usageError{fmt.Errorf("--file is required (- for stdin)")}
			}
			body, err := readInput(cmd.InOrStdin(), file)
			if err != nil {
				return err
			}
			client, err := agentClient()
			if err != nil {
				return err
			}
			if err := client.SubmitDigest(cmd.Context(), day, string(body)); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "delivered: the digest is in the human's Inbox")
			return err
		},
	}
	submit.Flags().StringVar(&file, "file", "", "the digest in Markdown (- for stdin)")
	submit.Flags().StringVar(&day, "day", "", "the day it covers (default: the one crowsnest asked for)")
	command.AddCommand(submit)
	return command
}
