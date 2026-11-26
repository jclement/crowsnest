// Package sysstat samples the machine for the header's live strip and the System page (DESIGN.md
// 12.x): overall CPU %, memory used and total, the load average against the core count, free disk
// on the data volume. It is how someone sees their agents hammering the box.
//
// The Sampler takes a sample every two seconds, but only while a browser is connected (Active),
// keeps the last minute for the sparkline, and publishes each sample as a `sys` event on the
// hub. In a container whose cgroup limits CPU or memory, the figures are the cgroup's, not the
// host's, and Scope says so.
package sysstat

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/hub"
)

// DefaultInterval is how often a sample is taken while someone is watching.
const DefaultInterval = 2 * time.Second

// historyLength is how many samples are kept: one minute at the default interval.
const historyLength = 30

// Scopes say whose figures a sample carries.
const (
	ScopeHost      = "host"
	ScopeContainer = "container" // a cgroup's limits and usage
)

// Sample is one reading.
type Sample struct {
	At       time.Time `json:"at"`
	CPU      float64   `json:"cpu"`   // percent of all cores, 0..100
	Cores    float64   `json:"cores"` // logical cores (or the cgroup's CPU quota in cores)
	MemUsed  uint64    `json:"mem_used"`
	MemTotal uint64    `json:"mem_total"`
	Load1    float64   `json:"load1"`
	Load5    float64   `json:"load5"`
	Load15   float64   `json:"load15"`
	// DiskFree and DiskTotal are the data volume's (0 when unknown).
	DiskFree  uint64 `json:"disk_free"`
	DiskTotal uint64 `json:"disk_total"`
	Scope     string `json:"scope"`
	// Working is how many agents are working right now (from the fleet; -1 when unknown).
	Working int `json:"working"`
}

// Source reads the machine. Tests give a fake.
type Source interface {
	Sample(ctx context.Context) (Sample, error)
}

// Options configure a Sampler.
type Options struct {
	Source   Source
	Hub      *hub.Hub      // nil: no events
	Interval time.Duration // DefaultInterval when zero
	// Active reports whether anyone is watching (a browser's event stream is open). Nil: always.
	Active func() bool
	// Working counts working agents (the fleet). Nil: Working is -1.
	Working func() int
	Logger  *slog.Logger
}

// Stats is GET /system/stats: the newest sample and the last minute.
type Stats struct {
	Latest  *Sample  `json:"latest"`
	History []Sample `json:"history"`
}

// Sampler samples while someone watches.
type Sampler struct {
	opts    Options
	mu      sync.Mutex
	history []Sample
	failed  bool
}

// New returns a Sampler; Run starts it.
func New(opts Options) *Sampler {
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Sampler{opts: opts}
}

// Run samples every Interval while Active, until ctx ends.
func (s *Sampler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.opts.Interval)
	defer ticker.Stop()
	for {
		if s.opts.Active == nil || s.opts.Active() {
			s.Tick(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Tick takes one sample, keeps it and publishes it.
func (s *Sampler) Tick(ctx context.Context) {
	sample, err := s.opts.Source.Sample(ctx)
	if err != nil {
		s.mu.Lock()
		first := !s.failed
		s.failed = true
		s.mu.Unlock()
		if first {
			s.opts.Logger.Warn("reading system stats", "err", err)
		}
		return
	}
	sample.Working = -1
	if s.opts.Working != nil {
		sample.Working = s.opts.Working()
	}
	s.mu.Lock()
	s.failed = false
	s.history = append(s.history, sample)
	if len(s.history) > historyLength {
		s.history = s.history[len(s.history)-historyLength:]
	}
	s.mu.Unlock()
	if s.opts.Hub != nil {
		s.opts.Hub.Publish(hub.Event{Type: "sys", Data: sample})
	}
}

// Stats returns the newest sample and the history. With nothing sampled yet (nobody was
// watching), one sample is taken now.
func (s *Sampler) Stats(ctx context.Context) Stats {
	s.mu.Lock()
	empty := len(s.history) == 0
	s.mu.Unlock()
	if empty {
		s.Tick(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := Stats{History: append([]Sample{}, s.history...)}
	if len(s.history) > 0 {
		latest := s.history[len(s.history)-1]
		stats.Latest = &latest
	}
	return stats
}
