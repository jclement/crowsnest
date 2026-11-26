// Tests for the instruction layers at a job start (DESIGN.md 21.3): with Options.Instructions
// wired, the prompt file carries the composed layers and then the protocol, the start is
// recorded, a Codex brief gets the same text unrecorded, and a failure fails the start.

package jobs

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
)

// fakeLayers records what the launcher asked of the instruction layers.
type fakeLayers struct {
	mu    sync.Mutex
	calls []string // "<job> record=<bool>"
	fail  bool
}

func (f *fakeLayers) JobLayers(_ context.Context, job Job, record bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return "", errors.New("the database is locked")
	}
	if record {
		f.calls = append(f.calls, job.ID+" record=true")
	} else {
		f.calls = append(f.calls, job.ID+" record=false")
	}
	return "## Instructions from the human (global)\n\nNever push.\n\n# Role: " + job.Role + "\n\nLayered role text.", nil
}

func TestJobPromptCarriesTheInstructionLayers(t *testing.T) {
	cases := []struct {
		name     string
		engine   string
		wantCall []string
	}{
		{"claude: the prompt file, recorded", "", []string{"j_1 record=true"}},
		// A Codex agent has no prompt file: its brief starts with the same text, not recorded twice.
		{"codex: the brief, recorded once", "codex", []string{"j_1 record=true", "j_1 record=false"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			layers := &fakeLayers{}
			r := newRig(t, func(o *Options) { o.Instructions = layers })
			r.create(CreateInput{Brief: "Add a widget", Dir: t.TempDir(), Engine: tc.engine})
			var text string
			if tc.engine == "codex" {
				text = briefFileOf(t, r.fake.Prompts()[0].Text)
			} else {
				body, err := os.ReadFile(argValue(r.fake.Starts()[0].Args, "--append-system-prompt-file"))
				if err != nil {
					t.Fatal(err)
				}
				text = string(body)
			}
			global, protocol := strings.Index(text, "Never push."), strings.Index(text, "The job protocol")
			if global < 0 || protocol < global || !strings.Contains(text, "Layered role text.") {
				t.Fatalf("the layers, then the protocol:\n%.600s", text)
			}
			if strings.Join(layers.calls, ",") != strings.Join(tc.wantCall, ",") {
				t.Fatalf("calls %v, want %v", layers.calls, tc.wantCall)
			}
		})
	}
}

func TestJobStartFailsWhenTheLayersCannotBeRead(t *testing.T) {
	layers := &fakeLayers{fail: true}
	r := newRig(t, func(o *Options) { o.Instructions = layers })
	job := r.create(CreateInput{Brief: "Add a widget", Dir: t.TempDir()})
	if len(r.fake.Starts()) != 0 || job.State == StateRunning {
		t.Fatalf("no agent may start without the human's standing instructions: %+v, starts %d", job.Job, len(r.fake.Starts()))
	}
}
