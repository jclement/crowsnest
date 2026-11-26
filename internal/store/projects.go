// Project rows (DESIGN.md 21.1, migration 0013). internal/projects owns validation, slugs, the
// directory rules and the leads; the store reads and writes rows. Projects are archived, never
// deleted, so a job's project_id always names a row.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ProjectRecord is one row of projects.
type ProjectRecord struct {
	ID             int64
	Slug           string
	Name           string
	Dir            string
	RootDir        string
	Description    string
	DefaultRole    string
	DefaultEngine  string
	DefaultModel   string
	DefaultIsolate int // -1 the role's default, 0 no, 1 yes
	PermissionMode string
	LeadStart      string // manual | open | job
	ArchivedAt     time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ErrConflict is returned when a unique rule refuses a write (a slug or a live directory taken).
var ErrConflict = errors.New("conflict")

const projectSelect = `SELECT id, slug, name, dir, root_dir, description, default_role, default_engine,
	default_model, default_isolate, permission_mode, lead_start, archived_at, created_at, updated_at FROM projects`

func scanProject(row interface{ Scan(...any) error }) (ProjectRecord, error) {
	var r ProjectRecord
	var archived, created, updated int64
	err := row.Scan(&r.ID, &r.Slug, &r.Name, &r.Dir, &r.RootDir, &r.Description, &r.DefaultRole, &r.DefaultEngine,
		&r.DefaultModel, &r.DefaultIsolate, &r.PermissionMode, &r.LeadStart, &archived, &created, &updated)
	r.ArchivedAt, r.CreatedAt, r.UpdatedAt = unixToTime(archived), unixToTime(created), unixToTime(updated)
	return r, err
}

// conflictOr maps SQLite's unique-constraint failure to ErrConflict.
func conflictOr(err error, what string) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("%s: %w", what, ErrConflict)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// InsertProject stores a new project and returns its id. A taken slug or a live project in the
// same root directory is ErrConflict.
func (s *Store) InsertProject(ctx context.Context, r ProjectRecord) (int64, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO projects (slug, name, dir, root_dir, description,
		default_role, default_engine, default_model, default_isolate, permission_mode, lead_start, archived_at,
		created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Slug, r.Name, r.Dir, r.RootDir, r.Description, r.DefaultRole, r.DefaultEngine, r.DefaultModel,
		r.DefaultIsolate, r.PermissionMode, r.LeadStart, unixSecondsOrZero(r.ArchivedAt), unixSecondsOrZero(r.CreatedAt), unixSecondsOrZero(r.UpdatedAt))
	if err := conflictOr(err, "inserting project "+r.Slug); err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// UpdateProject rewrites a project row (every column but id, slug and created_at).
func (s *Store) UpdateProject(ctx context.Context, r ProjectRecord) error {
	result, err := s.db.ExecContext(ctx, `UPDATE projects SET name = ?, dir = ?, root_dir = ?, description = ?,
		default_role = ?, default_engine = ?, default_model = ?, default_isolate = ?, permission_mode = ?,
		lead_start = ?, archived_at = ?, updated_at = ? WHERE id = ?`,
		r.Name, r.Dir, r.RootDir, r.Description, r.DefaultRole, r.DefaultEngine, r.DefaultModel, r.DefaultIsolate,
		r.PermissionMode, r.LeadStart, unixSecondsOrZero(r.ArchivedAt), unixSecondsOrZero(r.UpdatedAt), r.ID)
	if err := conflictOr(err, fmt.Sprintf("updating project %d", r.ID)); err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetProject returns one project by id.
func (s *Store) GetProject(ctx context.Context, id int64) (ProjectRecord, error) {
	r, err := scanProject(s.db.QueryRowContext(ctx, projectSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectRecord{}, ErrNotFound
	}
	if err != nil {
		return ProjectRecord{}, fmt.Errorf("reading project %d: %w", id, err)
	}
	return r, nil
}

// GetProjectBySlug returns one project by slug.
func (s *Store) GetProjectBySlug(ctx context.Context, slug string) (ProjectRecord, error) {
	r, err := scanProject(s.db.QueryRowContext(ctx, projectSelect+` WHERE slug = ?`, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectRecord{}, ErrNotFound
	}
	if err != nil {
		return ProjectRecord{}, fmt.Errorf("reading project %q: %w", slug, err)
	}
	return r, nil
}

// ListProjects returns projects by name; archived ones only when includeArchived.
func (s *Store) ListProjects(ctx context.Context, includeArchived bool) ([]ProjectRecord, error) {
	query := projectSelect
	if !includeArchived {
		query += ` WHERE archived_at = 0`
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ProjectRecord
	for rows.Next() {
		r, err := scanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("reading a project: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ProjectJobCounts is one project's live job counts for the board's switcher.
type ProjectJobCounts struct {
	ProjectID int64
	NeedsYou  int // needs_you, or stuck
	Running   int // running, blocked, in_review, queued
	Total     int // every job, settled included
}

// CountJobsByProject returns live counts per project id (0 is Unfiled).
func (s *Store) CountJobsByProject(ctx context.Context) (map[int64]ProjectJobCounts, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project_id,
		SUM(CASE WHEN state = 'needs_you' OR (stuck = 1 AND state NOT IN ('done','failed','cancelled')) THEN 1 ELSE 0 END),
		SUM(CASE WHEN state IN ('running','blocked','in_review','queued') AND stuck = 0 THEN 1 ELSE 0 END),
		COUNT(*) FROM jobs GROUP BY project_id`)
	if err != nil {
		return nil, fmt.Errorf("counting jobs by project: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]ProjectJobCounts{}
	for rows.Next() {
		var c ProjectJobCounts
		if err := rows.Scan(&c.ProjectID, &c.NeedsYou, &c.Running, &c.Total); err != nil {
			return nil, fmt.Errorf("reading job counts: %w", err)
		}
		out[c.ProjectID] = c
	}
	return out, rows.Err()
}

// OldestUnassignedQueuedIn is OldestUnassignedQueued for one project: the next job its lead
// takes (a queued job with no directory and no agent, not held for approval).
func (s *Store) OldestUnassignedQueuedIn(ctx context.Context, projectID int64) (JobRecord, bool, error) {
	jobs, err := s.queryJobs(ctx, jobSelect+" WHERE state = 'queued' AND dir = '' AND agent_name = '' AND cause != 'approval' AND project_id = ? ORDER BY id LIMIT 1", projectID)
	if err != nil || len(jobs) == 0 {
		return JobRecord{}, false, err
	}
	return jobs[0], true, nil
}

func unixSecondsOrZero(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.Unix()
}
