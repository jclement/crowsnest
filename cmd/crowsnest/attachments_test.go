package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/attach"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
)

// TestSettledOwnersReleaseTheirFiles: a job reaching a terminal state (by its event, or found
// by the reconcile pass after a restart) and a settled question release their files.
func TestSettledOwnersReleaseTheirFiles(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := config.Config{DataDir: t.TempDir(), StateDir: t.TempDir(), AttachmentMaxMB: 1, AttachmentQuotaMB: 10}
	files, err := newAttachments(cfg, db, hub.New(), &jobRefs{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	jobService := jobs.New(jobs.Options{Store: db})
	files.jobs = jobService
	questionService := questions.New(questions.Options{Store: db})

	job, err := jobService.Create(ctx, jobs.CreateInput{Brief: "x"}, "")
	if err != nil {
		t.Fatal(err)
	}
	upload := func(name string) attach.Attachment {
		t.Helper()
		file, err := files.service.Upload(ctx, name, strings.NewReader("data"))
		if err != nil {
			t.Fatal(err)
		}
		return file
	}
	first := upload("a.txt")
	if err := files.AttachBrief(ctx, jobs.Job{ID: job.ID, Dir: "/somewhere"}, []string{first.ID}); err != nil {
		t.Fatal(err)
	}
	if lines := files.BriefLines(ctx, job.ID); len(lines) != 1 || !strings.Contains(lines[0], filepath.Join(cfg.StateDir, "jobs", job.ID, "attachments")) {
		t.Fatalf("brief lines %q", lines)
	}
	// The job is cancelled while nobody is listening; the reconcile pass finds it.
	if _, err := jobService.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	files.reconcile(ctx, questionService)
	if list, _ := files.service.List(ctx, attach.OwnerJob, job.ID, ""); len(list) != 1 || list[0].ReleasedAt == nil {
		t.Fatalf("the cancelled job's files were not released: %+v", list)
	}

	// A question's files are released by its settling event.
	question, _ := questionService.Ask(ctx, questions.AskInput{Title: "Look", Kind: questions.KindAck}, questions.Caller{PaneID: "w1:p1"})
	second := upload("b.txt")
	if err := files.service.Adopt(ctx, []string{second.ID}, attach.OwnerQuestion, question.ID, attach.RoleAsk); err != nil {
		t.Fatal(err)
	}
	files.release(ctx, hub.Event{Type: "question", Data: questions.Event{ID: question.ID, Status: questions.StatusOpen}})
	if got, _ := files.service.Get(ctx, second.ID); got.ReleasedAt != nil {
		t.Fatal("an open question's files were released")
	}
	files.release(ctx, hub.Event{Type: "question", Data: questions.Event{ID: question.ID, Status: questions.StatusExpired}})
	if got, _ := files.service.Get(ctx, second.ID); got.ReleasedAt == nil {
		t.Fatal("an expired question's files were not released")
	}
}
