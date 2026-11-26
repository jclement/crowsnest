// First-run setup in serve (DESIGN.md 3 and 4.14): which login is in effect, and the locked
// start that waits for an owner before anything else runs.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"sync"

	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/httpd"
	"github.com/jclement/crowsnest/internal/opener"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/setup"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/system"
	"github.com/jclement/crowsnest/web"
)

// loginMode is the login a start runs with.
type loginMode string

const (
	loginNone     loginMode = "none"     // --no-auth
	loginConfig   loginMode = "config"   // --username and a password from flags, env or the config file
	loginDatabase loginMode = "database" // the owner chosen at setup, from the database
	loginSetup    loginMode = "setup"    // none of those: locked until setup is completed
)

// decideLogin is the start-up decision table. Flags, the environment and the config file come
// first and are used exactly as before setup existed; then the stored owner; then setup.
func decideLogin(ctx context.Context, cfg config.Config, db *store.Store) (loginMode, error) {
	switch {
	case cfg.NoAuth:
		return loginNone, nil
	case cfg.Username != "":
		return loginConfig, nil
	}
	_, err := auth.LoadOwner(ctx, db)
	switch {
	case err == nil:
		return loginDatabase, nil
	case errors.Is(err, auth.ErrNoOwner):
		return loginSetup, nil
	default:
		return "", err
	}
}

// logLoginMode says at start which login is in effect.
func logLoginMode(logger *slog.Logger, cfg config.Config, mode loginMode) {
	switch mode {
	case loginConfig:
		logger.Info("login: from --username and its password (flags, environment or config file); the stored owner, if any, is not used")
	case loginDatabase:
		logger.Info("login: the owner stored at setup (change it in the Account sheet, or with `crowsnest reset-password`)")
	case loginSetup:
		logger.Warn("no login is configured and none is stored: starting LOCKED in setup mode. Only the setup page is served; " +
			"herdr and the orchestrator start once setup is complete")
		if !cfg.LoopbackOnly() {
			logger.Warn("setup mode on a bind that is not loopback: anyone who can reach this port sees the setup page, " +
				"but only the token printed in this log completes it. Host and origin checks stay on")
		}
	}
}

// storedName applies the instance name saved at setup or in the Account sheet, unless --name or
// CROWSNEST_NAME gave one (those win).
func storedName(ctx context.Context, cfg config.Config, db *store.Store) config.Config {
	if cfg.NameExplicit {
		return cfg
	}
	if name, err := db.GetKV(ctx, store.InstanceNameKey); err == nil && config.ValidName(name) {
		cfg.Name = name
	}
	return cfg
}

// runSetupPhase serves the locked first-run server until setup completes (nil) or ctx ends
// (nil too: the caller checks ctx). Nothing else is started: no herdr, no orchestrator, no agent
// socket, no event stream, no terminal. The server shuts down gracefully, so the response that
// completed setup (and set the session cookie) is delivered before it closes.
func runSetupPhase(ctx context.Context, cfg config.Config, db *store.Store, logger *slog.Logger, hooks serveHooks) error {
	authService, err := auth.New(ctx, auth.Options{}, db)
	if err != nil {
		return err
	}
	announce := &setupAnnouncer{logger: logger, banner: stderrIsTerminal(), onURL: hooks.setupURL, open: hooks.open}
	announce.handoffs = opener.NewHandoffs(announce.target, nil)
	service, err := setup.New(setup.Options{Auth: authService, DefaultName: storedName(ctx, cfg, db).Name, Announce: announce.token})
	if err != nil {
		return err
	}
	events := hub.New()
	defer events.Close()
	deps := httpd.Deps{Config: cfg, Auth: authService, Hub: events, Logger: logger, Version: version, Setup: service, Handoffs: announce.handoffs}
	if !cfg.WebDev {
		deps.Web = web.Dist()
	}
	server, err := httpd.New(deps)
	if err != nil {
		return err
	}
	server.OnListening = announce.url

	serverCtx, stopServer := context.WithCancel(ctx)
	defer stopServer()
	served := make(chan error, 1)
	go func() { served <- server.Run(serverCtx) }()
	select {
	case err := <-served: // failed to bind, or ctx ended
		return err
	case <-ctx.Done():
		return <-served
	case <-service.Done():
	}
	stopServer()
	if err := <-served; err != nil {
		return err
	}
	if err := system.MarkOnboardingPending(ctx, db); err != nil {
		logger.Warn("recording the first-run checklist", "err", err)
	}
	logger.Info("setup complete; starting crowsnest")
	return nil
}

// setupAnnouncer prints the setup link: once per listener when it is bound, and again for every
// listener when the token rotates. The token is printed nowhere else.
type setupAnnouncer struct {
	logger *slog.Logger
	banner bool                     // also print a plain block on stderr (a terminal)
	onURL  func(link, token string) // tests read the link here
	// open, when set, opens a URL in the browser (serve's --open); handoffs mints the one-time
	// id it is given instead of the token. opened is set once a browser was asked.
	open     func(url string)
	handoffs *opener.Handoffs
	opened   bool
	mu       sync.Mutex
	value    string
	bases    []string
}

// target is where a redeemed handoff sends the browser: the setup page with the current token
// (it may have rotated since the id was minted), relative so a port-rewriting tunnel keeps working.
func (a *setupAnnouncer) target() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return "/setup#token=" + a.value
}

func (a *setupAnnouncer) token(token string, rotated bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.value = token
	if rotated {
		a.logger.Warn("too many wrong setup tokens: the token was replaced; the old link no longer works")
	}
	for _, base := range a.bases {
		a.print(base)
	}
}

func (a *setupAnnouncer) url(listenURL string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	base := browsableBase(listenURL)
	if slices.Contains(a.bases, base) {
		return
	}
	a.bases = append(a.bases, base)
	a.print(base)
	if a.open != nil && !a.opened && isLoopbackBase(base) {
		a.opened = true
		a.open(base + "/setup/open/" + a.handoffs.New())
	}
}

// isLoopbackBase reports whether a browsable base URL is on a loopback host.
func isLoopbackBase(base string) bool {
	parsed, err := url.Parse(base)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.IsLoopback()
}

// print logs one link. Called with mu held.
func (a *setupAnnouncer) print(base string) {
	link := base + "/setup#token=" + a.value
	a.logger.Warn("SETUP REQUIRED: open this link to choose the username and password (one-time token, new at every start)", "url", link)
	if a.banner {
		_, _ = fmt.Fprintf(os.Stderr, "\n  crowsnest is not set up yet. Open this link to choose a username and password:\n\n    %s\n\n", link)
	}
	if a.onURL != nil {
		a.onURL(link, a.value)
	}
}

// browsableBase turns a listen URL into one a browser can open: an unspecified address
// (0.0.0.0, ::) becomes localhost.
func browsableBase(listenURL string) string {
	parsed, err := url.Parse(listenURL)
	if err != nil {
		return listenURL
	}
	switch parsed.Hostname() {
	case "0.0.0.0", "::":
		parsed.Host = "localhost:" + parsed.Port()
	}
	return parsed.Scheme + "://" + parsed.Host
}
