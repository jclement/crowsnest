// Question rows (DESIGN.md section 11). The store keeps them as typed records with the JSON
// columns as raw bytes; internal/questions owns the shapes inside them, validation and the
// lifecycle. Every state change is a conditional UPDATE on the current status, so two settlers
// racing (an answer and the expiry sweep) cannot both win.

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// QuestionRecord is one row of questions.
type QuestionRecord struct {
	ID          int64
	Title       string
	BodyMD      string
	Kind        string
	Multi       bool
	Options     json.RawMessage
	Recommend   string
	Context     json.RawMessage
	Urgency     string
	Files       json.RawMessage
	Status      string
	AnswerValue json.RawMessage // nil until answered
	AnswerNote  string
	SourcePane  string
	JobID       string
	Wait        bool
	CreatedAt   time.Time
	ExpiresAt   time.Time
	SettledAt   time.Time // zero while open

	Delivery          string
	DeliveryClaimedAt time.Time // zero when unclaimed
	DeliveredAt       time.Time // zero until delivered
	DeliveryError     string
}

// ErrNotOpen is returned when a settle is attempted on a question that is no longer open.
var ErrNotOpen = errors.New("question is not open")

// questionColumns is the SELECT list matching scanQuestion.
const questionColumns = `id, title, body_md, kind, multi, options_json, recommend, context_json, urgency,
	files_json, status, answer_value_json, answer_note, source_pane, job_id, wait, created_at, expires_at,
	settled_at, delivery, delivery_claimed_at, delivered_at, delivery_error`

// InsertQuestion stores a new open question and returns its id.
func (s *Store) InsertQuestion(ctx context.Context, q QuestionRecord) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO questions (title, body_md, kind, multi, options_json, recommend, context_json, urgency,
		  files_json, status, source_pane, job_id, wait, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'open', ?, ?, ?, ?, ?)`,
		q.Title, q.BodyMD, q.Kind, q.Multi, rawOr(q.Options, "[]"), q.Recommend, rawOr(q.Context, "{}"), q.Urgency,
		rawOr(q.Files, "[]"), q.SourcePane, q.JobID, q.Wait, q.CreatedAt.Unix(), q.ExpiresAt.Unix())
	if err != nil {
		return 0, fmt.Errorf("inserting question: %w", err)
	}
	return result.LastInsertId()
}

// GetQuestion returns one question, or ErrNotFound.
func (s *Store) GetQuestion(ctx context.Context, id int64) (QuestionRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+questionColumns+` FROM questions WHERE id = ?`, id)
	q, err := scanQuestion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return QuestionRecord{}, ErrNotFound
	}
	return q, err
}

// ListQuestions returns questions with one of statuses (all when empty), open ones first and
// then newest first, at most limit rows.
func (s *Store) ListQuestions(ctx context.Context, statuses []string, limit int) ([]QuestionRecord, error) {
	query := `SELECT ` + questionColumns + ` FROM questions`
	args := make([]any, 0, len(statuses)+1)
	if len(statuses) > 0 {
		query += ` WHERE status IN (?` + strings.Repeat(", ?", len(statuses)-1) + `)`
		for _, status := range statuses {
			args = append(args, status)
		}
	}
	query += ` ORDER BY status = 'open' DESC, COALESCE(settled_at, created_at) DESC, id DESC LIMIT ?`
	args = append(args, limit)
	return s.queryQuestions(ctx, query, args...)
}

// CountOpenQuestions returns how many questions are open, and how many of those are blocking.
func (s *Store) CountOpenQuestions(ctx context.Context) (open, blocking int, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(urgency = 'blocking'), 0) FROM questions WHERE status = 'open'`).Scan(&open, &blocking)
	if err != nil {
		return 0, 0, fmt.Errorf("counting open questions: %w", err)
	}
	return open, blocking, nil
}

// SettleQuestion moves an open question to status (answered, expired or cancelled) with the
// answer, and records how its outcome will reach the asker. It returns ErrNotOpen when the
// question was already settled (or ErrNotFound when it does not exist).
func (s *Store) SettleQuestion(ctx context.Context, id int64, status string, value json.RawMessage, note, delivery string, at time.Time) error {
	var valueArg any
	if value != nil {
		valueArg = string(value)
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE questions SET status = ?, answer_value_json = ?, answer_note = ?, settled_at = ?, delivery = ?
		 WHERE id = ? AND status = 'open'`, status, valueArg, note, at.Unix(), delivery, id)
	if err != nil {
		return fmt.Errorf("settling question %d: %w", id, err)
	}
	return s.oneRowOrState(ctx, result, id)
}

// ExpiredOpenQuestions returns the ids of open questions whose expiry is at or before now.
func (s *Store) ExpiredOpenQuestions(ctx context.Context, now time.Time) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM questions WHERE status = 'open' AND expires_at <= ?`, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("finding expired questions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("reading expired question id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ---- Delivery ----------------------------------------------------------------------------

// PendingDeliveries returns settled questions whose outcome is to be typed into the asker's pane
// and which nobody has claimed yet.
func (s *Store) PendingDeliveries(ctx context.Context) ([]QuestionRecord, error) {
	return s.queryQuestions(ctx, `SELECT `+questionColumns+` FROM questions
		WHERE delivery = 'type' AND delivery_claimed_at IS NULL AND delivered_at IS NULL ORDER BY settled_at, id`)
}

// ClaimDelivery sets the write-ahead mark on a pending delivery. It reports false when another
// worker (or an earlier run) already claimed it.
func (s *Store) ClaimDelivery(ctx context.Context, id int64, at time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE questions SET delivery_claimed_at = ? WHERE id = ? AND delivery = 'type' AND delivery_claimed_at IS NULL AND delivered_at IS NULL`,
		at.Unix(), id)
	if err != nil {
		return false, fmt.Errorf("claiming delivery of question %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

// ReleaseDelivery removes the claim after an attempt that provably typed nothing (the agent was
// at a dialog, or not found), so a later attempt may try again.
func (s *Store) ReleaseDelivery(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE questions SET delivery_claimed_at = NULL WHERE id = ? AND delivered_at IS NULL`, id); err != nil {
		return fmt.Errorf("releasing delivery of question %d: %w", id, err)
	}
	return nil
}

// FinishDelivery records the end of a claimed delivery: delivered (errText empty) or given up
// with the reason. Either way it is never attempted again.
func (s *Store) FinishDelivery(ctx context.Context, id int64, at time.Time, errText string) error {
	var delivered any
	if errText == "" {
		delivered = at.Unix()
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE questions SET delivered_at = ?, delivery_error = ?, delivery_claimed_at = COALESCE(delivery_claimed_at, ?) WHERE id = ?`,
		delivered, errText, at.Unix(), id); err != nil {
		return fmt.Errorf("finishing delivery of question %d: %w", id, err)
	}
	return nil
}

// AbandonInterruptedDeliveries marks deliveries that were claimed but never finished (the process
// died between the mark and the result) as failed, and returns how many. They are not retried:
// the text may already have been typed, and typing it twice is worse than once.
func (s *Store) AbandonInterruptedDeliveries(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE questions SET delivery_error = 'interrupted while typing; not retried (at most once)'
		 WHERE delivery_claimed_at IS NOT NULL AND delivered_at IS NULL AND delivery_error = ''`)
	if err != nil {
		return 0, fmt.Errorf("abandoning interrupted deliveries: %w", err)
	}
	return result.RowsAffected()
}

// ---- Helpers -----------------------------------------------------------------------------

func (s *Store) queryQuestions(ctx context.Context, query string, args ...any) ([]QuestionRecord, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing questions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []QuestionRecord
	for rows.Next() {
		q, err := scanQuestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// rowScanner is the part of *sql.Row and *sql.Rows scanQuestion needs.
type rowScanner interface{ Scan(dest ...any) error }

func scanQuestion(row rowScanner) (QuestionRecord, error) {
	var q QuestionRecord
	var options, context, files string
	var answer sql.NullString
	var created, expires int64
	var settled, claimed, delivered sql.NullInt64
	err := row.Scan(&q.ID, &q.Title, &q.BodyMD, &q.Kind, &q.Multi, &options, &q.Recommend, &context, &q.Urgency,
		&files, &q.Status, &answer, &q.AnswerNote, &q.SourcePane, &q.JobID, &q.Wait, &created, &expires,
		&settled, &q.Delivery, &claimed, &delivered, &q.DeliveryError)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return QuestionRecord{}, err
		}
		return QuestionRecord{}, fmt.Errorf("reading question: %w", err)
	}
	q.Options, q.Context, q.Files = json.RawMessage(options), json.RawMessage(context), json.RawMessage(files)
	if answer.Valid {
		q.AnswerValue = json.RawMessage(answer.String)
	}
	q.CreatedAt, q.ExpiresAt = time.Unix(created, 0), time.Unix(expires, 0)
	q.SettledAt, q.DeliveryClaimedAt, q.DeliveredAt = unixOrZero(settled), unixOrZero(claimed), unixOrZero(delivered)
	return q, nil
}

// oneRowOrState turns a conditional update that touched nothing into ErrNotFound or ErrNotOpen.
func (s *Store) oneRowOrState(ctx context.Context, result sql.Result, id int64) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 1 {
		return nil
	}
	if _, err := s.GetQuestion(ctx, id); err != nil {
		return err
	}
	return ErrNotOpen
}

func unixOrZero(value sql.NullInt64) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return time.Unix(value.Int64, 0)
}

func rawOr(raw json.RawMessage, fallback string) string {
	if len(raw) == 0 {
		return fallback
	}
	return string(raw)
}
