// The Meter: every few seconds, read what each measured subject's transcripts gained since the
// last read and add it to the store (DESIGN.md 7.11).
//
// A subject is a job or the orchestrator, with the Claude session ids it has used. A job that is
// restarted or resumed under a new session id keeps every earlier session's file in its state and
// keeps accumulating; subagent files under each session are found and counted as the job's.
// Each file has a cursor (offset and inode) persisted with the subject, so a crowsnest restart
// resumes where reading stopped and nothing is counted twice; the last few hundred message ids
// are persisted too, because one API call is written as several records (see
// internal/transcript/usage_meter.go) and those copies may straddle two reads.
//
// A settled job is read once more after finalGrace (its last records land after the state
// changes) and then marked final and never read again.

package spend

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

const (
	// DefaultInterval is how often the Meter reads. Transcripts only grow while an agent works,
	// and a stat per file is cheap.
	DefaultInterval = 10 * time.Second
	// finalGrace is how long after a job settles its transcripts are still read.
	finalGrace = 2 * time.Minute
	// maxRecent is how many message ids a subject remembers for deduplication; copies of one call
	// are written back to back, so a few hundred is far more than needed.
	maxRecent = 512
	// maxRecentTools bounds the remembered tool_use ids the same way.
	maxRecentTools = 1024
	// maxSessions bounds the session ids remembered per subject (restarts are capped at 3 an
	// hour, so this is days of restarts).
	maxSessions = 64
	// EventType is the hub event published when spend changed: {at}.
	EventType = "spend"
)

// Session is one Claude session a subject used.
type Session struct {
	ID  string // Claude's session id
	Cwd string // the directory Claude ran in; Locate falls back to searching by id
}

// Source is a subject the Meter should read, as the serve wiring reports it each pass.
type Source struct {
	Subject  string
	Sessions []Session // the current one at least; earlier ones are remembered by the Meter
	// Settled is when a job reached a terminal state; nil while it is live (and always for the
	// orchestrator).
	Settled *time.Time
}

// SourcesFunc lists what to measure now.
type SourcesFunc func(ctx context.Context) ([]Source, error)

// Options configure a Meter. Store and Prices are required.
type Options struct {
	Store           *store.Store
	Prices          *Prices
	ClaudeConfigDir string
	Sources         SourcesFunc
	Hub             *hub.Hub
	Logger          *slog.Logger
	Now             func() time.Time
	// Location decides which local day a call belongs to; time.Local when nil.
	Location *time.Location
	Interval time.Duration
}

// Meter reads transcripts into the store. It embeds a Reader for the summaries.
type Meter struct {
	Reader
	opts Options

	mu     sync.Mutex
	states map[string]*subjectState // loaded lazily from the store
}

// subjectState is persisted as spend_totals.state_json.
type subjectState struct {
	Sessions []string                `json:"sessions"`
	Files    []transcript.FileCursor `json:"files"`
	Recent   []recentCall            `json:"recent"`
	Tools    []string                `json:"tools"`
	Final    bool                    `json:"final,omitempty"`
}

// recentCall is a call already counted: what was counted, and where it went.
type recentCall struct {
	ID     string            `json:"id"`
	Key    string            `json:"key"`
	Day    string            `json:"day"`
	Counts store.SpendCounts `json:"counts"`
}

// NewMeter returns a Meter; call Run to keep it reading.
func NewMeter(opts Options) *Meter {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	return &Meter{
		Reader: Reader{Store: opts.Store, Prices: opts.Prices, Now: opts.Now},
		opts:   opts, states: map[string]*subjectState{},
	}
}

// Location is the time zone days are counted in.
func (m *Meter) Location() *time.Location { return m.opts.Location }

// Run reads every Interval until ctx ends.
func (m *Meter) Run(ctx context.Context) {
	ticker := time.NewTicker(m.opts.Interval)
	defer ticker.Stop()
	for {
		m.Poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Poll reads every source once and reports whether any spend changed. Errors are logged, never
// fatal: a transcript that cannot be read leaves its subject as it was.
func (m *Meter) Poll(ctx context.Context) bool {
	if m.opts.Sources == nil {
		return false
	}
	sources, err := m.opts.Sources(ctx)
	if err != nil {
		m.opts.Logger.Debug("spend: listing subjects failed", "err", err)
		return false
	}
	changed := false
	for _, source := range sources {
		updated, err := m.pollSource(ctx, source)
		if err != nil {
			m.opts.Logger.Debug("spend: reading a subject failed", "subject", source.Subject, "err", err)
		}
		changed = changed || updated
	}
	if changed && m.opts.Hub != nil {
		m.opts.Hub.Publish(hub.Event{Type: EventType, Data: map[string]any{"at": m.opts.Now().UTC()}})
	}
	return changed
}

// pollSource reads one subject's files and stores what they added.
func (m *Meter) pollSource(ctx context.Context, source Source) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.stateLocked(ctx, source.Subject)
	if err != nil || state.Final {
		return false, err
	}
	before, _ := json.Marshal(state)
	state.addSessions(source.Sessions)
	m.discoverFiles(state, source.Sessions)
	batch := newBatch(source.Subject)
	for i := range state.Files {
		cursor := &state.Files[i]
		_, err := transcript.ReadUsage(cursor, func(entry transcript.UsageEntry) { m.count(state, &batch, entry) })
		if err != nil && !errors.Is(err, transcript.ErrUsageFileGone) {
			m.opts.Logger.Debug("spend: reading a transcript failed", "subject", source.Subject, "path", cursor.Path, "err", err)
		}
	}
	now := m.opts.Now()
	if source.Settled != nil && now.Sub(*source.Settled) >= finalGrace {
		state.Final = true
	}
	return m.saveLocked(ctx, state, batch, before)
}

// Record adds calls to a subject as if read from its transcript: the demo's seed and tests.
func (m *Meter) Record(ctx context.Context, subject string, entries []transcript.UsageEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.stateLocked(ctx, subject)
	if err != nil {
		return err
	}
	before, _ := json.Marshal(state)
	batch := newBatch(subject)
	for _, entry := range entries {
		m.count(state, &batch, entry)
	}
	changed, err := m.saveLocked(ctx, state, batch, before)
	if changed && m.opts.Hub != nil {
		m.opts.Hub.Publish(hub.Event{Type: EventType, Data: map[string]any{"at": m.opts.Now().UTC()}})
	}
	return err
}

// saveLocked stores the batch and the state, or just the state when only a cursor moved.
func (m *Meter) saveLocked(ctx context.Context, state *subjectState, batch store.SpendBatch, before []byte) (bool, error) {
	after, err := json.Marshal(state)
	if err != nil {
		return false, err
	}
	now := m.opts.Now()
	if len(batch.Daily) == 0 && len(batch.Turns) == 0 {
		if string(after) == string(before) {
			return false, nil
		}
		return false, m.opts.Store.SaveSpendState(ctx, batch.Subject, string(after), now)
	}
	batch.StateJSON, batch.Now, batch.KeepTurns = string(after), now, maxChartTurns
	return true, m.opts.Store.ApplySpend(ctx, batch)
}

// stateLocked returns a subject's state, loading it from the store the first time.
func (m *Meter) stateLocked(ctx context.Context, subject string) (*subjectState, error) {
	if state, ok := m.states[subject]; ok {
		return state, nil
	}
	state := &subjectState{}
	totals, err := m.opts.Store.GetSpendTotals(ctx, subject)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal([]byte(totals.StateJSON), state); err != nil {
			// A state that does not parse would re-read every file from the top and double the
			// totals; better to stop measuring this subject and say so.
			m.opts.Logger.Warn("spend: unreadable reader state; not measuring this subject again", "subject", subject, "err", err)
			state = &subjectState{Final: true}
		}
	}
	m.states[subject] = state
	return state, nil
}

// addSessions remembers session ids not seen before.
func (s *subjectState) addSessions(sessions []Session) {
	for _, session := range sessions {
		if session.ID == "" || contains(s.Sessions, session.ID) {
			continue
		}
		s.Sessions = append(s.Sessions, session.ID)
		if len(s.Sessions) > maxSessions {
			s.Sessions = s.Sessions[len(s.Sessions)-maxSessions:]
		}
	}
}

// discoverFiles adds a cursor for each remembered session's transcript and its subagent files.
// The current sessions' directories are tried first; earlier sessions are found by id.
func (m *Meter) discoverFiles(state *subjectState, current []Session) {
	cwd := map[string]string{}
	for _, session := range current {
		cwd[session.ID] = session.Cwd
	}
	for _, id := range state.Sessions {
		path, err := transcript.Locate(m.opts.ClaudeConfigDir, cwd[id], id)
		if err != nil {
			continue
		}
		state.addFile(path)
		for _, sub := range transcript.SubagentFiles(path) {
			state.addFile(sub)
		}
	}
}

func (s *subjectState) addFile(path string) {
	for _, file := range s.Files {
		if file.Path == path {
			return
		}
	}
	s.Files = append(s.Files, transcript.FileCursor{Path: path})
}

// newBatch starts an empty batch for subject.
func newBatch(subject string) store.SpendBatch { return store.SpendBatch{Subject: subject} }

// count adds one usage entry to the batch: a new call in full, a larger copy of a call already
// counted by the difference, an equal or smaller copy not at all.
func (m *Meter) count(state *subjectState, batch *store.SpendBatch, entry transcript.UsageEntry) {
	at := entry.At
	if at.IsZero() {
		at = m.opts.Now()
	}
	counts := store.SpendCounts{Input: entry.Input, Output: entry.Output, CacheWrite5m: entry.CacheWrite5m,
		CacheWrite1h: entry.CacheWrite1h, CacheRead: entry.CacheRead}
	newTools := state.newTools(entry.ToolUseIDs)
	var delta store.SpendCounts
	var key, day string
	if previous := state.recent(entry.MessageID); previous != nil {
		delta = growth(previous.Counts, counts)
		previous.Counts = maxCounts(previous.Counts, counts)
		key, day = previous.Key, previous.Day
	} else {
		delta = counts
		delta.Turns = 1
		key = m.opts.Prices.Key(entry.Model, entry.Speed, entry.Geo, entry.Prompt())
		day = at.In(m.opts.Location).Format(time.DateOnly)
		state.remember(recentCall{ID: entry.MessageID, Key: key, Day: day, Counts: counts})
	}
	delta.ToolCalls = int64(newTools)
	if delta.IsZero() {
		return
	}
	batch.Delta = batch.Delta.Add(delta)
	if batch.FirstAt.IsZero() || at.Before(batch.FirstAt) {
		batch.FirstAt = at
	}
	if at.After(batch.LastAt) {
		batch.LastAt = at
	}
	addDaily(batch, day, key, delta)
	turnCounts := delta
	turnCounts.Turns, turnCounts.ToolCalls = 0, 0
	if !turnCounts.IsZero() {
		batch.Turns = append(batch.Turns, store.SpendTurn{MessageID: entry.MessageID, At: at, Model: key, SpendCounts: turnCounts})
	}
}

func addDaily(batch *store.SpendBatch, day, key string, delta store.SpendCounts) {
	for i := range batch.Daily {
		if batch.Daily[i].Day == day && batch.Daily[i].Model == key {
			batch.Daily[i].SpendCounts = batch.Daily[i].Add(delta)
			return
		}
	}
	batch.Daily = append(batch.Daily, store.SpendDaily{Day: day, Model: key, SpendCounts: delta})
}

func (s *subjectState) recent(id string) *recentCall {
	for i := len(s.Recent) - 1; i >= 0; i-- {
		if s.Recent[i].ID == id {
			return &s.Recent[i]
		}
	}
	return nil
}

func (s *subjectState) remember(call recentCall) {
	s.Recent = append(s.Recent, call)
	if len(s.Recent) > maxRecent {
		s.Recent = s.Recent[len(s.Recent)-maxRecent:]
	}
}

// newTools remembers tool_use ids and returns how many were not seen before.
func (s *subjectState) newTools(ids []string) int {
	added := 0
	for _, id := range ids {
		if contains(s.Tools, id) {
			continue
		}
		s.Tools = append(s.Tools, id)
		added++
	}
	if len(s.Tools) > maxRecentTools {
		s.Tools = s.Tools[len(s.Tools)-maxRecentTools:]
	}
	return added
}

// growth is how much each field of next exceeds previous (never negative).
func growth(previous, next store.SpendCounts) store.SpendCounts {
	grow := func(a, b int64) int64 {
		if b > a {
			return b - a
		}
		return 0
	}
	return store.SpendCounts{Input: grow(previous.Input, next.Input), Output: grow(previous.Output, next.Output),
		CacheWrite5m: grow(previous.CacheWrite5m, next.CacheWrite5m), CacheWrite1h: grow(previous.CacheWrite1h, next.CacheWrite1h),
		CacheRead: grow(previous.CacheRead, next.CacheRead)}
}

func maxCounts(a, b store.SpendCounts) store.SpendCounts {
	return a.Add(growth(a, b))
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
