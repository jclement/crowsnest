// Approval rules (DESIGN.md 8.7 "Policy", migration 0016): the project rules the human keeps and
// the job rules remembered from an approval. internal/approvals validates and evaluates them;
// the store keeps rows. A rule is revoked, never deleted, so an approval it allowed can still
// name it.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ApprovalRuleRecord is one row of approval_rules.
type ApprovalRuleRecord struct {
	ID         int64
	Scope      string // project | job
	ProjectID  int64
	JobID      string
	Kind       string // bash | edit | tool
	Pattern    string
	MaxRisk    string
	Note       string
	CreatedAt  time.Time
	CreatedVia string
	RevokedAt  time.Time // zero while it applies
}

// ApprovalRuleFilter selects rules. Zero fields select everything; Live leaves out revoked ones.
type ApprovalRuleFilter struct {
	Scope     string
	ProjectID int64
	JobID     string
	Live      bool
}

const approvalRuleColumns = `id, scope, project_id, job_id, kind, pattern, max_risk, note, created_at, created_via, revoked_at`

// InsertApprovalRule stores a rule and returns its id.
func (s *Store) InsertApprovalRule(ctx context.Context, r ApprovalRuleRecord) (int64, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO approval_rules (scope, project_id, job_id, kind, pattern, max_risk, note, created_at, created_via)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.Scope, r.ProjectID, r.JobID, r.Kind, r.Pattern, r.MaxRisk, r.Note, r.CreatedAt.Unix(), r.CreatedVia)
	if err != nil {
		return 0, fmt.Errorf("inserting approval rule: %w", err)
	}
	return result.LastInsertId()
}

// GetApprovalRule returns one rule.
func (s *Store) GetApprovalRule(ctx context.Context, id int64) (ApprovalRuleRecord, error) {
	return scanApprovalRule(s.db.QueryRowContext(ctx, `SELECT `+approvalRuleColumns+` FROM approval_rules WHERE id = ?`, id))
}

// ListApprovalRules returns the rules the filter selects, oldest first (the order they were added).
func (s *Store) ListApprovalRules(ctx context.Context, filter ApprovalRuleFilter) ([]ApprovalRuleRecord, error) {
	query := `SELECT ` + approvalRuleColumns + ` FROM approval_rules WHERE 1 = 1`
	var args []any
	if filter.Scope != "" {
		query += ` AND scope = ?`
		args = append(args, filter.Scope)
	}
	if filter.ProjectID != 0 {
		query += ` AND project_id = ?`
		args = append(args, filter.ProjectID)
	}
	if filter.JobID != "" {
		query += ` AND job_id = ?`
		args = append(args, filter.JobID)
	}
	if filter.Live {
		query += ` AND revoked_at = 0`
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY id LIMIT 500`, args...)
	if err != nil {
		return nil, fmt.Errorf("listing approval rules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ApprovalRuleRecord
	for rows.Next() {
		record, err := scanApprovalRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// RevokeApprovalRule marks a live rule revoked. It returns ErrNotFound when there is no such rule
// and ErrNotPending (reused: "no longer in force") when it was already revoked.
func (s *Store) RevokeApprovalRule(ctx context.Context, id int64, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE approval_rules SET revoked_at = ? WHERE id = ? AND revoked_at = 0`, at.Unix(), id)
	if err != nil {
		return fmt.Errorf("revoking approval rule %d: %w", id, err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 1 {
		return err
	}
	if _, err := s.GetApprovalRule(ctx, id); err != nil {
		return err
	}
	return ErrNotPending
}

func scanApprovalRule(row interface{ Scan(...any) error }) (ApprovalRuleRecord, error) {
	var r ApprovalRuleRecord
	var created, revoked int64
	err := row.Scan(&r.ID, &r.Scope, &r.ProjectID, &r.JobID, &r.Kind, &r.Pattern, &r.MaxRisk, &r.Note, &created, &r.CreatedVia, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return ApprovalRuleRecord{}, ErrNotFound
	}
	if err != nil {
		return ApprovalRuleRecord{}, fmt.Errorf("reading approval rule: %w", err)
	}
	r.CreatedAt = time.Unix(created, 0)
	if revoked != 0 {
		r.RevokedAt = time.Unix(revoked, 0)
	}
	return r, nil
}
