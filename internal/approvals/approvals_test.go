package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/risk"
	"github.com/jclement/crowsnest/internal/store"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type harness struct {
	service *Service
	clock   *clock
	db      *store.Store
	mu      sync.Mutex
	changes []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	h := &harness{clock: &clock{now: time.Unix(1_800_000_000, 0)}, db: db}
	h.service = New(Options{Store: db, Now: h.clock.Now, RiskContext: func(context.Context, string) risk.Context {
		return risk.Context{ProtectedHosts: []string{"nyc1"}}
	}, OnChange: func(_ context.Context, a Approval, change string) {
		h.mu.Lock()
		h.changes = append(h.changes, a.ID+":"+change)
		h.mu.Unlock()
	}})
	return h
}

func (h *harness) request(t *testing.T, tool, command string) Approval {
	t.Helper()
	input, _ := json.Marshal(map[string]string{"command": command})
	approval, err := h.service.Request(context.Background(), RequestInput{JobID: "j_1", PaneID: "p1", ToolName: tool, ToolInput: input, Hooked: true})
	if err != nil {
		t.Fatal(err)
	}
	return approval
}

func TestAllowWakesTheWaiter(t *testing.T) {
	h := newHarness(t)
	approval := h.request(t, "Bash", "go test ./...")
	if approval.Status != StatusPending || approval.Risk != risk.Low || approval.Summary != "Bash: go test ./..." {
		t.Fatalf("requested: %+v", approval)
	}
	done := make(chan Approval)
	go func() {
		settled, _ := h.service.Wait(context.Background(), approval.ID, time.Minute)
		done <- settled
	}()
	time.Sleep(20 * time.Millisecond)
	if _, err := h.service.Decide(context.Background(), approval.ID, Decision{Allow: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case settled := <-done:
		if settled.Status != StatusAllowed || settled.DecidedVia != ViaBrowser {
			t.Fatalf("waiter saw %+v", settled)
		}
		var out struct {
			HookSpecificOutput struct {
				Decision map[string]any `json:"decision"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(HookOutput(settled)), &out); err != nil {
			t.Fatal(err)
		}
		decision := out.HookSpecificOutput.Decision
		if decision["behavior"] != "allow" || decision["updatedPermissions"] != nil {
			t.Fatalf("hook output %v: allow, and never a permission update", decision)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the waiter was not woken")
	}
}

func TestDenyCarriesTheNote(t *testing.T) {
	h := newHarness(t)
	approval := h.request(t, "Bash", "touch x")
	settled, err := h.service.Decide(context.Background(), approval.ID, Decision{Allow: false, Note: "use printf instead"})
	if err != nil || settled.Status != StatusDenied {
		t.Fatalf("deny: %+v %v", settled, err)
	}
	if out := HookOutput(settled); !strings.Contains(out, `"behavior":"deny"`) || !strings.Contains(out, "Their note: use printf instead") {
		t.Fatalf("hook output %s", out)
	}
	if _, err := h.service.Decide(context.Background(), approval.ID, Decision{Allow: true}); !errors.Is(err, ErrNotPending) {
		t.Fatalf("a second decision must be refused, got %v", err)
	}
}

func TestDangerousNeedsConfirmation(t *testing.T) {
	h := newHarness(t)
	approval := h.request(t, "Bash", "rm -rf build")
	if approval.Risk != risk.Dangerous {
		t.Fatalf("risk %s", approval.Risk)
	}
	if _, err := h.service.Decide(context.Background(), approval.ID, Decision{Allow: true}); !errors.Is(err, ErrConfirm) {
		t.Fatalf("an unconfirmed dangerous allow must be refused, got %v", err)
	}
	if _, err := h.service.Decide(context.Background(), approval.ID, Decision{Allow: true, Confirmed: true, Via: ViaNotification}); !errors.Is(err, ErrVia) {
		t.Fatalf("a dangerous approval can never be answered from a notification, got %v", err)
	}
	if settled, err := h.service.Decide(context.Background(), approval.ID, Decision{Allow: true, Confirmed: true}); err != nil || settled.Status != StatusAllowed {
		t.Fatalf("a confirmed allow: %+v %v", settled, err)
	}
}

func TestNotificationOnlyForLow(t *testing.T) {
	h := newHarness(t)
	elevated := h.request(t, "Bash", "git push origin feature/x")
	if elevated.Risk != risk.Elevated {
		t.Fatalf("risk %s", elevated.Risk)
	}
	if _, err := h.service.Decide(context.Background(), elevated.ID, Decision{Allow: false, Via: ViaNotification}); !errors.Is(err, ErrVia) {
		t.Fatalf("elevated from a notification (even a deny) must be refused, got %v", err)
	}
	low := h.request(t, "Bash", "ls")
	if settled, err := h.service.Decide(context.Background(), low.ID, Decision{Allow: true, Via: ViaNotification}); err != nil || settled.DecidedVia != ViaNotification {
		t.Fatalf("low from a notification: %+v %v", settled, err)
	}
}

func TestExpiry(t *testing.T) {
	h := newHarness(t)
	approval := h.request(t, "Bash", "ls")
	h.clock.add(DefaultTTL)
	h.service.Sweep(context.Background())
	got, _ := h.service.Get(context.Background(), approval.ID)
	if got.Status != StatusExpired || HookOutput(got) != "" {
		t.Fatalf("after its TTL: %+v (and the hook prints nothing)", got)
	}
}

func TestHookThatStopsPollingIsSuperseded(t *testing.T) {
	h := newHarness(t)
	approval := h.request(t, "Bash", "ls")
	h.clock.add(DefaultPollGrace / 2)
	h.service.Sweep(context.Background())
	if got, _ := h.service.Get(context.Background(), approval.ID); got.Status != StatusPending {
		t.Fatalf("still within grace: %s", got.Status)
	}
	h.clock.add(DefaultPollGrace)
	h.service.Sweep(context.Background())
	if got, _ := h.service.Get(context.Background(), approval.ID); got.Status != StatusSuperseded {
		t.Fatalf("no poll for longer than the grace: %s", got.Status)
	}
}

func TestActiveWaiterIsNotSuperseded(t *testing.T) {
	h := newHarness(t)
	approval := h.request(t, "Bash", "ls")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, _ = h.service.Wait(ctx, approval.ID, time.Hour) }()
	time.Sleep(20 * time.Millisecond)
	h.clock.add(2 * DefaultPollGrace)
	h.service.Sweep(context.Background())
	if got, _ := h.service.Get(context.Background(), approval.ID); got.Status != StatusPending {
		t.Fatalf("a hook on the long-poll is alive: %s", got.Status)
	}
}

func TestDemoApprovalsAreNotSupersededForNotPolling(t *testing.T) {
	h := newHarness(t)
	approval, err := h.service.Request(context.Background(), RequestInput{PaneID: "p1", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"ls"}`)})
	if err != nil {
		t.Fatal(err)
	}
	h.clock.add(time.Hour)
	h.service.Sweep(context.Background())
	h.service.RecoverAtStart(context.Background())
	if got, _ := h.service.Get(context.Background(), approval.ID); got.Status != StatusPending {
		t.Fatalf("an unhooked (demo) approval waits for a human: %s", got.Status)
	}
}

func TestSupersedePaneAndRan(t *testing.T) {
	h := newHarness(t)
	first := h.request(t, "Bash", "ls")
	second := h.request(t, "Bash", "make")
	if n := h.service.SupersedeRan(context.Background(), "p1", "Bash", json.RawMessage(`{"command":"make","description":"build"}`)); n != 1 {
		t.Fatalf("SupersedeRan superseded %d, want 1", n)
	}
	if got, _ := h.service.Get(context.Background(), second.ID); got.Status != StatusSuperseded {
		t.Fatalf("the tool that ran: %s", got.Status)
	}
	if got, _ := h.service.Get(context.Background(), first.ID); got.Status != StatusPending {
		t.Fatalf("a different call stays: %s", got.Status)
	}
	if n := h.service.SupersedePane(context.Background(), "other", "x"); n != 0 {
		t.Fatalf("another pane superseded %d", n)
	}
	if n := h.service.SupersedePane(context.Background(), "p1", "Turn ended"); n != 1 {
		t.Fatalf("SupersedePane superseded %d", n)
	}
	got, _ := h.service.Get(context.Background(), first.ID)
	if got.Status != StatusSuperseded || got.StatusReason != "Turn ended" || HookOutput(got) != "" {
		t.Fatalf("after the turn ended: %+v", got)
	}
}

func TestReconcileScreens(t *testing.T) {
	h := newHarness(t)
	approval := h.request(t, "Bash", "ls")
	noDialog := func(context.Context, string) Screen { return Screen{Known: true} }
	unknown := func(context.Context, string) Screen { return Screen{} }
	h.service.ReconcileScreens(context.Background(), noDialog)
	if got, _ := h.service.Get(context.Background(), approval.ID); got.Status != StatusPending {
		t.Fatal("too young: the dialog may not be drawn yet")
	}
	h.clock.add(screenGrace)
	h.service.ReconcileScreens(context.Background(), noDialog)
	h.service.ReconcileScreens(context.Background(), unknown)
	if got, _ := h.service.Get(context.Background(), approval.ID); got.Status != StatusPending {
		t.Fatal("one miss, then an unknown look: still pending")
	}
	h.service.ReconcileScreens(context.Background(), func(context.Context, string) Screen { return Screen{Known: true, Dialog: true} })
	h.service.ReconcileScreens(context.Background(), noDialog)
	if got, _ := h.service.Get(context.Background(), approval.ID); got.Status != StatusPending {
		t.Fatal("a dialog seen resets the misses")
	}
	h.service.ReconcileScreens(context.Background(), noDialog)
	if got, _ := h.service.Get(context.Background(), approval.ID); got.Status != StatusSuperseded {
		t.Fatalf("two misses in a row: %s", got.Status)
	}
}

// A restart keeps hooked approvals pending for the restart grace: one whose hook polls again stays
// pending and a later decision reaches the new waiter; one nobody polls is superseded after it.
func TestRestartSurvival(t *testing.T) {
	h := newHarness(t)
	polled := h.request(t, "Bash", "ls")
	silent := h.request(t, "Bash", "make")
	h.clock.add(time.Hour) // long past the poll grace of the old process
	fresh := New(Options{Store: h.db, Now: h.clock.Now})
	fresh.RecoverAtStart(context.Background())
	fresh.Sweep(context.Background())
	for _, id := range []string{polled.ID, silent.ID} {
		if got, _ := fresh.Get(context.Background(), id); got.Status != StatusPending {
			t.Fatalf("%s right after the restart: %s, want pending", id, got.Status)
		}
	}
	done := make(chan Approval, 1)
	go func() {
		settled, _ := fresh.Wait(context.Background(), polled.ID, time.Minute)
		done <- settled
	}()
	time.Sleep(20 * time.Millisecond)
	h.clock.add(DefaultRestartGrace + time.Second)
	fresh.Sweep(context.Background())
	if got, _ := fresh.Get(context.Background(), silent.ID); got.Status != StatusSuperseded || !strings.Contains(got.StatusReason, "restarted") {
		t.Fatalf("not polled within the grace: %+v", got)
	}
	if got, _ := fresh.Get(context.Background(), polled.ID); got.Status != StatusPending {
		t.Fatalf("polled within the grace: %s, want pending", got.Status)
	}
	if _, err := fresh.Decide(context.Background(), polled.ID, Decision{Allow: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case settled := <-done:
		if settled.Status != StatusAllowed {
			t.Fatalf("the waiter after the restart saw %s", settled.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a decision after the restart did not reach the hook's poll")
	}
}

func TestChangesAreAnnounced(t *testing.T) {
	h := newHarness(t)
	approval := h.request(t, "Bash", "ls")
	_, _ = h.service.Decide(context.Background(), approval.ID, Decision{Allow: true})
	h.mu.Lock()
	defer h.mu.Unlock()
	if strings.Join(h.changes, ",") != approval.ID+":requested,"+approval.ID+":allowed" {
		t.Fatalf("changes %v", h.changes)
	}
}

func TestPendingForJob(t *testing.T) {
	h := newHarness(t)
	approval := h.request(t, "Bash", "ls")
	if !h.service.PendingForJob(context.Background(), "j_1") || h.service.PendingForJob(context.Background(), "j_2") {
		t.Fatal("PendingForJob")
	}
	_, _ = h.service.Decide(context.Background(), approval.ID, Decision{})
	if h.service.PendingForJob(context.Background(), "j_1") {
		t.Fatal("settled")
	}
}

func TestValidation(t *testing.T) {
	h := newHarness(t)
	if _, err := h.service.Request(context.Background(), RequestInput{}); err == nil {
		t.Fatal("a tool name is required")
	}
	approval := h.request(t, "Bash", "ls")
	if _, err := h.service.Decide(context.Background(), approval.ID, Decision{Note: strings.Repeat("x", maxNote+1)}); err == nil {
		t.Fatal("an over-long note must be refused")
	}
	if _, err := h.service.Get(context.Background(), "q_1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("a malformed id is not found")
	}
}

// TestSummaryShowsWhatIsCut is R5-S7: the summary drops bidi and other format characters, and a
// long call says how much is not shown rather than ending in a bare ellipsis.
func TestSummaryShowsWhatIsCut(t *testing.T) {
	if got := Summary("Bash", json.RawMessage(`{"command":"ls ‮-la"}`)); got != "Bash: ls -la" {
		t.Fatalf("bidi control kept: %q", got)
	}
	long := strings.Repeat("a", 200)
	got := Summary("Bash", json.RawMessage(`{"command":"`+long+`"}`))
	if !strings.HasSuffix(got, "(41 more characters)") {
		t.Fatalf("long summary %q", got)
	}
}
