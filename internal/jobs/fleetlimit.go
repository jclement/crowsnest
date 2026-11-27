// The fleet-wide usage limit (DESIGN.md 6.17). On a subscription a plan's usage limit applies to
// every agent on the account at once, so when one agent hits it, all of them will: crowsnest
// records the limit once per engine with its reset time, holds new starts, nudges, retries and
// automatic pokes for that engine until then, shows one banner with a countdown instead of a
// stuck card per agent, and at the reset resumes the waiting jobs one at a time, oldest first.
//
// The reset time is read from the limit's message ("resets 3pm", "resets at 3:00 PM (Europe/…)",
// "try again in 2 hours"); when it cannot be read crowsnest guesses 30 minutes, doubling (to two
// hours) each time the limit is hit again after a guess ran out, and says it guessed.

package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/store"
)

const (
	// kvLimits keeps the fleet's limits across restarts.
	kvLimits = "limits.fleet"
	// limitGuess is the first guess when the reset time cannot be read; limitGuessMax caps the doubling.
	limitGuess    = 30 * time.Minute
	limitGuessMax = 2 * time.Hour
	// resumeEvery spaces the resumed jobs after a reset, so they do not all start at once.
	resumeEvery = 20 * time.Second
	// LimitsEvent is the hub event published when the fleet's limits change.
	LimitsEvent = "limits"
)

// LimitState is one engine's usage limit.
type LimitState struct {
	Engine  string    `json:"engine"`
	Since   time.Time `json:"since"`
	ResetAt time.Time `json:"reset_at"`
	// Guessed: the reset time could not be read from the message.
	Guessed bool   `json:"guessed"`
	Job     string `json:"job"`    // the first job that reported it
	Detail  string `json:"detail"` // the message, as the hook gave it
	// Held counts the jobs waiting on it (filled when listed).
	Held int `json:"held"`
	// Resuming: the reset has passed and the waiting jobs are being resumed one by one.
	Resuming bool `json:"resuming"`
	guess    time.Duration
}

// FleetLimits holds the fleet's usage limits. Build with NewFleetLimits.
type FleetLimits struct {
	mu       sync.Mutex
	store    *store.Store
	hub      *hub.Hub
	now      func() time.Time
	loaded   bool
	state    map[string]*LimitState
	resumeAt map[string]time.Time // engine → the last resume typed
}

// NewFleetLimits returns the limits, kept in st's kv.
func NewFleetLimits(st *store.Store, h *hub.Hub, now func() time.Time) *FleetLimits {
	if now == nil {
		now = time.Now
	}
	return &FleetLimits{store: st, hub: h, now: now, state: map[string]*LimitState{}, resumeAt: map[string]time.Time{}}
}

func (f *FleetLimits) loadLocked(ctx context.Context) {
	if f.loaded || f.store == nil {
		return
	}
	f.loaded = true
	value, err := f.store.GetKV(ctx, kvLimits)
	if err != nil || value == "" {
		return
	}
	var saved []LimitState
	if json.Unmarshal([]byte(value), &saved) == nil {
		for i := range saved {
			state := saved[i]
			f.state[state.Engine] = &state
		}
	}
}

func (f *FleetLimits) saveLocked(ctx context.Context) {
	list := f.listLocked()
	if f.store != nil {
		body, _ := json.Marshal(list)
		_ = f.store.SetKV(ctx, kvLimits, string(body))
	}
	if f.hub != nil {
		f.hub.Publish(hub.Event{Type: LimitsEvent, Data: map[string]any{"limits": list}})
	}
}

func (f *FleetLimits) listLocked() []LimitState {
	out := make([]LimitState, 0, len(f.state))
	for _, state := range f.state {
		copied := *state
		copied.Resuming = !f.now().Before(state.ResetAt)
		out = append(out, copied)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Engine < out[j].Engine })
	return out
}

// Note records that an agent of engine hit a usage limit and returns the limit in force: the
// first report sets it, later ones join it (and extend a guess that has run out).
func (f *FleetLimits) Note(ctx context.Context, engine, jobID, detail string) LimitState {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadLocked(ctx)
	now := f.now()
	state := f.state[engine]
	switch {
	case state == nil:
		reset, guessed := ParseLimitReset(detail, now)
		state = &LimitState{Engine: engine, Since: now, ResetAt: reset, Guessed: guessed, Job: jobID, Detail: oneLine(detail, 300), guess: limitGuess}
		f.state[engine] = state
	case !now.Before(state.ResetAt):
		// Hit again after the reset: a new limit (or the guess was short).
		reset, guessed := ParseLimitReset(detail, now)
		if guessed && state.Guessed {
			state.guess = min(max(state.guess, limitGuess)*2, limitGuessMax)
			reset = now.Add(state.guess)
		}
		state.ResetAt, state.Guessed, state.Detail, state.Job = reset, guessed, oneLine(detail, 300), jobID
	default:
		return *state
	}
	f.saveLocked(ctx)
	return *state
}

// Held reports whether engine is under a usage limit now, and the limit.
func (f *FleetLimits) Held(ctx context.Context, engine string) (bool, LimitState) {
	if f == nil {
		return false, LimitState{}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadLocked(ctx)
	state := f.state[engine]
	if state == nil {
		return false, LimitState{}
	}
	return f.now().Before(state.ResetAt), *state
}

// List returns the limits recorded (held or resuming).
func (f *FleetLimits) List(ctx context.Context) []LimitState {
	if f == nil {
		return []LimitState{}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadLocked(ctx)
	return f.listLocked()
}

// mayResume reports whether one more job of engine may be resumed now (one every resumeEvery
// after the reset), and takes that turn.
func (f *FleetLimits) mayResume(engine string) bool {
	if f == nil {
		return true
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	if last, ok := f.resumeAt[engine]; ok && now.Sub(last) < resumeEvery {
		return false
	}
	f.resumeAt[engine] = now
	return true
}

// resuming reports whether engine's limit has reset but is still recorded: jobs it held are being
// resumed one at a time.
func (f *FleetLimits) resuming(ctx context.Context, engine string) bool {
	if f == nil {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadLocked(ctx)
	state := f.state[engine]
	return state != nil && !f.now().Before(state.ResetAt)
}

// Clear forgets engine's limit (nothing waits on it any more).
func (f *FleetLimits) Clear(ctx context.Context, engine string) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadLocked(ctx)
	if _, ok := f.state[engine]; !ok {
		return
	}
	delete(f.state, engine)
	delete(f.resumeAt, engine)
	f.saveLocked(ctx)
}

var (
	resetClock    = regexp.MustCompile(`(?i)resets?\s+(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*([ap]\.?m\.?)?`)
	resetRelative = regexp.MustCompile(`(?i)(?:try again|resets?|available)\s+in\s+(?:about\s+)?(\d+)\s*(h|hours?|m|min(?:ute)?s?)(?:\s*(\d+)\s*(m|min(?:ute)?s?))?`)
)

// ParseLimitReset reads a reset time from a limit message, relative to now (local time). guessed
// is true when none could be read; then the time is now plus limitGuess.
func ParseLimitReset(message string, now time.Time) (time.Time, bool) {
	if match := resetRelative.FindStringSubmatch(message); match != nil {
		amount, _ := strconv.Atoi(match[1])
		unit := time.Minute
		if strings.HasPrefix(strings.ToLower(match[2]), "h") {
			unit = time.Hour
		}
		at := now.Add(time.Duration(amount) * unit)
		if match[3] != "" {
			extra, _ := strconv.Atoi(match[3])
			at = at.Add(time.Duration(extra) * time.Minute)
		}
		return at, false
	}
	if match := resetClock.FindStringSubmatch(message); match != nil {
		hour, _ := strconv.Atoi(match[1])
		minute, _ := strconv.Atoi(match[2])
		meridiem := strings.ToLower(strings.ReplaceAll(match[3], ".", ""))
		if hour > 23 || minute > 59 || (meridiem != "" && (hour < 1 || hour > 12)) {
			return now.Add(limitGuess), true
		}
		switch meridiem {
		case "pm":
			if hour != 12 {
				hour += 12
			}
		case "am":
			if hour == 12 {
				hour = 0
			}
		}
		local := now.Local()
		at := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, local.Location())
		if !at.After(local) {
			at = at.Add(24 * time.Hour)
		}
		return at, false
	}
	return now.Add(limitGuess), true
}

// ---- The service's side --------------------------------------------------------------------

// limitHeld reports whether engine is held by the fleet's usage limit now.
func (s *Service) limitHeld(ctx context.Context, engine string) (bool, LimitState) {
	if s.opts.Limits == nil || engine == "" {
		return false, LimitState{}
	}
	return s.opts.Limits.Held(ctx, engine)
}

// LimitHeld is limitHeld for other packages (the poker's hold).
func (s *Service) LimitHeld(ctx context.Context, engine string) (bool, string) {
	held, state := s.limitHeld(ctx, engine)
	if !held {
		return false, ""
	}
	return true, limitText(state)
}

// FleetLimitsNow lists the fleet's usage limits with how many jobs wait on each.
func (s *Service) FleetLimitsNow(ctx context.Context) []LimitState {
	if s.opts.Limits == nil {
		return []LimitState{}
	}
	list := s.opts.Limits.List(ctx)
	if len(list) == 0 {
		return list
	}
	counts := map[string]int{}
	_ = s.eachLive(ctx, liveStates, func(r store.JobRecord) {
		if (r.Stuck && Cause(r.Cause) == CauseLimit) || (Cause(r.Cause) == CauseHeld && strings.HasPrefix(r.CauseDetail, limitHoldPrefix)) {
			counts[r.Engine]++
		}
	})
	for i := range list {
		list[i].Held = counts[list[i].Engine]
	}
	return list
}

// limitHoldPrefix starts a start-hold reason for a usage limit.
const limitHoldPrefix = "usage limit: "

// limitText is a limit in words: "claude's usage limit resets at 15:00".
func limitText(state LimitState) string {
	at := state.ResetAt.Local().Format("15:04")
	if state.Guessed {
		return fmt.Sprintf("%s hit a usage limit; crowsnest guesses it resets around %s", state.Engine, at)
	}
	return fmt.Sprintf("%s's usage limit resets at %s", state.Engine, at)
}

// noteLimitLocked records a usage limit a job reported and schedules its retry for the reset
// (s.mu held). It returns the limit.
func (s *Service) noteLimitLocked(ctx context.Context, r *store.JobRecord, detail string) (LimitState, bool) {
	if s.opts.Limits == nil {
		return LimitState{}, false
	}
	state := s.opts.Limits.Note(ctx, r.Engine, FormatID(r.ID), detail)
	r.RetryAt = state.ResetAt
	return state, true
}

// tickLimits clears limits nothing waits on any more once their reset has passed.
func (s *Service) tickLimits(ctx context.Context) {
	if s.opts.Limits == nil {
		return
	}
	for _, state := range s.FleetLimitsNow(ctx) {
		if !s.opts.Now().Before(state.ResetAt) && state.Held == 0 {
			s.opts.Limits.Clear(ctx, state.Engine)
		}
	}
}
