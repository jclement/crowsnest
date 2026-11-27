// Tests for the orchestrator's lifecycle against the fake herdr: adopt, create, resume, the
// not-ready start, the watch restarting an exited agent within its budget, Restart and New
// conversation, and the status events.

package orch

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

type testRig struct {
	fake   *fake.Fake
	orch   *Orchestrator
	db     *store.Store
	events *hub.Subscription
	opts   Options
	claude string // Claude config dir
}

func newRig(t *testing.T, f *fake.Fake, adjust ...func(*Options)) *testRig {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	events := hub.New()
	sub := events.Subscribe(256)
	t.Cleanup(sub.Close)
	root := t.TempDir()
	opts := Options{
		Client: f, KV: db, Hub: events, Session: "crowsnest-test", Dir: filepath.Join(root, "orchestrator"),
		StateDir: filepath.Join(root, "state"), PermissionMode: "auto", Model: "haiku", Version: "test",
		BinDir: "/opt/crowsnest/bin", BasePath: "/usr/bin", ClaudeConfigDir: filepath.Join(root, "claude"),
		ShellWait: 200 * time.Millisecond, Logger: slog.New(slog.DiscardHandler),
	}
	for _, change := range adjust {
		change(&opts)
	}
	return &testRig{fake: f, orch: New(opts), db: db, events: sub, opts: opts, claude: opts.ClaudeConfigDir}
}

func argValue(args []string, flag string) string {
	if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

// lastState drains published orchestrator events and returns the states in order.
func (r *testRig) states() []State {
	var states []State
	for {
		select {
		case event := <-r.events.Events():
			if event.Type == "orchestrator" {
				states = append(states, event.Data.(Status).State)
			}
		default:
			return states
		}
	}
}

func TestEnsureAdoptsALiveOrchestrator(t *testing.T) {
	f := fake.New()
	_, _, pane := f.AddWorkspace("orchestrator", "/somewhere")
	_ = f.AddAgent(pane, Name, "claude", herdr.StatusWorking, "existing-session")
	rig := newRig(t, f)

	status, err := rig.orch.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateWorking || status.PaneID != pane || status.SessionID != "existing-session" {
		t.Fatalf("status = %+v", status)
	}
	if len(f.Starts()) != 0 {
		t.Fatal("adopting started a new agent")
	}
	if stored, _ := rig.db.GetKV(context.Background(), kvSessionID); stored != "existing-session" {
		t.Fatalf("stored session = %q", stored)
	}
	// Idempotent: a second Ensure changes nothing and publishes nothing new.
	rig.states()
	if _, err := rig.orch.Ensure(context.Background()); err != nil || len(rig.states()) != 0 || len(f.Starts()) != 0 {
		t.Fatal("second Ensure was not a no-op")
	}
}

func TestEnsureCreatesWorkspaceAndStarts(t *testing.T) {
	f := fake.New()
	rig := newRig(t, f)
	status, err := rig.orch.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateIdle || status.PaneID == "" || status.SessionID == "" {
		t.Fatalf("status = %+v", status)
	}
	starts := f.Starts()
	if len(starts) != 1 {
		t.Fatalf("starts = %+v", starts)
	}
	args := starts[0].Args
	if starts[0].Name != Name || starts[0].Kind != "claude" || argValue(args, "--session-id") != status.SessionID ||
		argValue(args, "--model") != "haiku" || argValue(args, "--permission-mode") != "auto" {
		t.Fatalf("start = %+v", starts[0])
	}
	if slices.Contains(args, "--resume") {
		t.Fatal("a first start resumed")
	}
	instructions := argValue(args, "--append-system-prompt-file")
	body, err := os.ReadFile(instructions)
	if err != nil || !strings.Contains(string(body), "crowsnest-test") || !strings.Contains(string(body), "crowsnest:job-protocol") {
		t.Fatalf("instructions file %s: %v\n%s", instructions, err, body)
	}
	if _, err := os.Stat(argValue(args, "--settings")); err != nil {
		t.Fatalf("settings file: %v", err)
	}
	env := f.PaneEnv(status.PaneID)
	if env["CROWSNEST"] != "1" || env["CROWSNEST_SESSION"] != "crowsnest-test" || env["PATH"] != "/opt/crowsnest/bin:/usr/bin" {
		t.Fatalf("pane env = %v", env)
	}
	if states := rig.states(); len(states) == 0 || states[len(states)-1] != StateIdle {
		t.Fatalf("published states = %v", states)
	}
}

func TestEnsureReusesTheOrchestratorWorkspace(t *testing.T) {
	f := fake.New()
	_, _, shell := f.AddWorkspace(WorkspaceLabel, "/x")
	rig := newRig(t, f)
	status, err := rig.orch.Ensure(context.Background())
	if err != nil || status.PaneID != shell {
		t.Fatalf("status = %+v, %v (want pane %s)", status, err, shell)
	}
	workspaces, _ := f.WorkspaceList(context.Background())
	if len(workspaces) != 1 {
		t.Fatalf("workspaces = %d, want the existing one reused", len(workspaces))
	}
}

func TestEnsureResumesAStoredConversation(t *testing.T) {
	f := fake.New()
	rig := newRig(t, f)
	ctx := context.Background()
	_ = rig.db.SetKV(ctx, kvSessionID, "stored-session")

	// No transcript for it: a fresh session.
	first, _ := rig.orch.Ensure(ctx)
	if first.SessionID == "stored-session" || slices.Contains(f.Starts()[0].Args, "--resume") {
		t.Fatalf("resumed a conversation with no transcript: %+v", f.Starts()[0].Args)
	}

	// With a transcript: --resume.
	_ = f.ExitAgent(first.PaneID)
	_ = rig.db.SetKV(ctx, kvSessionID, "stored-session")
	path := transcript.Path(rig.claude, rig.opts.Dir, "stored-session")
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte("{}\n"), 0o600)
	second, err := rig.orch.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	args := f.Starts()[1].Args
	if argValue(args, "--resume") != "stored-session" || second.SessionID != "stored-session" || second.PaneID != first.PaneID {
		t.Fatalf("second start = %v, status %+v", args, second)
	}
}

func TestEnsureNotReadyBecomesNeedsTerminal(t *testing.T) {
	f := fake.New()
	f.StartNotReadyNext()
	rig := newRig(t, f)
	status, err := rig.orch.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateNeedsTerminal || status.Detail == "" || status.PaneID == "" {
		t.Fatalf("status = %+v", status)
	}
	// Still at the dialog: the watch keeps needs_terminal and does not start another.
	rig.orch.Watch(context.Background())
	if got := rig.orch.Status(); got.State != StateNeedsTerminal || len(f.Starts()) != 1 {
		t.Fatalf("after watch: %+v, starts %d", got, len(f.Starts()))
	}
	// Someone answers the trust dialog in the terminal.
	_ = f.ClearDialog(Name)
	rig.orch.Watch(context.Background())
	if got := rig.orch.Status(); got.State != StateIdle || got.Detail != "" {
		t.Fatalf("after the dialog was answered: %+v", got)
	}
}

func TestWatchRestartsAnExitedOrchestratorWithinBudget(t *testing.T) {
	f := fake.New()
	now := time.Unix(1_700_000_000, 0)
	var clock sync.Mutex
	rig := newRig(t, f, func(o *Options) {
		o.Now = func() time.Time { clock.Lock(); defer clock.Unlock(); return now }
	})
	ctx := context.Background()
	first, _ := rig.orch.Ensure(ctx)
	for i := 1; i <= maxRestartsPerHour; i++ {
		_ = f.ExitAgent(first.PaneID)
		rig.orch.Watch(ctx)
		status := rig.orch.Status()
		if status.State != StateIdle || status.Restarts != i || status.PaneID != first.PaneID {
			t.Fatalf("restart %d: %+v", i, status)
		}
	}
	_ = f.ExitAgent(first.PaneID)
	rig.orch.Watch(ctx)
	if status := rig.orch.Status(); status.State != StateFailed || !strings.Contains(status.Detail, "Restart") {
		t.Fatalf("over budget: %+v", status)
	}
	if len(f.Starts()) != 1+maxRestartsPerHour {
		t.Fatalf("starts = %d", len(f.Starts()))
	}
	// A manual Restart brings it back and clears the budget.
	status, err := rig.orch.Restart(ctx)
	if err != nil || status.State != StateIdle || status.Restarts != 0 {
		t.Fatalf("manual restart: %+v, %v", status, err)
	}
	// An hour later the budget has refilled by itself too.
	clock.Lock()
	now = now.Add(2 * time.Hour)
	clock.Unlock()
	if rig.orch.Status().Restarts != 0 {
		t.Fatal("budget did not refill")
	}
}

func TestRestartResumesAndNewConversationStartsFresh(t *testing.T) {
	f := fake.New()
	rig := newRig(t, f)
	ctx := context.Background()
	first, _ := rig.orch.Ensure(ctx)
	path := transcript.Path(rig.claude, rig.opts.Dir, first.SessionID)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte("{}\n"), 0o600)

	restarted, err := rig.orch.Restart(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.SessionID != first.SessionID || argValue(f.Starts()[1].Args, "--resume") != first.SessionID {
		t.Fatalf("restart did not resume: %+v %v", restarted, f.Starts()[1].Args)
	}
	if prompts := f.Prompts(); len(prompts) == 0 || prompts[len(prompts)-1].Text != "/exit" {
		t.Fatalf("prompts = %+v, want /exit", prompts)
	}

	fresh, err := rig.orch.NewConversation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.SessionID == first.SessionID || argValue(f.Starts()[2].Args, "--session-id") != fresh.SessionID {
		t.Fatalf("new conversation reused the session: %+v %v", fresh, f.Starts()[2].Args)
	}
	if fresh.PaneID != first.PaneID {
		t.Fatalf("new conversation moved panes: %s → %s", first.PaneID, fresh.PaneID)
	}
}

func TestDisabled(t *testing.T) {
	f := fake.New()
	rig := newRig(t, f, func(o *Options) { o.Disabled = true })
	status, err := rig.orch.Ensure(context.Background())
	if err != nil || status.State != StateDisabled || len(f.Starts()) != 0 {
		t.Fatalf("disabled ensure: %+v, %v", status, err)
	}
	if _, err := rig.orch.Restart(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled restart: %v", err)
	}
}

func TestStartFailureIsReported(t *testing.T) {
	f := fake.New()
	f.FailNext("agent.start", &herdr.Error{Code: "pane_busy", Message: "pane is busy"})
	rig := newRig(t, f)
	status, err := rig.orch.Ensure(context.Background())
	// The status names herdr's code and what to do, but not herdr's own message (R1 I2).
	if err == nil || status.State != StateFailed || !strings.Contains(status.Detail, "pane_busy") || strings.Contains(status.Detail, "pane is busy") {
		t.Fatalf("status = %+v, err %v", status, err)
	}
}

// TestLingeringAgentRecordCountsAsGone pins a real-herdr behaviour found in M4: right after /exit,
// agent.get can still answer for the name while the pane is back at its shell.
func TestLingeringAgentRecordCountsAsGone(t *testing.T) {
	f := fake.New()
	rig := newRig(t, f)
	ctx := context.Background()
	first, _ := rig.orch.Ensure(ctx)

	_ = f.LingerAgent(first.PaneID)
	rig.orch.Watch(ctx)
	if status := rig.orch.Status(); status.State != StateIdle || len(f.Starts()) != 2 || status.Restarts != 1 {
		t.Fatalf("watch did not restart a lingering record: %+v, starts %d", status, len(f.Starts()))
	}

	_ = f.LingerAgent(first.PaneID)
	fresh, err := rig.orch.NewConversation(ctx)
	if err != nil || fresh.SessionID == "" || fresh.SessionID == first.SessionID || len(f.Starts()) != 3 {
		t.Fatalf("new conversation over a lingering record: %+v, %v, starts %d", fresh, err, len(f.Starts()))
	}
}

func TestRunFollowsFleetEvents(t *testing.T) {
	f := fake.New()
	events := hub.New()
	rig := newRig(t, f, func(o *Options) { o.Hub, o.WatchInterval = events, time.Hour })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go rig.orch.Run(ctx)
	deadline := time.Now().Add(15 * time.Second)
	for rig.orch.Status().State != StateIdle {
		if time.Now().After(deadline) {
			t.Fatalf("never started: %+v", rig.orch.Status())
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = f.SetStatus(Name, herdr.StatusWorking)
	events.Publish(hub.Event{Type: "fleet", Data: "changed"})
	for rig.orch.Status().State != StateWorking {
		if time.Now().After(deadline) {
			t.Fatalf("a fleet event did not refresh the status (the watch tick is an hour): %+v", rig.orch.Status())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStartRetriesAPaneThatIsNotReadyYet(t *testing.T) {
	f := fake.New()
	f.FailNext("agent.start", &herdr.Error{Code: "agent_pane_busy", Message: "agent target pane w1:p1 is not an available shell"})
	rig := newRig(t, f)
	status, err := rig.orch.Ensure(context.Background())
	if err != nil || status.State != StateIdle || len(f.Starts()) != 1 {
		t.Fatalf("status = %+v, %v, starts %d", status, err, len(f.Starts()))
	}
}

// TestNewConversationIgnoresAStaleSessionFromHerdr is R1-C02: right after a launch herdr can
// still report the previous conversation's session id. The id Claude was started with must be
// the one published, stored and used for the transcript, until herdr reports something new.
func TestNewConversationIgnoresAStaleSessionFromHerdr(t *testing.T) {
	f := fake.New()
	rig := newRig(t, f)
	ctx := context.Background()
	first, _ := rig.orch.Ensure(ctx)

	f.StartReportsSessionNext(first.SessionID) // herdr still sees the old conversation
	fresh, err := rig.orch.NewConversation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	launched := argValue(f.Starts()[1].Args, "--session-id")
	if launched == first.SessionID || fresh.SessionID != launched {
		t.Fatalf("status session = %q, launched %q, old %q", fresh.SessionID, launched, first.SessionID)
	}
	assertStoredSession(t, rig, launched)
	origin, err := TranscriptLookup(f, rig.orch)(ctx, Name)
	if err != nil || origin.SessionID != launched {
		t.Fatalf("transcript lookup = %+v, %v; want session %q", origin, err, launched)
	}

	rig.orch.Watch(ctx) // herdr still stale: nothing moves
	if got := rig.orch.Status().SessionID; got != launched {
		t.Fatalf("watch adopted the stale session: %q", got)
	}
	assertStoredSession(t, rig, launched)

	_ = f.SetAgentSession(Name, "after-clear") // a /clear in the terminal really moves it
	rig.orch.Watch(ctx)
	if got := rig.orch.Status().SessionID; got != "after-clear" {
		t.Fatalf("watch missed a genuine session change: %q", got)
	}
	assertStoredSession(t, rig, "after-clear")
}

// TestStartPublishesTheSessionBeforeHerdrDetectsIt is R1-C02's other half: herdr may not report
// any session yet when the agent is ready.
func TestStartPublishesTheSessionBeforeHerdrDetectsIt(t *testing.T) {
	f := fake.New()
	f.StartReportsSessionNext("")
	rig := newRig(t, f)
	ctx := context.Background()
	status, err := rig.orch.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	launched := argValue(f.Starts()[0].Args, "--session-id")
	if launched == "" || status.SessionID != launched {
		t.Fatalf("status session = %q, launched %q", status.SessionID, launched)
	}
	assertStoredSession(t, rig, launched)
	rig.orch.Watch(ctx)
	_ = f.SetAgentSession(Name, launched) // detection catches up
	rig.orch.Watch(ctx)
	if got := rig.orch.Status().SessionID; got != launched {
		t.Fatalf("after detection: %q", got)
	}
}

func assertStoredSession(t *testing.T, rig *testRig, want string) {
	t.Helper()
	if stored, _ := rig.db.GetKV(context.Background(), kvSessionID); stored != want {
		t.Fatalf("stored session = %q, want %q", stored, want)
	}
}

// Remote Control (DESIGN.md 7.10): the name is one argv entry after --remote-control, kept on a
// resume, absent when off; the status names it.
func TestStartArgsRemoteControl(t *testing.T) {
	f := fake.New()
	rig := newRig(t, f, func(o *Options) { o.RemoteControlName = "quark crowsnest" })
	status, err := rig.orch.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := argValue(f.Starts()[0].Args, "--remote-control"); got != "quark crowsnest" || status.RemoteControl != "quark crowsnest" {
		t.Fatalf("args %v status %+v", f.Starts()[0].Args, status)
	}
	if _, err := rig.orch.Restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := argValue(f.Starts()[1].Args, "--remote-control"); got != "quark crowsnest" {
		t.Fatalf("restart args %v", f.Starts()[1].Args)
	}
	off := newRig(t, fake.New())
	if status, err := off.orch.Ensure(context.Background()); err != nil || status.RemoteControl != "" {
		t.Fatalf("off: %+v %v", status, err)
	}
	if slices.Contains(off.fake.Starts()[0].Args, "--remote-control") {
		t.Fatalf("off args %v", off.fake.Starts()[0].Args)
	}
}

// The watch reports whether Remote Control came up: Claude's banner, or nothing within the
// window (Claude starts without it when the login cannot have it; nothing is restarted).
func TestRemoteControlNote(t *testing.T) {
	for _, tc := range []struct {
		screen string
		wait   time.Duration
		want   string
	}{
		{"  /remote-control is active · Continue here, on your phone", 0, RemoteControlActive},
		{"Welcome", time.Second, ""},
		{"Welcome", time.Minute, RemoteControlUnconfirmed},
	} {
		f := fake.New()
		clock := &pokeClock{now: pokeEpoch}
		rig := newRig(t, f, func(o *Options) { o.RemoteControlName = "quark crowsnest"; o.Now = clock.Now })
		status, err := rig.orch.Ensure(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := f.SetScreen(status.PaneID, tc.screen, tc.screen); err != nil {
			t.Fatal(err)
		}
		clock.Advance(tc.wait)
		rig.orch.Watch(context.Background())
		if got := rig.orch.Status().RemoteControlNote; got != tc.want {
			t.Errorf("screen %q after %s: note %q, want %q", tc.screen, tc.wait, got, tc.want)
		}
	}
}
