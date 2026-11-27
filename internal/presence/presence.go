// Package presence knows whether the human is here, and routes attention by it (DESIGN.md
// 6.14). It is the front door's presence_read (here / maybe / away, with a reason) built from the
// web app instead of Earshot: each open browser sends a heartbeat with whether its tab is
// visible and focused and how long ago it saw input; the service folds those into one state.
//
//   - here: a tab is visible and focused and saw input within HereWindow.
//   - maybe: a tab is visible (or was used within MaybeWindow), but nobody has touched it lately.
//   - away: no browser has sent a fresh heartbeat, or every tab is hidden and idle.
//
// The router uses it so one thing never reaches the human twice: here, the tray (and voice,
// when speak mode is on) and no push for what is already on screen; away, Web Push and
// --notify-url; maybe, Web Push only. An item delivered on one channel is not sent on another
// within DedupeWindow. "Do not disturb until HH:MM" holds every non-blocking notification and
// sends one summary when it ends.
//
// Privacy: heartbeats live in memory only, and carry nothing but the coarse fields above. The
// do-not-disturb time is the only thing stored (kv `presence.dnd_until`).
package presence

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/hub"
)

// States.
const (
	Here  = "here"
	Maybe = "maybe"
	Away  = "away"
)

const (
	// HereWindow: input this recent in a focused, visible tab means the human is at the screen.
	HereWindow = 2 * time.Minute
	// MaybeWindow: a tab used this recently may still be in front of them.
	MaybeWindow = 15 * time.Minute
	// StaleAfter: a browser that has not sent a heartbeat for this long has gone (closed, asleep,
	// offline). The app sends one every 30 s and on every visibility or focus change.
	StaleAfter = 75 * time.Second
	// DedupeWindow: an item that reached the human on one channel is not sent on another within it.
	DedupeWindow = 2 * time.Minute
	// maxBrowsers bounds the in-memory table (a misbehaving client cannot grow it without end).
	maxBrowsers = 64
	// kvDND keeps the do-not-disturb time.
	kvDND = "presence.dnd_until"
)

// Beat is one browser's heartbeat, as coarse as the state needs.
type Beat struct {
	Visible bool
	Focused bool
	// IdleFor is how long ago the tab last saw input (keys, pointer, touch); capped by the client.
	IdleFor time.Duration
}

// State is the presence as the API, the System page and the poke header show it.
type State struct {
	Presence string     `json:"presence"` // here | maybe | away
	Reason   string     `json:"reason"`
	Browsers int        `json:"browsers"` // browsers with a fresh heartbeat
	DNDUntil *time.Time `json:"dnd_until,omitempty"`
}

// KV is the store's key-value part.
type KV interface {
	GetKV(ctx context.Context, key string) (string, error)
	SetKV(ctx context.Context, key, value string) error
}

// Options configure a Service.
type Options struct {
	KV     KV       // nil: do not disturb is not kept across restarts
	Hub    *hub.Hub // nil: no `presence` events
	Logger *slog.Logger
	Now    func() time.Time
	// OnDNDEnd is called once when do not disturb ends with notifications held, with how many
	// (serve sends one summary push and notice). Nil: nothing.
	OnDNDEnd func(held int)
}

type beat struct {
	Beat
	at time.Time
}

// Service holds the heartbeats and routes notifications.
type Service struct {
	opts Options

	mu            sync.Mutex
	browsers      map[string]beat
	delivered     map[string]delivery // item → where and when it reached the human
	dndUntil      time.Time
	dndLoaded     bool
	heldDuringDND int
	lastState     string
}

type delivery struct {
	channel string
	at      time.Time
}

// New returns a Service.
func New(opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{opts: opts, browsers: map[string]beat{}, delivered: map[string]delivery{}}
}

// Heartbeat records one browser's beat. client is the browser's own random id; an empty one is
// ignored.
func (s *Service) Heartbeat(client string, b Beat) State {
	if client == "" || len(client) > 64 {
		return s.State(context.Background())
	}
	if b.IdleFor < 0 {
		b.IdleFor = 0
	}
	now := s.opts.Now()
	s.mu.Lock()
	if _, known := s.browsers[client]; !known && len(s.browsers) >= maxBrowsers {
		s.pruneLocked(now)
		if len(s.browsers) >= maxBrowsers {
			s.mu.Unlock()
			return s.State(context.Background())
		}
	}
	s.browsers[client] = beat{Beat: b, at: now}
	s.mu.Unlock()
	state := s.State(context.Background())
	s.publishIfChanged(state)
	return state
}

// Gone forgets a browser at once (its tab is closing: the app's pagehide beacon).
func (s *Service) Gone(client string) {
	s.mu.Lock()
	delete(s.browsers, client)
	s.mu.Unlock()
	s.publishIfChanged(s.State(context.Background()))
}

// State folds the heartbeats into here, maybe or away, with the reason.
func (s *Service) State(ctx context.Context) State {
	now := s.opts.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	state := fold(s.beatsLocked(), now)
	if dnd := s.dndLocked(ctx); dnd.After(now) {
		until := dnd.UTC()
		state.DNDUntil = &until
	}
	return state
}

func (s *Service) beatsLocked() []beat {
	out := make([]beat, 0, len(s.browsers))
	for _, b := range s.browsers {
		out = append(out, b)
	}
	return out
}

func (s *Service) pruneLocked(now time.Time) {
	for client, b := range s.browsers {
		if now.Sub(b.at) > StaleAfter {
			delete(s.browsers, client)
		}
	}
}

// fold is the pure rule from fresh beats to a state.
func fold(beats []beat, now time.Time) State {
	state := State{Presence: Away, Browsers: len(beats)}
	if len(beats) == 0 {
		state.Reason = "no browser has crowsnest open"
		return state
	}
	sort.Slice(beats, func(i, j int) bool { return beats[i].IdleFor < beats[j].IdleFor })
	best := beats[0]
	for _, b := range beats {
		if b.Visible && b.Focused && b.IdleFor <= HereWindow {
			state.Presence = Here
			state.Reason = "a crowsnest tab is in front, input " + ago(b.IdleFor)
			return state
		}
	}
	for _, b := range beats {
		if b.Visible || b.IdleFor <= MaybeWindow {
			state.Presence = Maybe
			if b.Visible {
				state.Reason = "a crowsnest tab is visible, but there has been no input for " + ago(b.IdleFor)
			} else {
				state.Reason = "crowsnest was used " + ago(b.IdleFor) + ", its tab is in the background now"
			}
			return state
		}
	}
	state.Reason = "every crowsnest tab is hidden, last input " + ago(best.IdleFor)
	return state
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + " s ago"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + " min ago"
	default:
		return fmt.Sprintf("%.0f h ago", d.Hours())
	}
}

// Presence is the state's word alone, for the poke header.
func (s *Service) Presence() string {
	if s == nil {
		return ""
	}
	return s.State(context.Background()).Presence
}

// ---- Do not disturb ------------------------------------------------------------------------

// SetDND holds non-blocking notifications until until (zero clears it).
func (s *Service) SetDND(ctx context.Context, until time.Time) (State, error) {
	now := s.opts.Now()
	if !until.IsZero() && !until.After(now) {
		return State{}, fmt.Errorf("the do-not-disturb time %s has already passed", until.Local().Format("15:04"))
	}
	s.mu.Lock()
	s.dndUntil, s.dndLoaded = until, true
	held := 0
	if until.IsZero() {
		held, s.heldDuringDND = s.heldDuringDND, 0
	}
	s.mu.Unlock()
	if s.opts.KV != nil {
		value := ""
		if !until.IsZero() {
			value = strconv.FormatInt(until.Unix(), 10)
		}
		if err := s.opts.KV.SetKV(ctx, kvDND, value); err != nil {
			return State{}, fmt.Errorf("saving do not disturb: %w", err)
		}
	}
	if held > 0 && s.opts.OnDNDEnd != nil {
		s.opts.OnDNDEnd(held)
	}
	state := s.State(ctx)
	s.publish(state)
	return state, nil
}

func (s *Service) dndLocked(ctx context.Context) time.Time {
	if !s.dndLoaded {
		s.dndLoaded = true
		if s.opts.KV != nil {
			if raw, err := s.opts.KV.GetKV(ctx, kvDND); err == nil && raw != "" {
				if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
					s.dndUntil = time.Unix(seconds, 0)
				}
			}
		}
	}
	return s.dndUntil
}

// Run ends do not disturb at its time (sending the summary of what was held) until ctx ends.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		s.mu.Lock()
		dnd := s.dndLocked(ctx)
		ended := !dnd.IsZero() && !s.opts.Now().Before(dnd)
		s.mu.Unlock()
		if ended {
			_, _ = s.SetDND(ctx, time.Time{})
		}
		s.publishIfChanged(s.State(ctx))
	}
}

// ---- Routing -------------------------------------------------------------------------------

// Channels.
const (
	ChannelTray   = "tray"
	ChannelVoice  = "voice"
	ChannelPush   = "push"
	ChannelNotify = "notify-url"
)

// Route decides whether item may go out on channel now, and records it when it may. blocking
// marks something the human must act on before work can continue (a blocking question, a
// dangerous approval): do not disturb never holds it. why explains a refusal for the log.
func (s *Service) Route(ctx context.Context, item, channel string, blocking bool) (ok bool, why string) {
	if s == nil {
		return true, ""
	}
	state := s.State(ctx)
	now := s.opts.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if item != "" {
		if previous, seen := s.delivered[item]; seen && group(previous.channel) != group(channel) && now.Sub(previous.at) < DedupeWindow {
			return false, "it reached you by " + previous.channel + " " + ago(now.Sub(previous.at))
		}
	}
	if channel == ChannelPush || channel == ChannelNotify {
		if state.DNDUntil != nil && !blocking {
			s.heldDuringDND++
			return false, "do not disturb is on until " + state.DNDUntil.Local().Format("15:04")
		}
		switch {
		case state.Presence == Here:
			return false, "you are here: it is on your screen"
		case state.Presence == Maybe && channel == ChannelNotify && !blocking:
			return false, "you may be at the screen: Web Push only"
		}
	}
	if channel == ChannelVoice && state.Presence != Here {
		return false, "you are not here to hear it"
	}
	if item != "" {
		s.delivered[item] = delivery{channel: channel, at: now}
		s.pruneDeliveredLocked(now)
	}
	return true, ""
}

// group puts the on-screen channels (tray, voice) on one side and the remote ones (Web Push,
// --notify-url) on the other: away, an item goes out by both remote channels; what matters is
// never reaching the human on the screen and on the phone for the same thing.
func group(channel string) string {
	if channel == ChannelPush || channel == ChannelNotify {
		return "remote"
	}
	return "screen"
}

func (s *Service) pruneDeliveredLocked(now time.Time) {
	if len(s.delivered) < 512 {
		return
	}
	for item, d := range s.delivered {
		if now.Sub(d.at) > DedupeWindow {
			delete(s.delivered, item)
		}
	}
}

func (s *Service) publishIfChanged(state State) {
	s.mu.Lock()
	changed := state.Presence != s.lastState
	s.lastState = state.Presence
	s.mu.Unlock()
	if changed {
		s.publish(state)
	}
}

func (s *Service) publish(state State) {
	if s.opts.Hub != nil {
		s.opts.Hub.Publish(hub.Event{Type: "presence", Data: state})
	}
}
