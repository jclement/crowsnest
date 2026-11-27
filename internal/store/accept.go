package store

// Acceptance, budgets and wake-ups (migration 0015, DESIGN.md 6.15 to 6.19): the checks crowsnest
// runs on a finished coding job, a project's check command and budget defaults, the jobs that
// wait on another job, a job's children, and when an answer was taken in.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CheckRecord is one run of a job's check (job_checks).
type CheckRecord struct {
	ID         int64
	JobID      int64
	Commit     string
	BaseCommit string
	Command    string
	Status     string // running | passed | failed | error
	ExitCode   int
	Duration   time.Duration
	OutputTail string
	Owner      string
	StartedAt  time.Time
	FinishedAt time.Time
	// DefinitionChanged lists files of the job's diff that define its check (migration 0019,
	// R6-S4), comma-separated.
	DefinitionChanged string
}

const checkSelect = `SELECT id, job_id, commit_sha, base_sha, command, status, exit_code, duration_ms, output_tail, owner,
	started_at, finished_at, definition_changed FROM job_checks`

func scanCheck(row interface{ Scan(...any) error }) (CheckRecord, error) {
	var r CheckRecord
	var duration, started, finished int64
	err := row.Scan(&r.ID, &r.JobID, &r.Commit, &r.BaseCommit, &r.Command, &r.Status, &r.ExitCode, &duration, &r.OutputTail, &r.Owner,
		&started, &finished, &r.DefinitionChanged)
	r.Duration = time.Duration(duration) * time.Millisecond
	r.StartedAt, r.FinishedAt = unixToTime(started), unixToTime(finished)
	return r, err
}

// InsertCheck records a check that has started and returns its id.
func (s *Store) InsertCheck(ctx context.Context, r CheckRecord) (int64, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO job_checks (job_id, commit_sha, base_sha, command, status, exit_code,
		duration_ms, output_tail, owner, started_at, finished_at, definition_changed) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.JobID, r.Commit, r.BaseCommit, r.Command, r.Status, r.ExitCode, r.Duration.Milliseconds(), r.OutputTail, r.Owner,
		unixOrNil(r.StartedAt), unixOrNil(r.FinishedAt), r.DefinitionChanged)
	if err != nil {
		return 0, fmt.Errorf("recording a check: %w", err)
	}
	return result.LastInsertId()
}

// FinishCheck records how a check ended.
func (s *Store) FinishCheck(ctx context.Context, r CheckRecord) error {
	_, err := s.db.ExecContext(ctx, `UPDATE job_checks SET status = ?, exit_code = ?, duration_ms = ?, output_tail = ?,
		finished_at = ?, commit_sha = ?, definition_changed = ? WHERE id = ?`, r.Status, r.ExitCode, r.Duration.Milliseconds(), r.OutputTail,
		unixOrNil(r.FinishedAt), r.Commit, r.DefinitionChanged, r.ID)
	if err != nil {
		return fmt.Errorf("finishing check %d: %w", r.ID, err)
	}
	return nil
}

// LatestCheck returns a job's newest check, or ErrNotFound.
func (s *Store) LatestCheck(ctx context.Context, jobID int64) (CheckRecord, error) {
	r, err := scanCheck(s.db.QueryRowContext(ctx, checkSelect+` WHERE job_id = ? ORDER BY id DESC LIMIT 1`, jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return CheckRecord{}, ErrNotFound
	}
	if err != nil {
		return CheckRecord{}, fmt.Errorf("reading the check of job %d: %w", jobID, err)
	}
	return r, nil
}

// ListChecks returns a job's checks, newest first, at most limit.
func (s *Store) ListChecks(ctx context.Context, jobID int64, limit int) ([]CheckRecord, error) {
	rows, err := s.db.QueryContext(ctx, checkSelect+` WHERE job_id = ? ORDER BY id DESC LIMIT ?`, jobID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing checks of job %d: %w", jobID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []CheckRecord
	for rows.Next() {
		r, err := scanCheck(rows)
		if err != nil {
			return nil, fmt.Errorf("reading a check: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ProjectAccept is a project's acceptance and budget defaults (projects columns from 0015).
type ProjectAccept struct {
	CheckCommand  string  // '' automatic, 'none', or a shell command
	BudgetUSD     float64 // 0: the server's default
	BudgetMinutes int
}

// GetProjectAccept reads a project's acceptance defaults (zero for an unknown project).
func (s *Store) GetProjectAccept(ctx context.Context, id int64) (ProjectAccept, error) {
	var r ProjectAccept
	err := s.db.QueryRowContext(ctx, `SELECT check_command, budget_usd, budget_minutes FROM projects WHERE id = ?`, id).
		Scan(&r.CheckCommand, &r.BudgetUSD, &r.BudgetMinutes)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectAccept{}, ErrNotFound
	}
	if err != nil {
		return ProjectAccept{}, fmt.Errorf("reading project %d's acceptance: %w", id, err)
	}
	return r, nil
}

// SetProjectAccept writes a project's acceptance defaults.
func (s *Store) SetProjectAccept(ctx context.Context, id int64, r ProjectAccept) error {
	result, err := s.db.ExecContext(ctx, `UPDATE projects SET check_command = ?, budget_usd = ?, budget_minutes = ? WHERE id = ?`,
		r.CheckCommand, r.BudgetUSD, r.BudgetMinutes, id)
	if err != nil {
		return fmt.Errorf("writing project %d's acceptance: %w", id, err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListJobsWaitingOn returns live jobs blocked until job id settles.
func (s *Store) ListJobsWaitingOn(ctx context.Context, id int64) ([]JobRecord, error) {
	return s.queryJobs(ctx, jobSelect+` WHERE wake_job = ? AND state = 'blocked' ORDER BY id`, id)
}

// ListChildJobs returns the jobs whose parent is id, oldest first.
func (s *Store) ListChildJobs(ctx context.Context, id int64) ([]JobRecord, error) {
	return s.queryJobs(ctx, jobSelect+` WHERE parent_id = ? ORDER BY id`, id)
}

// ListFixJobs returns the jobs crowsnest started to rebase job id's branch, oldest first.
func (s *Store) ListFixJobs(ctx context.Context, id int64) ([]JobRecord, error) {
	return s.queryJobs(ctx, jobSelect+` WHERE fix_of = ? ORDER BY id`, id)
}

// ListAcceptedJobs returns finished jobs whose evidence matters to Land (accept_state set) and
// that are not torn down, oldest first: the jobs whose evidence can go stale.
func (s *Store) ListAcceptedJobs(ctx context.Context, limit int) ([]JobRecord, error) {
	return s.queryJobs(ctx, jobSelect+` WHERE state = 'done' AND accept_state != '' AND torn_down_at = 0 AND land_state = ''
		ORDER BY id LIMIT ?`, limit)
}

// ListCheckingJobs returns jobs waiting on a check (accept_state checking), in any state: a
// finished job's re-check too, which the supervisor's live pages do not see.
func (s *Store) ListCheckingJobs(ctx context.Context, limit int) ([]JobRecord, error) {
	return s.queryJobs(ctx, jobSelect+` WHERE accept_state = 'checking' ORDER BY id LIMIT ?`, limit)
}

// MarkQuestionReceived records when the asker took an answer in. Only the first time counts.
func (s *Store) MarkQuestionReceived(ctx context.Context, id int64, at time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE questions SET received_at = ? WHERE id = ? AND received_at = 0 AND status != 'open'`,
		unixOrNil(at), id)
	if err != nil {
		return false, fmt.Errorf("marking question %d received: %w", id, err)
	}
	n, _ := result.RowsAffected()
	return n > 0, nil
}

// SetQuestionHeld records why the latest delivery attempt typed nothing ("" clears it).
func (s *Store) SetQuestionHeld(ctx context.Context, id int64, reason string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE questions SET held_reason = ? WHERE id = ?`, reason, id); err != nil {
		return fmt.Errorf("recording question %d's held delivery: %w", id, err)
	}
	return nil
}

// QuestionReceipt is what the Inbox shows about an answer's journey.
type QuestionReceipt struct {
	ReceivedAt time.Time
	HeldReason string
}

// GetQuestionReceipt reads a question's receipt columns.
func (s *Store) GetQuestionReceipt(ctx context.Context, id int64) (QuestionReceipt, error) {
	var received int64
	var r QuestionReceipt
	err := s.db.QueryRowContext(ctx, `SELECT received_at, held_reason FROM questions WHERE id = ?`, id).Scan(&received, &r.HeldReason)
	if errors.Is(err, sql.ErrNoRows) {
		return QuestionReceipt{}, ErrNotFound
	}
	if err != nil {
		return QuestionReceipt{}, fmt.Errorf("reading question %d's receipt: %w", id, err)
	}
	r.ReceivedAt = unixToTime(received)
	return r, nil
}

// DeliveredUnreceived lists typed answers of a job's questions that the asker has not been
// seen to take in yet (delivered, or uncertain), newest first: what reconciliation looks for in
// the transcript.
func (s *Store) DeliveredUnreceived(ctx context.Context, jobID string, limit int) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM questions WHERE job_id = ? AND status != 'open' AND delivery = 'type'
		AND received_at = 0 AND (delivered_at IS NOT NULL OR delivery_error != '') ORDER BY id DESC LIMIT ?`, jobID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing unreceived answers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ResetDelivery puts a typed answer whose delivery was uncertain or failed back in the delivery
// queue: the human's explicit "type it again" (never automatic). An answer seen received, or
// one delivered without doubt, is left alone.
func (s *Store) ResetDelivery(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `UPDATE questions SET delivery_claimed_at = NULL, delivered_at = NULL, delivery_error = '',
		held_reason = '' WHERE id = ? AND delivery = 'type' AND received_at = 0 AND delivery_error != ''`, id)
	if err != nil {
		return fmt.Errorf("resetting delivery of question %d: %w", id, err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
