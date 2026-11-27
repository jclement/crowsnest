package orch

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// TestPokerHeldWhilePausedThenDelivered: while the automation is paused no automatic poke is
// typed and the due reasons stay; "Poke now" still works; after the resume they go out in one
// poke, with the human's presence on the header (DESIGN.md 6.13, 6.14).
func TestPokerHeldWhilePausedThenDelivered(t *testing.T) {
	var mu sync.Mutex
	paused := true
	var held []string
	r := newPokeRig(t, func(o *PokerOptions) {
		o.Paused = func() bool { mu.Lock(); defer mu.Unlock(); return paused }
		o.Held = func(_ context.Context, key, _ string) { mu.Lock(); defer mu.Unlock(); held = append(held, key) }
		o.Presence = func() string { return "away" }
	})
	ctx := context.Background()
	r.poker.JobChanged(ctx, JobInfo{ID: "j_4", Title: "Docs", State: "failed", PaneID: r.builder})
	for range 3 {
		if got := r.check(); got.Outcome != OutcomeNothing {
			t.Fatalf("poked while paused: %+v", got)
		}
	}
	if len(r.pokes()) != 0 {
		t.Fatalf("typed while paused: %v", r.pokes())
	}
	mu.Lock()
	if len(held) == 0 || held[0] != "poke:the orchestrator" {
		t.Fatalf("held %v", held)
	}
	paused = false
	mu.Unlock()
	got := r.check()
	if got.Outcome != OutcomeSent || len(got.Reasons) != 1 {
		t.Fatalf("after resume: %+v", got)
	}
	if pokes := r.pokes(); len(pokes) != 1 || !strings.Contains(pokes[0], " human=away]") || !strings.Contains(pokes[0], "job j_4 is failed") {
		t.Fatalf("poke %v", pokes)
	}
}

func TestPokeNowWorksWhilePaused(t *testing.T) {
	r := newPokeRig(t, func(o *PokerOptions) { o.Paused = func() bool { return true } })
	r.poker.JobChanged(context.Background(), JobInfo{ID: "j_4", Title: "Docs", State: "failed", PaneID: r.builder})
	if got := r.poker.PokeNow(context.Background()); got.Outcome != OutcomeSent {
		t.Fatalf("Poke now while paused: %+v", got)
	}
}

func TestPokeTextWithPresence(t *testing.T) {
	text := PokeTextWith(pokeEpoch, []Reason{{Text: "x"}}, "here")
	if !strings.HasPrefix(text, "[crowsnest poke 2026-11-26T03:10:00Z human=here]\n- x") {
		t.Fatalf("text %q", text)
	}
	if PokeTextWith(pokeEpoch, nil, "bogus") != PokeHeader(pokeEpoch) {
		t.Fatal("an unknown presence must leave the header alone")
	}
}
