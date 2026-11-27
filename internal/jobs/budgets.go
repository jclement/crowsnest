// Budgets (DESIGN.md 6.16): bounding what an unattended job may use, independently of whether it
// looks busy.
//
// A job's limits are, per measure, the first set of: its own (budget_usd, _tokens, _minutes,
// _turns), its project's defaults (dollars and minutes), the server's defaults (Settings). A
// job with no dollar or token limit is still bounded by the fleet's daily ceiling when one is
// set. Usage counts the job and every job below it (children, grandchildren): delegating does
// not escape a budget. Dollars and tokens come from internal/spend; an agent whose spend is not
// measured (Codex) shows "not measured", never zero, and is bounded by time and turns, which
// crowsnest always knows.
//
// At 80% of any limit the card warns and the agent is told once. At 100% the agent is told to
// stop at a safe point, and the job goes needs_you (cause budget) with three choices: raise the
// budget, stop the job, or let it finish the step it is in (the next end of turn puts it back in
// front of the human). Nothing is killed mid-tool.

package jobs

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/spend"
	"github.com/jclement/crowsnest/internal/store"
)

// Budget states (jobs.budget_state).
const (
	BudgetOK       = ""
	BudgetWarned   = "warned"
	BudgetExceeded = "exceeded"
	BudgetGrace    = "grace"
)

const (
	// budgetWarnAt is the share of a limit at which the job is warned.
	budgetWarnAt = 0.8
	// budgetEvery is how often one job's budget is read by the tick.
	budgetEvery = 30 * time.Second
	// maxBudgetDepth bounds how far below a job its usage is gathered.
	maxBudgetDepth = 5
)

// BudgetOptions are the server's defaults and ceilings (Settings). Zero: unlimited.
type BudgetOptions struct {
	JobUSD     float64 // per job, estimated dollars
	JobTokens  int64
	JobMinutes int // wall time since the agent started
	JobTurns   int // ended turns
	// DailyUSD is the fleet's ceiling per local day: it bounds jobs with no dollar or token limit
	// of their own, and holds new starts once reached.
	DailyUSD float64
	// OrchestratorUSD and LeadUSD are daily ceilings for the standing agents: past them their
	// automatic pokes and dispatches are held.
	OrchestratorUSD float64
	LeadUSD         float64
}

// SetBudgets applies Settings' budgets.
func (s *Service) SetBudgets(next BudgetOptions) { s.liveBudgets.Store(&next) }

// Budgets returns the budgets in force.
func (s *Service) Budgets() BudgetOptions {
	if live := s.liveBudgets.Load(); live != nil {
		return *live
	}
	return s.opts.Budgets
}

// DaySpend is today's spend as the fleet ceiling reads it.
type DaySpend struct {
	USD      float64
	Measured bool
	// BySubject is each subject's dollars today (a job id, "orchestrator", a lead's subject).
	BySubject map[string]float64
}

// BudgetLimits is one job's limits; 0 is none. Source names where each came from.
type BudgetLimits struct {
	USD     float64           `json:"usd"`
	Tokens  int64             `json:"tokens"`
	Minutes int               `json:"minutes"`
	Turns   int               `json:"turns"`
	Source  map[string]string `json:"source"` // usd|tokens|minutes|turns → job | project | default | fleet
	// Fleet: the dollar bound is the fleet's daily ceiling, not the job's own.
	Fleet bool `json:"fleet"`
}

// BudgetUse is what a job and the jobs below it have used.
type BudgetUse struct {
	USD      *float64 `json:"usd"`    // nil: not measured
	Tokens   *int64   `json:"tokens"` // nil: not measured
	Minutes  float64  `json:"minutes"`
	Turns    int      `json:"turns"`
	Measured bool     `json:"measured"`
	Children int      `json:"children"` // jobs below counted
}

// BudgetView is a job's budget as the card shows it.
type BudgetView struct {
	Limits BudgetLimits `json:"limits"`
	Used   BudgetUse    `json:"used"`
	// Share is the largest used/limit over the measures that have a limit (0 with none); Over
	// names that measure.
	Share  float64 `json:"share"`
	Over   string  `json:"over"`
	State  string  `json:"state"`
	Detail string  `json:"detail"`
	// Fleet is today's spend against the fleet's ceiling, when one bounds this job.
	FleetUSD     float64 `json:"fleet_usd,omitempty"`
	FleetCeiling float64 `json:"fleet_ceiling,omitempty"`
}

// limitsFor is r's effective limits.
func (s *Service) limitsFor(ctx context.Context, r store.JobRecord) BudgetLimits {
	defaults := s.Budgets()
	limits := BudgetLimits{Source: map[string]string{}}
	var project store.ProjectAccept
	if r.ProjectID != 0 {
		project, _ = s.opts.Store.GetProjectAccept(ctx, r.ProjectID)
	}
	pickFloat := func(key string, job, proj, def float64) float64 {
		switch {
		case job > 0:
			limits.Source[key] = "job"
			return job
		case proj > 0:
			limits.Source[key] = "project"
			return proj
		case def > 0:
			limits.Source[key] = "default"
			return def
		}
		return 0
	}
	limits.USD = pickFloat("usd", r.BudgetUSD, project.BudgetUSD, defaults.JobUSD)
	limits.Tokens = int64(pickFloat("tokens", float64(r.BudgetTokens), 0, float64(defaults.JobTokens)))
	limits.Minutes = int(pickFloat("minutes", float64(r.BudgetMinutes), float64(project.BudgetMinutes), float64(defaults.JobMinutes)))
	limits.Turns = int(pickFloat("turns", float64(r.BudgetTurns), 0, float64(defaults.JobTurns)))
	if limits.USD == 0 && limits.Tokens == 0 && defaults.DailyUSD > 0 {
		limits.Fleet = true
		limits.Source["usd"] = "fleet"
	}
	return limits
}

// usageOf is what r and the jobs below it have used.
func (s *Service) usageOf(ctx context.Context, r store.JobRecord) BudgetUse {
	records := []store.JobRecord{r}
	frontier := []int64{r.ID}
	for depth := 0; depth < maxBudgetDepth && len(frontier) > 0; depth++ {
		var next []int64
		for _, id := range frontier {
			children, err := s.opts.Store.ListChildJobs(ctx, id)
			if err != nil {
				continue
			}
			for _, child := range children {
				if child.ReviewOf != 0 || child.FixOf != 0 {
					continue // crowsnest's own reviewers and rebases are not the agent's spending
				}
				records = append(records, child)
				next = append(next, child.ID)
			}
		}
		frontier = next
	}
	use := BudgetUse{Children: len(records) - 1}
	now := s.opts.Now()
	if !r.StartedAt.IsZero() {
		end := now
		if !r.SettledAt.IsZero() {
			end = r.SettledAt
		}
		use.Minutes = math.Max(0, end.Sub(r.StartedAt).Minutes())
	}
	for _, record := range records {
		use.Turns += record.Turns
	}
	if s.opts.Spend == nil {
		return use
	}
	infos := make([]spend.JobInfo, 0, len(records))
	for _, record := range records {
		infos = append(infos, spendInfo(s.toJob(record, 0)))
	}
	summaries, err := s.opts.Spend.Summaries(ctx, infos)
	if err != nil {
		return use
	}
	var usd float64
	var tokens int64
	priced := false
	for _, info := range infos {
		summary, ok := summaries[info.ID]
		if !ok || !summary.Measured {
			continue
		}
		use.Measured = true
		tokens += summary.TotalTokens
		if summary.CostUSD != nil {
			usd += *summary.CostUSD
			priced = true
		}
	}
	if use.Measured {
		use.Tokens = &tokens
		if priced {
			use.USD = &usd
		}
	}
	return use
}

// BudgetOf builds a job's budget view (nil when it has no limit at all).
func (s *Service) BudgetOf(ctx context.Context, r store.JobRecord) *BudgetView {
	limits := s.limitsFor(ctx, r)
	if limits.USD == 0 && limits.Tokens == 0 && limits.Minutes == 0 && limits.Turns == 0 && !limits.Fleet {
		if r.BudgetState == BudgetOK {
			return nil
		}
	}
	view := &BudgetView{Limits: limits, Used: s.usageOf(ctx, r), State: r.BudgetState, Detail: r.BudgetDetail}
	if limits.Fleet {
		view.FleetCeiling = s.Budgets().DailyUSD
		if day, ok := s.daySpend(ctx); ok {
			view.FleetUSD = day.USD
		}
	}
	view.Share, view.Over = budgetShare(*view)
	return view
}

// budgetShare is the largest used/limit and the measure it is for.
func budgetShare(view BudgetView) (float64, string) {
	share, over := 0.0, ""
	consider := func(name string, used, limit float64) {
		if limit <= 0 {
			return
		}
		if value := used / limit; value > share {
			share, over = value, name
		}
	}
	if view.Used.USD != nil {
		consider("usd", *view.Used.USD, view.Limits.USD)
	}
	if view.Used.Tokens != nil {
		consider("tokens", float64(*view.Used.Tokens), float64(view.Limits.Tokens))
	}
	consider("minutes", view.Used.Minutes, float64(view.Limits.Minutes))
	consider("turns", float64(view.Used.Turns), float64(view.Limits.Turns))
	if view.Limits.Fleet && view.FleetCeiling > 0 {
		consider("fleet", view.FleetUSD, view.FleetCeiling)
	}
	return share, over
}

// describeBudget is the measure in words, for the card and the agent: "$1.64 of $2.00".
func describeBudget(view BudgetView) string {
	switch view.Over {
	case "usd":
		return fmt.Sprintf("$%.2f of its $%.2f budget", deref(view.Used.USD), view.Limits.USD)
	case "tokens":
		return fmt.Sprintf("%s of its %s-token budget", compactNumber(float64(derefInt(view.Used.Tokens))), compactNumber(float64(view.Limits.Tokens)))
	case "minutes":
		return fmt.Sprintf("%.0f of its %d minutes", view.Used.Minutes, view.Limits.Minutes)
	case "turns":
		return fmt.Sprintf("%d of its %d turns", view.Used.Turns, view.Limits.Turns)
	case "fleet":
		return fmt.Sprintf("the fleet has spent $%.2f of today's $%.2f ceiling", view.FleetUSD, view.FleetCeiling)
	}
	return ""
}

func deref(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func derefInt(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func compactNumber(value float64) string {
	switch {
	case value >= 1e6:
		return fmt.Sprintf("%.1fM", value/1e6)
	case value >= 1e3:
		return fmt.Sprintf("%.0fk", value/1e3)
	}
	return fmt.Sprintf("%.0f", value)
}

// daySpend is today's spend (cached by the caller's wiring; ok false when unknown).
func (s *Service) daySpend(ctx context.Context) (DaySpend, bool) {
	if s.opts.SpendToday == nil {
		return DaySpend{}, false
	}
	day, err := s.opts.SpendToday(ctx)
	if err != nil {
		return DaySpend{}, false
	}
	return day, true
}

// FleetCeilingReached reports whether today's spend has reached the fleet's daily ceiling: new
// starts wait, and jobs with no budget of their own are stopped at a safe point.
func (s *Service) FleetCeilingReached(ctx context.Context) (bool, string) {
	ceiling := s.Budgets().DailyUSD
	if ceiling <= 0 {
		return false, ""
	}
	day, ok := s.daySpend(ctx)
	if !ok || !day.Measured || day.USD < ceiling {
		return false, ""
	}
	return true, fmt.Sprintf("the fleet has spent $%.2f today, its ceiling is $%.2f", day.USD, ceiling)
}

// StandingCeiling reports whether a standing agent's spend today has reached its ceiling:
// subject is spend.OrchestratorSubject or a lead's subject.
func (s *Service) StandingCeiling(ctx context.Context, subject string) (bool, string) {
	budgets := s.Budgets()
	ceiling := budgets.LeadUSD
	if subject == spend.OrchestratorSubject {
		ceiling = budgets.OrchestratorUSD
	}
	if ceiling <= 0 {
		return false, ""
	}
	day, ok := s.daySpend(ctx)
	if !ok {
		return false, ""
	}
	used, measured := day.BySubject[subject]
	if !measured {
		return false, ""
	}
	if used < ceiling {
		return false, ""
	}
	return true, fmt.Sprintf("%s has spent $%.2f today, its ceiling is $%.2f", subject, used, ceiling)
}

// checkBudget reads a live job's budget and acts on the thresholds (the tick, every budgetEvery).
func (s *Service) checkBudget(ctx context.Context, r store.JobRecord) {
	if IsStanding(r.AgentName) || r.ReviewOf != 0 || r.StartedAt.IsZero() {
		return
	}
	id := FormatID(r.ID)
	watch := s.watchOf(id)
	now := s.opts.Now()
	if now.Sub(watch.budgetAt) < budgetEvery && !watch.budgetAt.IsZero() {
		return
	}
	watch.budgetAt = now
	view := s.BudgetOf(ctx, r)
	if view == nil {
		return
	}
	s.applyBudget(ctx, id, *view)
}

// applyBudget moves a job across the warning and the limit, telling the agent each time.
func (s *Service) applyBudget(ctx context.Context, id string, view BudgetView) {
	message, exceeded := "", false
	job, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		state := State(r.State)
		if state.Terminal() {
			return errSkip
		}
		what := describeBudget(view)
		switch {
		case view.Share >= 1 && r.BudgetState != BudgetExceeded && r.BudgetState != BudgetGrace:
			r.BudgetState, r.BudgetDetail = BudgetExceeded, "Used "+what
			c.event(EventBudget, "Reached its budget: used "+what+"; the agent was told to stop at a safe point", map[string]any{"over": view.Over})
			if state == StateRunning || state == StateBlocked {
				r.State, r.Cause, r.CauseDetail = string(StateNeedsYou), string(CauseBudget), "Used "+what+": raise the budget, stop the job, or let it finish the step it is in"
				r.Stuck = false
			}
			c.notify = fmt.Sprintf("crowsnest: job %s reached its budget (%s): %s", FormatID(r.ID), what, oneLine(r.Title, 80))
			exceeded = true
			message = fmt.Sprintf("[crowsnest budget] Job %s has used %s. Stop at a safe point: finish the step you are in, commit what is safe to keep, "+
				"call job_update with where you got to, and end your turn. Do not start anything new; the human decides whether to raise the budget.", FormatID(r.ID), what)
		case view.Share >= budgetWarnAt && view.Share < 1 && r.BudgetState == BudgetOK:
			r.BudgetState, r.BudgetDetail = BudgetWarned, fmt.Sprintf("Used %.0f%%: %s", view.Share*100, what)
			c.event(EventBudget, "Budget warning: "+r.BudgetDetail, map[string]any{"over": view.Over})
			message = fmt.Sprintf("[crowsnest budget] Job %s has used %s (%.0f%%). Plan to finish within it: wrap up the current approach, and if it cannot fit, say so with job_update.",
				FormatID(r.ID), what, view.Share*100)
		case view.Share < budgetWarnAt && r.BudgetState == BudgetWarned:
			r.BudgetState, r.BudgetDetail = BudgetOK, ""
			c.event(EventBudget, "Back under 80% of its budget", nil)
		default:
			return errSkip
		}
		return nil
	})
	if err != nil {
		return
	}
	if exceeded {
		// The budget covers the jobs below it: they stop at a safe point too.
		s.exceedBelow(ctx, id, describeBudget(view), 0)
	}
	s.tellBudget(ctx, job, message)
}

// tellBudget types a budget notice to a job's agent (not while paused: the card says it).
func (s *Service) tellBudget(ctx context.Context, job Job, message string) {
	if message == "" || job.PaneID == "" || s.paused() {
		return
	}
	if err := s.sendText(ctx, job.PaneID, job.AgentName, message); err != nil {
		_ = s.Journal(ctx, job.ID, EventBudget, "The budget notice could not be typed to the agent: "+oneLine(err.Error(), 160), nil)
	}
}

// exceedBelow puts the live jobs below parent in front of the human with it: their spend is
// part of the budget that ran out.
func (s *Service) exceedBelow(ctx context.Context, parent, what string, depth int) {
	numeric, err := ParseID(parent)
	if err != nil || depth >= maxBudgetDepth {
		return
	}
	children, err := s.opts.Store.ListChildJobs(ctx, numeric)
	if err != nil {
		return
	}
	for _, child := range children {
		if child.ReviewOf != 0 || child.FixOf != 0 || State(child.State).Terminal() {
			continue
		}
		id := FormatID(child.ID)
		job, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
			if State(r.State).Terminal() || r.BudgetState == BudgetExceeded {
				return errSkip
			}
			r.BudgetState, r.BudgetDetail = BudgetExceeded, "Its parent "+parent+" used "+what
			c.event(EventBudget, "Its parent "+parent+" reached its budget; told to stop at a safe point", nil)
			if state := State(r.State); state == StateRunning || state == StateBlocked {
				r.State, r.Cause, r.CauseDetail, r.Stuck = string(StateNeedsYou), string(CauseBudget), r.BudgetDetail, false
			}
			return nil
		})
		if err != nil {
			continue
		}
		s.tellBudget(ctx, job, fmt.Sprintf("[crowsnest budget] Job %s's parent %s reached its budget (it counts yours). Stop at a safe point: finish the step you are in, "+
			"commit what is safe, call job_update with where you got to, and end your turn.", id, parent))
		s.exceedBelow(ctx, id, what, depth+1)
	}
}

// BudgetInput is the human's change to a job's budget: nil leaves a measure as it is, 0 clears
// the job's own limit (its project's or the server's default applies again).
type BudgetInput struct {
	USD     *float64 `json:"usd"`
	Tokens  *int64   `json:"tokens"`
	Minutes *int     `json:"minutes"`
	Turns   *int     `json:"turns"`
}

// SetJobBudget is the human's raise (or any change): it re-reads the budget at once, and a job
// waiting on its budget goes back to work when it is now under it.
func (s *Service) SetJobBudget(ctx context.Context, id string, input BudgetInput) (Job, error) {
	for _, value := range []float64{derefOr(input.USD), float64(derefOrInt64(input.Tokens)), float64(derefOrInt(input.Minutes)), float64(derefOrInt(input.Turns))} {
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return Job{}, invalid("a budget is a positive number, or 0 for the default")
		}
	}
	job, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if State(r.State).Terminal() {
			return invalid("job %s is already %s", FormatID(r.ID), r.State)
		}
		if input.USD != nil {
			r.BudgetUSD = *input.USD
		}
		if input.Tokens != nil {
			r.BudgetTokens = *input.Tokens
		}
		if input.Minutes != nil {
			r.BudgetMinutes = *input.Minutes
		}
		if input.Turns != nil {
			r.BudgetTurns = *input.Turns
		}
		c.event(EventBudget, "You changed the budget", map[string]any{"usd": r.BudgetUSD, "tokens": r.BudgetTokens, "minutes": r.BudgetMinutes, "turns": r.BudgetTurns})
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	record, err := s.opts.Store.GetJob(ctx, mustParse(id))
	if err != nil {
		return job, err
	}
	view := s.BudgetOf(ctx, record)
	share := 0.0
	if view != nil {
		share = view.Share
	}
	resumed := false
	job, err = s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if r.BudgetState != BudgetExceeded && r.BudgetState != BudgetGrace && r.BudgetState != BudgetWarned {
			return errSkip
		}
		if share >= 1 {
			c.event(EventBudget, "Still over the budget after the change", nil)
			return nil
		}
		r.BudgetState, r.BudgetDetail = BudgetOK, ""
		if share >= budgetWarnAt {
			r.BudgetState, r.BudgetDetail = BudgetWarned, fmt.Sprintf("Used %.0f%%", share*100)
		}
		if State(r.State) == StateNeedsYou && Cause(r.Cause) == CauseBudget {
			r.State, r.Cause, r.CauseDetail = string(StateRunning), "", ""
			s.markProgressLocked(r)
			resumed = true
		}
		return nil
	})
	if err == nil && resumed && job.PaneID != "" {
		_ = s.sendText(ctx, job.PaneID, job.AgentName, fmt.Sprintf("[crowsnest budget] The human raised job %s's budget. Carry on with the work.", job.ID))
	}
	return s.jobNow(ctx, id, job), err
}

// BudgetFinishStep lets a job at its budget finish the step it is in: back to running in grace;
// its next end of turn puts it in front of the human again.
func (s *Service) BudgetFinishStep(ctx context.Context, id string) (Job, error) {
	job, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if State(r.State) != StateNeedsYou || Cause(r.Cause) != CauseBudget {
			return invalid("job %s is not waiting on its budget", FormatID(r.ID))
		}
		r.State, r.Cause, r.CauseDetail = string(StateRunning), "", ""
		r.BudgetState = BudgetGrace
		s.markProgressLocked(r)
		c.event(EventBudget, "You let it finish the step it is in; its next end of turn comes back to you", nil)
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	if job.PaneID != "" {
		_ = s.sendText(ctx, job.PaneID, job.AgentName, fmt.Sprintf("[crowsnest budget] The human lets you finish the step you are in on job %s. "+
			"Finish it, commit what is safe to keep, call job_update with where you got to, then end your turn.", job.ID))
	}
	return job, nil
}

// budgetStopLocked applies a grace stop at the Stop hook (s.mu held): the step is over, so the
// job is back in front of the human. It reports whether it did.
func budgetStopLocked(r *store.JobRecord, c *change) bool {
	if r.BudgetState != BudgetGrace || State(r.State) != StateRunning {
		return false
	}
	r.BudgetState = BudgetExceeded
	r.State, r.Cause = string(StateNeedsYou), string(CauseBudget)
	r.CauseDetail = firstNonEmptyDir(r.BudgetDetail, "Reached its budget") + "; it finished the step it was in"
	c.event(EventBudget, "Finished the step it was in; back to you for the budget", nil)
	return true
}

func derefOr(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func derefOrInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func derefOrInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

// budgetLine is the job's budget in one line for job_get.
func budgetLine(view *BudgetView) string {
	if view == nil {
		return ""
	}
	var parts []string
	if view.Limits.USD > 0 {
		if view.Used.USD != nil {
			parts = append(parts, fmt.Sprintf("$%.2f of $%.2f", *view.Used.USD, view.Limits.USD))
		} else {
			parts = append(parts, fmt.Sprintf("$%.2f (your spend is not measured)", view.Limits.USD))
		}
	}
	if view.Limits.Minutes > 0 {
		parts = append(parts, fmt.Sprintf("%.0f of %d minutes", view.Used.Minutes, view.Limits.Minutes))
	}
	if view.Limits.Turns > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d turns", view.Used.Turns, view.Limits.Turns))
	}
	if view.Limits.Tokens > 0 && view.Used.Tokens != nil {
		parts = append(parts, fmt.Sprintf("%s of %s tokens", compactNumber(float64(*view.Used.Tokens)), compactNumber(float64(view.Limits.Tokens))))
	}
	if len(parts) == 0 {
		return ""
	}
	return "\n## Budget\n\nUsed so far (you and any jobs you delegated): " + strings.Join(parts, ", ") + ". At 80% you are warned; at 100% you are asked to stop at a safe point.\n"
}
