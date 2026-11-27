// The Now view's rows (DESIGN.md 22.2): one per live project, one for unfiled work and one for
// the orchestrator, each saying who owns the work, what it is doing in a line, how many jobs
// are running, waiting on the human and done today, what it spent today, and when it last did
// anything. Built on the server so the phone does not have to join five endpoints, and so the
// "waiting on you" figure is the attention queue's own count, never a second definition.

package attention

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/projects"
)

// Who owns what, in one sentence each (R4 Fable 1.7, Codex C3). The lead and orchestrator ones
// are internal/orch's, which also writes the division into the agents' own instructions.
const (
	OwnerLead         = orch.OwnershipLead
	OwnerNoLead       = "No lead is running: the orchestrator hears about this project's jobs until one is started."
	OwnerUnfiled      = "Jobs in no project belong to the orchestrator, which hears about each one."
	OwnerOrchestrator = orch.OwnershipOrchestrator
)

// RowCounts are a row's figures.
type RowCounts struct {
	Running     int `json:"running"`    // live and moving (running, blocked, in review, queued; not stuck)
	Waiting     int `json:"waiting"`    // attention items for this row: the queue's own count
	DoneToday   int `json:"done_today"` // reached done since local midnight
	FailedToday int `json:"failed_today"`
}

// LeadInfo is a standing agent's state on a row.
type LeadInfo struct {
	Name   string `json:"name"`
	State  string `json:"state"` // the lifecycle's state; "off" when not running
	Detail string `json:"detail,omitempty"`
	PaneID string `json:"pane_id,omitempty"`
}

// Row is one line of the Now view.
type Row struct {
	Kind    string      `json:"kind"` // project, unfiled, orchestrator
	Project *ProjectRef `json:"project,omitempty"`
	Lead    *LeadInfo   `json:"lead,omitempty"`
	Owner   string      `json:"owner"`
	Doing   string      `json:"doing"`
	Counts  RowCounts   `json:"counts"`
	// SpendTodayUSD is the estimated spend today (decision 66); nil when nothing was priced.
	SpendTodayUSD  *float64   `json:"spend_today_usd"`
	LastActivityAt *time.Time `json:"last_activity_at,omitempty"`
}

// OverviewSources is what the rows are built from.
type OverviewSources struct {
	Projects []projects.Project
	Jobs     []jobs.Job // the board's newest jobs, every state
	Standing []StandingAgent
	// Spend maps a spend subject (j_<n>, lead:<slug>, orchestrator) to today's estimated cost.
	Spend map[string]float64
	// Waiting is the attention queue's count per project id ("0" for unfiled work).
	Waiting map[string]int
	// Orchestrator is the standing agent named "orchestrator", if there is one.
	Now      time.Time
	Location *time.Location
}

// Rows builds the Now view's rows (pure): projects in name order, then unfiled, then the
// orchestrator (when there is one).
func Rows(src OverviewSources) []Row {
	if src.Location == nil {
		src.Location = time.Local
	}
	local := src.Now.In(src.Location)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, src.Location)
	standing := map[string]StandingAgent{}
	for _, agent := range src.Standing {
		standing[agent.Name] = agent
	}
	byProject := map[int64][]jobs.Job{}
	for _, job := range src.Jobs {
		byProject[job.ProjectID] = append(byProject[job.ProjectID], job)
	}
	live := make([]projects.Project, 0, len(src.Projects))
	for _, project := range src.Projects {
		if !project.Archived {
			live = append(live, project)
		}
	}
	sort.Slice(live, func(i, j int) bool { return live[i].Name < live[j].Name })

	var rows []Row
	for _, project := range live {
		row := Row{Kind: "project", Project: &ProjectRef{ID: project.ID, Slug: project.Slug, Name: project.Name}}
		lead, running := standing[projects.AgentName(project.Slug)]
		row.Lead = &LeadInfo{Name: projects.AgentName(project.Slug), State: string(projects.StateOff)}
		row.Owner = OwnerNoLead
		if running {
			row.Lead = &LeadInfo{Name: lead.Name, State: lead.State, Detail: lead.Detail, PaneID: lead.PaneID}
			if leadUp(lead.State) {
				row.Owner = OwnerLead
			}
		}
		fillRow(&row, byProject[project.ID], src, midnight, fmt.Sprint(project.ID), projects.Subject(project.Slug))
		rows = append(rows, row)
	}
	unfiled := Row{Kind: "unfiled", Owner: OwnerUnfiled}
	fillRow(&unfiled, byProject[0], src, midnight, "0", "")
	rows = append(rows, unfiled)
	if orchestrator, ok := standing["orchestrator"]; ok {
		row := Row{Kind: "orchestrator", Owner: OwnerOrchestrator,
			Lead: &LeadInfo{Name: orchestrator.Name, State: orchestrator.State, Detail: orchestrator.Detail, PaneID: orchestrator.PaneID}}
		row.Doing = standingDoing(orchestrator, src.Jobs)
		if cost, ok := src.Spend["orchestrator"]; ok {
			row.SpendTodayUSD = &cost
		}
		rows = append(rows, row)
	}
	return rows
}

// leadUp says whether a lead can take its project's events (DESIGN.md 7.4: lead-first routing).
func leadUp(state string) bool {
	switch state {
	case "off", "stopped", "failed", "needs_terminal", "disabled":
		return false
	}
	return true
}

// fillRow counts a row's jobs, sums its spend and picks its one line of "doing".
func fillRow(row *Row, list []jobs.Job, src OverviewSources, midnight time.Time, waitingKey, leadSubject string) {
	row.Counts.Waiting = src.Waiting[waitingKey]
	var total float64
	priced := false
	if cost, ok := src.Spend[leadSubject]; ok && leadSubject != "" {
		total, priced = total+cost, true
	}
	var moving []jobs.Job
	for _, job := range list {
		if cost, ok := src.Spend[job.ID]; ok {
			total, priced = total+cost, true
		}
		touch(row, job)
		switch {
		case job.State == jobs.StateDone && job.SettledAt != nil && !job.SettledAt.Before(midnight):
			row.Counts.DoneToday++
		case job.State == jobs.StateFailed && job.SettledAt != nil && !job.SettledAt.Before(midnight):
			row.Counts.FailedToday++
		case !job.State.Terminal() && !job.Stuck && job.State != jobs.StateNeedsYou:
			row.Counts.Running++
			moving = append(moving, job)
		}
	}
	if priced {
		row.SpendTodayUSD = &total
	}
	row.Doing = doingLine(row, moving)
}

// touch moves a row's last activity forward to a job's.
func touch(row *Row, job jobs.Job) {
	at := job.LastActivityAt
	if job.SettledAt != nil && (at == nil || job.SettledAt.After(*at)) {
		at = job.SettledAt
	}
	if at != nil && (row.LastActivityAt == nil || at.After(*row.LastActivityAt)) {
		copied := *at
		row.LastActivityAt = &copied
	}
}

// doingLine is the row's one line: what the most recently active moving job is doing, with how
// many others run beside it; else that it is quiet.
func doingLine(row *Row, moving []jobs.Job) string {
	if len(moving) == 0 {
		if row.Counts.Waiting > 0 {
			return "Nothing moving: waiting on you"
		}
		return "Quiet"
	}
	sort.SliceStable(moving, func(i, j int) bool { return later(moving[i].LastActivityAt, moving[j].LastActivityAt) })
	top := moving[0]
	line := top.DoingNow
	if line == "" {
		line = top.Progress
	}
	if line == "" {
		line = string(top.State)
	}
	line = top.ID + " " + top.Title + ": " + line
	if extra := len(moving) - 1; extra > 0 {
		line += fmt.Sprintf(" (+%d more)", extra)
	}
	return line
}

func later(a, b *time.Time) bool {
	switch {
	case a == nil:
		return false
	case b == nil:
		return true
	}
	return a.After(*b)
}

// standingDoing is what a standing agent is doing: the job it holds, else its state.
func standingDoing(agent StandingAgent, list []jobs.Job) string {
	for _, job := range list {
		if job.PaneID != "" && job.PaneID == agent.PaneID && !job.State.Terminal() {
			line := job.DoingNow
			if line == "" {
				line = job.Progress
			}
			if line == "" {
				line = string(job.State)
			}
			return job.ID + " " + job.Title + ": " + line
		}
	}
	switch agent.State {
	case "working":
		return "Working"
	case "idle":
		return "Idle, ready for the next thing"
	}
	return agent.State
}

// ---- The service side ---------------------------------------------------------------------

// OverviewOptions wire Overview. Each may be nil.
type OverviewOptions struct {
	Projects func(ctx context.Context) ([]projects.Project, error)
	Jobs     func(ctx context.Context) ([]jobs.Job, error)
	Standing func(ctx context.Context) []StandingAgent
	Spend    func(ctx context.Context) (map[string]float64, error)
	Location *time.Location
}

// Overview builds the rows with the attention service's counts.
func (s *Service) Overview(ctx context.Context, opts OverviewOptions) ([]Row, error) {
	src := OverviewSources{Now: s.opts.Now(), Location: opts.Location, Waiting: s.Snapshot(ctx).Counts.ByProject}
	var err error
	if opts.Projects != nil {
		if src.Projects, err = opts.Projects(ctx); err != nil {
			return nil, fmt.Errorf("listing projects: %w", err)
		}
	}
	if opts.Jobs != nil {
		if src.Jobs, err = opts.Jobs(ctx); err != nil {
			return nil, fmt.Errorf("listing jobs: %w", err)
		}
	}
	if opts.Standing != nil {
		src.Standing = opts.Standing(ctx)
	}
	if opts.Spend != nil {
		// Spend is information: a failed read leaves the figures out rather than the rows.
		src.Spend, _ = opts.Spend(ctx)
	}
	return Rows(src), nil
}
