// `crowsnest reset-password` and `crowsnest reset-setup` (DESIGN.md 4.14): the way back in for
// whoever can run commands on the machine. Write access to the database is the authority, so
// neither asks for the old password; both sign every session out.

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/store"
)

func newResetPasswordCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "reset-password",
		Short: "Set a new password for the owner chosen at setup, and sign every session out",
		Long: "Sets a new password for the login stored at first-run setup. The new password comes from\n" +
			"--password-file (first line, mode 600), else from standard input when it is not a terminal,\n" +
			"else it is asked for twice. Every browser is signed out, at once even while crowsnest runs.\n" +
			"Use the same --session and --data-dir as serve. A login given by --username and\n" +
			"CROWSNEST_PASSWORD is not stored, so there is nothing here to reset: change it where it is set.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, db, err := openAccountStore(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = db.Close() }()
			if _, err := auth.LoadOwner(cmd.Context(), db); errors.Is(err, auth.ErrNoOwner) {
				return errors.New("no owner is stored: crowsnest has not been set up. Start it and open the setup link from its log")
			}
			password, err := newPasswordInput(cmd)
			if err != nil {
				return err
			}
			username, err := auth.ResetPassword(cmd.Context(), db, password, time.Now())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Password changed for %s; every session is signed out.\n", username)
			if cfg.Username != "" {
				_, _ = fmt.Fprintln(out, "Note: --username/CROWSNEST_USERNAME is set here, and a configured login wins over the stored one.")
			}
			return nil
		},
	}
}

func newResetSetupCommand() *cobra.Command {
	var yes, force bool
	command := &cobra.Command{
		Use:   "reset-setup",
		Short: "Remove the stored owner and every session, so the next start is first-run setup again",
		Long: "Removes the login stored at setup and signs every session out. The next start of serve is\n" +
			"locked in setup mode and prints a new setup link. Refused while crowsnest is running\n" +
			"(stop it first) unless --force: a running crowsnest then lets nobody sign in until it is\n" +
			"restarted into setup mode. The instance name is kept.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, db, err := openAccountStore(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = db.Close() }()
			running := serverRunning(cmd.Context(), cfg)
			if running != "" && !force {
				return fmt.Errorf("crowsnest is running (%s). Stop it first, or pass --force: it then lets nobody sign in until you restart it", running)
			}
			if !yes {
				if err := confirm(cmd, "This removes the stored login and signs every browser out. Type \"reset\" to go on: ", "reset"); err != nil {
					return err
				}
			}
			existed, err := auth.ResetSetup(cmd.Context(), db, time.Now())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if !existed {
				_, _ = fmt.Fprintln(out, "No owner was stored; sessions cleared. The next start is setup.")
			} else {
				_, _ = fmt.Fprintln(out, "The stored login is removed and every session signed out.")
			}
			if running != "" {
				_, _ = fmt.Fprintln(out, "Restart crowsnest now: it starts in setup mode and prints a new setup link. Until then nobody can sign in.")
			} else {
				_, _ = fmt.Fprintln(out, "Start crowsnest: it prints a setup link to choose the login again.")
			}
			if cfg.Username != "" {
				_, _ = fmt.Fprintln(out, "Note: --username/CROWSNEST_USERNAME is set here, so serve will use that login rather than setup.")
			}
			return nil
		},
	}
	command.Flags().BoolVar(&yes, "yes", false, "do not ask for confirmation")
	command.Flags().BoolVar(&force, "force", false, "go ahead even though crowsnest is running")
	return command
}

// openAccountStore resolves the config the way serve does (same --session, --data-dir, config
// file) and opens the existing database. --password-file is this command's input, the new
// password, so it is kept out of the config (where it would mean the configured login).
func openAccountStore(cmd *cobra.Command) (config.Config, *store.Store, error) {
	flags := pflag.NewFlagSet(cmd.Name(), pflag.ContinueOnError)
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if flag.Name != "password-file" {
			flags.AddFlag(flag)
		}
	})
	cfg, err := config.Load(flags, config.Options{})
	if err != nil {
		return config.Config{}, nil, err
	}
	if cfg.Demo {
		return config.Config{}, nil, errors.New("--demo keeps no database between runs; there is nothing to reset")
	}
	path := cfg.DatabasePath()
	if _, err := os.Stat(path); err != nil {
		return config.Config{}, nil, fmt.Errorf("no crowsnest database at %s (check --session and --data-dir): %w", path, err)
	}
	db, err := store.Open(cmd.Context(), path)
	if err != nil {
		return config.Config{}, nil, fmt.Errorf("opening %s: %w", path, err)
	}
	return cfg, db, nil
}

// newPasswordInput reads the new password: --password-file, else a piped stdin's first line,
// else asked for twice on the terminal.
func newPasswordInput(cmd *cobra.Command) (string, error) {
	if flag := cmd.Flags().Lookup("password-file"); flag != nil && flag.Changed {
		return config.ReadPasswordFile(flag.Value.String())
	}
	stdin, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(int(stdin.Fd())) {
		line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("reading the new password from standard input: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	errOut := cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(errOut, "New password (at least %d characters): ", auth.MinPasswordLength)
	first, err := term.ReadPassword(int(stdin.Fd()))
	_, _ = fmt.Fprintln(errOut)
	if err != nil {
		return "", err
	}
	_, _ = fmt.Fprint(errOut, "Again: ")
	second, err := term.ReadPassword(int(stdin.Fd()))
	_, _ = fmt.Fprintln(errOut)
	if err != nil {
		return "", err
	}
	if string(first) != string(second) {
		return "", errors.New("the two passwords differ; nothing was changed")
	}
	return string(first), nil
}

// confirm asks on a terminal for the word; without a terminal it refuses and names --yes.
func confirm(cmd *cobra.Command, prompt, word string) error {
	stdin, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(int(stdin.Fd())) {
		return errors.New("not a terminal: pass --yes to confirm")
	}
	_, _ = fmt.Fprint(cmd.ErrOrStderr(), prompt)
	answer, _ := bufio.NewReader(stdin).ReadString('\n')
	if strings.TrimSpace(answer) != word {
		return errors.New("not confirmed; nothing was changed")
	}
	return nil
}

// serverRunning names how a running crowsnest for this config was seen ("" when none was): its
// agent socket accepts a connection, or its health endpoint answers. The locked setup server
// has no agent socket, but answers health.
func serverRunning(ctx context.Context, cfg config.Config) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	socket := agentSocketPath(cfg)
	var dialer net.Dialer
	if conn, err := dialer.DialContext(ctx, "unix", socket); err == nil {
		_ = conn.Close()
		return "its agent socket " + socket + " answers"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL(cfg), nil)
	if err != nil {
		return ""
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		return ""
	}
	_ = response.Body.Close()
	return healthURL(cfg) + " answers"
}
