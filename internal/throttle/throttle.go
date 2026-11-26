// Package throttle keeps a machine usable while agents work (DESIGN.md 6.12). Jeff's machine hit
// a load of 63 with three agents running test suites at once; the fix has three parts, and this
// package holds two of them:
//
//   - The running-job limit: at most MaxRunning job agents at once (default a quarter of the
//     cores, at least 2), with an optional limit per project. A job over the limit waits queued
//     with the reason shown and starts in order. Standing agents (the orchestrator, project
//     leads) are not counted. The count itself is a store query (store.CountSlots) run inside
//     the job launcher's claim, so two launches can never both take the last slot.
//   - The load hold: when the load average per core stays above LoadPerCore for a minute, new
//     job starts wait (running work is never touched). Sampled every 10 s.
//
// The third part, the heavy-command gate (`crowsnest heavy`), is heavy.go.
//
// Settings come from flags (--max-running-jobs, --load-hold) and can be changed in the app; the
// app's values are kept in kv `throttle.settings` and win over the flags until cleared.
package throttle

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime"
	"sort"
	"sync"
	"time"
)

const (
	kvKey = "throttle.settings"
	// loadWindow is how long the load must stay above the threshold before starts are held: a
	// single sample over it is a compile finishing, not an overloaded machine.
	loadWindow = time.Minute
	// sampleInterval is how often the load is read.
	sampleInterval = 10 * time.Second
	// maxLimit bounds any configured limit (a typo of 1000 is not a plan).
	maxLimit = 256
)

// DefaultMaxRunning is the running-job limit when none is configured: a quarter of the cores,
// at least two (one job must always be able to run beside another).
func DefaultMaxRunning(cores int) int {
	if n := cores / 4; n > 2 {
		return n
	}
	return 2
}

// Settings are the throttle's knobs as the API shows and takes them.
type Settings struct {
	// MaxRunning is the instance-wide limit on running job agents (at least 1).
	MaxRunning int `json:"max_running"`
	// ProjectLimits are optional per-project limits, by project id; 0 or absent: none.
	ProjectLimits map[int64]int `json:"project_limits"`
	// LoadPerCore is the load-average-per-core threshold for holding new starts; 0 disables it.
	LoadPerCore float64 `json:"load_per_core"`
}

// Options configure a Throttle.
type Options struct {
	KV interface {
		GetKV(ctx context.Context, key string) (string, error)
		SetKV(ctx context.Context, key, value string) error
		DeleteKV(ctx context.Context, key string) error
	}
	// Defaults are the flags' values; MaxRunning 0 means DefaultMaxRunning(Cores).
	Defaults Settings
	// Cores is the machine's core count; 0 means runtime.NumCPU().
	Cores int
	// Load reads the one-minute load average; nil means HostLoad. Tests pass a fake.
	Load   func() (float64, error)
	Now    func() time.Time
	Logger *slog.Logger
}

// Throttle holds the settings and the load samples.
type Throttle struct {
	opts Options

	mu      sync.Mutex
	samples []loadSample
	lastErr error
}

type loadSample struct {
	at      time.Time
	perCore float64
}

// New returns a Throttle with its defaults filled in.
func New(opts Options) *Throttle {
	if opts.Cores <= 0 {
		opts.Cores = runtime.NumCPU()
	}
	if opts.Defaults.MaxRunning <= 0 {
		opts.Defaults.MaxRunning = DefaultMaxRunning(opts.Cores)
	}
	if opts.Load == nil {
		opts.Load = HostLoad
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Throttle{opts: opts}
}

// Cores is the core count the defaults and the per-core load use.
func (t *Throttle) Cores() int { return t.opts.Cores }

// Settings returns the settings in force: the app's saved values, else the flags'.
func (t *Throttle) Settings(ctx context.Context) Settings {
	settings := t.opts.Defaults
	if t.opts.KV != nil {
		if raw, err := t.opts.KV.GetKV(ctx, kvKey); err == nil && raw != "" {
			var saved Settings
			if err := json.Unmarshal([]byte(raw), &saved); err == nil && saved.MaxRunning > 0 {
				settings = saved
			}
		}
	}
	if settings.ProjectLimits == nil {
		settings.ProjectLimits = map[int64]int{}
	}
	return settings
}

// Defaults are the flags' values (what Reset goes back to).
func (t *Throttle) Defaults() Settings { return t.opts.Defaults }

// Save validates and stores settings made in the app.
func (t *Throttle) Save(ctx context.Context, settings Settings) (Settings, error) {
	if settings.MaxRunning < 1 || settings.MaxRunning > maxLimit {
		return Settings{}, fmt.Errorf("the running-job limit must be between 1 and %d", maxLimit)
	}
	if settings.LoadPerCore < 0 || settings.LoadPerCore > 64 {
		return Settings{}, fmt.Errorf("the load threshold per core must be between 0 (off) and 64")
	}
	clean := map[int64]int{}
	for project, limit := range settings.ProjectLimits {
		if limit < 0 || limit > maxLimit {
			return Settings{}, fmt.Errorf("a project's limit must be between 0 (none) and %d", maxLimit)
		}
		if limit > 0 {
			clean[project] = limit
		}
	}
	settings.ProjectLimits = clean
	if t.opts.KV == nil {
		return Settings{}, fmt.Errorf("the throttle settings cannot be saved on this server")
	}
	body, _ := json.Marshal(settings)
	if err := t.opts.KV.SetKV(ctx, kvKey, string(body)); err != nil {
		return Settings{}, fmt.Errorf("saving the throttle settings: %w", err)
	}
	return settings, nil
}

// Reset forgets the app's settings, so the flags apply again.
func (t *Throttle) Reset(ctx context.Context) (Settings, error) {
	if t.opts.KV != nil {
		if err := t.opts.KV.DeleteKV(ctx, kvKey); err != nil {
			return Settings{}, err
		}
	}
	return t.Settings(ctx), nil
}

// ---- The load hold -------------------------------------------------------------------------

// Sample reads the load once and keeps the samples of the last window.
func (t *Throttle) Sample() {
	load, err := t.opts.Load()
	now := t.opts.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if err != nil {
		if t.lastErr == nil {
			t.opts.Logger.Debug("reading the load average failed; the load hold is idle", "err", err)
		}
		t.lastErr = err
		return
	}
	t.lastErr = nil
	t.samples = append(t.samples, loadSample{at: now, perCore: load / float64(t.opts.Cores)})
	cutoff := now.Add(-2 * loadWindow)
	kept := t.samples[:0]
	for _, sample := range t.samples {
		if sample.at.After(cutoff) {
			kept = append(kept, sample)
		}
	}
	t.samples = kept
}

// Run samples the load every 10 s until ctx ends.
func (t *Throttle) Run(ctx context.Context) {
	t.Sample()
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.Sample()
		}
	}
}

// LoadState is the load as the API shows it.
type LoadState struct {
	PerCore   float64 `json:"per_core"`  // the latest sample; 0 when none
	Threshold float64 `json:"threshold"` // 0: the hold is off
	Holding   bool    `json:"holding"`
	Reason    string  `json:"reason,omitempty"`
	Cores     int     `json:"cores"`
}

// Load reports whether new starts are held for load: every sample of the last minute is above
// the threshold, and the samples cover the minute (a server up for ten seconds cannot know).
func (t *Throttle) Load(ctx context.Context) LoadState {
	threshold := t.Settings(ctx).LoadPerCore
	now := t.opts.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	state := LoadState{Threshold: threshold, Cores: t.opts.Cores}
	if len(t.samples) > 0 {
		state.PerCore = t.samples[len(t.samples)-1].perCore
	}
	if threshold <= 0 || len(t.samples) == 0 {
		return state
	}
	// The minute is seen whole only from the last sample at or before its start; from there on
	// every sample must be over the threshold.
	windowStart, first := now.Add(-loadWindow), -1
	for i, sample := range t.samples {
		if !sample.at.After(windowStart) {
			first = i
		}
	}
	if first < 0 {
		return state
	}
	for _, sample := range t.samples[first:] {
		if sample.perCore <= threshold {
			return state
		}
	}
	state.Holding = true
	state.Reason = fmt.Sprintf("the machine is busy: load %.1f per core for a minute (hold above %.1f)", state.PerCore, threshold)
	return state
}

// ---- The slot decision ----------------------------------------------------------------------

// SlotInput is what Decide weighs for one job's start.
type SlotInput struct {
	// Running and Ahead are the instance-wide counts (store.CountSlots with project 0).
	Running, Ahead int
	// ProjectRunning and ProjectAhead are the job's project's counts (zero for Unfiled).
	ProjectRunning, ProjectAhead int
	ProjectID                    int64
}

// Decide says whether a job may start now under settings. It returns the reason it waits ("" to
// start): "waiting for a slot: 3 of 3 running" names the limit that holds it.
func Decide(settings Settings, in SlotInput) string {
	if in.Running+in.Ahead >= settings.MaxRunning {
		return waitLine(in.Running, settings.MaxRunning, in.Ahead, "")
	}
	if in.ProjectID != 0 {
		if limit := settings.ProjectLimits[in.ProjectID]; limit > 0 && in.ProjectRunning+in.ProjectAhead >= limit {
			return waitLine(in.ProjectRunning, limit, in.ProjectAhead, " in this project")
		}
	}
	return ""
}

func waitLine(running, limit, ahead int, scope string) string {
	line := fmt.Sprintf("waiting for a slot: %d of %d running%s", running, limit, scope)
	if ahead > 0 {
		line += fmt.Sprintf(", %d ahead in the queue", ahead)
	}
	return line
}

// SortedProjectLimits lists the per-project limits by project id, for stable output.
func SortedProjectLimits(limits map[int64]int) []int64 {
	ids := make([]int64, 0, len(limits))
	for id := range limits {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
