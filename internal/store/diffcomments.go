// Line comments on a diff (migration 0022, DESIGN.md 6.21). The rows only: what a comment's
// status is (draft, sent, outdated) is worked out by internal/changes against a fresh reading of
// the checkout, because "outdated" depends on what the file holds now.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	// Version counts edits of Body; SentVersion is the version last delivered (migration 0023,
	// R9-C5): the note is due while SentVersion < Version.
	Version     int64
	SentVersion int64
	// Claim is the send holding the note now ("" none), ClaimVersion the version it took,
	// ClaimedAt when. UncertainAt marks a note whose send may or may not have arrived.
	Claim        string
	ClaimVersion int64
	ClaimedAt    time.Time
	UncertainAt  time.Time
}

// Due reports whether the note goes with the next send: not dismissed, not held by a send, and
// edited (or never sent) since it was last delivered.
func (c DiffComment) Due() bool {
	return c.DismissedAt.IsZero() && c.Claim == "" && c.SentVersion < c.Version
}

const diffCommentColumns = `id, subject, author, scope, path, side, line_start, line_end, quote, body, hash, old_blob, new_blob,
	created_at, updated_at, sent_at, dismissed_at, version, sent_version, claim, claim_version, claimed_at, uncertain_at`

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
	comment.Version, comment.SentVersion = 1, 0 // the column defaults
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

// UpdateDiffCommentBody changes a comment's note and bumps its version, so a send holding the
// old text does not mark the new one sent (ErrNotFound when it is not subject's).
func (s *Store) UpdateDiffCommentBody(ctx context.Context, subject string, id int64, body string, at time.Time) error {
	return s.changeDiffComment(ctx, `UPDATE diff_comments SET body = ?, updated_at = ?, version = version + 1 WHERE subject = ? AND id = ?`,
		body, at.Unix(), subject, id)
}

// ResendDiffComment makes a note due again (the human's Re-send). A note held by a send is refused.
func (s *Store) ResendDiffComment(ctx context.Context, subject string, id int64) error {
	return s.changeDiffComment(ctx, `UPDATE diff_comments SET sent_at = 0, sent_version = 0 WHERE subject = ? AND id = ? AND claim = ''`, subject, id)
}

// ClaimedComment is one note a send took: its id and the version whose text it carries.
type ClaimedComment struct {
	ID      int64
	Version int64
}

// ErrClaimConflict means a note changed, or another send took it, between reading and claiming.
var ErrClaimConflict = errors.New("the comments changed while they were being sent")

// ClaimDiffComments takes notes of subject for send claim, in one transaction (R9-C5): each must
// still be at the version read, due and unclaimed, or nothing is taken (ErrClaimConflict).
func (s *Store) ClaimDiffComments(ctx context.Context, subject, claim string, notes []ClaimedComment, at time.Time) error {
	if len(notes) == 0 || claim == "" {
		return ErrClaimConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("claiming diff comments: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, note := range notes {
		result, err := tx.ExecContext(ctx, `UPDATE diff_comments SET claim = ?, claim_version = version, claimed_at = ?
			WHERE subject = ? AND id = ? AND version = ? AND claim = '' AND dismissed_at = 0 AND sent_version < version`,
			claim, at.Unix(), subject, note.ID, note.Version)
		if err != nil {
			return fmt.Errorf("claiming diff comment %d: %w", note.ID, err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			return ErrClaimConflict
		}
	}
	return tx.Commit()
}

// SettleDiffCommentClaim ends send claim's hold on its notes. delivered marks each sent at the
// version the send took (an edit made since stays due); otherwise they are due again, with
// uncertain marking ones whose send may have arrived. It returns the subject and how many notes
// it settled ("" and 0 when the claim holds none).
func (s *Store) SettleDiffCommentClaim(ctx context.Context, claim string, delivered, uncertain bool, at time.Time) (string, int, error) {
	var subject string
	err := s.db.QueryRowContext(ctx, `SELECT subject FROM diff_comments WHERE claim = ? LIMIT 1`, claim).Scan(&subject)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, fmt.Errorf("reading a comment send: %w", err)
	}
	var result sql.Result
	switch {
	case delivered:
		result, err = s.db.ExecContext(ctx, `UPDATE diff_comments SET sent_at = ?, sent_version = claim_version, uncertain_at = 0, claim = '',
			claim_version = 0, claimed_at = 0 WHERE claim = ?`, at.Unix(), claim)
	case uncertain:
		result, err = s.db.ExecContext(ctx, `UPDATE diff_comments SET uncertain_at = ?, claim = '', claim_version = 0, claimed_at = 0 WHERE claim = ?`,
			at.Unix(), claim)
	default:
		result, err = s.db.ExecContext(ctx, `UPDATE diff_comments SET claim = '', claim_version = 0, claimed_at = 0 WHERE claim = ?`, claim)
	}
	if err != nil {
		return "", 0, fmt.Errorf("settling a comment send: %w", err)
	}
	n, _ := result.RowsAffected()
	return subject, int(n), nil
}

// CommentClaim is one send holding notes: its claim, subject and when it took them.
type CommentClaim struct {
	Claim     string
	Subject   string
	ClaimedAt time.Time
}

// DiffCommentClaims lists the sends holding notes now, oldest first.
func (s *Store) DiffCommentClaims(ctx context.Context) ([]CommentClaim, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT claim, subject, MIN(claimed_at) FROM diff_comments WHERE claim != '' GROUP BY claim, subject ORDER BY 3`)
	if err != nil {
		return nil, fmt.Errorf("reading comment sends: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []CommentClaim{}
	for rows.Next() {
		var claim CommentClaim
		var at int64
		if err := rows.Scan(&claim.Claim, &claim.Subject, &at); err != nil {
			return nil, err
		}
		claim.ClaimedAt = time.Unix(at, 0).UTC()
		out = append(out, claim)
	}
	return out, rows.Err()
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
	var created, updated, sent, dismissed, claimed, uncertain int64
	if err := row.Scan(&comment.ID, &comment.Subject, &comment.Author, &comment.Scope, &comment.Path, &comment.Side, &comment.LineStart, &comment.LineEnd,
		&comment.Quote, &comment.Body, &comment.Hash, &comment.OldBlob, &comment.NewBlob, &created, &updated, &sent, &dismissed,
		&comment.Version, &comment.SentVersion, &comment.Claim, &comment.ClaimVersion, &claimed, &uncertain); err != nil {
		return DiffComment{}, err
	}
	if claimed > 0 {
		comment.ClaimedAt = time.Unix(claimed, 0).UTC()
	}
	if uncertain > 0 {
		comment.UncertainAt = time.Unix(uncertain, 0).UTC()
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
