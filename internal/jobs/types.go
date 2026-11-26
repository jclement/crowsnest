// Package jobs is crowsnest's unit of work and the supervisor that keeps it moving (DESIGN.md
// section 6): the job record and its timeline, the one function that validates state changes,
// the launcher that starts an agent for a job (a worktree, a tab, or the orchestrator), the
// agent protocol (job_update, done, fail, job_create, job_get), the Claude Code hooks with the
// disposition check that refuses an undeclared stop (decide.go), recovery from the real reasons
// agents stop (recovery.go), the heartbeat, and the roles.
//
// Everything that changes a job goes through Service, under one lock, and is journalled as a
// job_event and published as a `job` hub event.
package jobs

import (
	"fmt"
	"time"
)

// State is a job's declared state (6.1).
type State string

// States. Only done, failed and cancelled are terminal.
const (
	StateQueued    State = "queued"    // recorded, no agent yet (or waiting for the orchestrator)
	StateRunning   State = "running"   // an agent holds it and is expected to be working
	StateNeedsYou  State = "needs_you" // every line of work waits on Jeff (questions, a dialog)
	StateBlocked   State = "blocked"   // waiting on something else; carries a reason
	StateInReview  State = "in_review" // reserved for review gates (6.7)
	StateDone      State = "done"
	StateFailed    State = "failed"
	StateCancelled State = "cancelled"
)

// Terminal reports whether no further change is allowed.
func (s State) Terminal() bool {
	return s == StateDone || s == StateFailed || s == StateCancelled
}

// Valid reports whether s is a known state.
func (s State) Valid() bool {
	switch s {
	case StateQueued, StateRunning, StateNeedsYou, StateBlocked, StateInReview, StateDone, StateFailed, StateCancelled:
		return true
	}
	return false
}

// ErrTerminal is returned for any change to a finished job.
type TransitionError struct {
	From, To State
}

func (e *TransitionError) Error() string {
	if e.From.Terminal() {
		return fmt.Sprintf("the job is already %s; a finished job cannot change", e.From)
	}
	return fmt.Sprintf("a job cannot go from %s to %s", e.From, e.To)
}

// CheckTransition is the one place state changes are validated. Terminal states are final;
// a job cannot go back to queued once it has started; anything else between non-terminal states
// is allowed (running, needs_you and blocked move freely as questions are asked and answered).
// Staying in the same state is always allowed for a live job.
func CheckTransition(from, to State) error {
	if !to.Valid() || from.Terminal() {
		return &TransitionError{From: from, To: to}
	}
	if from == to {
		return nil
	}
	if to == StateQueued {
		return &TransitionError{From: from, To: to}
	}
	return nil
}

// Cause says why a job is stuck, or why it needs Jeff (6.4).
type Cause string

// Causes.
const (
	CauseNone         Cause = ""
	CauseQuestions    Cause = "questions"    // needs_you: open questions block everything left
	CauseDialog       Cause = "dialog"       // needs_you: a terminal dialog (trust, permission)
	CauseUnresponsive Cause = "unresponsive" // stuck: three nudges with no progress
	CausePermissions  Cause = "permissions"  // stuck: repeated auto-mode denials
	CauseLimit        Cause = "limit"        // stuck: usage or rate limit; retried
	CauseServerError  Cause = "server_error" // stuck: API error; retried
	CauseAuth         Cause = "auth"         // needs_you: authentication or billing; no retries
	CauseSilent       Cause = "silent"       // stuck: herdr says working, transcript quiet 20 min
	CauseIdle         Cause = "idle"         // stuck: no activity 10 min and not working (hook missing?)
	CauseExited       Cause = "exited"       // the agent process left; restarting
	CauseLaunch       Cause = "launch"       // the agent could not be started
	CauseConfig       Cause = "config"       // needs_you: Claude refused the request (model, request)
	// CauseApproval: queued, held until the human allows a job an agent asked for beyond its
	// own directory, engine or isolation (R2-H1). CauseDetail is the question's id.
	CauseApproval Cause = "approval"
)

// Describe is the cause in words, for the card and notices.
func (c Cause) Describe() string {
	switch c {
	case CauseQuestions:
		return "Waiting on your answers"
	case CauseDialog:
		return "A dialog in the terminal needs you"
	case CauseUnresponsive:
		return "Stopped three times without saying why"
	case CausePermissions:
		return "Tool calls keep being denied"
	case CauseLimit:
		return "Hit a usage or rate limit"
	case CauseServerError:
		return "The API returned errors"
	case CauseAuth:
		return "Claude needs a login or billing fix"
	case CauseSilent:
		return "Working but silent for 20 minutes"
	case CauseIdle:
		return "Idle with nothing declared"
	case CauseExited:
		return "The agent exited"
	case CauseLaunch:
		return "The agent could not be started"
	case CauseConfig:
		return "Claude refused the request (model or settings)"
	case CauseApproval:
		return "Waiting for your approval: an agent asked for it"
	}
	return ""
}

// Engines.
const (
	EngineClaude = "claude"
	EngineCodex  = "codex"
)

// Job is the record both APIs return (6.1).
type Job struct {
	ID             string     `json:"id"` // j_<n>
	Title          string     `json:"title"`
	BriefMD        string     `json:"brief_md"`
	DoneWhen       string     `json:"done_when"`
	Role           string     `json:"role"`
	Dir            string     `json:"dir"`
	WorktreeBranch string     `json:"worktree_branch"`
	WorkDir        string     `json:"work_dir"`
	Engine         string     `json:"engine"`
	Model          string     `json:"model"`
	ParentID       string     `json:"parent_id,omitempty"`
	State          State      `json:"state"`
	Stuck          bool       `json:"stuck"`
	Cause          Cause      `json:"cause"`
	CauseText      string     `json:"cause_text"` // Cause in words
	CauseDetail    string     `json:"cause_detail"`
	BlockedReason  string     `json:"blocked_reason"`
	Waiting        bool       `json:"waiting"` // the agent said nothing is left its questions do not block
	Progress       string     `json:"progress"`
	DoingNow       string     `json:"doing_now"` // progress, or the last tool call when progress is stale
	LastTool       string     `json:"last_tool"`
	ResultMD       string     `json:"result_md"`
	Verified       []string   `json:"verified"`
	NotVerified    []string   `json:"not_verified"`
	FailReason     string     `json:"fail_reason"`
	PaneID         string     `json:"pane_id"`
	AgentName      string     `json:"agent_name"`
	SessionID      string     `json:"claude_session_id"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at"`
	SettledAt      *time.Time `json:"settled_at"`
	LastActivityAt *time.Time `json:"last_activity_at"`
	Nudges         int        `json:"nudges"`
	Restarts       int        `json:"restarts"`
	RetryAt        *time.Time `json:"retry_at"`
	RetryCount     int        `json:"retry_count"`
	OpenQuestions  int        `json:"open_questions"`
	Isolate        bool       `json:"isolate"`
	// Origin is who created it: "" the human, "agent" a job agent, "orchestrator" (R2-H1).
	Origin string `json:"origin"`

	isolate   bool // the job asked for a worktree (Isolated)
	briefSent bool // the brief reached the agent (a restart resumes rather than briefs again)
}

// Event is one timeline entry.
type Event struct {
	ID      int64          `json:"id"`
	At      time.Time      `json:"at"`
	Kind    string         `json:"kind"`
	Summary string         `json:"summary"`
	Detail  map[string]any `json:"detail,omitempty"`
}

// Event kinds.
const (
	EventCreated  = "created"
	EventLaunch   = "launch"
	EventState    = "state"
	EventProgress = "progress"
	EventNudge    = "nudge"
	EventHook     = "hook"
	EventStuck    = "stuck"
	EventRetry    = "retry"
	EventRestart  = "restart"
	EventQuestion = "question"
	EventMessage  = "message"
	EventNotify   = "notify"
	EventDone     = "done"
	EventFail     = "fail"
	EventChild    = "child"
	EventSession  = "session"
)

// Detail is a job with its timeline and questions (GET /jobs/{id}).
type Detail struct {
	Job
	Timeline  []Event    `json:"timeline"`
	Questions []Question `json:"questions"`
}

// Question is the part of a question a job detail shows; the web app fetches the full one from
// /questions/{id} to answer it.
type Question struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Kind        string    `json:"kind"`
	Status      string    `json:"status"`
	Urgency     string    `json:"urgency"`
	Wait        bool      `json:"wait"`
	AnswerValue any       `json:"answer_value"`
	AnswerNote  string    `json:"answer_note"`
	CreatedAt   time.Time `json:"created_at"`
	// Options are the question's choices (key and label), so the job view can show an answer by
	// its label ("Friday") rather than its key ("fri"). Empty for kinds that take none.
	Options []QuestionOption `json:"options"`
}

// QuestionOption is one choice of a job's question: what was stored (key) and what was shown.
type QuestionOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// HubEvent is the data of a `job` hub event.
type HubEvent struct {
	ID     string `json:"id"`
	State  State  `json:"state"`
	Stuck  bool   `json:"stuck"`
	Cause  Cause  `json:"cause"`
	Title  string `json:"title"`
	Change string `json:"change"` // created, updated, state, event
}

// FormatID renders j_<n>.
func FormatID(id int64) string { return fmt.Sprintf("j_%d", id) }

// ParseID accepts j_<n> or a bare number.
func ParseID(text string) (int64, error) {
	var id int64
	if _, err := fmt.Sscanf(text, "j_%d", &id); err == nil && id > 0 && FormatID(id) == text {
		return id, nil
	}
	if _, err := fmt.Sscanf(text, "%d", &id); err == nil && id > 0 && fmt.Sprint(id) == text {
		return id, nil
	}
	return 0, fmt.Errorf("%q is not a job id (expected j_<number>)", text)
}
