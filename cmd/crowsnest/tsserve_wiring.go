// Publishing to the tailnet (DESIGN.md 3.5.1, internal/tsserve): the service's wiring into
// serve, and the hidden `crowsnest tailscale-serve-child` the service runs as its watchdog.
//
// It starts in the normal phase only, after first-run setup: a locked setup server never
// publishes itself, so the one-time setup link is never reachable from the tailnet (a decision:
// every tailnet peer could then race for the token and force rotations, and the owner is at the
// machine anyway to read the link). --demo uses a fake tailscale named by
// CROWSNEST_DEMO_TAILSCALE and nothing else; without it, Tailscale is "not installed" there, so
// the hermetic suite can never reach the real CLI.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"slices"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/httpd"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/tsserve"
)

// envDemoTailscale names the fake tailscale a --demo instance uses (the e2e's).
const envDemoTailscale = "CROWSNEST_DEMO_TAILSCALE"

// newTailscaleServeChildCommand is the watchdog between crowsnest and `tailscale serve`
// (internal/tsserve/child.go). Hidden: only crowsnest itself runs it.
func newTailscaleServeChildCommand() *cobra.Command {
	return &cobra.Command{
		Use:                tsserve.ChildCommand + " -- <tailscale> serve --https=<port> <target>",
		Short:              "internal: keep `tailscale serve` alive exactly as long as crowsnest",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && args[0] == "--" {
				args = args[1:]
			}
			if code := tsserve.RunChild(args, os.Stdin, os.Stdout, os.Stderr); code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}
}

// tailscaleServeWiring is what startTailscaleServe needs from serve.
type tailscaleServeWiring struct {
	cfg     config.Config
	db      *store.Store
	journal *journal.Journal
	server  func() *httpd.Server // nil until the server exists
	addrs   []net.Addr           // the bound listeners (the real port with --port 0)
	logger  *slog.Logger
}

// startTailscaleServe builds the publisher and runs it until ctx ends. The returned wait blocks
// until it has stopped its child: serve defers it, so crowsnest never exits leaving a
// published address behind (the watchdog's lifeline covers a crash; this covers a clean stop).
func startTailscaleServe(ctx context.Context, wiring tailscaleServeWiring) (*tsserve.Service, func()) {
	cfg, logger := wiring.cfg, wiring.logger
	self, err := os.Executable()
	if err != nil {
		logger.Warn("finding crowsnest's own binary for tailscale serve's watchdog", "err", err)
	}
	opts := tsserve.Options{
		FindReal: !cfg.Demo,
		Self:     self,
		SelfArgs: []string{tsserve.ChildCommand, "--"},
		Target:   func() (string, error) { return loopbackTarget(wiring.addrs) },
		Port:     cfg.TailscaleServePort,
		Auth: func() string {
			if cfg.NoAuth {
				return "crowsnest runs with --no-auth (no login), so publishing it would give every device on your tailnet a shell."
			}
			return ""
		},
		AllowHost: func(name string) string { return allowTailnetName(cfg, wiring.server(), name) },
		KV:        wiring.db,
		Logger:    logger.With("component", "tailscale-serve"),
		Journal: func(ctx context.Context, alert bool, summary string) {
			kind := journal.KindSettings
			if alert {
				kind = journal.KindAlert
			}
			wiring.journal.Add(ctx, kind, summary, nil)
		},
		OnPublished: func(url string) {
			if stderrIsTerminal() {
				fmt.Fprintf(os.Stderr, "\n  published on your tailnet: %s\n\n", url)
			}
		},
	}
	if cfg.Demo {
		opts.Binary = os.Getenv(envDemoTailscale)
	}
	service := tsserve.New(opts, cfg.TailscaleServe)
	if cfg.TailscaleServe {
		logger.Info("publishing to your tailnet (--tailscale-serve): the System page shows the address and its state")
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		service.Run(runCtx)
	}()
	return service, func() {
		cancel()
		<-done
	}
}

// loopbackTarget is the URL tailscale serve proxies to: crowsnest's loopback listener, IPv4
// preferred. None (a bind that leaves loopback out) is an error saying so.
func loopbackTarget(addrs []net.Addr) (string, error) {
	var v6 string
	for _, addr := range addrs {
		tcp, ok := addr.(*net.TCPAddr)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(tcp.IP)
		if !ok || !ip.Unmap().IsLoopback() {
			continue
		}
		if ip.Unmap().Is4() {
			return "http://127.0.0.1:" + strconv.Itoa(tcp.Port), nil
		}
		if v6 == "" && ip == netip.IPv6Loopback() {
			v6 = "http://[::1]:" + strconv.Itoa(tcp.Port)
		}
	}
	if v6 != "" {
		return v6, nil
	}
	return "", errors.New("crowsnest listens on no loopback address (its --bind leaves localhost out), and tailscale serve only proxies to this machine's loopback")
}

// allowTailnetName lets the host check accept the MagicDNS name crowsnest publishes under, or
// says why it will not: --no-tailscale-detect asked for the name not to be added.
func allowTailnetName(cfg config.Config, server *httpd.Server, name string) string {
	if server == nil {
		return "crowsnest is still starting."
	}
	if name == "" || slices.Contains(cfg.HostNames, name) || server.HostAllowed(name) {
		return ""
	}
	if cfg.NoTailscaleDetect {
		return "--no-tailscale-detect is on, so crowsnest would refuse requests addressed to " + name + "."
	}
	server.AllowHost(name)
	return ""
}
