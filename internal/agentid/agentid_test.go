package agentid

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

func newRegistry(t *testing.T) (*Registry, *store.Store) {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, nil), db
}

func TestMintAndResolve(t *testing.T) {
	registry, db := newRegistry(t)
	ctx := context.Background()
	token, err := registry.Mint(ctx, Binding{PaneID: "w1:p1", JobID: "j_3", Kind: KindStart})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "cnt_") || len(token) < 40 {
		t.Fatalf("token %q", token)
	}
	binding, ok, err := registry.Resolve(ctx, token)
	if err != nil || !ok || binding.PaneID != "w1:p1" || binding.JobID != "j_3" || binding.Kind != KindStart {
		t.Fatalf("resolve: %+v %v %v", binding, ok, err)
	}
	stored, _ := db.ListKV(ctx, kvPrefix)
	for key, value := range stored {
		if strings.Contains(key, token) || strings.Contains(value, token) {
			t.Fatal("the token itself is stored; only its hash may be")
		}
	}
	for _, bad := range []string{"", "w1:p1", "orchestrator", "cnt_unknown", strings.Repeat("cnt_x", 100)} {
		if _, ok, _ := registry.Resolve(ctx, bad); ok {
			t.Fatalf("%q resolved", bad)
		}
	}
}

func TestBindRevokesStaleTokens(t *testing.T) {
	registry, _ := newRegistry(t)
	ctx := context.Background()
	oldPane, _ := registry.Mint(ctx, Binding{PaneID: "w1:p1", Kind: KindPane})
	oldMCP, _ := registry.Mint(ctx, Binding{PaneID: "w1:p1", Kind: KindStart})
	other, _ := registry.Mint(ctx, Binding{PaneID: "w2:p1", Kind: KindPane})

	// The agent restarts in the same pane: a new MCP token replaces the old one only.
	newMCP, _ := registry.Mint(ctx, Binding{PaneID: "w1:p1", Kind: KindStart})
	if _, ok, _ := registry.Resolve(ctx, oldMCP); ok {
		t.Fatal("the previous start's MCP token still resolves")
	}
	if _, ok, _ := registry.Resolve(ctx, oldPane); !ok {
		t.Fatal("restarting the agent revoked the pane's own token")
	}
	// herdr reuses the pane id for a new pane: everything bound to the old pane goes.
	token, _ := NewToken()
	if err := registry.Bind(ctx, token, Binding{PaneID: "w1:p1", Kind: KindPane}); err != nil {
		t.Fatal(err)
	}
	for _, stale := range []string{oldPane, newMCP} {
		if _, ok, _ := registry.Resolve(ctx, stale); ok {
			t.Fatal("a token of the pane's previous life still resolves")
		}
	}
	if _, ok, _ := registry.Resolve(ctx, other); !ok {
		t.Fatal("another pane's token was revoked")
	}
}

// slowKV widens the window between listing and storing, where unserialised binds interleave.
type slowKV struct{ *store.Store }

func (s slowKV) ListKV(ctx context.Context, prefix string) (map[string]string, error) {
	all, err := s.Store.ListKV(ctx, prefix)
	time.Sleep(5 * time.Millisecond)
	return all, err
}

// R2-C15: concurrent starts in one pane leave exactly one start token valid.
func TestConcurrentBindsLeaveOneStartToken(t *testing.T) {
	_, db := newRegistry(t)
	registry := New(slowKV{db}, nil)
	ctx := context.Background()
	tokens := make([]string, 12)
	var wg sync.WaitGroup
	for i := range tokens {
		wg.Go(func() {
			token, err := registry.Mint(ctx, Binding{PaneID: "w1:p1", JobID: "j_1", Kind: KindStart})
			if err != nil {
				t.Error(err)
			}
			tokens[i] = token
		})
	}
	wg.Wait()
	valid := 0
	for _, token := range tokens {
		if _, ok, _ := registry.Resolve(ctx, token); ok {
			valid++
		}
	}
	if valid != 1 {
		t.Fatalf("%d start tokens of one pane are valid, want 1", valid)
	}
}

// R2-C15: a start for the job that used to hold a pane is refused once the pane is another job's.
func TestStaleStartIsRefused(t *testing.T) {
	registry, _ := newRegistry(t)
	ctx := context.Background()
	if _, err := registry.Mint(ctx, Binding{PaneID: "w1:p1", JobID: "j_2", Kind: KindPane}); err != nil {
		t.Fatal(err)
	}
	if token, err := registry.Mint(ctx, Binding{PaneID: "w1:p1", JobID: "j_1", Kind: KindStart}); !errors.Is(err, ErrStaleStart) {
		t.Fatalf("stale start: %q %v", token, err)
	}
	if _, err := registry.Mint(ctx, Binding{PaneID: "w1:p1", JobID: "j_2", Kind: KindStart}); err != nil {
		t.Fatalf("the pane's own job: %v", err)
	}
}

// R2-L1: a finished job's tokens and a closed pane's tokens stop resolving.
func TestTokensEndWithTheJobAndThePane(t *testing.T) {
	registry, _ := newRegistry(t)
	ctx := context.Background()
	jobPane, _ := registry.Mint(ctx, Binding{PaneID: "w1:p1", JobID: "j_1", Kind: KindPane})
	jobStart, _ := registry.Mint(ctx, Binding{PaneID: "w1:p1", JobID: "j_1", Kind: KindStart})
	orchestrator, _ := registry.Mint(ctx, Binding{PaneID: "w2:p1", Kind: KindPane})
	gone, _ := registry.Mint(ctx, Binding{PaneID: "w3:p1", Kind: KindPane})
	if err := registry.RevokeJob(ctx, "j_1"); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{jobPane, jobStart} {
		if _, ok, _ := registry.Resolve(ctx, token); ok {
			t.Fatal("a finished job's token still resolves")
		}
	}
	live := func(pane string) bool { return pane == "w2:p1" }
	// Bound just now: spared, the snapshot may not show its pane yet.
	if n, _ := registry.RevokeGonePanes(ctx, live, time.Now().Add(-time.Minute)); n != 0 {
		t.Fatalf("revoked %d fresh tokens", n)
	}
	if n, err := registry.RevokeGonePanes(ctx, live, time.Now().Add(time.Minute)); err != nil || n != 1 {
		t.Fatalf("revoked %d, %v; want the closed pane's one", n, err)
	}
	if _, ok, _ := registry.Resolve(ctx, gone); ok {
		t.Fatal("a closed pane's token still resolves")
	}
	if _, ok, _ := registry.Resolve(ctx, orchestrator); !ok {
		t.Fatal("a live pane's token was revoked")
	}
}
