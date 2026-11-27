// The small subcommands: `version` and `health`, plus logging setup shared by all commands.

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"runtime"
	"time"

	charmlog "github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/taglines"
)

// healthTimeout bounds `crowsnest health`; Docker's HEALTHCHECK has its own timeout on top.
const healthTimeout = 5 * time.Second

// ---- version -----------------------------------------------------------------

func newVersionCommand() *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "version",
		Short: "Print version, build and platform details",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return printVersion(cmd.OutOrStdout(), asJSON)
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return command
}

// VersionInfo is `crowsnest version --json`.
type VersionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	Go        string `json:"go"`
	Platform  string `json:"platform"`
	Tagline   string `json:"tagline"`
}

func printVersion(out io.Writer, asJSON bool) error {
	info := VersionInfo{
		Version: version, Commit: commit, BuildDate: buildDate,
		Go: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH, Tagline: taglines.Random(),
	}
	if asJSON {
		return json.NewEncoder(out).Encode(info)
	}
	_, err := fmt.Fprintf(out, "crowsnest %s\n%s %s\n%s\n", versionLine(), info.Go, info.Platform, info.Tagline)
	return err
}

// ---- health ------------------------------------------------------------------

func newHealthCommand() *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "health",
		Short: "Check a running crowsnest answers /api/v1/health (exit 0 healthy, 1 not)",
		Long: "Requests /api/v1/health on the first configured listen address, using the same\n" +
			"--bind, --port and TLS settings as serve. Meant for Docker's HEALTHCHECK.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(cmd.Flags(), config.Options{})
			if err != nil {
				return err
			}
			return checkHealth(cmd.Context(), cmd.OutOrStdout(), cfg, asJSON)
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "print the health response as JSON")
	return command
}

// healthResponse mirrors httpd.HealthResponse inside the envelope.
type healthResponse struct {
	Data struct {
		Status        string `json:"status"`
		Version       string `json:"version"`
		SetupRequired bool   `json:"setup_required"`
	} `json:"data"`
}

func checkHealth(ctx context.Context, out io.Writer, cfg config.Config, asJSON bool) error {
	url := healthURL(cfg)
	client := &http.Client{Timeout: healthTimeout}
	if cfg.TLS() {
		// This probes our own process, often by IP, where the certificate's names will not
		// match. Liveness is the question, not identity.
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // self-probe
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("crowsnest is not answering at %s: %w", url, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	var health healthResponse
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &health) != nil || health.Data.Status != "ok" {
		return fmt.Errorf("crowsnest at %s is unhealthy: HTTP %d", url, response.StatusCode)
	}
	if asJSON {
		_, err = out.Write(body)
		return err
	}
	if health.Data.SetupRequired {
		// Healthy (Docker should not restart it), but nobody can use it until setup is done.
		_, err = fmt.Fprintf(out, "ok %s %s (setup required: open the setup link from crowsnest's log)\n", health.Data.Version, url)
		return err
	}
	_, err = fmt.Fprintf(out, "ok %s %s\n", health.Data.Version, url)
	return err
}

// healthURL targets the first listener; an unspecified bind (0.0.0.0, ::) is reached over the
// matching loopback address.
func healthURL(cfg config.Config) string {
	addr := cfg.Listeners[0].Addr
	if addr.Addr().IsUnspecified() {
		loopback := netip.MustParseAddr("127.0.0.1")
		if addr.Addr().Is6() {
			loopback = netip.IPv6Loopback()
		}
		addr = netip.AddrPortFrom(loopback, addr.Port())
	}
	scheme := "http"
	if cfg.TLS() {
		scheme = "https"
	}
	return scheme + "://" + addr.String() + "/api/v1/health"
}

// ---- logging -----------------------------------------------------------------

// newLogger logs in colour to a terminal and as JSON when piped, both to stderr.
func newLogger() *slog.Logger {
	if stderrIsTerminal() {
		return slog.New(charmlog.NewWithOptions(os.Stderr, charmlog.Options{ReportTimestamp: true, TimeFormat: time.Kitchen}))
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, nil))
}

func stderrIsTerminal() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}

func stdoutIsTerminal() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}
