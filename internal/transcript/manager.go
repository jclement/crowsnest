// The manager: one followed conversation per agent pane, created when someone asks for it and
// dropped when nobody has asked for a while (the orchestrator's is pinned and never dropped).
//
// Each poll asks Lookup where the agent's transcript is (herdr reports Claude's session id per
// pane; it changes on /clear or a resume), switches files when the session changes, reads what
// was appended, and calls Notify when the conversation changed so the server can publish a
// `message` event. When no transcript can be found the conversation falls back to the pane's
// recent text as one preformatted message (DESIGN.md 7.2).

package transcript

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultPollInterval is how often followed transcripts are checked for appended lines.
	DefaultPollInterval = time.Second
	// DefaultIdleTimeout is how long an unpinned conversation is followed after its last read.
	DefaultIdleTimeout = 10 * time.Minute
	// fallbackLines is how much pane text the fallback shows.
	fallbackLines = 400
)

// Origin says where an agent's transcript should be.
type Origin struct {
	PaneID    string // the conversation's key: the same agent by name or pane id shares one
	Cwd       string // the directory Claude runs in
	SessionID string // Claude's session id; empty when herdr does not know it
}

// LookupFunc resolves an agent target (name or pane id) to its origin. An error that is
// ErrNoAgent (or wraps it) means there is no such agent.
type LookupFunc func(ctx context.Context, target string) (Origin, error)

// FallbackFunc returns a pane's recent text for the pane-text fallback.
type FallbackFunc func(ctx context.Context, paneID string) (string, error)

// NotifyFunc is called after a followed conversation changed. It must not block.
type NotifyFunc func(paneID, cursor string)

// ErrNoAgent is what a LookupFunc returns for a target that is not an agent.
var ErrNoAgent = errors.New("no such agent")

// ManagerOptions configure a Manager. ConfigDir and Lookup are required.
type ManagerOptions struct {
	ConfigDir    string // Claude's config dir (see ConfigDir)
	Lookup       LookupFunc
	Fallback     FallbackFunc  // nil: no fallback, an agent without a transcript is empty
	Notify       NotifyFunc    // nil: no notifications
	PollInterval time.Duration // DefaultPollInterval when zero
	IdleTimeout  time.Duration // DefaultIdleTimeout when zero
	Logger       *slog.Logger  // slog.Default() when nil
	Now          func() time.Time
}

// Manager follows the conversations people are looking at.
type Manager struct {
	opts     ManagerOptions
	mu       sync.Mutex
	watchers map[string]*watcher // pane id → watcher
	// pins maps a pinned target (the orchestrator's name) to the pane it last resolved to, so
	// when the target moves to another pane its pin moves with it instead of piling up (R1-C06).
	pins map[string]string
}

// watcher is one followed conversation.
type watcher struct {
	mu       sync.Mutex // serialises polls of this watcher
	origin   Origin
	conv     *Conversation
	tailer   *Tailer // nil while on the fallback
	lastRead time.Time
	pinned   bool
	paneText string // the fallback text last shown, to avoid needless revisions
}

// NewManager returns a manager; call Run to keep followed conversations moving.
func NewManager(opts ManagerOptions) *Manager {
	if opts.PollInterval <= 0 {
		opts.PollInterval = DefaultPollInterval
	}
	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = DefaultIdleTimeout
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Manager{opts: opts, watchers: map[string]*watcher{}, pins: map[string]string{}}
}

// Messages returns a page of target's conversation after cursor (see Conversation.Page). The
// first call for an agent starts following it; every call refreshes it first, so a page is never
// staler than the file.
func (m *Manager) Messages(ctx context.Context, target, after string, limit int) (Page, error) {
	w, err := m.watcherFor(ctx, target, false)
	if err != nil {
		return Page{}, err
	}
	m.poll(ctx, w, true)
	return w.conv.Page(after, limit), nil
}

// Pin follows target's conversation for as long as the manager runs (the orchestrator's), so
// `message` events flow even when no browser has asked for it yet. Pinning a target again after
// it moved to another pane moves the pin; the old pane's conversation is then unpinned and is
// dropped once idle or once its agent is gone.
func (m *Manager) Pin(ctx context.Context, target string) error {
	w, err := m.watcherFor(ctx, target, true)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	previous, had := m.pins[target]
	m.pins[target] = w.origin.PaneID
	if had && previous != w.origin.PaneID && !m.pinnedLocked(previous) {
		if old, ok := m.watchers[previous]; ok {
			old.pinned = false
		}
	}
	return nil
}

// pinnedLocked reports whether any pin points at paneID; called with m.mu held.
func (m *Manager) pinnedLocked(paneID string) bool {
	for _, pinned := range m.pins {
		if pinned == paneID {
			return true
		}
	}
	return false
}

// Run polls every followed conversation until ctx ends, and drops the idle ones.
func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(m.opts.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.PollAll(ctx)
		}
	}
}

// PollAll polls every followed conversation once and drops idle unpinned ones. Run calls it on
// a timer; tests call it directly.
func (m *Manager) PollAll(ctx context.Context) {
	now := m.opts.Now()
	m.mu.Lock()
	var active []*watcher
	for paneID, w := range m.watchers {
		if !w.pinned && now.Sub(w.lastRead) > m.opts.IdleTimeout {
			delete(m.watchers, paneID)
			continue
		}
		active = append(active, w)
	}
	m.mu.Unlock()
	var gone []*watcher
	for _, w := range active {
		if !m.poll(ctx, w, false) {
			gone = append(gone, w)
		}
	}
	m.dropGone(gone)
}

// dropGone stops following unpinned conversations whose agent no longer exists. A browser that
// asks again simply starts a new one; a pinned one waits for its pin to move (R1-C06).
func (m *Manager) dropGone(gone []*watcher) {
	if len(gone) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, w := range gone {
		paneID := w.paneID()
		if current, ok := m.watchers[paneID]; ok && current == w && !w.pinned {
			delete(m.watchers, paneID)
		}
	}
}

// watcherFor resolves target and returns its watcher, creating it if needed.
func (m *Manager) watcherFor(ctx context.Context, target string, pin bool) (*watcher, error) {
	origin, err := m.opts.Lookup(ctx, target)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.watchers[origin.PaneID]
	if !ok {
		w = &watcher{origin: origin, conv: NewConversation(SourcePane, origin.SessionID)}
		m.watchers[origin.PaneID] = w
	}
	w.lastRead = m.opts.Now()
	w.pinned = w.pinned || pin
	return w, nil
}

// paneID is the watcher's key, read under its own lock (poll rewrites origin).
func (w *watcher) paneID() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.origin.PaneID
}

// poll refreshes one watcher: re-resolve its origin, switch transcript when the session moved,
// read appended lines (or refresh the fallback), and notify on any change. A change is
// announced whoever found it: a browser's read advances the shared tailer, so a change it
// found and kept quiet about would never be announced to the other browsers (R1-C07).
// reading is true for a browser's read, which also refreshes the pane-text fallback. poll
// reports false when the agent is gone (Lookup said there is no such agent).
func (m *Manager) poll(ctx context.Context, w *watcher, reading bool) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	origin, err := m.opts.Lookup(ctx, w.origin.PaneID)
	switch {
	case err == nil:
		w.origin = origin
	case errors.Is(err, ErrNoAgent):
		return false
	}
	changed := m.follow(w)
	if w.tailer != nil {
		polled, err := w.tailer.Poll()
		changed = changed || polled
		if err != nil {
			m.opts.Logger.Debug("transcript poll failed", "pane", w.origin.PaneID, "path", w.tailer.Path(), "err", err)
		}
	} else if m.opts.Fallback != nil && reading {
		// The fallback is refreshed only when someone reads it: pane text has no events of
		// its own, and reading every pane every second for nobody is waste.
		changed = m.refreshFallback(ctx, w) || changed
	}
	if changed && m.opts.Notify != nil {
		m.opts.Notify(w.origin.PaneID, w.conv.Cursor())
	}
	return true
}

// follow points the watcher at the right transcript, resetting the conversation when the file
// changes. It reports whether the conversation was reset.
//
// A new session id whose transcript does not exist yet (Claude writes it on the first prompt, so
// a fresh conversation has none) drops the old transcript and goes back to the pane fallback, so
// "New conversation" does not keep showing the old one. The same session's file vanishing keeps
// what was read. A session change while on the fallback (neither session has a transcript yet)
// resets too, so the new session gets its own id and cursors (R1-C11).
func (m *Manager) follow(w *watcher) bool {
	path := ""
	if w.origin.SessionID != "" {
		path, _ = Locate(m.opts.ConfigDir, w.origin.Cwd, w.origin.SessionID)
	}
	switch {
	case path == "" && w.tailer == nil && w.origin.SessionID != "" && w.origin.SessionID != w.conv.SessionID():
		w.paneText = ""
		w.conv.Reset(SourcePane, w.origin.SessionID)
		return true
	case path == "" && w.tailer == nil:
		return false
	case path == "" && w.tailer.sessionID == w.origin.SessionID:
		return false
	case path == "":
		w.tailer, w.paneText = nil, ""
		w.conv.Reset(SourcePane, w.origin.SessionID)
		return true
	case w.tailer != nil && w.tailer.Path() == path:
		return false
	}
	w.conv.Reset(SourceTranscript, w.origin.SessionID)
	w.tailer = NewTailer(path, w.origin.SessionID, w.conv)
	w.paneText = ""
	return true
}

// refreshFallback replaces the single pane-text message when the text changed.
func (m *Manager) refreshFallback(ctx context.Context, w *watcher) bool {
	text, err := m.opts.Fallback(ctx, w.origin.PaneID)
	if err != nil {
		m.opts.Logger.Debug("pane fallback read failed", "pane", w.origin.PaneID, "err", err)
		return false
	}
	text = lastLines(stripANSI(text), fallbackLines)
	if text == w.paneText {
		return false
	}
	w.paneText = text
	return w.conv.Apply([]Message{{ID: "pane", Kind: KindPane, Text: text, At: m.opts.Now()}})
}

// lastLines keeps the last n lines of text, without trailing blank lines.
func lastLines(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, " \n\t"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
