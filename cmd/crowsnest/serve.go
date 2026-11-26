// `crowsnest serve`: load and check the config, open the database, connect to (or start) the herdr
// session, keep the fleet cache running, and run the HTTP server until SIGINT or SIGTERM. With
// --demo the herdr session is the in-memory fake with its scripted demo fleet. The orchestrator
// and the conversation tailer start here too (orchestrator.go), and so do the questions inbox and
// the agent socket (questions.go), and talk mode when an ElevenLabs key is set (voice.go).

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/httpd"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/term"
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
	restored, err := restoreHandedOverSecrets()
	if err != nil {
		return err
	}
	// The re-executed process (restored) never re-executes again. A failed re-exec is not
	// fatal: the secrets stay in this process's initial environment, as they did before the
	// re-exec existed, and children still never inherit them (UnsetSecretEnv).
	if !restored {
		if err := reexecWithoutSecrets(); err != nil {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "crowsnest: could not drop secrets from its own environment:", err)
		}
	}
	cfg, err := config.LoadForServe(cmd.Flags(), config.Options{})
	config.UnsetSecretEnv() // loaded or refused, nothing after this needs them (R1 H1)
	if err != nil {
		return err
	}
	if !cfg.WebDev && !web.HasApp() {
		return errors.New("this binary was built without the web app; build it with `mise run build`")
	}
	cfg, cleanupDemo, err := demoState(cfg)
	if err != nil {
		return err
	}
	defer cleanupDemo()
	logger := newLogger()
	slog.SetDefault(logger)
	logger.Info("starting crowsnest", "name", cfg.Name, "version", version, "config", cfg)
	for _, warning := range cfg.Warnings {
		logger.Warn(warning)
	}
	if err := privateDir(cfg.StateDir); err != nil {
		return err
	}
	if cfg.NoAuth {
		logger.Warn(noAuthWarning(cfg))
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(ctx, cfg, logger)
}

// noAuthWarning is the startup warning under --no-auth. On a tailnet bind it says who "anyone
// who can reach this port" is: every tailnet peer the ACL lets through, including nodes shared
// into the tailnet by other people (R1 I7).
func noAuthWarning(cfg config.Config) string {
	warning := "AUTHENTICATION IS OFF (--no-auth): anyone who can reach this port has a shell as you"
	for _, listener := range cfg.Listeners {
		if config.IsTailnet(listener.Addr.Addr().Unmap()) {
			return warning + ". On the tailnet that is every peer your Tailscale ACL lets reach this port, including nodes other people have shared into your tailnet"
		}
	}
	return warning
}

// serve wires the services together and blocks until ctx ends, or until a self-update from the
// browser has installed a new binary and asks for a restart.
func serve(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	ctx, restart := context.WithCancel(ctx)
	defer restart()
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
	client, herdrVersion, err := connectHerdr(ctx, cfg, logger)
	if err != nil {
		return err
	}
	fleetCache := fleet.New(fleet.Options{Client: client, Hub: events, Session: cfg.Session, Logger: logger})
	go fleetCache.Run(ctx)
	talk, err := startVoice(cfg, db, events, logger)
	if err != nil {
		return err
	}
	identity := agentid.New(db, nil)
	startTokenSweeper(ctx, client, identity, logger)
	agentSocket := agentSocketPath(cfg)
	journalLog := journal.New(db, events, logger, nil)
	refs := &jobRefs{}
	jobService := newJobService(cfg, client, db, events, agentSocket, identity, refs, journalLog, logger)
	pushService, pusher, err := startPush(ctx, cfg, db, events, logger)
	if err != nil {
		return err
	}
	questionService, stopQuestions, err := startQuestions(ctx, cfg, client, db, identity, events, jobService, speakerOf(talk), pusher, agentSocket,
		standingContext(cfg, refs, refs.questions.Load, jobService), logger)
	refs.questions.Store(questionService)
	defer stopQuestions() // deferred after db.Close, so it runs first
	if err != nil {
		return err
	}
	orchestrator, conversations := startOrchestrator(ctx, cfg, client, db, identity, events, agentSocket, talk, journalLog, logger)
	refs.orchestrator, refs.conversations = orchestrator, conversations
	startJobs(ctx, jobService, client, logger)
	var pokerRef atomic.Pointer[orch.Poker]
	scheduleService := startSchedule(ctx, cfg, db, events, journalLog, jobService, client, &pokerRef, logger)
	tender := newTender(cfg, client, db, journalLog, orchestrator, conversations, logger)
	poker := startPoker(ctx, cfg, db, events, pokeServices{
		orchestrator: orchestrator, jobs: jobService, questions: questionService, talk: talk, journal: journalLog, alert: refs.notify,
		tasks: scheduleTasks{service: scheduleService}, tender: tender,
	}, client, logger)
	pokerRef.Store(poker)

	deps := httpd.Deps{
		Config: cfg, Auth: authService, Hub: events, Logger: logger, Version: version,
		Herdr: client, Fleet: fleetCache, HerdrVersion: herdrVersion,
		Orchestrator: orchestrator, Conversations: conversations,
		Terminals: terminals(cfg), Questions: questionService, Jobs: jobService, Voice: talk,
		Journal: journalLog, Poker: poker, Push: pushService, Schedule: scheduleService, Tender: tender,
	}
	if !cfg.WebDev {
		deps.Web = web.Dist()
	}
	// Assigned only when non-nil: a nil *selfUpdater in the interface would not compare nil.
	if updater := newSelfUpdater(ctx, cfg.Demo, logger, restart); updater != nil {
		deps.Updater = updater
	}
	server, err := httpd.New(deps)
	if err != nil {
		return err
	}
	return server.Run(ctx)
}

// connectHerdr returns the session's client and the herdr version. --demo gets the fake with the
// demo fleet, animated until ctx ends; otherwise the named session is connected to, and its server
// started (detached, never stopped by crowsnest) if nothing answers.
func connectHerdr(ctx context.Context, cfg config.Config, logger *slog.Logger) (herdr.Client, string, error) {
	if cfg.Demo {
		demo := fake.NewDemo()
		if err := demo.EnableTranscripts(claudeConfigDir(cfg), 0); err != nil {
			return nil, "", fmt.Errorf("writing the demo transcripts: %w", err)
		}
		go demo.Run(ctx, fake.DefaultDemoStep)
		logger.Info("demo mode: using the in-memory herdr with a scripted fleet", "herdr_version", fake.Version)
		return demo, fake.Version, nil
	}
	session, err := herdr.EnsureSession(ctx, herdr.SessionOptions{
		Name: cfg.Session, AllowDefault: cfg.AllowDefaultSession, LogDir: cfg.StateDir, Logger: logger,
	})
	if err != nil {
		return nil, "", fmt.Errorf("connecting to herdr session %q: %w", cfg.Session, err)
	}
	logger.Info("connected to herdr", "session", session.Name, "socket", session.SocketPath,
		"herdr_version", session.Pong.Version, "protocol", session.Pong.Protocol, "started", session.Started)
	if session.Pong.Protocol != herdr.ProtocolVersion {
		logger.Warn("herdr speaks a different protocol than crowsnest was built for; some features may misbehave",
			"herdr_protocol", session.Pong.Protocol, "expected", herdr.ProtocolVersion)
	}
	return session.Client, session.Pong.Version, nil
}

// terminals picks the bridges behind the terminal routes. --demo gets the built-in toy: the fake
// herdr has no terminals, and a demo instance must never hand out a real shell.
func terminals(cfg config.Config) term.Launcher {
	if cfg.Demo {
		return term.DemoLauncher{}
	}
	return term.HerdrLauncher{Session: cfg.Session}
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
