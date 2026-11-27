package pause

import (
	"context"
	"os"
	"path/filepath"
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

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func newSwitch(t *testing.T, kv *memKV, c *clock) (*Switch, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "paused")
	return New(Options{KV: kv, File: file, Now: c.Now}), file
}

func TestNilSwitchIsNeverPaused(t *testing.T) {
	var s *Switch
	if s.Paused() {
		t.Fatal("a nil switch must not be paused")
	}
	if _, err := s.Pause(context.Background(), "you", time.Time{}, ""); err == nil {
		t.Fatal("pausing a nil switch must say it is not available")
	}
}

func TestPauseResumePersists(t *testing.T) {
	ctx := context.Background()
	kv := &memKV{}
	c := &clock{now: time.Date(2026, 11, 26, 20, 0, 0, 0, time.UTC)}
	s, _ := newSwitch(t, kv, c)
	if s.Paused() {
		t.Fatal("paused at start")
	}
	state, err := s.Pause(ctx, "you", time.Time{}, "deploy window")
	if err != nil || !state.Paused || state.By != "you" || state.Reason != "deploy window" {
		t.Fatalf("pause: %+v %v", state, err)
	}
	// A new switch on the same store (a restart) is still paused.
	again := New(Options{KV: kv, Now: c.Now})
	if !again.Paused() {
		t.Fatal("the pause did not survive a restart")
	}
	if _, err := again.Resume(ctx, "you"); err != nil {
		t.Fatal(err)
	}
	c.now = c.now.Add(2 * time.Second) // past the cache
	if s.Paused() || again.Paused() {
		t.Fatal("still paused after resume")
	}
}

func TestAutoResume(t *testing.T) {
	ctx := context.Background()
	c := &clock{now: time.Date(2026, 11, 26, 20, 0, 0, 0, time.UTC)}
	s, _ := newSwitch(t, &memKV{}, c)
	if _, err := s.Pause(ctx, "you", c.now.Add(-time.Minute), ""); err == nil {
		t.Fatal("an auto-resume time in the past must be refused")
	}
	if _, err := s.Pause(ctx, "you", c.now.Add(30*time.Minute), ""); err != nil {
		t.Fatal(err)
	}
	c.now = c.now.Add(29 * time.Minute)
	if !s.Paused() {
		t.Fatal("resumed early")
	}
	c.now = c.now.Add(2 * time.Minute)
	if s.Paused() {
		t.Fatal("did not resume at the auto-resume time")
	}
}

func TestPausedFile(t *testing.T) {
	ctx := context.Background()
	c := &clock{now: time.Date(2026, 11, 26, 20, 0, 0, 0, time.UTC)}
	s, file := newSwitch(t, &memKV{}, c)
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	state := s.State(ctx)
	if !state.Paused || state.By != "file" || !state.FileExists {
		t.Fatalf("the paused file did not pause: %+v", state)
	}
	if _, err := s.Resume(ctx, "you"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("resume must remove the paused file")
	}
	c.now = c.now.Add(2 * time.Second)
	if s.Paused() {
		t.Fatal("still paused")
	}
}

func TestHeldOncePerPause(t *testing.T) {
	ctx := context.Background()
	c := &clock{now: time.Date(2026, 11, 26, 20, 0, 0, 0, time.UTC)}
	s, _ := newSwitch(t, &memKV{}, c)
	s.Held(ctx, "nudge:j_1", "a nudge for j_1")
	s.Held(ctx, "nudge:j_1", "a nudge for j_1")
	if len(s.held) != 1 {
		t.Fatalf("held map %v", s.held)
	}
	if _, err := s.Resume(ctx, "you"); err != nil {
		t.Fatal(err)
	}
	if s.held != nil {
		t.Fatal("resume must forget what was held")
	}
}
