// Journal rows (DESIGN.md section 11, migration 0005): what crowsnest did on its own or for the
// human that is worth seeing later. internal/journal owns the kinds and the wording; the store
// only keeps rows and pages through them newest first.

package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// JournalRecord is one row of journal.
type JournalRecord struct {
	ID         int64
	At         time.Time
	Kind       string
	Summary    string
	DetailJSON string
}

// JournalQuery selects a page of the journal, newest first.
type JournalQuery struct {
	Kinds    []string // empty: every kind
	BeforeID int64    // 0: from the newest; otherwise rows with a smaller id (the next page)
	Limit    int
}

// InsertJournal stores an entry and returns its id.
func (s *Store) InsertJournal(ctx context.Context, record JournalRecord) (int64, error) {
	detail := record.DetailJSON
	if detail == "" {
		detail = "{}"
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO journal (at, kind, summary, detail_json) VALUES (?, ?, ?, ?)`,
		record.At.Unix(), record.Kind, record.Summary, detail)
	if err != nil {
		return 0, fmt.Errorf("inserting journal entry: %w", err)
	}
	return result.LastInsertId()
}

// ListJournal returns one page of entries, newest first, and the total number of entries the
// filter matches (for the page's meta).
func (s *Store) ListJournal(ctx context.Context, query JournalQuery) ([]JournalRecord, int, error) {
	where, args := journalFilter(query.Kinds)
	var total int
	// The WHERE clause is built from a fixed template; every value is a parameter.
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM journal"+where, args...).Scan(&total); err != nil { //nolint:gosec // see above
		return nil, 0, fmt.Errorf("counting journal entries: %w", err)
	}
	if query.BeforeID > 0 {
		where = appendCondition(where, "id < ?")
		args = append(args, query.BeforeID)
	}
	args = append(args, query.Limit)
	rows, err := s.db.QueryContext(ctx, "SELECT id, at, kind, summary, detail_json FROM journal"+where+" ORDER BY id DESC LIMIT ?", args...) //nolint:gosec // see above
	if err != nil {
		return nil, 0, fmt.Errorf("listing journal entries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []JournalRecord
	for rows.Next() {
		var record JournalRecord
		var at int64
		if err := rows.Scan(&record.ID, &at, &record.Kind, &record.Summary, &record.DetailJSON); err != nil {
			return nil, 0, fmt.Errorf("reading a journal entry: %w", err)
		}
		record.At = time.Unix(at, 0)
		out = append(out, record)
	}
	return out, total, rows.Err()
}

// journalFilter is the WHERE clause for a kind filter, with its arguments.
func journalFilter(kinds []string) (string, []any) {
	if len(kinds) == 0 {
		return "", nil
	}
	args := make([]any, len(kinds))
	for i, kind := range kinds {
		args[i] = kind
	}
	return " WHERE kind IN (?" + strings.Repeat(", ?", len(kinds)-1) + ")", args
}

func appendCondition(where, condition string) string {
	if where == "" {
		return " WHERE " + condition
	}
	return where + " AND " + condition
}
