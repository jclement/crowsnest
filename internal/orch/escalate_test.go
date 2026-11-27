// Tests for lead-first routing's orch half (DESIGN.md 7.4, 21.2): the escalation rule as a
// table, and against the fake herdr: an escalation is sent once and not before EscalateAfter,
// a pane owned by a lead's job makes no "agent settled" reason, a handed-over job's outcome is
// worded as such, a lead's timer follows the orchestrator's interval, and a lead's tender
// compacts at two thirds of the orchestrator's threshold under its own kv key and label.

package orch

import (
	"context"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/transcript"
)

func TestEscalations(t *testing.T) {
	now := pokeEpoch
	stuck := ProjectJob{ID: "j_1", Title: "Build", Project: "Alpha", Stuck: true, Cause: "usage limit"}
	waiting := ProjectJob{ID: "j_2", Title: "Pick a name", Project: "Alpha", NeedsYou: true}
	running := ProjectJob{ID: "j_3", Title: "Fine", Project: "Alpha"}
	at := func(job ProjectJob, ago time.Duration) ProjectJob { job.Since = now.Add(-ago); return job }
	cases := []struct {
		name string
		jobs []ProjectJob
		keys []string
	}{
		{"nothing", nil, nil},
		{"stuck under the threshold", []ProjectJob{at(stuck, EscalateAfter-time.Second)}, nil},
		{"stuck at the threshold", []ProjectJob{at(stuck, EscalateAfter)}, []string{"escalate:j_1:stuck:usage limit"}},
		{"waiting on the human past it", []ProjectJob{at(waiting, time.Hour)}, []string{"escalate:j_2:needs_you"}},
		{"neither stuck nor waiting", []ProjectJob{at(running, time.Hour)}, nil},
		{"since unknown", []ProjectJob{stuck}, nil},
		{"both", []ProjectJob{at(stuck, time.Hour), at(waiting, time.Hour)}, []string{"escalate:j_1:stuck:usage limit", "escalate:j_2:needs_you"}},
	}
	for _, tc := range cases {
		got := Escalations(now, tc.jobs)
		if keys := keysOf(got); !slices.Equal(keys, tc.keys) && (len(keys) != 0 || len(tc.keys) != 0) {
			t.Errorf("%s: keys %v, want %v", tc.name, keys, tc.keys)
		}
	}
	text := Escalations(now, []ProjectJob{at(stuck, 45*time.Minute)})[0].Text
	if text != `escalation: project "Alpha": job j_1 has been stuck (usage limit) for 45 min and its lead has not resolved it: "Build"` {
		t.Fatalf("text %q", text)
	}
}

func TestLeadCanTakePokes(t *testing.T) {
	for state, want := range map[State]bool{
		StateIdle: true, StateWorking: true, StateStarting: true, StateBlocked: true, StateRestarting: true,
		StateFailed: false, StateNeedsTerminal: false, StateStopped: false, StateDisabled: false, "off": false,
	} {
		if got := LeadCanTakePokes(state); got != want {
			t.Errorf("%s: %v, want %v", state, got, want)
		}
	}
}

func TestPokerEscalatesOnceAfterTheThreshold(t *testing.T) {
	since := pokeEpoch
	r := newPokeRig(t, func(o *PokerOptions) {
		o.Escalations = func(_ context.Context, now time.Time) []Reason {
			return Escalations(now, []ProjectJob{{ID: "j_7", Title: "Deploy", Project: "Alpha", Stuck: true, Cause: "no progress", Since: since}})
		}
	})
	r.check()
	r.clock.Advance(EscalateAfter - time.Minute)
	if got := r.check(); got.Outcome != OutcomeNothing {
		t.Fatalf("escalated before the threshold: %+v", got)
	}
	r.clock.Advance(2 * time.Minute)
	got := r.check()
	if got.Outcome != OutcomeSent || len(got.Reasons) != 1 || !strings.HasPrefix(got.Reasons[0], `escalation: project "Alpha": job j_7 has been stuck`) {
		t.Fatalf("escalation: %+v", got)
	}
	r.settleOrchestrator()
	for range 3 {
		r.clock.Advance(10 * time.Minute)
		if got := r.check(); got.Outcome != OutcomeNothing {
			t.Fatalf("escalated twice: %+v", got)
		}
	}
	if len(r.pokes()) != 1 {
		t.Fatalf("pokes %q", r.pokes())
	}
}

func TestPokerSkipsAPaneOwnedElsewhere(t *testing.T) {
	owned := true
	r := newPokeRig(t, func(o *PokerOptions) {
		o.OwnedElsewhere = func(pane string) bool { return owned && pane != "" }
	})
	r.set(r.builder, herdr.StatusDone)
	r.check()
	r.clock.Advance(DoneHold + time.Second)
	if got := r.check(); got.Outcome != OutcomeNothing {
		t.Fatalf("a lead-owned pane was reported to the orchestrator: %+v", got)
	}
	owned = false // its lead stopped: the orchestrator hears it again
	if got := r.check(); got.Outcome != OutcomeSent || !strings.Contains(got.Reasons[0], "finished its turn") {
		t.Fatalf("after ownership ended: %+v", got)
	}
}

func TestPokerWordsHandedOverAndPrefixedJobs(t *testing.T) {
	r := newPokeRig(t)
	ctx := context.Background()
	r.poker.JobChanged(ctx, JobInfo{ID: "j_3", Title: "Ship", State: "done", PaneID: r.builder, Prefix: `project "Alpha": `, HandedOver: true})
	r.poker.JobChanged(ctx, JobInfo{ID: "j_4", Title: "Docs", State: "running", Stuck: true, Cause: "usage limit", PaneID: r.builder, Prefix: `project "Beta" has no lead running: `})
	r.poker.QuestionSettled(ctx, QuestionInfo{ID: "q_9", Title: "Rename?", Status: "answered", SourcePane: r.builder, Prefix: `project "Beta" has no lead running: `})
	got := r.check()
	want := []string{
		`project "Alpha": job j_3 you handed it is done: "Ship"`,
		`project "Beta" has no lead running: job j_4 is stuck (usage limit): "Docs"`,
		`project "Beta" has no lead running: q_9 (asked by pane ` + r.builder + `) was answered: "Rename?"`,
	}
	if got.Outcome != OutcomeSent || !slices.Equal(got.Reasons, want) {
		t.Fatalf("reasons %q", got.Reasons)
	}
}

// A lead's poker follows the orchestrator's live interval and pokes once per change of its
// standing list.
func TestLeadTimerFollowsTheOrchestratorsInterval(t *testing.T) {
	source := NewPoker(PokerOptions{Interval: 15 * time.Minute})
	standing := []string{"job j_2 is stuck (usage limit): Build"}
	r := newPokeRig(t, func(o *PokerOptions) {
		o.IntervalFrom, o.NoAgentReasons, o.Label = source.Interval, true, "the lead of Alpha"
		o.Standing = func(context.Context) []string { return slices.Clone(standing) }
	})
	r.check()
	r.clock.Advance(15 * time.Minute)
	if got := r.check(); got.Outcome != OutcomeSent || !strings.Contains(r.pokes()[0], "still open: job j_2 is stuck") {
		t.Fatalf("timer: %+v", got)
	}
	r.settleOrchestrator()
	r.clock.Advance(15 * time.Minute)
	if got := r.check(); got.Outcome != OutcomeNothing {
		t.Fatalf("unchanged list poked: %+v", got)
	}
	source.SetInterval(5 * time.Minute) // the Settings change reaches the lead
	standing = append(standing, "job j_5 needs the human: Pick")
	r.clock.Advance(5 * time.Minute)
	if got := r.check(); got.Outcome != OutcomeSent || len(r.pokes()) != 2 {
		t.Fatalf("changed list at the new interval: %+v", got)
	}
}

func TestLeadTenderCompactsAtTwoThirds(t *testing.T) {
	r := &tenderRig{pokeRig: newPokeRig(t), handoff: filepath.Join(t.TempDir(), "leads", "alpha", "handoff.md")}
	orchestrator := NewTender(TenderOptions{CompactAt: 150_000, FreshAt: -1})
	r.tender = NewTender(TenderOptions{
		Client: r.fake, KV: r.db, Journal: r.journal, Logger: slog.New(slog.DiscardHandler), Now: r.clock.Now,
		Usage:     func(context.Context) (transcript.Usage, error) { return r.usage, nil },
		CompactAt: LeadCompactAt(150_000), FreshAt: -1, Location: time.UTC, HandoffFile: r.handoff,
		StateKey: "lead.alpha.upkeep.state", Label: "the lead of Alpha",
		ThresholdsFrom: func() (int, int) { c, f := orchestrator.Thresholds(); return LeadCompactAt(c), f },
	})
	if compactAt, _ := r.tender.Thresholds(); compactAt != 100_000 {
		t.Fatalf("threshold %d, want two thirds of 150k", compactAt)
	}
	r.usage = transcript.Usage{Tokens: 99_000, Known: true}
	if r.tend() {
		t.Fatal("compacted under two thirds")
	}
	r.usage.Tokens = 101_000
	if !r.tend() || len(r.pokes()) != 1 || !strings.HasPrefix(r.pokes()[0], "/compact ") {
		t.Fatalf("did not compact at two thirds: %q", r.pokes())
	}
	page, _ := r.journal.List(context.Background(), journal.Query{Limit: 10})
	if len(page.Entries) != 1 || !strings.HasPrefix(page.Entries[0].Summary, "Compacted the lead of Alpha (automatic) at 101000 tokens") {
		t.Fatalf("journal %+v", page.Entries)
	}
	if _, err := r.db.GetKV(context.Background(), "lead.alpha.upkeep.state"); err != nil {
		t.Fatalf("the lead's bookkeeping is not under its own key: %v", err)
	}
	if _, err := r.db.GetKV(context.Background(), kvUpkeep); err == nil {
		t.Fatal("the lead wrote the orchestrator's bookkeeping")
	}
	orchestrator.SetThresholds(300_000, -1) // Settings: the lead follows at two thirds
	if compactAt, _ := r.tender.Thresholds(); compactAt != 200_000 {
		t.Fatalf("live threshold %d", compactAt)
	}
	orchestrator.SetThresholds(0, -1)
	if compactAt, _ := r.tender.Thresholds(); compactAt != 0 {
		t.Fatal("0 must stay off for leads")
	}
}

func TestOwnershipSentence(t *testing.T) {
	if !strings.Contains(OwnershipSentence("lead"), "Owns every job") || !strings.Contains(OwnershipSentence("orchestrator"), "a project's lead owns its jobs") {
		t.Fatal("ownership sentences")
	}
}
