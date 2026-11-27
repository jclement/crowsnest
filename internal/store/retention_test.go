// Tests for the retention queries: archiving, search, deleting only settled jobs with what is
// theirs, sweeps that count exactly what they delete, and pinned evidence kept.

package store

import (
	"context"
	"testing"
	"time"
)

func retentionStore(t *testing.T) (*Store, time.Time) {
	t.Helper()
	ctx := context.Background()
	db := openTest(t)
	now := time.Date(2026, 11, 27, 3, 0, 0, 0, time.UTC)
	old := now.Add(-400 * 24 * time.Hour)
	insert := func(record JobRecord) int64 {
		id, err := db.InsertJob(ctx, record)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	insert(JobRecord{Title: "old cancelled", BriefMD: "b", State: "cancelled", CreatedAt: old, SettledAt: old})
	insert(JobRecord{Title: "old done in a worktree", BriefMD: "b", State: "done", Isolate: true, WorktreeBranch: "cn/j2", CreatedAt: old, SettledAt: old})
	insert(JobRecord{Title: "still running", BriefMD: "needle in the brief", State: "running", CreatedAt: old})
	insert(JobRecord{Title: "fresh done", BriefMD: "b", State: "done", CreatedAt: now, SettledAt: now.Add(-time.Hour)})
	exec := func(query string, args ...any) {
		if _, err := db.db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec(`INSERT INTO job_events (job_id, at, kind, summary, detail_json) VALUES (1, ?, 'state', 's', '{}')`, old.Unix())
	exec(`INSERT INTO questions (title, body_md, kind, options_json, context_json, files_json, status, job_id, created_at, expires_at, settled_at)
		VALUES ('q of j_1', '', 'single', '[]', '{}', '[]', 'answered', 'j_1', ?, ?, ?)`, old.Unix(), old.Unix(), old.Unix())
	exec(`INSERT INTO questions (title, body_md, kind, options_json, context_json, files_json, status, job_id, created_at, expires_at, settled_at)
		VALUES ('loose old', '', 'single', '[]', '{}', '[]', 'expired', '', ?, ?, ?)`, old.Unix(), old.Unix(), old.Unix())
	exec(`INSERT INTO questions (title, body_md, kind, options_json, context_json, files_json, status, job_id, created_at, expires_at)
		VALUES ('loose open', '', 'single', '[]', '{}', '[]', 'open', '', ?, ?)`, old.Unix(), now.Add(time.Hour).Unix())
	exec(`INSERT INTO journal (at, kind, summary, detail_json) VALUES (?, 'poke', 'old', '{}'), (?, 'poke', 'new', '{}')`, old.Unix(), now.Unix())
	for _, record := range []AttachmentRecord{
		{ID: "a_1", Name: "evidence.png", SHA256: "sha-evidence", Size: 10, OwnerKind: "job", OwnerID: "j_4", Role: "result", CreatedAt: old, ReleasedAt: old},
		{ID: "a_2", Name: "pasted.png", SHA256: "sha-pasted", Size: 20, OwnerKind: "job", OwnerID: "j_4", Role: "message", CreatedAt: old, ReleasedAt: old},
		{ID: "a_3", Name: "j1.txt", SHA256: "sha-j1", Size: 5, OwnerKind: "job", OwnerID: "j_1", Role: "brief", CreatedAt: old, ReleasedAt: old},
		{ID: "a_4", Name: "never sent.txt", SHA256: "sha-pending", Size: 5, CreatedAt: old},
	} {
		if err := db.InsertAttachment(ctx, record); err != nil {
			t.Fatal(err)
		}
		if record.OwnerKind != "" {
			exec(`UPDATE attachments SET owner_kind = ?, owner_id = ?, role = ?, attached_at = ?, released_at = ? WHERE id = ?`,
				record.OwnerKind, record.OwnerID, record.Role, record.CreatedAt.Unix(), record.ReleasedAt.Unix(), record.ID)
		}
	}
	return db, now
}

func TestArchiveAndSearch(t *testing.T) {
	ctx := context.Background()
	db, now := retentionStore(t)
	cutoff := now.Add(-14 * 24 * time.Hour)
	if count, _ := db.CountArchivable(ctx, cutoff); count != 2 {
		t.Fatalf("archivable = %d, want the two old settled jobs", count)
	}
	archived, err := db.ArchiveSettledJobs(ctx, cutoff, now)
	if err != nil || archived != 2 {
		t.Fatalf("archived %d, %v", archived, err)
	}
	if again, _ := db.ArchiveSettledJobs(ctx, cutoff, now); again != 0 {
		t.Errorf("archiving twice archived %d more", again)
	}
	ids, _ := db.ArchivedJobIDs(ctx)
	if !ids[1] || !ids[2] || ids[3] || ids[4] {
		t.Errorf("archived set %v", ids)
	}
	found, err := db.SearchJobIDs(ctx, "WORKTREE", true, 10)
	if err != nil || len(found) != 1 || found[0] != 2 {
		t.Errorf("search archived for worktree: %v, %v", found, err)
	}
	if found, _ := db.SearchJobIDs(ctx, "needle", false, 10); len(found) != 1 || found[0] != 3 {
		t.Errorf("search all for a brief's text: %v", found)
	}
	if found, _ := db.SearchJobIDs(ctx, "100%_", false, 10); len(found) != 0 {
		t.Errorf("LIKE wildcards in a query were not escaped: %v", found)
	}
}

func TestDeleteJobsTakesOnlySettledJobsAndWhatIsTheirs(t *testing.T) {
	ctx := context.Background()
	db, _ := retentionStore(t)
	deleted, orphans, err := db.DeleteJobs(ctx, []int64{1, 3})
	if err != nil || deleted != 1 {
		t.Fatalf("deleted %d, %v; want only the settled job", deleted, err)
	}
	if len(orphans) != 1 || orphans[0] != "sha-j1" {
		t.Errorf("orphans %v, want j_1's blob", orphans)
	}
	if exists, _, _ := db.JobExists(ctx, 3); !exists {
		t.Error("a running job was deleted")
	}
	for query, want := range map[string]int{
		`SELECT COUNT(*) FROM job_events WHERE job_id = 1`:        0,
		`SELECT COUNT(*) FROM questions WHERE job_id = 'j_1'`:     0,
		`SELECT COUNT(*) FROM questions WHERE job_id = ''`:        2,
		`SELECT COUNT(*) FROM attachments WHERE owner_id = 'j_1'`: 0,
	} {
		if got, _ := db.count(ctx, query); got != want {
			t.Errorf("%s = %d, want %d", query, got, want)
		}
	}
}

func TestSweepsCountWhatTheyDeleteAndKeepEvidence(t *testing.T) {
	ctx := context.Background()
	db, now := retentionStore(t)
	sweeps := Sweeps(now.Add(-180*24*time.Hour), now.Add(-24*time.Hour), now.Add(-30*24*time.Hour), now)
	want := map[string]int{"questions": 1, "journal": 1, "attachments_pending": 1, "attachments_released": 2}
	for _, sweep := range sweeps {
		counted, err := db.CountSweep(ctx, sweep)
		if err != nil {
			t.Fatal(err)
		}
		removed, _, err := db.RunSweep(ctx, sweep)
		if err != nil {
			t.Fatal(err)
		}
		if counted != removed || removed != want[sweep.Name] {
			t.Errorf("%s: counted %d, removed %d, want %d", sweep.Name, counted, removed, want[sweep.Name])
		}
	}
	if exists, _ := db.AttachmentShaExists(ctx, "sha-evidence"); !exists {
		t.Error("an attachment pinned as evidence was swept")
	}
	if got, _ := db.count(ctx, `SELECT COUNT(*) FROM questions WHERE status = 'open'`); got != 1 {
		t.Error("an open question was swept")
	}
}

func TestInventoryCountsAndDates(t *testing.T) {
	ctx := context.Background()
	db, _ := retentionStore(t)
	uses, err := db.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, use := range uses {
		if use.Table == "jobs" && (use.Rows != 4 || use.Oldest.IsZero()) {
			t.Errorf("jobs inventory %+v", use)
		}
	}
	if total, _ := db.AttachmentBytes(ctx); total != 40 {
		t.Errorf("attachment bytes %d, want 40", total)
	}
}
