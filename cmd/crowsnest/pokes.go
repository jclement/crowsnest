// Wiring for pokes and the backstop (M8, DESIGN.md 7.4 and 6.3): the poker that tells the
// orchestrator what changed, fed by the hub's `job` and `question` events and by the standing
// list of open work at each timer tick; the herdr-side backstop that nudges job agents without
// hooks (it runs in the poker's loop, through the jobs service). Under --demo pokes go out only
// for "Poke now" and the backstop is off (the demo's jobs are scripted).
//
// Routing is lead-first (DESIGN.md 7.4, 21.2): a project job is its lead's business, so its
// events go to the lead's poker only; the orchestrator hears Unfiled work, the outcome of jobs it
// handed to a project, and escalations (no lead able to take it, a job stuck or waiting on the
// human past orch.EscalateAfter, a lead that failed).

package main

import (
	"context"
	"fmt"
	"github.com/jclement/crowsnest/internal/spend"
	"log/slog"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/termtext"
	"github.com/jclement/crowsnest/internal/voice"
)

// pokeServices are what the poker reads.
type pokeServices struct {
	orchestrator *orch.Orchestrator
	jobs         *jobs.Service
	questions    *questions.Service
	talk         *voice.Service
	journal      *journal.Journal
	alert        func(text string)
	tasks        orch.TaskSource // scheduled tasks (7.8)
	tender       orch.Upkeep     // compaction and the daily new conversation (7.9)
	// leads are the projects' leads; each has a poker of its own for its project's jobs and
	// the questions those jobs asked (DESIGN.md 21.2). Nil: none.
	leads func() *projects.Leads
	// db reads projects and jobs by pane for the routing; startPoker fills it.
	db *store.Store
	// leadWatch remembers each lead's last state, so a lead that fails is escalated once.
	leadWatch *leadWatch
	// ctl are the pause and presence (landing_wiring.go); nil: neither.
	ctl *controls
}

// startPoker builds the poker, feeds it from the hub, and runs it until ctx ends.
func startPoker(ctx context.Context, cfg config.Config, db *store.Store, events *hub.Hub, services pokeServices, client herdr.Client, logger *slog.Logger) *orch.Poker {
	services.db, services.leadWatch = db, newLeadWatch()
	options := orch.PokerOptions{
		Client: client, KV: db, Journal: services.journal, Hub: events, Logger: logger,
		Status:   services.orchestrator.Status,
		Interval: cfg.PokeInterval, Standing: standingWork(services), Alert: services.alert,
		ManualOnly: cfg.Demo, Tasks: services.tasks,
		OwnedElsewhere: services.ownedElsewhere(ctx), Escalations: services.escalations,
	}
	if services.ctl != nil {
		options.Paused, options.Held, options.Presence = services.ctl.paused, services.ctl.held, services.ctl.presenceWord
	}
	if services.jobs != nil {
		options.Hold = standingHold(services.jobs, spend.OrchestratorSubject)
	}
	if services.tender != nil && !cfg.NoOrchestrator {
		options.Tender = services.tender
	}
	if !cfg.Demo && services.jobs != nil {
		options.Backstop = services.jobs
	}
	poker := orch.NewPoker(options)
	go feedPoker(ctx, events, poker, services, logger)
	go poker.Run(ctx)
	logger.Info("pokes on", "interval", cfg.PokeInterval, "backstop", options.Backstop != nil, "manual_only", cfg.Demo)
	return poker
}

// feedPoker turns hub events into poke reasons: a job that finished or got stuck, a question that
// was settled. A subscription the hub closes (it fell behind) is renewed, and what settled while
// it was falling behind is read back from the store (R2-C13): the poker skips anything already
// delivered or pending, so the replay costs nothing when nothing was lost.
func feedPoker(ctx context.Context, events *hub.Hub, poker *orch.Poker, services pokeServices, logger *slog.Logger) {
	for ctx.Err() == nil {
		since := time.Now()
		sub := events.Subscribe(0)
		for event := range sub.Events() {
			handlePokeEvent(ctx, event, poker, services, logger)
		}
		sub.Close()
		if ctx.Err() == nil {
			replaySettled(ctx, since, poker, services, logger)
		}
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond): // a closed hub must not spin this loop
		}
	}
}

// handlePokeEvent routes one hub event: a job that settled or got stuck, a question that was
// settled. Who hears it is lead-first (routeJob).
func handlePokeEvent(ctx context.Context, event hub.Event, poker *orch.Poker, services pokeServices, logger *slog.Logger) {
	router := services.router(poker)
	switch data := event.Data.(type) {
	case jobs.HubEvent:
		if !data.State.Terminal() && !data.Stuck {
			return
		}
		detail, err := services.jobs.Get(ctx, data.ID)
		if err != nil {
			logger.Debug("poker could not read a job", "job", data.ID, "err", err)
			return
		}
		info := orch.JobInfo{
			ID: data.ID, Title: data.Title, State: string(data.State), Stuck: data.Stuck,
			Cause: detail.CauseText, PaneID: detail.PaneID,
		}
		router.job(ctx, info, detail.ProjectID, detail.Origin)
	case questions.Event:
		if data.Change != "answered" && data.Change != "expired" && data.Change != "cancelled" {
			return
		}
		question, err := services.questions.Get(ctx, data.ID)
		if err != nil {
			return
		}
		router.question(ctx, questionInfo(question), services.questionProject(ctx, question.JobID, question.SourcePane))
	}
}

func questionInfo(question questions.Question) orch.QuestionInfo {
	return orch.QuestionInfo{ID: question.ID, Title: question.Title, Status: string(question.Status), Kind: string(question.Kind), SourcePane: question.SourcePane}
}

// ---- Lead-first routing (DESIGN.md 7.4, 21.2) ---------------------------------------------

// leadView is what routing needs to know of a project's lead.
type leadView struct {
	Project string      // the project's name ("" for Unfiled)
	Up      bool        // running and able to take pokes (orch.LeadCanTakePokes)
	Poker   *orch.Poker // the lead's poker; nil when it is not running
}

// jobRoute is who hears about a job or question event.
type jobRoute struct {
	Lead, Orchestrator bool
	Prefix             string // starts the orchestrator's reason
	HandedOver         bool   // the orchestrator's reason is the outcome of work it handed over
}

// noLeadPrefix starts the orchestrator's reason for a project job whose lead cannot take it.
func noLeadPrefix(project string) string {
	return "project " + termtext.Quote(project) + " has no lead running: "
}

// routeJob decides who hears a job event (pure). Unfiled work (project 0) is the
// orchestrator's. A project job is its lead's alone while the lead can take pokes; the
// orchestrator also hears when a job it created settles (the outcome of work it routed). With
// no lead able to take it, the orchestrator hears it, prefixed so it knows why.
func routeJob(projectID int64, origin string, settled bool, lead leadView) jobRoute {
	switch {
	case projectID == 0:
		return jobRoute{Orchestrator: true}
	case !lead.Up:
		return jobRoute{Orchestrator: true, Prefix: noLeadPrefix(lead.Project)}
	case origin == jobs.OriginOrchestrator && settled:
		return jobRoute{Lead: true, Orchestrator: true, HandedOver: true, Prefix: "project " + termtext.Quote(lead.Project) + ": "}
	}
	return jobRoute{Lead: true}
}

// pokeRouter delivers routed events to the orchestrator's poker and the leads' pokers.
type pokeRouter struct {
	orchestrator *orch.Poker
	lead         func(ctx context.Context, projectID int64) leadView
}

// job routes a job that settled or got stuck.
func (r pokeRouter) job(ctx context.Context, info orch.JobInfo, projectID int64, origin string) {
	lead := r.lead(ctx, projectID)
	settled := jobs.State(info.State).Terminal()
	route := routeJob(projectID, origin, settled, lead)
	if route.Lead && lead.Poker != nil {
		lead.Poker.JobChanged(ctx, info)
	}
	if route.Orchestrator && r.orchestrator != nil {
		info.Prefix, info.HandedOver = route.Prefix, route.HandedOver
		r.orchestrator.JobChanged(ctx, info)
	}
}

// question routes a settled question; projectID is its job's project, or the asking lead's.
func (r pokeRouter) question(ctx context.Context, info orch.QuestionInfo, projectID int64) {
	lead := r.lead(ctx, projectID)
	route := routeJob(projectID, "", false, lead)
	if route.Lead && lead.Poker != nil {
		lead.Poker.QuestionSettled(ctx, info)
	}
	if route.Orchestrator && r.orchestrator != nil {
		info.Prefix = route.Prefix
		r.orchestrator.QuestionSettled(ctx, info)
	}
}

// router is the routing over the live services.
func (s pokeServices) router(orchestrator *orch.Poker) pokeRouter {
	return pokeRouter{orchestrator: orchestrator, lead: s.leadView}
}

// leadsNow are the leads, or nil before serve built them.
func (s pokeServices) leadsNow() *projects.Leads {
	if s.leads == nil {
		return nil
	}
	return s.leads()
}

// leadView looks a project's lead up; the zero view for Unfiled.
func (s pokeServices) leadView(ctx context.Context, projectID int64) leadView {
	if projectID == 0 {
		return leadView{}
	}
	var view leadView
	if leads := s.leadsNow(); leads != nil {
		if lead := leads.Get(projectID); lead != nil {
			view = leadView{Project: lead.Project.Name, Up: orch.LeadCanTakePokes(lead.Agent.Status().State), Poker: lead.Poker}
		}
	}
	if view.Project == "" && s.db != nil {
		if project, err := s.db.GetProject(ctx, projectID); err == nil {
			view.Project = project.Name
		}
	}
	if view.Project == "" {
		view.Project = fmt.Sprintf("#%d", projectID)
	}
	return view
}

// questionProject is the project a question belongs to: its job's, else the asking lead's.
func (s pokeServices) questionProject(ctx context.Context, jobID, sourcePane string) int64 {
	if jobID != "" && s.jobs != nil {
		if job, err := s.jobs.Get(ctx, jobID); err == nil {
			return job.ProjectID
		}
	}
	if leads := s.leadsNow(); leads != nil {
		if lead := leads.ByPane(sourcePane); lead != nil {
			return lead.Project.ID
		}
	}
	return 0
}

// liveStates are the states of a job someone is still working on.
var liveStates = []string{string(jobs.StateQueued), string(jobs.StateRunning), string(jobs.StateNeedsYou), string(jobs.StateBlocked)}

// ownedElsewhere reports a pane whose settling is not the orchestrator's news: a lead's own
// pane, or a job agent holding a job of a project whose lead can take pokes.
func (s pokeServices) ownedElsewhere(ctx context.Context) func(paneID string) bool {
	return func(paneID string) bool {
		leads := s.leadsNow()
		if leads == nil || paneID == "" {
			return false
		}
		if leads.ByPane(paneID) != nil {
			return true
		}
		if s.db == nil {
			return false
		}
		records, err := s.db.ListJobsByPane(ctx, paneID, liveStates)
		if err != nil {
			return false // unknown: the orchestrator hears it, as before
		}
		for _, record := range records {
			if record.ProjectID != 0 && s.leadView(ctx, record.ProjectID).Up {
				return true
			}
		}
		return false
	}
}

// replaySettled feeds the routing every job and question that settled (or job that is stuck)
// since a dropped subscription began.
func replaySettled(ctx context.Context, since time.Time, poker *orch.Poker, services pokeServices, logger *slog.Logger) {
	router := services.router(poker)
	if services.jobs != nil {
		list, err := services.jobs.List(ctx, nil)
		if err != nil {
			logger.Warn("poker: re-reading jobs after a dropped subscription", "err", err)
		}
		for _, job := range list {
			settled := job.SettledAt != nil && !job.SettledAt.Before(since)
			if settled || (job.Stuck && !job.State.Terminal()) {
				info := orch.JobInfo{ID: job.ID, Title: job.Title, State: string(job.State), Stuck: job.Stuck, Cause: job.CauseText, PaneID: job.PaneID}
				router.job(ctx, info, job.ProjectID, job.Origin)
			}
		}
	}
	if services.questions != nil {
		list, err := services.questions.List(ctx, "")
		if err != nil {
			logger.Warn("poker: re-reading questions after a dropped subscription", "err", err)
		}
		for _, question := range list {
			if question.SettledAt != nil && !question.SettledAt.Before(since) {
				router.question(ctx, questionInfo(question), services.questionProject(ctx, question.JobID, question.SourcePane))
			}
		}
	}
}

// standingWork lists what is open right now for the orchestrator's timer, one line each: stuck
// jobs, jobs that need the human, blocked jobs (less those a project's lead is looking after),
// and the open question count. The timer pokes only when this list changed.
func standingWork(services pokeServices) func(context.Context) []string {
	return func(ctx context.Context) []string {
		lines := standingJobs(ctx, services.jobs, func(job jobs.Job) bool {
			return job.ProjectID == 0 || !services.leadView(ctx, job.ProjectID).Up
		})
		if services.questions != nil {
			if summary, err := services.questions.Summary(ctx); err == nil && summary.Open > 0 {
				lines = append(lines, fmt.Sprintf("%d open question(s) for the human, %d blocking", summary.Open, summary.Blocking))
			}
		}
		return lines
	}
}

// leadStandingWork is a lead's timer list: its project's stuck, needs-you and blocked jobs, and
// the open questions its project's jobs asked.
func leadStandingWork(jobService *jobs.Service, questionService *questions.Service, projectID int64) func(context.Context) []string {
	return func(ctx context.Context) []string {
		lines := standingJobs(ctx, jobService, func(job jobs.Job) bool { return job.ProjectID == projectID })
		if questionService == nil || jobService == nil {
			return lines
		}
		open, err := questionService.List(ctx, "open")
		if err != nil {
			return lines
		}
		for _, question := range open {
			if question.JobID == "" {
				continue
			}
			if job, err := jobService.Get(ctx, question.JobID); err == nil && job.ProjectID == projectID {
				lines = append(lines, fmt.Sprintf("question %s (job %s) is open for the human: %s", question.ID, job.ID, termtext.Quote(question.Title)))
			}
		}
		return lines
	}
}

// standingJobs lists the live jobs keep accepts that are stuck, need the human or are blocked.
func standingJobs(ctx context.Context, service *jobs.Service, keep func(jobs.Job) bool) []string {
	if service == nil {
		return nil
	}
	list, err := service.List(ctx, []jobs.State{jobs.StateRunning, jobs.StateNeedsYou, jobs.StateBlocked})
	if err != nil {
		return nil
	}
	var lines []string
	for _, job := range list {
		if !keep(job) {
			continue
		}
		switch {
		case job.Stuck:
			lines = append(lines, fmt.Sprintf("job %s is stuck (%s): %s", job.ID, job.CauseText, termtext.Quote(job.Title)))
		case job.State == jobs.StateNeedsYou:
			lines = append(lines, fmt.Sprintf("job %s needs the human: %s", job.ID, termtext.Quote(job.Title)))
		case job.State == jobs.StateBlocked:
			lines = append(lines, fmt.Sprintf("job %s is blocked: %s", job.ID, termtext.Quote(job.Title)))
		}
	}
	return lines
}

// ---- Escalations ------------------------------------------------------------------------------

// escalations are what the leads could not resolve, for the orchestrator's poker: a project job
// stuck or waiting on the human past orch.EscalateAfter while its lead is up, and a lead that
// failed or stopped at a terminal prompt.
func (s pokeServices) escalations(ctx context.Context, now time.Time) []orch.Reason {
	reasons := orch.Escalations(now, s.projectJobs(ctx))
	if leads := s.leadsNow(); leads != nil && s.leadWatch != nil {
		reasons = append(reasons, s.leadWatch.observe(now, leads.Running())...)
	}
	return reasons
}

// projectJobs are the live project jobs whose lead is up, with when each last changed (its last
// activity, else its start, else its creation: the simplest honest "since" a job records).
func (s pokeServices) projectJobs(ctx context.Context) []orch.ProjectJob {
	if s.jobs == nil {
		return nil
	}
	list, err := s.jobs.List(ctx, []jobs.State{jobs.StateRunning, jobs.StateNeedsYou, jobs.StateBlocked})
	if err != nil {
		return nil
	}
	var out []orch.ProjectJob
	for _, job := range list {
		if job.ProjectID == 0 || (!job.Stuck && job.State != jobs.StateNeedsYou) {
			continue
		}
		lead := s.leadView(ctx, job.ProjectID)
		if !lead.Up {
			continue // the orchestrator already heard it with the "no lead running" prefix
		}
		out = append(out, orch.ProjectJob{
			ID: job.ID, Title: job.Title, Project: lead.Project, NeedsYou: job.State == jobs.StateNeedsYou,
			Stuck: job.Stuck, Cause: job.CauseText, Since: jobSince(job),
		})
	}
	return out
}

func jobSince(job jobs.Job) time.Time {
	switch {
	case job.LastActivityAt != nil:
		return *job.LastActivityAt
	case job.StartedAt != nil:
		return *job.StartedAt
	}
	return job.CreatedAt
}

// leadWatch turns a lead's move into failed or needs_terminal into one escalation.
type leadWatch struct {
	mu   sync.Mutex
	seen map[int64]leadSeen
}

type leadSeen struct {
	state orch.State
	since time.Time
}

func newLeadWatch() *leadWatch { return &leadWatch{seen: map[int64]leadSeen{}} }

// observe records each running lead's state and returns a reason for every lead now failed or
// at a terminal prompt (keyed by when it got there, so the poker sends it once).
func (w *leadWatch) observe(now time.Time, running []*projects.Lead) []orch.Reason {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []orch.Reason
	for _, lead := range running {
		state := lead.Agent.Status().State
		seen, ok := w.seen[lead.Project.ID]
		if !ok || seen.state != state {
			seen = leadSeen{state: state, since: now}
			w.seen[lead.Project.ID] = seen
		}
		if state == orch.StateFailed || state == orch.StateNeedsTerminal {
			out = append(out, orch.LeadDownReason(lead.Project.Name, state, seen.since))
		}
	}
	return out
}
