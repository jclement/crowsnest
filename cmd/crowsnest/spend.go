// Wiring for spend (DESIGN.md 7.11): the price table (embedded, or ~/.config/crowsnest/
// prices.json on top), the meter that reads every job's, the orchestrator's and each running
// project lead's transcripts into the store, and under --demo a seeded history instead (there
// are no real transcripts to read).

package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/demostory"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/spend"
	"github.com/jclement/crowsnest/internal/store"
)

// pricesPath is ~/.config/crowsnest/prices.json (next to the config file, like roles/).
func pricesPath(cfg config.Config) string {
	return filepath.Join(filepath.Dir(rolesDir(cfg)), "prices.json")
}

// newSpendMeter builds the meter. A prices file that does not parse is logged and the embedded
// table used, rather than refusing to start over a price.
func newSpendMeter(cfg config.Config, db *store.Store, events *hub.Hub, refs *jobRefs, jobService func() *jobs.Service, logger *slog.Logger) *spend.Meter {
	prices := spend.Embedded()
	if !cfg.Demo {
		loaded, err := spend.Load(pricesPath(cfg))
		if err != nil {
			logger.Warn("ignoring the prices file; using the built-in prices", "err", err)
		} else {
			prices = loaded
			if loaded.Override != "" {
				logger.Info("prices file applied", "path", loaded.Override, "checked", loaded.Checked())
			}
		}
	}
	return spend.NewMeter(spend.Options{
		Store: db, Prices: prices, ClaudeConfigDir: claudeConfigDir(cfg), Hub: events, Logger: logger,
		Sources: func(ctx context.Context) ([]spend.Source, error) {
			service := jobService()
			if service == nil {
				return nil, nil
			}
			sources, err := service.SpendSources(ctx)
			if err != nil {
				return nil, err
			}
			if refs.orchestrator != nil {
				if status := refs.orchestrator.Status(); status.SessionID != "" {
					sources = append(sources, spend.Source{Subject: spend.OrchestratorSubject,
						Sessions: []spend.Session{{ID: status.SessionID, Cwd: cfg.Dir}}})
				}
			}
			return append(sources, leadSpendSources(refs.projects)...), nil
		},
	})
}

// leadSpendSources are the running leads' conversations, one subject each ("lead:<slug>", in
// the project's directory). The meter keeps every session id it has seen per subject, so a
// lead's earlier conversations stay counted across new conversations and restarts.
func leadSpendSources(wiring *projectWiring) []spend.Source {
	if wiring == nil {
		return nil
	}
	leads := wiring.leads.Load()
	if leads == nil {
		return nil
	}
	var out []spend.Source
	for _, lead := range leads.Running() {
		if status := lead.Agent.Status(); status.SessionID != "" {
			out = append(out, spend.Source{Subject: projects.Subject(lead.Project.Slug),
				Sessions: []spend.Session{{ID: status.SessionID, Cwd: lead.Project.Dir}}})
		}
	}
	return out
}

// startSpend runs the meter, or under --demo seeds the demo's history once the jobs exist.
func startSpend(ctx context.Context, cfg config.Config, meter *spend.Meter, service *jobs.Service, logger *slog.Logger) {
	if cfg.Demo {
		if err := seedDemoSpend(ctx, meter, service); err != nil {
			logger.Warn("seeding demo spend", "err", err)
		}
		return
	}
	go meter.Run(ctx)
}

// demoModels gives the demo jobs a spread of models, so the by-model view has rows.
var demoModels = []string{"claude-opus-5-5", "claude-fable-5-2", "claude-opus-5-5", "claude-haiku-5-5", "claude-sonnet-5-5", "claude-opus-5-5", "claude-sonnet-5-5"}

func seedDemoSpend(ctx context.Context, meter *spend.Meter, service *jobs.Service) error {
	list, err := service.List(ctx, nil)
	if err != nil {
		return err
	}
	now := time.Now()
	var subjects []spend.DemoSubject
	for i, job := range list {
		if job.StartedAt == nil || jobs.IsStanding(job.AgentName) || job.Engine == jobs.EngineCodex {
			continue
		}
		end := now
		if job.SettledAt != nil {
			end = *job.SettledAt
		}
		calls := int(end.Sub(*job.StartedAt) / (90 * time.Second))
		subjects = append(subjects, spend.DemoSubject{
			Subject: job.ID, Model: demoModels[i%len(demoModels)], Start: *job.StartedAt, End: end,
			Calls: min(max(calls, 8), 60), Subagent: i%3 == 0,
		})
	}
	// The orchestrator: a little every day this past week, so the by-day view has a shape. Today's
	// stretch runs up to now, so some of it is today even just after midnight (the Chat menu's
	// "today" figure was "cost unknown" for a demo started before 03:00).
	for day := 6; day >= 0; day-- {
		start := now.AddDate(0, 0, -day).Add(-3 * time.Hour)
		end := start.Add(2 * time.Hour)
		if day == 0 {
			start, end = now.Add(-90*time.Minute), now
		}
		subjects = append(subjects, spend.DemoSubject{Subject: spend.OrchestratorSubject, Model: "claude-sonnet-5-5",
			Start: start, End: end, Calls: 6 + day})
	}
	// The three running leads (DESIGN.md 21.5): a morning's worth each, on their own lines.
	for i, slug := range []string{demostory.Crowsnest, demostory.Website, demostory.Mobile} {
		start := now.Add(-time.Duration(3+i) * time.Hour)
		subjects = append(subjects, spend.DemoSubject{Subject: spend.LeadSubjectPrefix + slug, Model: "claude-sonnet-5-5",
			Start: start, End: now, Calls: 10 - 2*i})
	}
	return spend.SeedDemo(ctx, meter, subjects)
}
