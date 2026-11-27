// Queries for the start holds (slots), the review gate, the sweep of finished jobs whose
// worktrees are still around, and the daily digests (migration 0014). Kept beside jobs.go rather
// than in it so the jobs core stays readable.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SlotCount is how a job's start is weighed against the running-job limit.
type SlotCount struct {
	// Running counts job agents that hold a slot: live jobs (queued with a launch in flight,
	// running, needs you, blocked) whose own agent (job-N, never a standing agent) has a pane or
	// is being launched. Jobs in review are not counted: their agent sits idle while a reviewer
	// works, and counting them would let two reviewed jobs starve their own reviewers.
	Running int
	// Ahead counts older jobs waiting for a slot that have not been told to start anyway: they
	// go first, so a new job never overtakes the queue.
	Ahead int
}

// holdsSlot is the one definition of a job agent holding a slot, shared by the start check and
// the counts shown (R5-C06): a live job whose own agent (job-N) has a pane or is being launched,
// or a directory job whose launch is claimed but whose agent is not named yet (the name is set
// only once the pane exists, so without this a launch preparing its worktree held no slot and a
// second launch could take the same one).
const holdsSlot = `state IN ('queued', 'running', 'needs_you', 'blocked')
	AND ((agent_name LIKE 'job-%' AND (pane_id != '' OR lifecycle_op = 'launch'))
	  OR (agent_name = '' AND dir != '' AND lifecycle_op = 'launch'))`

// CountSlots weighs job id's start: in the whole instance when projectID is 0, else in that
// project only. The job itself is never counted.
func (s *Store) CountSlots(ctx context.Context, id, projectID int64) (SlotCount, error) {
	scope, args := "", []any{id}
	if projectID != 0 {
		scope, args = " AND project_id = ?", append(args, projectID)
	}
	var count SlotCount
	running := `SELECT COUNT(*) FROM jobs WHERE id != ? AND ` + holdsSlot + scope
	if err := s.db.QueryRowContext(ctx, running, args...).Scan(&count.Running); err != nil {
		return SlotCount{}, fmt.Errorf("counting running job agents: %w", err)
	}
	ahead := `SELECT COUNT(*) FROM jobs WHERE id < ? AND state = 'queued' AND dir != '' AND pane_id = ''
		AND lifecycle_op = '' AND cause != 'approval' AND start_anyway = 0` + scope
	if err := s.db.QueryRowContext(ctx, ahead, args...).Scan(&count.Ahead); err != nil {
		return SlotCount{}, fmt.Errorf("counting jobs waiting for a slot: %w", err)
	}
	return count, nil
}

// RunningAgentsByProject counts the job agents holding a slot, per project (0 Unfiled), for the
// System page and the queue line.
func (s *Store) RunningAgentsByProject(ctx context.Context) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project_id, COUNT(*) FROM jobs WHERE `+holdsSlot+` GROUP BY project_id`)
	if err != nil {
		return nil, fmt.Errorf("counting running job agents by project: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]int{}
	for rows.Next() {
		var project int64
		var count int
		if err := rows.Scan(&project, &count); err != nil {
			return nil, fmt.Errorf("reading running job agents: %w", err)
		}
		out[project] = count
	}
	return out, rows.Err()
}

// ListReviewJobs returns the reviewer jobs of job id, oldest first.
func (s *Store) ListReviewJobs(ctx context.Context, id int64) ([]JobRecord, error) {
	return s.queryJobs(ctx, jobSelect+" WHERE review_of = ? ORDER BY id", id)
}

// ListUnlandedWorktrees returns finished jobs (done, failed, cancelled) that ran in a worktree
// of their own and have not been torn down: the sweep's list, oldest first, at most limit.
func (s *Store) ListUnlandedWorktrees(ctx context.Context, limit int) ([]JobRecord, error) {
	return s.queryJobs(ctx, jobSelect+` WHERE state IN ('done', 'failed', 'cancelled') AND worktree_branch != ''
		AND torn_down_at = 0 AND review_of = 0 ORDER BY id LIMIT ?`, limit)
}

// ListLandRequests returns jobs holding an agent's land request (land_request set), oldest first:
// the reconciler's list (R5-C09). There are few: one request per job at a time.
func (s *Store) ListLandRequests(ctx context.Context, limit int) ([]JobRecord, error) {
	return s.queryJobs(ctx, jobSelect+` WHERE land_request != '' ORDER BY id LIMIT ?`, limit)
}

// FindLandQuestion returns the newest question whose context binds it to job jobID's land
// request with token, or ErrNotFound. It repairs a request whose question id was never stored.
func (s *Store) FindLandQuestion(ctx context.Context, jobID, token string) (QuestionRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+questionColumns+` FROM questions
		WHERE json_extract(context_json, '$.land_for') = ? AND json_extract(context_json, '$.land_token') = ?
		ORDER BY id DESC LIMIT 1`, jobID, token)
	q, err := scanQuestion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return QuestionRecord{}, ErrNotFound
	}
	return q, err
}

// JobsSettledBetween returns jobs that settled in [from, to), oldest first: the digest's facts.
func (s *Store) JobsSettledBetween(ctx context.Context, from, to time.Time) ([]JobRecord, error) {
	return s.queryJobs(ctx, jobSelect+` WHERE settled_at >= ? AND settled_at < ? ORDER BY settled_at, id LIMIT 500`,
		from.Unix(), to.Unix())
}

// JobsCreatedBetween returns jobs created in [from, to), oldest first.
func (s *Store) JobsCreatedBetween(ctx context.Context, from, to time.Time) ([]JobRecord, error) {
	return s.queryJobs(ctx, jobSelect+` WHERE created_at >= ? AND created_at < ? ORDER BY id LIMIT 500`,
		from.Unix(), to.Unix())
}

// ---- Digests -------------------------------------------------------------------------------

// DigestRecord is one row of digests.
type DigestRecord struct {
	ID         int64
	Day        string
	At         time.Time
	Source     string
	BodyMD     string
	QuestionID int64
}

// InsertDigest stores a digest and returns its id.
func (s *Store) InsertDigest(ctx context.Context, record DigestRecord) (int64, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO digests (day, at, source, body_md, question_id) VALUES (?, ?, ?, ?, ?)`,
		record.Day, record.At.Unix(), record.Source, record.BodyMD, record.QuestionID)
	if err != nil {
		return 0, fmt.Errorf("storing a digest: %w", err)
	}
	return result.LastInsertId()
}

// ListDigests returns digests newest first, at most limit.
func (s *Store) ListDigests(ctx context.Context, limit int) ([]DigestRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, day, at, source, body_md, question_id FROM digests ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing digests: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []DigestRecord
	for rows.Next() {
		var record DigestRecord
		var at int64
		if err := rows.Scan(&record.ID, &record.Day, &at, &record.Source, &record.BodyMD, &record.QuestionID); err != nil {
			return nil, fmt.Errorf("reading a digest: %w", err)
		}
		record.At = unixToTime(at)
		out = append(out, record)
	}
	return out, rows.Err()
}

// DigestForDay returns the newest digest for day; ok is false when there is none.
func (s *Store) DigestForDay(ctx context.Context, day string) (DigestRecord, bool, error) {
	var record DigestRecord
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT id, day, at, source, body_md, question_id FROM digests WHERE day = ? ORDER BY id DESC LIMIT 1`, day).
		Scan(&record.ID, &record.Day, &at, &record.Source, &record.BodyMD, &record.QuestionID)
	if err != nil {
		if isNoRows(err) {
			return DigestRecord{}, false, nil
		}
		return DigestRecord{}, false, fmt.Errorf("reading the digest for %s: %w", day, err)
	}
	record.At = unixToTime(at)
	return record, true, nil
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
