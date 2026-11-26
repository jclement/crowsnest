// When is a task due: pure functions of the task, its stamp and the clock, so every cadence is a
// table row in due_test.go with a fake clock and a fixed time zone.
//
// Times of day and calendar days are wall-clock in the given location, never "24 hours later":
// a daily task after 17:00 runs at the first check after 17:00 local time on each calendar day,
// across DST changes in either direction, and never twice on one day.

package schedule

import (
	"time"
)

// Stamp is what crowsnest remembers about a task between checks (kept in kv, so it survives a
// restart). The stamps are crowsnest's, set when the poke carrying the task was delivered or the
// job was created, never by the model: a task cannot repeat because an agent forgot to mark it.
type Stamp struct {
	LastRun     time.Time `json:"last_run,omitzero"`
	LastOutcome string    `json:"last_outcome,omitempty"` // one line: "carried by a poke", "job j_4 created"
	JobID       string    `json:"job_id,omitempty"`       // the job of the last as: job run
	// Picture is the fingerprint of the fleet and job picture at the last run of an
	// every: change task.
	Picture string `json:"picture,omitempty"`
	// RunNow is a "Run now" press not yet carried out: due at once, enabled or not.
	RunNow bool `json:"run_now,omitempty"`
}

// Verdict is whether a task is due at a check.
type Verdict string

// Verdicts.
const (
	NotDue    Verdict = ""
	Due       Verdict = "due"        // due on its own: it makes a poke (or a job)
	RideAlong Verdict = "ride_along" // every: poke: goes with a poke that has something else due
)

// ChangeMinGap is the least time between two runs of an every: change task. Agents settle and
// start all day; a task that asked for a turn on every flicker would cost more than it saves.
const ChangeMinGap = 5 * time.Minute

// DueInput is what Check reads besides the task.
type DueInput struct {
	Now      time.Time
	Location *time.Location // local time for after: and calendar days; nil means time.Local
	Stamp    Stamp
	Enabled  bool
	// Picture is the fleet and job picture's fingerprint now, for every: change ("" unknown).
	Picture string
}

// Check decides whether task is due (pure).
func Check(task Task, in DueInput) Verdict {
	if in.Stamp.RunNow {
		return Due // pressed by hand: now, whatever the cadence (an every: poke task included)
	}
	if !in.Enabled || !afterPassed(task, in) {
		return NotDue
	}
	last := in.Stamp.LastRun
	switch task.Every.Kind {
	case CadencePoke:
		return RideAlong
	case CadenceChange:
		if in.Picture == "" || in.Picture == in.Stamp.Picture {
			return NotDue
		}
		if !last.IsZero() && in.Now.Sub(last) < ChangeMinGap {
			return NotDue
		}
		return Due
	case CadenceInterval:
		if last.IsZero() || in.Now.Sub(last) >= task.Every.Interval {
			return Due
		}
	case CadenceDays:
		if last.IsZero() || daysBetween(last, in.Now, location(in)) >= task.Every.Days {
			return Due
		}
	}
	return NotDue
}

// NextDue is when the task will next be due, for the Schedule view; zero when that depends on
// something other than the clock (every: poke and change) or the task is disabled.
func NextDue(task Task, in DueInput) time.Time {
	if !in.Enabled || task.Every.Kind == CadencePoke || task.Every.Kind == CadenceChange {
		return time.Time{}
	}
	loc := location(in)
	last := in.Stamp.LastRun
	var next time.Time
	switch {
	case last.IsZero() || in.Stamp.RunNow:
		next = in.Now
	case task.Every.Kind == CadenceInterval:
		next = last.Add(task.Every.Interval)
	default:
		day := last.In(loc)
		next = time.Date(day.Year(), day.Month(), day.Day()+task.Every.Days, 0, 0, 0, 0, loc)
	}
	if next.Before(in.Now) {
		next = in.Now
	}
	return notBeforeAfter(task, next, loc)
}

// notBeforeAfter moves t to the task's after: time on t's day when t is earlier than it.
func notBeforeAfter(task Task, t time.Time, loc *time.Location) time.Time {
	if task.After == nil {
		return t
	}
	local := t.In(loc)
	if minutesOf(local) >= task.After.Minutes {
		return t
	}
	return time.Date(local.Year(), local.Month(), local.Day(), task.After.Minutes/60, task.After.Minutes%60, 0, 0, loc)
}

// afterPassed reports whether today's after: time (local wall clock) has passed.
func afterPassed(task Task, in DueInput) bool {
	return task.After == nil || minutesOf(in.Now.In(location(in))) >= task.After.Minutes
}

func minutesOf(t time.Time) int { return t.Hour()*60 + t.Minute() }

// daysBetween counts calendar days from a's local date to b's (DST-proof: dates, not hours).
func daysBetween(a, b time.Time, loc *time.Location) int {
	ay, am, ad := a.In(loc).Date()
	by, bm, bd := b.In(loc).Date()
	from := time.Date(ay, am, ad, 12, 0, 0, 0, time.UTC)
	to := time.Date(by, bm, bd, 12, 0, 0, 0, time.UTC)
	return int(to.Sub(from).Hours() / 24)
}

func location(in DueInput) *time.Location {
	if in.Location == nil {
		return time.Local
	}
	return in.Location
}
