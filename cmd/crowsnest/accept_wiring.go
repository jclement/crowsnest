// Wiring for acceptance, budgets, the fleet's usage limit and the wait invariant (DESIGN.md 6.15
// to 6.19): the check runs under `crowsnest heavy`, the budgets and ceilings come from Settings,
// today's spend from the meter, and an invariant violation is journalled.

package main

import (
	"context"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/spend"
	"github.com/jclement/crowsnest/internal/store"
)

// acceptOptions turns acceptance on: the check runs through the heavy gate.
func acceptOptions(cfg config.Config, executable string) jobs.AcceptOptions {
	return jobs.AcceptOptions{Enabled: true, Timeout: cfg.CheckTimeout, MaxRounds: cfg.CheckRounds, Heavy: []string{executable, "heavy", "--"}}
}

// budgetOptions are Settings' budgets and ceilings.
func budgetOptions(cfg config.Config) jobs.BudgetOptions {
	return jobs.BudgetOptions{JobUSD: cfg.JobBudgetUSD, JobTokens: cfg.JobBudgetTokens, JobMinutes: cfg.JobBudgetMinutes, JobTurns: cfg.JobBudgetTurns,
		DailyUSD: cfg.DailyCeilingUSD, OrchestratorUSD: cfg.OrchestratorCeilingUSD, LeadUSD: cfg.LeadCeilingUSD}
}

// spendDayLimits reads today's spend from the meter's reader, cached for 30 seconds: the supervisor
// asks every tick and the board on every read.
func spendDayLimits(reader jobs.SpendReader) func(ctx context.Context) (jobs.DaySpend, error) {
	meter, ok := reader.(*spend.Meter)
	if !ok || meter == nil {
		return nil
	}
	var mu sync.Mutex
	var cached jobs.DaySpend
	var at time.Time
	return func(ctx context.Context) (jobs.DaySpend, error) {
		mu.Lock()
		defer mu.Unlock()
		if !at.IsZero() && time.Since(at) < 30*time.Second {
			return cached, nil
		}
		today := time.Now().In(meter.Location()).Format(time.DateOnly)
		report, err := meter.Report(ctx, today, today, spend.GroupJob, nil)
		if err != nil {
			return jobs.DaySpend{}, err
		}
		day := jobs.DaySpend{Measured: report.Total.Measured, BySubject: map[string]float64{}}
		if report.Total.CostUSD != nil {
			day.USD = *report.Total.CostUSD
		}
		for _, row := range report.Rows {
			if row.CostUSD != nil {
				day.BySubject[row.Key] = *row.CostUSD
			}
		}
		cached, at = day, time.Now()
		return day, nil
	}
}

// fleetLimits keeps the fleet's usage limits in the database.
func fleetLimits(db *store.Store, events *hub.Hub) *jobs.FleetLimits {
	return jobs.NewFleetLimits(db, events, nil)
}

// violationJournal journals a wait-invariant violation (the System page lists them too).
func violationJournal(journalLog *journal.Journal) func(jobs.Violation) {
	return func(violation jobs.Violation) {
		journalLog.Add(context.Background(), journal.KindAlert, "Wait invariant: "+violation.JobID+" "+violation.Rule, map[string]any{"job": violation.JobID, "rule": violation.Rule})
	}
}

// applyAcceptConfig pushes Settings' budgets and check settings into the jobs service.
func applyAcceptConfig(cfg config.Config, service *jobs.Service) {
	if service == nil {
		return
	}
	service.SetBudgets(budgetOptions(cfg))
	service.SetAccept(cfg.CheckTimeout, cfg.CheckRounds)
}

// standingHold is a standing agent's poke hold: its daily ceiling, or a usage limit on Claude
// (the standing agents' engine; DESIGN.md 6.16, 6.17).
func standingHold(service *jobs.Service, subject string) func() (bool, string) {
	return func() (bool, string) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if reached, why := service.StandingCeiling(ctx, subject); reached {
			return true, why
		}
		return service.LimitHeld(ctx, jobs.EngineClaude)
	}
}
