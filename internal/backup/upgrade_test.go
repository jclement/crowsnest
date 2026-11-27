// Tests for the upgrade path: a copy before a version change or a migration, three kept, and a
// database from a newer crowsnest refused with what to do.

package backup

import (
	"context"
	"database/sql"
	"errors"
	"os"
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

// TestUpgradeCopyIsScrubbed is R5-S8: the pre-migration copy carries none of the credentials a
// backup leaves out, and no staging directory is left behind.
func TestUpgradeCopyIsScrubbed(t *testing.T) {
	ctx := context.Background()
	dbPath := openedDatabase(t, "v1.0.0")
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	mustDo(t, db.SetKV(ctx, "push.vapid", "private-key"))
	mustDo(t, db.SetKV(ctx, "settings.notify-url", "https://ntfy.sh/secret-topic"))
	mustDo(t, db.Close())
	dir := filepath.Join(t.TempDir(), "backups")
	check, err := PrepareUpgrade(ctx, dbPath, dir, "crowsnest", "v1.1.0", time.Date(2026, 11, 27, 1, 0, 0, 0, time.UTC))
	if err != nil || check.BackupPath == "" {
		t.Fatalf("%+v %v", check, err)
	}
	rows, err := store.ReadKVPrefix(ctx, check.BackupPath, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"push.vapid", "settings.notify-url"} {
		if _, ok := rows[key]; ok {
			t.Errorf("the pre-upgrade copy kept %s", key)
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if entry.IsDir() {
			t.Errorf("a staging directory was left: %s", entry.Name())
		}
	}
}
