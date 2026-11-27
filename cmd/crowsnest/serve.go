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
	"github.com/jclement/crowsnest/internal/machines"
	"github.com/jclement/crowsnest/internal/tsauth"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/jclement/crowsnest/internal/actions"
	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/doctor"
	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/httpd"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/instructions"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/sysstat"
	"github.com/jclement/crowsnest/internal/system"
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
	cfg, err := loadConfig(cmd.Context(), cmd.Flags())
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
	home, _ := os.UserHomeDir()
	logs := system.NewLogRing(home)
	logger := slog.New(logs.Handler(newLogger().Handler()))
	slog.SetDefault(logger)
	logger.Info("starting crowsnest", "name", cfg.Name, "version", version, "config", cfg)
	for _, warning := range cfg.Warnings {
		logger.Warn(warning)
	}
	if cfg.TailnetName != "" {
		logger.Info("Tailscale name allowed as a host, so `tailscale serve` reaches this crowsnest", "name", cfg.TailnetName, "turn_off", "--no-tailscale-detect")
	}
	if err := privateDir(cfg.StateDir); err != nil {
		return err
	}
	if cfg.NoAuth {
		logger.Warn(noAuthWarning(cfg))
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	hooks := defaultServeHooks()
	hooks.logs, hooks.reload, hooks.open = logs, reloader(cmd.Flags()), browserOpener(cfg, logger)
	return serve(ctx, cfg, logger, hooks)
}

// serveHooks are the seams serve's tests use: how herdr is reached (a test passes a fake and
// checks nothing reaches it before setup), and where the setup link goes besides the log.
type serveHooks struct {
	connect  func(ctx context.Context, cfg config.Config, logger *slog.Logger) (herdr.Client, string, error)
	setupURL func(link, token string)
	// logs keeps recent warnings for the System page (nil in tests); reload re-reads the config
	// with saved settings (nil: settings cannot be changed in the app).
	logs   *system.LogRing
	reload func(stored map[string]string) (config.Config, error)
	// open opens a URL in the browser (--open); nil never opens (tests).
	open func(url string)
	// doctorEnv, when set, is the machine the doctor and the start gate see (tests); nil: the
	// real one (or --demo's scripted one).
	doctorEnv *doctor.Env
}

func defaultServeHooks() serveHooks { return serveHooks{connect: connectHerdr} }

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
//
// With no login configured or stored it first runs the locked setup phase (setup.go) and only
// continues once an owner exists: herdr, the orchestrator and the agent socket never start
// before then.
func serve(ctx context.Context, cfg config.Config, logger *slog.Logger, hooks serveHooks) error {
	ctx, restart := context.WithCancel(ctx)
	defer restart()
	if err := prepareUpgrade(ctx, cfg, logger); err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg.DatabasePath())
	if err != nil {
		return fmt.Errorf("opening database %s: %w", cfg.DatabasePath(), err)
	}
	defer func() { _ = db.Close() }()
	recordVersion(ctx, db, logger)

	mode, err := decideLogin(ctx, cfg, db)
	if err != nil {
		return err
	}
	logLoginMode(logger, cfg, mode)
	if mode == loginSetup {
		if err := runSetupPhase(ctx, cfg, db, logger, hooks); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
	}
	cfg = storedName(ctx, cfg, db)
	// The port is bound now, before the slow part of the start, and held from here on: a port in
	// use is reported at once, and nothing can take it while the services are wired (the test
	// that reserved a port and bound it seconds later lost it about one run in five to the
	// parallel suite's own connections).
	// They answer at once from here on: 503 with Retry-After until the server is ready, so a
	// browser reconnecting across a restart is told to retry rather than left waiting on a
	// connection nobody answers.
	early, err := httpd.ServeEarly(ctx, cfg, logger)
	if err != nil {
		return err
	}
	handedOver := false
	defer func() {
		if !handedOver {
			early.Close()
		}
	}()

	authService, err := auth.New(ctx, auth.Options{Username: cfg.Username, Password: cfg.Password, NoAuth: cfg.NoAuth}, db)
	if err != nil {
		return err
	}
	go cleanSessions(ctx, authService, logger)

	events := hub.New()
	defer events.Close()
	client, herdrVersion, err := hooks.connect(ctx, cfg, logger)
	if err != nil {
		return err
	}
	fleetCache := fleet.New(fleet.Options{Client: client, Hub: events, Session: cfg.Session, Logger: logger})
	go fleetCache.Run(ctx)
	talk, err := startVoice(cfg, db, events, logger)
	if err != nil {
		return err
	}
	live := &liveServices{}
	live.config.set(cfg)
	journalLog := journal.New(db, events, logger, nil)
	sys := newSystem(cfg, db, events, journalLog, client, agentSocketPath(cfg), hooks, live, logger)
	identity := agentid.New(db, nil)
	startTokenSweeper(ctx, client, identity, logger)
	agentSocket := agentSocketPath(cfg)
	ctl := newControls(cfg, db, events, journalLog, logger)
	ctl.run(ctx, cfg.Demo)
	live.ctl.Store(ctl)
	refs := &jobRefs{ctl: ctl, guard: guardRules(cfg, logger), projects: newProjectWiring(cfg, db, events), instructions: instructions.New(instructions.Options{Store: db, RolesDir: rolesDir(cfg), Hub: events, Logger: logger})}
	approvalLinks := &approvalWiring{journal: journalLog, projects: refs.projects.service}
	approvalService, err := newApprovals(cfg, db, events, refs.guard, approvalLinks, logger)
	if err != nil {
		return err
	}
	refs.approvals = approvalService
	files, err := newAttachments(cfg, db, events, refs, logger)
	if err != nil {
		return err
	}
	var jobServiceRef atomic.Pointer[jobs.Service]
	// Spend is metered only here, in the normal phase: setup mode (above) returns before any of
	// this, so no transcript is read until an owner exists.
	meter := newSpendMeter(cfg, db, events, refs, jobServiceRef.Load, logger)
	jobService := newJobService(cfg, client, db, events, agentSocket, identity, refs, journalLog, files, meter, logger)
	jobServiceRef.Store(jobService)
	live.jobs.Store(jobService)
	refs.projects.jobs.Store(jobService)
	go live.applyJobDefaults(ctx, cfg)
	approvalLinks.jobs.Store(jobService)
	files.jobs = jobService
	go files.run(ctx, events, refs.questions.Load)
	pushService, pusher, err := startPush(ctx, cfg, db, events, ctl, pushDescriber(jobService, refs.projects), logger)
	if err != nil {
		return err
	}
	approvalLinks.pusher.Store(pusher)
	runApprovals(ctx, approvalService, fleetCache, logger)
	digests := &digestProxy{}
	questionService, stopQuestions, err := startQuestions(ctx, cfg, client, db, identity, events, jobService, speakerOf(talk), pusher, agentSocket,
		standingContext(cfg, refs, refs.questions.Load, jobService), files, approvalService, refs.instructions, refs.projects.subjectOf, projectReader{w: refs.projects, jobs: jobService}, sys, ctl, digests, logger)
	refs.questions.Store(questionService)
	defer stopQuestions() // deferred after db.Close, so it runs first
	if err != nil {
		return err
	}
	if cfg.Demo {
		seedDemoLayers(ctx, refs.instructions, logger) // before any demo agent starts with them
	}
	orchestrator, conversations := startOrchestrator(ctx, cfg, client, db, identity, events, agentSocket, talk, journalLog, refs.guard, refs.instructions,
		orchGate{gate: sys.Gate, launch: sys.LaunchSettings(orchestratorRemoteControl)}, ctl, logger)
	sys.SetOrchestrator(orchestrator)
	greet := greeter(ctx, client, orchestrator, logger)
	live.greeter.Store(&greet)
	refs.orchestrator, refs.conversations = orchestrator, conversations
	refs.instructions.SetStandingStale(func(subject string) bool {
		if subject == orch.Name {
			return orchestrator.Status().InstructionsStale
		}
		if leads := refs.projects.leads.Load(); leads != nil {
			for _, lead := range leads.Running() {
				if projects.Subject(lead.Project.Slug) == subject {
					return lead.Agent.Status().InstructionsStale
				}
			}
		}
		return false
	})
	var projectOf func(string) (int64, string)
	if cfg.Demo {
		projectOf = seedDemoProjects(ctx, cfg, refs.projects, logger)
	}
	// After a restore, take stock of the agents herdr really has (DESIGN.md 4.17). The restore
	// left the pause switch on, so nothing is dispatched meanwhile; the stock-taking can wait for
	// herdr, so it runs in the background and the app comes up.
	go reconcileRestore(ctx, db, fleetCache, journalLog, logger)
	startJobs(ctx, jobService, client, projectOf, cfg.StateDir, logger)
	if demo, ok := client.(*fake.Demo); ok {
		// After the jobs: each demo approval and question names the job whose pane raised it.
		inbox := demoInbox{demo: demo, jobs: jobService, approvals: approvalService, questions: questionService, files: files, stateDir: cfg.StateDir}
		if err := startDemoInbox(ctx, inbox, logger); err != nil {
			return err
		}
	}
	startSpend(ctx, cfg, meter, jobService, logger)
	var pokerRef atomic.Pointer[orch.Poker]
	scheduleService := startSchedule(ctx, cfg, db, events, journalLog, jobService, client, &pokerRef, ctl, logger)
	tender := newTender(cfg, client, db, journalLog, orchestrator, conversations, logger)
	live.tender.Store(tender)
	poker := startPoker(ctx, cfg, db, events, pokeServices{
		orchestrator: orchestrator, jobs: jobService, questions: questionService, talk: talk, journal: journalLog, alert: refs.notify,
		tasks: scheduleTasks{service: scheduleService}, tender: tender, leads: refs.projects.leads.Load, ctl: ctl,
	}, client, logger)
	pokerRef.Store(poker)
	live.poker.Store(poker)
	tasksFor := func(projectID int64) orch.TaskSource {
		return scheduleTasks{service: scheduleService, projectID: projectID}
	}
	refs.projects.taskSource.Store(&tasksFor)
	refs.projects.useInstructions(refs.instructions, logger)
	refs.projects.useUpkeep(leadUpkeep{jobs: jobService, questions: questionService, conversations: conversations, poker: poker, tender: tender})
	leads := refs.projects.startLeads(ctx, leadDeps{
		cfg: cfg, client: client, db: db, events: events, journal: journalLog, identity: identity, agentSocket: agentSocket, guard: refs.guard, logger: logger, ctl: ctl,
		onStatus: func(project projects.Project, status orch.Status) {
			if status.PaneID != "" {
				go func() { _ = conversations.Pin(ctx, status.Name) }()
			}
			standingReplies.status(ctx, status.Name, status.State == orch.StateIdle, status.PaneID)
			if status.State == orch.StateIdle || status.State == orch.StateWorking {
				// A lead that just became ready takes the job waiting for it now, not at the
				// supervisor's next tick (which --demo does not run at all).
				go jobService.DispatchStanding(context.WithoutCancel(ctx))
			}
			events.Publish(hub.Event{Type: "lead", Data: projects.LeadStatus{Status: status, ProjectID: project.ID, Slug: project.Slug, Running: true}})
		},
	})
	approvalLinks.leads.Store(leads)
	if cfg.Demo {
		// Before the server answers (it serves "starting" until Run): a page opened at once finds
		// the demo's leads already launched, rather than racing a goroutine that starts them.
		seedDemoExtras(ctx, refs.projects, leads, scheduleService, refs.instructions, logger)
	}

	attentionService, nowSources := startAttention(ctx, attentionDeps{
		db: db, events: events, fleet: fleetCache, approvals: approvalService, questions: questionService, jobs: jobService,
		projects: refs.projects.service, orchestrator: orchestrator, leads: leads, meter: meter, logger: logger,
	})

	digestService := startDigest(ctx, digestDeps{cfg: cfg, db: db, journal: journalLog, client: client, orchestrator: orchestrator,
		questions: questionService, approvals: approvalService, meter: meter, projects: refs.projects.service, ctl: ctl, sys: sys, logger: logger})
	digests.service.Store(digestService)
	ctl.presence.SetOnDNDEnd(dndSummary(refs.notify))
	deps := httpd.Deps{
		Pause: ctl.pause, Throttle: ctl.throttle, Presence: ctl.presence, Digest: digestService,
		HeavyDir: ctl.heavyDir, HeavyDeferred: ctl.heavyDeferred,
		Config: cfg, Auth: authService, Hub: events, Logger: logger, Version: version,
		Herdr: client, Fleet: fleetCache, HerdrVersion: herdrVersion,
		Orchestrator: orchestrator, Conversations: conversations,
		Terminals: terminals(cfg), Questions: questionService, Jobs: jobService, Voice: talk,
		Journal: journalLog, Poker: poker, Push: pushService, Schedule: scheduleService, Tender: tender,
		Attachments: files.service, AttachLayout: files.layout, Spend: meter,
		Names: db, Projects: refs.projects.service, Leads: leads, Instructions: refs.instructions, Approvals: approvalService, Actions: &actions.Tokens{Store: db},
		System: sys, OpenShell: openShell(cfg, client), HerdrVersionNow: herdrVersionNow(client),
		Processes: sysstat.NewProcesses(), Attention: attentionService, Now: nowSources,
	}
	// The nightly clean-up and the automatic backup are automation: the pause switch holds both
	// (DESIGN.md 6.13). Clean up now and Download backup are the human's and are not held.
	cleanup := newRetention(live.config.get, db, journalLog, ctl.paused, logger)
	if !cfg.Demo {
		go cleanup.RunNightly(ctx)
		go backupScheduler{cfg: live.config.get, db: db, journal: journalLog, paused: ctl.paused, logger: logger}.run(ctx)
	}
	deps.Machines = &machines.Service{Store: db}
	deps.Tailscale = tsauth.New(tsauth.Options{Enabled: cfg.TailscaleAuth, KV: db, Port: cfg.Port})
	var serverRef atomic.Pointer[httpd.Server]
	deps.Stats = startStats(ctx, cfg, events, fleetCache, func() bool {
		server := serverRef.Load()
		return server != nil && server.OpenStreams() > 0
	}, logger)
	deps.Ops = &opsWiring{cfg: live.config.get, db: db, retention: cleanup, jobs: jobService, questions: refs.questions.Load,
		approvals: approvalService, orch: orchestrator, stats: deps.Stats}
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
	serverRef.Store(server)
	server.Early, handedOver = early, true
	var readyOnce sync.Once
	server.OnListening = func(url string) {
		readyOnce.Do(func() { logReady(ctx, sys, []string{browsableBase(url)}, logger) })
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
	if err := herdr.ValidateSessionName(cfg.Session, cfg.AllowDefaultSession); err != nil {
		return nil, "", err
	}
	// herdr may be missing or slow to start: serve comes up anyway and connects in the
	// background, so the app can explain and things start once herdr is there (DESIGN.md 3.4).
	lazy := herdr.NewLazy()
	go lazy.Connect(ctx, herdr.SessionOptions{
		Name: cfg.Session, AllowDefault: cfg.AllowDefaultSession, LogDir: cfg.StateDir, Logger: logger,
	}, func(session *herdr.Session) {
		logger.Info("connected to herdr", "session", session.Name, "socket", session.SocketPath,
			"herdr_version", session.Pong.Version, "protocol", session.Pong.Protocol, "started", session.Started)
		if session.Pong.Protocol != herdr.ProtocolVersion {
			logger.Warn("herdr speaks a different protocol than crowsnest was built for; some features may misbehave",
				"herdr_protocol", session.Pong.Protocol, "expected", herdr.ProtocolVersion)
		}
	})
	return lazy, "", nil
}

// terminals picks the bridges behind the terminal routes. --demo gets the built-in toy: the fake
// herdr has no terminals, and a demo instance must never hand out a real shell.
func terminals(cfg config.Config) term.Launcher {
	if cfg.Demo {
		return term.DemoLauncher{}
	}
	return term.HerdrLauncher{Session: cfg.Session, ClientStateDir: cfg.TerminalClientStateDir()}
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
