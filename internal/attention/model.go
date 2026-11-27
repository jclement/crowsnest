// Package attention is crowsnest's one answer to "what needs me?" (DESIGN.md 22). Every surface
// that counts or lists things waiting on the human (the nav badge, the tab title, the needs-you
// tray, the command palette, the Inbox and the Now view) renders from the snapshot this package
// builds, so they can never disagree about the number.
//
// This file is the pure half: Build turns what the other services hold (pending approvals, open
// questions, agents at a terminal dialog, live and recently failed jobs, the standing agents'
// states, the projects) into one de-duplicated, ranked list of Items. It reads nothing and
// takes the time as an argument, so every de-duplication rule is a row in model_test.go.
// service.go gathers the sources, applies snoozes and publishes the `attention` event.
package attention

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/risk"
)

// Kind is what an item is.
type Kind string

// Kinds.
const (
	KindApproval Kind = "approval" // a tool call waiting for allow or deny
	KindQuestion Kind = "question" // a question an agent asked
	KindDialog   Kind = "dialog"   // an agent stopped at a terminal dialog
	KindJob      Kind = "job"      // a job stuck, failed, needing a fix, or waiting to start
	KindAgent    Kind = "agent"    // the orchestrator or a lead needs the terminal or a restart
)

// Urgency splits the queue in two (R4 Fable 1.1): blocking means work has stopped until the human
// acts; can-wait means the work carries on meanwhile (a non-blocking question, a job that is
// retrying by itself, a failure to look at).
type Urgency string

// Urgencies.
const (
	Blocking Urgency = "blocking"
	CanWait  Urgency = "can_wait"
)

// Action is what resolves an item, so every surface offers the same verb.
type Action struct {
	Kind  string `json:"kind"`  // decide, answer, dialog, terminal, nudge, retry, restart, open
	Label string `json:"label"` // the button's words ("Allow or deny", "Answer", "Open terminal")
}

// ProjectRef names the project an item belongs to; nil is unfiled work.
type ProjectRef struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Item is one thing waiting on the human, with everything a surface needs to show and answer it
// without another request: the approval, question, agent or job itself rides along in the
// shape its own endpoint returns.
type Item struct {
	ID      string      `json:"id"` // approval:ap_3, question:q_7, dialog:<pane>, job:j_4, agent:<name>
	Kind    Kind        `json:"kind"`
	Urgency Urgency     `json:"urgency"`
	Rank    int         `json:"rank"`  // higher first within an urgency
	Since   time.Time   `json:"since"` // how long the human has been the bottleneck
	Title   string      `json:"title"`
	Detail  string      `json:"detail,omitempty"`
	Project *ProjectRef `json:"project"`
	// JobID and JobTitle name the job the item belongs to ("" when none: an agent crowsnest did
	// not start, or a standing agent's own conversation).
	JobID    string `json:"job_id,omitempty"`
	JobTitle string `json:"job_title,omitempty"`
	Agent    string `json:"agent,omitempty"`
	PaneID   string `json:"pane_id,omitempty"`
	Action   Action `json:"action"`
	// SnoozedUntil is set (and the item left out of the count) while the human has snoozed it.
	SnoozedUntil *time.Time `json:"snoozed_until,omitempty"`

	Approval  *approvals.Approval `json:"approval,omitempty"`
	Question  *questions.Question `json:"question,omitempty"`
	DialogFor *fleet.AgentEntry   `json:"dialog_agent,omitempty"`
	Job       *jobs.Job           `json:"job,omitempty"`
	Standing  *StandingAgent      `json:"standing,omitempty"`
}

// StandingAgent is the orchestrator or a project's lead as the queue needs it.
type StandingAgent struct {
	Name      string `json:"name"` // orchestrator, lead-<slug>
	Label     string `json:"label"`
	ProjectID int64  `json:"project_id"`
	PaneID    string `json:"pane_id"`
	State     string `json:"state"`
	Detail    string `json:"detail,omitempty"`
}

// Sources is everything Build reads.
type Sources struct {
	Approvals []approvals.Approval // pending ones count; others are ignored
	Questions []questions.Question // open ones count; notices never do
	Agents    []fleet.AgentEntry   // the fleet's flat agent list
	Jobs      []jobs.Job           // live jobs plus recently failed ones
	Standing  []StandingAgent
	Projects  []projects.Project
	// FirstSeen says when an item without a timestamp of its own (a dialog, a standing agent's
	// state) was first seen; the service remembers it. Missing: now.
	FirstSeen map[string]time.Time
	// Dismissed are failed-job items the human has looked at; they leave the queue.
	Dismissed map[string]bool
}

// Ranks, higher first within an urgency band. Named so a surface's legend and the tests agree.
const (
	rankDangerous    = 60
	rankElevated     = 50
	rankBlockingQ    = 50
	rankStanding     = 48
	rankLow          = 40
	rankDialog       = 40
	rankJobBlocking  = 35
	rankQuestion     = 30
	rankLowQuestion  = 20
	rankJobRecover   = 15
	rankJobFailed    = 10
	rankJobHeldStart = 12
)

// FailedWindow is how long a failed job stays in the queue (unless dismissed): long enough to be
// seen after a night away, short enough that the queue does not fill with history.
const FailedWindow = 72 * time.Hour

// builder carries the indexes Build needs while it assembles the list.
type builder struct {
	now       time.Time
	src       Sources
	projects  map[int64]projects.Project
	jobByID   map[string]jobs.Job
	jobByPane map[string]jobs.Job
	// covered marks jobs some approval, question or dialog item already stands for: the job is
	// then not listed again by itself (a job stuck on a dialog that also has an approval is one
	// item).
	covered map[string]bool
	items   []Item
}

// Build turns the sources into the ranked, de-duplicated queue (pure).
func Build(src Sources, now time.Time) []Item {
	b := &builder{now: now, src: src, projects: map[int64]projects.Project{}, jobByID: map[string]jobs.Job{},
		jobByPane: map[string]jobs.Job{}, covered: map[string]bool{}}
	for _, project := range src.Projects {
		b.projects[project.ID] = project
	}
	for _, job := range src.Jobs {
		b.jobByID[job.ID] = job
		if job.PaneID != "" && !job.State.Terminal() {
			b.jobByPane[job.PaneID] = job
		}
	}
	approvalPanes := b.addApprovals()
	b.addQuestions()
	dialogPanes := b.addDialogs(approvalPanes)
	b.addStanding(dialogPanes, approvalPanes)
	b.addJobs()
	sortItems(b.items)
	return b.items
}

// sortItems orders blocking before can-wait, then by rank, then the longest wait first, so the
// item that has held work up longest is on top of its band (R4 Fable 1.9).
func sortItems(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		a, c := items[i], items[j]
		if a.Urgency != c.Urgency {
			return a.Urgency == Blocking
		}
		if a.Rank != c.Rank {
			return a.Rank > c.Rank
		}
		if !a.Since.Equal(c.Since) {
			return a.Since.Before(c.Since)
		}
		return a.ID < c.ID
	})
}

// ---- Each source -------------------------------------------------------------------------

func (b *builder) addApprovals() map[string]bool {
	panes := map[string]bool{}
	for i := range b.src.Approvals {
		approval := b.src.Approvals[i]
		if approval.Status != approvals.StatusPending {
			continue
		}
		if approval.PaneID != "" {
			panes[approval.PaneID] = true
		}
		rank := rankLow
		switch approval.Risk {
		case risk.Dangerous:
			rank = rankDangerous
		case risk.Elevated:
			rank = rankElevated
		}
		item := Item{ID: "approval:" + approval.ID, Kind: KindApproval, Urgency: Blocking, Rank: rank, Since: approval.CreatedAt,
			Title: approval.Summary, Detail: riskWords(approval), Agent: approval.AgentName, PaneID: approval.PaneID,
			Action: Action{Kind: "decide", Label: "Allow or deny"}, Approval: &approval}
		b.attachJob(&item, approval.JobID, approval.PaneID)
		b.items = append(b.items, item)
	}
	return panes
}

func riskWords(approval approvals.Approval) string {
	words := string(approval.Risk) + " risk"
	if len(approval.RiskReasons) > 0 {
		words += ": " + strings.Join(approval.RiskReasons, "; ")
	}
	return words
}

func (b *builder) addQuestions() {
	for i := range b.src.Questions {
		question := b.src.Questions[i]
		if question.Status != questions.StatusOpen || question.Kind == questions.KindNotice {
			continue
		}
		item := Item{ID: "question:" + question.ID, Kind: KindQuestion, Since: question.CreatedAt, Title: question.Title,
			PaneID: question.SourcePane, Action: Action{Kind: "answer", Label: "Answer"}, Question: &question}
		job := b.attachJob(&item, question.JobID, question.SourcePane)
		item.Urgency, item.Rank = questionUrgency(question, job)
		if question.Recommend != "" {
			item.Detail = "recommended: " + optionLabel(question, question.Recommend)
		}
		b.items = append(b.items, item)
	}
}

// questionUrgency: a question blocks when its asker said so (urgency blocking, or it waits on the
// answer), or when its job declared that nothing is left the questions do not block.
func questionUrgency(question questions.Question, job *jobs.Job) (Urgency, int) {
	stopped := job != nil && job.State == jobs.StateNeedsYou && job.Cause == jobs.CauseQuestions
	switch {
	case question.Urgency == questions.UrgencyBlocking:
		return Blocking, rankBlockingQ
	case question.Wait || stopped:
		return Blocking, rankQuestion
	case question.Urgency == questions.UrgencyLow:
		return CanWait, rankLowQuestion
	}
	return CanWait, rankQuestion
}

func optionLabel(question questions.Question, key string) string {
	for _, option := range question.Options {
		if option.Key == key {
			return option.Label
		}
	}
	return key
}

// addDialogs lists agents at a terminal dialog, except one whose pane has a pending approval:
// the approval and the dialog are the same request, drawn twice.
func (b *builder) addDialogs(approvalPanes map[string]bool) map[string]bool {
	panes := map[string]bool{}
	for i := range b.src.Agents {
		agent := b.src.Agents[i]
		if !agent.NeedsYou || agent.PaneID == "" || approvalPanes[agent.PaneID] {
			continue
		}
		panes[agent.PaneID] = true
		id := "dialog:" + agent.PaneID
		name := agent.Name
		if name == "" {
			name = agent.PaneID
		}
		item := Item{ID: id, Kind: KindDialog, Urgency: Blocking, Rank: rankDialog, Since: b.firstSeen(id),
			Title: name + " is waiting at a dialog", Agent: agent.Name, PaneID: agent.PaneID,
			Action: Action{Kind: "dialog", Label: "Answer the dialog"}, DialogFor: &agent}
		job := b.attachJob(&item, "", agent.PaneID)
		if job != nil {
			item.Title = "Dialog: " + job.Title
			item.Detail = job.CauseDetail
		} else if standing := b.standingByPane(agent.PaneID); standing != nil {
			item.Title = standing.Label + " is waiting at a dialog"
			item.Project = b.projectRef(standing.ProjectID)
		}
		b.items = append(b.items, item)
	}
	return panes
}

// addStanding lists a standing agent that needs the terminal (a start-up prompt) or has failed,
// unless its dialog is already listed.
func (b *builder) addStanding(dialogPanes, approvalPanes map[string]bool) {
	for i := range b.src.Standing {
		agent := b.src.Standing[i]
		if agent.PaneID != "" && (dialogPanes[agent.PaneID] || approvalPanes[agent.PaneID]) {
			continue
		}
		var action Action
		var title string
		switch agent.State {
		case "needs_terminal", "blocked":
			action, title = Action{Kind: "terminal", Label: "Open terminal"}, agent.Label+" needs you at its terminal"
		case "failed":
			action, title = Action{Kind: "restart", Label: "Restart"}, agent.Label+" has stopped and will not restart by itself"
		default:
			continue
		}
		id := "agent:" + agent.Name
		b.items = append(b.items, Item{ID: id, Kind: KindAgent, Urgency: Blocking, Rank: rankStanding, Since: b.firstSeen(id),
			Title: title, Detail: agent.Detail, Project: b.projectRef(agent.ProjectID), Agent: agent.Name, PaneID: agent.PaneID,
			Action: action, Standing: &agent})
	}
}

// addJobs lists jobs no other item already stands for: stuck ones, ones needing a fix only the
// human can make, ones held for approval to start, and recent failures.
func (b *builder) addJobs() {
	for i := range b.src.Jobs {
		job := b.src.Jobs[i]
		if b.covered[job.ID] {
			continue
		}
		urgency, rank, action, ok := b.jobDisposition(job)
		if !ok {
			continue
		}
		id := "job:" + job.ID
		since := job.CreatedAt
		if job.LastActivityAt != nil {
			since = *job.LastActivityAt
		}
		if job.State == jobs.StateFailed && job.SettledAt != nil {
			since = *job.SettledAt
		}
		item := Item{ID: id, Kind: KindJob, Urgency: urgency, Rank: rank, Since: since, Title: job.Title,
			Detail: jobDetail(job), Project: b.projectRef(job.ProjectID), JobID: job.ID, JobTitle: job.Title,
			Agent: job.AgentName, PaneID: job.PaneID, Action: action, Job: &job}
		b.items = append(b.items, item)
	}
}

// jobDisposition says whether a job is an item by itself, and how urgent.
func (b *builder) jobDisposition(job jobs.Job) (Urgency, int, Action, bool) {
	switch {
	case job.State == jobs.StateFailed:
		recent := job.SettledAt != nil && b.now.Sub(*job.SettledAt) < FailedWindow
		if !recent || b.src.Dismissed["job:"+job.ID] {
			return "", 0, Action{}, false
		}
		return CanWait, rankJobFailed, Action{Kind: "open", Label: "Look at it"}, true
	case job.State.Terminal():
		return "", 0, Action{}, false
	case job.State == jobs.StateQueued && job.Cause == jobs.CauseApproval:
		return Blocking, rankJobHeldStart, Action{Kind: "open", Label: "Allow it to start"}, true
	case job.State == jobs.StateNeedsYou:
		switch job.Cause {
		case jobs.CauseAuth, jobs.CauseConfig, jobs.CauseLaunch, jobs.CauseDialog:
			return Blocking, rankJobBlocking, Action{Kind: "terminal", Label: "Open terminal"}, true
		}
		return "", 0, Action{}, false // questions or an approval: those items stand for it
	case job.Stuck:
		switch job.Cause {
		case jobs.CauseLimit, jobs.CauseServerError:
			return CanWait, rankJobRecover, Action{Kind: "retry", Label: "Retry now"}, true
		case jobs.CauseExited:
			return CanWait, rankJobRecover, Action{Kind: "restart", Label: "Restart agent"}, true
		case jobs.CauseSilent:
			return Blocking, rankJobBlocking, Action{Kind: "terminal", Label: "Open terminal"}, true
		}
		return Blocking, rankJobBlocking, Action{Kind: "nudge", Label: "Nudge"}, true
	}
	return "", 0, Action{}, false
}

func jobDetail(job jobs.Job) string {
	switch {
	case job.State == jobs.StateFailed:
		return "Failed: " + job.FailReason
	case job.State == jobs.StateQueued && job.Cause == jobs.CauseApproval:
		return "Waiting for your approval to start"
	case job.CauseDetail != "":
		return fmt.Sprintf("%s: %s", job.CauseText, job.CauseDetail)
	}
	return job.CauseText
}

// ---- Helpers ------------------------------------------------------------------------------

// attachJob labels an item with its job and the job's project (found by id, else by the pane
// that holds it) and marks the job covered. It returns the job, or nil.
func (b *builder) attachJob(item *Item, jobID, paneID string) *jobs.Job {
	job, ok := b.jobByID[jobID]
	if !ok && paneID != "" {
		job, ok = b.jobByPane[paneID]
	}
	if !ok {
		if standing := b.standingByPane(paneID); standing != nil {
			item.Project = b.projectRef(standing.ProjectID)
			if item.Agent == "" {
				item.Agent = standing.Name
			}
		}
		return nil
	}
	b.covered[job.ID] = true
	item.JobID, item.JobTitle = job.ID, job.Title
	item.Project = b.projectRef(job.ProjectID)
	if item.Agent == "" {
		item.Agent = job.AgentName
	}
	copied := job
	item.Job = &copied
	return &copied
}

func (b *builder) standingByPane(paneID string) *StandingAgent {
	if paneID == "" {
		return nil
	}
	for i := range b.src.Standing {
		if b.src.Standing[i].PaneID == paneID {
			return &b.src.Standing[i]
		}
	}
	return nil
}

func (b *builder) projectRef(id int64) *ProjectRef {
	project, ok := b.projects[id]
	if id == 0 || !ok {
		return nil
	}
	return &ProjectRef{ID: project.ID, Slug: project.Slug, Name: project.Name}
}

func (b *builder) firstSeen(id string) time.Time {
	if at, ok := b.src.FirstSeen[id]; ok {
		return at
	}
	return b.now
}

// ---- Counts -------------------------------------------------------------------------------

// Counts is the one number (and its split) every surface shows.
type Counts struct {
	Total    int `json:"total"` // every item not snoozed: the badge, the title, the tray
	Blocking int `json:"blocking"`
	CanWait  int `json:"can_wait"`
	Snoozed  int `json:"snoozed"`
	// ByProject counts unsnoozed items per project id ("0" is unfiled work).
	ByProject map[string]int `json:"by_project"`
}

// Count is the counts of a built queue.
func Count(items []Item) Counts {
	counts := Counts{ByProject: map[string]int{}}
	for _, item := range items {
		if item.SnoozedUntil != nil {
			counts.Snoozed++
			continue
		}
		counts.Total++
		if item.Urgency == Blocking {
			counts.Blocking++
		} else {
			counts.CanWait++
		}
		key := "0"
		if item.Project != nil {
			key = fmt.Sprint(item.Project.ID)
		}
		counts.ByProject[key]++
	}
	return counts
}
