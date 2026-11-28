// Tests for comment sends as R9 left them (R9-C3, C5): a send to a job that may not continue
// keeps its notes waiting, not sent, until the job's batch is typed; a repeated request with the
// same send id answers the first one's result instead of sending twice.

package httpd

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jclement/crowsnest/internal/attach"
	"github.com/jclement/crowsnest/internal/changes"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/store"
)

// switchPause is a pause switch a test flips.
type switchPause struct{ on atomic.Bool }

func (p *switchPause) Paused() bool                         { return p.on.Load() }
func (p *switchPause) Held(context.Context, string, string) {}
func (p *switchPause) set(on bool)                          { p.on.Store(on) }

// pausedCommentHarness is commentHarness with a pause switch, and the jobs service's feedback
// settled into the changes service as serve wires it.
func pausedCommentHarness(t *testing.T) (*harness, *fake.Fake, *http.Cookie, *switchPause, *jobs.Service) {
	t.Helper()
	dir := changesRepo(t)
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := fake.New()
	pause := &switchPause{}
	jobService := jobs.New(jobs.Options{Store: db, Herdr: f, Synchronous: true, Pause: pause,
		Launch: jobs.LaunchOptions{StateDir: t.TempDir(), Executable: "/bin/crowsnest", AgentSocket: "/tmp/test.sock"}})
	off := false
	if _, err := jobService.Create(context.Background(), jobs.CreateInput{Brief: "Add A", Dir: dir, Isolate: &off}, ""); err != nil {
		t.Fatal(err)
	}
	attachments, err := attach.New(attach.Options{Store: db, Dir: filepath.Join(t.TempDir(), "attachments")})
	if err != nil {
		t.Fatal(err)
	}
	service := changes.New(changes.Options{Store: db, Resolver: oneJob{root: dir}})
	jobService.SetFeedbackSettled(func(ctx context.Context, ref string, outcome jobs.FeedbackOutcome, _ string) {
		settle := changes.SendDelivered
		if outcome != jobs.FeedbackDelivered {
			settle = changes.SendReturned
		}
		_ = service.Settle(ctx, ref, settle)
	})
	h := newHarness(t, harnessOptions{changes: service, jobs: jobService, herdr: f, attachments: attachments, attachLayout: attach.Layout{StateDir: t.TempDir()}})
	return h, f, h.login(), pause, jobService
}

func addNote(t *testing.T, h *harness, cookie *http.Cookie, text string) {
	t.Helper()
	hash := fileHash(t, h, cookie)
	body := `{"path":"a.go","scope":"committed","side":"new","line_start":3,"line_end":3,"body":"` + text + `","hash":"` + hash + `"}`
	if response := h.do(withCookie(request(http.MethodPost, "/api/v1/changes/jobs/j_1/comments", body), cookie)); response.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", response.Code, response.Body)
	}
}

func TestAWaitingSendIsNotMarkedSentUntilTyped(t *testing.T) {
	h, f, cookie, pause, jobService := pausedCommentHarness(t)
	addNote(t, h, cookie, "rename A to Add")
	pause.set(true)
	response := h.do(withCookie(request(http.MethodPost, "/api/v1/changes/jobs/j_1/comments/send", `{"send_id":"browser-send-0001"}`), cookie))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"how":"waiting"`) {
		t.Fatalf("send = %d %s", response.Code, response.Body)
	}
	if list := commentList(t, h, cookie); list.Waiting != 1 || list.Sent != 0 || list.Drafts != 0 {
		t.Fatalf("while the job is paused the note is not sent: %+v", list)
	}
	// The browser retries (a lost answer): the first result, no second batch.
	again := h.do(withCookie(request(http.MethodPost, "/api/v1/changes/jobs/j_1/comments/send", `{"send_id":"browser-send-0001"}`), cookie))
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), `"how":"waiting"`) {
		t.Fatalf("retried send = %d %s", again.Code, again.Body)
	}
	if detail, _ := jobService.Get(context.Background(), "j_1"); len(detail.Feedback) != 1 {
		t.Fatalf("the retry queued another batch: %+v", detail.Feedback)
	}
	// A second, different send finds nothing due: the note is in the waiting send.
	if response := h.do(withCookie(request(http.MethodPost, "/api/v1/changes/jobs/j_1/comments/send", `{}`), cookie)); response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "already in a send") {
		t.Fatalf("a second send = %d %s", response.Code, response.Body)
	}
	pause.set(false)
	jobService.Tick(context.Background())
	typed := 0
	for _, prompt := range f.Prompts() {
		if strings.Contains(prompt.Text, "Comment: rename A to Add") {
			typed++
		}
	}
	if typed != 1 {
		t.Fatalf("typed %d times after the resume, want 1", typed)
	}
	if list := commentList(t, h, cookie); list.Sent != 1 || list.Waiting != 0 {
		t.Fatalf("after the batch was typed: %+v", list)
	}
}
