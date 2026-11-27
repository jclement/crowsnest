// Tests for the day-thirty wiring: the start held after a restore, the version record behind
// "what changed", and the scheduled backup's rotation.

package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/backup"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/store"
)

func opsStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "crowsnest-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestRestoreHoldShutsTheGateUntilResumed(t *testing.T) {
	ctx := context.Background()
	db := opsStore(t)
	hold := restoreHold{db: db}
	open := func(context.Context) string { return "" }
	if hold.held(ctx) || hold.gate(open)(ctx) != "" {
		t.Fatal("held with no pause row")
	}
	if err := db.SetKV(ctx, backup.PauseKey, `{"paused":true,"by":"you","at":"2026-11-27T00:00:00Z"}`); err != nil {
		t.Fatal(err)
	}
	if hold.held(ctx) {
		t.Error("a pause the human set is not the restore's to hold or lift")
	}
	if err := db.SetKV(ctx, backup.PauseKey, `{"paused":true,"by":"restore","at":"2026-11-27T00:00:00Z","reason":"restored"}`); err != nil {
		t.Fatal(err)
	}
	if !hold.held(ctx) || !strings.Contains(hold.gate(open)(ctx), "Resume") {
		t.Fatal("a restore's pause did not shut the orchestrator's gate")
	}
	waitCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if hold.wait(waitCtx) {
		t.Error("wait went on while held")
	}
	if err := hold.resume(ctx); err != nil || hold.held(ctx) {
		t.Fatalf("resume: %v, still held %v", err, hold.held(ctx))
	}
}

func TestRecordVersionAndUpdateNotice(t *testing.T) {
	ctx := context.Background()
	db := opsStore(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ops := &opsWiring{db: db}
	saved := version
	t.Cleanup(func() { version = saved })

	version = "v1.0.0"
	recordVersion(ctx, db, logger)
	if ops.Updated(ctx) != nil {
		t.Error("a first start shows an update notice")
	}
	version = "v1.1.0"
	recordVersion(ctx, db, logger)
	notice := ops.Updated(ctx)
	if notice == nil || notice.From != "v1.0.0" || notice.To != "v1.1.0" || notice.ReleaseURL != releaseURLBase+"v1.1.0" {
		t.Fatalf("notice %+v", notice)
	}
	if err := ops.DismissUpdated(ctx); err != nil || ops.Updated(ctx) != nil {
		t.Errorf("dismissed notice still shown: %v", err)
	}
}

func TestScheduledBackupWritesAndRotates(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "crowsnest-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := config.Config{DataDir: dir, StateDir: dir, Session: "crowsnest-test", Dir: filepath.Join(dir, "orchestrator"), BackupEvery: time.Hour, BackupKeep: 2}
	scheduler := backupScheduler{cfg: func() config.Config { return cfg }, db: db, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	start := time.Now()
	for i := 0; i < 4; i++ {
		at := start.Add(time.Duration(i) * 2 * time.Hour)
		scheduler.tick(ctx, at)
		// The scheduler dates backups by file time: make each look as old as its tick.
		entries, _ := os.ReadDir(cfg.BackupDir())
		for _, entry := range entries {
			if strings.Contains(entry.Name(), backup.Stamp(at)) {
				_ = os.Chtimes(filepath.Join(cfg.BackupDir(), entry.Name()), at, at)
			}
		}
	}
	scheduler.tick(ctx, start.Add(6*time.Hour+time.Minute)) // not due: the newest is a minute old
	entries, _ := os.ReadDir(cfg.BackupDir())
	if len(entries) != 2 {
		t.Fatalf("%d automatic backups kept, want 2", len(entries))
	}
	if _, err := backup.Verify(filepath.Join(cfg.BackupDir(), entries[1].Name())); err != nil {
		t.Errorf("the newest automatic backup does not verify: %v", err)
	}
}
