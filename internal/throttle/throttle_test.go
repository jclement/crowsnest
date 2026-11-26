package throttle

import (
	"context"
	"sync"
	"testing"
	"time"
)

type memKV struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *memKV) GetKV(_ context.Context, key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.m[key], nil
}
func (k *memKV) SetKV(_ context.Context, key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[string]string{}
	}
	k.m[key] = value
	return nil
}
func (k *memKV) DeleteKV(_ context.Context, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.m, key)
	return nil
}

func TestDefaultMaxRunning(t *testing.T) {
	for cores, want := range map[int]int{1: 2, 4: 2, 8: 2, 12: 3, 16: 4, 64: 16} {
		if got := DefaultMaxRunning(cores); got != want {
			t.Errorf("%d cores: %d, want %d", cores, got, want)
		}
	}
}

func TestDecide(t *testing.T) {
	settings := Settings{MaxRunning: 3, ProjectLimits: map[int64]int{7: 1}}
	cases := []struct {
		name string
		in   SlotInput
		want string
	}{
		{"free", SlotInput{Running: 1}, ""},
		{"full", SlotInput{Running: 3}, "waiting for a slot: 3 of 3 running"},
		{"queue ahead takes the last slot", SlotInput{Running: 2, Ahead: 1}, "waiting for a slot: 2 of 3 running, 1 ahead in the queue"},
		{"project limit", SlotInput{Running: 1, ProjectID: 7, ProjectRunning: 1}, "waiting for a slot: 1 of 1 running in this project"},
		{"another project is free", SlotInput{Running: 1, ProjectID: 8, ProjectRunning: 1}, ""},
	}
	for _, tc := range cases {
		if got := Decide(settings, tc.in); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSettingsSaveAndReset(t *testing.T) {
	ctx := context.Background()
	th := New(Options{KV: &memKV{}, Cores: 16, Load: func() (float64, error) { return 0, nil }})
	if got := th.Settings(ctx).MaxRunning; got != 4 {
		t.Fatalf("default %d", got)
	}
	if _, err := th.Save(ctx, Settings{MaxRunning: 0}); err == nil {
		t.Fatal("a limit of 0 must be refused")
	}
	if _, err := th.Save(ctx, Settings{MaxRunning: 6, ProjectLimits: map[int64]int{2: 1, 3: 0}, LoadPerCore: 1.5}); err != nil {
		t.Fatal(err)
	}
	got := th.Settings(ctx)
	if got.MaxRunning != 6 || got.ProjectLimits[2] != 1 || len(got.ProjectLimits) != 1 || got.LoadPerCore != 1.5 {
		t.Fatalf("saved %+v", got)
	}
	if reset, _ := th.Reset(ctx); reset.MaxRunning != 4 {
		t.Fatalf("reset %+v", reset)
	}
}

// fakeLoad is a load source the test sets, and a clock it moves.
type fakeLoad struct {
	now  time.Time
	load float64
}

func TestLoadHoldNeedsAWholeMinuteOver(t *testing.T) {
	ctx := context.Background()
	f := &fakeLoad{now: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC), load: 40}
	th := New(Options{Cores: 10, Defaults: Settings{LoadPerCore: 2}, Load: func() (float64, error) { return f.load, nil }, Now: func() time.Time { return f.now }})
	step := func() { th.Sample(); f.now = f.now.Add(10 * time.Second) }
	for i := 0; i < 5; i++ { // samples over 40 s, now at 50 s: not a whole minute yet
		step()
	}
	if th.Load(ctx).Holding {
		t.Fatal("held before a whole minute was seen")
	}
	step()
	step()
	state := th.Load(ctx)
	if !state.Holding || state.Reason == "" {
		t.Fatalf("not held after a minute at 4 per core: %+v", state)
	}
	f.load = 5 // 0.5 per core: one sample under releases the hold
	step()
	if th.Load(ctx).Holding {
		t.Fatal("still held after the load fell")
	}
	f.load = 40
	step()
	if th.Load(ctx).Holding {
		t.Fatal("held again without a whole minute over")
	}
}

func TestLoadHoldOff(t *testing.T) {
	f := &fakeLoad{now: time.Now(), load: 400}
	th := New(Options{Cores: 4, Load: func() (float64, error) { return f.load, nil }, Now: func() time.Time { return f.now }})
	for i := 0; i < 10; i++ {
		th.Sample()
		f.now = f.now.Add(10 * time.Second)
	}
	if th.Load(context.Background()).Holding {
		t.Fatal("the hold is off by default here (threshold 0) and must not hold")
	}
}
