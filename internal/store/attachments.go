// Attachments (DESIGN.md 8.6, migration 0010). internal/attach owns the rules (sniffing, limits,
// the quota, where copies go, what an agent may attach); the store only keeps the rows. The
// bytes are files named by SHA-256 under the data directory, so one blob can back several rows;
// a blob is deleted when the last row naming it goes (attach decides that from BlobInUse).

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AttachmentRecord is one row of attachments.
type AttachmentRecord struct {
	ID         string
	Name       string
	Type       string
	Size       int64
	SHA256     string
	HasThumb   bool
	Origin     string // "human" or "agent"
	OriginPane string
	OwnerKind  string // "" (pending), "job", "orchestrator", "pane", "question"
	OwnerID    string
	Role       string
	Ref        string
	Path       string
	CreatedAt  time.Time
	AttachedAt time.Time // zero while pending
	ReleasedAt time.Time // zero while its owner is live
}

const attachmentColumns = `id, name, type, size, sha256, has_thumb, origin, origin_pane, owner_kind, owner_id, role, ref, path, created_at, attached_at, released_at`

// InsertAttachment stores a new row.
func (s *Store) InsertAttachment(ctx context.Context, record AttachmentRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO attachments (`+attachmentColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.Name, record.Type, record.Size, record.SHA256, record.HasThumb, record.Origin, record.OriginPane,
		record.OwnerKind, record.OwnerID, record.Role, record.Ref, record.Path,
		record.CreatedAt.Unix(), attachmentUnix(record.AttachedAt), attachmentUnix(record.ReleasedAt))
	if err != nil {
		return fmt.Errorf("inserting attachment %s: %w", record.ID, err)
	}
	return nil
}

// GetAttachment returns one row, or ErrNotFound.
func (s *Store) GetAttachment(ctx context.Context, id string) (AttachmentRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+attachmentColumns+` FROM attachments WHERE id = ?`, id)
	return scanAttachment(row)
}

// AssignAttachment gives a pending upload its owner, its role and the path of the copy the agent
// reads. It succeeds only while the row is still pending, so one upload cannot be claimed by two
// sends (a double-clicked Send): the loser gets ErrNotFound.
func (s *Store) AssignAttachment(ctx context.Context, id string, owner AttachmentRecord, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE attachments SET owner_kind = ?, owner_id = ?, role = ?, ref = ?, path = ?, attached_at = ?
		WHERE id = ? AND owner_kind = ''`, owner.OwnerKind, owner.OwnerID, owner.Role, owner.Ref, owner.Path, at.Unix(), id)
	if err != nil {
		return fmt.Errorf("assigning attachment %s: %w", id, err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return ErrNotFound
	}
	return nil
}

// ListAttachmentsByOwner returns an owner's rows, oldest first. An empty role matches every role.
func (s *Store) ListAttachmentsByOwner(ctx context.Context, kind, ownerID, role string) ([]AttachmentRecord, error) {
	query := `SELECT ` + attachmentColumns + ` FROM attachments WHERE owner_kind = ? AND owner_id = ?`
	args := []any{kind, ownerID}
	if role != "" {
		query += ` AND role = ?`
		args = append(args, role)
	}
	return s.queryAttachments(ctx, query+` ORDER BY created_at, id`, args...)
}

// ListAttachmentsByRef returns the rows that point at ref (an answer's files, by question).
func (s *Store) ListAttachmentsByRef(ctx context.Context, role, ref string) ([]AttachmentRecord, error) {
	return s.queryAttachments(ctx, `SELECT `+attachmentColumns+` FROM attachments WHERE role = ? AND ref = ? ORDER BY created_at, id`, role, ref)
}

// ReleaseAttachments marks every live row of an owner released (its job or question settled) and
// returns them, so the caller can remove the copies made for the agent.
func (s *Store) ReleaseAttachments(ctx context.Context, kind, ownerID string, at time.Time) ([]AttachmentRecord, error) {
	rows, err := s.queryAttachments(ctx, `SELECT `+attachmentColumns+` FROM attachments WHERE owner_kind = ? AND owner_id = ? AND released_at = 0`, kind, ownerID)
	if err != nil || len(rows) == 0 {
		return rows, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE attachments SET released_at = ? WHERE owner_kind = ? AND owner_id = ? AND released_at = 0`,
		at.Unix(), kind, ownerID); err != nil {
		return nil, fmt.Errorf("releasing attachments of %s %s: %w", kind, ownerID, err)
	}
	return rows, nil
}

// ReleaseAttachmentsOlderThan releases the live rows of the given owner kinds attached before
// cutoff (the orchestrator's and plain panes' files, which have no settling event of their own).
func (s *Store) ReleaseAttachmentsOlderThan(ctx context.Context, kinds []string, cutoff, at time.Time) ([]AttachmentRecord, error) {
	var released []AttachmentRecord
	for _, kind := range kinds {
		rows, err := s.queryAttachments(ctx, `SELECT `+attachmentColumns+` FROM attachments
			WHERE owner_kind = ? AND released_at = 0 AND attached_at > 0 AND attached_at < ?`, kind, cutoff.Unix())
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if _, err := s.db.ExecContext(ctx, `UPDATE attachments SET released_at = ? WHERE id = ?`, at.Unix(), row.ID); err != nil {
				return nil, fmt.Errorf("releasing attachment %s: %w", row.ID, err)
			}
		}
		released = append(released, rows...)
	}
	return released, nil
}

// CleanupCandidates lists rows that may be deleted to make room, best candidates first: released
// rows (oldest release first), then pending uploads created before pendingBefore (oldest first).
// Rows still owned by something live are never listed.
func (s *Store) CleanupCandidates(ctx context.Context, pendingBefore time.Time, limit int) ([]AttachmentRecord, error) {
	return s.queryAttachments(ctx, `SELECT `+attachmentColumns+` FROM attachments
		WHERE released_at > 0 OR (owner_kind = '' AND created_at < ?)
		ORDER BY CASE WHEN released_at > 0 THEN 0 ELSE 1 END, released_at, created_at, id LIMIT ?`, pendingBefore.Unix(), limit)
}

// StalePendingAttachments lists uploads that never got an owner and were made before cutoff.
func (s *Store) StalePendingAttachments(ctx context.Context, cutoff time.Time) ([]AttachmentRecord, error) {
	return s.queryAttachments(ctx, `SELECT `+attachmentColumns+` FROM attachments WHERE owner_kind = '' AND created_at < ? ORDER BY created_at`, cutoff.Unix())
}

// DeleteAttachment removes one row. A row already gone is not an error.
func (s *Store) DeleteAttachment(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM attachments WHERE id = ?`, id); err != nil {
		return fmt.Errorf("deleting attachment %s: %w", id, err)
	}
	return nil
}

// BlobInUse reports whether any row still names the blob sha.
func (s *Store) BlobInUse(ctx context.Context, sha string) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attachments WHERE sha256 = ?`, sha).Scan(&count); err != nil {
		return false, fmt.Errorf("counting uses of a blob: %w", err)
	}
	return count > 0, nil
}

// BlobBytes is the disk the attachments take: the sum of the sizes of distinct blobs.
func (s *Store) BlobBytes(ctx context.Context) (int64, error) {
	var total sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT SUM(size) FROM (SELECT sha256, MAX(size) AS size FROM attachments GROUP BY sha256)`).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("summing attachment sizes: %w", err)
	}
	return total.Int64, nil
}

// BlobKnown reports whether a row already names sha (an identical upload reuses the blob) and
// whether that blob has a thumbnail.
func (s *Store) BlobKnown(ctx context.Context, sha string) (known, thumb bool, err error) {
	var count, thumbs int
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(has_thumb), 0) FROM attachments WHERE sha256 = ?`, sha).Scan(&count, &thumbs)
	if err != nil {
		return false, false, fmt.Errorf("looking up a blob: %w", err)
	}
	return count > 0, thumbs > 0, nil
}

func (s *Store) queryAttachments(ctx context.Context, query string, args ...any) ([]AttachmentRecord, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing attachments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AttachmentRecord
	for rows.Next() {
		record, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func scanAttachment(row interface{ Scan(...any) error }) (AttachmentRecord, error) {
	var record AttachmentRecord
	var created, attached, released int64
	err := row.Scan(&record.ID, &record.Name, &record.Type, &record.Size, &record.SHA256, &record.HasThumb, &record.Origin, &record.OriginPane,
		&record.OwnerKind, &record.OwnerID, &record.Role, &record.Ref, &record.Path, &created, &attached, &released)
	if errors.Is(err, sql.ErrNoRows) {
		return AttachmentRecord{}, ErrNotFound
	}
	if err != nil {
		return AttachmentRecord{}, fmt.Errorf("reading attachment: %w", err)
	}
	record.CreatedAt = time.Unix(created, 0).UTC()
	record.AttachedAt = attachmentTime(attached)
	record.ReleasedAt = attachmentTime(released)
	return record, nil
}

func attachmentUnix(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.Unix()
}

func attachmentTime(seconds int64) time.Time {
	if seconds == 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}
