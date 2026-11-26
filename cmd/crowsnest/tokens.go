// The token sweeper (R2-L1): agent tokens of panes herdr no longer has are revoked, once a
// minute. herdr reuses pane ids, so a token that outlived its pane would let whoever holds it
// have answers typed into the next pane given that id. A job's tokens are also revoked when the
// job finishes (internal/jobs/tokens.go); this catches panes closed by hand and agents that
// crowsnest restarted elsewhere.

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/herdr"
)

const (
	// tokenSweepEvery is how often the sweeper compares the bindings with herdr's panes.
	tokenSweepEvery = time.Minute
	// tokenSweepGrace spares bindings younger than this: a pane created a moment ago may not be
	// in the list the sweeper just read.
	tokenSweepGrace = 2 * time.Minute
)

// startTokenSweeper runs the sweep until ctx ends.
func startTokenSweeper(ctx context.Context, client herdr.Client, identity *agentid.Registry, logger *slog.Logger) {
	go func() {
		ticker := time.NewTicker(tokenSweepEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweepTokens(ctx, client, identity, logger)
			}
		}
	}()
}

// sweepTokens revokes the tokens of panes missing from herdr's list. When herdr cannot be read,
// nothing is revoked: an outage is not proof that a pane is gone.
func sweepTokens(ctx context.Context, client herdr.Client, identity *agentid.Registry, logger *slog.Logger) {
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	panes, err := client.PaneList(callCtx, "")
	cancel()
	if err != nil {
		logger.Debug("token sweep: herdr did not list its panes; nothing revoked", "err", err)
		return
	}
	live := make(map[string]bool, len(panes))
	for _, pane := range panes {
		live[pane.ID] = true
	}
	revoked, err := identity.RevokeGonePanes(ctx, func(paneID string) bool { return live[paneID] }, time.Now().Add(-tokenSweepGrace))
	switch {
	case err != nil:
		logger.Warn("token sweep failed", "err", err)
	case revoked > 0:
		logger.Info("revoked the agent tokens of closed panes", "count", revoked)
	}
}
