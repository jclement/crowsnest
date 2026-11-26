package jobs

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// fakeAttacher records what the jobs service asked of attachments (DESIGN.md 8.6).
type fakeAttacher struct {
	mu       sync.Mutex
	attached map[string][]string
	refuse   error
}

func (f *fakeAttacher) CheckBrief(_ context.Context, ids []string) error {
	if f.refuse != nil {
		return f.refuse
	}
	return nil
}

func (f *fakeAttacher) AttachBrief(_ context.Context, job Job, ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attached[job.ID] = append(f.attached[job.ID], ids...)
	return nil
}

func (f *fakeAttacher) BriefLines(_ context.Context, jobID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var lines []string
	for _, id := range f.attached[jobID] {
		lines = append(lines, "Attached: /state/jobs/"+jobID+"/attachments/"+id+"-shot.png (image/png, 2 KB)")
	}
	return lines
}

func TestBriefListsItsAttachmentsBeforeLaunch(t *testing.T) {
	files := &fakeAttacher{attached: map[string][]string{}}
	r := newRig(t, func(o *Options) { o.Attachments = files })
	job := r.create(CreateInput{Brief: "Fix the layout in this screenshot", Dir: t.TempDir(), Attachments: []string{"a_1"}})
	prompts := r.fake.Prompts()
	if len(prompts) != 1 {
		t.Fatalf("prompts %+v", prompts)
	}
	brief := briefFileOf(t, prompts[0].Text)
	if !strings.Contains(brief, "Fix the layout") || !strings.Contains(brief, "Attached: /state/jobs/"+job.ID+"/attachments/a_1-shot.png (image/png, 2 KB)") {
		t.Fatalf("brief file:\n%s", brief)
	}
	if strings.Contains(prompts[0].Text, "\n") {
		t.Fatal("the typed line grew a line break")
	}
}

func TestAttachmentsAreRefusedWhenTheyCannotBeGiven(t *testing.T) {
	r := newRig(t, nil) // no attacher wired
	if _, err := r.svc.Create(context.Background(), CreateInput{Brief: "x", Attachments: []string{"a_1"}}, ""); err == nil || !IsValidation(err) {
		t.Fatalf("no attacher: %v", err)
	}
	files := &fakeAttacher{attached: map[string][]string{}, refuse: errors.New("attachment a_1: that attachment was already sent")}
	r = newRig(t, func(o *Options) { o.Attachments = files })
	if _, err := r.svc.Create(context.Background(), CreateInput{Brief: "x", Attachments: []string{"a_1"}}, ""); err == nil || !strings.Contains(err.Error(), "already sent") {
		t.Fatalf("refused upload: %v", err)
	}
	if list, _ := r.svc.List(context.Background(), nil); len(list) != 0 {
		t.Fatalf("a refused job was recorded: %+v", list)
	}
	// An agent cannot hand its child the human's uploads.
	if _, err := r.svc.CreateForAgent(context.Background(), CreateInput{Brief: "x", Attachments: []string{"a_1"}}, AgentCaller{PaneID: "w1:p1"}); err == nil || !strings.Contains(err.Error(), "agent's job cannot carry them") {
		t.Fatalf("agent job with attachments: %v", err)
	}
}
