// Tests for the upgrade path: a copy before a version change or a migration, three kept, and a
// database from a newer crowsnest refused with what to do.

package backup

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

func openedDatabase(t *testing.T, lastVersion string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crowsnest.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if lastVersion != "" {
		mustDo(t, db.SetKV(context.Background(), LastVersionKey, lastVersion))
	}
	mustDo(t, db.Close())
	return path
}

func TestPrepareUpgradeCopiesOnAVersionChangeAndKeepsThree(t *testing.T) {
	ctx := context.Background()
	dbPath := openedDatabase(t, "v1.0.0")
	dir := filepath.Join(t.TempDir(), "backups")
	start := time.Date(2026, 11, 27, 1, 0, 0, 0, time.UTC)

	check, err := PrepareUpgrade(ctx, dbPath, dir, "crowsnest", "v1.0.0", start)
	if err != nil || check.BackupPath != "" {
		t.Fatalf("same version: %+v, %v; want no copy", check, err)
	}
	for i := 0; i < 5; i++ {
		check, err = PrepareUpgrade(ctx, dbPath, dir, "crowsnest", "v1.1.0", start.Add(time.Duration(i)*time.Minute))
		if err != nil || check.BackupPath == "" {
			t.Fatalf("version change %d: %+v, %v; want a copy", i, check, err)
		}
		if version, err := store.SnapshotSchemaVersion(ctx, check.BackupPath); err != nil || version != store.KnownSchemaVersion() {
			t.Fatalf("the copy is not a database: %d, %v", version, err)
		}
	}
	copies, err := UpgradeBackups(dir, "crowsnest")
	if err != nil || len(copies) != UpgradeKeep {
		t.Fatalf("copies kept: %v, %v; want %d", copies, err, UpgradeKeep)
	}
	if filepath.Base(copies[0]) <= filepath.Base(copies[len(copies)-1]) {
		t.Errorf("copies not newest first: %v", copies)
	}
}

func TestPrepareUpgradeNeedsNothingOnAFirstStart(t *testing.T) {
	check, err := PrepareUpgrade(context.Background(), filepath.Join(t.TempDir(), "none.db"), t.TempDir(), "crowsnest", "v1.0.0", time.Now())
	if err != nil || check.BackupPath != "" {
		t.Fatalf("%+v, %v", check, err)
	}
}

func TestPrepareUpgradeRefusesANewerDatabase(t *testing.T) {
	dbPath := openedDatabase(t, "v9.0.0")
	raw, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO schema_migrations (version, name) VALUES (999, '0999_future.sql')`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	dir := t.TempDir()
	_, err = PrepareUpgrade(context.Background(), dbPath, dir, "crowsnest", "v1.0.0", time.Now())
	if !errors.Is(err, ErrNewerDatabase) {
		t.Fatalf("err = %v, want ErrNewerDatabase", err)
	}
	if copies, _ := UpgradeBackups(dir, "crowsnest"); len(copies) != 0 {
		t.Errorf("copied a database it refused: %v", copies)
	}
}
