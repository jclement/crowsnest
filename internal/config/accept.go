// Acceptance and budgets (DESIGN.md 6.15, 6.16): how long crowsnest's check of a finished job
// may run and how many failed checks go back to the agent, the default budgets of a job, and the
// daily ceilings for the fleet and the standing agents. Kept in their own file and loader step,
// like landing.go.

package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Option names for this file's settings.
const (
	optCheckTimeout      = "check-timeout"
	optCheckRounds       = "check-rounds"
	optJobBudgetUSD      = "job-budget-usd"
	optJobBudgetMinutes  = "job-budget-minutes"
	optJobBudgetTurns    = "job-budget-turns"
	optJobBudgetTokens   = "job-budget-tokens"
	optDailyCeilingUSD   = "daily-ceiling-usd"
	optOrchCeilingUSD    = "orchestrator-ceiling-usd"
	optLeadCeilingUSD    = "lead-ceiling-usd"
	defaultCheckTimeout  = 30 * time.Minute
	defaultCheckRounds   = 3
	maxBudgetUSD         = 100000.0
	maxBudgetMinutesTurn = 1000000
)

func init() {
	options = append(options,
		option{name: optCheckTimeout, env: []string{"CROWSNEST_CHECK_TIMEOUT"}, def: "30m", usage: "longest crowsnest's check of a finished job may run before it counts as failed"},
		option{name: optCheckRounds, env: []string{"CROWSNEST_CHECK_ROUNDS"}, kind: kindInt, def: "3", usage: "failed checks sent back to the agent before the job needs you"},
		option{name: optJobBudgetUSD, env: []string{"CROWSNEST_JOB_BUDGET_USD"}, def: "0", usage: "default budget per job in estimated dollars, its delegated jobs included (0: none)"},
		option{name: optJobBudgetMinutes, env: []string{"CROWSNEST_JOB_BUDGET_MINUTES"}, kind: kindInt, def: "0", usage: "default budget per job in minutes since its agent started (0: none)"},
		option{name: optJobBudgetTurns, env: []string{"CROWSNEST_JOB_BUDGET_TURNS"}, kind: kindInt, def: "0", usage: "default budget per job in ended turns, its delegated jobs included (0: none)"},
		option{name: optJobBudgetTokens, env: []string{"CROWSNEST_JOB_BUDGET_TOKENS"}, kind: kindInt, def: "0", usage: "default budget per job in tokens, its delegated jobs included (0: none)"},
		option{name: optDailyCeilingUSD, env: []string{"CROWSNEST_DAILY_CEILING_USD"}, def: "0", usage: "the fleet's spend ceiling per day in estimated dollars: bounds jobs with no budget of their own and holds new starts once reached (0: none)"},
		option{name: optOrchCeilingUSD, env: []string{"CROWSNEST_ORCHESTRATOR_CEILING_USD"}, def: "0", usage: "the orchestrator's spend ceiling per day; past it automatic pokes wait (0: none)"},
		option{name: optLeadCeilingUSD, env: []string{"CROWSNEST_LEAD_CEILING_USD"}, def: "0", usage: "each project lead's spend ceiling per day where its spend is measured (0: none)"},
	)
}

func (l *loader) loadAccept(cfg *Config) error {
	timeout, err := l.duration(optCheckTimeout)
	if err != nil {
		return err
	}
	if timeout < time.Minute || timeout > 24*time.Hour {
		return fmt.Errorf("--check-timeout %s: give 1m to 24h", timeout)
	}
	cfg.CheckTimeout = timeout
	if cfg.CheckRounds, err = l.integer(optCheckRounds); err != nil {
		return err
	}
	if cfg.CheckRounds < 1 || cfg.CheckRounds > 20 {
		return fmt.Errorf("--check-rounds %d: give 1 to 20", cfg.CheckRounds)
	}
	for _, money := range []struct {
		name string
		into *float64
	}{{optJobBudgetUSD, &cfg.JobBudgetUSD}, {optDailyCeilingUSD, &cfg.DailyCeilingUSD}, {optOrchCeilingUSD, &cfg.OrchestratorCeilingUSD}, {optLeadCeilingUSD, &cfg.LeadCeilingUSD}} {
		raw := strings.TrimPrefix(strings.TrimSpace(l.str(money.name)), "$")
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || value < 0 || value > maxBudgetUSD {
			return fmt.Errorf("--%s %q: give dollars such as 5 or 2.50, or 0 for none", money.name, raw)
		}
		*money.into = value
	}
	for _, count := range []struct {
		name string
		into *int
	}{{optJobBudgetMinutes, &cfg.JobBudgetMinutes}, {optJobBudgetTurns, &cfg.JobBudgetTurns}} {
		if *count.into, err = l.integer(count.name); err != nil {
			return err
		}
		if *count.into < 0 || *count.into > maxBudgetMinutesTurn {
			return fmt.Errorf("--%s %d: give 0 (none) or a positive number", count.name, *count.into)
		}
	}
	tokens, err := l.integer(optJobBudgetTokens)
	if err != nil {
		return err
	}
	if tokens < 0 {
		return fmt.Errorf("--%s %d: give 0 (none) or a positive number", optJobBudgetTokens, tokens)
	}
	cfg.JobBudgetTokens = int64(tokens)
	return nil
}

// acceptSettings are this file's rows in the app's Settings.
var acceptSettings = []Setting{
	{Name: optJobBudgetUSD, Label: "Budget per job ($)", Section: "Budgets and acceptance", Kind: "text", Apply: ApplyLive,
		Help: "estimated dollars a job and its delegated jobs may use before it stops at a safe point and asks; 0 none"},
	{Name: optJobBudgetMinutes, Label: "Budget per job (minutes)", Section: "Budgets and acceptance", Kind: "int", Apply: ApplyLive,
		Help: "minutes since a job's agent started; bounds agents whose spend is not measured too; 0 none"},
	{Name: optJobBudgetTurns, Label: "Budget per job (turns)", Section: "Budgets and acceptance", Kind: "int", Apply: ApplyLive,
		Help: "ended turns, delegated jobs included; 0 none"},
	{Name: optJobBudgetTokens, Label: "Budget per job (tokens)", Section: "Budgets and acceptance", Kind: "int", Apply: ApplyLive,
		Help: "tokens, delegated jobs included; 0 none"},
	{Name: optDailyCeilingUSD, Label: "Fleet ceiling per day ($)", Section: "Budgets and acceptance", Kind: "text", Apply: ApplyLive,
		Help: "bounds jobs with no budget of their own and holds new starts once today's spend reaches it; 0 none"},
	{Name: optOrchCeilingUSD, Label: "Orchestrator ceiling per day ($)", Section: "Budgets and acceptance", Kind: "text", Apply: ApplyLive,
		Help: "past it the orchestrator's automatic pokes wait until tomorrow; 0 none"},
	{Name: optLeadCeilingUSD, Label: "Lead ceiling per day ($)", Section: "Budgets and acceptance", Kind: "text", Apply: ApplyLive,
		Help: "each project lead's, where its spend is measured; 0 none"},
	{Name: optCheckTimeout, Label: "Check timeout", Section: "Budgets and acceptance", Kind: "text", Apply: ApplyLive,
		Help: "longest crowsnest's check of a finished job may run (such as 30m)"},
	{Name: optCheckRounds, Label: "Check rounds", Section: "Budgets and acceptance", Kind: "int", Apply: ApplyLive,
		Help: "failed checks sent back to the agent before the job needs you"},
}

func init() { editable = append(editable, acceptSettings...) }

// acceptSettingValue renders this file's settings for the Settings page ("" and false when name
// is not one of them).
func (c Config) acceptSettingValue(name string) (string, bool) {
	money := func(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }
	switch name {
	case optCheckTimeout:
		return durationText(c.CheckTimeout.String()), true
	case optCheckRounds:
		return itoa(c.CheckRounds), true
	case optJobBudgetUSD:
		return money(c.JobBudgetUSD), true
	case optJobBudgetMinutes:
		return itoa(c.JobBudgetMinutes), true
	case optJobBudgetTurns:
		return itoa(c.JobBudgetTurns), true
	case optJobBudgetTokens:
		return strconv.FormatInt(c.JobBudgetTokens, 10), true
	case optDailyCeilingUSD:
		return money(c.DailyCeilingUSD), true
	case optOrchCeilingUSD:
		return money(c.OrchestratorCeilingUSD), true
	case optLeadCeilingUSD:
		return money(c.LeadCeilingUSD), true
	}
	return "", false
}

// withAcceptSettings copies this file's settings from from.
func (c Config) withAcceptSettings(from Config) Config {
	c.CheckTimeout, c.CheckRounds = from.CheckTimeout, from.CheckRounds
	c.JobBudgetUSD, c.JobBudgetMinutes, c.JobBudgetTurns, c.JobBudgetTokens = from.JobBudgetUSD, from.JobBudgetMinutes, from.JobBudgetTurns, from.JobBudgetTokens
	c.DailyCeilingUSD, c.OrchestratorCeilingUSD, c.LeadCeilingUSD = from.DailyCeilingUSD, from.OrchestratorCeilingUSD, from.LeadCeilingUSD
	return c
}
