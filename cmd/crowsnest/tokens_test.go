package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/store"
)

// failingPanes is a herdr whose pane list fails, as during an outage.
type failingPanes struct{ *fake.Fake }

func (failingPanes) PaneList(context.Context, string) ([]herdr.Pane, error) {
	return nil, errors.New("herdr is restarting")
}

// R2-L1: the sweeper revokes the tokens of panes herdr no longer has, and nothing when herdr
// cannot be read.
func TestSweepRevokesTokensOfClosedPanes(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	client := fake.New()
	_, _, pane := client.AddWorkspace("w", "/x")
	registry := agentid.New(db, func() time.Time { return time.Now().Add(-time.Hour) }) // bound long ago
	alive, _ := registry.Mint(ctx, agentid.Binding{PaneID: pane, Kind: agentid.KindPane})
	closed, _ := registry.Mint(ctx, agentid.Binding{PaneID: "w9:p9", Kind: agentid.KindPane})

	sweepTokens(ctx, failingPanes{client}, registry, slog.Default())
	if _, ok, _ := registry.Resolve(ctx, closed); !ok {
		t.Fatal("an outage revoked a token")
	}
	sweepTokens(ctx, client, registry, slog.Default())
	if _, ok, _ := registry.Resolve(ctx, closed); ok {
		t.Fatal("a closed pane's token still resolves")
	}
	if _, ok, _ := registry.Resolve(ctx, alive); !ok {
		t.Fatal("a live pane's token was revoked")
	}
}
