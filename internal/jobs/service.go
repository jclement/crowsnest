// The Service: every change to a job goes through mutate, under one lock, so a hook, an agent
// call, the recovery tick and a browser action never interleave on the same row. mutate loads
// the row, applies the change, re-derives needs_you from the job's questions, validates the
// state change with CheckTransition, saves, journals the events and publishes a `job` event.

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/spend"
	"github.com/jclement/crowsnest/internal/store"
)

const (
	// MaxBriefBytes caps a brief; it is typed into the agent as its first prompt.
	MaxBriefBytes = 24 << 10
	// maxProgressRunes caps the one-line progress.
	maxProgressRunes = 200
	// maxResultBytes caps result_md.
	maxResultBytes = 64 << 10
	// timelineLimit bounds one detail's timeline.
	timelineLimit = 300
	// listLimit bounds the board. It is a page for people, never the supervisor's work set: the
	// tick and the backstop page through every live job (R2-C16).
	listLimit = 300
	// superviseBatch is the page size the supervisor reads live jobs in.
	superviseBatch = 200
	// progressStale is how old a job_update may get before "doing now" falls back to the last
	// tool call (6.5).
	progressStale = 5 * time.Minute
)

// ErrNotFound is returned for an unknown job.
var ErrNotFound = errors.New("no such job")

// ValidationError is a refusal worded for whoever sent the request (an agent or the browser).
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

// IsValidation reports whether err is a ValidationError or a TransitionError.
func IsValidation(err error) bool {
	var validation *ValidationError
	var transition *TransitionError
	return errors.As(err, &validation) || errors.As(err, &transition)
}

func invalid(format string, args ...any) error {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}

// OrchestratorInfo is what the launcher needs to know about the orchestrator.
type OrchestratorInfo struct {
	PaneID string // empty: no orchestrator pane yet
	Ready  bool   // idle, working or blocked: it can be given a job
}

// ActivityFunc reports a job agent's transcript activity: when the transcript last grew, and a
// summary of the last tool call. serve wires it to internal/transcript (M4's tailer).
type ActivityFunc func(ctx context.Context, job Job) (lastAppend time.Time, lastTool string)

// Options configure a Service. Store is required; the rest may be zero in tests.
type Options struct {
	Store  *store.Store
	Hub    *hub.Hub
	Logger *slog.Logger
	Now    func() time.Time

	// Herdr is the session's client; nil means jobs are recorded but never launched.
	Herdr herdr.Client
	// Launch is the launcher's configuration.
	Launch LaunchOptions
	// Orchestrator reports the orchestrator's pane; nil means no-dir jobs stay queued.
	Orchestrator func() OrchestratorInfo
	// Projects are the projects and their running leads (DESIGN.md 21); nil: no projects.
	Projects ProjectSource
	// ApprovalPending reports whether a job has a tool call waiting for approval (8.7); the tick
	// uses it to bring back a job whose "settled" callback was lost. nil: never.
	ApprovalPending func(ctx context.Context, jobID string) bool
	// CancelQuestions cancels a job's open questions, with reason as their note and nothing typed
	// back (a cancelled job's questions must not wait in Needs you); nil: they stay open.
	CancelQuestions func(ctx context.Context, jobID, reason string)
	// Activity reads transcript activity for the heartbeat; nil disables it.
	Activity ActivityFunc
	// Notify tells Jeff something (serve wires --notify-url). It must not block.
	Notify func(text string)
	// Roles are the role prompts (embedded plus overrides). Nil: the embedded set.
	Roles *Roles
	// Synchronous runs launches and restarts inline instead of in goroutines (tests).
	Synchronous bool
	// AskHuman asks the human a question (questions.Service.Ask): a job an agent asked for beyond
	// its own directory, engine or isolation waits on one (R2-H1). Nil: such jobs are refused.
	AskHuman func(ctx context.Context, input questions.AskInput, caller questions.Caller) (questions.Question, error)
	// Delegation bounds what agents may create; zero fields take the defaults (delegate.go).
	Delegation DelegationLimits
	// Spend reads what jobs have used (internal/spend); nil: jobs carry no spend.
	Spend SpendReader
	// DefaultModel is the model a Claude job agent gets when its job names none and does not
	// inherit one from its parent (--job-model). Empty: Claude's own default.
	DefaultModel string
	// DefaultEngine is the engine of a job with a directory that names none (--engine, resolved:
	// claude or codex). Empty: claude. A job without a directory goes to the orchestrator.
	DefaultEngine string
	// Attachments hands a job the files the human gave with it (internal/attach, wired in
	// serve). Nil: a job given with attachments is refused.
	Attachments BriefAttacher
	// Instructions composes the instruction layers a job agent is started with (global, role,
	// project; DESIGN.md 21.3) and records them. Nil: the bare role prompt, as before layers.
	Instructions InstructionLayers
	// Pause is the automation's pause switch (DESIGN.md 6.13): while paused nothing automatic
	// reaches an agent. Nil: never paused.
	Pause PauseSwitch
	// Throttle limits the job agents running at once and holds new starts on a busy machine
	// (6.12). Nil: no limit.
	Throttle StartThrottle
	// Landing configures the review gate and Land (DESIGN.md 6.7, 6.11). Zero: no review gate,
	// merge commits, no protected branches.
	Landing LandingOptions
	// Accept turns on acceptance: crowsnest's own evidence for a finished coding job (6.15).
	// Zero: off, done is the agent's word.
	Accept AcceptOptions
	// Budgets are the default budgets and the fleet's ceilings (6.16). Zero: unlimited.
	Budgets BudgetOptions
	// Limits is the fleet-wide usage limit (6.17); nil: each job retries on its own.
	Limits *FleetLimits
	// SpendToday is today's spend for the fleet's ceiling and the standing agents' (6.16); nil:
	// the ceilings never apply.
	SpendToday func(ctx context.Context) (DaySpend, error)
	// OnViolation is told each new wait-invariant violation (6.18), for the server's journal.
	// Nil: they are on the job's timeline and the System page only.
	OnViolation func(Violation)
	// OnReceived is told when an answer is seen taken in by its asker (6.19), so the Inbox can
	// update. Nil: nothing is published.
	OnReceived func(questionID int64)
}

// InstructionLayers is what the launcher needs of internal/instructions, as an interface so that
// package can import this one (it reads the built-in roles) without a cycle.
type InstructionLayers interface {
	// JobLayers returns the markdown that takes the place of the bare role prompt for job's
	// agent: the global, role and project layers in that order. With record set it also records
	// what the agent is being started with, for "instructions changed since start".
	JobLayers(ctx context.Context, job Job, record bool) (string, error)
}

// BriefAttacher is what the jobs service needs of attachments (DESIGN.md 8.6): to check the
// files named with a new job before it is recorded, to copy them into its inbox once it has an
// id (before its brief is written), and to list them in the brief.
type BriefAttacher interface {
	CheckBrief(ctx context.Context, ids []string) error
	AttachBrief(ctx context.Context, job Job, ids []string) error
	BriefLines(ctx context.Context, jobID string) []string
}

// SpendReader is the part of internal/spend the jobs service reads (a *spend.Meter in serve).
type SpendReader interface {
	Summaries(ctx context.Context, jobs []spend.JobInfo) (map[string]spend.Summary, error)
	Turns(ctx context.Context, subject string) ([]spend.Turn, error)
}

// Service holds the jobs.
type Service struct {
	opts Options
	// liveDefaults overrides opts.DefaultModel and the default engine (Settings, SetDefaults).
	liveDefaults atomic.Pointer[[2]string]
	// liveLanding overrides opts.Landing's review default and merge mode (Settings, SetLanding).
	liveLanding atomic.Pointer[LandingOptions]
	// liveAccept and liveBudgets override opts.Accept and opts.Budgets (Settings).
	liveAccept  atomic.Pointer[AcceptOptions]
	liveBudgets atomic.Pointer[BudgetOptions]
	mu          sync.Mutex
	// instance names this process in the lifecycle claims it writes (lifecycle.go).
	instance string

	busyMu sync.Mutex
	busy   map[string]bool // jobs with a launch or restart running in the background

	effectsMu sync.Mutex
	effects   map[string]*inflightEffect // a step's herdr effect while it runs (lifecycle.go)

	checksMu sync.Mutex
	checks   map[string]bool // jobs whose check this process is running (accept.go)

	landingMu  sync.Mutex
	landingNow map[string]bool // jobs whose allowed land this process is running (landrequest.go)

	watchMu sync.Mutex
	watch   map[string]*jobWatch // per-job supervisor memory: spinning samples, budget reads (spinning.go)

	inv invariants // the latest wait-invariant check (waits.go)
}

// New returns a Service.
func New(opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Roles == nil {
		opts.Roles = EmbeddedRoles()
	}
	if opts.Notify == nil {
		opts.Notify = func(string) {}
	}
	return &Service{opts: opts, instance: newInstance()}
}

// Roles returns the role set.
func (s *Service) Roles() *Roles { return s.opts.Roles }

// ---- Reading -------------------------------------------------------------------------------

// Get returns a job with its timeline and questions.
func (s *Service) Get(ctx context.Context, id string) (Detail, error) {
	numeric, err := ParseID(id)
	if err != nil {
		return Detail{}, ErrNotFound
	}
	record, err := s.opts.Store.GetJob(ctx, numeric)
	if errors.Is(err, store.ErrNotFound) {
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, err
	}
	questionList, err := s.questionsOf(ctx, numeric)
	if err != nil {
		return Detail{}, err
	}
	events, err := s.opts.Store.ListJobEvents(ctx, numeric, timelineLimit)
	if err != nil {
		return Detail{}, err
	}
	detail := Detail{Job: s.toJob(record, countOpen(questionList)), Questions: questionList, Timeline: []Event{}}
	for _, event := range events {
		detail.Timeline = append(detail.Timeline, toEvent(event))
	}
	jobsWithSpend := []Job{detail.Job}
	s.attachSpend(ctx, jobsWithSpend)
	detail.Job = jobsWithSpend[0]
	if detail.AcceptState != AcceptNone {
		detail.Evidence = s.EvidenceOf(ctx, detail.Job)
		if fresh, err := s.opts.Store.GetJob(ctx, numeric); err == nil {
			detail.AcceptState, detail.AcceptDetail = fresh.AcceptState, fresh.AcceptDetail
		}
	}
	if !detail.State.Terminal() {
		detail.Budget = s.BudgetOf(ctx, record)
	}
	if s.opts.Spend != nil && detail.Spend != nil && detail.Spend.Measured {
		if turns, err := s.opts.Spend.Turns(ctx, detail.ID); err == nil {
			detail.SpendTurns = turns
		}
	}
	return detail, nil
}

// List returns jobs, newest first; states filters (empty: all).
func (s *Service) List(ctx context.Context, states []State) ([]Job, error) {
	names := make([]string, 0, len(states))
	for _, state := range states {
		if !state.Valid() {
			return nil, invalid("unknown state %q", state)
		}
		names = append(names, string(state))
	}
	records, err := s.opts.Store.ListJobs(ctx, names, listLimit)
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(records))
	for _, record := range records {
		open := 0
		if !State(record.State).Terminal() {
			summary, err := s.opts.Store.SummarizeJobQuestions(ctx, FormatID(record.ID))
			if err != nil {
				return nil, err
			}
			open = summary.Open
		}
		job := s.toJob(record, open)
		if !job.State.Terminal() {
			job.Budget = s.BudgetOf(ctx, record)
		}
		out = append(out, job)
	}
	s.attachSpend(ctx, out)
	return out, nil
}

// ---- Spend ---------------------------------------------------------------------------------

// spendInfo is what internal/spend needs to know about a job.
func spendInfo(job Job) spend.JobInfo {
	return spend.JobInfo{ID: job.ID, Engine: job.Engine, Orchestrated: IsStanding(job.AgentName),
		LeadHeld:  strings.HasPrefix(job.AgentName, LeadPrefix),
		StartedAt: job.StartedAt, SettledAt: job.SettledAt}
}

// attachSpend fills each job's Spend. A failed read leaves them without: spend is information,
// never a reason to fail the board.
func (s *Service) attachSpend(ctx context.Context, list []Job) {
	if s.opts.Spend == nil || len(list) == 0 {
		return
	}
	infos := make([]spend.JobInfo, len(list))
	for i, job := range list {
		infos[i] = spendInfo(job)
	}
	summaries, err := s.opts.Spend.Summaries(ctx, infos)
	if err != nil {
		s.opts.Logger.Debug("reading spend failed", "err", err)
		return
	}
	for i := range list {
		if summary, ok := summaries[list[i].ID]; ok {
			list[i].Spend = &summary
		}
	}
}

// SpendOf is a job's own spend so far: what job_get shows the agent, and the hook budgets will
// hang off (6.7). ok is false when this server measures nothing.
func (s *Service) SpendOf(ctx context.Context, job Job) (spend.Summary, bool, error) {
	if s.opts.Spend == nil {
		return spend.Summary{}, false, nil
	}
	summaries, err := s.opts.Spend.Summaries(ctx, []spend.JobInfo{spendInfo(job)})
	if err != nil {
		return spend.Summary{}, false, err
	}
	summary, ok := summaries[job.ID]
	return summary, ok, nil
}

// eachLive calls fn for every live job, oldest first, a page at a time (R2-C16: the supervisor
// must see every live job, not the board's newest page).
func (s *Service) eachLive(ctx context.Context, states []State, fn func(store.JobRecord)) error {
	names := make([]string, len(states))
	for i, state := range states {
		names[i] = string(state)
	}
	after := int64(0)
	for {
		page, err := s.opts.Store.ListJobsPage(ctx, names, after, superviseBatch)
		if err != nil {
			return err
		}
		for _, record := range page {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			fn(record)
		}
		if len(page) < superviseBatch {
			return nil
		}
		after = page[len(page)-1].ID
	}
}

// liveStates are the states the supervisor looks after.
var liveStates = []State{StateQueued, StateRunning, StateNeedsYou, StateBlocked}

func (s *Service) questionsOf(ctx context.Context, id int64) ([]Question, error) {
	records, err := s.opts.Store.ListQuestionsByJob(ctx, FormatID(id))
	if err != nil {
		return nil, err
	}
	out := make([]Question, 0, len(records))
	for _, record := range records {
		question := Question{ID: fmt.Sprintf("q_%d", record.ID), Title: record.Title, Kind: record.Kind, Status: record.Status,
			Urgency: record.Urgency, Wait: record.Wait, AnswerNote: record.AnswerNote, CreatedAt: record.CreatedAt}
		if len(record.AnswerValue) > 0 {
			_ = json.Unmarshal(record.AnswerValue, &question.AnswerValue)
		}
		question.Options = []QuestionOption{}
		if len(record.Options) > 0 {
			_ = json.Unmarshal(record.Options, &question.Options)
		}
		if record.Status != "open" {
			full := questions.Question{Status: questions.Status(record.Status), Delivery: questions.Delivery(record.Delivery),
				DeliveredAt: timePtr(record.DeliveredAt), DeliveryError: record.DeliveryError}
			if receipt, err := s.opts.Store.GetQuestionReceipt(ctx, record.ID); err == nil {
				full.ReceivedAt, full.HeldReason = timePtr(receipt.ReceivedAt), receipt.HeldReason
			}
			question.Receipt = questions.ReceiptOf(full)
		}
		out = append(out, question)
	}
	return out, nil
}

func countOpen(list []Question) int {
	open := 0
	for _, question := range list {
		if question.Status == "open" {
			open++
		}
	}
	return open
}

// ---- Mutating ------------------------------------------------------------------------------

// change is what one mutation produced: events to journal, and whether to tell Jeff. questions
// is the job's question summary, read once per mutation (R2-C12) for the change to use.
type change struct {
	events    []store.JobEventRecord
	notify    string
	questions store.JobQuestionSummary
}

// event appends a timeline entry.
func (c *change) event(kind, summary string, detail map[string]any) {
	encoded := "{}"
	if len(detail) > 0 {
		if body, err := json.Marshal(detail); err == nil {
			encoded = string(body)
		}
	}
	c.events = append(c.events, store.JobEventRecord{Kind: kind, Summary: summary, DetailJSON: encoded})
}

// mutate applies fn to job id under the lock and saves it. fn may return an error to abandon
// the change. The returned job is the saved state.
func (s *Service) mutate(ctx context.Context, id string, fn func(record *store.JobRecord, c *change) error) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutateLocked(ctx, id, fn)
}

// mutateLocked is mutate for a caller that already holds s.mu.
func (s *Service) mutateLocked(ctx context.Context, id string, fn func(record *store.JobRecord, c *change) error) (Job, error) {
	numeric, err := ParseID(id)
	if err != nil {
		return Job{}, ErrNotFound
	}
	record, err := s.opts.Store.GetJob(ctx, numeric)
	if errors.Is(err, store.ErrNotFound) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	before := record
	var c change
	if !State(record.State).Terminal() {
		if c.questions, err = s.opts.Store.SummarizeJobQuestions(ctx, id); err != nil {
			return Job{}, err
		}
	}
	if err := fn(&record, &c); err != nil {
		return Job{}, err
	}
	open := s.reconcileQuestionsLocked(&record, &c)
	applyHoldsLocked(&record) // a held job stays in the state its holds imply (holdset.go)
	if err := CheckTransition(State(before.State), State(record.State)); err != nil && before.State != record.State {
		return Job{}, err
	}
	if before.State != record.State {
		c.event(EventState, fmt.Sprintf("%s → %s", before.State, record.State), map[string]any{"from": before.State, "to": record.State})
		if State(record.State).Terminal() {
			if record.SettledAt.IsZero() {
				record.SettledAt = s.opts.Now()
			}
			// Whatever step was acting on the job loses its claim: it must not start, type or
			// record anything for a finished job (R2-C08).
			clearClaim(&record)
			record.LifecycleGen++
		}
	}
	if err := s.save(ctx, record, c); err != nil {
		return Job{}, err
	}
	if State(record.State).Terminal() && !State(before.State).Terminal() {
		s.revokeTokensLocked(ctx, record) // R2-L1 (tokens.go)
	}
	job := s.toJob(record, open)
	changeName := "updated"
	if before.State != record.State {
		changeName = "state"
	}
	s.publish(job, changeName)
	if c.notify != "" {
		s.opts.Notify(c.notify)
	}
	return job, nil
}

// save writes the record and its new timeline events together (one transaction).
func (s *Service) save(ctx context.Context, record store.JobRecord, c change) error {
	now := s.opts.Now()
	events := make([]store.JobEventRecord, len(c.events))
	for i, event := range c.events {
		event.JobID, event.At = record.ID, now
		events[i] = event
	}
	return s.opts.Store.UpdateJobWithEvents(ctx, record, events)
}

// reconcileQuestionsLocked derives needs_you from the job's questions (c.questions): a live job
// goes to needs_you when a blocking ask (wait=true) is open, or when the agent declared waiting
// and questions are open; it comes back to running when that stops being true. It returns the
// open question count.
func (s *Service) reconcileQuestionsLocked(record *store.JobRecord, c *change) int {
	if State(record.State).Terminal() {
		return 0
	}
	deriveFromQuestions(record, c.questions)
	return c.questions.Open
}

// deriveFromQuestions applies the question-derived part of a job's state to record and reports
// whether anything changed. The tick uses it to find jobs whose questions settled while nothing
// else touched them (R2-C03).
func deriveFromQuestions(record *store.JobRecord, q store.JobQuestionSummary) bool {
	before := *record
	if q.Open == 0 {
		record.Waiting = false
	}
	wantNeedsYou := q.Blocking > 0 || (record.Waiting && q.Open > 0)
	switch {
	case State(record.State) == StateRunning && wantNeedsYou:
		record.State, record.Cause, record.CauseDetail = string(StateNeedsYou), string(CauseQuestions), fmt.Sprintf("%d open question(s)", q.Open)
	case State(record.State) == StateNeedsYou && Cause(record.Cause) == CauseQuestions && !wantNeedsYou:
		record.State, record.Cause, record.CauseDetail = string(StateRunning), "", ""
	}
	return before.Waiting != record.Waiting || before.State != record.State || before.Cause != record.Cause
}

func (s *Service) publish(job Job, changeName string) {
	if s.opts.Hub == nil {
		return
	}
	s.opts.Hub.Publish(hub.Event{Type: "job", Data: HubEvent{ID: job.ID, State: job.State, Stuck: job.Stuck, Cause: job.Cause, Title: job.Title, Change: changeName}})
}

// Journal adds a timeline entry to a job without changing it.
func (s *Service) Journal(ctx context.Context, id, kind, summary string, detail map[string]any) error {
	_, err := s.mutate(ctx, id, func(_ *store.JobRecord, c *change) error {
		c.event(kind, summary, detail)
		return nil
	})
	return err
}

// ---- Conversion ----------------------------------------------------------------------------

func (s *Service) toJob(r store.JobRecord, open int) Job {
	job := Job{
		ID: FormatID(r.ID), Title: r.Title, BriefMD: r.BriefMD, DoneWhen: r.DoneWhen, Role: r.Role, Dir: r.Dir,
		WorktreeBranch: r.WorktreeBranch, WorkDir: r.WorkDir, Engine: r.Engine, Model: r.Model,
		State: State(r.State), Stuck: r.Stuck, Cause: Cause(r.Cause), CauseText: Cause(r.Cause).Describe(), CauseDetail: r.CauseDetail,
		BlockedReason: r.BlockedReason, Waiting: r.Waiting, Progress: r.Progress, LastTool: r.LastTool,
		ResultMD: r.ResultMD, FailReason: r.FailReason, PaneID: r.PaneID, AgentName: r.AgentName, SessionID: r.ClaudeSessionID,
		CreatedAt: r.CreatedAt, StartedAt: timePtr(r.StartedAt), SettledAt: timePtr(r.SettledAt),
		LastActivityAt: timePtr(r.LastActivityAt), Nudges: r.Nudges, Restarts: r.Restarts,
		RetryAt: timePtr(r.RetryAt), RetryCount: r.RetryCount, OpenQuestions: open,
		Verified: decodeList(r.VerifiedJSON), NotVerified: decodeList(r.NotVerifiedJSON),
		Isolate: r.Isolate, isolate: r.Isolate, briefSent: r.BriefSent, Origin: r.Origin, ProjectID: r.ProjectID,
	}
	if r.ParentID > 0 {
		job.ParentID = FormatID(r.ParentID)
	}
	job.BaseBranch, job.WalkthroughMD, job.Reviewer, job.ReviewRound = r.BaseBranch, r.WalkthroughMD, r.Reviewer, r.ReviewRound
	job.ReviewVerdict, job.ReviewMD, job.LandState, job.LandDetail = r.ReviewVerdict, r.ReviewMD, r.LandState, r.LandDetail
	job.LandedAt, job.TornDownAt, job.LandRequest, job.StartAnyway = timePtr(r.LandedAt), timePtr(r.TornDownAt), r.LandRequest, r.StartAnyway
	if r.ReviewOf > 0 {
		job.ReviewOf = FormatID(r.ReviewOf)
	}
	job.AcceptState, job.AcceptDetail, job.CheckRounds, job.NoCommitsReason = r.AcceptState, r.AcceptDetail, r.CheckRounds, r.NoCommitsReason
	if r.FixOf > 0 {
		job.FixOf = FormatID(r.FixOf)
	}
	job.Turns, job.Rechecks, job.WakeKind, job.WakeAt = r.Turns, r.Rechecks, r.WakeKind, timePtr(r.WakeAt)
	if r.WakeJob > 0 {
		job.WakeJob = FormatID(r.WakeJob)
	}
	job.LastMessage, job.LastMessageAt = r.LastMessage, timePtr(r.LastMessageAt)
	job.WaitOwner, job.Wake = waitOf(r, s.paused())
	job.DoingNow = doingNow(r, s.opts.Now())
	return job
}

// doingNow is the progress line, or the last tool call when the agent has not said anything for
// a while (6.5).
func doingNow(r store.JobRecord, now time.Time) string {
	progressAt := r.LastProgressAt
	if r.Progress != "" && (r.LastTool == "" || progressAt.IsZero() || now.Sub(progressAt) < progressStale) {
		return r.Progress
	}
	if r.LastTool != "" {
		return r.LastTool
	}
	return r.Progress
}

func toEvent(r store.JobEventRecord) Event {
	event := Event{ID: r.ID, At: r.At, Kind: r.Kind, Summary: r.Summary}
	if r.DetailJSON != "" && r.DetailJSON != "{}" {
		_ = json.Unmarshal([]byte(r.DetailJSON), &event.Detail)
	}
	return event
}

func decodeList(encoded string) []string {
	out := []string{}
	_ = json.Unmarshal([]byte(encoded), &out)
	return out
}

func encodeList(items []string) string {
	clean := []string{}
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			clean = append(clean, item)
		}
	}
	body, _ := json.Marshal(clean)
	return string(body)
}

func timePtr(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	utc := at.UTC()
	return &utc
}

// oneLine collapses whitespace and caps a string at limit runes.
func oneLine(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return text
}

// SpendSources lists the jobs the spend meter reads: the board's newest jobs whose own Claude
// agent has a session. Orchestrator-held jobs are left out (their use is the orchestrator's
// line), and so are Codex jobs (not measured). A finished job is still listed; the meter marks
// it final after one last read.
func (s *Service) SpendSources(ctx context.Context) ([]spend.Source, error) {
	records, err := s.opts.Store.ListJobs(ctx, nil, listLimit)
	if err != nil {
		return nil, err
	}
	var out []spend.Source
	for _, record := range records {
		job := s.toJob(record, 0)
		if job.Engine == EngineCodex || IsStanding(job.AgentName) || job.SessionID == "" {
			continue
		}
		source := spend.Source{Subject: job.ID, Sessions: []spend.Session{{ID: job.SessionID, Cwd: firstNonEmptyDir(job.WorkDir, job.Dir)}}}
		if job.State.Terminal() {
			source.Settled = job.SettledAt
			if source.Settled == nil {
				settled := job.CreatedAt
				source.Settled = &settled
			}
		}
		out = append(out, source)
	}
	return out, nil
}

func firstNonEmptyDir(dirs ...string) string {
	for _, dir := range dirs {
		if dir != "" {
			return dir
		}
	}
	return ""
}

// SetDefaults changes, at once, the model of a Claude job agent whose job names none and the
// engine of a job with a directory that names none (the app's Settings). engine "" or "auto"
// means claude.
func (s *Service) SetDefaults(engine, model string) { s.liveDefaults.Store(&[2]string{engine, model}) }

// SetLanding applies a change of the review gate's default or Land's merge mode made in Settings.
func (s *Service) SetLanding(reviewDefault, mergeMode string) {
	next := s.landing()
	next.ReviewDefault, next.MergeMode = reviewDefault, mergeMode
	s.liveLanding.Store(&next)
}

// landing is the review and Land settings in force.
func (s *Service) landing() LandingOptions {
	if live := s.liveLanding.Load(); live != nil {
		return *live
	}
	return s.opts.Landing
}

func (s *Service) defaultModel() string {
	if live := s.liveDefaults.Load(); live != nil {
		return live[1]
	}
	return s.opts.DefaultModel
}

// defaultEngine is the engine of a job with a directory that names none.
func (s *Service) defaultEngine() string {
	engine := s.opts.DefaultEngine
	if live := s.liveDefaults.Load(); live != nil {
		engine = live[0]
	}
	if engine != EngineCodex {
		return EngineClaude
	}
	return engine
}
