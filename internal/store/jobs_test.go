package store

import (
	"context"
	"testing"
	"time"
)

func TestJobRoundTripAndTimeline(t *testing.T) {
	ctx := context.Background()
	db, err := OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	created := time.Unix(1_700_000_000, 0)
	id, err := db.InsertJob(ctx, JobRecord{Title: "t", BriefMD: "b", Role: "builder", Engine: "claude", State: "queued",
		VerifiedJSON: "[]", NotVerifiedJSON: "[]", CreatedAt: created})
	if err != nil {
		t.Fatal(err)
	}
	job, err := db.GetJob(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if job.Title != "t" || !job.CreatedAt.Equal(created) || !job.StartedAt.IsZero() {
		t.Fatalf("round trip: %+v", job)
	}
	job.State, job.Stuck, job.Nudges, job.RetryAt = "running", true, 2, created.Add(time.Minute)
	if err := db.UpdateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	again, _ := db.GetJob(ctx, id)
	if again.State != "running" || !again.Stuck || again.Nudges != 2 || !again.RetryAt.Equal(created.Add(time.Minute)) {
		t.Fatalf("update: %+v", again)
	}
	if _, err := db.GetJob(ctx, 99); err != ErrNotFound {
		t.Fatalf("missing job: %v", err)
	}
	list, err := db.ListJobs(ctx, []string{"running"}, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list %v %v", list, err)
	}
	for i := range 3 {
		if _, err := db.InsertJobEvent(ctx, JobEventRecord{JobID: id, At: created, Kind: "nudge", Summary: "n", DetailJSON: `{"q":"q_` + string(rune('1'+i)) + `"}`}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := db.ListJobEvents(ctx, id, 2)
	if err != nil || len(events) != 2 || events[0].ID >= events[1].ID {
		t.Fatalf("events %v %v", events, err)
	}
	if found, _ := db.HasJobEvent(ctx, id, "nudge", `"q_2"`); !found {
		t.Fatal("HasJobEvent missed")
	}
	if found, _ := db.HasJobEvent(ctx, id, "nudge", `"q_9"`); found {
		t.Fatal("HasJobEvent false positive")
	}
}
