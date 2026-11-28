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
// in every schema since its migration; a table missing from an older copy is skipped. The last one
// marks the copy as scrubbed (a snapshot only; ScrubRestored runs the others).
var scrubStatements = []struct{ table, sql string }{
	{"auth_sessions", `DELETE FROM auth_sessions`},
	{"action_tokens", `DELETE FROM action_tokens`},
	{"push_subscriptions", `DELETE FROM push_subscriptions`},
	{"api_tokens", `DELETE FROM api_tokens`},
	{"machines", `UPDATE machines SET token = ''`},
	{"kv", `DELETE FROM kv WHERE key LIKE 'agent_token:%' OR key IN ('push.vapid', 'settings.notify-url') OR key LIKE '%remote_control'`},
	// The mark that this copy is scrubbed to this list (R6-S6): a copy without it, or with an older
	// one, is scrubbed again (RescrubOld).
	{"kv", `INSERT OR REPLACE INTO kv (key, value) VALUES ('` + ScrubbedKey + `', '` + ScrubVersion + `')`},
}

// ScrubbedKey and ScrubVersion mark a scrubbed copy; bump ScrubVersion when the list above grows.
const (
	ScrubbedKey  = "snapshot.scrubbed"
	ScrubVersion = "2" // 2: Remote Control session links (R6-S6)
)

// CodexConfigSeenPrefix keys the kv records of the .codex/ fingerprints the human has seen
// (internal/jobs): a "seen" from a backup is not the human's word on this machine.
const CodexConfigSeenPrefix = "codex.config-seen."

// restoreScrubStatements are what a restore removes from the incoming database besides the
// snapshot's credentials (R10, R9-S2 residual, new-H1): the .codex/ "seen" marks, and the isolated
// check directories its rows name (a path from a backup is never removed).
var restoreScrubStatements = []struct{ table, sql string }{
	{"kv", `DELETE FROM kv WHERE substr(key, 1, ` + fmt.Sprint(len(CodexConfigSeenPrefix)) + `) = '` + CodexConfigSeenPrefix + `'`},
	{"job_checks", `UPDATE job_checks SET workspace = '' WHERE workspace != ''`},
}

// ScrubRestored removes from a database just restored from a backup what a backup must not bring
// with it: the same credentials a snapshot drops (scrubStatements: browser sessions, action and
// API tokens, push subscriptions, machine and agent tokens, the push keys, the notify URL, Remote
// Control links), so a backup made elsewhere, by an older crowsnest or by hand cannot bring its
// author a way in; and restoreScrubStatements. The owner's record stays, so the human signs in to
// their own restore with their own password.
func (s *Store) ScrubRestored(ctx context.Context) error {
	statements := append([]struct{ table, sql string }{}, scrubStatements[:len(scrubStatements)-1]...)
	for _, statement := range append(statements, restoreScrubStatements...) {
		if !tableExists(ctx, s.db, statement.table) {
			continue
		}
		if _, err := s.db.ExecContext(ctx, statement.sql); err != nil {
			return fmt.Errorf("removing what a backup must not bring (%s): %w", statement.table, err)
		}
	}
	return nil
}

// Scrubbed reports whether the database copy at path carries the current scrub mark.
func Scrubbed(ctx context.Context, path string) bool {
	rows, err := ReadKVPrefix(ctx, path, ScrubbedKey)
	return err == nil && rows[""] == ScrubVersion
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
