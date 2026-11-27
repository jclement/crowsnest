// Line comments on a diff (migration 0022, DESIGN.md 6.21). The rows only: what a comment's
// status is (draft, sent, outdated) is worked out by internal/changes against a fresh reading of
// the checkout, because "outdated" depends on what the file holds now.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DiffComment is one note on a subject's diff.
type DiffComment struct {
	ID      int64
	Subject string
	Author  string
	Scope   string
	Path    string
	// Side is old or new (LineStart..LineEnd on that side), or file (the whole file, lines 0).
	Side      string
	LineStart int
	LineEnd   int
	Quote     string
	Body      string
	Hash      string
	OldBlob   string
	NewBlob   string
	CreatedAt time.Time
	UpdatedAt time.Time
	// SentAt is zero for a draft; DismissedAt zero unless the human dismissed it.
	SentAt      time.Time
	DismissedAt time.Time
}

const diffCommentColumns = `id, subject, author, scope, path, side, line_start, line_end, quote, body, hash, old_blob, new_blob,
	created_at, updated_at, sent_at, dismissed_at`

// InsertDiffComment saves a new comment and returns it with its id.
func (s *Store) InsertDiffComment(ctx context.Context, comment DiffComment) (DiffComment, error) {
	if comment.Author == "" {
		comment.Author = "you"
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO diff_comments (subject, author, scope, path, side, line_start, line_end, quote, body,
		hash, old_blob, new_blob, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		comment.Subject, comment.Author, comment.Scope, comment.Path, comment.Side, comment.LineStart, comment.LineEnd, comment.Quote, comment.Body,
		comment.Hash, comment.OldBlob, comment.NewBlob, comment.CreatedAt.Unix(), comment.UpdatedAt.Unix())
	if err != nil {
		return DiffComment{}, fmt.Errorf("saving a diff comment: %w", err)
	}
	comment.ID, err = result.LastInsertId()
	return comment, err
}

// GetDiffComment reads one comment of subject (ErrNotFound when it is not that subject's).
func (s *Store) GetDiffComment(ctx context.Context, subject string, id int64) (DiffComment, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+diffCommentColumns+` FROM diff_comments WHERE subject = ? AND id = ?`, subject, id)
	comment, err := scanDiffComment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DiffComment{}, ErrNotFound
	}
	return comment, err
}

// ListDiffComments is every comment on subject, dismissed ones included, oldest first.
func (s *Store) ListDiffComments(ctx context.Context, subject string) ([]DiffComment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+diffCommentColumns+` FROM diff_comments WHERE subject = ? ORDER BY id`, subject)
	if err != nil {
		return nil, fmt.Errorf("reading diff comments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	comments := []DiffComment{}
	for rows.Next() {
		comment, err := scanDiffComment(rows)
		if err != nil {
			return nil, err
		}
		comments = append(comments, comment)
	}
	return comments, rows.Err()
}

// UpdateDiffCommentBody changes a comment's note (ErrNotFound when it is not subject's).
func (s *Store) UpdateDiffCommentBody(ctx context.Context, subject string, id int64, body string, at time.Time) error {
	return s.changeDiffComment(ctx, `UPDATE diff_comments SET body = ?, updated_at = ? WHERE subject = ? AND id = ?`, body, at.Unix(), subject, id)
}

// SetDiffCommentsSent marks comments sent at at (zero puts them back to drafts). Ids that are not
// subject's are ignored.
func (s *Store) SetDiffCommentsSent(ctx context.Context, subject string, ids []int64, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	sentAt := int64(0)
	if !at.IsZero() {
		sentAt = at.Unix()
	}
	query := `UPDATE diff_comments SET sent_at = ? WHERE subject = ? AND id IN (?` + strings.Repeat(",?", len(ids)-1) + `)` //nolint:gosec // placeholders, not values
	args := []any{sentAt, subject}
	for _, id := range ids {
		args = append(args, id)
	}
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("marking diff comments sent: %w", err)
	}
	return nil
}

// DismissDiffComment hides a comment for good (ErrNotFound when it is not subject's).
func (s *Store) DismissDiffComment(ctx context.Context, subject string, id int64, at time.Time) error {
	return s.changeDiffComment(ctx, `UPDATE diff_comments SET dismissed_at = ? WHERE subject = ? AND id = ?`, at.Unix(), subject, id)
}

// DeleteDiffComment removes one comment (ErrNotFound when it is not subject's).
func (s *Store) DeleteDiffComment(ctx context.Context, subject string, id int64) error {
	return s.changeDiffComment(ctx, `DELETE FROM diff_comments WHERE subject = ? AND id = ?`, subject, id)
}

func (s *Store) changeDiffComment(ctx context.Context, query string, args ...any) error {
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("changing a diff comment: %w", err)
	}
	if n, err := result.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSubjectComments removes every comment on subject (a job that landed, was discarded or
// deleted).
func (s *Store) DeleteSubjectComments(ctx context.Context, subject string) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM diff_comments WHERE subject = ?`, subject)
	if err != nil {
		return 0, fmt.Errorf("removing a subject's diff comments: %w", err)
	}
	return result.RowsAffected()
}

// DiffCommentSubjects lists the subjects that have comments.
func (s *Store) DiffCommentSubjects(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT subject FROM diff_comments ORDER BY subject`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	subjects := []string{}
	for rows.Next() {
		var subject string
		if err := rows.Scan(&subject); err != nil {
			return nil, err
		}
		subjects = append(subjects, subject)
	}
	return subjects, rows.Err()
}

func scanDiffComment(row rowScanner) (DiffComment, error) {
	var comment DiffComment
	var created, updated, sent, dismissed int64
	if err := row.Scan(&comment.ID, &comment.Subject, &comment.Author, &comment.Scope, &comment.Path, &comment.Side, &comment.LineStart, &comment.LineEnd,
		&comment.Quote, &comment.Body, &comment.Hash, &comment.OldBlob, &comment.NewBlob, &created, &updated, &sent, &dismissed); err != nil {
		return DiffComment{}, err
	}
	comment.CreatedAt, comment.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	if sent > 0 {
		comment.SentAt = time.Unix(sent, 0).UTC()
	}
	if dismissed > 0 {
		comment.DismissedAt = time.Unix(dismissed, 0).UTC()
	}
	return comment, nil
}
