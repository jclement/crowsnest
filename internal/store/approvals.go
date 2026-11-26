// Approvals and action tokens (DESIGN.md 8.7, migration 0012). internal/approvals owns the
// lifecycle; the store keeps rows and makes every settle conditional on the row still pending,
// so a browser answer, a notification answer, the expiry sweep and a supersession race to one
// winner.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ApprovalRecord is one row of approvals.
type ApprovalRecord struct {
	ID           int64
	JobID        string
	PaneID       string
	AgentName    string
	SessionID    string
	Cwd          string
	ToolName     string
	ToolInput    string
	Suggestions  string
	Risk         string
	RiskReasons  string
	Status       string
	Hooked       bool
	DecidedVia   string
	Note         string
	StatusReason string
	CreatedAt    time.Time
	SettledAt    time.Time
	ExpiresAt    time.Time
}

const approvalColumns = `id, job_id, pane_id, agent_name, session_id, cwd, tool_name, tool_input, suggestions, risk, risk_reasons,
	status, hooked, decided_via, note, status_reason, created_at, settled_at, expires_at`

// InsertApproval stores a new pending approval and returns its id.
func (s *Store) InsertApproval(ctx context.Context, a ApprovalRecord) (int64, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO approvals (job_id, pane_id, agent_name, session_id, cwd, tool_name, tool_input,
		suggestions, risk, risk_reasons, status, hooked, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?)`,
		a.JobID, a.PaneID, a.AgentName, a.SessionID, a.Cwd, a.ToolName, a.ToolInput, a.Suggestions, a.Risk, a.RiskReasons,
		boolInt(a.Hooked), a.CreatedAt.Unix(), a.ExpiresAt.Unix())
	if err != nil {
		return 0, fmt.Errorf("inserting approval: %w", err)
	}
	return result.LastInsertId()
}

// GetApproval returns one approval.
func (s *Store) GetApproval(ctx context.Context, id int64) (ApprovalRecord, error) {
	return scanApproval(s.db.QueryRowContext(ctx, `SELECT `+approvalColumns+` FROM approvals WHERE id = ?`, id))
}

// ListApprovals returns approvals with one of statuses (all when empty), newest first.
func (s *Store) ListApprovals(ctx context.Context, statuses []string, limit int) ([]ApprovalRecord, error) {
	query := `SELECT ` + approvalColumns + ` FROM approvals`
	var args []any
	if len(statuses) > 0 {
		query += ` WHERE status IN (` + strings.TrimSuffix(strings.Repeat("?,", len(statuses)), ",") + `)`
		for _, status := range statuses {
			args = append(args, status)
		}
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing approvals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ApprovalRecord
	for rows.Next() {
		record, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// CountPendingApprovals counts pending approvals for a job ("" counts all).
func (s *Store) CountPendingApprovals(ctx context.Context, jobID string) (int, error) {
	var count int
	query, args := `SELECT COUNT(*) FROM approvals WHERE status = 'pending'`, []any{}
	if jobID != "" {
		query += ` AND job_id = ?`
		args = append(args, jobID)
	}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}

// ErrNotPending is returned by SettleApproval when the approval is no longer pending.
var ErrNotPending = errors.New("approval is not pending")

// SettleApproval moves a pending approval to status, recording how and why. It fails with
// ErrNotPending when another settle won, and ErrNotFound when there is no such approval.
func (s *Store) SettleApproval(ctx context.Context, id int64, status, via, note, reason string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE approvals SET status = ?, decided_via = ?, note = ?, status_reason = ?, settled_at = ?
		WHERE id = ? AND status = 'pending'`, status, via, note, reason, at.Unix(), id)
	if err != nil {
		return fmt.Errorf("settling approval %d: %w", id, err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 1 {
		return err
	}
	if _, err := s.GetApproval(ctx, id); err != nil {
		return err
	}
	return ErrNotPending
}

func scanApproval(row interface{ Scan(...any) error }) (ApprovalRecord, error) {
	var a ApprovalRecord
	var hooked int
	var created, settled, expires int64
	err := row.Scan(&a.ID, &a.JobID, &a.PaneID, &a.AgentName, &a.SessionID, &a.Cwd, &a.ToolName, &a.ToolInput, &a.Suggestions,
		&a.Risk, &a.RiskReasons, &a.Status, &hooked, &a.DecidedVia, &a.Note, &a.StatusReason, &created, &settled, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return ApprovalRecord{}, ErrNotFound
	}
	if err != nil {
		return ApprovalRecord{}, fmt.Errorf("reading approval: %w", err)
	}
	a.Hooked = hooked != 0
	a.CreatedAt = time.Unix(created, 0)
	if settled != 0 {
		a.SettledAt = time.Unix(settled, 0)
	}
	a.ExpiresAt = time.Unix(expires, 0)
	return a, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---- Action tokens -------------------------------------------------------------------------

// ActionTokenRecord is one row of action_tokens.
type ActionTokenRecord struct {
	Hash      string
	Item      string
	Actions   string // JSON array
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    time.Time
}

// InsertActionToken stores a minted token (by hash).
func (s *Store) InsertActionToken(ctx context.Context, t ActionTokenRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO action_tokens (hash, item, actions, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		t.Hash, t.Item, t.Actions, t.CreatedAt.Unix(), t.ExpiresAt.Unix())
	if err != nil {
		return fmt.Errorf("storing action token: %w", err)
	}
	return nil
}

// UseActionToken marks a token used and returns it, in one conditional UPDATE: a token is used at
// most once even when two requests race. It returns ErrNotFound for an unknown, used or expired
// token (deliberately one answer for all three).
func (s *Store) UseActionToken(ctx context.Context, hash string, now time.Time) (ActionTokenRecord, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE action_tokens SET used_at = ? WHERE hash = ? AND used_at = 0 AND expires_at > ?`,
		now.Unix(), hash, now.Unix())
	if err != nil {
		return ActionTokenRecord{}, fmt.Errorf("using action token: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return ActionTokenRecord{}, ErrNotFound
	}
	var t ActionTokenRecord
	var created, expires, used int64
	err = s.db.QueryRowContext(ctx, `SELECT hash, item, actions, created_at, expires_at, used_at FROM action_tokens WHERE hash = ?`, hash).
		Scan(&t.Hash, &t.Item, &t.Actions, &created, &expires, &used)
	if err != nil {
		return ActionTokenRecord{}, ErrNotFound
	}
	t.CreatedAt, t.ExpiresAt, t.UsedAt = time.Unix(created, 0), time.Unix(expires, 0), time.Unix(used, 0)
	return t, nil
}

// DeleteActionTokensBefore removes tokens that expired before t.
func (s *Store) DeleteActionTokensBefore(ctx context.Context, t time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM action_tokens WHERE expires_at < ?`, t.Unix())
	return err
}
