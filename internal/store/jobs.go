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
	}
	return cols
}

func textCol(name string, p *string) column {
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
// oldest first.
func (s *Store) ListQuestionsByJob(ctx context.Context, jobID string) ([]QuestionRecord, error) {
	return s.queryQuestions(ctx, `SELECT `+questionColumns+` FROM questions WHERE job_id = ? ORDER BY id`, jobID)
}
