// Retention (DESIGN.md 4.19): the queries behind archiving settled jobs, the nightly sweeper and
// the System page's Storage card. Every sweep is a pair of statements, one that counts and one
// that deletes the same rows, so "clean up now" can show exactly what it would do first.
//
// What is never touched here: a job that is not settled, an open question or pending approval,
// and an attachment pinned as evidence (role `result` or `ask`: what an agent produced or showed
// when it asked). Deleting a settled job is its own step (DeleteJobs) that the caller takes only
// for jobs it has judged safe, or that the human confirmed one by one.

package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// settledStates are the job states retention may act on.
const settledStates = `('done', 'failed', 'cancelled')`

// ---- Archive ------------------------------------------------------------------

// ArchiveSettledJobs archives every settled job that settled before cutoff and is not archived
// yet. It returns how many it archived.
func (s *Store) ArchiveSettledJobs(ctx context.Context, cutoff, now time.Time) (int, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO job_archive (job_id, archived_at)
		SELECT id, ? FROM jobs WHERE state IN `+settledStates+` AND settled_at > 0 AND settled_at < ?
		AND id NOT IN (SELECT job_id FROM job_archive)`, now.Unix(), cutoff.Unix())
	if err != nil {
		return 0, fmt.Errorf("archiving settled jobs: %w", err)
	}
	count, _ := result.RowsAffected()
	return int(count), nil
}

// CountArchivable is how many jobs ArchiveSettledJobs would archive now.
func (s *Store) CountArchivable(ctx context.Context, cutoff time.Time) (int, error) {
	return s.count(ctx, `SELECT COUNT(*) FROM jobs WHERE state IN `+settledStates+` AND settled_at > 0 AND settled_at < ?
		AND id NOT IN (SELECT job_id FROM job_archive)`, cutoff.Unix())
}

// ArchivedJobIDs is the set of archived job ids (the board leaves them out).
func (s *Store) ArchivedJobIDs(ctx context.Context) (map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT job_id FROM job_archive`)
	if err != nil {
		return nil, fmt.Errorf("reading archived jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("reading archived jobs: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// SearchJobIDs finds jobs whose title, brief or result contains query (case-insensitive),
// newest first; archivedOnly limits it to archived ones. An empty query matches everything.
func (s *Store) SearchJobIDs(ctx context.Context, query string, archivedOnly bool, limit int) ([]int64, error) {
	sqlText := `SELECT id FROM jobs WHERE 1 = 1`
	var args []any
	if archivedOnly {
		sqlText += ` AND id IN (SELECT job_id FROM job_archive)`
	}
	if query = strings.TrimSpace(query); query != "" {
		pattern := "%" + escapeLike(strings.ToLower(query)) + "%"
		sqlText += ` AND (lower(title) LIKE ? ESCAPE '\' OR lower(brief_md) LIKE ? ESCAPE '\' OR lower(result_md) LIKE ? ESCAPE '\')`
		args = append(args, pattern, pattern, pattern)
	}
	sqlText += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("searching jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("searching jobs: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func escapeLike(text string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(text)
}

// ---- Deleting settled jobs ----------------------------------------------------

// SettledJob is what retention needs to judge whether a settled job may go.
type SettledJob struct {
	ID             int64
	Title          string
	State          string
	Isolate        bool
	WorktreeBranch string
	WorkDir        string
	SettledAt      time.Time
}

// SettledJobsBefore lists settled jobs that settled before cutoff, oldest first.
func (s *Store) SettledJobsBefore(ctx context.Context, cutoff time.Time, limit int) ([]SettledJob, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, state, isolate, worktree_branch, work_dir, settled_at FROM jobs
		WHERE state IN `+settledStates+` AND settled_at > 0 AND settled_at < ? ORDER BY settled_at, id LIMIT ?`, cutoff.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("listing settled jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SettledJob
	for rows.Next() {
		var job SettledJob
		var settled int64
		if err := rows.Scan(&job.ID, &job.Title, &job.State, &job.Isolate, &job.WorktreeBranch, &job.WorkDir, &settled); err != nil {
			return nil, fmt.Errorf("listing settled jobs: %w", err)
		}
		job.SettledAt = time.Unix(settled, 0)
		out = append(out, job)
	}
	return out, rows.Err()
}

// DeleteJobs deletes settled jobs with everything that belongs only to them: the timeline,
// settled questions and approvals, what the agent loaded, spend detail, the archive mark and
// attachment rows. A job that is not settled is skipped. It returns the jobs deleted and the
// attachment blobs no row refers to any more (the caller removes those files).
func (s *Store) DeleteJobs(ctx context.Context, ids []int64) (int, []string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("deleting jobs: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	deleted := 0
	var touched []string
	for _, id := range ids {
		ok, shas, err := deleteJob(ctx, tx, id)
		if err != nil {
			return 0, nil, err
		}
		if ok {
			deleted++
			touched = append(touched, shas...)
		}
	}
	orphans, err := orphanBlobs(ctx, tx, touched)
	if err != nil {
		return 0, nil, err
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, fmt.Errorf("deleting jobs: %w", err)
	}
	return deleted, orphans, nil
}

func deleteJob(ctx context.Context, tx *sql.Tx, id int64) (bool, []string, error) {
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM jobs WHERE id = ?`, id).Scan(&state); err != nil || !strings.Contains(settledStates, "'"+state+"'") {
		return false, nil, nil //nolint:nilerr // a missing or live job is skipped, not an error
	}
	ref := "j_" + strconv.FormatInt(id, 10)
	shas, err := attachmentShas(ctx, tx, `SELECT sha256 FROM attachments WHERE owner_kind = 'job' AND owner_id = ?`, ref)
	if err != nil {
		return false, nil, err
	}
	statements := []struct {
		sql string
		arg any
	}{
		{`DELETE FROM job_events WHERE job_id = ?`, id},
		{`DELETE FROM questions WHERE job_id = ? AND status != 'open'`, ref},
		{`DELETE FROM approvals WHERE job_id = ? AND status != 'pending'`, ref},
		{`DELETE FROM agent_layers WHERE subject = ?`, ref},
		{`DELETE FROM agent_instructions WHERE subject = ?`, ref},
		{`DELETE FROM spend_turns WHERE subject = ?`, ref},
		{`DELETE FROM attachments WHERE owner_kind = 'job' AND owner_id = ?`, ref},
		{`DELETE FROM job_archive WHERE job_id = ?`, id},
		{`DELETE FROM jobs WHERE id = ?`, id},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.sql, statement.arg); err != nil {
			return false, nil, fmt.Errorf("deleting job %s: %w", ref, err)
		}
	}
	return true, shas, nil
}

func attachmentShas(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing attachments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, err
		}
		out = append(out, sha)
	}
	return out, rows.Err()
}

// orphanBlobs keeps the shas no attachment row refers to any more.
func orphanBlobs(ctx context.Context, tx *sql.Tx, shas []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, sha := range shas {
		if seen[sha] {
			continue
		}
		seen[sha] = true
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM attachments WHERE sha256 = ?`, sha).Scan(&count); err != nil {
			return nil, fmt.Errorf("checking attachment %s: %w", sha, err)
		}
		if count == 0 {
			out = append(out, sha)
		}
	}
	return out, nil
}

// ---- Sweeps ---------------------------------------------------------------------

// Sweep is one kind of row the sweeper removes, with a count and a delete of the same rows.
type Sweep struct {
	Name  string // a stable key: questions, approvals, journal, spend_turns, action_tokens, attachments_pending, attachments_released, push_subscriptions
	Label string // what the Storage card calls it
	count string
	del   string
	args  []any
}

// Sweeps are the sweeps for a delete cutoff (settled history), a pending-upload cutoff and a
// dead-subscription cutoff.
func Sweeps(historyCutoff, pendingCutoff, deadPushCutoff, now time.Time) []Sweep {
	history := historyCutoff.Unix()
	where := map[string]string{
		"questions":            `FROM questions WHERE status != 'open' AND job_id = '' AND COALESCE(settled_at, created_at) < ?`,
		"approvals":            `FROM approvals WHERE status != 'pending' AND job_id = '' AND COALESCE(NULLIF(settled_at, 0), created_at) < ?`,
		"journal":              `FROM journal WHERE at < ?`,
		"spend_turns":          `FROM spend_turns WHERE at < ?`,
		"action_tokens":        `FROM action_tokens WHERE expires_at < ?`,
		"attachments_pending":  `FROM attachments WHERE owner_kind = '' AND created_at < ?`,
		"attachments_released": `FROM attachments WHERE released_at > 0 AND released_at < ? AND role NOT IN ('result', 'ask')`,
		"push_subscriptions":   `FROM push_subscriptions WHERE failures >= 5 AND last_success_at < ? AND created_at < ?`,
	}
	args := map[string][]any{
		"questions": {history}, "approvals": {history}, "journal": {history}, "spend_turns": {history},
		"action_tokens": {now.Add(-24 * time.Hour).Unix()}, "attachments_pending": {pendingCutoff.Unix()},
		"attachments_released": {history}, "push_subscriptions": {deadPushCutoff.Unix(), deadPushCutoff.Unix()},
	}
	labels := []struct{ name, label string }{
		{"questions", "Settled questions (not of a job)"}, {"approvals", "Settled approvals (not of a job)"},
		{"journal", "Journal entries"}, {"spend_turns", "Spend detail (per call)"},
		{"action_tokens", "Expired notification action tokens"}, {"attachments_pending", "Uploads never sent"},
		{"attachments_released", "Attachments no longer needed (evidence kept)"}, {"push_subscriptions", "Notification devices that stopped answering"},
	}
	out := make([]Sweep, 0, len(labels))
	for _, item := range labels {
		out = append(out, Sweep{Name: item.name, Label: item.label,
			count: `SELECT COUNT(*) ` + where[item.name], del: `DELETE ` + where[item.name], args: args[item.name]})
	}
	return out
}

// CountSweep is how many rows sweep would delete now.
func (s *Store) CountSweep(ctx context.Context, sweep Sweep) (int, error) {
	return s.count(ctx, sweep.count, sweep.args...)
}

// RunSweep deletes sweep's rows and returns how many, plus the attachment blobs no row refers
// to any more when it removed attachment rows.
func (s *Store) RunSweep(ctx context.Context, sweep Sweep) (int, []string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("sweeping %s: %w", sweep.Name, err)
	}
	defer func() { _ = tx.Rollback() }()
	var shas []string
	if strings.HasPrefix(sweep.Name, "attachments_") {
		if shas, err = attachmentShas(ctx, tx, `SELECT sha256 `+strings.TrimPrefix(sweep.count, `SELECT COUNT(*) `), sweep.args...); err != nil {
			return 0, nil, err
		}
	}
	result, err := tx.ExecContext(ctx, sweep.del, sweep.args...)
	if err != nil {
		return 0, nil, fmt.Errorf("sweeping %s: %w", sweep.Name, err)
	}
	orphans, err := orphanBlobs(ctx, tx, shas)
	if err != nil {
		return 0, nil, err
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, fmt.Errorf("sweeping %s: %w", sweep.Name, err)
	}
	count, _ := result.RowsAffected()
	return int(count), orphans, nil
}

// ---- Inventory ------------------------------------------------------------------

// TableUse is one table's row count and its oldest row's time (zero when empty or untimed).
type TableUse struct {
	Table  string    `json:"table"`
	Label  string    `json:"label"`
	Rows   int       `json:"rows"`
	Oldest time.Time `json:"oldest,omitzero"`
}

// inventoryTables pairs each table the Storage card lists with the column that dates its rows.
var inventoryTables = []struct{ table, label, at string }{
	{"jobs", "Jobs", "created_at"}, {"job_events", "Job timeline entries", "at"},
	{"job_archive", "Archived jobs", "archived_at"}, {"questions", "Questions", "created_at"},
	{"approvals", "Approvals", "created_at"}, {"journal", "Journal entries", "at"},
	{"spend_turns", "Spend detail (per call)", "at"}, {"attachments", "Attachments", "created_at"},
	{"push_subscriptions", "Notification devices", "created_at"}, {"auth_sessions", "Sign-in sessions", "created_at"},
}

// Inventory counts each listed table's rows and finds its oldest.
func (s *Store) Inventory(ctx context.Context) ([]TableUse, error) {
	out := make([]TableUse, 0, len(inventoryTables))
	for _, item := range inventoryTables {
		use := TableUse{Table: item.table, Label: item.label}
		var oldest sql.NullInt64
		err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), MIN(NULLIF(`+item.at+`, 0)) FROM `+item.table).Scan(&use.Rows, &oldest)
		if err != nil {
			return nil, fmt.Errorf("counting %s: %w", item.table, err)
		}
		if oldest.Valid {
			use.Oldest = time.Unix(oldest.Int64, 0).UTC()
		}
		out = append(out, use)
	}
	return out, nil
}

// AttachmentBytes is the total size of every attachment row's blob, counting a blob shared by
// rows once, and the oldest attachment's time.
func (s *Store) AttachmentBytes(ctx context.Context) (int64, error) {
	var total sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT SUM(size) FROM (SELECT sha256, MAX(size) AS size FROM attachments GROUP BY sha256)`).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("measuring attachments: %w", err)
	}
	return total.Int64, nil
}

// AttachmentShaExists reports whether any attachment row refers to the blob sha.
func (s *Store) AttachmentShaExists(ctx context.Context, sha string) (bool, error) {
	count, err := s.count(ctx, `SELECT COUNT(*) FROM attachments WHERE sha256 = ?`, sha)
	return count > 0, err
}

// JobExists reports whether job id exists (any state) and, when it does, whether it is settled.
func (s *Store) JobExists(ctx context.Context, id int64) (exists, settled bool, err error) {
	var state string
	err = s.db.QueryRowContext(ctx, `SELECT state FROM jobs WHERE id = ?`, id).Scan(&state)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("reading job %d: %w", id, err)
	}
	return true, strings.Contains(settledStates, "'"+state+"'"), nil
}

func (s *Store) count(ctx context.Context, query string, args ...any) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting: %w", err)
	}
	return count, nil
}
