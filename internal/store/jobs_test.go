package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	if _, err := db.GetJob(ctx, 99); !errors.Is(err, ErrNotFound) {
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

// R2-C12: what the supervisor reads about a job's questions never carries their attachments, and
// the counts and the answer-incoming time come from one aggregate query.
func TestJobQuestionsLeaveTheFilesBehind(t *testing.T) {
	ctx := context.Background()
	db, err := OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	big := strings.Repeat("x", 256<<10)
	files := json.RawMessage(`[{"filename":"a.go","original":"` + big + `","final":"y"}]`)
	at := time.Unix(1_800_000_000, 0)
	insert := func(wait bool) int64 {
		id, err := db.InsertQuestion(ctx, QuestionRecord{Title: "t", Kind: "ack", Urgency: "normal", JobID: "j_1", Wait: wait,
			Files: files, CreatedAt: at, ExpiresAt: at.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	insert(true) // open, blocking
	delivered, failed := insert(false), insert(false)
	for _, id := range []int64{delivered, failed} {
		if err := db.SettleQuestion(ctx, id, "answered", json.RawMessage(`"ack"`), "", "type", at.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.FinishDelivery(ctx, delivered, at.Add(2*time.Minute), ""); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishDelivery(ctx, failed, at.Add(3*time.Minute), "typing failed"); err != nil {
		t.Fatal(err)
	}
	list, err := db.ListQuestionsByJob(ctx, "j_1")
	if err != nil || len(list) != 3 {
		t.Fatalf("list %d %v", len(list), err)
	}
	for _, q := range list {
		if string(q.Files) != "[]" || q.FileCount != 1 {
			t.Fatalf("question %d came with %d bytes of files (count %d)", q.ID, len(q.Files), q.FileCount)
		}
	}
	summary, err := db.SummarizeJobQuestions(ctx, "j_1")
	if err != nil || summary.Open != 1 || summary.Blocking != 1 || !summary.AnswerIncomingAt.Equal(at.Add(2*time.Minute)) {
		t.Fatalf("summary %+v %v (the failed delivery must not count)", summary, err)
	}
}
