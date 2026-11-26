// Self-update (DESIGN.md section 16.1): `crowsnest update`, and the selfUpdater that serve hands
// to the HTTP server for /health's update_available and POST /api/v1/admin/update. The checking,
// verifying and installing are internal/update's; this file decides when they apply.

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/jclement/crowsnest/internal/update"
)

// selfUpdater ties a background Checker to Apply for the running binary.
type selfUpdater struct {
	client  *update.Client
	checker *update.Checker
	current string
	restart func()
}

// newSelfUpdater returns the updater for serve, already checking in the background, or nil when
// self-update does not apply: --demo (hermetic, never calls out), a development build, or a
// container. restart ends serve after a successful update.
func newSelfUpdater(ctx context.Context, demo bool, logger *slog.Logger, restart func()) *selfUpdater {
	if demo || update.Unsupported(version) != nil {
		return nil
	}
	client := update.NewClient(version)
	checker := update.NewChecker(client, version, logger)
	checker.Start(ctx)
	return &selfUpdater{client: client, checker: checker, current: version, restart: restart}
}

// Available is the newer release the background check has seen.
func (u *selfUpdater) Available() (update.Release, bool) { return u.checker.Available() }

// Apply asks GitHub again (the background result may be a day old), then verifies and installs
// a newer release over the running binary.
func (u *selfUpdater) Apply(ctx context.Context) (update.Release, error) {
	return applyLatest(ctx, u.client, u.current)
}

// Restart ends serve so a supervisor starts the new binary.
func (u *selfUpdater) Restart() { u.restart() }

// applyLatest is the one path both the CLI and the HTTP route take.
func applyLatest(ctx context.Context, client *update.Client, current string) (update.Release, error) {
	if refusal := update.Unsupported(current); refusal != nil {
		return update.Release{}, refusal
	}
	execPath, err := update.Executable()
	if err != nil {
		return update.Release{}, err
	}
	latest, err := client.Latest(ctx)
	if err != nil {
		return update.Release{}, err
	}
	if !update.Newer(latest.Version, current) {
		return update.Release{}, &update.Refusal{Reason: fmt.Sprintf("crowsnest %s is already the latest release", current)}
	}
	if err := client.Apply(ctx, latest, execPath); err != nil {
		return update.Release{}, err
	}
	return latest, nil
}

// ---- crowsnest update ----------------------------------------------------------------------

func newUpdateCommand() *cobra.Command {
	var checkOnly bool
	command := &cobra.Command{
		Use:   "update",
		Short: "Install the latest release over this binary, after verifying its signature",
		Long: "Asks GitHub for the latest crowsnest release, verifies checksums.txt against its\n" +
			"Sigstore signature from this repository's release workflow, checks the archive's\n" +
			"SHA-256, and replaces this binary. Nothing is installed unless every check passes.\n" +
			"Refused inside a container (pull a new image), for a development build, and for a\n" +
			"Homebrew install (brew upgrade crowsnest). A running `crowsnest serve` keeps the old\n" +
			"version until it is restarted.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdate(cmd.Context(), cmd.OutOrStdout(), update.NewClient(version), version, checkOnly)
		},
	}
	command.Flags().BoolVar(&checkOnly, "check", false, "only report whether a newer release exists (exit 0 either way)")
	return command
}

func runUpdate(ctx context.Context, out io.Writer, client *update.Client, current string, checkOnly bool) error {
	if checkOnly {
		if refusal := update.Unsupported(current); refusal != nil {
			return refusal
		}
		latest, err := client.Latest(ctx)
		if err != nil {
			return err
		}
		if update.Newer(latest.Version, current) {
			_, err = fmt.Fprintf(out, "crowsnest %s is available (running %s): %s\nrun `crowsnest update` to install it\n", latest.TagName, current, latest.URL)
			return err
		}
		_, err = fmt.Fprintf(out, "crowsnest %s is the latest release\n", current)
		return err
	}
	installed, err := applyLatest(ctx, client, current)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "updated crowsnest %s -> %s (signature and checksum verified)\nrestart a running `crowsnest serve` to use it\n", current, installed.TagName)
	return err
}
