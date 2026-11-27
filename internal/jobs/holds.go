// Start holds and the pause (DESIGN.md 6.12 and 6.13): what may keep a job from starting, and
// what the supervisor leaves alone while the human has paused the automation.
//
// A job with a directory is held queued, cause `held` with the reason as its detail, when
//   - the automation is paused ("crowsnest's automation is paused"),
//   - the machine is busy (the load hold), or
//   - every slot is taken ("waiting for a slot: 3 of 3 running"), instance-wide or in its
//     project, counting older jobs still waiting as ahead of it so the queue starts in order.
// The check runs inside the launch's claim (under the jobs lock, with the store's count), so two
// launches cannot both take the last slot. The tick tries a held job again on every pass, oldest
// first. The human's "Start anyway" sets start_anyway and launches it past every hold.
//
// While paused the supervisor still observes and records, but does nothing to an agent by
// itself: no new starts, no dispatch to a standing agent, no restarts, no retries, no held brief
// typed, no backstop nudge; and the Stop hook lets every turn end. What the human does (Nudge,
// Retry now, Restart, a message) still works.

package jobs

import (
	"context"
	"errors"
	"strings"

	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/throttle"
)

// PauseSwitch is what the jobs service needs of internal/pause.
type PauseSwitch interface {
	Paused() bool
	// Held journals, once per key while the pause lasts, that something was held.
	Held(ctx context.Context, key, what string)
}

// StartThrottle is what the jobs service needs of internal/throttle.
type StartThrottle interface {
	Settings(ctx context.Context) throttle.Settings
	Load(ctx context.Context) throttle.LoadState
}

// ceilingHoldPrefix starts a start-hold reason for the fleet's daily spend ceiling (6.16).
const ceilingHoldPrefix = "spend ceiling: "

// pausedText is the hold reason while paused.
const pausedText = "crowsnest's automation is paused; it starts when you resume (or Start anyway)"

// errHeld abandons a launch claim whose job must wait.
var errHeld = errors.New("held")

// paused reports whether the automation is paused.
func (s *Service) paused() bool { return s.opts.Pause != nil && s.opts.Pause.Paused() }

// heldWhilePaused reports whether an automatic step must wait for the pause to end, journalling
// it once per key.
func (s *Service) heldWhilePaused(ctx context.Context, key, what string) bool {
	if !s.paused() {
		return false
	}
	s.opts.Pause.Held(ctx, key, what)
	return true
}

// startHoldLocked returns why r may not start now, or "" when it may (s.mu held: the counts are
// read inside the launch claim).
func (s *Service) startHoldLocked(ctx context.Context, r store.JobRecord) string {
	if r.StartAnyway {
		return ""
	}
	if s.paused() {
		return pausedText
	}
	if held, state := s.limitHeld(ctx, r.Engine); held {
		return limitHoldPrefix + limitText(state) + "; it starts after the reset (or Start anyway)"
	}
	if reached, why := s.FleetCeilingReached(ctx); reached {
		return ceilingHoldPrefix + why + "; it starts tomorrow, when the ceiling is raised, or with Start anyway"
	}
	if s.opts.Throttle == nil {
		return ""
	}
	if load := s.opts.Throttle.Load(ctx); load.Holding {
		return "waiting: " + load.Reason
	}
	settings := s.opts.Throttle.Settings(ctx)
	all, err := s.opts.Store.CountSlots(ctx, r.ID, 0)
	if err != nil {
		s.opts.Logger.Warn("counting slots failed; starting anyway", "job", FormatID(r.ID), "err", err)
		return ""
	}
	input := throttle.SlotInput{Running: all.Running, Ahead: all.Ahead, ProjectID: r.ProjectID}
	if r.ProjectID != 0 && settings.ProjectLimits[r.ProjectID] > 0 {
		project, err := s.opts.Store.CountSlots(ctx, r.ID, r.ProjectID)
		if err == nil {
			input.ProjectRunning, input.ProjectAhead = project.Running, project.Ahead
		}
	}
	return throttle.Decide(settings, input)
}

// markHeld records why a queued job waits. The timeline gets a line only when the reason's kind
// changes (a slot count ticking from "2 ahead" to "1 ahead" is not news), the detail always.
func (s *Service) markHeld(ctx context.Context, id, reason string) {
	_, _ = s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if State(r.State) != StateQueued || r.PaneID != "" || r.LifecycleOp != "" {
			return errSkip
		}
		if Cause(r.Cause) == CauseHeld && r.CauseDetail == reason {
			return errSkip
		}
		if Cause(r.Cause) != CauseHeld || holdKind(r.CauseDetail) != holdKind(reason) {
			c.event(EventLaunch, "Held: "+reason, map[string]any{"reason": reason})
		}
		r.Cause, r.CauseDetail = string(CauseHeld), reason
		return nil
	})
}

// holdKind groups hold reasons for the timeline: paused, load or slot.
func holdKind(reason string) string {
	switch {
	case reason == pausedText:
		return "paused"
	case strings.HasPrefix(reason, limitHoldPrefix):
		return "limit"
	case strings.HasPrefix(reason, ceilingHoldPrefix):
		return "ceiling"
	case len(reason) >= 8 && reason[:8] == "waiting:":
		return "load"
	default:
		return "slot"
	}
}

// StartAnyway is the human's override for a held job: it starts now, past the slot limit, the
// load hold and the pause.
func (s *Service) StartAnyway(ctx context.Context, id string) (Job, error) {
	job, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if State(r.State) != StateQueued || r.PaneID != "" {
			return invalid("job %s is not waiting to start", FormatID(r.ID))
		}
		if Cause(r.Cause) == CauseApproval {
			return invalid("job %s waits for your approval in the Inbox, not for a slot", FormatID(r.ID))
		}
		r.StartAnyway = true
		if Cause(r.Cause) == CauseHeld {
			r.Cause, r.CauseDetail = "", ""
		}
		c.event(EventLaunch, "You started it anyway, past the limits", nil)
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	s.goBackground(ctx, job.ID, func(ctx context.Context) { s.launch(ctx, job.ID) })
	return job, nil
}

// QueueLine describes the slots for the board's header: "2 of 3 running · 1 waiting · paused".
type QueueLine struct {
	Running     int     `json:"running"`
	Limit       int     `json:"limit"`
	Waiting     int     `json:"waiting"`
	Paused      bool    `json:"paused"`
	LoadHold    string  `json:"load_hold,omitempty"`
	LoadPerCore float64 `json:"load_per_core"`
}

// Queue reads the slots now.
func (s *Service) Queue(ctx context.Context) (QueueLine, error) {
	line := QueueLine{Paused: s.paused()}
	byProject, err := s.opts.Store.RunningAgentsByProject(ctx)
	if err != nil {
		return line, err
	}
	for _, count := range byProject {
		line.Running += count
	}
	if s.opts.Throttle != nil {
		line.Limit = s.opts.Throttle.Settings(ctx).MaxRunning
		load := s.opts.Throttle.Load(ctx)
		line.LoadPerCore = load.PerCore
		if load.Holding {
			line.LoadHold = load.Reason
		}
	}
	held, err := s.opts.Store.ListJobs(ctx, []string{string(StateQueued)}, listLimit)
	if err != nil {
		return line, err
	}
	for _, record := range held {
		if Cause(record.Cause) == CauseHeld {
			line.Waiting++
		}
	}
	return line, nil
}
