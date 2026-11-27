// The minimal "start paused" after a restore (DESIGN.md 4.17). A restore writes the pause
// switch's own row (kv `pause.state`, by "restore"); where the full pause switch (internal/pause,
// the landing branch) is not built in, this file is what honours that row: while it says paused
// by a restore, the orchestrator's start gate stays shut and the job supervisor does not start,
// so nothing is dispatched until the human has looked and pressed Resume (System page). Resume
// deletes the row. With the full switch present, the same row pauses everything it covers too.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jclement/crowsnest/internal/backup"
	"github.com/jclement/crowsnest/internal/httpd"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/store"
)

// restoreHoldPoll is how often a held start looks whether it was resumed.
const restoreHoldPoll = 3 * time.Second

// restoreHold reads the pause row a restore wrote.
type restoreHold struct {
	db      *store.Store
	journal *journal.Journal
}

// state is the hold, or nil when nothing a restore paused is in force.
func (h restoreHold) state(ctx context.Context) *httpd.RestoreHold {
	raw, err := h.db.GetKV(ctx, backup.PauseKey)
	if err != nil {
		return nil
	}
	var row struct {
		Paused bool      `json:"paused"`
		By     string    `json:"by"`
		At     time.Time `json:"at"`
		Reason string    `json:"reason"`
	}
	if json.Unmarshal([]byte(raw), &row) != nil || !row.Paused || row.By != "restore" {
		return nil
	}
	return &httpd.RestoreHold{At: row.At.UTC().Format(time.RFC3339), Reason: row.Reason}
}

func (h restoreHold) held(ctx context.Context) bool { return h.state(ctx) != nil }

// gate wraps the orchestrator's start gate: shut while held.
func (h restoreHold) gate(next func(context.Context) string) func(context.Context) string {
	return func(ctx context.Context) string {
		if h.held(ctx) {
			return "Paused after a restore: check the agents and the journal, then press Resume on the System page."
		}
		return next(ctx)
	}
}

// wait blocks while held (or until ctx ends) and reports whether it may go on.
func (h restoreHold) wait(ctx context.Context) bool {
	for h.held(ctx) {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(restoreHoldPoll):
		}
	}
	return true
}

// resume lifts a restore's pause (and only that one).
func (h restoreHold) resume(ctx context.Context) error {
	if !h.held(ctx) {
		return nil
	}
	if err := h.db.DeleteKV(ctx, backup.PauseKey); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if h.journal != nil {
		h.journal.Add(ctx, journal.KindStorage, "Resumed after a restore: crowsnest's automation runs again", nil)
	}
	return nil
}
