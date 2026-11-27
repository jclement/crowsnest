package presence

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type memKV struct{ m map[string]string }

func (k *memKV) GetKV(_ context.Context, key string) (string, error) { return k.m[key], nil }
func (k *memKV) SetKV(_ context.Context, key, value string) error {
	if k.m == nil {
		k.m = map[string]string{}
	}
	k.m[key] = value
	return nil
}

func TestFold(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name  string
		beats []beat
		want  string
	}{
		{"nobody", nil, Away},
		{"focused with recent input", []beat{{Beat: Beat{Visible: true, Focused: true, IdleFor: 20 * time.Second}}}, Here},
		{"focused but idle", []beat{{Beat: Beat{Visible: true, Focused: true, IdleFor: 10 * time.Minute}}}, Maybe},
		{"visible, not focused", []beat{{Beat: Beat{Visible: true, IdleFor: time.Hour}}}, Maybe},
		{"hidden, used recently", []beat{{Beat: Beat{IdleFor: 5 * time.Minute}}}, Maybe},
		{"hidden and idle", []beat{{Beat: Beat{IdleFor: time.Hour}}}, Away},
		{"one of two browsers is here", []beat{{Beat: Beat{IdleFor: time.Hour}}, {Beat: Beat{Visible: true, Focused: true}}}, Here},
	}
	for _, tc := range cases {
		state := fold(tc.beats, now)
		if state.Presence != tc.want || state.Reason == "" {
			t.Errorf("%s: %+v, want %s", tc.name, state, tc.want)
		}
	}
}

func TestHeartbeatsGoStale(t *testing.T) {
	c := &clock{now: time.Now()}
	s := New(Options{Now: c.Now})
	if got := s.Heartbeat("b1", Beat{Visible: true, Focused: true}); got.Presence != Here || got.Browsers != 1 {
		t.Fatalf("after a beat: %+v", got)
	}
	c.Advance(StaleAfter + time.Second)
	if got := s.State(context.Background()); got.Presence != Away || got.Browsers != 0 {
		t.Fatalf("a stale browser still counts: %+v", got)
	}
	s.Heartbeat("b1", Beat{Visible: true, Focused: true})
	s.Gone("b1")
	if s.Presence() != Away {
		t.Fatal("a closed tab still counts")
	}
}

// TestRouting is the notification routing table (DESIGN.md 6.14).
func TestRouting(t *testing.T) {
	ctx := context.Background()
	type step struct {
		presence string // the beat sent first: here, maybe or away (none)
		item     string
		channel  string
		blocking bool
		want     bool
	}
	cases := []struct {
		name  string
		steps []step
	}{
		{"here: tray yes, voice yes, no push, no notify", []step{
			{Here, "a", ChannelTray, false, true}, {Here, "b", ChannelVoice, false, true},
			{Here, "c", ChannelPush, false, false}, {Here, "d", ChannelNotify, true, false},
		}},
		{"away: push and notify, no voice", []step{
			{Away, "a", ChannelPush, false, true}, {Away, "a", ChannelNotify, false, true}, {Away, "b", ChannelVoice, false, false},
		}},
		{"maybe: push only", []step{
			{Maybe, "a", ChannelPush, false, true}, {Maybe, "b", ChannelNotify, false, false}, {Maybe, "c", ChannelNotify, true, true},
		}},
		{"never both channels for one item within the window", []step{
			{Here, "q_1", ChannelVoice, false, true}, {Away, "q_1", ChannelPush, false, false},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &clock{now: time.Now()}
			s := New(Options{Now: c.Now})
			for i, st := range tc.steps {
				switch st.presence {
				case Here:
					s.Heartbeat("b", Beat{Visible: true, Focused: true})
				case Maybe:
					s.Heartbeat("b", Beat{Visible: true, IdleFor: time.Hour})
				default:
					s.Gone("b")
				}
				if ok, why := s.Route(ctx, st.item, st.channel, st.blocking); ok != st.want {
					t.Fatalf("step %d (%s %s blocking=%v): %v (%s), want %v", i, st.item, st.channel, st.blocking, ok, why, st.want)
				}
			}
		})
	}
}

func TestDoNotDisturbHoldsAndSummarises(t *testing.T) {
	ctx := context.Background()
	c := &clock{now: time.Now()}
	var summary int
	s := New(Options{Now: c.Now, KV: &memKV{}, OnDNDEnd: func(held int) { summary = held }})
	if _, err := s.SetDND(ctx, c.now.Add(-time.Minute)); err == nil {
		t.Fatal("a do-not-disturb time in the past must be refused")
	}
	if _, err := s.SetDND(ctx, c.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if ok, why := s.Route(ctx, "n1", ChannelPush, false); ok || !strings.Contains(why, "do not disturb") {
		t.Fatalf("not held: %v %s", ok, why)
	}
	if ok, _ := s.Route(ctx, "n2", ChannelNotify, false); ok {
		t.Fatal("notify-url not held")
	}
	if ok, _ := s.Route(ctx, "q_9", ChannelPush, true); !ok {
		t.Fatal("a blocking item must get through do not disturb")
	}
	// It survives a restart (kv).
	again := New(Options{Now: c.Now, KV: s.opts.KV})
	if again.State(ctx).DNDUntil == nil {
		t.Fatal("do not disturb was not kept")
	}
	if _, err := s.SetDND(ctx, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if summary != 2 {
		t.Fatalf("summary of held notifications: %d", summary)
	}
}
