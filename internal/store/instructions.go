// Instruction layers and what agents loaded (DESIGN.md 21.3, migration 0012). internal/instructions
// owns the layering, the built-ins and the file overrides; the store keeps the human's text, its
// revisions, the layers each agent was started with, and the files Claude reported loading.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// InstructionLayer is one row of instruction_layers: the human's text in effect for a scope.
type InstructionLayer struct {
	Scope     string // global | role | project
	Key       string // '' for global, the role name, or the project id
	BodyMD    string
	Revision  int
	UpdatedAt time.Time
}

// InstructionRevision is one saved version of a layer. Deleted marks "reset to built-in".
type InstructionRevision struct {
	ID        int64
	Scope     string
	Key       string
	Revision  int
	BodyMD    string
	Deleted   bool
	CreatedAt time.Time
}

// GetInstructionLayer returns the layer in effect, or ErrNotFound when none is set.
func (s *Store) GetInstructionLayer(ctx context.Context, scope, key string) (InstructionLayer, error) {
	var layer InstructionLayer
	var updated int64
	err := s.db.QueryRowContext(ctx, `SELECT scope, key, body_md, revision, updated_at FROM instruction_layers
		WHERE scope = ? AND key = ?`, scope, key).Scan(&layer.Scope, &layer.Key, &layer.BodyMD, &layer.Revision, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return InstructionLayer{}, ErrNotFound
	}
	if err != nil {
		return InstructionLayer{}, fmt.Errorf("reading instructions %s/%s: %w", scope, key, err)
	}
	layer.UpdatedAt = unixToTime(updated)
	return layer, nil
}

// ListInstructionLayers returns every layer in effect.
func (s *Store) ListInstructionLayers(ctx context.Context) ([]InstructionLayer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT scope, key, body_md, revision, updated_at FROM instruction_layers ORDER BY scope, key`)
	if err != nil {
		return nil, fmt.Errorf("listing instructions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []InstructionLayer
	for rows.Next() {
		var layer InstructionLayer
		var updated int64
		if err := rows.Scan(&layer.Scope, &layer.Key, &layer.BodyMD, &layer.Revision, &updated); err != nil {
			return nil, fmt.Errorf("reading instructions: %w", err)
		}
		layer.UpdatedAt = unixToTime(updated)
		out = append(out, layer)
	}
	return out, rows.Err()
}

// SaveInstructionLayer records a new revision of a layer and makes it the one in effect (or,
// with deleted, removes the layer so the built-in applies again). Older revisions past keep are
// pruned. It returns the new revision number. One transaction: the layer and its history agree.
func (s *Store) SaveInstructionLayer(ctx context.Context, scope, key, body string, deleted bool, at time.Time, keep int) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("saving instructions %s/%s: %w", scope, key, err)
	}
	defer func() { _ = tx.Rollback() }()
	var last int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision), 0) FROM instruction_revisions WHERE scope = ? AND key = ?`,
		scope, key).Scan(&last); err != nil {
		return 0, fmt.Errorf("saving instructions %s/%s: %w", scope, key, err)
	}
	revision := last + 1
	if _, err := tx.ExecContext(ctx, `INSERT INTO instruction_revisions (scope, key, revision, body_md, deleted, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, scope, key, revision, body, deleted, at.Unix()); err != nil {
		return 0, fmt.Errorf("saving instructions %s/%s: %w", scope, key, err)
	}
	if deleted {
		_, err = tx.ExecContext(ctx, `DELETE FROM instruction_layers WHERE scope = ? AND key = ?`, scope, key)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO instruction_layers (scope, key, body_md, revision, updated_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(scope, key) DO UPDATE SET body_md = excluded.body_md, revision = excluded.revision, updated_at = excluded.updated_at`,
			scope, key, body, revision, at.Unix())
	}
	if err != nil {
		return 0, fmt.Errorf("saving instructions %s/%s: %w", scope, key, err)
	}
	if keep > 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM instruction_revisions WHERE scope = ? AND key = ? AND revision <= ?`,
			scope, key, revision-keep); err != nil {
			return 0, fmt.Errorf("pruning instructions %s/%s: %w", scope, key, err)
		}
	}
	return revision, tx.Commit()
}

// ListInstructionRevisions returns a layer's kept revisions, newest first.
func (s *Store) ListInstructionRevisions(ctx context.Context, scope, key string) ([]InstructionRevision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, scope, key, revision, body_md, deleted, created_at FROM instruction_revisions
		WHERE scope = ? AND key = ? ORDER BY revision DESC`, scope, key)
	if err != nil {
		return nil, fmt.Errorf("listing revisions of %s/%s: %w", scope, key, err)
	}
	defer func() { _ = rows.Close() }()
	var out []InstructionRevision
	for rows.Next() {
		var r InstructionRevision
		var created int64
		if err := rows.Scan(&r.ID, &r.Scope, &r.Key, &r.Revision, &r.BodyMD, &r.Deleted, &created); err != nil {
			return nil, fmt.Errorf("reading a revision: %w", err)
		}
		r.CreatedAt = unixToTime(created)
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetInstructionRevision returns one kept revision.
func (s *Store) GetInstructionRevision(ctx context.Context, scope, key string, revision int) (InstructionRevision, error) {
	var r InstructionRevision
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT id, scope, key, revision, body_md, deleted, created_at FROM instruction_revisions
		WHERE scope = ? AND key = ? AND revision = ?`, scope, key, revision).
		Scan(&r.ID, &r.Scope, &r.Key, &r.Revision, &r.BodyMD, &r.Deleted, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return InstructionRevision{}, ErrNotFound
	}
	if err != nil {
		return InstructionRevision{}, fmt.Errorf("reading revision %d of %s/%s: %w", revision, scope, key, err)
	}
	r.CreatedAt = unixToTime(created)
	return r, nil
}

// ---- What agents were started with, and what they loaded -----------------------------------

// AgentLayers is one row of agent_layers: the crowsnest layers an agent was last started with.
type AgentLayers struct {
	Subject    string
	StartedAt  time.Time
	Hash       string
	LayersJSON string
}

// SetAgentLayers replaces the subject's record of what it was started with.
func (s *Store) SetAgentLayers(ctx context.Context, record AgentLayers) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_layers (subject, started_at, hash, layers_json) VALUES (?, ?, ?, ?)
		ON CONFLICT(subject) DO UPDATE SET started_at = excluded.started_at, hash = excluded.hash, layers_json = excluded.layers_json`,
		record.Subject, record.StartedAt.Unix(), record.Hash, record.LayersJSON)
	if err != nil {
		return fmt.Errorf("recording the layers of %s: %w", record.Subject, err)
	}
	return nil
}

// GetAgentLayers returns what the subject was last started with, or ErrNotFound.
func (s *Store) GetAgentLayers(ctx context.Context, subject string) (AgentLayers, error) {
	var record AgentLayers
	var started int64
	err := s.db.QueryRowContext(ctx, `SELECT subject, started_at, hash, layers_json FROM agent_layers WHERE subject = ?`, subject).
		Scan(&record.Subject, &started, &record.Hash, &record.LayersJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentLayers{}, ErrNotFound
	}
	if err != nil {
		return AgentLayers{}, fmt.Errorf("reading the layers of %s: %w", subject, err)
	}
	record.StartedAt = unixToTime(started)
	return record, nil
}

// LoadedInstruction is one file Claude reported loading (the InstructionsLoaded hook).
type LoadedInstruction struct {
	ID         int64
	Subject    string
	SessionID  string
	At         time.Time
	Path       string
	MemoryType string
	LoadReason string
	Size       int64
}

// maxLoadedPerSubject bounds the recorded files per subject: a session that lazily loads
// rules on every file it opens must not grow the table without end.
const maxLoadedPerSubject = 200

// RecordLoadedInstruction stores one load (a repeat of the same path in the same session only
// refreshes the time and size) and prunes the subject's oldest rows past the bound.
func (s *Store) RecordLoadedInstruction(ctx context.Context, record LoadedInstruction) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_instructions (subject, session_id, at, path, memory_type, load_reason, size)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(subject, session_id, path) DO UPDATE SET at = excluded.at, size = excluded.size`,
		record.Subject, record.SessionID, record.At.Unix(), record.Path, record.MemoryType, record.LoadReason, record.Size)
	if err != nil {
		return fmt.Errorf("recording a loaded instruction file for %s: %w", record.Subject, err)
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM agent_instructions WHERE subject = ? AND id NOT IN
		(SELECT id FROM agent_instructions WHERE subject = ? ORDER BY id DESC LIMIT ?)`, record.Subject, record.Subject, maxLoadedPerSubject)
	if err != nil {
		return fmt.Errorf("pruning loaded instruction files for %s: %w", record.Subject, err)
	}
	return nil
}

// ListLoadedInstructions returns the files a subject loaded in its latest session (the session
// of its newest row), oldest first.
func (s *Store) ListLoadedInstructions(ctx context.Context, subject string) ([]LoadedInstruction, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, subject, session_id, at, path, memory_type, load_reason, size
		FROM agent_instructions WHERE subject = ? AND session_id = (SELECT session_id FROM agent_instructions WHERE subject = ? ORDER BY id DESC LIMIT 1)
		ORDER BY id`, subject, subject)
	if err != nil {
		return nil, fmt.Errorf("listing loaded instruction files for %s: %w", subject, err)
	}
	defer func() { _ = rows.Close() }()
	var out []LoadedInstruction
	for rows.Next() {
		var r LoadedInstruction
		var at int64
		if err := rows.Scan(&r.ID, &r.Subject, &r.SessionID, &at, &r.Path, &r.MemoryType, &r.LoadReason, &r.Size); err != nil {
			return nil, fmt.Errorf("reading a loaded instruction file: %w", err)
		}
		r.At = unixToTime(at)
		out = append(out, r)
	}
	return out, rows.Err()
}
