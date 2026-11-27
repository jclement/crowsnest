// Job rows and their timelines (DESIGN.md sections 6.1 and 11). The store keeps a job as a flat
// record mirroring the table; internal/jobs owns the meaning of every field, the state machine
// and the supervisor. Times are stored as unix seconds and come back as time.Time (zero = unset).
//
// One mapping (jobRecord.columns) lists every column with a pointer into the record, so the
// SELECT list, the scan and the UPDATE cannot drift apart.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// JobRecord is one row of jobs.
type JobRecord struct {
	ID              int64
	Title           string
	BriefMD         string
	DoneWhen        string
	Role            string
	Dir             string
	WorktreeBranch  string
	WorkDir         string
	Isolate         bool
	Engine          string
	Model           string
	ParentID        int64
	State           string
	Stuck           bool
	Cause           string
	CauseDetail     string
	BlockedReason   string
	Waiting         bool
	Progress        string
	LastTool        string
	ResultMD        string
	VerifiedJSON    string
	NotVerifiedJSON string
	FailReason      string
	PaneID          string
	AgentName       string
	ClaudeSessionID string
	BriefSent       bool
	CreatedAt       time.Time
	StartedAt       time.Time
	SettledAt       time.Time
	LastActivityAt  time.Time
	LastProgressAt  time.Time
	Nudges          int
	NudgeStreak     int
	LastNudgeAt     time.Time
	Denials         int
	Restarts        int
	RestartTimes    string
	RetryAt         time.Time
	RetryCount      int
	// LastStopAt is when the agent's Stop hook last ran (migration 0005); the backstop reads it.
	LastStopAt time.Time
	// The lifecycle claim (migration 0006): the step acting outside crowsnest right now ("" for
	// none), the crowsnest process that holds it, when it was claimed, and the generation every
	// claim and every terminal transition bumps (internal/jobs/lifecycle.go).
	LifecycleOp    string
	LifecycleOwner string
	LifecycleAt    time.Time
	LifecycleGen   int64
	// Origin is who created the job (migration 0007, R2-H1): "" the human, "agent" a job agent,
	// "orchestrator" the orchestrator.
	Origin string
	// RootDir is the resolved directory the job's children may use without asking (migration
	// 0008, R3-H1), recorded when crowsnest created its pane; "" grants nothing.
	RootDir string
	// ProjectID is the project the job belongs to (migration 0013); 0 is "Unfiled".
	ProjectID int64

	// Landing and review (migration 0014, DESIGN.md 6.7 and 6.11).
	BaseBranch     string
	BaseCommit     string
	WorkspaceID    string
	WalkthroughMD  string
	Reviewer       string
	ReviewOf       int64
	ReviewRound    int
	ReviewVerdict  string
	ReviewMD       string
	LandState      string
	LandDetail     string
	LandedAt       time.Time
	TornDownAt     time.Time
	LandRequest    string
	LandQuestionID int64
	StartAnyway    bool

	// Acceptance, budgets, wake-ups, the last message and the spinning detector (migration
	// 0015, DESIGN.md 6.15 to 6.19).
	AcceptState     string
	AcceptDetail    string
	CheckRounds     int
	NoCommitsReason string
	FixOf           int64
	ReviewCommit    string
	LandHead        string
	BudgetUSD       float64
	BudgetTokens    int64
	BudgetMinutes   int
	BudgetTurns     int
	BudgetState     string
	BudgetDetail    string
	Turns           int
	WakeKind        string
	WakeAt          time.Time
	WakeJob         int64
	Rechecks        int
	LastMessage     string
	LastMessageAt   time.Time
	TreeHash        string
	TreeChangedAt   time.Time
	// LandToken binds a land request to its question (migration 0018, R5-C01).
	LandToken       string
	LandRequestedAt time.Time
}

// JobEventRecord is one row of job_events.
type JobEventRecord struct {
	ID         int64
	JobID      int64
	At         time.Time
	Kind       string
	Summary    string
	DetailJSON string
}

// column pairs a column name with how to read and write it.
type column struct {
	name  string
	scan  any        // pointer handed to Scan
	value func() any // value written by INSERT/UPDATE
	after func()     // converts scanned helpers back into the record (times)
}

// jobColumns maps every column except id onto r. Time columns scan into int64 helpers that
// after() converts.
func jobColumns(r *JobRecord) []column {
	cols := []column{
		textCol("title", &r.Title), textCol("brief_md", &r.BriefMD), textCol("done_when", &r.DoneWhen),
		textCol("role", &r.Role), textCol("dir", &r.Dir), textCol("worktree_branch", &r.WorktreeBranch),
		textCol("work_dir", &r.WorkDir), boolCol("isolate", &r.Isolate), textCol("engine", &r.Engine),
		textCol("model", &r.Model), int64Col("parent_id", &r.ParentID), textCol("state", &r.State),
		boolCol("stuck", &r.Stuck), textCol("cause", &r.Cause), textCol("cause_detail", &r.CauseDetail),
		textCol("blocked_reason", &r.BlockedReason), boolCol("waiting", &r.Waiting), textCol("progress", &r.Progress),
		textCol("last_tool", &r.LastTool), textCol("result_md", &r.ResultMD), textCol("verified_json", &r.VerifiedJSON),
		textCol("not_verified_json", &r.NotVerifiedJSON), textCol("fail_reason", &r.FailReason),
		textCol("pane_id", &r.PaneID), textCol("agent_name", &r.AgentName), textCol("claude_session_id", &r.ClaudeSessionID),
		boolCol("brief_sent", &r.BriefSent),
		timeCol("created_at", &r.CreatedAt), timeCol("started_at", &r.StartedAt), timeCol("settled_at", &r.SettledAt),
		timeCol("last_activity_at", &r.LastActivityAt), timeCol("last_progress_at", &r.LastProgressAt),
		intCol("nudges", &r.Nudges), intCol("nudge_streak", &r.NudgeStreak), timeCol("last_nudge_at", &r.LastNudgeAt),
		intCol("denials", &r.Denials), intCol("restarts", &r.Restarts), textCol("restart_times", &r.RestartTimes),
		timeCol("retry_at", &r.RetryAt), intCol("retry_count", &r.RetryCount),
		timeCol("last_stop_at", &r.LastStopAt),
		textCol("lifecycle_op", &r.LifecycleOp), textCol("lifecycle_owner", &r.LifecycleOwner),
		timeCol("lifecycle_at", &r.LifecycleAt), int64Col("lifecycle_gen", &r.LifecycleGen),
		textCol("origin", &r.Origin), textCol("root_dir", &r.RootDir), int64Col("project_id", &r.ProjectID),
		textCol("base_branch", &r.BaseBranch), textCol("base_commit", &r.BaseCommit), textCol("workspace_id", &r.WorkspaceID),
		textCol("walkthrough_md", &r.WalkthroughMD), textCol("reviewer", &r.Reviewer), int64Col("review_of", &r.ReviewOf),
		intCol("review_round", &r.ReviewRound), textCol("review_verdict", &r.ReviewVerdict), textCol("review_md", &r.ReviewMD),
		textCol("land_state", &r.LandState), textCol("land_detail", &r.LandDetail), timeCol("landed_at", &r.LandedAt),
		timeCol("torn_down_at", &r.TornDownAt), textCol("land_request", &r.LandRequest),
		int64Col("land_question_id", &r.LandQuestionID), boolCol("start_anyway", &r.StartAnyway),
		textCol("accept_state", &r.AcceptState), textCol("accept_detail", &r.AcceptDetail), intCol("check_rounds", &r.CheckRounds),
		textCol("no_commits_reason", &r.NoCommitsReason), int64Col("fix_of", &r.FixOf), textCol("review_commit", &r.ReviewCommit), textCol("land_head", &r.LandHead),
		floatCol("budget_usd", &r.BudgetUSD), int64Col("budget_tokens", &r.BudgetTokens), intCol("budget_minutes", &r.BudgetMinutes),
		intCol("budget_turns", &r.BudgetTurns), textCol("budget_state", &r.BudgetState), textCol("budget_detail", &r.BudgetDetail),
		intCol("turns", &r.Turns), textCol("wake_kind", &r.WakeKind), timeCol("wake_at", &r.WakeAt), int64Col("wake_job", &r.WakeJob),
		intCol("rechecks", &r.Rechecks), textCol("last_message", &r.LastMessage), timeCol("last_message_at", &r.LastMessageAt),
		textCol("tree_hash", &r.TreeHash), timeCol("tree_changed_at", &r.TreeChangedAt),
		textCol("land_token", &r.LandToken), timeCol("land_requested_at", &r.LandRequestedAt),
	}
	return cols
}

func textCol(name string, p *string) column {
	return column{name: name, scan: p, value: func() any { return *p }}
}
func floatCol(name string, p *float64) column {
	return column{name: name, scan: p, value: func() any { return *p }}
}
func boolCol(name string, p *bool) column {
	return column{name: name, scan: p, value: func() any { return *p }}
}
func intCol(name string, p *int) column {
	return column{name: name, scan: p, value: func() any { return *p }}
}
func int64Col(name string, p *int64) column {
	return column{name: name, scan: p, value: func() any { return *p }}
}
func timeCol(name string, p *time.Time) column {
	var raw int64
	return column{
		name: name, scan: &raw,
		value: func() any { return unixOrNil(*p) },
		after: func() { *p = unixToTime(raw) },
	}
}

// unixOrNil stores a zero time as 0, so "unset" survives the round trip.
func unixOrNil(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.Unix()
}

func unixToTime(seconds int64) time.Time {
	if seconds == 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0)
}

func columnNames(cols []column) []string {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.name
	}
	return names
}

var jobSelect = "SELECT id, " + strings.Join(columnNames(jobColumns(&JobRecord{})), ", ") + " FROM jobs"

// InsertJob stores a new job and returns its id.
func (s *Store) InsertJob(ctx context.Context, record JobRecord) (int64, error) {
	cols := jobColumns(&record)
	values := make([]any, len(cols))
	for i, c := range cols {
		values[i] = c.value()
	}
	// Column names come from jobColumns (compile-time constants); every value is a parameter.
	query := "INSERT INTO jobs (" + strings.Join(columnNames(cols), ", ") + ") VALUES (?" + strings.Repeat(", ?", len(cols)-1) + ")" //nolint:gosec // see above
	result, err := s.db.ExecContext(ctx, query, values...)
	if err != nil {
		return 0, fmt.Errorf("inserting job: %w", err)
	}
	return result.LastInsertId()
}

// UpdateJob writes every column of record (identified by its ID). internal/jobs serialises its
// changes under one lock, so a whole-row write is safe and keeps the store simple.
func (s *Store) UpdateJob(ctx context.Context, record JobRecord) error {
	cols := jobColumns(&record)
	sets := make([]string, len(cols))
	values := make([]any, 0, len(cols)+1)
	for i, c := range cols {
		sets[i] = c.name + " = ?"
		values = append(values, c.value())
	}
	values = append(values, record.ID)
	// Column names come from jobColumns (compile-time constants); every value is a parameter.
	result, err := s.db.ExecContext(ctx, "UPDATE jobs SET "+strings.Join(sets, ", ")+" WHERE id = ?", values...) //nolint:gosec // see above
	if err != nil {
		return fmt.Errorf("updating job %d: %w", record.ID, err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

// GetJob returns one job, or ErrNotFound.
func (s *Store) GetJob(ctx context.Context, id int64) (JobRecord, error) {
	jobs, err := s.queryJobs(ctx, jobSelect+" WHERE id = ?", id)
	if err != nil {
		return JobRecord{}, err
	}
	if len(jobs) == 0 {
		return JobRecord{}, ErrNotFound
	}
	return jobs[0], nil
}

// ListJobs returns jobs, newest first, at most limit. With states, only those states.
func (s *Store) ListJobs(ctx context.Context, states []string, limit int) ([]JobRecord, error) {
	query := jobSelect
	args := make([]any, 0, len(states)+1)
	if len(states) > 0 {
		query += " WHERE state IN (?" + strings.Repeat(", ?", len(states)-1) + ")"
		for _, state := range states {
			args = append(args, state)
		}
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)
	return s.queryJobs(ctx, query, args...)
}

// ListJobsPage returns jobs in states with an id above afterID, oldest first, at most limit.
// The supervisor pages through every live job with it: the board's newest-first page is not a
// work set (R2-C16).
func (s *Store) ListJobsPage(ctx context.Context, states []string, afterID int64, limit int) ([]JobRecord, error) {
	query := jobSelect + " WHERE id > ?"
	args := []any{afterID}
	if len(states) > 0 {
		query += " AND state IN (?" + strings.Repeat(", ?", len(states)-1) + ")"
		for _, state := range states {
			args = append(args, state)
		}
	}
	query += " ORDER BY id LIMIT ?"
	args = append(args, limit)
	return s.queryJobs(ctx, query, args...)
}

// ListJobsByPane returns the jobs in states whose agent runs in paneID, newest first.
func (s *Store) ListJobsByPane(ctx context.Context, paneID string, states []string) ([]JobRecord, error) {
	query := jobSelect + " WHERE pane_id = ?"
	args := []any{paneID}
	if len(states) > 0 {
		query += " AND state IN (?" + strings.Repeat(", ?", len(states)-1) + ")"
		for _, state := range states {
			args = append(args, state)
		}
	}
	return s.queryJobs(ctx, query+" ORDER BY id DESC", args...)
}

// CountJobsHeldBy counts the jobs in states held by the agent named agentName.
func (s *Store) CountJobsHeldBy(ctx context.Context, agentName string, states []string) (int, error) {
	if len(states) == 0 {
		return 0, errors.New("counting held jobs needs at least one state")
	}
	args := []any{agentName}
	for _, state := range states {
		args = append(args, state)
	}
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs WHERE agent_name = ? AND state IN (?"+strings.Repeat(", ?", len(states)-1)+")", args...).Scan(&count) //nolint:gosec // placeholders only
	if err != nil {
		return 0, fmt.Errorf("counting the jobs %s holds: %w", agentName, err)
	}
	return count, nil
}

// OldestUnassignedQueued returns the oldest queued job with no directory and no agent: the next
// one the orchestrator takes. A job held for the human's approval (cause 'approval', R2-H1) is
// not offered, nor a project's job (its lead takes those). ok is false when there is none.
func (s *Store) OldestUnassignedQueued(ctx context.Context) (JobRecord, bool, error) {
	jobs, err := s.queryJobs(ctx, jobSelect+" WHERE state = 'queued' AND dir = '' AND agent_name = '' AND cause != 'approval' AND project_id = 0 ORDER BY id LIMIT 1")
	if err != nil || len(jobs) == 0 {
		return JobRecord{}, false, err
	}
	return jobs[0], true, nil
}

// CountChildJobs is how many jobs name parentID as their parent, in any state (R2-H1's fan-out
// cap counts every child ever made, so a loop that finishes its children quickly is still
// stopped).
func (s *Store) CountChildJobs(ctx context.Context, parentID int64) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE parent_id = ?`, parentID).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting the children of job %d: %w", parentID, err)
	}
	return count, nil
}

// CountLiveAgentJobs is how many jobs an agent (or the orchestrator) created that are not yet
// done, failed or cancelled (R2-H1's global cap). Jobs still held for the human's approval are not
// counted: they hold no agent, and an agent filing requests must not crowd out every other
// agent's delegation (R3-L3); CountHeldApprovals caps them per creator instead.
func (s *Store) CountLiveAgentJobs(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE origin != '' AND cause != 'approval' AND state NOT IN ('done', 'failed', 'cancelled')`).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting live agent-created jobs: %w", err)
	}
	return count, nil
}

// CountHeldApprovals is how many jobs a creator has waiting for the human's approval: a job
// agent's (parentID > 0), or the orchestrator's made while it held no job (parentID 0).
func (s *Store) CountHeldApprovals(ctx context.Context, origin string, parentID int64) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE origin = ? AND parent_id = ? AND state = 'queued' AND cause = 'approval'`, origin, parentID).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting jobs held for approval: %w", err)
	}
	return count, nil
}

// CountLiveOwnJobs is how many live jobs (not held for approval) a creator made with no parent
// job: the orchestrator's fan-out while it holds no job of its own (R3-M2).
func (s *Store) CountLiveOwnJobs(ctx context.Context, origin string) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE origin = ? AND parent_id = 0 AND cause != 'approval' AND state NOT IN ('done', 'failed', 'cancelled')`, origin).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting live jobs made by the %s: %w", origin, err)
	}
	return count, nil
}

func (s *Store) queryJobs(ctx context.Context, query string, args ...any) ([]JobRecord, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []JobRecord
	for rows.Next() {
		var record JobRecord
		cols := jobColumns(&record)
		dest := make([]any, 0, len(cols)+1)
		dest = append(dest, &record.ID)
		for _, c := range cols {
			dest = append(dest, c.scan)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("reading job: %w", err)
		}
		for _, c := range cols {
			if c.after != nil {
				c.after()
			}
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// ---- Timeline ------------------------------------------------------------------------------

// InsertJobEvent appends one event to a job's timeline and returns its id.
func (s *Store) InsertJobEvent(ctx context.Context, event JobEventRecord) (int64, error) {
	if event.DetailJSON == "" {
		event.DetailJSON = "{}"
	}
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO job_events (job_id, at, kind, summary, detail_json) VALUES (?, ?, ?, ?, ?)`,
		event.JobID, event.At.Unix(), event.Kind, event.Summary, event.DetailJSON)
	if err != nil {
		return 0, fmt.Errorf("recording job event: %w", err)
	}
	return result.LastInsertId()
}

// ListJobEvents returns a job's timeline, oldest first, at most limit (the newest limit when
// there are more).
func (s *Store) ListJobEvents(ctx context.Context, jobID int64, limit int) ([]JobEventRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, job_id, at, kind, summary, detail_json FROM
		   (SELECT * FROM job_events WHERE job_id = ? ORDER BY id DESC LIMIT ?) ORDER BY id`, jobID, limit)
	if err != nil {
		return nil, fmt.Errorf("reading the timeline of job %d: %w", jobID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []JobEventRecord
	for rows.Next() {
		var event JobEventRecord
		var at int64
		if err := rows.Scan(&event.ID, &event.JobID, &at, &event.Kind, &event.Summary, &event.DetailJSON); err != nil {
			return nil, fmt.Errorf("reading job event: %w", err)
		}
		event.At = time.Unix(at, 0)
		out = append(out, event)
	}
	return out, rows.Err()
}

// HasJobEvent reports whether a job's timeline already holds an event of kind whose detail
// contains marker (used to do something once, such as re-notifying about one question).
func (s *Store) HasJobEvent(ctx context.Context, jobID int64, kind, marker string) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM job_events WHERE job_id = ? AND kind = ? AND instr(detail_json, ?) > 0 LIMIT 1`,
		jobID, kind, marker).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("searching the timeline of job %d: %w", jobID, err)
	}
	return true, nil
}

// ListQuestionsByJob returns the questions asked under a job (questions.job_id = "j_<id>"),
// oldest first, without their attachments (FileCount says how many; R2-C12).
func (s *Store) ListQuestionsByJob(ctx context.Context, jobID string) ([]QuestionRecord, error) {
	return s.queryQuestions(ctx, `SELECT `+listColumns+` FROM questions WHERE job_id = ? ORDER BY id`, jobID)
}

// JobQuestionSummary is what the supervisor needs from a job's questions, in one aggregate
// query (R2-C12).
type JobQuestionSummary struct {
	Open     int // open questions
	Blocking int // open questions asked with wait=true
	// AnswerIncomingAt is the newest moment an outcome was typed back into the agent, or is still
	// on its way: for each settled question whose outcome is typed, its delivery time, else (not
	// delivered and not failed) when it was claimed or settled. A delivery that failed or was
	// abandoned counts for nothing (R2-C04). Zero: none.
	AnswerIncomingAt time.Time
}

// SummarizeJobQuestions returns the summary of the questions asked under jobID.
func (s *Store) SummarizeJobQuestions(ctx context.Context, jobID string) (JobQuestionSummary, error) {
	var summary JobQuestionSummary
	var incoming int64
	err := s.db.QueryRowContext(ctx, `SELECT
		  COALESCE(SUM(status = 'open'), 0),
		  COALESCE(SUM(status = 'open' AND wait), 0),
		  COALESCE(MAX(CASE WHEN status != 'open' AND delivery = 'type' AND delivery_error = ''
		      THEN COALESCE(delivered_at, delivery_claimed_at, settled_at) END), 0)
		FROM questions WHERE job_id = ?`, jobID).Scan(&summary.Open, &summary.Blocking, &incoming)
	if err != nil {
		return JobQuestionSummary{}, fmt.Errorf("summarising the questions of job %s: %w", jobID, err)
	}
	summary.AnswerIncomingAt = unixToTime(incoming)
	return summary, nil
}
