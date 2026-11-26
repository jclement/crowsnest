// Tests for keeping the orchestrator small (DESIGN.md 7.9): the compaction and daily-fresh rules
// as tables, then the Tender against the fake herdr with a fake clock.

package orch

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/transcript"
)

func TestDecideCompact(t *testing.T) {
	t0 := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	ready := Readiness{Idle: true, ScreenRead: true}
	known := func(tokens int) transcript.Usage { return transcript.Usage{Tokens: tokens, Known: true, At: t0} }
	cases := []struct {
		name    string
		in      CompactInput
		compact bool
		wait    bool
	}{
		{"off", CompactInput{Threshold: 0, Usage: known(900_000), Readiness: ready}, false, false},
		{"unknown size", CompactInput{Threshold: 150_000, Usage: transcript.Usage{}, Readiness: ready}, false, false},
		{"under", CompactInput{Threshold: 150_000, Usage: known(149_999), Readiness: ready}, false, false},
		{"at the threshold", CompactInput{Threshold: 150_000, Usage: known(150_000), Readiness: ready}, true, false},
		{"working", CompactInput{Threshold: 150_000, Usage: known(200_000), Readiness: Readiness{ScreenRead: true}}, false, true},
		{"draft", CompactInput{Threshold: 150_000, Usage: known(200_000), Readiness: Readiness{Idle: true, ScreenRead: true, Draft: true}}, false, true},
		{"dialog", CompactInput{Threshold: 150_000, Usage: known(200_000), Readiness: Readiness{Idle: true, ScreenRead: true, Dialog: true}}, false, true},
		{"unread screen", CompactInput{Threshold: 150_000, Usage: known(200_000), Readiness: Readiness{Idle: true}}, false, true},
		{"held job mid-step", CompactInput{Threshold: 150_000, Usage: known(200_000), Readiness: ready, HeldJobBusy: true}, false, true},
		{
			"no growth since an attempt that did not take",
			CompactInput{Threshold: 150_000, Usage: known(200_000), Last: CompactAttempt{At: t0.Add(-time.Minute), Tokens: 200_000}, Readiness: ready},
			false, false,
		},
		{
			"growth since the attempt",
			CompactInput{Threshold: 150_000, Usage: known(210_000), Last: CompactAttempt{At: t0.Add(-time.Minute), Tokens: 200_000}, Readiness: ready},
			true, false,
		},
		{
			"a compaction happened since the attempt and it grew back",
			CompactInput{Threshold: 150_000, Usage: transcript.Usage{Tokens: 160_000, Known: true, CompactedAt: t0.Add(-30 * time.Second)}, Last: CompactAttempt{At: t0.Add(-time.Hour), Tokens: 200_000}, Readiness: ready},
			true, false,
		},
	}
	for _, tc := range cases {
		got := DecideCompact(tc.in)
		if got.Compact != tc.compact || got.Wait != tc.wait || (!got.Compact && got.Reason == "") {
			t.Errorf("%s: %+v, want compact=%v wait=%v", tc.name, got, tc.compact, tc.wait)
		}
	}
}

func TestDecideFresh(t *testing.T) {
	loc := time.UTC
	at := func(hhmm string) time.Time {
		parsed, _ := time.ParseInLocation("2006-01-02 15:04", "2026-11-26 "+hhmm, loc)
		return parsed
	}
	ready := Readiness{Idle: true, ScreenRead: true}
	base := FreshInput{Location: loc, AtMinutes: 4 * 60, DoneDay: "2026-11-25", HasTranscript: true, Readiness: ready}
	cases := []struct {
		name   string
		change func(*FreshInput)
		want   FreshStep
	}{
		{"off", func(in *FreshInput) { in.AtMinutes = -1 }, FreshNone},
		{"before the time", func(in *FreshInput) { in.Now = at("03:59") }, FreshNone},
		{"first run marks today", func(in *FreshInput) { in.DoneDay = "" }, FreshMarkToday},
		{"done today", func(in *FreshInput) { in.DoneDay = "2026-11-26" }, FreshNone},
		{"busy", func(in *FreshInput) { in.Readiness = Readiness{ScreenRead: true} }, FreshNone},
		{"human spoke 10 minutes ago", func(in *FreshInput) { in.Usage.HumanAt = at("04:20") }, FreshNone},
		{"human quiet 20 minutes", func(in *FreshInput) { in.Usage.HumanAt = at("04:10") }, FreshHandoff},
		{"handoff first", func(*FreshInput) {}, FreshHandoff},
		{"handoff yesterday does not count", func(in *FreshInput) { in.HandoffAt = at("04:30").Add(-24 * time.Hour) }, FreshHandoff},
		{"handoff just sent: let the turn end", func(in *FreshInput) { in.HandoffAt = at("04:29").Add(40 * time.Second) }, FreshNone},
		{"after the handoff: restart", func(in *FreshInput) { in.HandoffAt = at("04:25") }, FreshRestart},
		{"empty conversation: restart without a handoff", func(in *FreshInput) { in.HasTranscript = false }, FreshRestart},
	}
	for _, tc := range cases {
		in := base
		in.Now = at("04:30")
		tc.change(&in)
		if got := DecideFresh(in); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// ---- The Tender against the fake -------------------------------------------------------

type tenderRig struct {
	*pokeRig
	usage   transcript.Usage
	tender  *Tender
	fresh   int
	handoff string
}

func newTenderRig(t *testing.T, freshAt int) *tenderRig {
	r := &tenderRig{pokeRig: newPokeRig(t), handoff: filepath.Join(t.TempDir(), "handoff.md")}
	r.tender = NewTender(TenderOptions{
		Client: r.fake, KV: r.db, Journal: r.journal, Logger: slog.New(slog.DiscardHandler), Now: r.clock.Now,
		Usage:     func(context.Context) (transcript.Usage, error) { return r.usage, nil },
		CompactAt: 150_000, FreshAt: freshAt, Location: time.UTC, HandoffFile: r.handoff,
		NewConversation: func(context.Context) (Status, error) { r.fresh++; return Status{State: StateIdle}, nil },
	})
	return r
}

func (r *tenderRig) tend() bool { return r.tender.Tend(context.Background(), r.clock.Now(), r.status()) }

func TestTenderCompactsOnceUntilGrowth(t *testing.T) {
	r := newTenderRig(t, -1)
	r.usage = transcript.Usage{Tokens: 100_000, Known: true}
	if r.tend() || len(r.pokes()) != 0 {
		t.Fatal("compacted under the threshold")
	}
	r.usage.Tokens = 160_000
	r.set(r.orch, herdr.StatusWorking)
	if r.tend() {
		t.Fatal("compacted a working orchestrator")
	}
	r.settleOrchestrator()
	r.screen(r.orch, draftInput)
	if r.tend() {
		t.Fatal("compacted over a draft")
	}
	r.screen(r.orch, emptyInput)
	if !r.tend() {
		t.Fatal("did not compact")
	}
	if prompts := r.pokes(); len(prompts) != 1 || !strings.HasPrefix(prompts[0], "/compact Keep: every open job") {
		t.Fatalf("typed %q", prompts)
	}
	r.settleOrchestrator()
	r.clock.Advance(time.Minute)
	if r.tend() {
		t.Fatal("compacted twice without growth")
	}
	r.usage = transcript.Usage{Tokens: 20_000, Known: true, CompactedAt: r.clock.Now()}
	if r.tend() {
		t.Fatal("compacted after the compaction took")
	}
	if kinds := r.journalKinds(); len(kinds) != 1 || kinds[0] != journal.KindUpkeep {
		t.Fatalf("journal %v", kinds)
	}
	// A restart keeps the attempt: the same unchanged reading is not compacted again.
	r.usage = transcript.Usage{Tokens: 160_000, Known: true}
	again := NewTender(r.tender.opts)
	if again.Tend(context.Background(), r.clock.Now(), r.status()) {
		t.Fatal("a restarted tender compacted without growth")
	}
	if info := again.Info(context.Background()); info.Threshold != 150_000 || info.LastCompactAt.IsZero() || info.Tokens != 160_000 {
		t.Fatalf("info %+v", info)
	}
}

func TestTenderCompactNow(t *testing.T) {
	r := newTenderRig(t, -1)
	r.usage = transcript.Usage{Tokens: 5_000, Known: true}
	r.set(r.orch, herdr.StatusWorking)
	if err := r.tender.CompactNow(context.Background(), r.status()); err != ErrNotReady {
		t.Fatalf("working: %v", err)
	}
	r.settleOrchestrator()
	if err := r.tender.CompactNow(context.Background(), r.status()); err != nil || len(r.pokes()) != 1 {
		t.Fatalf("idle: %v %q", err, r.pokes())
	}
}

// The daily sequence: first sight marks the day; the next day after the time and a quiet human,
// the hand-over prompt; once the turn has ended, the new conversation; then nothing until the
// following day.
func TestTenderFreshSequence(t *testing.T) {
	r := newTenderRig(t, 3*60+10) // pokeEpoch is 03:10 UTC
	r.usage = transcript.Usage{Tokens: 50_000, Known: true, At: pokeEpoch, HumanAt: pokeEpoch.Add(-time.Hour)}
	if r.tend() || r.fresh != 0 {
		t.Fatal("the first run must only mark the day")
	}
	r.clock.Advance(24 * time.Hour)
	if !r.tend() || len(r.pokes()) != 1 || !strings.Contains(r.pokes()[0], r.handoff) {
		t.Fatalf("hand-over: %q", r.pokes())
	}
	if !strings.HasPrefix(r.pokes()[0], "[crowsnest upkeep]") {
		t.Fatalf("the hand-over must not read as the human: %q", r.pokes()[0])
	}
	r.settleOrchestrator()
	if r.tend() || r.fresh != 0 {
		t.Fatal("restarted before the hand-over turn settled")
	}
	if err := os.WriteFile(r.handoff, []byte("notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(time.Minute)
	if err := os.Chtimes(r.handoff, r.clock.Now(), r.clock.Now()); err != nil { // written on the fake clock
		t.Fatal(err)
	}
	if !r.tend() || r.fresh != 1 {
		t.Fatalf("no new conversation (fresh=%d)", r.fresh)
	}
	r.clock.Advance(time.Hour)
	if r.tend() || r.fresh != 1 {
		t.Fatal("a second new conversation on the same day")
	}
	page, _ := r.journal.List(context.Background(), journal.Query{Kinds: []journal.Kind{journal.KindUpkeep}})
	if len(page.Entries) != 2 || !strings.Contains(page.Entries[0].Summary, "hand-over is in its first context") {
		t.Fatalf("journal %+v", page.Entries)
	}
}
