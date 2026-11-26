package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestMigrationsApplyAndAreIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sub", "crow.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	version, err := first.SchemaVersion(ctx)
	if err != nil || version != 3 {
		t.Fatalf("version %d err %v", version, err)
	}
	if err := first.SetKV(ctx, "k", "v"); err != nil {
		t.Fatal(err)
	}
	_ = first.Close()

	second, err := Open(ctx, path) // reopening must not re-run 0001
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	if value, err := second.GetKV(ctx, "k"); err != nil || value != "v" {
		t.Fatalf("data lost across reopen: %q %v", value, err)
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("data directory mode %v err %v", info.Mode().Perm(), err)
	}
}

func TestLoadMigrationsRejectsBadSets(t *testing.T) {
	tests := []struct {
		name  string
		files fstest.MapFS
	}{
		{"gap", fstest.MapFS{"migrations/0001_a.sql": {}, "migrations/0003_c.sql": {}}},
		{"duplicate", fstest.MapFS{"migrations/0001_a.sql": {}, "migrations/0001_b.sql": {}}},
		{"bad name", fstest.MapFS{"migrations/init.sql": {}}},
		{"not sql", fstest.MapFS{"migrations/0001_a.txt": {}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadMigrations(tc.files); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
	good, err := loadMigrations(fstest.MapFS{"migrations/0002_b.sql": {}, "migrations/0001_a.sql": {}})
	if err != nil || len(good) != 2 || good[0].version != 1 {
		t.Fatalf("good set: %+v %v", good, err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	now := time.Unix(1_800_000_000, 0)
	session := AuthSession{IDHash: "h1", CredDigest: "d1", CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour), UserAgent: "test"}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSession(ctx, "h1")
	if err != nil || got != session {
		t.Fatalf("got %+v err %v", got, err)
	}
	later := now.Add(30 * time.Minute)
	if err := store.TouchSession(ctx, "h1", later, later.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetSession(ctx, "h1")
	if !got.LastSeenAt.Equal(later) || !got.ExpiresAt.Equal(later.Add(time.Hour)) {
		t.Fatalf("touch did not stick: %+v", got)
	}
	if err := store.DeleteSession(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSession(ctx, "h1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if err := store.DeleteSession(ctx, "h1"); err != nil {
		t.Fatalf("deleting twice: %v", err)
	}
}

func TestDeleteStaleSessions(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	now := time.Unix(1_800_000_000, 0)
	rows := []AuthSession{
		{IDHash: "live", CredDigest: "current", ExpiresAt: now.Add(time.Hour)},
		{IDHash: "expired", CredDigest: "current", ExpiresAt: now.Add(-time.Second)},
		{IDHash: "old-password", CredDigest: "previous", ExpiresAt: now.Add(time.Hour)},
	}
	for _, row := range rows {
		row.CreatedAt, row.LastSeenAt = now, now
		if err := store.CreateSession(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := store.DeleteStaleSessions(ctx, now, "current")
	if err != nil || removed != 2 {
		t.Fatalf("removed %d err %v", removed, err)
	}
	if _, err := store.GetSession(ctx, "live"); err != nil {
		t.Fatalf("live session went too: %v", err)
	}
}

func TestKV(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	if _, err := store.GetKV(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing key: %v", err)
	}
	for _, value := range []string{"one", "two"} {
		if err := store.SetKV(ctx, "orchestrator.session", value); err != nil {
			t.Fatal(err)
		}
	}
	if value, _ := store.GetKV(ctx, "orchestrator.session"); value != "two" {
		t.Fatalf("upsert: got %q", value)
	}
	if err := store.DeleteKV(ctx, "orchestrator.session"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetKV(ctx, "orchestrator.session"); !errors.Is(err, ErrNotFound) {
		t.Fatal("key survived delete")
	}
}

// TestOpenMakesTheDatabasePrivate is R1 L5: an existing directory or file with loose modes is
// tightened, and a new file is created 0600 whatever the umask.
func TestOpenMakesTheDatabasePrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shared")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(dir, 0o755)
	path := filepath.Join(dir, "crow.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.SetKV(context.Background(), "k", "v") // makes SQLite write its WAL
	_ = db.Close()
	assertMode(t, dir, 0o700)
	assertMode(t, path, 0o600)

	_ = os.Chmod(path, 0o644) // loosened by something else since
	db, err = Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	assertMode(t, path, 0o600)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s has mode %s, want %s", path, got, want)
	}
}
