package orch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/transcript"
)

// template is a Render whose text the test changes, as an upgrade changes CROWSNEST.md.
type tmplText struct {
	mu   sync.Mutex
	text string
}

func (t *tmplText) set(text string) { t.mu.Lock(); t.text = text; t.mu.Unlock() }
func (t *tmplText) render() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.text, nil
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// freshRig is an orchestrator on the fake herdr whose instructions and clock the test controls.
func freshRig(t *testing.T, f *fake.Fake, adjust ...func(*Options)) (*testRig, *tmplText, *clock) {
	t.Helper()
	tmpl := &tmplText{text: "Every reply starts with a SAY line, but only when the header says talk=on."}
	now := &clock{now: time.Unix(1_800_000_000, 0)}
	rig := newRig(t, f, append([]func(*Options){func(o *Options) { o.Render, o.Now = tmpl.render, now.Now }}, adjust...)...)
	return rig, tmpl, now
}

func startsWithResume(f *fake.Fake) int {
	count := 0
	for _, start := range f.Starts() {
		if argValue(start.Args, "--resume") != "" {
			count++
		}
	}
	return count
}

// TestNewInstructionsRestartAnIdleAgent: the tmplText changes while the orchestrator runs; at the
// next watch with the agent idle and its box empty it is restarted on the same conversation, and
// the new text is what it now has. Nothing changes while the text is the same.
func TestNewInstructionsRestartAnIdleAgent(t *testing.T) {
	f := fake.New()
	rig, tmpl, now := freshRig(t, f)
	ctx := context.Background()
	first, err := rig.orch.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := transcript.Path(rig.claude, rig.opts.Dir, first.SessionID)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte("{}\n"), 0o600)
	_ = f.SetStatus(first.PaneID, herdr.StatusIdle)
	_ = f.SetScreen(first.PaneID, fleet.StripANSI(emptyInput), emptyInput) // idle: Claude's empty input box

	rig.orch.Watch(ctx)
	if len(f.Starts()) != 1 || rig.orch.Status().InstructionsStale {
		t.Fatalf("unchanged instructions restarted it (%d starts) or marked it", len(f.Starts()))
	}

	tmpl.set("Every reply starts with a SAY line. Codeword: PELICAN.")
	now.advance(time.Minute)
	rig.orch.Watch(ctx)
	if len(f.Starts()) != 2 || startsWithResume(f) != 1 || argValue(f.Starts()[1].Args, "--resume") != first.SessionID {
		t.Fatalf("not restarted on the same conversation: %+v", f.Starts())
	}
	body, _ := os.ReadFile(argValue(f.Starts()[1].Args, "--append-system-prompt-file"))
	if !strings.Contains(string(body), "PELICAN") {
		t.Fatalf("the restart did not get the new text: %s", body)
	}
	if rig.orch.Status().InstructionsStale {
		t.Fatal("still marked after the restart")
	}
	// The next watches leave it alone.
	_ = f.SetStatus(rig.orch.Status().PaneID, herdr.StatusIdle)
	now.advance(time.Minute)
	rig.orch.Watch(ctx)
	if len(f.Starts()) != 2 {
		t.Fatal("restarted again with nothing changed")
	}
}

// TestAdoptedAgentFromBeforeIsStale is Jeff's case: an orchestrator started before crowsnest
// recorded what it wrote is adopted and marked at once, waits while it works, and is restarted
// once idle.
func TestAdoptedAgentFromBeforeIsStale(t *testing.T) {
	f := fake.New()
	_, _, pane := f.AddWorkspace("orchestrator", "/somewhere")
	_ = f.AddAgent(pane, Name, "claude", herdr.StatusWorking, "yesterday")
	rig, _, now := freshRig(t, f)
	ctx := context.Background()
	if status, _ := rig.orch.Ensure(ctx); !status.InstructionsStale || len(f.Starts()) != 0 {
		t.Fatalf("adoption: stale %v, starts %d", status.InstructionsStale, len(f.Starts()))
	}
	now.advance(time.Minute)
	rig.orch.Watch(ctx)
	if len(f.Starts()) != 0 {
		t.Fatal("restarted mid-turn")
	}
	_ = f.SetStatus(pane, herdr.StatusIdle)
	// R7-S13: a screen whose input box crowsnest does not recognise is not "no draft".
	_ = f.SetScreen(pane, "something crowsnest does not recognise", "something crowsnest does not recognise")
	now.advance(time.Minute)
	rig.orch.Watch(ctx)
	if len(f.Starts()) != 0 {
		t.Fatal("restarted with an unrecognised screen: /exit could land in a draft")
	}
	_ = f.SetScreen(pane, fleet.StripANSI(emptyInput), emptyInput)
	now.advance(time.Minute)
	rig.orch.Watch(ctx)
	if len(f.Starts()) != 1 {
		t.Fatalf("an idle stale agent was not restarted: %d starts", len(f.Starts()))
	}
}

// TestNoRestartWhenOffPausedOrBusy: the Setting off, the pause, and a step on a held job each
// keep the agent running (marked); none of them is a restart.
func TestNoRestartWhenOffPausedOrBusy(t *testing.T) {
	cases := map[string]func(*Options){
		"setting off": func(o *Options) { o.AutoInstructionRestart = func() bool { return false } },
		"paused":      func(o *Options) { o.Paused = func() bool { return true } },
		"held job":    func(o *Options) { o.HeldJobBusy = func(context.Context) bool { return true } },
	}
	for name, adjust := range cases {
		t.Run(name, func(t *testing.T) {
			f := fake.New()
			_, _, pane := f.AddWorkspace("orchestrator", "/somewhere")
			_ = f.AddAgent(pane, Name, "claude", herdr.StatusIdle, "yesterday")
			rig, _, now := freshRig(t, f, adjust)
			ctx := context.Background()
			_, _ = rig.orch.Ensure(ctx)
			now.advance(time.Minute)
			rig.orch.Watch(ctx)
			if len(f.Starts()) != 0 {
				t.Fatal("restarted")
			}
			if !rig.orch.Status().InstructionsStale {
				t.Fatal("not marked")
			}
		})
	}
}

// TestVersionAloneIsNotNewInstructions: a rebuild changes the version the file names, not its
// instructions.
func TestVersionAloneIsNotNewInstructions(t *testing.T) {
	f := fake.New()
	rig := newRig(t, f)
	rig.orch.opts.Version = "v1.2.3-4-gabc"
	before := rig.orch.fingerprint("crowsnest v1.2.3-4-gabc wrote this")
	rig.orch.opts.Version = "v1.2.3-5-gdef"
	after := rig.orch.fingerprint("crowsnest v1.2.3-5-gdef wrote this")
	if after != before || rig.orch.fingerprint("crowsnest v1.2.3-5-gdef now says something else") == before {
		t.Fatal("the version changed the fingerprint")
	}
}

// TestResumedConversationGetsItsNewInstructions: Claude Code keeps a resumed conversation's
// original system prompt, so a resume after the instructions changed leaves a notice (once) with
// the current text for the SessionStart context; a resume with nothing changed leaves none.
func TestResumedConversationGetsItsNewInstructions(t *testing.T) {
	f := fake.New()
	rig, tmpl, _ := freshRig(t, f)
	ctx := context.Background()
	first, err := rig.orch.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := transcript.Path(rig.claude, rig.opts.Dir, first.SessionID)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte("{}\n"), 0o600)
	if _, err := rig.orch.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	if notice, _ := rig.orch.InstructionsNotice(ctx); notice != "" {
		t.Fatalf("a resume with the same instructions left a notice: %q", notice)
	}
	tmpl.set("Codeword: PELICAN.")
	if _, err := rig.orch.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	notice, version := rig.orch.InstructionsNotice(ctx)
	if !strings.Contains(notice, "PELICAN") || !strings.Contains(notice, "replaces") || version == "" {
		t.Fatalf("notice %q (version %q)", notice, version)
	}
	// R7-C11: a reply that never reached the agent (no ack) is delivered again at the next start.
	if again, _ := rig.orch.InstructionsNotice(ctx); !strings.Contains(again, "PELICAN") {
		t.Fatal("an unacknowledged notice was dropped")
	}
	if rig.orch.AckInstructionsNotice(ctx, "an-older-version") {
		t.Fatal("an ack of another version was taken")
	}
	if !rig.orch.AckInstructionsNotice(ctx, version) {
		t.Fatal("the ack was refused")
	}
	if again, _ := rig.orch.InstructionsNotice(ctx); again != "" {
		t.Fatal("the notice was given after it was acknowledged")
	}
}
