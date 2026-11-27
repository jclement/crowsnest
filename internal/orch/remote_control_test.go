// The Remote Control link through a standing agent's lifecycle (DESIGN.md 7.10), for the
// orchestrator and a lead alike: captured from the banner after a start (wrapped at 44 columns
// too), cleared by Restart and New conversation and captured again, kept in kv so a crowsnest
// restart that adopts the running agent still has it, dropped when the conversation changes,
// and found in the transcript by a later look when the pane no longer shows it.

package orch

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/transcript"
)

func remoteRig(t *testing.T, kind standingKind, f *fake.Fake, clock *pokeClock) *testRig {
	t.Helper()
	return newRig(t, f, kind.adjust, func(o *Options) {
		if o.RemoteControlName == "" {
			o.RemoteControlName = "quark crowsnest"
		}
		o.Now = clock.Now
	})
}

func showScreen(t *testing.T, f *fake.Fake, pane, file string) {
	t.Helper()
	text := readTestdata(t, file)
	if err := f.SetScreen(pane, text, text); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteControlURLLifecycle(t *testing.T) {
	ctx := context.Background()
	for _, kind := range standingKinds() {
		t.Run(kind.name, func(t *testing.T) {
			f := fake.New()
			clock := &pokeClock{now: pokeEpoch}
			rig := remoteRig(t, kind, f, clock)
			status, err := rig.orch.Ensure(ctx)
			if err != nil || status.RemoteControl == "" || status.RemoteControlURL != "" {
				t.Fatalf("started %+v %v", status, err)
			}
			showScreen(t, f, status.PaneID, "banner-44cols.txt")
			rig.orch.Watch(ctx)
			if got := rig.orch.Status(); got.RemoteControlURL != firstURL || got.RemoteControlNote != RemoteControlActive {
				t.Fatalf("after the banner: %+v", got)
			}

			// Restart (same conversation): cleared at the start, captured again.
			showScreen(t, f, status.PaneID, "no-banner.txt")
			if status, err = rig.orch.Restart(ctx); err != nil || status.RemoteControlURL != "" || status.RemoteControlNote != "" {
				t.Fatalf("restart %+v %v", status, err)
			}
			showScreen(t, f, status.PaneID, "banner-80cols-resumed.txt")
			rig.orch.Watch(ctx)
			if got := rig.orch.Status().RemoteControlURL; got != firstURL {
				t.Fatalf("after restart: %q", got)
			}

			// New conversation: a new session URL; the old banner above the new header is not taken.
			if status, err = rig.orch.NewConversation(ctx); err != nil || status.RemoteControlURL != "" {
				t.Fatalf("new conversation %+v %v", status, err)
			}
			showScreen(t, f, status.PaneID, "banner-80cols-resumed.txt")
			screen := readTestdata(t, "banner-80cols-resumed.txt") + readTestdata(t, "no-banner.txt")
			_ = f.SetScreen(status.PaneID, screen, screen)
			rig.orch.Watch(ctx)
			if got := rig.orch.Status().RemoteControlURL; got != "" {
				t.Fatalf("took the previous conversation's link: %q", got)
			}
			showScreen(t, f, status.PaneID, "two-banners.txt")
			rig.orch.Watch(ctx)
			if got := rig.orch.Status().RemoteControlURL; got != secondURL {
				t.Fatalf("after new conversation: %q", got)
			}
			if stored, _ := rig.db.GetKV(ctx, kind.kvPrefix+".remote_control"); !strings.Contains(stored, secondURL) || !strings.Contains(stored, rig.orch.Status().SessionID) {
				t.Fatalf("kv %q", stored)
			}
		})
	}
}

func TestRemoteControlURLSurvivesAdoption(t *testing.T) {
	ctx := context.Background()
	for _, kind := range standingKinds() {
		t.Run(kind.name, func(t *testing.T) {
			f := fake.New()
			clock := &pokeClock{now: pokeEpoch}
			rig := remoteRig(t, kind, f, clock)
			status, _ := rig.orch.Ensure(ctx)
			showScreen(t, f, status.PaneID, "banner-80cols-new.txt")
			rig.orch.Watch(ctx)

			// crowsnest restarts; the agent is still running, its banner long scrolled away.
			showScreen(t, f, status.PaneID, "no-banner.txt")
			again := New(rig.opts)
			adopted, err := again.Ensure(ctx)
			if err != nil || len(f.Starts()) != 1 {
				t.Fatalf("adopt %+v %v (starts %d)", adopted, err, len(f.Starts()))
			}
			if adopted.RemoteControlURL != secondURL || adopted.RemoteControlNote != RemoteControlActive || adopted.RemoteControl == "" {
				t.Fatalf("adopted without its link: %+v", adopted)
			}

			// The conversation changes under it (a /clear): the link is dropped and looked for again.
			if err := f.SetAgentSession(adopted.PaneID, "cleared-session"); err != nil {
				t.Fatal(err)
			}
			again.Watch(ctx)
			if got := again.Status(); got.SessionID != "cleared-session" || got.RemoteControlURL != "" {
				t.Fatalf("after the session changed: %+v", got)
			}
			// A third crowsnest adopting now must not bring the old link back.
			third, _ := New(rig.opts).Ensure(ctx)
			if third.RemoteControlURL != "" {
				t.Fatalf("stale link restored: %+v", third)
			}
		})
	}
}

// The demo's made-up link is shown after a start and kept, and Claude is never started with
// --remote-control for it.
func TestDemoRemoteControl(t *testing.T) {
	ctx := context.Background()
	demo := &DemoRemoteControl{Name: "demo crowsnest", URL: "https://claude.ai/code/session_demoorchestrator0000000000"}
	rig := newRig(t, fake.New(), func(o *Options) { o.DemoRemoteControl = demo })
	status, err := rig.orch.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.RemoteControl != demo.Name || status.RemoteControlURL != demo.URL || status.RemoteControlNote != RemoteControlActive {
		t.Fatalf("demo status %+v", status)
	}
	if slices.Contains(rig.fake.Starts()[0].Args, "--remote-control") {
		t.Fatalf("demo started Remote Control: %v", rig.fake.Starts()[0].Args)
	}
	if !ValidRemoteControlURL(demo.URL) {
		t.Fatal("the demo link must have the real shape")
	}
	rig.orch.Watch(ctx)
	if got := rig.orch.Status().RemoteControlURL; got != demo.URL {
		t.Fatalf("after a watch: %q", got)
	}
}

// After the first window the watch looks again now and then; the transcript's bridge_status
// gives the link even though the pane no longer shows it. With Remote Control off nothing is
// looked for at all.
func TestRemoteControlURLFromTranscriptLater(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	clock := &pokeClock{now: pokeEpoch}
	rig := remoteRig(t, standingKinds()[0], f, clock)
	status, _ := rig.orch.Ensure(ctx)
	showScreen(t, f, status.PaneID, "no-banner.txt")
	clock.Advance(time.Minute)
	rig.orch.Watch(ctx)
	if got := rig.orch.Status(); got.RemoteControlNote != RemoteControlUnconfirmed || got.RemoteControlURL != "" {
		t.Fatalf("after the window: %+v", got)
	}
	path := transcript.Path(rig.claude, rig.opts.Dir, status.SessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"system","subtype":"bridge_status","url":"` + firstURL + `"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	clock.Advance(30 * time.Second) // not yet time for the next look
	rig.orch.Watch(ctx)
	if got := rig.orch.Status().RemoteControlURL; got != "" {
		t.Fatalf("looked too early: %q", got)
	}
	clock.Advance(remoteControlRelookEvery)
	rig.orch.Watch(ctx)
	if got := rig.orch.Status(); got.RemoteControlURL != firstURL || got.RemoteControlNote != RemoteControlActive {
		t.Fatalf("from the transcript: %+v", got)
	}

	off := newRig(t, fake.New(), func(o *Options) { o.Now = clock.Now })
	offStatus, _ := off.orch.Ensure(ctx)
	showScreen(t, off.fake, offStatus.PaneID, "banner-80cols-new.txt")
	off.orch.Watch(ctx)
	if got := off.orch.Status(); got.RemoteControlURL != "" || got.RemoteControlNote != "" {
		t.Fatalf("off, yet looked: %+v", got)
	}
}
