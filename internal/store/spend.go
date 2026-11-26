// Spend rows (DESIGN.md 7.11, migration 0010): running token totals per subject (a job or the
// orchestrator) with the reader's state, per-day rollups by pricing key, and the latest model
// calls for a job's chart. internal/spend owns what the numbers mean and how they are priced; the
// store adds deltas in one transaction and reads them back.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SpendCounts are token and call counts; every spend row carries them.
type SpendCounts struct {
	Input        int64
	Output       int64
	CacheWrite5m int64
	CacheWrite1h int64
	CacheRead    int64
	Turns        int64
	ToolCalls    int64
}

// Add returns c plus other.
func (c SpendCounts) Add(other SpendCounts) SpendCounts {
	return SpendCounts{
		Input: c.Input + other.Input, Output: c.Output + other.Output,
		CacheWrite5m: c.CacheWrite5m + other.CacheWrite5m, CacheWrite1h: c.CacheWrite1h + other.CacheWrite1h,
		CacheRead: c.CacheRead + other.CacheRead, Turns: c.Turns + other.Turns, ToolCalls: c.ToolCalls + other.ToolCalls,
	}
}

// IsZero reports whether nothing was counted.
func (c SpendCounts) IsZero() bool { return c == SpendCounts{} }

// SpendTotals is one subject's spend_totals row.
type SpendTotals struct {
	Subject string
	SpendCounts
	FirstAt   time.Time
	LastAt    time.Time
	UpdatedAt time.Time
	StateJSON string
}

// SpendDaily is one spend_daily row (or an aggregate of several, by whatever the query grouped).
type SpendDaily struct {
	Day     string // YYYY-MM-DD, local
	Subject string
	Model   string // pricing key
	SpendCounts
}

// SpendTurn is one model call for the chart.
type SpendTurn struct {
	MessageID string
	At        time.Time
	Model     string
	SpendCounts
}

// SpendBatch is what one read of a subject's transcripts adds: applied in one transaction, so the
// totals, the rollups and the reader's cursors never disagree after a crash.
type SpendBatch struct {
	Subject   string
	Delta     SpendCounts // added to the totals
	FirstAt   time.Time   // the earliest call in this batch (zero: none)
	LastAt    time.Time   // the latest
	Daily     []SpendDaily
	Turns     []SpendTurn // added to an existing row with the same message id
	StateJSON string      // replaces the stored reader state
	Now       time.Time
	// KeepTurns is how many spend_turns rows the subject keeps; older ones are deleted.
	KeepTurns int
}

// ApplySpend adds a batch.
func (s *Store) ApplySpend(ctx context.Context, batch SpendBatch) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("spend for %s: %w", batch.Subject, err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := upsertSpendTotals(ctx, tx, batch); err != nil {
		return err
	}
	for _, row := range batch.Daily {
		if _, err := tx.ExecContext(ctx, `INSERT INTO spend_daily
			(day, subject, model, input, output, cache_write_5m, cache_write_1h, cache_read, turns, tool_calls)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (day, subject, model) DO UPDATE SET input = input + excluded.input, output = output + excluded.output,
			cache_write_5m = cache_write_5m + excluded.cache_write_5m, cache_write_1h = cache_write_1h + excluded.cache_write_1h,
			cache_read = cache_read + excluded.cache_read, turns = turns + excluded.turns, tool_calls = tool_calls + excluded.tool_calls`,
			row.Day, batch.Subject, row.Model, row.Input, row.Output, row.CacheWrite5m, row.CacheWrite1h, row.CacheRead, row.Turns, row.ToolCalls); err != nil {
			return fmt.Errorf("adding daily spend for %s: %w", batch.Subject, err)
		}
	}
	if err := upsertSpendTurns(ctx, tx, batch); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertSpendTotals(ctx context.Context, tx *sql.Tx, batch SpendBatch) error {
	d := batch.Delta
	_, err := tx.ExecContext(ctx, `INSERT INTO spend_totals
		(subject, input, output, cache_write_5m, cache_write_1h, cache_read, turns, tool_calls, first_at, last_at, updated_at, state_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (subject) DO UPDATE SET input = input + excluded.input, output = output + excluded.output,
		cache_write_5m = cache_write_5m + excluded.cache_write_5m, cache_write_1h = cache_write_1h + excluded.cache_write_1h,
		cache_read = cache_read + excluded.cache_read, turns = turns + excluded.turns, tool_calls = tool_calls + excluded.tool_calls,
		first_at = CASE WHEN first_at = 0 OR (excluded.first_at > 0 AND excluded.first_at < first_at) THEN excluded.first_at ELSE first_at END,
		last_at = MAX(last_at, excluded.last_at), updated_at = excluded.updated_at, state_json = excluded.state_json`,
		batch.Subject, d.Input, d.Output, d.CacheWrite5m, d.CacheWrite1h, d.CacheRead, d.Turns, d.ToolCalls,
		unixOrNil(batch.FirstAt), unixOrNil(batch.LastAt), batch.Now.Unix(), stateOrEmpty(batch.StateJSON))
	if err != nil {
		return fmt.Errorf("adding spend totals for %s: %w", batch.Subject, err)
	}
	return nil
}

func upsertSpendTurns(ctx context.Context, tx *sql.Tx, batch SpendBatch) error {
	for _, turn := range batch.Turns {
		if _, err := tx.ExecContext(ctx, `INSERT INTO spend_turns
			(subject, message_id, at, model, input, output, cache_write_5m, cache_write_1h, cache_read)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (subject, message_id) DO UPDATE SET input = input + excluded.input, output = output + excluded.output,
			cache_write_5m = cache_write_5m + excluded.cache_write_5m, cache_write_1h = cache_write_1h + excluded.cache_write_1h,
			cache_read = cache_read + excluded.cache_read`,
			batch.Subject, turn.MessageID, turn.At.Unix(), turn.Model, turn.Input, turn.Output, turn.CacheWrite5m, turn.CacheWrite1h, turn.CacheRead); err != nil {
			return fmt.Errorf("adding a spend turn for %s: %w", batch.Subject, err)
		}
	}
	if len(batch.Turns) == 0 || batch.KeepTurns <= 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM spend_turns WHERE subject = ? AND id <=
		(SELECT id FROM spend_turns WHERE subject = ? ORDER BY id DESC LIMIT 1 OFFSET ?)`, batch.Subject, batch.Subject, batch.KeepTurns)
	if err != nil {
		return fmt.Errorf("pruning spend turns for %s: %w", batch.Subject, err)
	}
	return nil
}

func stateOrEmpty(state string) string {
	if state == "" {
		return "{}"
	}
	return state
}

// SaveSpendState replaces a subject's reader state without changing its counts (a pass that read
// nothing new but moved a cursor, or marked the subject final).
func (s *Store) SaveSpendState(ctx context.Context, subject, state string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO spend_totals (subject, updated_at, state_json) VALUES (?, ?, ?)
		ON CONFLICT (subject) DO UPDATE SET updated_at = excluded.updated_at, state_json = excluded.state_json`,
		subject, now.Unix(), stateOrEmpty(state))
	if err != nil {
		return fmt.Errorf("saving spend state for %s: %w", subject, err)
	}
	return nil
}

const spendCountColumns = "input, output, cache_write_5m, cache_write_1h, cache_read, turns, tool_calls"

// GetSpendTotals returns a subject's totals; ErrNotFound when nothing was ever recorded.
func (s *Store) GetSpendTotals(ctx context.Context, subject string) (SpendTotals, error) {
	list, err := s.ListSpendTotals(ctx, []string{subject})
	if err != nil {
		return SpendTotals{}, err
	}
	if len(list) == 0 {
		return SpendTotals{}, ErrNotFound
	}
	return list[0], nil
}

// ListSpendTotals returns the totals rows of the given subjects (missing ones are left out).
func (s *Store) ListSpendTotals(ctx context.Context, subjects []string) ([]SpendTotals, error) {
	if len(subjects) == 0 {
		return nil, nil
	}
	query := "SELECT subject, " + spendCountColumns + ", first_at, last_at, updated_at, state_json FROM spend_totals WHERE subject IN (" + placeholders(len(subjects)) + ")"
	rows, err := s.db.QueryContext(ctx, query, stringArgs(subjects)...) //nolint:gosec // placeholders only
	if err != nil {
		return nil, fmt.Errorf("listing spend totals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SpendTotals
	for rows.Next() {
		var row SpendTotals
		var first, last, updated int64
		c := &row.SpendCounts
		if err := rows.Scan(&row.Subject, &c.Input, &c.Output, &c.CacheWrite5m, &c.CacheWrite1h, &c.CacheRead, &c.Turns, &c.ToolCalls,
			&first, &last, &updated, &row.StateJSON); err != nil {
			return nil, fmt.Errorf("reading spend totals: %w", err)
		}
		row.FirstAt, row.LastAt, row.UpdatedAt = unixToTime(first), unixToTime(last), unixToTime(updated)
		out = append(out, row)
	}
	return out, rows.Err()
}

// SpendBySubjectModel sums spend_daily per (subject, pricing key) over all days for the given
// subjects: what a subject's cost is computed from.
func (s *Store) SpendBySubjectModel(ctx context.Context, subjects []string) ([]SpendDaily, error) {
	if len(subjects) == 0 {
		return nil, nil
	}
	query := `SELECT '', subject, model, ` + sumColumns() + ` FROM spend_daily WHERE subject IN (` + placeholders(len(subjects)) + `) GROUP BY subject, model`
	return s.querySpendDaily(ctx, query, stringArgs(subjects)...)
}

// SpendDailyRange returns every spend_daily row with from <= day <= to (YYYY-MM-DD, inclusive).
func (s *Store) SpendDailyRange(ctx context.Context, from, to string) ([]SpendDaily, error) {
	return s.querySpendDaily(ctx, `SELECT day, subject, model, `+spendCountColumns+` FROM spend_daily WHERE day >= ? AND day <= ? ORDER BY day, subject, model`, from, to)
}

func sumColumns() string {
	parts := strings.Split(spendCountColumns, ", ")
	for i, column := range parts {
		parts[i] = "SUM(" + column + ")"
	}
	return strings.Join(parts, ", ")
}

func (s *Store) querySpendDaily(ctx context.Context, query string, args ...any) ([]SpendDaily, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("reading daily spend: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SpendDaily
	for rows.Next() {
		var row SpendDaily
		c := &row.SpendCounts
		if err := rows.Scan(&row.Day, &row.Subject, &row.Model, &c.Input, &c.Output, &c.CacheWrite5m, &c.CacheWrite1h, &c.CacheRead, &c.Turns, &c.ToolCalls); err != nil {
			return nil, fmt.Errorf("reading daily spend: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ListSpendTurns returns a subject's latest calls, oldest first, at most limit.
func (s *Store) ListSpendTurns(ctx context.Context, subject string, limit int) ([]SpendTurn, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT message_id, at, model, input, output, cache_write_5m, cache_write_1h, cache_read FROM
		(SELECT * FROM spend_turns WHERE subject = ? ORDER BY id DESC LIMIT ?) ORDER BY id`, subject, limit)
	if err != nil {
		return nil, fmt.Errorf("listing spend turns for %s: %w", subject, err)
	}
	defer func() { _ = rows.Close() }()
	var out []SpendTurn
	for rows.Next() {
		var turn SpendTurn
		var at int64
		c := &turn.SpendCounts
		if err := rows.Scan(&turn.MessageID, &at, &turn.Model, &c.Input, &c.Output, &c.CacheWrite5m, &c.CacheWrite1h, &c.CacheRead); err != nil {
			return nil, fmt.Errorf("reading spend turns: %w", err)
		}
		turn.At = unixToTime(at)
		out = append(out, turn)
	}
	return out, rows.Err()
}

// SpendStates returns every subject's reader state, for the meter to resume from at start.
func (s *Store) SpendStates(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT subject, state_json FROM spend_totals`)
	if err != nil {
		return nil, fmt.Errorf("reading spend states: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var subject, state string
		if err := rows.Scan(&subject, &state); err != nil {
			return nil, fmt.Errorf("reading spend states: %w", err)
		}
		out[subject] = state
	}
	if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return out, nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func stringArgs(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}
