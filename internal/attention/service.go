// The attention service: it gathers the sources Build reads, remembers when undated items were
// first seen, keeps the human's snoozes and dismissals on the server (so snoozing on the phone
// snoozes on the desktop too, R4 Fable 5.6 and Codex C2), and publishes the whole snapshot as one
// `attention` SSE event whenever it changes. GET /attention answers from the same snapshot.

package attention

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/questions"
)

const (
	// EventType is the hub (and SSE) event carrying a Snapshot.
	EventType = "attention"
	// SnoozeFor is how long one "Snooze" hides an item from the count.
	SnoozeFor = 10 * time.Minute
	// debounce coalesces a burst of source events into one rebuild.
	debounce = 250 * time.Millisecond
	// refreshEvery rebuilds even without events, so snoozes run out and the failed window
	// closes on time.
	refreshEvery = 30 * time.Second
	// kvSnoozed and kvDismissed hold the human's choices: id → unix seconds.
	kvSnoozed   = "attention.snoozed"
	kvDismissed = "attention.dismissed"
	// dismissKeep is how long a dismissal is remembered (past FailedWindow it no longer matters).
	dismissKeep = FailedWindow + 24*time.Hour
)

// sourceEvents are the hub events that can change the queue.
var sourceEvents = map[string]bool{
	"job": true, "question": true, "approval": true, "fleet": true, "orchestrator": true, "lead": true, "project": true,
	"limits": true,
}

// KV is where snoozes and dismissals live (the store's kv table).
type KV interface {
	GetKV(ctx context.Context, key string) (string, error)
	SetKV(ctx context.Context, key, value string) error
}

// Options wire the service. Each source may be nil (that source is then empty).
type Options struct {
	Approvals func(ctx context.Context) ([]approvals.Approval, error)
	Questions func(ctx context.Context) ([]questions.Question, error)
	Agents    func(ctx context.Context) ([]fleet.AgentEntry, error)
	Jobs      func(ctx context.Context) ([]jobs.Job, error)
	Standing  func(ctx context.Context) []StandingAgent
	Projects  func(ctx context.Context) ([]projects.Project, error)
	// Limits and Violations are the jobs service's fleet usage limits and wait-invariant
	// violations (DESIGN.md 6.17, 6.18); nil: none.
	Limits     func(ctx context.Context) []jobs.LimitState
	Violations func() []jobs.Violation
	KV         KV
	Hub        *hub.Hub
	Logger     *slog.Logger
	Now        func() time.Time
}

// Snapshot is the queue at one moment: what GET /attention returns and the `attention` event
// carries.
type Snapshot struct {
	Items    []Item    `json:"items"`
	Counts   Counts    `json:"counts"`
	Revision uint64    `json:"revision"`
	At       time.Time `json:"at"`
	// Partial lists sources that could not be read; their items are missing from this snapshot.
	Partial []string `json:"partial,omitempty"`
}

// Service builds and publishes snapshots.
type Service struct {
	opts Options

	mu        sync.Mutex
	firstSeen map[string]time.Time
	last      Snapshot
	lastBytes []byte
	kick      chan struct{}
}

// New returns the service.
func New(opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{opts: opts, firstSeen: map[string]time.Time{}, kick: make(chan struct{}, 1)}
}

// Snapshot builds the queue now, publishes it if it changed, and returns it.
func (s *Service) Snapshot(ctx context.Context) Snapshot {
	now := s.opts.Now()
	src, partial := s.gather(ctx)
	snoozed := s.readTimes(ctx, kvSnoozed)
	dismissed := s.readTimes(ctx, kvDismissed)

	s.mu.Lock()
	defer s.mu.Unlock()
	src.FirstSeen = s.firstSeen
	src.Dismissed = map[string]bool{}
	for id := range dismissed {
		src.Dismissed[id] = true
	}
	items := Build(src, now)
	s.rememberFirstSeen(items, now)
	for i := range items {
		if until, ok := snoozed[items[i].ID]; ok && until.After(now) {
			at := until
			items[i].SnoozedUntil = &at
		}
	}
	next := Snapshot{Items: items, Counts: Count(items), Revision: s.last.Revision, At: now, Partial: partial}
	if next.Items == nil {
		next.Items = []Item{}
	}
	encoded, err := json.Marshal(struct {
		Items  []Item
		Counts Counts
	}{next.Items, next.Counts})
	if err == nil && !bytes.Equal(encoded, s.lastBytes) {
		next.Revision++
		s.lastBytes = encoded
		s.last = next
		if s.opts.Hub != nil {
			s.opts.Hub.Publish(hub.Event{Type: EventType, Data: next})
		}
	}
	s.last.At, s.last.Partial = now, partial
	return s.last
}

// rememberFirstSeen keeps the time each undated item first appeared, and forgets items that
// have gone (a dialog that comes back later is a new wait).
func (s *Service) rememberFirstSeen(items []Item, now time.Time) {
	present := map[string]bool{}
	for _, item := range items {
		present[item.ID] = true
		if _, ok := s.firstSeen[item.ID]; !ok && (item.Kind == KindDialog || item.Kind == KindAgent) {
			s.firstSeen[item.ID] = now
		}
	}
	for id := range s.firstSeen {
		if !present[id] {
			delete(s.firstSeen, id)
		}
	}
}

// gather reads every source; one that fails is left empty and named in the result.
func (s *Service) gather(ctx context.Context) (Sources, []string) {
	var src Sources
	var partial []string
	note := func(name string, err error) {
		if err != nil {
			partial = append(partial, name)
			s.opts.Logger.Debug("attention: reading a source failed", "source", name, "err", err)
		}
	}
	if s.opts.Approvals != nil {
		list, err := s.opts.Approvals(ctx)
		src.Approvals = list
		note("approvals", err)
	}
	if s.opts.Questions != nil {
		list, err := s.opts.Questions(ctx)
		src.Questions = list
		note("questions", err)
	}
	if s.opts.Agents != nil {
		list, err := s.opts.Agents(ctx)
		src.Agents = list
		note("fleet", err)
	}
	if s.opts.Jobs != nil {
		list, err := s.opts.Jobs(ctx)
		src.Jobs = list
		note("jobs", err)
	}
	if s.opts.Projects != nil {
		list, err := s.opts.Projects(ctx)
		src.Projects = list
		note("projects", err)
	}
	if s.opts.Standing != nil {
		src.Standing = s.opts.Standing(ctx)
	}
	if s.opts.Limits != nil {
		src.Limits = s.opts.Limits(ctx)
	}
	if s.opts.Violations != nil {
		src.Violations = s.opts.Violations()
	}
	return src, partial
}

// ---- Snooze and dismiss -------------------------------------------------------------------

// ErrUnknownItem refuses a snooze or dismissal for an id the queue does not hold.
var ErrUnknownItem = errors.New("no such item in the queue")

// ErrNotDismissable refuses dismissing anything but a failed job: every other item goes away
// only when the thing it stands for is resolved.
var ErrNotDismissable = errors.New("only a failed job can be dismissed; answer or resolve the others")

// Snooze hides an item from the count for SnoozeFor (or until when given), on every device.
func (s *Service) Snooze(ctx context.Context, id string, until time.Time) (Snapshot, error) {
	if !s.holds(ctx, id) {
		return Snapshot{}, ErrUnknownItem
	}
	now := s.opts.Now()
	if until.IsZero() || until.Before(now) || until.Sub(now) > 24*time.Hour {
		until = now.Add(SnoozeFor)
	}
	if err := s.update(ctx, kvSnoozed, id, until, now); err != nil {
		return Snapshot{}, err
	}
	return s.Snapshot(ctx), nil
}

// Unsnooze brings a snoozed item back at once.
func (s *Service) Unsnooze(ctx context.Context, id string) (Snapshot, error) {
	if err := s.update(ctx, kvSnoozed, id, time.Time{}, s.opts.Now()); err != nil {
		return Snapshot{}, err
	}
	return s.Snapshot(ctx), nil
}

// Dismiss takes a failed job out of the queue (it stays on the board and in history).
func (s *Service) Dismiss(ctx context.Context, id string) (Snapshot, error) {
	if !strings.HasPrefix(id, "job:") {
		return Snapshot{}, ErrNotDismissable
	}
	item, ok := s.find(ctx, id)
	if !ok {
		return Snapshot{}, ErrUnknownItem
	}
	if item.Job == nil || item.Job.State != jobs.StateFailed {
		return Snapshot{}, ErrNotDismissable
	}
	now := s.opts.Now()
	if err := s.update(ctx, kvDismissed, id, now.Add(dismissKeep), now); err != nil {
		return Snapshot{}, err
	}
	return s.Snapshot(ctx), nil
}

func (s *Service) holds(ctx context.Context, id string) bool {
	_, ok := s.find(ctx, id)
	return ok
}

func (s *Service) find(ctx context.Context, id string) (Item, bool) {
	for _, item := range s.Snapshot(ctx).Items {
		if item.ID == id {
			return item, true
		}
	}
	return Item{}, false
}

// update sets (or with a zero until, removes) one id in a kv map, dropping expired entries.
func (s *Service) update(ctx context.Context, key, id string, until, now time.Time) error {
	if s.opts.KV == nil {
		return errors.New("attention: no store to keep the choice in")
	}
	times := s.readTimes(ctx, key)
	out := map[string]int64{}
	for other, at := range times {
		if at.After(now) && other != id {
			out[other] = at.Unix()
		}
	}
	if !until.IsZero() {
		out[id] = until.Unix()
	}
	body, err := json.Marshal(out)
	if err != nil {
		return err
	}
	return s.opts.KV.SetKV(ctx, key, string(body))
}

func (s *Service) readTimes(ctx context.Context, key string) map[string]time.Time {
	out := map[string]time.Time{}
	if s.opts.KV == nil {
		return out
	}
	raw, err := s.opts.KV.GetKV(ctx, key)
	if err != nil || raw == "" {
		return out
	}
	var stored map[string]int64
	if json.Unmarshal([]byte(raw), &stored) != nil {
		return out
	}
	for id, at := range stored {
		out[id] = time.Unix(at, 0)
	}
	return out
}

// ---- The watcher ------------------------------------------------------------------------

// Kick asks the watcher for a rebuild soon (after a write the hub does not announce).
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Run rebuilds the snapshot whenever a source event arrives (coalesced), and every
// refreshEvery, until ctx ends. A subscription the hub drops for falling behind is renewed.
func (s *Service) Run(ctx context.Context) {
	s.Snapshot(ctx)
	for ctx.Err() == nil {
		if s.opts.Hub == nil {
			s.loop(ctx, nil)
			return
		}
		sub := s.opts.Hub.Subscribe(0)
		s.loop(ctx, sub.Events())
		sub.Close()
	}
}

// loop runs until ctx ends or the subscription closes.
func (s *Service) loop(ctx context.Context, events <-chan hub.Event) {
	ticker := time.NewTicker(refreshEvery)
	defer ticker.Stop()
	var timer <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if sourceEvents[event.Type] && timer == nil {
				timer = time.After(debounce)
			}
		case <-s.kick:
			if timer == nil {
				timer = time.After(debounce)
			}
		case <-timer:
			timer = nil
			s.Snapshot(ctx)
		case <-ticker.C:
			s.Snapshot(ctx)
		}
	}
}
