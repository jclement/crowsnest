// Wiring for internal/system in serve: the doctor as the running server sees it, settings
// changed in the app (validated by reloading the config with the process's own flags, applied
// live where a service can take it), the orchestrator's start gate, the setup shell, and the
// friendly start-up lines.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/spf13/pflag"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/doctor"
	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/sysstat"
	"github.com/jclement/crowsnest/internal/system"
)

// envDemoDoctor picks the --demo scripted machine's state (doctor.DemoStates).
const envDemoDoctor = "CROWSNEST_DEMO_DOCTOR"

// setupWorkspace is the herdr workspace label of the checklist's shell.
const setupWorkspace = "setup"

// liveServices are the services a settings change is applied to, set as they are built.
type liveServices struct {
	poker  atomic.Pointer[orch.Poker]
	tender atomic.Pointer[orch.Tender]
	jobs   atomic.Pointer[jobs.Service]
	system atomic.Pointer[system.Service]
}

// apply pushes the settings that apply live into the services.
func (l *liveServices) apply(_ config.Config, next config.Config) {
	if poker := l.poker.Load(); poker != nil {
		poker.SetInterval(next.PokeInterval)
	}
	if tender := l.tender.Load(); tender != nil {
		tender.SetThresholds(next.CompactAt, next.FreshAt)
	}
	l.applyJobDefaults(context.Background(), next)
}

// applyJobDefaults sets the jobs' default engine (auto resolved against what is installed) and
// model.
func (l *liveServices) applyJobDefaults(ctx context.Context, cfg config.Config) {
	service, sys := l.jobs.Load(), l.system.Load()
	if service == nil || sys == nil {
		return
	}
	engine := sys.Selection(ctx).Jobs
	service.SetDefaults(engine, cfg.JobModel)
}

// reloader returns the function that loads the config again with saved settings: the
// process's own flags and environment, as at start.
func reloader(flags *pflag.FlagSet) func(map[string]string) (config.Config, error) {
	if flags == nil {
		return nil
	}
	return func(stored map[string]string) (config.Config, error) {
		return config.Load(flags, config.Options{Stored: stored})
	}
}

// newSystem builds the system service. In --demo the doctor looks at a scripted machine.
func newSystem(cfg config.Config, db *store.Store, events *hub.Hub, journalLog *journal.Journal, client herdr.Client, socket string, hooks serveHooks, live *liveServices, logger *slog.Logger) *system.Service {
	lazy, _ := client.(*herdr.Lazy)
	env := doctor.Env{}
	if cfg.Demo {
		state := os.Getenv(envDemoDoctor)
		if !doctor.ValidDemoState(state) {
			state = doctor.DemoAllGreen
		}
		env = doctor.DemoEnv(state)
	}
	service := system.New(system.Options{
		Config: cfg, Version: version, KV: db, Journal: journalLog, Hub: events, Logs: hooks.logs, Herdr: lazy,
		AgentSocket: socket, Reload: hooks.reload, Apply: live.apply, Env: env, Logger: logger,
	})
	live.system.Store(service)
	return service
}

// herdrVersionNow reports the connected herdr's version for /auth/me.
func herdrVersionNow(client herdr.Client) func() string {
	lazy, ok := client.(*herdr.Lazy)
	if !ok {
		return nil
	}
	return func() string {
		if session := lazy.Session(); session != nil {
			return session.Pong.Version
		}
		return ""
	}
}

// openShell returns the checklist's shell opener: a workspace "setup" in the orchestrator's
// folder (so signing in to Claude there also answers its folder-trust question), with the fix
// command typed into its shell.
func openShell(cfg config.Config, client herdr.Client) func(ctx context.Context, command string) (string, error) {
	return func(ctx context.Context, command string) (string, error) {
		if cfg.Demo {
			return "", errors.New("the demo has no real shell")
		}
		if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
			return "", fmt.Errorf("creating %s: %w", cfg.Dir, err)
		}
		created, err := client.WorkspaceCreate(ctx, herdr.WorkspaceCreateParams{Label: setupWorkspace, Cwd: cfg.Dir})
		if err != nil {
			return "", err
		}
		paneID := created.RootPane.ID
		if command == "" {
			return paneID, nil
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if info, err := client.PaneProcessInfo(ctx, paneID); err == nil && len(info.ForegroundProcesses) > 0 {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		time.Sleep(500 * time.Millisecond) // the shell prints its prompt after it shows up
		return paneID, client.PaneRun(ctx, paneID, command)
	}
}

// logReady prints the few friendly lines after the server is up: the URL and the doctor's
// one-line summary, with the hint to run `crowsnest doctor`. The doctor runs in the background
// so it never delays serving.
func logReady(ctx context.Context, service *system.Service, urls []string, logger *slog.Logger) {
	go func() {
		report := service.Report(ctx, true)
		if ctx.Err() != nil {
			return
		}
		logger.Info("doctor: "+report.Summary, "verdict", report.Verdict, "more", "crowsnest doctor")
		if stderrIsTerminal() {
			marker := "✓ ready"
			if report.Verdict == doctor.StatusFail {
				marker = "! not ready yet: open the app for the checklist, or run `crowsnest doctor`"
			}
			fmt.Fprintf(os.Stderr, "\n  crowsnest is running: %s\n  %s\n  %s   (details: crowsnest doctor)\n\n", strings.Join(urls, "  "), marker, report.Summary)
		}
	}()
}

// startStats starts the machine-stats sampler: every two seconds while a browser has the event
// stream open, the host's (or the container's cgroup's) CPU, memory, load and data-volume disk,
// with the number of working agents from the fleet. --demo samples the real machine too: the
// numbers are harmless and make the strip look alive.
func startStats(ctx context.Context, cfg config.Config, events *hub.Hub, fleetCache *fleet.Cache, active func() bool, logger *slog.Logger) *sysstat.Sampler {
	sampler := sysstat.New(sysstat.Options{
		Source: &sysstat.HostSource{DataDir: cfg.DataDir}, Hub: events, Active: active, Logger: logger,
		Working: func() int {
			view, err := fleetCache.Fleet(ctx)
			if err != nil {
				return -1
			}
			working := 0
			for _, agent := range view.Agents {
				if agent.Status == herdr.StatusWorking {
					working++
				}
			}
			return working
		},
	})
	go sampler.Run(ctx)
	return sampler
}
