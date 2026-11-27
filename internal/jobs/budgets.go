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
	"errors"
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
		case view.Share >= 1 && r.BudgetState != BudgetGrace && !hasOwnBudgetHold(*r):
			r.BudgetState, r.BudgetDetail = BudgetExceeded, "Used "+what
			c.event(EventBudget, "Reached its budget: used "+what+"; the agent was told to stop at a safe point", map[string]any{"over": view.Over})
			// A hold, not just a state (R6-C01): nothing the agent declares lifts it, and a job
			// crowsnest is checking is held the moment the check sends it back to work.
			s.placeHoldLocked(r, c, HoldBudget, "Used "+what+": raise the budget, stop the job, or let it finish the step it is in", "")
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
		s.exceedBelow(ctx, id, id, describeBudget(view), 0)
	}
	s.tellBudget(ctx, job, message)
}

// hasOwnBudgetHold reports whether r is held by its own budget (not one a job above it reached):
// a child its parent's budget stopped is still held by its own when that runs out (R6-C05).
func hasOwnBudgetHold(r store.JobRecord) bool {
	for _, hold := range holdsOf(r) {
		if hold.Kind == HoldBudget && hold.From == "" {
			return true
		}
	}
	return false
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
// part of the budget that ran out. Each gets a budget hold from root (the job whose budget ran
// out), queued ones too, so a child waiting for a slot does not start past it (R6-C05); raising
// root's budget lifts them (liftBudgetBelow).
func (s *Service) exceedBelow(ctx context.Context, root, parent, what string, depth int) {
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
		alreadyHeld := false
		job, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
			if State(r.State).Terminal() {
				return errSkip
			}
			if held, ok := holdOf(*r, HoldBudget); ok && held.From == root {
				alreadyHeld = true
				return errSkip
			}
			if r.BudgetState != BudgetExceeded {
				r.BudgetState, r.BudgetDetail = BudgetExceeded, "Its parent "+parent+" used "+what
			}
			c.event(EventBudget, "Its parent "+parent+" reached its budget; told to stop at a safe point", nil)
			s.placeHoldLocked(r, c, HoldBudget, "Its parent "+parent+" used "+what+": raise "+root+"'s budget, or stop the job", root)
			return nil
		})
		if alreadyHeld {
			// Held by an earlier walk that may have been cut short below it (R7-C02): go on down.
			s.exceedBelow(ctx, root, id, what, depth+1)
			continue
		}
		if err != nil {
			continue
		}
		s.tellBudget(ctx, job, fmt.Sprintf("[crowsnest budget] Job %s's parent %s reached its budget (it counts yours). Stop at a safe point: finish the step you are in, "+
			"commit what is safe, call job_update with where you got to, and end your turn.", id, parent))
		s.exceedBelow(ctx, root, id, what, depth+1)
	}
}

// reconcileBudgetHolds derives the jobs below every job held by its own budget from that hold,
// on every tick and so at the first one after a start (R7-C02). The walk after the hold is placed
// is a side effect a crash or a failed child write can cut short; the parent's hold is the durable
// fact, and this puts back whatever the walk did not finish. Placing an existing hold is a no-op.
func (s *Service) reconcileBudgetHolds(ctx context.Context) {
	var roots []store.JobRecord
	if err := s.eachLive(ctx, append(append([]State{}, liveStates...), StateInReview), func(r store.JobRecord) {
		if hasOwnBudgetHold(r) {
			roots = append(roots, r)
		}
	}); err != nil && ctx.Err() == nil {
		s.opts.Logger.Warn("supervisor: listing jobs for budget holds", "err", err)
	}
	for _, r := range roots {
		what := strings.TrimPrefix(r.BudgetDetail, "Used ")
		s.exceedBelow(ctx, FormatID(r.ID), FormatID(r.ID), what, 0)
	}
}

// ancestorBudgetHold returns why r may not start because a job above it is held by its own
// budget ("" when none is). It reads the ancestors themselves, so a child the walk has not reached
// yet is still held (R7-C02); an ancestor that cannot be read holds it too (fail closed).
func (s *Service) ancestorBudgetHold(ctx context.Context, r store.JobRecord) string {
	parent := r.ParentID
	for depth := 0; parent != 0; depth++ {
		if depth >= maxBudgetDepth {
			return ""
		}
		above, err := s.opts.Store.GetJob(ctx, parent)
		if errors.Is(err, store.ErrNotFound) {
			return ""
		}
		if err != nil {
			return "crowsnest could not read job " + FormatID(parent) + " above it, whose budget covers it; it starts once that can be read"
		}
		if !State(above.State).Terminal() && hasOwnBudgetHold(above) {
			return "job " + FormatID(above.ID) + " above it reached its budget: raise that budget, or stop the job"
		}
		parent = above.ParentID
	}
	return ""
}

// liftBudgetBelow lifts the budget holds root's exhausted budget put on the jobs below it, now
// that root is under its budget again (the human raised it).
func (s *Service) liftBudgetBelow(ctx context.Context, root, parent string, depth int) {
	numeric, err := ParseID(parent)
	if err != nil || depth >= maxBudgetDepth {
		return
	}
	children, err := s.opts.Store.ListChildJobs(ctx, numeric)
	if err != nil {
		return
	}
	for _, child := range children {
		if State(child.State).Terminal() {
			continue
		}
		id := FormatID(child.ID)
		job, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
			if !liftHoldLocked(r, c, HoldBudget, LiftHuman, func(h Hold) bool { return h.From == root }) {
				return errSkip
			}
			if !hasHold(*r, HoldBudget) && r.BudgetState == BudgetExceeded {
				r.BudgetState, r.BudgetDetail = BudgetOK, ""
			}
			c.event(EventBudget, root+"'s budget was raised; this job may carry on", nil)
			if State(r.State) == StateRunning {
				s.markProgressLocked(r)
			}
			return nil
		})
		if err == nil && job.State == StateRunning && job.PaneID != "" {
			_ = s.sendText(ctx, job.PaneID, job.AgentName, fmt.Sprintf("[crowsnest budget] The human raised the budget of %s, which covers job %s. Carry on with the work.", root, id))
		}
		s.liftBudgetBelow(ctx, root, id, depth+1)
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
		// The human's raise lifts the hold of this job's own budget; one its parent's budget put
		// on it stays until that budget is raised.
		liftHoldLocked(r, c, HoldBudget, LiftHuman, func(h Hold) bool { return h.From == "" })
		if State(r.State) == StateRunning && !hasHold(*r, HoldBudget) {
			s.markProgressLocked(r)
			resumed = true
		}
		return nil
	})
	if errors.Is(err, errSkip) {
		err = nil // nothing waited on the budget: the change itself is recorded
	}
	if err == nil && share < 1 {
		s.liftBudgetBelow(ctx, id, id, 0)
	}
	if err == nil && resumed && job.PaneID != "" {
		_ = s.sendText(ctx, job.PaneID, job.AgentName, fmt.Sprintf("[crowsnest budget] The human raised job %s's budget. Carry on with the work.", job.ID))
	}
	return s.jobNow(ctx, id, job), err
}

// BudgetFinishStep lets a job at its budget finish the step it is in: back to running in grace;
// its next end of turn puts it in front of the human again.
func (s *Service) BudgetFinishStep(ctx context.Context, id string) (Job, error) {
	job, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if !hasHold(*r, HoldBudget) {
			return invalid("job %s is not waiting on its budget", FormatID(r.ID))
		}
		liftHoldLocked(r, c, HoldBudget, LiftHuman, nil)
		if State(r.State) == StateNeedsYou && Cause(r.Cause) == CauseBudget {
			r.State, r.Cause, r.CauseDetail = string(StateRunning), "", ""
		}
		r.BudgetState = BudgetGrace
		s.markProgressLocked(r)
		c.event(EventBudget, "You let it finish the step it is in; its next end of turn comes back to you", nil)
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	_ = s.opts.Store.SetKV(ctx, graceKey(job.ID), s.opts.Now().UTC().Format(time.RFC3339Nano)) // R7-C03: bounded in time too
	if job.PaneID != "" {
		_ = s.sendText(ctx, job.PaneID, job.AgentName, fmt.Sprintf("[crowsnest budget] The human lets you finish the step you are in on job %s. "+
			"Finish it, commit what is safe to keep, call job_update with where you got to, then end your turn.", job.ID))
	}
	return job, nil
}

// budgetStopLocked applies a grace stop at an observed end of turn (s.mu held): the Stop hook,
// or the backstop for an agent without hooks (R6-C06). The step is over, so the job is held for
// the human again. It reports whether it did.
//
// Grace ends at the first end of turn whatever state the agent declared (R7-C03): an agent that
// declares blocked with a short recheck before ending its turn would otherwise keep grace across
// the wake and every turn after it. The hold then keeps the job (and its wake) for the human.
func (s *Service) budgetStopLocked(r *store.JobRecord, c *change) bool {
	if r.BudgetState != BudgetGrace || State(r.State).Terminal() {
		return false
	}
	r.BudgetState = BudgetExceeded
	s.placeHoldLocked(r, c, HoldBudget, firstNonEmptyDir(r.BudgetDetail, "Reached its budget")+"; it finished the step it was in", "")
	c.event(EventBudget, "Finished the step it was in; back to you for the budget", nil)
	return true
}

// budgetGraceMax bounds "finish the step you are in" in time as well (R7-C03): an agent that
// never ends its turn, or one whose end of turn crowsnest cannot see, is back in front of the
// human after this long all the same.
const budgetGraceMax = 30 * time.Minute

// graceKey is the kv row holding when a job's grace began (kv, not a column: nothing else needs it).
func graceKey(id string) string { return "budget.grace_at." + id }

// checkGraceExpiry ends a grace that has run past budgetGraceMax. A grace with no recorded start
// (granted before this was built, or the row lost) starts its clock now.
func (s *Service) checkGraceExpiry(ctx context.Context, r store.JobRecord) {
	id := FormatID(r.ID)
	if r.BudgetState != BudgetGrace {
		return
	}
	now := s.opts.Now()
	since, err := s.opts.Store.GetKV(ctx, graceKey(id))
	began, parseErr := time.Parse(time.RFC3339Nano, since)
	if err != nil || parseErr != nil {
		_ = s.opts.Store.SetKV(ctx, graceKey(id), now.UTC().Format(time.RFC3339Nano))
		return
	}
	if now.Sub(began) < budgetGraceMax {
		return
	}
	_, _ = s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if r.BudgetState != BudgetGrace || State(r.State).Terminal() {
			return errSkip
		}
		r.BudgetState = BudgetExceeded
		s.placeHoldLocked(r, c, HoldBudget, firstNonEmptyDir(r.BudgetDetail, "Reached its budget")+
			fmt.Sprintf("; the step it was let finish ran past %d minutes", int(budgetGraceMax/time.Minute)), "")
		c.event(EventBudget, fmt.Sprintf("The step it was let finish ran past %d minutes; back to you for the budget", int(budgetGraceMax/time.Minute)), nil)
		return nil
	})
	_ = s.opts.Store.DeleteKV(ctx, graceKey(id))
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
