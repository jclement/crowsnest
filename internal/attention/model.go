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
	KindLimit    Kind = "limit"    // an engine is under its plan's usage limit (DESIGN.md 6.17)
	KindWait     Kind = "wait"     // a live job with no owner or no wake-up (6.18)
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

// Label is the queue's word for an item, what every surface shows and counts by (R8: "Blocking"
// had come to mean nearly everything, so a routine `npm run test:links` approval and a force push
// to main wore the same word). Urgency stays the fact underneath (has work stopped?); the label
// adds whether the human was truly needed.
type Label string

// Labels, in the queue's order.
const (
	// LabelDangerous: a dangerous approval. Work has stopped, no rule may ever allow it.
	LabelDangerous Label = "dangerous"
	// LabelBlocking: work has stopped on it and no approval rule could have answered it (a
	// question that stops its job, a terminal dialog, a job only the human can fix, an elevated or
	// unmatchable call).
	LabelBlocking Label = "blocking"
	// LabelApproval: a routine approval, a call a rule could have allowed. Work waits on it, but it
	// is the kind of tap a project rule exists to save.
	LabelApproval Label = "approval"
	// LabelCanWait: work carries on meanwhile (an ordinary question, a job retrying by itself, a
	// failure to look at, a worktree ready to land).
	LabelCanWait Label = "can_wait"
)

// band orders the labels: dangerous and blocking together (rank puts dangerous first), then
// routine approvals, then what can wait.
func (l Label) band() int {
	switch l {
	case LabelDangerous, LabelBlocking:
		return 0
	case LabelApproval:
		return 1
	}
	return 2
}

// LabelOf is the one ranking function (R8): the label an item carries, from its urgency and, for an
// approval, its risk and whether a rule could have allowed it (approvals.RuleAnswerable). The
// queue's order, its counts, Now's sentence and the lock screen's "blocking question" all follow
// it.
func LabelOf(item Item) Label {
	if item.Kind == KindApproval && item.Approval != nil {
		switch {
		case item.Approval.Risk == risk.Dangerous:
			return LabelDangerous
		case item.Approval.Risk == risk.Low && approvals.RuleAnswerable(item.Approval.Risk, item.Approval.ToolName, item.Approval.ToolInput):
			return LabelApproval
		}
		return LabelBlocking
	}
	if item.Urgency == Blocking {
		return LabelBlocking
	}
	return LabelCanWait
}

// QuestionLabel is the label an open question carries, given whether its job declared that it has
// stopped on its questions: the same rule LabelOf applies to a question's item, for a surface (a
// push) that holds the question and not the queue.
func QuestionLabel(question questions.Question, jobStopped bool) Label {
	urgency, _ := questionUrgency(question, nil)
	if urgency == Blocking || jobStopped {
		return LabelBlocking
	}
	return LabelCanWait
}

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
	Label   Label       `json:"label"` // the word the queue shows and counts by (LabelOf)
	Rank    int         `json:"rank"`  // higher first within a label's band
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
	Limit     *jobs.LimitState    `json:"limit,omitempty"`
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
	// Limits are the fleet's usage limits (6.17): one item each, standing for every job held by it.
	Limits []jobs.LimitState
	// Violations are live jobs with no owner or no wake-up (6.18).
	Violations []jobs.Violation
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
	rankReviewObject = 36
	rankReadyToLand  = 25
	rankCheckFailed  = 37
	rankBudget       = 38
	rankSpinning     = 34
	rankNeedsRebase  = 24
	rankLimit        = 16
	rankNoOwner      = 33
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
	b.addLimits()
	b.addJobs()
	b.addViolations()
	for i := range b.items {
		b.items[i].Label = LabelOf(b.items[i])
	}
	sortItems(b.items)
	return b.items
}

// sortItems orders by the label's band (dangerous and blocking, then routine approvals, then what
// can wait), then by rank, then the longest wait first, so the item that has held work up longest
// is on top of its band (R4 Fable 1.9).
func sortItems(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		a, c := items[i], items[j]
		if a.Label.band() != c.Label.band() {
			return a.Label.band() < c.Label.band()
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
		who := name
		if job != nil {
			item.Title = "Dialog: " + job.Title
			item.Detail = job.CauseDetail
		} else if standing := b.standingByPane(agent.PaneID); standing != nil {
			item.Title = standing.Label + " is waiting at a dialog"
			item.Project = b.projectRef(standing.ProjectID)
			who = standing.Label
		}
		if agent.TrustPrompt {
			// The folder-trust question (DESIGN.md 8.2.1): said as the decision it is.
			folder := agent.TrustFolder
			if folder == "" {
				folder = "its folder"
			}
			item.Title = who + " asks to trust " + folder
			item.Action = Action{Kind: "dialog", Label: "Trust or not"}
			if job != nil {
				item.Detail = "Job " + job.ID + ", " + job.Title + ": its agent cannot start until you answer."
			}
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

// addLimits lists each engine's usage limit once; the jobs it holds are covered by it (one
// banner, not a stuck card per agent: DESIGN.md 6.17).
func (b *builder) addLimits() {
	for i := range b.src.Limits {
		limit := b.src.Limits[i]
		for _, job := range b.src.Jobs {
			if job.Engine == limit.Engine && !job.State.Terminal() && ((job.Stuck && job.Cause == jobs.CauseLimit) || (job.Cause == jobs.CauseHeld && strings.HasPrefix(job.CauseDetail, "usage limit"))) {
				b.covered[job.ID] = true
			}
		}
		title := fmt.Sprintf("%s usage limit: resets at %s", limit.Engine, limit.ResetAt.Local().Format("15:04"))
		if !b.now.Before(limit.ResetAt) {
			title = fmt.Sprintf("%s usage limit reset: resuming the waiting jobs", limit.Engine)
		}
		detail := fmt.Sprintf("%d job(s) wait; new starts, nudges and retries resume in order after the reset", limit.Held)
		if limit.Guessed {
			detail += " (the reset time is a guess)"
		}
		b.items = append(b.items, Item{ID: "limit:" + limit.Engine, Kind: KindLimit, Urgency: CanWait, Rank: rankLimit, Since: limit.Since,
			Title: title, Detail: detail, JobID: limit.Job, Action: Action{Kind: "open", Label: "See the waiting jobs"}, Limit: &limit})
	}
}

// addViolations lists live jobs with no owner or wake-up that nothing else already stands for.
func (b *builder) addViolations() {
	listed := map[string]bool{}
	for _, item := range b.items {
		if item.JobID != "" {
			listed[item.JobID] = true
		}
	}
	for _, violation := range b.src.Violations {
		if listed[violation.JobID] || b.covered[violation.JobID] {
			continue
		}
		item := Item{ID: "wait:" + violation.JobID, Kind: KindWait, Urgency: Blocking, Rank: rankNoOwner, Since: violation.Since,
			Title: violation.Title, Detail: "Nobody owns this wait: " + violation.Rule, JobID: violation.JobID, JobTitle: violation.Title,
			Action: Action{Kind: "open", Label: "Look at it"}}
		if job, ok := b.jobByID[violation.JobID]; ok {
			item.Project, item.Agent, item.PaneID = b.projectRef(job.ProjectID), job.AgentName, job.PaneID
			copied := job
			item.Job = &copied
		}
		b.items = append(b.items, item)
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
	case readyToLand(job) && (job.AcceptState == jobs.AcceptNeedsRebase || job.AcceptState == jobs.AcceptStale || job.AcceptState == jobs.AcceptFailed):
		// Finished, but crowsnest's evidence does not support landing it as it is (6.15).
		label := "Run the check again"
		if job.AcceptState == jobs.AcceptNeedsRebase {
			label = "Ask an agent to rebase"
		}
		return CanWait, rankNeedsRebase, Action{Kind: "open", Label: label}, true
	case readyToLand(job):
		return CanWait, rankReadyToLand, Action{Kind: "land", Label: "Review and land"}, true
	case job.State == jobs.StateNeedsYou && job.Cause == jobs.CauseReview:
		return Blocking, rankReviewObject, Action{Kind: "land", Label: "Land, message the builder or discard"}, true
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
		case jobs.CauseCheck:
			return Blocking, rankCheckFailed, Action{Kind: "open", Label: "Run the check again"}, true
		case jobs.CauseBudget:
			return Blocking, rankBudget, Action{Kind: "open", Label: "Raise, finish the step or stop"}, true
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
		case jobs.CauseSpinning:
			return Blocking, rankSpinning, Action{Kind: "terminal", Label: "Open terminal"}, true
		}
		return Blocking, rankJobBlocking, Action{Kind: "nudge", Label: "Nudge"}, true
	case job.State == jobs.StateBlocked && job.WakeKind == jobs.WakeHuman:
		// Blocked until the human looks (6.18).
		return Blocking, rankJobBlocking, Action{Kind: "open", Label: "Wake it"}, true
	}
	return "", 0, Action{}, false
}

// readyToLand: a finished builder's worktree waits to be merged, opened as a pull request or
// discarded (DESIGN.md 6.11). It stays in the queue until the human lands or discards it.
func readyToLand(job jobs.Job) bool {
	return job.State == jobs.StateDone && job.WorktreeBranch != "" && job.ReviewOf == "" && job.LandState == "" && job.TornDownAt == nil
}

func jobDetail(job jobs.Job) string {
	switch {
	case readyToLand(job) && job.AcceptState != jobs.AcceptNone && job.AcceptState != jobs.AcceptAccepted && job.AcceptState != jobs.AcceptUnverified:
		return job.AcceptDetail
	case readyToLand(job):
		return "Ready to land: " + job.WorktreeBranch
	case job.State == jobs.StateNeedsYou && job.Cause == jobs.CauseReview:
		return "Reviewer still objects: " + job.CauseDetail
	case job.State == jobs.StateFailed:
		return "Failed: " + job.FailReason
	case job.State == jobs.StateQueued && job.Cause == jobs.CauseApproval:
		return "Waiting for your approval to start"
	case job.State == jobs.StateBlocked:
		return "Blocked until you look: " + job.BlockedReason
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
	Total int `json:"total"` // every item not snoozed: the badge, the title, the tray
	// Blocking counts the dangerous and blocking labels: work stopped and only the human can move
	// it. Approval counts routine approvals (a rule could have allowed them); CanWait the rest.
	Blocking int `json:"blocking"`
	Approval int `json:"approval"`
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
		switch item.Label {
		case LabelDangerous, LabelBlocking:
			counts.Blocking++
		case LabelApproval:
			counts.Approval++
		default:
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
