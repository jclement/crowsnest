// `crowsnest forget-scratch <dir>…` (hidden): for crowsnest's own real-agent test suites. Real
// Claude and real Codex run with the user's login, so they record their folders in the user's own
// ~/.claude.json and ~/.codex/config.toml; the browser rig calls this at teardown for its scratch
// folder and its job worktrees' folder, and exactly the records at or under those are removed, with
// the same careful update as a trust write (internal/agenttrust/forget.go). It refuses home, its
// ancestors and shallow paths. Go suites call agenttrust.ForgetScratch directly.

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jclement/crowsnest/internal/agenttrust"
)

func newForgetScratchCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "forget-scratch <dir>…",
		Short:  "Remove the agents' records of a test's scratch folders (for crowsnest's own test suites)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, dir := range args {
				note, err := agenttrust.ForgetScratch(dir)
				if err != nil {
					return fmt.Errorf("%s: %w", dir, err)
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), note)
			}
			return nil
		},
	}
}
