// Package spend measures what each job, and the orchestrator's own conversation, has used:
// tokens in, out, written to and read from the cache, model calls, tool calls, wall time, and an
// estimated cost in USD (DESIGN.md 7.11).
//
// The source of truth is the agent's Claude transcript (internal/transcript.ReadUsage). The Meter
// reads every measured subject's transcripts incrementally, its subagents' files included,
// deduplicates calls by message id, and adds the result to the store: running totals per subject
// and per-day rollups by pricing key. Costs are computed when read, from the rollups and the
// price table (prices.go), and are always an estimate: subscription plans are not billed per
// token, so the figure is what the same use would cost on the API.
//
// The package is a leaf: it knows nothing of jobs. The serve wiring tells the Meter which
// subjects exist and which sessions they used; jobs ask SpendOf for a job's numbers.
package spend

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// OrchestratorSubject is the orchestrator's own conversation as a spend subject.
const OrchestratorSubject = "orchestrator"

// Tokens are token counts by kind.
type Tokens struct {
	Input        int64 `json:"input"`
	Output       int64 `json:"output"`
	CacheWrite5m int64 `json:"cache_write_5m"`
	CacheWrite1h int64 `json:"cache_write_1h"`
	CacheRead    int64 `json:"cache_read"`
}

func tokensOf(c store.SpendCounts) Tokens {
	return Tokens{Input: c.Input, Output: c.Output, CacheWrite5m: c.CacheWrite5m, CacheWrite1h: c.CacheWrite1h, CacheRead: c.CacheRead}
}

// Not-measured reasons (Summary.Reason).
const (
	// ReasonCodex: Codex records its usage in its own session files, which crowsnest does not map
	// to jobs; shown as "not measured", never as zero.
	ReasonCodex = "codex"
	// ReasonOrchestrator: the orchestrator does the job in its own conversation, so its use is on
	// the orchestrator's line.
	ReasonOrchestrator = "orchestrator"
)

// Summary is a subject's spend as the APIs return it.
type Summary struct {
	// Measured is false when crowsnest cannot measure this subject (Reason says why); every
	// count is then zero and means "unknown", not "nothing".
	Measured bool   `json:"measured"`
	Reason   string `json:"reason,omitempty"`

	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"` // 5-minute and 1-hour together
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	Turns            int64 `json:"turns"` // model calls
	ToolCalls        int64 `json:"tool_calls"`
	// WallSeconds: a job's from its start to when it settled (or now); the orchestrator's from its
	// first call measured to its last.
	WallSeconds int64 `json:"wall_seconds"`

	// CostUSD is the estimated API-equivalent cost; nil when no token could be priced (an unknown
	// model), never a misleading zero.
	CostUSD *float64 `json:"cost_usd"`
	// CostPartial: some tokens are from a model the price table does not know, so CostUSD covers
	// only the rest (UnpricedTokens).
	CostPartial    bool  `json:"cost_partial"`
	UnpricedTokens int64 `json:"unpriced_tokens"`
	// CacheHitShare is cache reads over all prompt tokens (fresh input, cache writes and reads).
	CacheHitShare float64  `json:"cache_hit_share"`
	Models        []string `json:"models"`

	FirstAt *time.Time `json:"first_at"`
	LastAt  *time.Time `json:"last_at"`
}

// costAccumulator sums a subject's cost from its per-key rows.
type costAccumulator struct {
	nano     int64
	priced   bool
	unpriced int64
	models   map[string]bool
}

func (a *costAccumulator) add(prices *Prices, key string, counts store.SpendCounts) {
	if a.models == nil {
		a.models = map[string]bool{}
	}
	a.models[ModelOf(key)] = true
	tokens := tokensOf(counts)
	nano, ok := prices.Cost(key, tokens)
	if !ok {
		a.unpriced += tokens.Input + tokens.Output + tokens.CacheWrite5m + tokens.CacheWrite1h + tokens.CacheRead
		return
	}
	a.nano += nano
	a.priced = true
}

// apply writes the cost fields into s.
func (a *costAccumulator) apply(s *Summary) {
	if a.priced {
		usd := float64(a.nano) / 1e9
		s.CostUSD = &usd
	}
	s.UnpricedTokens = a.unpriced
	s.CostPartial = a.priced && a.unpriced > 0
	s.Models = make([]string, 0, len(a.models))
	for model := range a.models {
		s.Models = append(s.Models, model)
	}
	sort.Strings(s.Models)
}

// fillCounts sets the token fields and the cache-hit share from counts.
func fillCounts(s *Summary, c store.SpendCounts) {
	s.InputTokens, s.OutputTokens, s.CacheReadTokens = c.Input, c.Output, c.CacheRead
	s.CacheWriteTokens = c.CacheWrite5m + c.CacheWrite1h
	s.TotalTokens = c.Input + c.Output + s.CacheWriteTokens + c.CacheRead
	s.Turns, s.ToolCalls = c.Turns, c.ToolCalls
	if prompt := c.Input + s.CacheWriteTokens + c.CacheRead; prompt > 0 {
		s.CacheHitShare = float64(c.CacheRead) / float64(prompt)
	}
}

// JobInfo is what a summary needs to know about a job: the jobs package fills it.
type JobInfo struct {
	ID           string // j_<n>
	Engine       string // claude or codex
	Orchestrated bool   // held by the orchestrator: its use is on the orchestrator's line
	StartedAt    *time.Time
	SettledAt    *time.Time
}

// Reader answers spend questions from the store. The Meter embeds one; tests and the demo use it
// directly.
type Reader struct {
	Store  *store.Store
	Prices *Prices
	Now    func() time.Time
}

// SpendOf is one job's spend: the function the job_get tool, `crowsnest job get` and (later)
// budgets read. A job never measured has a zero, measured summary.
func (r *Reader) SpendOf(ctx context.Context, job JobInfo) (Summary, error) {
	summaries, err := r.Summaries(ctx, []JobInfo{job})
	if err != nil {
		return Summary{}, err
	}
	return summaries[job.ID], nil
}

// Summaries returns the spend of many jobs at once (the board), keyed by job id.
func (r *Reader) Summaries(ctx context.Context, jobs []JobInfo) (map[string]Summary, error) {
	out := make(map[string]Summary, len(jobs))
	subjects := make([]string, 0, len(jobs))
	for _, job := range jobs {
		switch {
		case job.Orchestrated:
			out[job.ID] = Summary{Reason: ReasonOrchestrator, Models: []string{}}
		case job.Engine == "codex":
			out[job.ID] = Summary{Reason: ReasonCodex, Models: []string{}}
		default:
			subjects = append(subjects, job.ID)
		}
	}
	summaries, err := r.subjectSummaries(ctx, subjects)
	if err != nil {
		return nil, err
	}
	now := r.now()
	for _, job := range jobs {
		summary, ok := summaries[job.ID]
		if !ok {
			continue
		}
		summary.WallSeconds = wallSeconds(job.StartedAt, job.SettledAt, now)
		out[job.ID] = summary
	}
	return out, nil
}

// Orchestrator is the orchestrator conversation's spend; its wall time spans its first to its
// last measured call.
func (r *Reader) Orchestrator(ctx context.Context) (Summary, error) {
	summaries, err := r.subjectSummaries(ctx, []string{OrchestratorSubject})
	if err != nil {
		return Summary{}, err
	}
	summary := summaries[OrchestratorSubject]
	if summary.FirstAt != nil && summary.LastAt != nil {
		summary.WallSeconds = int64(summary.LastAt.Sub(*summary.FirstAt).Seconds())
	}
	return summary, nil
}

// subjectSummaries builds measured summaries (counts, cost, models, times) for subjects; every
// subject asked for is in the result, zero when nothing was recorded.
func (r *Reader) subjectSummaries(ctx context.Context, subjects []string) (map[string]Summary, error) {
	out := make(map[string]Summary, len(subjects))
	if len(subjects) == 0 {
		return out, nil
	}
	totals, err := r.Store.ListSpendTotals(ctx, subjects)
	if err != nil {
		return nil, fmt.Errorf("reading spend: %w", err)
	}
	byKey, err := r.Store.SpendBySubjectModel(ctx, subjects)
	if err != nil {
		return nil, fmt.Errorf("reading spend by model: %w", err)
	}
	costs := map[string]*costAccumulator{}
	for _, row := range byKey {
		if costs[row.Subject] == nil {
			costs[row.Subject] = &costAccumulator{}
		}
		costs[row.Subject].add(r.Prices, row.Model, row.SpendCounts)
	}
	for _, subject := range subjects {
		out[subject] = Summary{Measured: true, Models: []string{}}
	}
	for _, row := range totals {
		summary := Summary{Measured: true}
		fillCounts(&summary, row.SpendCounts)
		if !row.FirstAt.IsZero() {
			first, last := row.FirstAt, row.LastAt
			summary.FirstAt, summary.LastAt = &first, &last
		}
		accumulator := costs[row.Subject]
		if accumulator == nil {
			accumulator = &costAccumulator{}
		}
		accumulator.apply(&summary)
		out[row.Subject] = summary
	}
	return out, nil
}

func (r *Reader) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// wallSeconds is the time from started to settled, or to now while the job is live.
func wallSeconds(started, settled *time.Time, now time.Time) int64 {
	if started == nil || started.IsZero() {
		return 0
	}
	end := now
	if settled != nil && !settled.IsZero() {
		end = *settled
	}
	if end.Before(*started) {
		return 0
	}
	return int64(end.Sub(*started).Seconds())
}

// Turn is one model call for a job's chart.
type Turn struct {
	At      time.Time `json:"at"`
	Model   string    `json:"model"`
	Tokens  int64     `json:"tokens"`   // every kind together
	Output  int64     `json:"output"`   // output tokens alone
	CostUSD *float64  `json:"cost_usd"` // nil for an unknown model
}

// maxChartTurns is how many calls a chart shows (and how many the store keeps per subject).
const maxChartTurns = 400

// Turns returns a subject's latest calls, oldest first.
func (r *Reader) Turns(ctx context.Context, subject string) ([]Turn, error) {
	rows, err := r.Store.ListSpendTurns(ctx, subject, maxChartTurns)
	if err != nil {
		return nil, err
	}
	out := make([]Turn, 0, len(rows))
	for _, row := range rows {
		tokens := tokensOf(row.SpendCounts)
		turn := Turn{At: row.At, Model: ModelOf(row.Model), Output: tokens.Output,
			Tokens: tokens.Input + tokens.Output + tokens.CacheWrite5m + tokens.CacheWrite1h + tokens.CacheRead}
		if nano, ok := r.Prices.Cost(row.Model, tokens); ok {
			usd := float64(nano) / 1e9
			turn.CostUSD = &usd
		}
		out = append(out, turn)
	}
	return out, nil
}
