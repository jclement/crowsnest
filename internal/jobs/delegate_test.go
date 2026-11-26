// Tests for R2-H1: what an agent may create. A child inside its parent's directory with the
// parent's engine starts at once and inherits engine, model and isolation; anything wider waits
// on a blocking question and starts only on "allow"; depth, fan-out and the live total are
// capped; the orchestrator may use the human's remembered directories; model names are strict.

package jobs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
)

// delegateRig is a rig whose AskHuman is a real questions service on the same database.
type delegateRig struct {
	*rig
	questions *questions.Service
}

func newDelegateRig(t *testing.T, limits DelegationLimits) *delegateRig {
	t.Helper()
	d := &delegateRig{}
	d.rig = newRig(t, func(o *Options) {
		o.Delegation = limits
		o.AskHuman = func(ctx context.Context, input questions.AskInput, caller questions.Caller) (questions.Question, error) {
			return d.questions.Ask(ctx, input, caller)
		}
	})
	d.questions = questions.New(questions.Options{Store: d.db})
	return d
}

// parent starts a human's job in a fresh directory (its agent runs in a fake pane there) and
// returns it with the caller its agent's token stands for.
func (d *delegateRig) parent(input CreateInput) (Detail, AgentCaller) {
	d.t.Helper()
	if input.Brief == "" {
		input.Brief = "Parent work"
	}
	if input.Dir == "" {
		input.Dir = d.t.TempDir()
	}
	job := d.create(input)
	if job.State != StateRunning || job.WorkDir == "" {
		d.t.Fatalf("parent did not start: %+v", job.Job)
	}
	return job, AgentCaller{PaneID: job.PaneID, JobID: job.ID}
}

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(parts...)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestChildInsideItsParentStartsAndInherits(t *testing.T) {
	d := newDelegateRig(t, DelegationLimits{})
	parent, caller := d.parent(CreateInput{Model: "haiku", Role: "lead"})
	child, err := d.svc.CreateForAgent(context.Background(), CreateInput{Brief: "Test it", Dir: mkdir(t, parent.WorkDir, "pkg"), Role: "tester"}, caller)
	if err != nil {
		t.Fatal(err)
	}
	got := d.get(child.ID)
	if got.State != StateRunning || got.Engine != EngineClaude || got.Model != "haiku" || got.ParentID != parent.ID || got.Origin != OriginAgent {
		t.Fatalf("child %+v", got.Job)
	}
}

func TestWiderChildrenWaitForTheHuman(t *testing.T) {
	ctx := context.Background()
	outside := t.TempDir()
	isolate := false
	for name, setup := range map[string]func(parent Detail) CreateInput{
		"another directory": func(Detail) CreateInput { return CreateInput{Brief: "x", Dir: outside} },
		"no directory":      func(Detail) CreateInput { return CreateInput{Brief: "x"} },
		"home":              func(Detail) CreateInput { return CreateInput{Brief: "x", Dir: "~", Role: "tester"} },
		"another engine":    func(p Detail) CreateInput { return CreateInput{Brief: "x", Dir: p.WorkDir, Engine: EngineCodex} },
		"a symlink out": func(p Detail) CreateInput {
			link := filepath.Join(p.WorkDir, "escape")
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			return CreateInput{Brief: "x", Dir: link}
		},
		"not isolated": func(p Detail) CreateInput {
			// The fake herdr records the worktree without making it on disk.
			return CreateInput{Brief: "x", Dir: mkdir(t, p.WorkDir), Isolate: &isolate}
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDelegateRig(t, DelegationLimits{})
			input := CreateInput{Role: "lead"}
			if name == "not isolated" {
				input = CreateInput{Role: "builder", Dir: gitRepo(t)}
			}
			parent, caller := d.parent(input)
			starts := len(d.fake.Starts())
			job, err := d.svc.CreateForAgent(ctx, setup(parent), caller)
			if err != nil {
				t.Fatal(err)
			}
			if job.State != StateQueued || job.Cause != CauseApproval || !strings.HasPrefix(job.CauseDetail, "q_") {
				t.Fatalf("held job %+v", job)
			}
			if len(d.fake.Starts()) != starts {
				t.Fatalf("a held job was launched: %+v", d.fake.Starts())
			}
			question, err := d.questions.Get(ctx, job.CauseDetail)
			if err != nil {
				t.Fatal(err)
			}
			if question.Urgency != questions.UrgencyBlocking || question.SourcePane != caller.PaneID || question.JobID != job.ID ||
				!strings.Contains(question.Title, parent.ID+" wants to start a job") {
				t.Fatalf("question %+v", question)
			}
			// The tick and the orchestrator dispatch leave it alone.
			d.clock.Advance(10 * relaunchAfter)
			d.svc.Tick(ctx)
			if got := d.get(job.ID); got.State != StateQueued || len(d.fake.Starts()) != starts {
				t.Fatalf("the tick moved a held job: %+v", got.Job)
			}
		})
	}
}

func TestApprovalAllowLaunchesAndDenyOrExpiryCancels(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		settle func(d *delegateRig, questionID string)
		want   State
	}{
		{"allow", func(d *delegateRig, id string) {
			if _, err := d.questions.Answer(ctx, id, questions.AnswerInput{Value: json.RawMessage(`"allow"`)}); err != nil {
				t.Fatal(err)
			}
		}, StateRunning},
		{"deny", func(d *delegateRig, id string) {
			if _, err := d.questions.Answer(ctx, id, questions.AnswerInput{Value: json.RawMessage(`"deny"`)}); err != nil {
				t.Fatal(err)
			}
		}, StateCancelled},
		{"cancelled", func(d *delegateRig, id string) {
			if _, err := d.questions.Cancel(ctx, id, false); err != nil {
				t.Fatal(err)
			}
		}, StateCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newDelegateRig(t, DelegationLimits{})
			_, caller := d.parent(CreateInput{Role: "lead"})
			other := t.TempDir()
			job, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "Elsewhere", Dir: other, Role: "tester"}, caller)
			if err != nil {
				t.Fatal(err)
			}
			tc.settle(d, job.CauseDetail)
			// The settle callback and the start-up reconcile both end here, reading the question
			// from the store.
			d.svc.ReconcileApprovals(ctx)
			got := d.get(job.ID)
			if got.State != tc.want || got.Cause == CauseApproval {
				t.Fatalf("after %s: %+v", tc.name, got.Job)
			}
			remembered := strings.Contains(strings.Join(d.svc.RememberedDirs(ctx), "\n"), other)
			if remembered != (tc.want == StateRunning) {
				t.Fatalf("remembered %v after %s", remembered, tc.name)
			}
		})
	}
}

func TestDelegationLimits(t *testing.T) {
	ctx := context.Background()
	t.Run("fan-out", func(t *testing.T) {
		d := newDelegateRig(t, DelegationLimits{MaxChildren: 2})
		parent, caller := d.parent(CreateInput{Role: "lead"})
		for i := range 3 {
			_, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "child", Dir: parent.WorkDir, Role: "tester"}, caller)
			if (i < 2) != (err == nil) {
				t.Fatalf("child %d: %v", i, err)
			}
			if i == 2 && (!IsValidation(err) || !strings.Contains(err.Error(), "already created 2 jobs")) {
				t.Fatalf("third child: %v", err)
			}
		}
	})
	t.Run("live total", func(t *testing.T) {
		d := newDelegateRig(t, DelegationLimits{MaxLiveAgentJobs: 1})
		parent, caller := d.parent(CreateInput{Role: "lead"})
		if _, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "one", Dir: parent.WorkDir, Role: "tester"}, caller); err != nil {
			t.Fatal(err)
		}
		if _, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "two", Dir: parent.WorkDir, Role: "tester"}, caller); !IsValidation(err) || !strings.Contains(err.Error(), "agents already have 1 jobs open") {
			t.Fatalf("second live agent job: %v", err)
		}
	})
	t.Run("depth", func(t *testing.T) {
		d := newDelegateRig(t, DelegationLimits{MaxDepth: 2})
		_, caller := d.parent(CreateInput{Role: "lead"})
		var last Job
		for level := 1; level <= 3; level++ {
			job, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "deeper", Dir: d.get(firstNonEmpty(last.ID, caller.JobID)).WorkDir, Role: "lead"}, caller)
			if level <= 2 && err != nil {
				t.Fatalf("level %d: %v", level, err)
			}
			if level == 3 {
				if !IsValidation(err) || !strings.Contains(err.Error(), "levels of delegation") {
					t.Fatalf("level 3: %v", err)
				}
				return
			}
			last = d.get(job.ID).Job
			caller = AgentCaller{PaneID: last.PaneID, JobID: last.ID}
		}
	})
	t.Run("finished parent", func(t *testing.T) {
		d := newDelegateRig(t, DelegationLimits{})
		parent, caller := d.parent(CreateInput{Role: "lead"})
		if _, err := d.svc.Fail(ctx, parent.ID, "gave up"); err != nil {
			t.Fatal(err)
		}
		if _, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "x", Dir: parent.WorkDir}, caller); !IsValidation(err) {
			t.Fatalf("a finished job delegated: %v", err)
		}
	})
	t.Run("a pane that is not the orchestrator", func(t *testing.T) {
		d := newDelegateRig(t, DelegationLimits{})
		if _, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "x", Dir: t.TempDir()}, AgentCaller{PaneID: "w9:p9"}); !IsValidation(err) {
			t.Fatalf("a stray pane created a job: %v", err)
		}
	})
}

func TestOrchestratorUsesRememberedDirsAndAsksForOthers(t *testing.T) {
	ctx := context.Background()
	d := newDelegateRig(t, DelegationLimits{})
	_, _, pane := d.fake.AddWorkspace("orchestrator", "/tmp/orch")
	if err := d.fake.AddAgent(pane, "orchestrator", "claude", herdr.StatusWorking, "s-orch"); err != nil {
		t.Fatal(err)
	}
	d.orch = OrchestratorInfo{PaneID: pane, Ready: true}
	known := t.TempDir()
	d.create(CreateInput{Brief: "The human's job", Dir: known}) // remembers known
	caller := AgentCaller{PaneID: pane}
	inside, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "In a known repo", Dir: mkdir(t, known, "sub"), Role: "tester"}, caller)
	if err != nil || inside.Cause == CauseApproval || inside.Origin != OriginOrchestrator {
		t.Fatalf("known dir: %+v %v", inside, err)
	}
	elsewhere, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "Somewhere new", Dir: t.TempDir(), Engine: EngineCodex}, caller)
	if err != nil || elsewhere.Cause != CauseApproval {
		t.Fatalf("unknown dir: %+v %v", elsewhere, err)
	}
	question, _ := d.questions.Get(ctx, elsewhere.CauseDetail)
	if !strings.HasPrefix(question.Title, "The orchestrator wants to start a job in ") || !strings.HasSuffix(question.Title, "as codex: allow?") {
		t.Fatalf("title %q", question.Title)
	}
	if mine, err := d.svc.CreateForAgent(ctx, CreateInput{Brief: "For me"}, caller); err != nil || mine.Cause == CauseApproval {
		t.Fatalf("no dir (itself): %+v %v", mine, err)
	}
}

func TestModelNamesAreStrict(t *testing.T) {
	r := newRig(t, nil)
	for _, model := range []string{"haiku; rm -rf ~", "$(id)", "a b", strings.Repeat("m", 65), "--dangerously-skip-permissions\nx"} {
		if _, err := r.svc.Create(context.Background(), CreateInput{Brief: "x", Dir: t.TempDir(), Model: model}, ""); !IsValidation(err) {
			t.Errorf("model %q accepted: %v", model, err)
		}
	}
	for _, model := range []string{"haiku", "claude-opus-4-5[1m]", "gpt-5.1-codex", "us.anthropic.claude:1"} {
		if _, err := r.svc.Create(context.Background(), CreateInput{Brief: "x", Dir: t.TempDir(), Model: model}, ""); err != nil {
			t.Errorf("model %q refused: %v", model, err)
		}
	}
}

// The store keeps the origin (migration 0006).
func TestOriginIsStored(t *testing.T) {
	d := newDelegateRig(t, DelegationLimits{})
	parent, caller := d.parent(CreateInput{Role: "lead"})
	child, err := d.svc.CreateForAgent(context.Background(), CreateInput{Brief: "x", Dir: parent.WorkDir, Role: "tester"}, caller)
	if err != nil {
		t.Fatal(err)
	}
	numeric, _ := ParseID(child.ID)
	record, err := d.db.GetJob(context.Background(), numeric)
	if err != nil || record.Origin != OriginAgent {
		t.Fatalf("record %+v %v", record, err)
	}
	var _ store.JobRecord = record
}
