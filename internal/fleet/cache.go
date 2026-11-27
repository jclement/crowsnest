// The fleet cache: crowsnest's current picture of the herdr session, kept fresh by herdr's events
// with a poll as the backstop, and announced on the hub whenever it changes (DESIGN.md section 5).
//
// Events are treated purely as invalidations. Any event schedules a refetch of session.snapshot
// after a short coalescing delay, so a burst of events (a workspace created emits four) costs one
// snapshot. Two subscriptions feed it: one for the lifecycle types, and one carrying
// pane.agent_status_changed for every known pane, rebuilt whenever the pane set changes (herdr
// requires a pane id for that type). A poll every few seconds catches anything the streams miss,
// and is how the cache recovers after events_lost or a dropped connection while it resubscribes.

package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/hub"
)

// Defaults for Options.
const (
	DefaultDebounce    = 150 * time.Millisecond
	DefaultPoll        = 3 * time.Second
	DefaultCallTimeout = 5 * time.Second
	// maxResubscribeDelay caps the backoff between failed subscription attempts.
	maxResubscribeDelay = 5 * time.Second
	initialResubscribe  = 250 * time.Millisecond
)

// EventType is the hub event published when the fleet changes. Its data is the new Fleet.
const EventType = "fleet"

// Options configure a Cache. Client and Hub are required.
type Options struct {
	Client      herdr.Client
	Hub         *hub.Hub
	Session     string
	Logger      *slog.Logger
	Debounce    time.Duration // delay that coalesces a burst of events into one refetch
	Poll        time.Duration // backstop refetch interval
	CallTimeout time.Duration // bound on each herdr call made by a refresh
	Now         func() time.Time
}

// Cache holds the latest Fleet. Create it with New and start it with Run.
type Cache struct {
	opts Options

	refreshMu sync.Mutex // one refresh at a time

	mu      sync.Mutex
	current Fleet
	ready   bool
	panes   []string // sorted pane ids from the last snapshot

	invalidate chan struct{} // an event arrived: refetch soon
	paneSet    chan struct{} // the pane set changed: rebuild the status subscription
}

// New returns a cache that has not fetched anything yet.
func New(opts Options) *Cache {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Debounce <= 0 {
		opts.Debounce = DefaultDebounce
	}
	if opts.Poll <= 0 {
		opts.Poll = DefaultPoll
	}
	if opts.CallTimeout <= 0 {
		opts.CallTimeout = DefaultCallTimeout
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Cache{opts: opts, invalidate: make(chan struct{}, 1), paneSet: make(chan struct{}, 1)}
}

// Invalidate schedules a refetch, as an event would. Callers that just changed the session (sent
// keys, started an agent) use it so the UI catches up without waiting for the poll.
func (c *Cache) Invalidate() {
	select {
	case c.invalidate <- struct{}{}:
	default: // one pending invalidation is as good as many
	}
}

// Fleet returns the current fleet, fetching it first if no refresh has succeeded yet.
func (c *Cache) Fleet(ctx context.Context) (Fleet, error) {
	c.mu.Lock()
	current, ready := c.current, c.ready
	c.mu.Unlock()
	if ready {
		return current, nil
	}
	if err := c.Refresh(ctx); err != nil {
		return Fleet{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current, nil
}

// Run keeps the cache fresh until ctx ends.
func (c *Cache) Run(ctx context.Context) {
	if err := c.Refresh(ctx); err != nil {
		c.opts.Logger.Warn("first fleet refresh failed; will keep polling", "err", err)
	}
	go c.subscribeLoop(ctx, "lifecycle", lifecycleSubscriptions())

	poll := time.NewTicker(c.opts.Poll)
	defer poll.Stop()
	var (
		debounce     <-chan time.Time
		stopStatuses context.CancelFunc = func() {}
	)
	defer func() { stopStatuses() }()
	c.signalPaneSet() // start the status subscription for whatever the first refresh found
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.invalidate:
			if debounce == nil {
				debounce = time.After(c.opts.Debounce)
			}
		case <-debounce:
			debounce = nil
			c.refreshLogged(ctx)
		case <-poll.C:
			c.refreshLogged(ctx)
		case <-c.paneSet:
			stopStatuses()
			stopStatuses = c.startStatusSubscription(ctx)
		}
	}
}

func (c *Cache) refreshLogged(ctx context.Context) {
	if err := c.Refresh(ctx); err != nil && ctx.Err() == nil {
		c.opts.Logger.Debug("fleet refresh failed", "err", err)
	}
}

// Refresh fetches a snapshot and the screens of agent panes, and publishes the result if it
// differs from the current fleet. A failure keeps the last good fleet but records the error on
// it, so the UI can say herdr is unreachable rather than show stale state as live.
func (c *Cache) Refresh(ctx context.Context) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	next, panes, err := c.fetch(ctx)
	if err != nil {
		c.recordError(err)
		return err
	}
	c.store(next, panes)
	return nil
}

func (c *Cache) fetch(ctx context.Context) (Fleet, []string, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.opts.CallTimeout)
	defer cancel()
	snapshot, err := c.opts.Client.Snapshot(callCtx)
	if err != nil {
		return Fleet{}, nil, err
	}
	dialogs := c.readDialogs(ctx, snapshot.Agents)
	panes := make([]string, 0, len(snapshot.Panes))
	for _, pane := range snapshot.Panes {
		panes = append(panes, pane.ID)
	}
	slices.Sort(panes)
	return buildFleet(c.opts.Session, snapshot, dialogs), panes, nil
}

// paneDialog is what one agent's screen showed: a dialog, and whether it is a folder-trust
// question (and about which folder).
type paneDialog struct {
	present bool
	trust   *TrustPrompt
}

// readDialogs reads each agent's visible screen and records which show a dialog. A failed read
// (the pane closed mid-refresh) just means no dialog for that pane this round.
func (c *Cache) readDialogs(ctx context.Context, agents []herdr.Agent) map[string]paneDialog {
	dialogs := make(map[string]paneDialog, len(agents))
	for _, agent := range agents {
		callCtx, cancel := context.WithTimeout(ctx, c.opts.CallTimeout)
		read, err := c.opts.Client.PaneRead(callCtx, agent.PaneID, herdr.ReadOptions{Source: herdr.SourceVisible})
		cancel()
		if err == nil {
			dialog := DetectDialog(read.Text)
			dialogs[agent.PaneID] = paneDialog{present: dialog.Present, trust: dialog.Trust}
		}
	}
	return dialogs
}

// store swaps in a freshly fetched fleet if it changed, and announces it.
func (c *Cache) store(next Fleet, panes []string) {
	c.mu.Lock()
	changed := !c.ready || !sameFleet(c.current, next)
	paneSetChanged := !slices.Equal(c.panes, panes)
	c.panes = panes
	if changed {
		next.Revision = c.current.Revision + 1
		next.UpdatedAt = c.opts.Now().UTC()
		c.current, c.ready = next, true
	}
	c.mu.Unlock()
	if changed {
		c.publish(next)
	}
	if paneSetChanged {
		c.signalPaneSet()
	}
}

func (c *Cache) recordError(err error) {
	c.mu.Lock()
	if !c.ready || c.current.Error == err.Error() {
		c.mu.Unlock()
		return
	}
	if c.current.Error == "" {
		c.opts.Logger.Warn("herdr is not answering; the fleet view is stale", "err", err)
	}
	next := c.current
	next.Error = err.Error()
	next.Revision++
	next.UpdatedAt = c.opts.Now().UTC()
	c.current = next
	c.mu.Unlock()
	c.publish(next)
}

func (c *Cache) publish(fleet Fleet) {
	if c.opts.Hub != nil {
		c.opts.Hub.Publish(hub.Event{Type: EventType, Data: fleet})
	}
}

// sameFleet compares two fleets ignoring the bookkeeping fields.
func sameFleet(a, b Fleet) bool {
	a.Revision, a.UpdatedAt = 0, time.Time{}
	b.Revision, b.UpdatedAt = 0, time.Time{}
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)
	return errLeft == nil && errRight == nil && bytes.Equal(left, right)
}

// ---- Subscriptions ---------------------------------------------------------------

func lifecycleSubscriptions() []herdr.Subscription {
	subscriptions := make([]herdr.Subscription, 0, len(herdr.LifecycleEventTypes))
	for _, eventType := range herdr.LifecycleEventTypes {
		subscriptions = append(subscriptions, herdr.Subscription{Type: eventType})
	}
	return subscriptions
}

func (c *Cache) signalPaneSet() {
	select {
	case c.paneSet <- struct{}{}:
	default:
	}
}

// startStatusSubscription subscribes to agent status changes for the current panes and returns
// the function that stops it.
func (c *Cache) startStatusSubscription(ctx context.Context) context.CancelFunc {
	c.mu.Lock()
	panes := slices.Clone(c.panes)
	c.mu.Unlock()
	if len(panes) == 0 {
		return func() {}
	}
	subscriptions := make([]herdr.Subscription, 0, len(panes))
	for _, pane := range panes {
		subscriptions = append(subscriptions, herdr.Subscription{Type: herdr.EventAgentStatusChanged, PaneID: pane})
	}
	statusCtx, cancel := context.WithCancel(ctx)
	go c.subscribeLoop(statusCtx, "agent status", subscriptions)
	return cancel
}

// subscribeLoop keeps one subscription alive until ctx ends: every event invalidates the cache,
// and when the stream ends (events_lost, a dropped connection, a restarted server) the cache is
// refreshed at once and the subscription retried with backoff.
func (c *Cache) subscribeLoop(ctx context.Context, name string, subscriptions []herdr.Subscription) {
	delay := initialResubscribe
	for ctx.Err() == nil {
		stream, err := c.opts.Client.Subscribe(ctx, subscriptions)
		if err != nil {
			c.opts.Logger.Debug("herdr subscription failed; retrying", "stream", name, "err", err, "in", delay)
			if !sleep(ctx, delay) {
				return
			}
			delay = min(delay*2, maxResubscribeDelay)
			continue
		}
		delay = initialResubscribe
		c.Invalidate() // anything that happened while we were not subscribed
		c.drain(ctx, stream)
		_ = stream.Close()
		if err := stream.Err(); err != nil && ctx.Err() == nil {
			c.opts.Logger.Debug("herdr event stream ended; resyncing", "stream", name, "err", err)
		}
		c.Invalidate()
		if !sleep(ctx, delay) {
			return
		}
	}
}

func (c *Cache) drain(ctx context.Context, stream herdr.EventStream) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-stream.Events():
			if !ok {
				return
			}
			c.Invalidate()
		}
	}
}

func sleep(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
