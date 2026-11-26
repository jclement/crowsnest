package agentid

import (
	"context"
	"strings"
	"testing"

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
	token, err := registry.Mint(ctx, Binding{PaneID: "w1:p1", JobID: "j_3", Kind: KindMCP})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "cnt_") || len(token) < 40 {
		t.Fatalf("token %q", token)
	}
	binding, ok, err := registry.Resolve(ctx, token)
	if err != nil || !ok || binding.PaneID != "w1:p1" || binding.JobID != "j_3" || binding.Kind != KindMCP {
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
	oldMCP, _ := registry.Mint(ctx, Binding{PaneID: "w1:p1", Kind: KindMCP})
	other, _ := registry.Mint(ctx, Binding{PaneID: "w2:p1", Kind: KindPane})

	// The agent restarts in the same pane: a new MCP token replaces the old one only.
	newMCP, _ := registry.Mint(ctx, Binding{PaneID: "w1:p1", Kind: KindMCP})
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
