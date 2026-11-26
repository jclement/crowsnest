// Read-only access for code that must not change the database: `crowsnest doctor` (which must be
// safe to run beside a live server and before the first start) and serve's early read of the
// settings saved in the app, which happens before the config is final and so before Open would
// run migrations. Nothing here creates a file, tightens a mode or applies a migration.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"strings"
)

// Inspection is what a read-only look at a database file found.
type Inspection struct {
	Exists        bool   // the file is there
	SchemaVersion int    // highest applied migration; 0 for a database never migrated
	KnownVersion  int    // the highest migration this binary carries
	Owner         bool   // an owner was stored at first-run setup
	PushKeys      bool   // Web Push (VAPID) keys exist
	Mode          fs.FileMode
}

// KnownSchemaVersion is the number of migrations this binary embeds.
func KnownSchemaVersion() int {
	migrations, err := loadMigrations(migrationFiles)
	if err != nil {
		return 0
	}
	return len(migrations)
}

// openReadOnly opens path with SQLite's read-only mode. A missing file is fs.ErrNotExist, never
// created.
func openReadOnly(ctx context.Context, path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	dsn := "file:" + (&url.URL{Path: path}).EscapedPath() + "?mode=ro&_pragma=busy_timeout(2000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database %s read-only: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("opening database %s read-only: %w", path, err)
	}
	return db, nil
}

// Inspect opens path read-only and reports its schema version and a few facts doctor shows. A
// missing file is not an error: Exists is false.
func Inspect(ctx context.Context, path string) (Inspection, error) {
	result := Inspection{KnownVersion: KnownSchemaVersion()}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("reading database %s: %w", path, err)
	}
	result.Exists, result.Mode = true, info.Mode().Perm()
	db, err := openReadOnly(ctx, path)
	if err != nil {
		return result, err
	}
	defer func() { _ = db.Close() }()
	if !tableExists(ctx, db, "schema_migrations") {
		return result, nil
	}
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&result.SchemaVersion); err != nil {
		return result, fmt.Errorf("reading schema version of %s: %w", path, err)
	}
	if tableExists(ctx, db, "kv") {
		result.Owner = kvExists(ctx, db, "auth.owner")
		result.PushKeys = kvPrefixExists(ctx, db, "push.vapid")
	}
	return result, nil
}

// ReadKVPrefix returns every kv row whose key starts with prefix, with the prefix removed, from
// the database at path opened read-only. A missing file or a database without a kv table yet
// gives an empty map.
func ReadKVPrefix(ctx context.Context, path, prefix string) (map[string]string, error) {
	out := map[string]string{}
	db, err := openReadOnly(ctx, path)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	if !tableExists(ctx, db, "kv") {
		return out, nil
	}
	return scanKVPrefix(ctx, db, prefix)
}

// KVPrefix is ReadKVPrefix on an open store.
func (s *Store) KVPrefix(ctx context.Context, prefix string) (map[string]string, error) {
	return scanKVPrefix(ctx, s.db, prefix)
}

func scanKVPrefix(ctx context.Context, db *sql.DB, prefix string) (map[string]string, error) {
	out := map[string]string{}
	rows, err := db.QueryContext(ctx, `SELECT key, value FROM kv WHERE substr(key, 1, ?) = ?`, len(prefix), prefix)
	if err != nil {
		return nil, fmt.Errorf("reading kv %s*: %w", prefix, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("reading kv %s*: %w", prefix, err)
		}
		out[strings.TrimPrefix(key, prefix)] = value
	}
	return out, rows.Err()
}

func tableExists(ctx context.Context, db *sql.DB, name string) bool {
	var found string
	err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&found)
	return err == nil
}

func kvExists(ctx context.Context, db *sql.DB, key string) bool {
	var value string
	return db.QueryRowContext(ctx, `SELECT value FROM kv WHERE key = ?`, key).Scan(&value) == nil
}

func kvPrefixExists(ctx context.Context, db *sql.DB, prefix string) bool {
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM kv WHERE substr(key, 1, ?) = ?`, len(prefix), prefix).Scan(&count)
	return err == nil && count > 0
}
