// Package store is crowsnest's SQLite database (modernc.org/sqlite, so CGO stays off). All SQL
// lives in this package; handlers and services call typed methods and never hold a query.
//
// Migrations are numbered .sql files embedded from migrations/ and applied in order at Open,
// each in its own transaction, recorded in schema_migrations. A shipped migration is never
// edited; a schema change is always a new file.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Store wraps the database handle.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path, with its directory made 0700 because it
// holds session hashes, and applies pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating data directory: %w", err)
	}
	// WAL lets the SSE readers and the writers overlap; busy_timeout absorbs the rest.
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	return open(ctx, dsn)
}

// OpenMemory opens a private in-memory database, for tests.
func OpenMemory(ctx context.Context) (*Store, error) {
	return open(ctx, "file::memory:?_pragma=foreign_keys(1)")
}

func open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	// One connection: SQLite serialises writers anyway, an in-memory database exists per
	// connection, and crowsnest's write volume is a handful of rows a minute.
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks the database answers.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// ---- Migrations --------------------------------------------------------------

// migration is one numbered file.
type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads migrations/NNNN_name.sql in version order and rejects gaps or duplicates,
// which would otherwise apply in a surprising order.
func loadMigrations(files fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(files, "migrations")
	if err != nil {
		return nil, fmt.Errorf("reading migrations: %w", err)
	}
	var out []migration
	for _, entry := range entries {
		number, _, ok := strings.Cut(entry.Name(), "_")
		version, err := strconv.Atoi(number)
		if !ok || err != nil || !strings.HasSuffix(entry.Name(), ".sql") {
			return nil, fmt.Errorf("migration %q is not named NNNN_name.sql", entry.Name())
		}
		body, err := fs.ReadFile(files, "migrations/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("reading migration %s: %w", entry.Name(), err)
		}
		out = append(out, migration{version: version, name: entry.Name(), sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i, item := range out {
		if item.version != i+1 {
			return nil, fmt.Errorf("migration %s: expected version %d (numbers must start at 1 with no gaps)", item.name, i+1)
		}
	}
	return out, nil
}

func (s *Store) migrate(ctx context.Context) error {
	migrations, err := loadMigrations(migrationFiles)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL DEFAULT (unixepoch()))`); err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
	}
	current, err := s.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if current > len(migrations) {
		return fmt.Errorf("database schema is version %d but this crowsnest knows only %d; run a newer crowsnest", current, len(migrations))
	}
	for _, item := range migrations[current:] {
		if err := s.apply(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) apply(ctx context.Context, item migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migration %s: %w", item.name, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, item.sql); err != nil {
		return fmt.Errorf("applying migration %s: %w", item.name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, item.version, item.name); err != nil {
		return fmt.Errorf("recording migration %s: %w", item.name, err)
	}
	return tx.Commit()
}

// SchemaVersion is the highest applied migration number (0 for a fresh database).
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("reading schema version: %w", err)
	}
	return version, nil
}
