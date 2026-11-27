// Schedules made in the app and the run history of every scheduled task (DESIGN.md 7.8 and
// 21.4, migration 0013). internal/schedule owns validation and the due rules; the store keeps
// rows. The on/off switch and the stamps stay in kv schedule.state for file and app tasks alike.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ScheduleRecord is one row of schedules.
type ScheduleRecord struct {
	ID        int64
	Name      string
	Every     string
	After     string
	RunAs     string // poke | job
	ProjectID int64
	BodyMD    string
	Dir       string
	Role      string
	Engine    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

const scheduleSelect = `SELECT id, name, every, after, run_as, project_id, body_md, dir, role, engine, created_at, updated_at FROM schedules`

func scanSchedule(row interface{ Scan(...any) error }) (ScheduleRecord, error) {
	var r ScheduleRecord
	var created, updated int64
	err := row.Scan(&r.ID, &r.Name, &r.Every, &r.After, &r.RunAs, &r.ProjectID, &r.BodyMD, &r.Dir, &r.Role, &r.Engine, &created, &updated)
	r.CreatedAt, r.UpdatedAt = unixToTime(created), unixToTime(updated)
	return r, err
}

// InsertSchedule stores a new schedule; a taken name is ErrConflict.
func (s *Store) InsertSchedule(ctx context.Context, r ScheduleRecord) (int64, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO schedules (name, every, after, run_as, project_id, body_md, dir, role, engine,
		created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Name, r.Every, r.After, r.RunAs, r.ProjectID, r.BodyMD, r.Dir, r.Role, r.Engine, unixSecondsOrZero(r.CreatedAt), unixSecondsOrZero(r.UpdatedAt))
	if err := conflictOr(err, "inserting schedule "+r.Name); err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// UpdateSchedule rewrites a schedule (every column but id, name and created_at).
func (s *Store) UpdateSchedule(ctx context.Context, r ScheduleRecord) error {
	result, err := s.db.ExecContext(ctx, `UPDATE schedules SET every = ?, after = ?, run_as = ?, project_id = ?, body_md = ?,
		dir = ?, role = ?, engine = ?, updated_at = ? WHERE id = ?`,
		r.Every, r.After, r.RunAs, r.ProjectID, r.BodyMD, r.Dir, r.Role, r.Engine, unixSecondsOrZero(r.UpdatedAt), r.ID)
	if err != nil {
		return fmt.Errorf("updating schedule %d: %w", r.ID, err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSchedule removes a schedule (its run history stays, keyed by name).
func (s *Store) DeleteSchedule(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM schedules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting schedule %d: %w", id, err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetScheduleByName returns one schedule.
func (s *Store) GetScheduleByName(ctx context.Context, name string) (ScheduleRecord, error) {
	r, err := scanSchedule(s.db.QueryRowContext(ctx, scheduleSelect+` WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return ScheduleRecord{}, ErrNotFound
	}
	if err != nil {
		return ScheduleRecord{}, fmt.Errorf("reading schedule %q: %w", name, err)
	}
	return r, nil
}

// ListSchedules returns every schedule by name.
func (s *Store) ListSchedules(ctx context.Context) ([]ScheduleRecord, error) {
	rows, err := s.db.QueryContext(ctx, scheduleSelect+` ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing schedules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ScheduleRecord
	for rows.Next() {
		r, err := scanSchedule(rows)
		if err != nil {
			return nil, fmt.Errorf("reading a schedule: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ScheduleRun is one row of schedule_runs.
type ScheduleRun struct {
	ID      int64
	Name    string
	At      time.Time
	Outcome string
	JobID   string
}

// AddScheduleRun records a run and keeps only the newest keep runs of that task.
func (s *Store) AddScheduleRun(ctx context.Context, run ScheduleRun, keep int) error {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO schedule_runs (name, at, outcome, job_id) VALUES (?, ?, ?, ?)`,
		run.Name, run.At.Unix(), run.Outcome, run.JobID); err != nil {
		return fmt.Errorf("recording a run of %s: %w", run.Name, err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM schedule_runs WHERE name = ? AND id NOT IN
		(SELECT id FROM schedule_runs WHERE name = ? ORDER BY id DESC LIMIT ?)`, run.Name, run.Name, keep); err != nil {
		return fmt.Errorf("pruning the runs of %s: %w", run.Name, err)
	}
	return nil
}

// ListScheduleRuns returns a task's runs, newest first, at most limit.
func (s *Store) ListScheduleRuns(ctx context.Context, name string, limit int) ([]ScheduleRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, at, outcome, job_id FROM schedule_runs WHERE name = ? ORDER BY id DESC LIMIT ?`, name, limit)
	if err != nil {
		return nil, fmt.Errorf("listing the runs of %s: %w", name, err)
	}
	defer func() { _ = rows.Close() }()
	var out []ScheduleRun
	for rows.Next() {
		var r ScheduleRun
		var at int64
		if err := rows.Scan(&r.ID, &r.Name, &at, &r.Outcome, &r.JobID); err != nil {
			return nil, fmt.Errorf("reading a run: %w", err)
		}
		r.At = unixToTime(at)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- Task file reviews (migration 0018, R5-S3) ---------------------------------------------

// TaskReviewApproved is the state of an approved task file.
const TaskReviewApproved = "approved"

// TaskReviewIsApproved reports whether the human approved the task file at path with this content
// hash.
func (s *Store) TaskReviewIsApproved(ctx context.Context, path, hash string) (bool, error) {
	var state string
	err := s.db.QueryRowContext(ctx, `SELECT state FROM task_reviews WHERE path = ? AND hash = ?`, path, hash).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading a task review: %w", err)
	}
	return state == TaskReviewApproved, nil
}

// ApproveTaskFile records the human's approval of the task file at path with this content hash.
// Earlier hashes of the same path are forgotten: only the content approved last may run, so
// putting back an older file asks again.
func (s *Store) ApproveTaskFile(ctx context.Context, path, hash string, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("approving a task file: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM task_reviews WHERE path = ?`, path); err != nil {
		return fmt.Errorf("approving a task file: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_reviews (path, hash, state, question_id, at) VALUES (?, ?, ?, 0, ?)`,
		path, hash, TaskReviewApproved, at.Unix()); err != nil {
		return fmt.Errorf("approving a task file: %w", err)
	}
	return tx.Commit()
}

// ClearTaskReviews forgets every task file approval. A restore calls it on the database it is
// about to install: an approval is the human's word about a file on this machine, and a backup
// from elsewhere (or a crafted one) must not arrive with its own task files pre-approved.
func (s *Store) ClearTaskReviews(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM task_reviews`); err != nil {
		return fmt.Errorf("clearing task reviews: %w", err)
	}
	return nil
}
