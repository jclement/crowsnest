// Viewed marks on a diff's files (migration 0019, DESIGN.md 6.20).

package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// DiffMark is one reviewer's Viewed tick on one file of a subject's diff.
type DiffMark struct {
	Subject  string
	Reviewer string
	Scope    string
	Path     string
	Hash     string
	OldBlob  string
	NewBlob  string
	ViewedAt time.Time
}

// PutDiffMarks records marks, replacing any for the same subject, reviewer, scope and path.
func (s *Store) PutDiffMarks(ctx context.Context, marks []DiffMark) error {
	if len(marks) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, mark := range marks {
		if mark.Reviewer == "" {
			mark.Reviewer = "you"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO diff_marks (subject, reviewer, scope, path, hash, old_blob, new_blob, viewed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (subject, reviewer, scope, path) DO UPDATE SET hash = excluded.hash, old_blob = excluded.old_blob,
				new_blob = excluded.new_blob, viewed_at = excluded.viewed_at`,
			mark.Subject, mark.Reviewer, mark.Scope, mark.Path, mark.Hash, mark.OldBlob, mark.NewBlob, mark.ViewedAt.Unix()); err != nil {
			return fmt.Errorf("saving a viewed mark: %w", err)
		}
	}
	return tx.Commit()
}

// ListDiffMarks is every mark on subject (all reviewers and scopes).
func (s *Store) ListDiffMarks(ctx context.Context, subject string) ([]DiffMark, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT subject, reviewer, scope, path, hash, old_blob, new_blob, viewed_at
		FROM diff_marks WHERE subject = ? ORDER BY path`, subject)
	if err != nil {
		return nil, fmt.Errorf("reading viewed marks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	marks := []DiffMark{}
	for rows.Next() {
		var mark DiffMark
		var at int64
		if err := rows.Scan(&mark.Subject, &mark.Reviewer, &mark.Scope, &mark.Path, &mark.Hash, &mark.OldBlob, &mark.NewBlob, &at); err != nil {
			return nil, err
		}
		mark.ViewedAt = time.Unix(at, 0).UTC()
		marks = append(marks, mark)
	}
	return marks, rows.Err()
}

// DeleteDiffMarks removes reviewer's marks on subject in scope for paths (every path when paths
// is empty), and reports how many went.
func (s *Store) DeleteDiffMarks(ctx context.Context, subject, reviewer, scope string, paths []string) (int64, error) {
	if reviewer == "" {
		reviewer = "you"
	}
	query, args := `DELETE FROM diff_marks WHERE subject = ? AND reviewer = ? AND scope = ?`, []any{subject, reviewer, scope}
	if len(paths) > 0 {
		query += ` AND path IN (?` + strings.Repeat(",?", len(paths)-1) + `)`
		for _, path := range paths {
			args = append(args, path)
		}
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("clearing viewed marks: %w", err)
	}
	return result.RowsAffected()
}

// DeleteSubjectMarks removes every mark on subject (a job that landed, was discarded or deleted).
func (s *Store) DeleteSubjectMarks(ctx context.Context, subject string) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM diff_marks WHERE subject = ?`, subject)
	if err != nil {
		return 0, fmt.Errorf("removing a subject's viewed marks: %w", err)
	}
	return result.RowsAffected()
}

// DiffMarkSubjects lists the subjects that have marks.
func (s *Store) DiffMarkSubjects(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT subject FROM diff_marks ORDER BY subject`)
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
