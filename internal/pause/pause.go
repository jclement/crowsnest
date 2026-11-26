// Package pause is the one switch that stops crowsnest's own automation without stopping any
// agent (DESIGN.md 6.13). While paused nothing crowsnest does by itself reaches an agent: no
// pokes, no nudges or backstop prompts, no scheduled tasks, no new job starts, no restarts or
// retries, no compaction. What the human does still works (chat, answers, approvals, Start
// anyway, Poke now), hooks still record, and questions and approvals still arrive. The Stop hook
// lets every turn end while paused, so no agent is held in a loop against a supervisor that will
// not act.
//
// The state lives in kv `pause.state` (it survives restarts) and may carry an auto-resume time.
// A file, `~/.config/crowsnest/paused` (beside the config file), pauses too: the front door's
// switch, so a shell or another tool can pause crowsnest without its API. Resume removes it.
//
// Every caller asks Paused(); it is cheap (one kv read and one stat, cached for a second) so the
// supervisor tick, the poker and the schedule can ask on every pass.
package pause

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/journal"
)

const (
	// kvKey holds the state as JSON.
	kvKey = "pause.state"
	// cacheFor bounds how stale Paused() may be: callers ask many times a second at worst, and a
	// pause that takes a second to bite is fine.
	cacheFor = time.Second
	// maxReasonRunes keeps the banner's reason to a line.
	maxReasonRunes = 200
	// watchInterval is how often Run looks for an auto-resume time passing or the file changing.
	watchInterval = 5 * time.Second
)

// KV is the part of the store the switch needs.
type KV interface {
	GetKV(ctx context.Context, key string) (string, error)
	SetKV(ctx context.Context, key, value string) error
}

// State is the switch as the API returns it.
type State struct {
	Paused bool `json:"paused"`
	// By is who paused: "you" (the web app), "cli", "file", or an agent's name.
	By     string     `json:"by,omitempty"`
	At     *time.Time `json:"at,omitempty"`
	Until  *time.Time `json:"until,omitempty"` // auto-resume time; nil: until resumed
	Reason string     `json:"reason,omitempty"`
	// File is the paused file's path; FileExists says it is what holds the pause (also when the
	// stored state is not paused).
	File       string `json:"file"`
	FileExists bool   `json:"file_exists"`
}

// stored is what kv keeps.
type stored struct {
	Paused bool      `json:"paused"`
	By     string    `json:"by,omitempty"`
	At     time.Time `json:"at"`
	Until  time.Time `json:"until"`
	Reason string    `json:"reason,omitempty"`
}

// Options configure a Switch.
type Options struct {
	KV KV
	// File is the paused file (DefaultFile). Empty: no file is honoured.
	File    string
	Hub     *hub.Hub         // nil: no `pause` events
	Journal *journal.Journal // nil: nothing journalled
	Logger  *slog.Logger
	Now     func() time.Time
}

// Switch is the pause switch. The zero value is not usable; call New. A nil *Switch is never
// paused, so callers that were not given one need no check.
type Switch struct {
	opts Options

	mu       sync.Mutex
	cached   State
	cachedAt time.Time
	last     bool // the last paused value Run saw, to publish changes once
	held     map[string]bool
}

// New returns a Switch.
func New(opts Options) *Switch {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Switch{opts: opts}
}

// DefaultFile is the paused file beside the config file: ~/.config/crowsnest/paused (XDG).
func DefaultFile(configDir string) string {
	if configDir == "" {
		return ""
	}
	return strings.TrimRight(configDir, "/") + "/paused"
}

// Paused reports whether the automation is paused now. A nil switch is never paused.
func (s *Switch) Paused() bool {
	if s == nil {
		return false
	}
	return s.State(context.Background()).Paused
}

// State reads the switch: the stored state (an auto-resume time that has passed resumes it,
// journalled once) and the paused file.
func (s *Switch) State(ctx context.Context) State {
	if s == nil {
		return State{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	if !s.cachedAt.IsZero() && now.Sub(s.cachedAt) < cacheFor && now.After(s.cachedAt) {
		return s.cached
	}
	state := s.readLocked(ctx, now)
	s.cached, s.cachedAt = state, now
	return state
}

func (s *Switch) readLocked(ctx context.Context, now time.Time) State {
	saved := s.load(ctx)
	if saved.Paused && !saved.Until.IsZero() && !now.Before(saved.Until) {
		// The auto-resume time has passed: resume, once.
		saved = stored{}
		s.save(ctx, saved)
		s.record(ctx, "Automation resumed: the auto-resume time came", map[string]any{"by": "auto-resume"})
	}
	state := State{File: s.opts.File}
	if saved.Paused {
		at := saved.At.UTC()
		state.Paused, state.By, state.At, state.Reason = true, saved.By, &at, saved.Reason
		if !saved.Until.IsZero() {
			until := saved.Until.UTC()
			state.Until = &until
		}
	}
	if s.opts.File != "" {
		if info, err := os.Stat(s.opts.File); err == nil && !info.IsDir() {
			state.FileExists = true
			if !state.Paused {
				at := info.ModTime().UTC()
				state.Paused, state.By, state.At = true, "file", &at
				state.Reason = "the paused file exists: " + s.opts.File
			}
		}
	}
	return state
}

// Pause pauses the automation. until, when not zero, resumes it by itself then; it must be in
// the future. by names who asked; reason is optional.
func (s *Switch) Pause(ctx context.Context, by string, until time.Time, reason string) (State, error) {
	if s == nil {
		return State{}, errors.New("the pause switch is not available on this server")
	}
	now := s.opts.Now()
	if !until.IsZero() && !until.After(now) {
		return State{}, fmt.Errorf("the auto-resume time %s has already passed; give a time in the future", until.Format(time.RFC3339))
	}
	reason = oneLine(reason, maxReasonRunes)
	s.mu.Lock()
	saved := stored{Paused: true, By: oneLine(by, 64), At: now, Until: until, Reason: reason}
	if err := s.save(ctx, saved); err != nil {
		s.mu.Unlock()
		return State{}, err
	}
	s.cachedAt = time.Time{}
	s.mu.Unlock()
	summary := "Automation paused by " + saved.By
	if !until.IsZero() {
		summary += " until " + until.Local().Format("15:04")
	}
	if reason != "" {
		summary += ": " + reason
	}
	s.record(ctx, summary, map[string]any{"by": saved.By, "until": until, "reason": reason})
	state := s.State(ctx)
	s.publish(state)
	return state, nil
}

// Resume resumes the automation: the stored pause is cleared and the paused file, if any, is
// removed (it is crowsnest's own file). A file that cannot be removed is an error, since the
// automation would stay paused.
func (s *Switch) Resume(ctx context.Context, by string) (State, error) {
	if s == nil {
		return State{}, errors.New("the pause switch is not available on this server")
	}
	s.mu.Lock()
	if err := s.save(ctx, stored{}); err != nil {
		s.mu.Unlock()
		return State{}, err
	}
	if s.opts.File != "" {
		if err := os.Remove(s.opts.File); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.mu.Unlock()
			return State{}, fmt.Errorf("removing the paused file %s: %w (remove it by hand to resume)", s.opts.File, err)
		}
	}
	s.cachedAt = time.Time{}
	s.held = nil
	s.mu.Unlock()
	s.record(ctx, "Automation resumed by "+oneLine(by, 64), map[string]any{"by": by})
	state := s.State(ctx)
	s.publish(state)
	return state, nil
}

// Run publishes a `pause` event whenever the switch changes by itself (an auto-resume, the file
// appearing or going), until ctx ends.
func (s *Switch) Run(ctx context.Context) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.last = s.readLocked(ctx, s.opts.Now()).Paused
	s.mu.Unlock()
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		state := s.State(ctx)
		s.mu.Lock()
		changed := state.Paused != s.last
		s.last = state.Paused
		s.mu.Unlock()
		if changed {
			s.publish(state)
		}
	}
}

// Held journals, once per key for as long as the pause lasts, that something was not done
// because the automation is paused: "a nudge for j_4", "the start of j_7". A supervisor that asks
// every ten seconds journals each held thing once, not every tick.
func (s *Switch) Held(ctx context.Context, key, what string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.held == nil {
		s.held = map[string]bool{}
	}
	seen := s.held[key]
	s.held[key] = true
	s.mu.Unlock()
	if !seen {
		s.record(ctx, "Held while paused: "+oneLine(what, 240), map[string]any{"key": key})
	}
}

func (s *Switch) load(ctx context.Context) stored {
	if s.opts.KV == nil {
		return stored{}
	}
	raw, err := s.opts.KV.GetKV(ctx, kvKey)
	if err != nil || raw == "" {
		return stored{}
	}
	var saved stored
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		s.opts.Logger.Warn("the stored pause state does not parse; treating it as not paused", "err", err)
		return stored{}
	}
	return saved
}

func (s *Switch) save(ctx context.Context, saved stored) error {
	if s.opts.KV == nil {
		return errors.New("the pause switch has no store")
	}
	body, _ := json.Marshal(saved)
	if err := s.opts.KV.SetKV(ctx, kvKey, string(body)); err != nil {
		return fmt.Errorf("saving the pause state: %w", err)
	}
	return nil
}

func (s *Switch) record(ctx context.Context, summary string, detail map[string]any) {
	s.opts.Logger.Info(summary)
	if s.opts.Journal != nil {
		s.opts.Journal.Add(ctx, journal.KindPause, summary, detail)
	}
}

func (s *Switch) publish(state State) {
	if s.opts.Hub != nil {
		s.opts.Hub.Publish(hub.Event{Type: "pause", Data: state})
	}
}

func oneLine(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return text
}
