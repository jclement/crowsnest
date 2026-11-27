// `crowsnest open`: open the running instance in the browser, with the same opener rule as
// serve's --open (internal/opener). It prints the URL either way, and says when crowsnest is not
// running or is waiting for first-run setup (whose one-time link is only in serve's log).

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/jclement/crowsnest/internal/doctor"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/opener"
)

func newOpenCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "open",
		Short: "Open the running crowsnest in your browser",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			cfg, err := loadConfig(ctx, cmd.Flags())
			if err != nil {
				return err
			}
			if len(cfg.Listeners) == 0 {
				return usageError{fmt.Errorf("no listen address is configured")}
			}
			scheme := "http"
			if cfg.TLS() {
				scheme = "https"
			}
			base := browsableBase(scheme + "://" + cfg.Listeners[0].Addr.String())
			out := cmd.OutOrStdout()
			inputs := doctor.Inputs{Config: cfg, Version: version}
			report := doctor.Run(ctx, inputs)
			switch {
			case report.Live == nil:
				return exitCodeError{code: 1, message: fmt.Sprintf("crowsnest is not running at %s; start it with `crowsnest`", base)}
			case report.Live.SetupRequired:
				_, _ = fmt.Fprintf(out, "crowsnest at %s is waiting for first-run setup: open the setup link from its log (it carries a one-time token).\n", base)
				return nil
			}
			// Asked for explicitly, so it counts as interactive; the SSH, display and container
			// rules of --open auto still apply.
			decision := opener.Decide(cfg.Open, opener.Detect(true, doctor.InContainer()))
			_, _ = fmt.Fprintln(out, base)
			if decision.Command == nil {
				_, _ = fmt.Fprintf(out, "(not opening a browser: %s)\n", decision.Reason)
				return nil
			}
			return opener.Open(decision, base+"/", herdr.ScrubEnv(os.Environ()))
		},
	}
}
