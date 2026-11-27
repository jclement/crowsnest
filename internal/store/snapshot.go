// Consistent copies of the database, for `crowsnest backup`, the scheduled backup and the
// automatic copy taken before a migration (DESIGN.md 4.17, 11). VACUUM INTO writes a complete,
// compacted database from one read transaction, so the copy is consistent while the server keeps
// writing (WAL readers do not block writers), and it works on a read-only connection too, which
// is how serve copies a database before Open migrates it.
//
// A backup must never carry a credential in a usable form. ScrubSnapshot removes, from the copy
// only, everything that would let the file's holder act as the user or as an agent: login
// sessions, notification action tokens, agent token hashes, the Web Push private key and the
// subscriptions it encrypts to, the notify URL, scoped API tokens and the tokens this instance
// holds for other machines. The owner's argon2id hash stays (it is not the password, and without
// it a restore could not be signed in to).

package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
)

// VacuumInto writes a consistent copy of the database to path, which must not exist yet.
func (s *Store) VacuumInto(ctx context.Context, path string) error {
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("copying the database to %s: %w", path, err)
	}
	return nil
}

// VacuumFileInto copies the database file at source to path through a read-only connection: no
// migration runs and nothing in source changes. path must not exist yet.
func VacuumFileInto(ctx context.Context, source, path string) error {
	db, err := openReadOnly(ctx, source)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("copying the database %s to %s: %w", source, path, err)
	}
	return nil
}

// scrubStatements remove credentials from a snapshot. Each names a table or key that exists
// in every schema since its migration; a table missing from an older copy is skipped.
var scrubStatements = []struct{ table, sql string }{
	{"auth_sessions", `DELETE FROM auth_sessions`},
	{"action_tokens", `DELETE FROM action_tokens`},
	{"push_subscriptions", `DELETE FROM push_subscriptions`},
	{"api_tokens", `DELETE FROM api_tokens`},
	{"machines", `UPDATE machines SET token = ''`},
	{"kv", `DELETE FROM kv WHERE key LIKE 'agent_token:%' OR key IN ('push.vapid', 'settings.notify-url')`},
}

// ScrubSnapshot removes credentials from the database copy at path (see the file comment) and
// compacts it, so deleted rows do not linger in free pages.
func ScrubSnapshot(ctx context.Context, path string) error {
	dsn := "file:" + (&url.URL{Path: path}).EscapedPath() + "?_pragma=busy_timeout(2000)&_pragma=secure_delete(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("opening the snapshot %s: %w", path, err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	for _, statement := range scrubStatements {
		if !tableExists(ctx, db, statement.table) {
			continue
		}
		if _, err := db.ExecContext(ctx, statement.sql); err != nil {
			return fmt.Errorf("removing credentials from the snapshot (%s): %w", statement.table, err)
		}
	}
	if _, err := db.ExecContext(ctx, `VACUUM`); err != nil {
		return fmt.Errorf("compacting the snapshot: %w", err)
	}
	return os.Chmod(path, 0o600)
}

// SnapshotSchemaVersion reads the schema version of a database file read-only (0 when it has
// never been migrated).
func SnapshotSchemaVersion(ctx context.Context, path string) (int, error) {
	db, err := openReadOnly(ctx, path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	if !tableExists(ctx, db, "schema_migrations") {
		return 0, nil
	}
	var version int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return 0, fmt.Errorf("reading the schema version of %s: %w", path, err)
	}
	return version, nil
}
