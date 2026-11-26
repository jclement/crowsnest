// `crowsnest serve`: load and check the config, open the database, and run the HTTP server
// until SIGINT or SIGTERM. herdr, the orchestrator, questions and talk mode join here in later
// milestones; M1 serves auth, health, the event stream and the web app.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/httpd"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/web"
)

// sessionCleanupInterval is how often expired login sessions are deleted.
const sessionCleanupInterval = time.Hour

func newServeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Serve the web app (the default command)",
		Args:  cobra.NoArgs,
		RunE:  runServe,
	}
}

func runServe(cmd *cobra.Command, _ []string) error {
	cfg, err := config.LoadForServe(cmd.Flags(), config.Options{})
	if err != nil {
		return err
	}
	if !cfg.WebDev && !web.HasApp() {
		return errors.New("this binary was built without the web app; build it with `mise run build`")
	}
	logger := newLogger()
	slog.SetDefault(logger)
	logger.Info("starting crowsnest", "version", version, "config", cfg)
	if cfg.NoAuth {
		logger.Warn("AUTHENTICATION IS OFF (--no-auth): anyone who can reach this port has a shell as you")
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(ctx, cfg, logger)
}

// serve wires the services together and blocks until ctx ends.
func serve(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	db, err := store.Open(ctx, cfg.DatabasePath())
	if err != nil {
		return fmt.Errorf("opening database %s: %w", cfg.DatabasePath(), err)
	}
	defer func() { _ = db.Close() }()

	authService, err := auth.New(ctx, auth.Options{Username: cfg.Username, Password: cfg.Password, NoAuth: cfg.NoAuth}, db)
	if err != nil {
		return err
	}
	go cleanSessions(ctx, authService, logger)

	events := hub.New()
	defer events.Close()
	deps := httpd.Deps{Config: cfg, Auth: authService, Hub: events, Logger: logger, Version: version}
	if !cfg.WebDev {
		deps.Web = web.Dist()
	}
	server, err := httpd.New(deps)
	if err != nil {
		return err
	}
	return server.Run(ctx)
}

// cleanSessions deletes expired sessions every hour until ctx ends.
func cleanSessions(ctx context.Context, service *auth.Service, logger *slog.Logger) {
	ticker := time.NewTicker(sessionCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if removed, err := service.Cleanup(ctx); err != nil {
				logger.Warn("cleaning up sessions", "err", err)
			} else if removed > 0 {
				logger.Info("removed expired sessions", "count", removed)
			}
		}
	}
}
