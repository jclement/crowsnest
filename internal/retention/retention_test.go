// Tests for the retention service: the preview is exactly the run, a job that may hold unlanded
// work is kept until confirmed (never by the nightly run), state folders go with their jobs, and
// the nightly schedule.

package retention

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

func service(t *testing.T) (*Service, *store.Store, string, time.Time) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "crowsnest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Date(2026, 11, 27, 3, 30, 0, 0, time.Local)
	old := now.Add(-400 * 24 * time.Hour)
	for _, record := range []store.JobRecord{
		{Title: "cancelled long ago", BriefMD: "b", State: "cancelled", CreatedAt: old, SettledAt: old},
		{Title: "done on a branch", BriefMD: "b", State: "done", WorktreeBranch: "cn/j2", Isolate: true, CreatedAt: old, SettledAt: old},
		{Title: "running", BriefMD: "b", State: "running", CreatedAt: old},
		{Title: "landed on a branch", BriefMD: "b", State: "done", WorktreeBranch: "cn/j4", Isolate: true, CreatedAt: old, SettledAt: old, LandState: "merged"},
	} {
		if _, err := db.InsertJob(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	state := filepath.Join(dir, "state")
	for _, name := range []string{"j_1", "j_2", "j_3", "j_99"} {
		path := filepath.Join(state, "jobs", name)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "brief.md"), []byte("brief"), 0o400); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(path, old, old)
	}
	svc := New(Options{
		Store: db, StateDir: state, AttachmentsDir: filepath.Join(dir, "attachments"), DatabasePath: filepath.Join(dir, "crowsnest.db"),
		BackupDir: filepath.Join(dir, "backups"), Policy: func() Policy { return Policy{ArchiveAfterDays: 14, DeleteAfterDays: 180} },
		Now: func() time.Time { return now },
	})
	return svc, db, state, now
}

func TestPlanIsTheRunAndHeldJobsNeedConfirmation(t *testing.T) {
	ctx := context.Background()
	svc, db, state, _ := service(t)
	plan, err := svc.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Archive != 3 || plan.DeleteJobs != 2 || len(plan.Held) != 1 || plan.Held[0].ID != "j_2" || plan.StateDirs != 3 {
		t.Fatalf("plan %+v", plan)
	}
	if exists, _, _, _ := db.JobExists(ctx, 1); !exists {
		t.Fatal("the preview deleted a job")
	}
	run, err := svc.Run(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.DeleteJobs != plan.DeleteJobs || len(run.Held) != 1 || run.StateDirs != plan.StateDirs || run.Archive != plan.Archive {
		t.Errorf("run %+v differs from the plan %+v", run, plan)
	}
	if exists, _, _, _ := db.JobExists(ctx, 2); !exists {
		t.Fatal("a job that may hold unlanded work was deleted without confirmation")
	}
	if exists, _, _, _ := db.JobExists(ctx, 3); !exists {
		t.Fatal("a running job was deleted")
	}
	// j_2's folder goes even though the job is held: it holds the brief and files given to the
	// agent, not the work (that is in the worktree, which this never touches).
	for name, want := range map[string]bool{"j_1": false, "j_99": false, "j_2": false, "j_3": true} {
		_, err := os.Stat(filepath.Join(state, "jobs", name))
		if (err == nil) != want {
			t.Errorf("state folder %s present = %v, want %v", name, err == nil, want)
		}
	}
	confirmed, err := svc.Run(ctx, []string{"j_2"})
	if err != nil || confirmed.Confirmed != 1 {
		t.Fatalf("confirmed run %+v, %v", confirmed, err)
	}
	if exists, _, _, _ := db.JobExists(ctx, 2); exists {
		t.Error("a confirmed held job was kept")
	}
	if svc.LastRun(ctx) == nil {
		t.Error("the run was not recorded")
	}
}

func TestNothingIsDeletedWhenDeletingIsOff(t *testing.T) {
	ctx := context.Background()
	svc, db, _, _ := service(t)
	svc.opts.Policy = func() Policy { return Policy{ArchiveAfterDays: 14} }
	if _, err := svc.Run(ctx, []string{"j_2"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{1, 2, 3} {
		if exists, _, _, _ := db.JobExists(ctx, id); !exists {
			t.Errorf("j_%d deleted with delete-after-days 0", id)
		}
	}
}

func TestNightlySchedule(t *testing.T) {
	ctx := context.Background()
	svc, _, _, now := service(t)
	if !svc.due(ctx, now) {
		t.Error("never run, in the 03:00 hour: not due")
	}
	if svc.due(ctx, now.Add(5*time.Hour)) {
		t.Error("never run, at 08:30: due")
	}
	if _, err := svc.Run(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if svc.due(ctx, now.Add(time.Hour)) {
		t.Error("due an hour after a run")
	}
	if !svc.due(ctx, now.Add(24*time.Hour)) {
		t.Error("not due the next night")
	}
	if !svc.due(ctx, now.Add(49*time.Hour+5*time.Hour)) {
		t.Error("not due after two days at another hour")
	}
	svc.opts.Paused = func() bool { return true }
	if svc.due(ctx, now.Add(49*time.Hour+5*time.Hour)) {
		t.Error("due while the pause switch is on")
	}
}

func TestStorageReport(t *testing.T) {
	svc, _, _, _ := service(t)
	report, err := svc.Storage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]Area{}
	for _, area := range report.Areas {
		found[area.Name] = area
	}
	if found["database"].Bytes == 0 || found["job_dirs"].Count != 4 || report.Worktrees == "" {
		t.Errorf("storage %+v", report)
	}
}
