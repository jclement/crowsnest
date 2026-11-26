package agentapi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/attach"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
)

// attachFixture serves the socket with attachments on; the agent in pane w1:p1 may attach files
// from work, nobody else from anywhere.
func attachFixture(t *testing.T) (*socketFixture, *attach.Service, string) {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := attach.New(attach.Options{Store: db, Dir: filepath.Join(t.TempDir(), "blobs")})
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	f := startSocket(t, func(options *ServerOptions) {
		options.Attachments = service
		options.AttachRoots = func(_ context.Context, caller questions.Caller) []string {
			if caller.PaneID == "w1:p1" {
				return []string{work}
			}
			return nil
		}
	})
	return f, service, work
}

func TestAskWithAttachmentsInsideTheAgentsDirectory(t *testing.T) {
	f, service, work := attachFixture(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(work, "shot.png"), []byte("\x89PNG\r\n\x1a\nfake"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work) // the client makes a relative path absolute against the agent's directory
	client := NewClient(f.socket, f.token(t, "w1:p1", ""))
	asked, err := client.Ask(ctx, questions.AskInput{Title: "Does this look right?", Kind: questions.KindAck, Attachments: []string{"shot.png"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(asked.Attachments) != 1 || asked.Attachments[0].Type != "image/png" || asked.Attachments[0].Origin != "agent" || asked.Attachments[0].OwnerID != asked.ID {
		t.Fatalf("question attachments %+v", asked.Attachments)
	}
	listed, _ := service.List(ctx, attach.OwnerQuestion, asked.ID, attach.RoleAsk)
	if len(listed) != 1 || listed[0].Name != "shot.png" {
		t.Fatalf("stored %+v", listed)
	}
}

func TestAskRefusesFilesOutsideAndCallersWithoutAToken(t *testing.T) {
	f, _, work := attachFixture(t)
	ctx := context.Background()
	outside := filepath.Join(t.TempDir(), "id_ed25519")
	_ = os.WriteFile(outside, []byte("secret"), 0o600)
	_ = os.Symlink(outside, filepath.Join(work, "looks-fine.txt"))
	client := NewClient(f.socket, f.token(t, "w1:p1", ""))
	for _, path := range []string{outside, filepath.Join(work, "looks-fine.txt"), work + "/../" + filepath.Base(filepath.Dir(outside)) + "/id_ed25519"} {
		_, err := client.Ask(ctx, questions.AskInput{Title: "Look", Kind: questions.KindAck, Attachments: []string{path}})
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != 400 || !strings.Contains(apiErr.Message, "could not be attached") {
			t.Fatalf("%s: %v", path, err)
		}
	}
	if open, _ := f.service.List(ctx, "open"); len(open) != 0 {
		t.Fatalf("a refused ask was recorded: %+v", open)
	}
	// No token: crowsnest reads no file for an unidentified process.
	anonymous := NewClient(f.socket, "")
	_ = os.WriteFile(filepath.Join(work, "ok.txt"), []byte("fine"), 0o600)
	_, err := anonymous.Ask(ctx, questions.AskInput{Title: "Look", Kind: questions.KindAck, Attachments: []string{filepath.Join(work, "ok.txt")}})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 403 {
		t.Fatalf("unidentified: %v", err)
	}
	// Another agent's token: its roots are not w1:p1's.
	other := NewClient(f.socket, f.token(t, "w2:p1", ""))
	if _, err := other.Ask(ctx, questions.AskInput{Title: "Look", Kind: questions.KindAck, Attachments: []string{filepath.Join(work, "ok.txt")}}); err == nil {
		t.Fatal("another agent attached a file from w1:p1's directory")
	}
}

func TestDoneWithAttachmentsGoesOnTheResult(t *testing.T) {
	f, service, work := attachFixture(t)
	ctx := context.Background()
	job, err := f.jobs.Create(ctx, jobs.CreateInput{Brief: "Write a report"}, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(work, "report.md"), []byte("# Report\n"), 0o600)
	client := NewClient(f.socket, f.token(t, "w1:p1", job.ID))
	done, err := client.DoneJob(ctx, job.ID, jobs.DoneInput{ResultMD: "Wrote it", Verified: []string{}, NotVerified: []string{}, Attachments: []string{filepath.Join(work, "report.md")}})
	if err != nil {
		t.Fatal(err)
	}
	results, _ := service.List(ctx, attach.OwnerJob, done.ID, attach.RoleResult)
	if len(results) != 1 || results[0].Type != "text/markdown" {
		t.Fatalf("result attachments %+v", results)
	}
	// A done whose file is refused does not finish the job.
	second, _ := f.jobs.Create(ctx, jobs.CreateInput{Brief: "Another"}, "")
	client = NewClient(f.socket, f.token(t, "w1:p1", second.ID))
	if _, err := client.DoneJob(ctx, second.ID, jobs.DoneInput{ResultMD: "x", Verified: []string{}, NotVerified: []string{}, Attachments: []string{"/etc/hosts"}}); err == nil {
		t.Fatal("done with a file outside was accepted")
	}
	if detail, _ := f.jobs.Get(ctx, second.ID); detail.State.Terminal() {
		t.Fatalf("the job finished anyway: %s", detail.State)
	}
}
