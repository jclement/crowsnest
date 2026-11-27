// Tests for an all-or-nothing restore (R5-C04), its size caps (R5-S9) and the task approvals a
// restore forgets (R5-S3).

package backup

import (
	"archive/tar"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// TestFailedRestoreLeavesTheOldData: a forced restore over a working instance fails while
// placing a config file (its directory cannot be written). The old database is still the one in
// place, nothing is half-installed, and no temporary is left behind.
func TestFailedRestoreLeavesTheOldData(t *testing.T) {
	ctx := context.Background()
	src, dir := populated(t)
	path := filepath.Join(dir, "backup.tar.gz")
	if _, err := Create(ctx, src, path); err != nil {
		t.Fatal(err)
	}
	target := freshTarget(t)
	if _, err := Restore(ctx, path, target); err != nil {
		t.Fatal(err)
	}
	// The instance moves on: a job only the current database has.
	db, err := store.Open(ctx, target.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertJob(ctx, store.JobRecord{Title: "Only in the current database", State: "queued", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	mustDo(t, db.Close())
	roles := filepath.Join(target.ConfigDir, "roles")
	mustDo(t, os.Chmod(roles, 0o500)) // the role file cannot be placed
	t.Cleanup(func() { _ = os.Chmod(roles, 0o700) })

	target.Force = true
	if _, err := Restore(ctx, path, target); err == nil {
		t.Fatal("the restore succeeded although a file could not be placed")
	}
	db, err = store.Open(ctx, target.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	jobs, err := db.ListJobs(ctx, nil, 10)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("the old database is not the one in place: %d jobs, %v", len(jobs), err)
	}
	entries, _ := os.ReadDir(filepath.Dir(target.DatabasePath))
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "before-restore") || strings.HasPrefix(entry.Name(), ".restore-") {
			t.Errorf("a failed restore left %s", entry.Name())
		}
	}
	for _, d := range []string{target.ConfigDir, roles} {
		entries, _ := os.ReadDir(d)
		for _, entry := range entries {
			if strings.Contains(entry.Name(), ".restore-") {
				t.Errorf("a temporary was left in %s: %s", d, entry.Name())
			}
		}
	}
}

// TestRestoredDatabaseIsPausedAndForgetsTaskApprovals: the database that reaches its place is
// paused and pending, and carries no task approvals from where it was made.
func TestRestoredDatabaseIsPausedAndForgetsTaskApprovals(t *testing.T) {
	ctx := context.Background()
	src, dir := populated(t)
	mustDo(t, src.DB.ApproveTaskFile(ctx, "/home/x/tasks/sync.md", "abc", time.Now()))
	path := filepath.Join(dir, "backup.tar.gz")
	if _, err := Create(ctx, src, path); err != nil {
		t.Fatal(err)
	}
	target := freshTarget(t)
	if _, err := Restore(ctx, path, target); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, target.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	assertPausedAndPending(t, db)
	if ok, _ := db.TaskReviewIsApproved(ctx, "/home/x/tasks/sync.md", "abc"); ok {
		t.Fatal("a restored database arrived with a task file pre-approved")
	}
}

// TestRestoreRefusesAnOversizedManifest is R5-S9: a manifest promising more than a restore
// extracts is refused before anything is staged.
func TestRestoreRefusesAnOversizedManifest(t *testing.T) {
	ctx := context.Background()
	src, dir := populated(t)
	path := filepath.Join(dir, "backup.tar.gz")
	if _, err := Create(ctx, src, path); err != nil {
		t.Fatal(err)
	}
	bad := rewrite(t, path, func(h *tar.Header, b []byte) (*tar.Header, []byte) {
		if h.Name != manifestName {
			return h, b
		}
		var manifest Manifest
		mustDo(t, json.Unmarshal(b, &manifest))
		for i := 0; i < 5; i++ {
			manifest.Files = append(manifest.Files, File{Path: attachPrefix + strings.Repeat(string(rune('a'+i)), 64), Size: maxEntryBytes, SHA256: strings.Repeat("0", 64)})
		}
		out, _ := json.Marshal(manifest)
		return h, out
	})
	target := freshTarget(t)
	if _, err := Restore(ctx, bad, target); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("an oversized manifest: %v", err)
	}
	if _, err := os.Stat(target.DatabasePath); err == nil {
		t.Error("a refused restore wrote the database")
	}
}
