// Tests for migration 0012's tables: projects (unique slug, one live project per directory,
// archive), instruction layers with revisions, pruning and reset, agent records, schedules and
// their run history.

package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	db, err := OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestProjectsUniqueAndArchive(t *testing.T) {
	ctx := context.Background()
	db := openTest(t)
	now := time.Unix(1_800_000_000, 0)
	first := ProjectRecord{Slug: "alpha", Name: "Alpha", Dir: "/w/a", RootDir: "/w/a", DefaultIsolate: -1, LeadStart: "manual", CreatedAt: now, UpdatedAt: now}
	id, err := db.InsertProject(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertProject(ctx, ProjectRecord{Slug: "alpha", Name: "x", Dir: "/w/b", RootDir: "/w/b", CreatedAt: now, UpdatedAt: now}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate slug: %v", err)
	}
	if _, err := db.InsertProject(ctx, ProjectRecord{Slug: "beta", Name: "x", Dir: "/w/a", RootDir: "/w/a", CreatedAt: now, UpdatedAt: now}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate live root: %v", err)
	}
	got, err := db.GetProject(ctx, id)
	if err != nil || got.Name != "Alpha" || got.DefaultIsolate != -1 {
		t.Fatalf("get: %+v %v", got, err)
	}
	got.ArchivedAt = now
	if err := db.UpdateProject(ctx, got); err != nil {
		t.Fatal(err)
	}
	// An archived project frees its directory.
	if _, err := db.InsertProject(ctx, ProjectRecord{Slug: "beta", Name: "Beta", Dir: "/w/a", RootDir: "/w/a", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("root after archive: %v", err)
	}
	live, _ := db.ListProjects(ctx, false)
	all, _ := db.ListProjects(ctx, true)
	if len(live) != 1 || live[0].Slug != "beta" || len(all) != 2 {
		t.Fatalf("live %d all %d", len(live), len(all))
	}
}

func TestJobsCarryProjectAndLeadQueue(t *testing.T) {
	ctx := context.Background()
	db := openTest(t)
	now := time.Now()
	unfiled, _ := db.InsertJob(ctx, JobRecord{Title: "u", State: "queued", CreatedAt: now})
	filed, _ := db.InsertJob(ctx, JobRecord{Title: "p", State: "queued", ProjectID: 7, CreatedAt: now})
	_, _ = db.InsertJob(ctx, JobRecord{Title: "n", State: "needs_you", ProjectID: 7, CreatedAt: now})
	if job, ok, _ := db.OldestUnassignedQueued(ctx); !ok || job.ID != unfiled {
		t.Fatalf("orchestrator takes the unfiled job, got %+v", job)
	}
	if job, ok, _ := db.OldestUnassignedQueuedIn(ctx, 7); !ok || job.ID != filed || job.ProjectID != 7 {
		t.Fatalf("lead takes its project's job, got %+v", job)
	}
	counts, err := db.CountJobsByProject(ctx)
	if err != nil || counts[7].NeedsYou != 1 || counts[7].Running != 1 || counts[0].Total != 1 {
		t.Fatalf("counts %+v %v", counts, err)
	}
}

func TestInstructionLayerRevisions(t *testing.T) {
	ctx := context.Background()
	db := openTest(t)
	at := time.Unix(1_800_000_000, 0)
	for i, body := range []string{"one", "two", "three"} {
		rev, err := db.SaveInstructionLayer(ctx, "role", "builder", body, false, at, 2)
		if err != nil || rev != i+1 {
			t.Fatalf("rev %d err %v", rev, err)
		}
	}
	layer, err := db.GetInstructionLayer(ctx, "role", "builder")
	if err != nil || layer.BodyMD != "three" || layer.Revision != 3 {
		t.Fatalf("layer %+v %v", layer, err)
	}
	revisions, _ := db.ListInstructionRevisions(ctx, "role", "builder")
	if len(revisions) != 2 || revisions[0].Revision != 3 {
		t.Fatalf("kept %d revisions, want the newest 2: %+v", len(revisions), revisions)
	}
	if _, err := db.SaveInstructionLayer(ctx, "role", "builder", "", true, at, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetInstructionLayer(ctx, "role", "builder"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reset leaves no layer, got %v", err)
	}
	old, err := db.GetInstructionRevision(ctx, "role", "builder", 3)
	if err != nil || old.BodyMD != "three" {
		t.Fatalf("revision 3 kept for restore: %+v %v", old, err)
	}
}

func TestAgentRecords(t *testing.T) {
	ctx := context.Background()
	db := openTest(t)
	at := time.Unix(1_800_000_000, 0)
	if err := db.SetAgentLayers(ctx, AgentLayers{Subject: "j_1", StartedAt: at, Hash: "h1", LayersJSON: "[]"}); err != nil {
		t.Fatal(err)
	}
	_ = db.SetAgentLayers(ctx, AgentLayers{Subject: "j_1", StartedAt: at, Hash: "h2", LayersJSON: "[]"})
	if got, _ := db.GetAgentLayers(ctx, "j_1"); got.Hash != "h2" {
		t.Fatalf("replaced: %+v", got)
	}
	_ = db.RecordLoadedInstruction(ctx, LoadedInstruction{Subject: "j_1", SessionID: "s1", At: at, Path: "/a/CLAUDE.md", MemoryType: "Project"})
	_ = db.RecordLoadedInstruction(ctx, LoadedInstruction{Subject: "j_1", SessionID: "s2", At: at, Path: "/a/CLAUDE.md", MemoryType: "Project"})
	_ = db.RecordLoadedInstruction(ctx, LoadedInstruction{Subject: "j_1", SessionID: "s2", At: at, Path: "/a/CLAUDE.md", Size: 9})
	_ = db.RecordLoadedInstruction(ctx, LoadedInstruction{Subject: "j_1", SessionID: "s2", At: at, Path: "/home/.claude/CLAUDE.md", MemoryType: "User"})
	loaded, err := db.ListLoadedInstructions(ctx, "j_1")
	if err != nil || len(loaded) != 2 || loaded[0].Size != 9 || loaded[0].SessionID != "s2" {
		t.Fatalf("latest session only, deduplicated: %+v %v", loaded, err)
	}
}

func TestSchedulesAndRuns(t *testing.T) {
	ctx := context.Background()
	db := openTest(t)
	now := time.Unix(1_800_000_000, 0)
	id, err := db.InsertSchedule(ctx, ScheduleRecord{Name: "tidy", Every: "1h", RunAs: "poke", BodyMD: "Tidy up.", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertSchedule(ctx, ScheduleRecord{Name: "tidy", Every: "1h", BodyMD: "x"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name: %v", err)
	}
	record, _ := db.GetScheduleByName(ctx, "tidy")
	record.Every, record.ProjectID = "2h", 3
	if err := db.UpdateSchedule(ctx, record); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetScheduleByName(ctx, "tidy"); got.Every != "2h" || got.ProjectID != 3 || got.ID != id {
		t.Fatalf("updated: %+v", got)
	}
	for i := range 5 {
		_ = db.AddScheduleRun(ctx, ScheduleRun{Name: "tidy", At: now.Add(time.Duration(i) * time.Minute), Outcome: "ok"}, 3)
	}
	runs, _ := db.ListScheduleRuns(ctx, "tidy", 10)
	if len(runs) != 3 || !runs[0].At.Equal(now.Add(4*time.Minute)) {
		t.Fatalf("newest 3 kept: %+v", runs)
	}
	if err := db.DeleteSchedule(ctx, id); err != nil {
		t.Fatal(err)
	}
	if list, _ := db.ListSchedules(ctx); len(list) != 0 {
		t.Fatalf("deleted: %+v", list)
	}
}
